package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	pathpkg "path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"harness/internal/config"
	"harness/internal/credential"
	"harness/internal/session"
)

// Shell executes commands without a workspace jail or OS sandbox. The workspace is
// only its initial working directory; identity, approval, timeout, and deny-list
// controls do not make it workspace-confined.
type Shell struct {
	mu               sync.RWMutex
	cfg              config.Shell
	workspace        string
	fileCoordinator  *FileCoordinator
	credential       shellCredentialReader
	startService     serviceProcessStarter
	identityMu       sync.RWMutex
	identity         ShellIdentityStatus
	identityReporter func(ShellIdentityStatus)
}

type shellCredentialReader interface {
	Read() ([]byte, error)
}

type ShellIdentityStatus struct {
	Fallback                 bool   `json:"fallback"`
	OperatorApprovalRequired bool   `json:"operator_approval_required"`
	OperatorContext          bool   `json:"operator_context"`
	OperatorContextExpiresAt string `json:"operator_context_expires_at"`
	Reason                   string `json:"reason"`
	Since                    string `json:"since"`
}

type operatorOverrideRequired struct{ reason string }

func (e *operatorOverrideRequired) Error() string { return e.reason }

func NewShell(cfg config.Shell) *Shell {
	return &Shell{cfg: cfg, startService: startServiceAccountProcess}
}
func (*Shell) Name() string { return "shell" }
func (*Shell) Description() string {
	return "Run an unconfined inline command from the workspace root. Shell has no network in service context (enforced outside the tool layer); use fetch_url for every network operation. Script artifacts written by an agent cannot be executed."
}
func (s *Shell) Schema() map[string]any {
	cfg := s.config()
	return map[string]any{"type": "object", "properties": map[string]any{"command": map[string]any{"type": "string"}, "timeout_s": map[string]any{"type": "integer", "default": cfg.TimeoutS, "maximum": cfg.MaxTimeoutS}}, "required": []string{"command"}}
}
func (s *Shell) Call(ctx context.Context, item *session.Session, args map[string]any) (string, error) {
	detail := s.CallDetailed(ctx, item, args)
	return detail.Content, detail.Err
}

func (s *Shell) CallDetailed(ctx context.Context, item *session.Session, args map[string]any) CallDetail {
	return s.call(ctx, item, args, false)
}

// CallAsOperator is intentionally absent from the Tool interface and schema.
// Only the dispatcher invokes it after a separate, unconditional approval.
func (s *Shell) CallAsOperator(ctx context.Context, item *session.Session, args map[string]any) (string, error) {
	detail := s.call(ctx, item, args, true)
	return detail.Content, detail.Err
}

