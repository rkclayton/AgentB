package canary

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

type ToolDefinition struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

type ToolFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	} `json:"function"`
}

type ToolExecutor struct {
	workspace string
}

func NewToolExecutor(workspace string) (*ToolExecutor, error) {
	abs, err := filepath.Abs(workspace)
	if err != nil {
		return nil, err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, err
	}
	return &ToolExecutor{workspace: filepath.Clean(resolved)}, nil
}

func ToolDefinitions() []ToolDefinition {
	stringValue := func() map[string]any { return map[string]any{"type": "string"} }
	return []ToolDefinition{
		tool("read_file", "Read a UTF-8 text file in the workspace. Returns lines from offset, at most limit lines; a trailing note says how many lines remain.", map[string]any{
			"path": stringValue(), "offset": map[string]any{"type": "integer", "default": 1}, "limit": map[string]any{"type": "integer", "default": 200, "maximum": 400},
		}, []string{"path"}),
		tool("list_dir", "List a workspace directory, one entry per line, directories end with /. Skips .git and build folders.", map[string]any{
			"path": map[string]any{"type": "string", "default": "."}, "depth": map[string]any{"type": "integer", "default": 1, "maximum": 3},
		}, nil),
		tool("write_file", "Create or overwrite a file with the given content, creating parent folders. For changes inside an existing file use edit_file.", map[string]any{
			"path": stringValue(), "content": stringValue(),
		}, []string{"path", "content"}),
		tool("edit_file", "Replace exactly one occurrence of old_string with new_string. old_string must match the file text exactly, including indentation, and must be unique; include surrounding lines if needed. Returns what changed, or an error naming the closest match.", map[string]any{
			"path": stringValue(), "old_string": stringValue(), "new_string": stringValue(),
		}, []string{"path", "old_string", "new_string"}),
		tool("search_text", "Search local file contents under path for pattern, optionally filtering filenames with glob. Unlike find_files, it returns matching text lines.", map[string]any{
			"pattern": stringValue(), "path": map[string]any{"type": "string", "default": "."}, "glob": stringValue(),
		}, []string{"pattern"}),
		tool("shell", "Run a non-interactive shell command from the workspace root with a timeout. Returns the exit code and combined output, cut to head and tail if long.", map[string]any{
			"command": stringValue(), "timeout_s": map[string]any{"type": "integer", "default": 60},
		}, []string{"command"}),
	}
}

func tool(name, description string, properties map[string]any, required []string) ToolDefinition {
	parameters := map[string]any{"type": "object", "properties": properties}
	if len(required) > 0 {
		parameters["required"] = required
	}
	return ToolDefinition{Type: "function", Function: ToolFunction{Name: name, Description: description, Parameters: parameters}}
}

func (e *ToolExecutor) Execute(ctx context.Context, name string, raw json.RawMessage) (string, bool) {
	var args map[string]any
	if err := json.Unmarshal(raw, &args); err != nil {
		return errorResult("invalid JSON arguments: %v", err), false
	}
	var result string
	var err error
	switch name {
	case "read_file":
		result, err = e.readFile(args)
	case "list_dir":
		result, err = e.listDir(args)
	case "write_file":
		result, err = e.writeFile(args)
	case "edit_file":
		result, err = e.editFile(args)
	case "search_text":
		result, err = e.grep(args)
	case "shell":
		result, err = e.shell(ctx, args)
	default:
		err = fmt.Errorf("unknown tool %s", name)
	}
	if err != nil {
		return errorResult("%v", err), false
	}
	return result, true
}

func (e *ToolExecutor) resolve(path string, allowMissing bool) (string, error) {
	if path == "" {
		path = "."
	}
	if filepath.IsAbs(path) {
		return "", fmt.Errorf("path is outside the workspace")
	}
	candidate := filepath.Clean(filepath.Join(e.workspace, filepath.FromSlash(path)))
	check := candidate
	if _, err := os.Lstat(check); err != nil {
		if !allowMissing || !os.IsNotExist(err) {
			return "", err
		}
		existing := filepath.Dir(check)
		for {
			if _, statErr := os.Lstat(existing); statErr == nil {
				break
			} else if !os.IsNotExist(statErr) {
				return "", statErr
			}
			next := filepath.Dir(existing)
			if next == existing {
				return "", fmt.Errorf("path is outside the workspace")
			}
			existing = next
		}
		resolvedParent, resolveErr := filepath.EvalSymlinks(existing)
		if resolveErr != nil {
			return "", resolveErr
		}
		remainder, relErr := filepath.Rel(existing, check)
		if relErr != nil {
			return "", relErr
		}
		check = filepath.Join(resolvedParent, remainder)
	} else {
		resolved, err := filepath.EvalSymlinks(check)
		if err != nil {
			return "", err
		}
		check = resolved
	}
	rel, err := filepath.Rel(e.workspace, check)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("path is outside the workspace")
	}
	return check, nil
}

