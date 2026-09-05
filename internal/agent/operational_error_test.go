package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"harness/internal/config"
	"harness/internal/events"
	"harness/internal/session"
)

func TestOperationalErrorIsPublished(t *testing.T) {
	bus := events.NewBus()
	runner := &Runner{bus: bus}
	runner.operationalError(&session.Session{ID: "main"}, "run", "budget", fmt.Errorf("tokenizer unavailable"))
	recent := bus.Recent("main")
	if len(recent) != 1 || recent[0].Type != events.Error {
		t.Fatalf("recent events=%#v", recent)
	}
	data := recent[0].Data.(map[string]any)
	if data["where"] != "budget" || data["message"] != "tokenizer unavailable" {
		t.Fatalf("error data=%#v", data)
	}
}

func TestFallbackTokenCountAndPreviewUseUnicodeCharacters(t *testing.T) {
	runner := &Runner{}
	count, estimated := runner.count(context.Background(), &config.Profile{}, "🙂🙂🙂🙂")
	if count != 2 || !estimated {
		t.Fatalf("fallback count=%d estimated=%v", count, estimated)
	}
	value := strings.Repeat("🙂", 501)
	got := preview(value)
	if !utf8.ValidString(got) || len([]rune(got)) != 501 || !strings.HasSuffix(got, "…") {
		t.Fatalf("preview is not a 500-character UTF-8 window: runes=%d valid=%v", len([]rune(got)), utf8.ValidString(got))
	}
}