func (s *Shell) call(ctx context.Context, item *session.Session, args map[string]any, forceOperator bool) (detail CallDetail) {
	cfg := s.config()
	defer func() { detail.OperatorContext = cfg.OperatorContext && !forceOperator }()
	command, ok := args["command"].(string)
	if !ok || strings.TrimSpace(command) == "" {
		return CallDetail{Err: fmt.Errorf("command is required")}
	}
	if reason := forbiddenShellCommand(command, item, s.fileCoordinatorSnapshot()); reason != "" {
		return CallDetail{Err: fmt.Errorf("command blocked: %s", reason)}
	}
	if cfg.FileRoutingGuardEnabled() {
		refusal, ambiguous := inspectShellFileRouting(command)
		if refusal != nil {
			result, _ := json.Marshal(refusal)
			log.Printf("shell file-routing refusal: session=%s tool=%s command=%q", item.ID, refusal.Replacement.Tool, command)
			return CallDetail{Err: fmt.Errorf("note: command was not executed; %s", result)}
		}
		if ambiguous {
			log.Printf("debug: shell file-routing guard allowed ambiguous compound command: %q", command)
		}
	}
	for _, denied := range cfg.Deny {
		if denied != "" && strings.Contains(strings.ToLower(command), strings.ToLower(denied)) {
			return CallDetail{Err: fmt.Errorf("command blocked by deny list")}
		}
	}
	timeout := number(args["timeout_s"], cfg.TimeoutS)
	if timeout <= 0 {
		timeout = cfg.TimeoutS
	}
	if timeout > cfg.MaxTimeoutS {
		timeout = cfg.MaxTimeoutS
	}
	argv := append(append([]string(nil), cfg.Command[1:]...), command)
	var output lockedBuffer
	var process runningShellProcess
	var usedService bool
	var err error
	if forceOperator {
		process, err = startHarnessProcess(cfg.Command[0], argv, item.Workspace, &output)
	} else {
		process, usedService, err = s.start(cfg, item.Workspace, argv, &output)
	}
	if err != nil {
		var required *operatorOverrideRequired
		if errors.As(err, &required) {
			return CallDetail{
				Content:                "service-account shell was not started: " + required.reason,
				OperatorOverrideReason: required.reason,
			}
		}
		return CallDetail{Err: err}
	}
	type waitResult struct {
		code int
		err  error
	}
	done := make(chan waitResult, 1)
	go func() {
		code, err := process.Wait()
		done <- waitResult{code: code, err: err}
	}()
	timer := time.NewTimer(time.Duration(timeout) * time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		process.KillTree()
		<-done
		return CallDetail{Err: ctx.Err()}
	case <-timer.C:
		process.KillTree()
		<-done
		partial := cutOutput(output.String(), cfg.MaxOutputLinesHead, cfg.MaxOutputLinesTail)
		if partial != "" {
			return CallDetail{Err: fmt.Errorf("timed out after %ds; partial output:\n%s", timeout, partial)}
		}
		return CallDetail{Err: fmt.Errorf("timed out after %ds; partial output:", timeout)}
	case result := <-done:
		if result.err != nil {
			return CallDetail{Err: result.err}
		}
		body := cutOutput(output.String(), cfg.MaxOutputLinesHead, cfg.MaxOutputLinesTail)
		if result.code != 0 {
			content := fmt.Sprintf("exit=%d", result.code)
			if body != "" {
				content += "\n" + body
			}
			if usedService && permissionDeniedOutput(body) {
				return CallDetail{Content: content, OperatorOverrideReason: "service account was denied permission"}
			}
			return CallDetail{Err: fmt.Errorf("command failed\n%s", content)}
		}
		if body == "" {
			return CallDetail{Content: "exit=0"}
		}
		return CallDetail{Content: "exit=0\n" + body}
	}
}

func (s *Shell) start(cfg config.Shell, workspace string, argv []string, output *lockedBuffer) (runningShellProcess, bool, error) {
	service := cfg.ServiceAccount
	if cfg.OperatorContext || !service.Enabled {
		process, err := startHarnessProcess(cfg.Command[0], argv, workspace, output)
		return process, false, err
	}
	reason := ""
	if s.credential == nil {
		reason = "service-account credential is not configured"
	} else {
		password, err := s.credential.Read()
		if err == nil {
			defer clearBytes(password)
			process, spawnErr := s.startService(cfg.Command[0], argv, workspace, minimalShellEnvironment(service, workspace), service, password, output)
			if spawnErr == nil {
				s.setIdentity(s.configuredIdentityStatus())
				return process, true, nil
			}
			reason = serviceSpawnReason(spawnErr)
		} else if errors.Is(err, credential.ErrNotStored) {
			reason = "service-account credential is not stored"
		} else {
			reason = "service-account credential cannot be decrypted by this Agent_b process identity"
		}
	}
	s.setIdentity(ShellIdentityStatus{OperatorApprovalRequired: true, Reason: reason, Since: time.Now().UTC().Format(time.RFC3339)})
	log.Printf("ALARM: shell service-account spawn failed; operator approval required: %s", reason)
	return nil, false, &operatorOverrideRequired{reason: reason}
}

func permissionDeniedOutput(output string) bool {
	value := strings.ToLower(output)
	for _, marker := range []string{
		"access is denied",
		"permission denied",
		"unauthorizedaccessexception",
		"attempted to perform an unauthorized operation",
		"requested operation requires elevation",
		"requires elevation",
		"operation not permitted",
		"administrator privileges are required",
	} {
		if strings.Contains(value, marker) {
			return true
		}
	}
	return strings.Contains(value, "access to") && strings.Contains(value, "is denied")
}

