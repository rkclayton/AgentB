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

func TestBuildRequestCarriesProfileReasoningCap(t *testing.T) {
	profile := &config.Profile{Reasoning: config.Reasoning{Control: "chat_template_kwargs", Enabled: true, MaxTokens: 1000}}
	body := BuildRequest(profile, Request{Thinking: true}, false)
	kwargs := body["chat_template_kwargs"].(map[string]any)
	if kwargs["reasoning_budget"] != 1000 {
		t.Fatalf("kwargs=%v", kwargs)
	}
	profile.Reasoning.Control = "top_level"
	body = BuildRequest(profile, Request{Thinking: true}, false)
	if body["reasoning_budget"] != 1000 {
		t.Fatalf("body=%v", body)
	}
}