func (e *ToolExecutor) readFile(args map[string]any) (string, error) {
	path, err := requiredString(args, "path")
	if err != nil {
		return "", err
	}
	resolved, err := e.resolve(path, false)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(resolved)
	if err != nil {
		return "", err
	}
	offset := intArg(args, "offset", 1)
	limit := intArg(args, "limit", 200)
	if offset < 1 {
		return "", fmt.Errorf("offset must be at least 1")
	}
	if limit < 1 || limit > 400 {
		return "", fmt.Errorf("limit must be between 1 and 400")
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	if offset > len(lines) {
		return "", fmt.Errorf("offset %d exceeds file length %d", offset, len(lines))
	}
	end := min(len(lines), offset-1+limit)
	result := strings.Join(lines[offset-1:end], "\n")
	if remaining := len(lines) - end; remaining > 0 {
		result += fmt.Sprintf("\n[%d lines remain]", remaining)
	}
	return result, nil
}

func (e *ToolExecutor) listDir(args map[string]any) (string, error) {
	path := stringArg(args, "path", ".")
	depth := intArg(args, "depth", 1)
	if depth < 1 || depth > 3 {
		return "", fmt.Errorf("depth must be between 1 and 3")
	}
	root, err := e.resolve(path, false)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("not a directory: %s", path)
	}
	var entries []string
	var walk func(string, int) error
	walk = func(dir string, level int) error {
		children, readErr := os.ReadDir(dir)
		if readErr != nil {
			return readErr
		}
		sort.Slice(children, func(i, j int) bool { return children[i].Name() < children[j].Name() })
		for _, child := range children {
			if child.IsDir() && (child.Name() == ".git" || child.Name() == "build") {
				continue
			}
			full := filepath.Join(dir, child.Name())
			rel, _ := filepath.Rel(root, full)
			display := filepath.ToSlash(rel)
			if child.IsDir() {
				display += "/"
			}
			entries = append(entries, display)
			if child.IsDir() && level < depth {
				if err := walk(full, level+1); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(root, 1); err != nil {
		return "", err
	}
	return strings.Join(entries, "\n"), nil
}

func (e *ToolExecutor) writeFile(args map[string]any) (string, error) {
	path, err := requiredString(args, "path")
	if err != nil {
		return "", err
	}
	content, err := requiredString(args, "content")
	if err != nil {
		return "", err
	}
	resolved, err := e.resolve(path, true)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(resolved), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(resolved, []byte(content), 0o644); err != nil {
		return "", err
	}
	return fmt.Sprintf("ok: wrote %s (%d bytes)", filepath.ToSlash(path), len(content)), nil
}

func (e *ToolExecutor) editFile(args map[string]any) (string, error) {
	path, err := requiredString(args, "path")
	if err != nil {
		return "", err
	}
	oldText, err := requiredString(args, "old_string")
	if err != nil {
		return "", err
	}
	newText, err := requiredString(args, "new_string")
	if err != nil {
		return "", err
	}
	resolved, err := e.resolve(path, false)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(resolved)
	if err != nil {
		return "", err
	}
	updated, ok := replaceUniqueExact(string(data), oldText, newText)
	if !ok {
		updated, ok = replaceTrailingWhitespaceMatch(string(data), oldText, newText)
	}
	if !ok {
		return "", fmt.Errorf("old_string not found in %s; read the file and retry with the exact text.", filepath.ToSlash(path))
	}
	if err := os.WriteFile(resolved, []byte(updated), 0o644); err != nil {
		return "", err
	}
	return fmt.Sprintf("ok: edited %s", filepath.ToSlash(path)), nil
}

func replaceUniqueExact(text, oldText, newText string) (string, bool) {
	if oldText == "" || strings.Count(text, oldText) != 1 {
		return text, false
	}
	return strings.Replace(text, oldText, newText, 1), true
}

func replaceTrailingWhitespaceMatch(text, oldText, newText string) (string, bool) {
	normalize := func(value string) string {
		value = strings.ReplaceAll(value, "\r\n", "\n")
		lines := strings.Split(value, "\n")
		for index := range lines {
			lines[index] = strings.TrimRight(lines[index], " \t")
		}
		return strings.Join(lines, "\n")
	}
	normalizedText := normalize(text)
	normalizedOld := normalize(oldText)
	if normalizedOld == "" || strings.Count(normalizedText, normalizedOld) != 1 {
		return text, false
	}
	start := strings.Index(normalizedText, normalizedOld)
	if start < 0 {
		return text, false
	}
	// Map normalized offsets back by scanning line windows; this tier intentionally
	// only ignores trailing horizontal whitespace.
	oldLines := strings.Split(strings.ReplaceAll(oldText, "\r\n", "\n"), "\n")
	originalLF := strings.ReplaceAll(text, "\r\n", "\n")
	fileLines := strings.Split(originalLF, "\n")
	matches := make([]int, 0, 1)
	for i := 0; i+len(oldLines) <= len(fileLines); i++ {
		match := true
		for j := range oldLines {
			if strings.TrimRight(fileLines[i+j], " \t") != strings.TrimRight(oldLines[j], " \t") {
				match = false
				break
			}
		}
		if match {
			matches = append(matches, i)
		}
	}
	if len(matches) != 1 {
		return text, false
	}
	i := matches[0]
	replacement := append([]string{}, fileLines[:i]...)
	replacement = append(replacement, strings.Split(strings.ReplaceAll(newText, "\r\n", "\n"), "\n")...)
	replacement = append(replacement, fileLines[i+len(oldLines):]...)
	result := strings.Join(replacement, "\n")
	if strings.Contains(text, "\r\n") {
		result = strings.ReplaceAll(result, "\n", "\r\n")
	}
	return result, true
}

func (e *ToolExecutor) grep(args map[string]any) (string, error) {
	pattern, err := requiredString(args, "pattern")
	if err != nil {
		return "", err
	}
	expression, err := regexp.Compile(pattern)
	if err != nil {
		return "", fmt.Errorf("invalid regular expression: %v", err)
	}
	path := stringArg(args, "path", ".")
	glob := stringArg(args, "glob", "")
	root, err := e.resolve(path, false)
	if err != nil {
		return "", err
	}
	var matches []string
	err = filepath.WalkDir(root, func(filePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if filePath != root && (entry.Name() == ".git" || entry.Name() == "build") {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(e.workspace, filePath)
		rel = filepath.ToSlash(rel)
		if glob != "" {
			matched, matchErr := filepath.Match(glob, entry.Name())
			if matchErr != nil {
				return fmt.Errorf("invalid glob: %v", matchErr)
			}
			if !matched {
				return nil
			}
		}
		file, openErr := os.Open(filePath)
		if openErr != nil {
			return openErr
		}
		defer file.Close()
		scanner := bufio.NewScanner(file)
		lineNumber := 0
		for scanner.Scan() {
			lineNumber++
			if expression.MatchString(scanner.Text()) {
				matches = append(matches, fmt.Sprintf("%s:%d: %s", rel, lineNumber, scanner.Text()))
				if len(matches) == 50 {
					return errMatchLimit
				}
			}
		}
		return scanner.Err()
	})
	if err != nil && err != errMatchLimit {
		return "", err
	}
	return strings.Join(matches, "\n"), nil
}

var errMatchLimit = fmt.Errorf("match limit reached")

func (e *ToolExecutor) shell(ctx context.Context, args map[string]any) (string, error) {
	command, err := requiredString(args, "command")
	if err != nil {
		return "", err
	}
	timeoutSeconds := intArg(args, "timeout_s", 60)
	if timeoutSeconds < 1 || timeoutSeconds > 600 {
		return "", fmt.Errorf("timeout_s must be between 1 and 600")
	}
	timeoutContext, cancel := context.WithTimeout(ctx, time.Duration(timeoutSeconds)*time.Second)
	defer cancel()
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", command)
	} else {
		cmd = exec.Command("bash", "-lc", command)
	}
	cmd.Dir = e.workspace
	prepareProcess(cmd)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Start(); err != nil {
		return "", err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	var runErr error
	select {
	case runErr = <-done:
	case <-timeoutContext.Done():
		killProcessTree(cmd)
		<-done
		return "", fmt.Errorf("timed out after %ds; partial output:\n%s", timeoutSeconds, boundedOutput(output.String()))
	}
	exitCode := 0
	if runErr != nil {
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return "", runErr
		}
	}
	return fmt.Sprintf("exit=%d\n%s", exitCode, boundedOutput(output.String())), nil
}

func boundedOutput(value string) string {
	lines := strings.Split(strings.TrimRight(value, "\r\n"), "\n")
	if len(lines) <= 120 {
		return strings.Join(lines, "\n")
	}
	return strings.Join(append(append(lines[:60], fmt.Sprintf("[… %d lines omitted …]", len(lines)-120)), lines[len(lines)-60:]...), "\n")
}

func requiredString(args map[string]any, key string) (string, error) {
	value, ok := args[key]
	if !ok {
		return "", fmt.Errorf("missing required argument %s", key)
	}
	text, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("argument %s must be a string", key)
	}
	return text, nil
}

func stringArg(args map[string]any, key, fallback string) string {
	value, ok := args[key].(string)
	if !ok {
		return fallback
	}
	return value
}

func intArg(args map[string]any, key string, fallback int) int {
	value, ok := args[key]
	if !ok {
		return fallback
	}
	switch typed := value.(type) {
	case float64:
		return int(typed)
	case int:
		return typed
	case string:
		parsed, err := strconv.Atoi(typed)
		if err == nil {
			return parsed
		}
	}
	return fallback
}

func errorResult(format string, args ...any) string {
	return "error: " + fmt.Sprintf(format, args...)
}
