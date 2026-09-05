package llm

import (
	"testing"

	"harness/internal/config"
)

func TestBuildRequestUsesCanonicalReserveDefault(t *testing.T) {
	body := BuildRequest(&config.Profile{}, Request{}, false)
	if got := body["max_tokens"]; got != config.DefaultReserveOutput {
		t.Fatalf("max_tokens=%v, want %d", got, config.DefaultReserveOutput)
	}
}