func (s *Shell) TestServiceAccount(ctx context.Context) (string, error) {
	cfg, workspace := s.configWithWorkspace()
	workspace, err := usableShellWorkspace(workspace)
	if err != nil {
		return s.failedServiceTest("service-account working directory is unavailable", err)
	}
	if s.credential == nil {
		return s.failedServiceTest("service-account credential is not configured", errors.New("service-account credential is not configured"))
	}
	password, err := s.credential.Read()
	if errors.Is(err, credential.ErrNotStored) {
		return s.failedServiceTest("service-account credential is not stored", err)
	}
	if err != nil {
		return s.failedServiceTest("service-account credential cannot be decrypted by this Agent_b process identity", err)
	}
	defer clearBytes(password)
	command := shellNoop(cfg.Command[0])
	argv := append(append([]string(nil), cfg.Command[1:]...), command)
	var output lockedBuffer
	process, err := s.startService(cfg.Command[0], argv, workspace, minimalShellEnvironment(cfg.ServiceAccount, workspace), cfg.ServiceAccount, password, &output)
	if err != nil {
		return s.failedServiceTest(serviceSpawnReason(err), err)
	}
	type waitResult struct {
		code int
		err  error
	}
	done := make(chan waitResult, 1)
	go func() {
		code, waitErr := process.Wait()
		done <- waitResult{code: code, err: waitErr}
	}()
	select {
	case <-ctx.Done():
		process.KillTree()
		<-done
		return s.failedServiceTest("service-account test timed out", ctx.Err())
	case result := <-done:
		if result.err != nil {
			return s.failedServiceTest("service-account test process failed", result.err)
		}
		if result.code != 0 {
			return s.failedServiceTest(fmt.Sprintf("service-account test process exited %d", result.code), errors.New("test process returned nonzero exit"))
		}
		s.setIdentity(s.configuredIdentityStatus())
		return "service-account shell spawn succeeded", nil
	}
}

func (s *Shell) failedServiceTest(reason string, err error) (string, error) {
	status := ShellIdentityStatus{OperatorApprovalRequired: true, Reason: reason, Since: time.Now().UTC().Format(time.RFC3339)}
	if s.config().OperatorContext {
		status = s.configuredIdentityStatus()
	}
	s.setIdentity(status)
	log.Printf("ALARM: service-account test failed; operator approval required: %s", reason)
	return reason, err
}

func (s *Shell) SetCredentialStore(reader shellCredentialReader) {
	s.mu.Lock()
	s.credential = reader
	s.mu.Unlock()
}

func (s *Shell) SetFileCoordinator(coordinator *FileCoordinator) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fileCoordinator = coordinator
}

func (s *Shell) fileCoordinatorSnapshot() *FileCoordinator {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.fileCoordinator
}

func (s *Shell) SetIdentityReporter(reporter func(ShellIdentityStatus)) {
	s.identityMu.Lock()
	s.identityReporter = reporter
	s.identityMu.Unlock()
}

func (s *Shell) IdentityStatus() ShellIdentityStatus {
	s.identityMu.RLock()
	defer s.identityMu.RUnlock()
	return s.identity
}

func (s *Shell) setIdentity(status ShellIdentityStatus) {
	s.identityMu.Lock()
	if (status.Fallback || status.OperatorApprovalRequired) &&
		(s.identity.Fallback || s.identity.OperatorApprovalRequired) &&
		s.identity.Reason == status.Reason {
		status.Since = s.identity.Since
	}
	s.identity = status
	reporter := s.identityReporter
	s.identityMu.Unlock()
	if reporter != nil {
		reporter(status)
	}
}

func (s *Shell) Configure(value config.Config) {
	workspace := value.Workspace
	if absolute, err := filepath.Abs(workspace); err == nil {
		workspace = absolute
	}
	s.mu.Lock()
	s.cfg = value.Shell
	s.workspace = workspace
	s.mu.Unlock()
	if value.Shell.OperatorContext {
		s.setIdentity(s.configuredIdentityStatus())
	} else if !value.Shell.ServiceAccount.Enabled || s.IdentityStatus().OperatorContext {
		s.setIdentity(ShellIdentityStatus{})
	}
}

