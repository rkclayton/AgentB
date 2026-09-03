package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadServingFactsCompleteness(t *testing.T) {
	path := filepath.Join(t.TempDir(), "SERVING.md")
	if facts := readServingFacts(path); facts.Complete {
		t.Fatal("missing file reported complete")
	}
	if err := os.WriteFile(path, []byte("tokenize_idle_ms=8\ntokenize_blocks_on_slot=no\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if facts := readServingFacts(path); facts.Complete {
		t.Fatal("partial file reported complete")
	}
	if err := os.WriteFile(path, []byte("tokenize_idle_ms=8\ntokenize_busy_ms=9\ntokenize_blocks_on_slot=no\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	facts := readServingFacts(path)
	if !facts.Complete || facts.TokenizeIdleMS != 8 || facts.TokenizeBusyMS != 9 || facts.TokenizeBlocksOnSlot != "no" {
		t.Fatalf("unexpected facts: %+v", facts)
	}
}

func TestStartupElevationGuard(t *testing.T) {
	if err := startupElevationError(false); err != nil {
		t.Fatalf("standard token refused: %v", err)
	}
	err := startupElevationError(true)
	if err == nil || !strings.Contains(err.Error(), "refuses to run") || !strings.Contains(err.Error(), "File Explorer") {
		t.Fatalf("elevated token error = %v", err)
	}
}
