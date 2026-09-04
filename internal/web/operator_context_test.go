package web

import (
	"bufio"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"harness/internal/config"
	"harness/internal/events"
	"harness/internal/session"
	"harness/internal/tools"
)

type fakeOperatorTimer struct {
	clock   *fakeOperatorClock
	due     time.Time
	fn      func()
	stopped bool
	fired   bool
}

func (t *fakeOperatorTimer) Stop() bool {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	wasActive := !t.stopped && !t.fired
	t.stopped = true
	return wasActive
}

type fakeOperatorClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*fakeOperatorTimer
}

func newFakeOperatorClock() *fakeOperatorClock {
	return &fakeOperatorClock{now: time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)}
}

func (c *fakeOperatorClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeOperatorClock) After(duration time.Duration, fn func()) operatorTimer {
	c.mu.Lock()
	defer c.mu.Unlock()
	timer := &fakeOperatorTimer{clock: c, due: c.now.Add(duration), fn: fn}
	c.timers = append(c.timers, timer)
	return timer
}

func (c *fakeOperatorClock) Advance(duration time.Duration) {
	c.mu.Lock()
	target := c.now.Add(duration)
	for {
		var next *fakeOperatorTimer
		for _, timer := range c.timers {
			if timer.stopped || timer.fired || timer.due.After(target) || next != nil && !timer.due.Before(next.due) {
				continue
			}
			next = timer
		}
		if next == nil {
			c.now = target
			c.mu.Unlock()
			return
		}
		c.now = next.due
		next.fired = true
		fn := next.fn
		c.mu.Unlock()
		fn()
		c.mu.Lock()
	}
}

func (c *fakeOperatorClock) Fire(timer *fakeOperatorTimer) {
	timer.fn()
}

func useFakeOperatorClock(server *Server) *fakeOperatorClock {
	clock := newFakeOperatorClock()
	server.operatorNow = clock.Now
	server.operatorAfter = clock.After
	return clock
}

func operatorTestServer(t *testing.T) (*Server, *tools.Shell, string) {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, "harness.json")
	cfg := config.Defaults(root)
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	bus := events.NewBus()
	server := New(&cfg, path, root, bus)
	writers, err := events.NewWriters(filepath.Join(root, "logs"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = writers.Close() })
	server.SetRegistry(session.NewRegistry(bus, writers, server.Profile, cfg.Run.MaxTurns, server.ConfigSnapshot))
	shell := tools.NewShell(cfg.Shell)
	server.SetShellSecurity(nil, shell)
	return server, shell, path
}

func postOperatorContext(t *testing.T, server *Server, enabled bool) *httptest.ResponseRecorder {
	t.Helper()
	body := `{"shell":{"operator_context":false}}`
	if enabled {
		body = `{"shell":{"operator_context":true}}`
	}
	request := httptest.NewRequest(http.MethodPost, "/api/config", strings.NewReader(body))
	authorizeMutation(request, server)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	return response
}

func firstSSESnapshotOperatorContext(t *testing.T, server *Server) bool {
	t.Helper()
	host := httptest.NewServer(server.Handler())
	defer host.Close()
	response, err := host.Client().Get(host.URL + "/api/events")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	scanner := bufio.NewScanner(response.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var event events.Event
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event); err != nil {
			t.Fatal(err)
		}
		if event.Type != events.Snapshot {
			t.Fatalf("first SSE event type=%q, want snapshot", event.Type)
		}
		data, ok := event.Data.(map[string]any)
		if !ok {
			t.Fatalf("snapshot data=%T", event.Data)
		}
		identity, ok := data["shell_identity"].(map[string]any)
		if !ok {
			t.Fatalf("snapshot shell_identity=%T", data["shell_identity"])
		}
		value, _ := identity["operator_context"].(bool)
		return value
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	t.Fatal("SSE ended before snapshot")
	return false
}

