package tools

import (
	pathpkg "path"
	"path/filepath"
	"strings"

	"harness/internal/session"
)

type shellRoutingReplacement struct {
	Tool      string            `json:"tool"`
	Arguments map[string]string `json:"arguments"`
}

type shellRoutingRefusal struct {
	Refused     bool                     `json:"refused"`
	Reason      string                   `json:"reason"`
	Replacement *shellRoutingReplacement `json:"replacement,omitempty"`
	Guidance    string                   `json:"guidance,omitempty"`
	Command     string                   `json:"command"`
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
		Replacement: &shellRoutingReplacement{
			Tool: tool, Arguments: arguments,
		},
		Command: command,
	}
}

func routingReplacementOutsideWorkspace(workspace string, refusal *shellRoutingRefusal) bool {
	if refusal == nil || refusal.Replacement == nil {
		return false
	}
	requested := refusal.Replacement.Arguments["path"]
	if requested == "" || !filepath.IsAbs(filepath.FromSlash(requested)) {
		return false
	}
	root, err := filepath.Abs(workspace)
	if err != nil {
		return true
	}
	relative, err := filepath.Rel(root, filepath.Clean(filepath.FromSlash(requested)))
	return err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative)
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

func shellDialect(executable string) string {
	switch name := shellCommandName(executable); name {
	case "powershell", "pwsh":
		return "PowerShell"
	case "cmd":
		return "cmd.exe"
	case "":
		return "the configured shell"
	default:
		return name
	}
}

func forbiddenShellCommand(command string, item *session.Session, coordinator *FileCoordinator) string {
	if reason := forbiddenLOLBinCommand(command); reason != "" {
		return reason
	}
	if reason := forbiddenSigningCommand(command); reason != "" {
		return reason
	}
	if requestsExecutionPolicyBypass(command) {
		return "PowerShell execution-policy bypass is forbidden"
	}
	if shellWritesScriptArtifact(command) {
		return "Windows script-host rule: writing host-executable script artifacts through shell is forbidden; use write_file or edit_file"
	}
	for _, candidate := range shellScriptExecutions(command) {
		if item == nil {
			return "Windows script-host rule: script execution without session provenance is forbidden; use run_script"
		}
		resolved, ok := literalShellPath(item.Workspace, candidate)
		if !ok {
			return "Windows script-host rule: script execution with a non-literal path is forbidden; use run_script"
		}
		if coordinator == nil || coordinator.wasAgentWritten(item, resolved) {
			return "Windows script-host rule: an agent-written host script cannot be executed; use run_script"
		}
	}
	return ""
}

func forbiddenLOLBinCommand(command string) string {
	words := shellPolicyTokens(strings.ToLower(command))
	for index, word := range words {
		switch shellCommandName(word) {
		case "regsvr32", "rundll32":
			return "Windows LOLBin rule: regsvr32 and rundll32 execution is forbidden"
		case "certutil":
			for _, argument := range words[index+1:] {
				argument = strings.TrimLeft(strings.ToLower(argument), "-/")
				if argument == "decode" || argument == "decodehex" || argument == "urlcache" {
					return "Windows LOLBin rule: certutil decode and URL-cache operations are forbidden"
				}
			}
		}
	}
	return ""
}

func forbiddenSigningCommand(command string) string {
	normalized := strings.ToLower(command)
	normalized = strings.NewReplacer("-", "", "_", "", ".", "", "\\", "/").Replace(normalized)
	if strings.Contains(normalized, "setauthenticodesignature") || strings.Contains(normalized, "signtool") {
		return "code-signing rule: signing commands are reserved for Settings > Security"
	}
	if strings.Contains(normalized, "certutil") {
		for _, operation := range []string{"addstore", "delstore", "importpfx", "repairstore", "setreg", "pulse"} {
			if strings.Contains(normalized, operation) {
				return "certificate-store rule: certutil store operations are reserved for Settings > Security"
			}
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
		if hasScriptSuffix(target, ".ps1", ".psm1", ".cmd", ".bat", ".vbs", ".wsf", ".hta") {
			return true
		}
	}
	return false
}

func containsScriptSuffix(value string) bool {
	for _, suffix := range []string{".ps1", ".psm1", ".bat", ".cmd", ".vbs", ".wsf", ".hta"} {
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
			if hasScriptSuffix(word, ".ps1", ".psm1", ".cmd", ".bat", ".vbs", ".wsf", ".hta") {
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
				if hasScriptSuffix(word, ".vbs", ".wsf", ".js") {
					candidates = append(candidates, word)
					break
				}
			}
		case "mshta":
			for _, word := range words[1:] {
				word = cleanShellScriptToken(word)
				if hasScriptSuffix(word, ".hta") {
					candidates = append(candidates, word)
					break
				}
			}
		case "start-process":
			for _, word := range words[1:] {
				word = cleanShellScriptToken(word)
				if hasScriptSuffix(word, ".ps1", ".psm1", ".cmd", ".bat", ".vbs", ".wsf", ".hta") {
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
	return hasScriptSuffix(value, ".ps1", ".psm1", ".cmd", ".bat", ".vbs", ".wsf", ".hta") && name != ""
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
