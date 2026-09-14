package web

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"harness/internal/config"
	"harness/internal/probe"
)

func TestFailedProbePreservesPreviousTimestamp(t *testing.T) {
	profile := &config.Profile{Capabilities: config.Capabilities{ProbedAt: "2026-09-04T12:00:00Z", Server: "llama.cpp"}}
	caps, findings := failedProbeCapabilities(profile, fmt.Errorf("connection refused"))
	if caps.ProbedAt != profile.Capabilities.ProbedAt || caps.Server != "llama.cpp" {
		t.Fatalf("failed probe capabilities=%+v", caps)
	}
	if len(findings) != 1 || findings[0] != "probe failed: connection refused" {
		t.Fatalf("findings=%v", findings)
	}
}

func TestProbeNonJSONFailuresUseFriendlyRowsAndDiagnosticFindings(t *testing.T) {
	tests := []struct {
		name, want string
		handler    http.HandlerFunc
	}{
		{name: "html root", want: "Connection returned a web page, not model API JSON. Add the API path to base_url.", handler: func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, "<!doctype html><title>Server home</title>")
		}},
		{name: "unauthorized", want: "Connection requires credentials. Add the API credential, then Test again.", handler: func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, "<html>Unauthorized</html>")
		}},
		{name: "login redirect", want: "Connection was redirected to a sign-in page. Use the model API URL and configure its credential.", handler: func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/login" {
				http.Redirect(w, r, "/login", http.StatusFound)
				return
			}
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, "<html>Please sign in</html>")
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(test.handler)
			defer server.Close()
			profile := config.Defaults(t.TempDir()).Servers[0]
			profile.BaseURL, profile.Model = server.URL, "fake"
			_, _, err := probe.Probe(context.Background(), &profile)
			if err == nil {
				t.Fatal("probe unexpectedly passed")
			}
			_, findings := failedProbeCapabilities(&profile, err)
			if len(findings) != 2 || findings[0] != "probe failed: "+test.want {
				t.Fatalf("findings=%v", findings)
			}
			if !strings.Contains(findings[1], "status ") || !strings.Contains(findings[1], "content_type") || !strings.Contains(findings[1], "prefix") {
				t.Fatalf("diagnostic metadata missing: %v", findings)
			}
			if test.name != "unauthorized" && !strings.Contains(findings[1], "invalid character") {
				t.Fatalf("decoder detail missing: %v", findings)
			}
		})
	}
}
