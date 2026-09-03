package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"canary/internal/canary"
)

const systemPrompt = "You are a coding agent working in the workspace at {workspace}. Tools are the only way to see or change files; never guess file contents. Method: inspect before editing; make small, exact edits; verify with a build or test when one exists; then stop and report in one line. Rules: paths are relative to the workspace. If a tool returns an error, fix the call; never repeat an identical call. When the task is done or blocked, say so and stop calling tools."

var taskText = map[string]string{
	"fix": `The check in check/main.go fails. Find the bug it exercises, fix it with edit_file, run "go run ./check" with shell to confirm, then report in one line.`,
	"add": `Add a Multiply(a, b int) int function to calc/ops.go, add a check for it to check/main.go, run "go run ./check" with shell, then report in one line.`,
}

type options struct {
	baseURL, model, workspace, task, out, effort, reasoningControl string
	temperature                                                    float64
	maxTurns                                                       int
	selftest                                                       bool
}

type apiResponse struct {
	Choices []struct {
		Message json.RawMessage `json:"message"`
	} `json:"choices"`
}

type messageView struct {
	Content   any `json:"content"`
	ToolCalls []struct {
		ID       string `json:"id"`
		Type     string `json:"type"`
		Function struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		} `json:"function"`
	} `json:"tool_calls"`
}

type logger struct{ file *os.File }

func main() {
	var opt options
	flag.StringVar(&opt.baseURL, "base-url", "http://127.0.0.1:8080", "OpenAI-compatible server URL")
	flag.StringVar(&opt.model, "model", "qwen3.8-27b", "model alias")
	flag.StringVar(&opt.workspace, "workspace", "", "fixture workspace")
	flag.StringVar(&opt.task, "task", "fix", "fix or add")
	flag.StringVar(&opt.out, "out", "", "JSONL output path")
	flag.Float64Var(&opt.temperature, "temperature", 0.6, "sampling temperature")
	flag.StringVar(&opt.effort, "effort", "medium", "reasoning effort")
	flag.StringVar(&opt.reasoningControl, "reasoning-control", "chat_template_kwargs", "chat_template_kwargs, top_level, or server_flag")
	flag.IntVar(&opt.maxTurns, "max-turns", 12, "maximum model calls")
	flag.BoolVar(&opt.selftest, "selftest", false, "exercise all tools without a model")
	flag.Parse()
	if opt.workspace == "" {
		fatal("--workspace is required")
	}
	if opt.selftest {
		if err := selftest(opt.workspace); err != nil {
			fatal("selftest: %v", err)
		}
		fmt.Println("selftest: ok")
		return
	}
	if taskText[opt.task] == "" || opt.out == "" {
		fatal("--task fix|add and --out are required")
	}
	if err := run(opt); err != nil {
		fatal("%v", err)
	}
}

