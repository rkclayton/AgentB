package probe

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"harness/internal/config"
	"harness/internal/llm"
)

func TestProbeVisionClassifiesAllResponseShapes(t *testing.T) {
	for _, shape := range []string{config.VisionReadsImages, config.VisionAcceptsUnreadable, config.VisionRejected} {
		t.Run(shape, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				if shape == config.VisionRejected {
					http.Error(w, "images rejected", http.StatusBadRequest)
					return
				}
				var body struct {
					Messages []struct {
						Content []map[string]any `json:"content"`
					} `json:"messages"`
				}
				if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				imageURL := body.Messages[0].Content[1]["image_url"].(map[string]any)["url"].(string)
				encoded := strings.TrimPrefix(imageURL, "data:image/png;base64,")
				imageBytes, err := base64.StdEncoding.DecodeString(encoded)
				if err != nil {
					t.Fatal(err)
				}
				digit := ""
				for candidate := 0; candidate < 10; candidate++ {
					if bytes.Equal(imageBytes, probeDigitPNG(candidate)) {
						digit = string(rune('0' + candidate))
					}
				}
				if digit == "" {
					t.Fatal("probe image did not match a generated digit")
				}
				if shape == config.VisionAcceptsUnreadable {
					digit = "I cannot see an image"
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": digit}}}})
			}))
			defer server.Close()

			profile := config.Defaults(t.TempDir()).Servers[0]
			profile.BaseURL = server.URL
			profile.Model = "vision-fixture"
			vision, accepted := probeVision(context.Background(), llm.New(&profile))
			if vision != shape {
				t.Fatalf("vision=%q want=%q", vision, shape)
			}
			if accepted != (shape != config.VisionRejected) {
				t.Fatalf("accepted=%t shape=%q", accepted, shape)
			}
		})
	}
}

func TestLiveVisionProbe(t *testing.T) {
	baseURL := os.Getenv("AGENTB_VISION_LIVE_URL")
	if baseURL == "" {
		t.Skip("set AGENTB_VISION_LIVE_URL and AGENTB_VISION_LIVE_MODEL for an authorized live classification")
	}
	profile := config.Defaults(t.TempDir()).Servers[0]
	profile.BaseURL = baseURL
	profile.Model = os.Getenv("AGENTB_VISION_LIVE_MODEL")
	vision, accepted := probeVision(context.Background(), llm.New(&profile))
	t.Logf("LIVE_VISION_CLASSIFICATION url=%s model=%s vision=%q accepted=%t", baseURL, profile.Model, vision, accepted)
}
