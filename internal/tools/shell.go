package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	pathpkg "path"
	"strings"
	"sync"
	"time"

	"harness/internal/config"
	"harness/internal/credential"
	"harness/internal/session"
)

// Shell executes commands without an OS sandbox. Its jail, timeout, approval gate,
// and deny list reduce accidents but are not a security boundary.
type Shell struct {
	mu               sync.RWMutex
	cfg              config.Shell
	workspace        string
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
	Fallback bool   `json:"fallback"`
	Reason   string `json:"reason"`
	Since    string `json:"since"`
}

func NewShell(cfg config.Shell) *Shell {
	return &Shell{cfg: cfg, startService: startServiceAccountProcess}
}
func (*Shell) Name() string { return "shell" }
func (*Shell) Description() string {
	return "Run a non-interactive shell command from the workspace root with a timeout and bounded output. Direct file discovery and reads are refused; use glob or read_file."
}
func (s *Shell) Schema() map[string]any {
	cfg := s.config()
	return map[string]any{"type": "object", "properties": map[string]any{"command": map[string]any{"type": "string"}, "timeout_s": map[string]any{"type": "integer", "default": cfg.TimeoutS, "maximum": cfg.MaxTimeoutS}}, "required": []string{"command"}}
}
func (s *Shell) Call(ctx context.Context, item *session.Session, args map[string]any) (string, error) {
	result, err, _ := s.CallDetailed(ctx, item, args)
	return result, err
}

func (s *Shell) CallDetailed(ctx context.Context, item *session.Session, args map[string]any) (string, error, bool) {
	return s.call(ctx, item, args, false)
}

// CallAsOperator is intentionally absent from the Tool interface and schema.
// Only the dispatcher invokes it after a separate, unconditional approval.
func (s *Shell) CallAsOperator(ctx context.Context, item *session.Session, args map[string]any) (string, error) {
	result, err, _ := s.call(ctx, item, args, true)
	return result, err
}

func (s *Shell) call(ctx context.Context, item *session.Session, args map[string]any, forceOperator bool) (string, error, bool) {
	cfg := s.config()
	command, ok := args["command"].(string)
	if !ok || strings.TrimSpace(command) == "" {
		return "", fmt.Errorf("command is required"), false
	}
	if cfg.FileRoutingGuardEnabled() {
		refusal, ambiguous := inspectShellFileRouting(command)
		if refusal != nil {
			result, _ := json.Marshal(refusal)
			log.Printf("shell file-routing refusal: session=%s tool=%s command=%q", item.ID, refusal.Replacement.Tool, command)
			return string(result), nil, false
		}
		if ambiguous {
			log.Printf("debug: shell file-routing guard allowed ambiguous compound command: %q", command)
		}
	}
	for _, denied := range cfg.Deny {
		if denied != "" && strings.Contains(strings.ToLower(command), strings.ToLower(denied)) {
			return "", fmt.Errorf("command blocked by deny list"), false
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
		return "", err, false
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
		return "", ctx.Err(), false
	case <-timer.C:
		process.KillTree()
		<-done
		partial := cutOutput(output.String(), cfg.MaxOutputLinesHead, cfg.MaxOutputLinesTail)
		if partial != "" {
			return "", fmt.Errorf("timed out after %ds; partial output:\n%s", timeout, partial), false
		}
		return "", fmt.Errorf("timed out after %ds; partial output:", timeout), false
	case result := <-done:
		if result.err != nil {
			return "", result.err, false
		}
		body := cutOutput(output.String(), cfg.MaxOutputLinesHead, cfg.MaxOutputLinesTail)
		if body == "" {
			return fmt.Sprintf("exit=%d", result.code), nil, false
		}
		return fmt.Sprintf("exit=%d\n%s", result.code, body), nil, usedService && permissionDeniedOutput(body)
	}
}

func (s *Shell) start(cfg config.Shell, workspace string, argv []string, output *lockedBuffer) (runningShellProcess, bool, error) {
	service := cfg.ServiceAccount
	if !service.Enabled {
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
				s.setIdentity(ShellIdentityStatus{})
				return process, true, nil
			}
			reason = serviceSpawnReason(spawnErr)
		} else if errors.Is(err, credential.ErrNotStored) {
			reason = "service-account credential is not stored"
		} else {
			reason = "service-account credential cannot be decrypted by this harness identity"
		}
	}
	s.setIdentity(ShellIdentityStatus{Fallback: true, Reason: reason, Since: time.Now().UTC().Format(time.RFC3339)})
	log.Printf("ALARM: shell service-account spawn failed; falling back to harness identity: %s", reason)
	process, err := startHarnessProcess(cfg.Command[0], argv, workspace, output)
	return process, false, err
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
	if s.credential == nil {
		return "service-account credential is not configured", errors.New("service-account credential is not configured")
	}
	password, err := s.credential.Read()
	if errors.Is(err, credential.ErrNotStored) {
		return "service-account credential is not stored", err
	}
	if err != nil {
		return "service-account credential cannot be decrypted by this harness identity", err
	}
	defer clearBytes(password)
	command := shellNoop(cfg.Command[0])
	argv := append(append([]string(nil), cfg.Command[1:]...), command)
	var output lockedBuffer
	process, err := s.startService(cfg.Command[0], argv, workspace, minimalShellEnvironment(cfg.ServiceAccount, workspace), cfg.ServiceAccount, password, &output)
	if err != nil {
		return serviceSpawnReason(err), err
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
		return "service-account test timed out", ctx.Err()
	case result := <-done:
		if result.err != nil {
			return "service-account test process failed", result.err
		}
		if result.code != 0 {
			return fmt.Sprintf("service-account test process exited %d", result.code), errors.New("test process returned nonzero exit")
		}
		s.setIdentity(ShellIdentityStatus{})
		return "service-account shell spawn succeeded", nil
	}
}

func (s *Shell) SetCredentialStore(reader shellCredentialReader) {
	s.mu.Lock()
	s.credential = reader
	s.mu.Unlock()
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
	if status.Fallback && s.identity.Fallback && s.identity.Reason == status.Reason {
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
	s.mu.Lock()
	s.cfg = value.Shell
	s.workspace = value.Workspace
	s.mu.Unlock()
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
			return routingRefusal(command, "file discovery", "glob", discoveryArguments(segment.words)), false
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
