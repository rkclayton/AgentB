package tools

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"harness/internal/config"
	"harness/internal/session"
)

func TestFetchExtractsReadableHTMLAndKeepsLinks(t *testing.T) {
	page, err := extractHTML([]byte(`<!doctype html><html><head><title>Example</title><style>hidden</style></head><body><nav>menu</nav><main><h1>Hello</h1><p>Read <a href="/more">more</a>.</p><script>bad()</script></main></body></html>`), mustURL(t, "https://example.com/base"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Example", "Hello", "Read more (https://example.com/more)"} {
		if !strings.Contains(page, want) {
			t.Fatalf("extracted text %q does not contain %q", page, want)
		}
	}
	for _, unwanted := range []string{"hidden", "menu", "bad()"} {
		if strings.Contains(page, unwanted) {
			t.Fatalf("extracted text retained %q: %q", unwanted, page)
		}
	}
}

func TestFetchSingleLineByteWindowsDoNotRepeat(t *testing.T) {
	var source strings.Builder
	for index := 0; index < 120; index++ {
		fmt.Fprintf(&source, "item-%04d|", index)
	}
	source.WriteString("🙂-tail")
	payload := source.String()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		fmt.Fprint(w, payload)
	}))
	defer server.Close()

	offset := 1
	seen := map[string]bool{}
	var rebuilt strings.Builder
	for {
		detail := NewFetch(fetchTestConfig()).CallDetailed(context.Background(), &session.Session{}, map[string]any{"url": server.URL, "offset": offset, "limit": 73})
		if detail.Err != nil {
			t.Fatal(detail.Err)
		}
		window := envelopePayload(t, detail.Content)
		if seen[window] {
			t.Fatalf("fetch_url repeated window %q at offset %d", window, offset)
		}
		seen[window] = true
		rebuilt.WriteString(window)
		if detail.Metadata["window_offset"] != offset || detail.Metadata["window_bytes"] != len([]byte(window)) || detail.Metadata["total_bytes"] != len([]byte(payload)) {
			t.Fatalf("window metadata=%#v content=%q", detail.Metadata, window)
		}
		more, _ := detail.Metadata["more"].(bool)
		if !more {
			if _, exists := detail.Metadata["next_offset"]; exists {
				t.Fatalf("completed response retained cursor: %#v", detail.Metadata)
			}
			break
		}
		next, ok := detail.Metadata["next_offset"].(int)
		if !ok || next <= offset {
			t.Fatalf("invalid next cursor in %#v", detail.Metadata)
		}
		offset = next
	}
	if rebuilt.String() != payload {
		t.Fatalf("rebuilt payload=%q want=%q", rebuilt.String(), payload)
	}
}

func TestFetchRedirectsAndLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/start" {
			http.Redirect(w, r, "/final", http.StatusFound)
			return
		}
		fmt.Fprint(w, "arrived")
	}))
	defer server.Close()

	cfg := fetchTestConfig()
	detail := NewFetch(cfg).CallDetailed(context.Background(), &session.Session{}, map[string]any{"url": server.URL + "/start"})
	if detail.Err != nil || !strings.Contains(detail.Content, "> arrived") {
		t.Fatalf("redirect fetch: content=%q err=%v", detail.Content, detail.Err)
	}
	cfg.MaxRedirects = 0
	detail = NewFetch(cfg).CallDetailed(context.Background(), &session.Session{}, map[string]any{"url": server.URL + "/start"})
	if detail.Err == nil || !strings.Contains(detail.Err.Error(), "redirect limit") {
		t.Fatalf("redirect limit err=%v", detail.Err)
	}
}

func TestFetchResponseSizeLimitAndEnvelope(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, "123456789")
	}))
	defer server.Close()
	cfg := fetchTestConfig()
	cfg.MaxBytes = 5
	detail := NewFetch(cfg).CallDetailed(context.Background(), &session.Session{}, map[string]any{"url": server.URL})
	if detail.Err != nil {
		t.Fatal(detail.Err)
	}
	for _, want := range []string{"[BEGIN UNTRUSTED FETCHED CONTENT]", fetchWarning, "source_bytes: 5", "source_truncated: true", "window_offset: 1", "window_bytes: 5", "total_bytes: 5", "more: false", "> 12345", "[END UNTRUSTED FETCHED CONTENT]"} {
		if !strings.Contains(detail.Content, want) {
			t.Fatalf("envelope missing %q: %q", want, detail.Content)
		}
	}
	if detail.Category != "fetched" || !detail.Untrusted || detail.Metadata["source_truncated"] != true {
		t.Fatalf("detail classification: %+v", detail)
	}
}

func TestFetchTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(1200 * time.Millisecond)
		fmt.Fprint(w, "late")
	}))
	defer server.Close()
	cfg := fetchTestConfig()
	cfg.TimeoutS = 1
	detail := NewFetch(cfg).CallDetailed(context.Background(), &session.Session{}, map[string]any{"url": server.URL})
	if detail.Err == nil || !strings.Contains(detail.Err.Error(), "request failed") {
		t.Fatalf("timeout err=%v", detail.Err)
	}
}

func TestFetchNon2xxIsFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		fmt.Fprint(w, "upstream unavailable")
	}))
	defer server.Close()
	registry := New(NewFetch(fetchTestConfig()))
	item := &session.Session{ToolsEnabled: map[string]bool{"fetch_url": true}}
	outcome := registry.CallDetailed(context.Background(), item, "fetch_url", map[string]any{"url": server.URL})
	if outcome.OK || !strings.Contains(outcome.Content, "HTTP status 502") || outcome.Metadata["status"] != http.StatusBadGateway {
		t.Fatalf("non-2xx outcome = %#v", outcome)
	}
}

func TestFetchRefusesBinaryMIME(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Write([]byte{0, 1, 2})
	}))
	defer server.Close()
	detail := NewFetch(fetchTestConfig()).CallDetailed(context.Background(), &session.Session{}, map[string]any{"url": server.URL})
	if detail.Err == nil || !strings.Contains(detail.Err.Error(), "unsupported content type") {
		t.Fatalf("binary MIME err=%v", detail.Err)
	}
}

func TestFetchDomainAllowList(t *testing.T) {
	cfg := fetchTestConfig()
	cfg.AllowDomains = []string{"example.com"}
	detail := NewFetch(cfg).CallDetailed(context.Background(), &session.Session{}, map[string]any{"url": "http://not-example.invalid/"})
	if detail.Err == nil || !strings.Contains(detail.Err.Error(), "allow_domains") {
		t.Fatalf("allow-list err=%v", detail.Err)
	}
	if !domainAllowed("docs.example.com", cfg.AllowDomains) {
		t.Fatal("subdomain of allowed domain was refused")
	}
}

func TestFetchRegistryClassifiesRefusalsAsUntrustedFetchedResults(t *testing.T) {
	cfg := fetchTestConfig()
	cfg.AllowInternalHosts = nil
	registry := New(NewFetch(cfg))
	item := &session.Session{ToolsEnabled: map[string]bool{"fetch_url": true}}
	outcome := registry.CallDetailed(context.Background(), item, "fetch_url", map[string]any{"url": "http://127.0.0.1/"})
	if outcome.OK || outcome.Category != "fetched" || !outcome.Untrusted {
		t.Fatalf("outcome classification = %+v", outcome)
	}
}

func TestFetchSSRFGuardAndSpecificInternalException(t *testing.T) {
	cfg := fetchTestConfig()
	cfg.AllowInternalHosts = nil
	detail := NewFetch(cfg).CallDetailed(context.Background(), &session.Session{}, map[string]any{"url": "http://127.0.0.1/"})
	if detail.Err == nil || !strings.Contains(detail.Err.Error(), "SSRF guard") {
		t.Fatalf("SSRF err=%v", detail.Err)
	}
	if err := validateFetchTarget(mustURL(t, "http://127.0.0.1/"), fetchTestConfig()); err != nil {
		t.Fatalf("specific internal exception refused: %v", err)
	}
	for _, raw := range []string{"10.0.0.1", "172.16.0.1", "192.168.1.1", "169.254.1.1", "100.64.0.1", "::1", "fd00::1", "fe80::1"} {
		if !blockedFetchIP(net.ParseIP(raw)) {
			t.Errorf("private address %s was not blocked", raw)
		}
	}
}

func fetchTestConfig() config.FetchTool {
	return config.FetchTool{TimeoutS: 5, MaxBytes: 1 << 20, MaxRedirects: 3, DefaultLimit: 16 << 10, MaxLimit: 64 << 10, AllowDomains: []string{}, AllowInternalHosts: []string{"127.0.0.1"}}
}

func envelopePayload(t *testing.T, envelope string) string {
	t.Helper()
	for _, line := range strings.Split(envelope, "\n") {
		if strings.HasPrefix(line, "> ") {
			return strings.TrimPrefix(line, "> ")
		}
	}
	t.Fatalf("envelope has no content line: %q", envelope)
	return ""
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}
