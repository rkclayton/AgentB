package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"harness/internal/config"
	"harness/internal/session"
	"harness/internal/tools"
)

func TestFitWindowResultRequestsSmallerSameOffsetInsteadOfOverflowingContext(t *testing.T) {
	cfg := config.Defaults(t.TempDir())
	runner := &Runner{cfg: func() config.Config { return cfg }}
	profile := &cfg.Servers[0]
	metadata := map[string]any{"window_offset": 65537, "next_offset": 131073, "more": true}

	content, ok, gotMetadata, tokens := runner.fitWindowResult(
		context.Background(), nil, profile, "fetch_url",
		map[string]any{"offset": float64(65537), "limit": float64(65536)},
		strings.Repeat("x", 65536), true, metadata, 34903, 19996, false,
	)

	if ok || tokens < 1 {
		t.Fatalf("ok=%t tokens=%d content=%q", ok, tokens, content)
	}
	for _, want := range []string{"too large for the current model context", "same offset=65537", "limit no greater than 16384", "Do not advance"} {
		if !strings.Contains(content, want) {
			t.Fatalf("content %q does not contain %q", content, want)
		}
	}
	if gotMetadata["result_too_large"] != true || gotMetadata["retry_offset"] != 65537 || gotMetadata["retry_limit"] != 16384 {
		t.Fatalf("metadata = %#v", gotMetadata)
	}
	if metadata["result_too_large"] != nil {
		t.Fatalf("source metadata was mutated: %#v", metadata)
	}
}

func TestFitReadFileClampsToLargestSuccessfulWindow(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "large.txt")
	if err := os.WriteFile(path, []byte(strings.Repeat("abcdefghij", 4000)), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults(root)
	registry := tools.New(tools.NewReadFile(cfg.Tools.ReadFile))
	item := &session.Session{Workspace: root, ToolsEnabled: map[string]bool{"read_file": true}, LastSeen: map[string]time.Time{}}
	runner := &Runner{cfg: func() config.Config { return cfg }, tools: registry}
	profile := &cfg.Servers[0]
	args := map[string]any{"path": "large.txt", "offset": 1, "limit": 40000}
	original, originalOK := registry.Call(context.Background(), item, "read_file", args)
	if !originalOK {
		t.Fatal(original)
	}
	originalTokens := runner.textTokens(context.Background(), profile, original)
	available := 1000
	content, ok, metadata, tokens := runner.fitWindowResult(context.Background(), item, profile, "read_file", args, original, true, nil, originalTokens, available, false)
	if !ok || tokens > available || metadata["result_clamped"] != true {
		t.Fatalf("ok=%t tokens=%d metadata=%#v content=%q", ok, tokens, metadata, content)
	}
	for _, want := range []string{"requested_bytes=40000", "remaining_bytes=", "next_offset=", "[byte window:"} {
		if !strings.Contains(content, want) {
			t.Fatalf("clamped content missing %q: %s", want, content)
		}
	}
	limit := metadata["returned_limit"].(int)
	largerArgs := cloneMetadata(args)
	largerArgs["limit"] = limit + 1
	larger, largerOK := registry.Call(context.Background(), item, "read_file", largerArgs)
	if !largerOK {
		t.Fatal(larger)
	}
	returned := headerInteger(larger, "bytes")
	remaining := max(0, headerInteger(larger, "total")-returned)
	next := headerInteger(larger, "next_offset")
	note := fmt.Sprintf("[context clamp: requested_bytes=40000 returned_bytes=%d remaining_bytes=%d next_offset=%d]\n", returned, remaining, next)
	if runner.textTokens(context.Background(), profile, note+larger) <= available {
		t.Fatalf("returned limit %d was not maximal", limit)
	}
}

func TestFitWindowResultLeavesFittingAndNonWindowResultsUnchanged(t *testing.T) {
	cfg := config.Defaults(t.TempDir())
	runner := &Runner{cfg: func() config.Config { return cfg }}
	profile := &cfg.Servers[0]

	for _, test := range []struct {
		name      string
		ok        bool
		available int
		tokens    int
	}{
		{name: "read_file", ok: true, available: 100, tokens: 100},
		{name: "shell", ok: true, available: 10, tokens: 100},
		{name: "fetch_url", ok: false, available: 10, tokens: 100},
	} {
		content, ok, _, tokens := runner.fitWindowResult(context.Background(), nil, profile, test.name, nil, "original", test.ok, nil, test.tokens, test.available, false)
		if content != "original" || ok != test.ok || tokens != test.tokens {
			t.Fatalf("%s changed: content=%q ok=%t tokens=%d", test.name, content, ok, tokens)
		}
	}
}
