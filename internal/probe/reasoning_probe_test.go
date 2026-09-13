package probe

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"harness/internal/config"
	"harness/internal/llm"
)

func TestProbeReasoningClassifiesEmissionShape(t *testing.T) {
	for _, shape := range []string{"structured", "inline", "none"} {
		t.Run(shape, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				var body map[string]any
				if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				message := map[string]any{"role": "assistant", "content": "391"}
				disabled := false
				if kwargs, ok := body["chat_template_kwargs"].(map[string]any); ok && kwargs["enable_thinking"] == false {
					disabled = true
				}
				if !disabled {
					switch shape {
					case "structured":
						message["reasoning_content"] = "17 times 23 is 391"
					case "inline":
						message["content"] = "<think>17 times 23 is 391</think>\n391"
					}
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": message}}})
			}))
			defer server.Close()

			profile := config.Defaults(t.TempDir()).Servers[0]
			profile.BaseURL = server.URL
			profile.Model = "reasoning-fixture"
			caps := config.Capabilities{ReasoningControl: "none"}
			findings := []string{}
			probeReasoning(context.Background(), llm.New(&profile), &profile, &caps, &findings)
			if caps.ReasoningEmission != shape {
				t.Fatalf("emission=%q findings=%v", caps.ReasoningEmission, findings)
			}
			joined := strings.Join(findings, "\n")
			if !strings.Contains(joined, "reasoning emission: "+shape) {
				t.Fatalf("findings=%v", findings)
			}
			if shape == "inline" && !strings.Contains(joined, "--reasoning-format deepseek") {
				t.Fatalf("inline finding has no server fix: %v", findings)
			}
		})
	}
}
