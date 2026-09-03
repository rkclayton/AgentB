package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type event struct {
	Event     string          `json:"event"`
	Task      string          `json:"task"`
	Name      string          `json:"name"`
	Args      map[string]any  `json:"args"`
	Result    string          `json:"result"`
	OK        bool            `json:"ok"`
	Turns     int             `json:"turns"`
	CheckExit int             `json:"check_exit"`
	Body      json.RawMessage `json:"body"`
}

type score struct {
	trial, task string
	completed   bool
	wrong       int
	invalid     int
	turns       int
	pass        bool
}

func main() {
	if len(os.Args) != 2 {
		fatal("usage: go run ./cmd/score <run dir>")
	}
	files, err := filepath.Glob(filepath.Join(os.Args[1], "*.jsonl"))
	if err != nil {
		fatal("%v", err)
	}
	sort.Strings(files)
	if len(files) == 0 {
		fatal("no JSONL trials in %s", os.Args[1])
	}
	results := make([]score, 0, len(files))
	for _, path := range files {
		value, err := scoreFile(path)
		if err != nil {
			fatal("%s: %v", path, err)
		}
		results = append(results, value)
	}
	fmt.Println("| trial | task | completed | wrong_tool | invalid_args | turns | pass |")
	fmt.Println("|---|---|---:|---:|---:|---:|---:|")
	passes := 0
	for _, value := range results {
		if value.pass {
			passes++
		}
		fmt.Printf("| %s | %s | %t | %d | %d | %d | %t |\n", value.trial, value.task, value.completed, value.wrong, value.invalid, value.turns, value.pass)
	}
	fmt.Printf("passes: %d/%d\n", passes, len(results))
}

func scoreFile(path string) (score, error) {
	file, err := os.Open(path)
	if err != nil {
		return score{}, err
	}
	defer file.Close()
	value := score{trial: strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))}
	readPaths := map[string]bool{}
	inspectionSucceeded := false
	textOnlySeen := false
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 64*1024*1024)
	for scanner.Scan() {
		var item event
		if err := json.Unmarshal(scanner.Bytes(), &item); err != nil {
			return score{}, err
		}
		if item.Task != "" {
			value.task = item.Task
		}
		switch item.Event {
		case "response":
			value.turns++
			var response struct {
				Choices []struct {
					Message struct {
						Content   any   `json:"content"`
						ToolCalls []any `json:"tool_calls"`
					} `json:"message"`
				} `json:"choices"`
			}
			if json.Unmarshal(item.Body, &response) == nil && len(response.Choices) > 0 && len(response.Choices[0].Message.ToolCalls) == 0 && response.Choices[0].Message.Content != nil {
				textOnlySeen = true
			}
		case "tool":
			if textOnlySeen {
				value.wrong++
			}
			pathArg, _ := item.Args["path"].(string)
			pathArg = filepath.ToSlash(filepath.Clean(pathArg))
			switch item.Name {
			case "write_file":
				value.wrong++
			case "shell":
				if !inspectionSucceeded {
					value.wrong++
				}
			case "edit_file":
				if !readPaths[pathArg] {
					value.wrong++
				}
			}
			if item.OK && (item.Name == "read_file" || item.Name == "grep") {
				inspectionSucceeded = true
				if item.Name == "read_file" {
					readPaths[pathArg] = true
				}
			}
			if strings.HasPrefix(item.Result, "error:") {
				value.invalid++
			}
		case "stop":
			if item.Turns > value.turns {
				value.turns = item.Turns
			}
		case "final":
			value.completed = item.CheckExit == 0
		}
	}
	if err := scanner.Err(); err != nil {
		return score{}, err
	}
	value.pass = value.completed && value.wrong <= 1 && value.invalid <= 1 && value.turns <= 10
	if value.task == "" {
		value.task = "unknown"
	}
	return value, nil
}

func fatal(format string, args ...any) { fmt.Fprintf(os.Stderr, format+"\n", args...); os.Exit(1) }