func (s *Shell) configuredIdentityStatus() ShellIdentityStatus {
	if !s.config().OperatorContext {
		return ShellIdentityStatus{}
	}
	return ShellIdentityStatus{
		OperatorContext:          true,
		OperatorContextExpiresAt: s.config().OperatorContextExpiresAt,
		Reason:                   "operator context is enabled; tools are running as the Windows account that launched Agent_b",
		Since:                    time.Now().UTC().Format(time.RFC3339),
	}
}

func usableShellWorkspace(workspace string) (string, error) {
	absolute, err := filepath.Abs(workspace)
	if err != nil {
		return "", fmt.Errorf("resolve shell workspace: %w", err)
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return "", fmt.Errorf("open shell workspace %q: %w", absolute, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("shell workspace %q is not a directory", absolute)
	}
	return absolute, nil
}

func (s *Shell) config() config.Shell { s.mu.RLock(); defer s.mu.RUnlock(); return s.cfg }
func (s *Shell) configWithWorkspace() (config.Shell, string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg, s.workspace
}

func clearBytes(value []byte) {
	for i := range value {
		value[i] = 0
	}
}

func shellNoop(executable string) string {
	name := shellCommandName(executable)
	if name == "cmd" {
		return "exit 0"
	}
	if name == "sh" || name == "bash" || name == "zsh" {
		return ":"
	}
	return "$null"
}

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}
func (b *lockedBuffer) String() string { b.mu.Lock(); defer b.mu.Unlock(); return b.b.String() }
func cutOutput(value string, head, tail int) string {
	value = strings.TrimRight(normalizeLF(value), "\n")
	if value == "" {
		return ""
	}
	lines := strings.Split(value, "\n")
	if len(lines) <= head+tail {
		return value
	}
	omitted := len(lines) - head - tail
	kept := append([]string(nil), lines[:head]...)
	kept = append(kept, fmt.Sprintf("[… %d lines omitted …]", omitted))
	kept = append(kept, lines[len(lines)-tail:]...)
	return strings.Join(kept, "\n")
}

type shellRoutingReplacement struct {
	Tool      string            `json:"tool"`
	Arguments map[string]string `json:"arguments"`
}

type shellRoutingRefusal struct {
	Refused     bool                    `json:"refused"`
	Reason      string                  `json:"reason"`
	Replacement shellRoutingReplacement `json:"replacement"`
	Command     string                  `json:"command"`
}

type shellSegmentKind int

const (
	shellSegmentOther shellSegmentKind = iota
	shellSegmentDiscovery
	shellSegmentRead
	shellSegmentDisplay
)

var shellDiscoveryCommands = map[string]bool{
	"ls": true, "dir": true, "get-childitem": true, "gci": true,
	"find": true, "tree": true, "where": true, "where-object": true, "fd": true,
}

var shellReadCommands = map[string]bool{
	"cat": true, "type": true, "get-content": true, "gc": true,
	"head": true, "tail": true, "more": true, "less": true,
}

var shellDisplayCommands = map[string]bool{
	"select-object": true, "select": true, "sort-object": true, "sort": true,
	"format-table": true, "ft": true, "format-list": true, "fl": true, "out-string": true,
}

func inspectShellFileRouting(command string) (*shellRoutingRefusal, bool) {
	segments := strings.Split(command, "|")
	type inspectedSegment struct {
		kind  shellSegmentKind
		words []string
	}
	inspected := make([]inspectedSegment, 0, len(segments))
	hasFileShape, hasOther := false, false
	for _, segment := range segments {
		words := shellWords(segment)
		kind := classifyShellSegment(words)
		inspected = append(inspected, inspectedSegment{kind: kind, words: words})
		if kind == shellSegmentDiscovery || kind == shellSegmentRead {
			hasFileShape = true
		}
		if kind == shellSegmentOther {
			hasOther = true
		}
	}
	if !hasFileShape {
		return nil, false
	}
	if len(segments) > 1 && hasOther {
		return nil, true
	}
	for _, segment := range inspected {
		switch segment.kind {
		case shellSegmentDiscovery:
			return routingRefusal(command, "file discovery", "find_files", discoveryArguments(segment.words)), false
		case shellSegmentRead:
			return routingRefusal(command, "file read", "read_file", readArguments(segment.words)), false
		}
	}
	return nil, false
}

