package tools

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
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

func TestFetchFeedFixturesHaveExactDeterministicOutput(t *testing.T) {
	for _, name := range []string{"rss-cdata", "atom"} {
		data := fetchFixture(t, name+".xml")
		want := fetchExpected(t, name+".expected.txt")
		got, dropped, err := extractFeed(data)
		if err != nil {
			t.Fatal(err)
		}
		actual := fmt.Sprintf("dropped: %d\n%s", dropped, got)
		if actual != want {
			t.Fatalf("%s output:\n%s\nwant:\n%s", name, actual, want)
		}
	}
}

func TestFetchMalformedFeedReportsExactParsePosition(t *testing.T) {
	_, _, err := extractFeed(fetchFixture(t, "malformed.xml"))
	want := fetchExpected(t, "malformed.expected.txt")
	if err == nil || err.Error() != want {
		t.Fatalf("error=%q want=%q", err, want)
	}
}

func TestFetchArticleFixturesHaveExactDeterministicOutput(t *testing.T) {
	base := mustURL(t, "https://example.test/base")
	for _, name := range []string{"news", "docs", "main", "none"} {
		got, dropped, fallback, err := extractArticle(fetchFixture(t, name+".html"), base)
		if err != nil {
			t.Fatal(err)
		}
		actual := fmt.Sprintf("dropped: %d\nfallback: %t\n%s", dropped, fallback, got)
		want := fetchExpected(t, name+".expected.txt")
		if actual != want {
			t.Fatalf("%s output:\n%s\nwant:\n%s", name, actual, want)
		}
	}
}

func TestFetchModesUseSameWindowEnvelopeAndReportDroppedContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/feed" {
			w.Header().Set("Content-Type", "application/xml")
			_, _ = w.Write(fetchFixture(t, "rss-cdata.xml"))
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write(fetchFixture(t, "main.html"))
	}))
	defer server.Close()
	for _, test := range []struct {
		path, mode, want string
		dropped          int
	}{
		{"/feed", "feed", "# Alpha & Beta", 1},
		{"/article", "article", "Main fixture", 1},
	} {
		detail := NewFetch(fetchTestConfig()).CallDetailed(context.Background(), &session.Session{}, map[string]any{"url": server.URL + test.path, "mode": test.mode, "limit": 64 << 10})
		if detail.Err != nil || !strings.Contains(detail.Content, "extraction_mode: "+test.mode) || !strings.Contains(detail.Content, "> "+test.want) || detail.Metadata["dropped"] != test.dropped {
			t.Fatalf("%s detail=%+v", test.mode, detail)
		}
	}
}

func TestFetchArticleNoMainFallsBackToDefaultWindowExplicitly(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write(fetchFixture(t, "none.html"))
	}))
	defer server.Close()
	detail := NewFetch(fetchTestConfig()).CallDetailed(context.Background(), &session.Session{}, map[string]any{"url": server.URL, "mode": "article"})
	if detail.Err != nil || detail.Metadata["extraction_fallback"] != true || !strings.Contains(detail.Content, "no identifiable main content; returned the default byte window") || !strings.Contains(detail.Content, "> Loose heading") {
		t.Fatalf("fallback detail=%+v", detail)
	}
}

func fetchFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "fetch", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func fetchExpected(t *testing.T, name string) string {
	return strings.TrimSuffix(strings.ReplaceAll(string(fetchFixture(t, name)), "\r\n", "\n"), "\n")
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

func TestFetchDeniedGeolocationHostReturnsRuleWithoutRequest(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		fmt.Fprint(w, "must not be reached")
	}))
	defer server.Close()
	cfg := fetchTestConfig()
	cfg.AllowInternalHosts = nil
	cfg.DenyDomains = []string{"127.0.0.1"}
	item := &session.Session{ToolsEnabled: map[string]bool{"fetch_url": true}}
	outcome := New(NewFetch(cfg)).CallDetailed(context.Background(), item, "fetch_url", map[string]any{"url": server.URL})
	if outcome.OK || !strings.HasPrefix(outcome.Content, "note: network-location rule refused domain 127.0.0.1") || requests.Load() != 0 {
		t.Fatalf("outcome=%+v requests=%d", outcome, requests.Load())
	}

	cfg.AllowDomains = []string{"ipinfo.io"}
	cfg.DenyDomains = []string{"ipinfo.io"}
	if err := validateFetchTarget(mustURL(t, "https://ipinfo.io/json"), cfg); err != nil {
		t.Fatalf("explicit allow_domains did not supersede deny_domains: %v", err)
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

func TestFetchLANPolicyKeepsPermanentRefusals(t *testing.T) {
	cfg := config.Defaults(t.TempDir())
	cfg.Shell.AllowLocalNetwork = true
	cfg.Shell.ConfirmedLocalSubnets = []string{"192.168.50.0/24"}
	fetch := NewFetch(cfg.Tools.Fetch)
	fetch.Configure(cfg)
	prefixes := fetch.localNetworkPrefixes()
	if err := validateFetchTarget(mustURL(t, "http://192.168.50.10/"), cfg.Tools.Fetch, prefixes); err != nil {
		t.Fatalf("confirmed LAN refused: %v", err)
	}
	for _, raw := range []string{"http://127.0.0.1:8790/", "http://169.254.1.1/", "http://169.254.169.254/"} {
		var err error
		if strings.Contains(raw, ":8790") {
			cfg.Listen = "127.0.0.1:8790"
			fetch.Configure(cfg)
			err = fetch.validateTarget(mustURL(t, raw), cfg.Tools.Fetch)
		} else {
			err = validateFetchTarget(mustURL(t, raw), cfg.Tools.Fetch, prefixes)
		}
		if err == nil {
			t.Fatalf("permanent refusal accepted: %s", raw)
		}
	}
	cfg.Tools.Fetch.AllowInternalHosts = []string{"169.254.1.1", "169.254.169.254"}
	for _, raw := range []string{"http://169.254.1.1/", "http://169.254.169.254/"} {
		if err := validateFetchTarget(mustURL(t, raw), cfg.Tools.Fetch, prefixes); err == nil || !strings.Contains(err.Error(), "always refuses") {
			t.Fatalf("internal-host exception bypassed permanent refusal: %s err=%v", raw, err)
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