func run(opt options) error {
	absWorkspace, err := filepath.Abs(opt.workspace)
	if err != nil {
		return err
	}
	if opt.task == "add" {
		if err := canary.PrepareAddFixture(absWorkspace); err != nil {
			return err
		}
	}
	executor, err := canary.NewToolExecutor(absWorkspace)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(opt.out), 0o755); err != nil {
		return err
	}
	file, err := os.Create(opt.out)
	if err != nil {
		return err
	}
	defer file.Close()
	log := logger{file: file}
	messages := []any{
		map[string]any{"role": "system", "content": strings.Replace(systemPrompt, "{workspace}", filepath.ToSlash(absWorkspace), 1)},
		map[string]any{"role": "user", "content": taskText[opt.task]},
	}
	seen := map[string]bool{}
	client := &http.Client{Timeout: 15 * time.Minute}
	turns, stopReason := 0, "max_turns"
	for turns < opt.maxTurns {
		turns++
		body := map[string]any{
			"model": opt.model, "messages": messages, "tools": canary.ToolDefinitions(),
			"tool_choice": "auto", "stream": false, "temperature": opt.temperature,
			"top_p": 0.95, "top_k": 20, "min_p": 0.0, "max_tokens": 8192,
		}
		switch opt.reasoningControl {
		case "chat_template_kwargs":
			body["chat_template_kwargs"] = map[string]any{"enable_thinking": true, "reasoning_effort": opt.effort}
		case "top_level":
			body["reasoning_effort"] = opt.effort
		case "server_flag":
		default:
			return fmt.Errorf("unsupported reasoning control %q", opt.reasoningControl)
		}
		if err := log.write(map[string]any{"event": "request", "turn": turns, "task": opt.task, "body": body}); err != nil {
			return err
		}
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		req, err := http.NewRequest(http.MethodPost, strings.TrimRight(opt.baseURL, "/")+"/v1/chat/completions", bytes.NewReader(encoded))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		response, err := client.Do(req)
		if err != nil {
			return err
		}
		raw, readErr := readBounded(response.Body, 32<<20)
		response.Body.Close()
		if readErr != nil {
			return readErr
		}
		if err := log.write(map[string]any{"event": "response", "turn": turns, "status": response.StatusCode, "body": json.RawMessage(raw)}); err != nil {
			return err
		}
		if response.StatusCode != http.StatusOK {
			return fmt.Errorf("model HTTP %d: %s", response.StatusCode, raw)
		}
		var parsed apiResponse
		if err := json.Unmarshal(raw, &parsed); err != nil {
			return err
		}
		if len(parsed.Choices) == 0 {
			return fmt.Errorf("model response has no choices")
		}
		assistant := map[string]any{}
		if err := json.Unmarshal(parsed.Choices[0].Message, &assistant); err != nil {
			return err
		}
		delete(assistant, "reasoning_content")
		messages = append(messages, assistant)
		var view messageView
		if err := json.Unmarshal(parsed.Choices[0].Message, &view); err != nil {
			return err
		}
		if len(view.ToolCalls) == 0 {
			stopReason = "text"
			break
		}
		cycle := false
		for _, call := range view.ToolCalls {
			args := json.RawMessage(call.Function.Arguments)
			if !json.Valid(args) {
				args = json.RawMessage(`null`)
			}
			result, ok := executor.Execute(context.Background(), call.Function.Name, args)
			digest := sha256.Sum256([]byte(call.Function.Name + "\x00" + call.Function.Arguments + "\x00" + result))
			key := hex.EncodeToString(digest[:])
			if seen[key] {
				cycle = true
			} else {
				seen[key] = true
			}
			if err := log.write(map[string]any{"event": "tool", "turn": turns, "task": opt.task, "id": call.ID, "name": call.Function.Name, "args": json.RawMessage(call.Function.Arguments), "result": result, "ok": ok}); err != nil {
				return err
			}
			messages = append(messages, map[string]any{"role": "tool", "tool_call_id": call.ID, "content": result})
		}
		if cycle {
			stopReason = "cycle"
			break
		}
	}
	if err := log.write(map[string]any{"event": "stop", "reason": stopReason, "turns": turns}); err != nil {
		return err
	}
	checkRaw, _ := json.Marshal(map[string]any{"command": "go run ./check", "timeout_s": 120})
	checkResult, _ := executor.Execute(context.Background(), "shell", checkRaw)
	checkExit := parseExit(checkResult)
	return log.write(map[string]any{"event": "final", "task": opt.task, "check_exit": checkExit, "check_output": checkResult})
}

func (l logger) write(value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = l.file.Write(append(data, '\n'))
	return err
}

func readBounded(body interface{ Read([]byte) (int, error) }, maximum int64) ([]byte, error) {
	var buffer bytes.Buffer
	_, err := buffer.ReadFrom(&limitedReader{reader: body, remaining: maximum + 1})
	if err != nil {
		return nil, err
	}
	if int64(buffer.Len()) > maximum {
		return nil, fmt.Errorf("response exceeds %d bytes", maximum)
	}
	return buffer.Bytes(), nil
}

type limitedReader struct {
	reader    interface{ Read([]byte) (int, error) }
	remaining int64
}

func (r *limitedReader) Read(p []byte) (int, error) {
	if r.remaining <= 0 {
		return 0, fmt.Errorf("response limit exceeded")
	}
	if int64(len(p)) > r.remaining {
		p = p[:r.remaining]
	}
	n, err := r.reader.Read(p)
	r.remaining -= int64(n)
	return n, err
}

func parseExit(result string) int {
	var code int
	if _, err := fmt.Sscanf(result, "exit=%d", &code); err != nil {
		return -1
	}
	return code
}

func selftest(workspace string) error {
	if err := canary.CreateFixture(workspace); err != nil {
		return err
	}
	executor, err := canary.NewToolExecutor(workspace)
	if err != nil {
		return err
	}
	cases := []struct {
		name, args string
		wantOK     bool
		contains   string
	}{
		{"list_dir", `{"path":".","depth":2}`, true, "calc/ops.go"},
		{"read_file", `{"path":"calc/ops.go"}`, true, "func Subtract"},
		{"grep", `{"pattern":"Subtract","path":"."}`, true, "calc/ops.go"},
		{"write_file", `{"path":"scratch/note.txt","content":"ok"}`, true, "ok: wrote"},
		{"edit_file", `{"path":"calc/ops.go","old_string":"return a + b\n}\n\nfunc Divide","new_string":"return a - b\n}\n\nfunc Divide"}`, true, "ok: edited"},
		{"edit_file", `{"path":"calc/ops.go","old_string":"does not exist","new_string":"x"}`, false, "error: old_string not found in calc/ops.go; read the file and retry with the exact text."},
		{"shell", `{"command":"go run ./check","timeout_s":120}`, true, "OK 5 checks"},
	}
	for _, test := range cases {
		result, ok := executor.Execute(context.Background(), test.name, json.RawMessage(test.args))
		if ok != test.wantOK || !strings.Contains(result, test.contains) {
			return fmt.Errorf("%s: ok=%v result=%q", test.name, ok, result)
		}
	}
	return nil
}

func fatal(format string, args ...any) { fmt.Fprintf(os.Stderr, format+"\n", args...); os.Exit(1) }
