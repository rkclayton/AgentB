package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"harness/internal/config"
)

func TestChatStreamRejectsMalformedChunk(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {not-json}\n\ndata: [DONE]\n\n"))
	}))
	defer server.Close()
	client := New(&config.Profile{BaseURL: server.URL, RequestTimeoutS: 5})
	if _, err := client.ChatStream(context.Background(), Request{}, func(Delta) {}); err == nil || !strings.Contains(err.Error(), "decode chat stream chunk") {
		t.Fatalf("malformed stream error=%v", err)
	}
}

func TestChatStreamRejectsEmptyStream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()
	client := New(&config.Profile{BaseURL: server.URL, RequestTimeoutS: 5})
	if _, err := client.ChatStream(context.Background(), Request{}, func(Delta) {}); err == nil || !strings.Contains(err.Error(), "no decodable chunks") {
		t.Fatalf("empty stream error=%v", err)
	}
}

func TestReadBoundedRejectsTruncation(t *testing.T) {
	if raw, err := readBounded(strings.NewReader("123456"), 5); err == nil || raw != nil {
		t.Fatalf("oversize body raw=%q err=%v", raw, err)
	}
	if raw, err := readBounded(strings.NewReader("12345"), 5); err != nil || string(raw) != "12345" {
		t.Fatalf("at-limit body raw=%q err=%v", raw, err)
	}
}