func routingRefusal(command, reason, tool string, arguments map[string]string) *shellRoutingRefusal {
	return &shellRoutingRefusal{
		Refused: true,
		Reason:  "direct " + reason + " is routed to " + tool,
		Replacement: shellRoutingReplacement{
			Tool: tool, Arguments: arguments,
		},
		Command: command,
	}
}

func classifyShellSegment(words []string) shellSegmentKind {
	if len(words) == 0 {
		return shellSegmentOther
	}
	name := shellCommandName(words[0])
	if shellDiscoveryCommands[name] {
		return shellSegmentDiscovery
	}
	if shellReadCommands[name] {
		return shellSegmentRead
	}
	if shellDisplayCommands[name] {
		return shellSegmentDisplay
	}
	return shellSegmentOther
}

func shellCommandName(value string) string {
	return strings.ToLower(strings.TrimSuffix(pathpkg.Base(strings.ReplaceAll(value, `\`, "/")), ".exe"))
}

func forbiddenShellCommand(command string, item *session.Session, coordinator *FileCoordinator) string {
	if requestsExecutionPolicyBypass(command) {
		return "PowerShell execution-policy bypass is forbidden"
	}
	if shellWritesScriptArtifact(command) {
		return "writing executable script artifacts through shell is forbidden; use write_file or edit_file"
	}
	for _, candidate := range shellScriptExecutions(command) {
		if item == nil {
			return "script-file execution without session provenance is forbidden; pass the command body inline"
		}
		resolved, ok := literalShellPath(item.Workspace, candidate)
		if !ok {
			return "script-file execution with a non-literal path is forbidden; pass the command body inline"
		}
		if coordinator == nil || coordinator.wasAgentWritten(item, resolved) {
			return "an agent-written script file cannot be executed; pass the command body inline"
		}
	}
	return ""
}

func requestsExecutionPolicyBypass(command string) bool {
	normalized := strings.NewReplacer(
		"\"", " ", "'", " ", "`", " ", ":", " ", "=", " ",
		"(", " ", ")", " ", "{", " ", "}", " ", "[", " ", "]", " ",
		";", " ", "|", " ", "&", " ", ",", " ",
	).Replace(strings.ToLower(command))
	tokens := strings.Fields(normalized)
	options := map[string]bool{
		"-executionpolicy": true, "/executionpolicy": true,
		"-ep": true, "/ep": true, "-exec": true, "/exec": true,
	}
	for index, token := range tokens {
		if options[token] && index+1 < len(tokens) && tokens[index+1] == "bypass" {
			return true
		}
	}
	return false
}

func shellWritesScriptArtifact(command string) bool {
	lower := strings.ToLower(command)
	if !containsScriptSuffix(lower) {
		return false
	}
	for _, marker := range []string{
		"set-content", "add-content", "out-file", "writealltext", "writeallbytes",
		"new-item", "copy-item", "move-item", "invoke-webrequest", "curl ", "curl.exe",
		"wget ", "wget.exe", " -outfile", "tee ",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return redirectsToScriptArtifact(command)
}

func redirectsToScriptArtifact(command string) bool {
	words := shellPolicyTokens(command)
	for index, word := range words {
		redirect := strings.LastIndex(word, ">")
		if redirect < 0 {
			continue
		}
		target := strings.TrimLeft(word[redirect+1:], ">")
		if target == "" && index+1 < len(words) {
			target = words[index+1]
		}
		if hasScriptSuffix(target, ".ps1", ".psm1", ".cmd", ".bat", ".vbs", ".wsf", ".sh") {
			return true
		}
	}
	return false
}

func containsScriptSuffix(value string) bool {
	for _, suffix := range []string{".ps1", ".psm1", ".bat", ".cmd", ".vbs", ".wsf", ".sh"} {
		if strings.Contains(value, suffix) {
			return true
		}
	}
	return false
}

func shellScriptExecutions(command string) []string {
	return shellScriptExecutionsDepth(command, 0)
}

func shellScriptExecutionsDepth(command string, depth int) []string {
	if depth > 2 {
		return nil
	}
	var candidates []string
	lower := strings.ToLower(command)
	if strings.Contains(lower, "invoke-expression") || strings.Contains(lower, "iex ") ||
		strings.Contains(lower, "[scriptblock]::create") {
		for _, word := range shellPolicyTokens(command) {
			word = cleanShellScriptToken(word)
			if hasScriptSuffix(word, ".ps1", ".psm1", ".cmd", ".bat", ".vbs", ".wsf", ".sh") {
				candidates = append(candidates, word)
			}
		}
	}
	for _, segment := range splitShellCommands(command) {
		words := shellWords(segment)
		if len(words) == 0 {
			continue
		}
		for len(words) > 0 && (words[0] == "&" || words[0] == "." || strings.EqualFold(words[0], "call")) {
			words = words[1:]
		}
		if len(words) == 0 {
			continue
		}
		name := shellCommandName(words[0])
		if scriptSuffixForInterpreter(name, words[0]) {
			candidates = append(candidates, cleanShellScriptToken(words[0]))
			continue
		}
		switch name {
		case "powershell", "pwsh":
			for index := 1; index < len(words); index++ {
				word := cleanShellScriptToken(words[index])
				if strings.EqualFold(word, "-command") || strings.EqualFold(word, "/command") || strings.EqualFold(word, "-c") {
					if index+1 < len(words) {
						candidates = append(candidates, shellScriptExecutionsDepth(strings.Join(words[index+1:], " "), depth+1)...)
					}
					break
				}
				if strings.EqualFold(word, "-file") || strings.EqualFold(word, "/file") || strings.EqualFold(word, "-f") {
					if index+1 < len(words) {
						candidates = append(candidates, cleanShellScriptToken(words[index+1]))
					}
					break
				}
				if hasScriptSuffix(word, ".ps1", ".psm1") {
					candidates = append(candidates, word)
					break
				}
			}
		case "cmd":
			for index, word := range words[1:] {
				word = cleanShellScriptToken(word)
				if (strings.EqualFold(word, "/c") || strings.EqualFold(word, "/k")) && index+2 < len(words) {
					candidates = append(candidates, shellScriptExecutionsDepth(strings.Join(words[index+2:], " "), depth+1)...)
					break
				}
				if hasScriptSuffix(word, ".cmd", ".bat") {
					candidates = append(candidates, word)
					break
				}
			}
		case "wscript", "cscript":
			for _, word := range words[1:] {
				word = cleanShellScriptToken(word)
				if hasScriptSuffix(word, ".vbs", ".wsf") {
					candidates = append(candidates, word)
					break
				}
			}
		case "sh", "bash", "zsh":
			for _, word := range words[1:] {
				word = cleanShellScriptToken(word)
				if hasScriptSuffix(word, ".sh") {
					candidates = append(candidates, word)
					break
				}
			}
		case "start-process":
			for _, word := range words[1:] {
				word = cleanShellScriptToken(word)
				if hasScriptSuffix(word, ".ps1", ".psm1", ".cmd", ".bat", ".vbs", ".wsf", ".sh") {
					candidates = append(candidates, word)
					break
				}
			}
		}
	}
	return candidates
}

func splitShellCommands(command string) []string {
	var segments []string
	var current strings.Builder
	var quote rune
	for _, char := range command {
		switch {
		case quote != 0 && char == quote:
			quote = 0
			current.WriteRune(char)
		case quote != 0:
			current.WriteRune(char)
		case char == '\'' || char == '"':
			quote = char
			current.WriteRune(char)
		case char == ';' || char == '|' || char == '&' || char == '\n' || char == '\r':
			if strings.TrimSpace(current.String()) != "" {
				segments = append(segments, current.String())
			}
			current.Reset()
		default:
			current.WriteRune(char)
		}
	}
	if strings.TrimSpace(current.String()) != "" {
		segments = append(segments, current.String())
	}
	return segments
}

func shellPolicyTokens(command string) []string {
	var tokens []string
	for _, segment := range splitShellCommands(command) {
		tokens = append(tokens, shellWords(segment)...)
	}
	return tokens
}

func cleanShellScriptToken(value string) string {
	return strings.Trim(value, "'\"`(){}[],;")
}

func hasScriptSuffix(value string, suffixes ...string) bool {
	lower := strings.ToLower(cleanShellScriptToken(value))
	for _, suffix := range suffixes {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}
	return false
}

func scriptSuffixForInterpreter(name, value string) bool {
	return hasScriptSuffix(value, ".ps1", ".psm1", ".cmd", ".bat", ".vbs", ".wsf", ".sh") && name != ""
}

func literalShellPath(workspace, value string) (string, bool) {
	value = cleanShellScriptToken(value)
	if value == "" || strings.ContainsAny(value, "$%*?`") {
		return "", false
	}
	value = filepath.FromSlash(value)
	if !filepath.IsAbs(value) {
		value = filepath.Join(workspace, value)
	}
	absolute, err := filepath.Abs(value)
	if err != nil {
		return "", false
	}
	return filepath.Clean(absolute), true
}

// shellWords only separates a pipeline segment into quote-aware words. It does
// not interpret redirects, substitutions, separators, escapes, or shell grammar.
func shellWords(segment string) []string {
	var words []string
	var word strings.Builder
	var quote rune
	flush := func() {
		if word.Len() > 0 {
			words = append(words, word.String())
			word.Reset()
		}
	}
	for _, char := range strings.TrimSpace(segment) {
		switch {
		case quote != 0 && char == quote:
			quote = 0
		case quote == 0 && (char == '\'' || char == '"'):
			quote = char
		case quote == 0 && (char == ' ' || char == '\t' || char == '\r' || char == '\n'):
			flush()
		default:
			word.WriteRune(char)
		}
	}
	flush()
	return words
}

func discoveryArguments(words []string) map[string]string {
	pattern, root := "*", "."
	if len(words) < 2 {
		return map[string]string{"pattern": pattern, "path": root}
	}
	name := shellCommandName(words[0])
	if name == "find" {
		if !strings.HasPrefix(words[1], "-") {
			root = words[1]
		}
		if value := optionValue(words, "-name", "-iname"); value != "" {
			pattern = value
		}
		return map[string]string{"pattern": pattern, "path": root}
	}
	if name == "get-childitem" || name == "gci" {
		if value := optionValue(words, "-path", "-literalpath"); value != "" {
			root = value
		} else if value := firstPositional(words[1:]); value != "" {
			root = value
		}
		if value := optionValue(words, "-filter", "-include"); value != "" {
			pattern = value
		}
		return map[string]string{"pattern": pattern, "path": root}
	}
	if name == "fd" {
		values := positionalValues(words[1:])
		if len(values) > 0 {
			pattern = values[0]
		}
		if len(values) > 1 {
			root = values[1]
		}
		return map[string]string{"pattern": pattern, "path": root}
	}
	if value := firstPositional(words[1:]); value != "" {
		if strings.ContainsAny(value, "*?[") {
			pattern, root = splitPatternPath(value)
		} else if name == "where" || name == "where-object" {
			pattern = value
		} else {
			root = value
		}
	}
	return map[string]string{"pattern": pattern, "path": root}
}

func readArguments(words []string) map[string]string {
	path := "<path>"
	if value := optionValue(words, "-path", "-literalpath"); value != "" {
		path = value
	} else if len(words) > 1 {
		for index := 1; index < len(words); index++ {
			word := words[index]
			if strings.HasPrefix(word, "-") || index > 1 && (words[index-1] == "-n" || words[index-1] == "-c") {
				continue
			}
			path = word
			break
		}
	}
	return map[string]string{"path": path}
}

func optionValue(words []string, names ...string) string {
	for index := 0; index+1 < len(words); index++ {
		for _, name := range names {
			if strings.EqualFold(words[index], name) {
				return words[index+1]
			}
		}
	}
	return ""
}

func firstPositional(words []string) string {
	values := positionalValues(words)
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func positionalValues(words []string) []string {
	values := []string{}
	for _, word := range words {
		if strings.HasPrefix(word, "-") || word == "/s" || word == "/b" || word == "/a" {
			continue
		}
		values = append(values, word)
	}
	return values
}

func splitPatternPath(value string) (string, string) {
	value = strings.ReplaceAll(value, `\`, "/")
	dir, pattern := pathpkg.Split(value)
	if dir == "" {
		return pattern, "."
	}
	return pattern, strings.TrimSuffix(dir, "/")
}