func TestEverySSEConnectionStartsWithCurrentOperatorContext(t *testing.T) {
	server, _, _ := operatorTestServer(t)
	if firstSSESnapshotOperatorContext(t, server) {
		t.Fatal("initial SSE snapshot reported operator context on")
	}
	server.setOperatorContext(true, "test enable", 0)
	if !firstSSESnapshotOperatorContext(t, server) {
		t.Fatal("new SSE connection missed enabled operator context")
	}
	server.setOperatorContext(false, "test disable", 0)
	if firstSSESnapshotOperatorContext(t, server) {
		t.Fatal("new SSE connection missed disabled operator context")
	}
}

func TestOperatorContextEnableRequiresOperatorOwnedHTTPClient(t *testing.T) {
	server, shell, _ := operatorTestServer(t)
	server.operatorRequest = func(*http.Request) error { return errors.New("service identity") }

	response := postOperatorContext(t, server, true)
	if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "local process owned by") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
	if server.ConfigSnapshot().Shell.OperatorContext || shell.IdentityStatus().OperatorContext {
		t.Fatal("refused request enabled operator context")
	}
}

func TestServiceIdentityCannotDisableItsOwnSplit(t *testing.T) {
	server, _, _ := operatorTestServer(t)
	server.operatorRequest = func(*http.Request) error { return errors.New("service identity") }
	request := httptest.NewRequest(http.MethodPost, "/api/config", strings.NewReader(`{"shell":{"service_account":{"enabled":false}}}`))
	authorizeMutation(request, server)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "security-sensitive shell settings") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
}

func TestOperatorContextIsRuntimeOnlyAndExpiresAfterIdleTimeout(t *testing.T) {
	server, shell, path := operatorTestServer(t)
	server.operatorRequest = func(*http.Request) error { return nil }
	clock := useFakeOperatorClock(server)

	response := postOperatorContext(t, server, true)
	if response.Code != http.StatusOK {
		t.Fatalf("enable status=%d body=%s", response.Code, response.Body)
	}
	status := shell.IdentityStatus()
	if !status.OperatorContext || status.OperatorContextExpiresAt == "" {
		t.Fatalf("identity after enable=%+v", status)
	}
	loaded, _, _, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Shell.OperatorContext {
		t.Fatal("operator context persisted to config")
	}
	clock.Advance(20 * time.Minute)
	if shell.IdentityStatus().OperatorContext || server.ConfigSnapshot().Shell.OperatorContext {
		t.Fatal("operator context did not expire")
	}
	eventsSeen := server.bus.Recent("")
	var enabled, disabled bool
	for _, event := range eventsSeen {
		if event.Type != events.OperatorContext {
			continue
		}
		data, _ := event.Data.(map[string]any)
		if data["enabled"] == true {
			enabled = true
		}
		if data["enabled"] == false && data["reason"] == "idle timeout expired" {
			disabled = true
		}
	}
	if !enabled || !disabled {
		t.Fatalf("audit events enabled=%t disabled=%t events=%#v", enabled, disabled, eventsSeen)
	}
}

func TestOperatorActivityKeepsGrantActivePastFormerAbsoluteCeiling(t *testing.T) {
	server, _, _ := operatorTestServer(t)
	clock := useFakeOperatorClock(server)
	server.setOperatorContext(true, "test enable", 0)
	for range 7 {
		clock.Advance(10 * time.Minute)
		server.touchOperatorContext("idle window reset: tool execution completed")
	}
	if !server.ConfigSnapshot().Shell.OperatorContext {
		t.Fatal("sub-window tool activity did not keep operator context active past 60 minutes")
	}
	clock.Advance(20 * time.Minute)
	if server.ConfigSnapshot().Shell.OperatorContext {
		t.Fatal("operator context remained active after a full idle window")
	}
}

