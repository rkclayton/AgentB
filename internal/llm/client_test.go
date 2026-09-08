package llm

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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

func TestTransportErrorsDistinguishDialFromConnectedDeadline(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	dialClient := New(&config.Profile{BaseURL: "http://" + address, RequestTimeoutS: 1})
	if _, _, err := dialClient.DoJSON(context.Background(), http.MethodGet, "/props", nil); TransportKindOf(err) != TransportDial {
		t.Fatalf("dial error kind=%q err=%v", TransportKindOf(err), err)
	}

	connected := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		<-request.Context().Done()
	}))
	defer connected.Close()
	connectedClient := New(&config.Profile{BaseURL: connected.URL, RequestTimeoutS: 1})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, _, err := connectedClient.DoJSON(ctx, http.MethodGet, "/props", nil); TransportKindOf(err) != TransportConnected {
		t.Fatalf("connected error kind=%q err=%v", TransportKindOf(err), err)
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
