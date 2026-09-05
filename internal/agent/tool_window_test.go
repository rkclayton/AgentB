package agent

import (
	"context"
	"strings"
	"testing"

	"harness/internal/config"
)

func TestFitWindowResultRequestsSmallerSameOffsetInsteadOfOverflowingContext(t *testing.T) {
	cfg := config.Defaults(t.TempDir())
	runner := &Runner{cfg: func() config.Config { return cfg }}
	profile := &cfg.Servers[0]
	metadata := map[string]any{"window_offset": 65537, "next_offset": 131073, "more": true}

	content, ok, gotMetadata, tokens := runner.fitWindowResult(
		context.Background(), profile, "fetch_url",
		map[string]any{"offset": float64(65537), "limit": float64(65536)},
		strings.Repeat("x", 65536), true, metadata, 34903, 19996,
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
		content, ok, _, tokens := runner.fitWindowResult(context.Background(), profile, test.name, nil, "original", test.ok, nil, test.tokens, test.available)
		if content != "original" || ok != test.ok || tokens != test.tokens {
			t.Fatalf("%s changed: content=%q ok=%t tokens=%d", test.name, content, ok, tokens)
		}
	}
}