func TestLongExecutionLapsesAndCompletionDoesNotResurrectGrant(t *testing.T) {
	server, _, _ := operatorTestServer(t)
	clock := useFakeOperatorClock(server)
	server.setOperatorContext(true, "test enable", 0)
	server.touchOperatorContext("idle window reset: tool execution started")
	clock.Advance(21 * time.Minute)
	if server.ConfigSnapshot().Shell.OperatorContext {
		t.Fatal("long execution held operator context beyond the idle window")
	}
	server.touchOperatorContext("idle window reset: tool execution completed")
	if server.ConfigSnapshot().Shell.OperatorContext {
		t.Fatal("completion resurrected an expired operator grant")
	}
}

func TestManualRevokeDuringExecutionCannotBeResurrected(t *testing.T) {
	server, _, _ := operatorTestServer(t)
	useFakeOperatorClock(server)
	server.setOperatorContext(true, "test enable", 0)
	server.touchOperatorContext("idle window reset: tool execution started")
	server.setOperatorContext(false, "disabled by operator request", 0)
	server.touchOperatorContext("idle window reset: tool execution completed")
	if server.ConfigSnapshot().Shell.OperatorContext {
		t.Fatal("completion resurrected a manually revoked operator grant")
	}
}

func TestToolActivityWithoutGrantCreatesNoGrant(t *testing.T) {
	server, _, _ := operatorTestServer(t)
	clock := useFakeOperatorClock(server)
	server.touchOperatorContext("idle window reset: tool execution started")
	server.touchOperatorContext("idle window reset: tool execution completed")
	if server.ConfigSnapshot().Shell.OperatorContext || len(clock.timers) != 0 {
		t.Fatalf("activity created grant=%t timers=%d", server.ConfigSnapshot().Shell.OperatorContext, len(clock.timers))
	}
}

func TestStaleTimerCannotRevokeResetGrant(t *testing.T) {
	server, _, _ := operatorTestServer(t)
	clock := useFakeOperatorClock(server)
	server.setOperatorContext(true, "test enable", 0)
	first := clock.timers[0]
	clock.Advance(10 * time.Minute)
	server.touchOperatorContext("idle window reset: tool execution started")
	clock.Fire(first)
	if !server.ConfigSnapshot().Shell.OperatorContext {
		t.Fatal("stale pre-reset timer revoked the active grant")
	}
	clock.Advance(20 * time.Minute)
	if server.ConfigSnapshot().Shell.OperatorContext {
		t.Fatal("current timer did not revoke the idle grant")
	}
}

func TestBrowserAndConfigReadsDoNotResetIdleTimeout(t *testing.T) {
	server, _, _ := operatorTestServer(t)
	clock := useFakeOperatorClock(server)
	server.setOperatorContext(true, "test enable", 0)
	clock.Advance(19 * time.Minute)
	for _, path := range []string{"/api/state", "/api/config"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("GET %s status=%d", path, response.Code)
		}
	}
	if !firstSSESnapshotOperatorContext(t, server) {
		t.Fatal("SSE snapshot did not report active operator context")
	}
	clock.Advance(time.Minute)
	if server.ConfigSnapshot().Shell.OperatorContext {
		t.Fatal("browser, SSE, or config read reset the idle timeout")
	}
}

func TestOperatorContextPatchMustBeIsolatedAndTimeoutIsProtected(t *testing.T) {
	server, _, _ := operatorTestServer(t)
	server.operatorRequest = func(*http.Request) error { return nil }

	request := httptest.NewRequest(http.MethodPost, "/api/config", strings.NewReader(`{"shell":{"operator_context":true,"timeout_s":10}}`))
	authorizeMutation(request, server)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || server.ConfigSnapshot().Shell.OperatorContext {
		t.Fatalf("mixed patch status=%d body=%s", response.Code, response.Body)
	}

	request = httptest.NewRequest(http.MethodPost, "/api/config", strings.NewReader(`{"shell":{"operator_context_idle_timeout_minutes":1}}`))
	authorizeMutation(request, server)
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "protected configuration file") {
		t.Fatalf("timeout patch status=%d body=%s", response.Code, response.Body)
	}
}
