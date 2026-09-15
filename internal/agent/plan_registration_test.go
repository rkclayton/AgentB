package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"harness/internal/config"
	"harness/internal/events"
	"harness/internal/session"
	"harness/internal/tools"
)

func TestAddPlanSentenceUsesAllowThisAndApprovalControlsRegistration(t *testing.T) {
	for _, tc := range []struct {
		name, decision string
		wantRegistered bool
	}{{"approved", "once", true}, {"declined", "deny", false}} {
		t.Run(tc.name, func(t *testing.T) {
			var modelRequests atomic.Int32
			model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				modelRequests.Add(1)
				http.Error(w, "model request was not expected", http.StatusInternalServerError)
			}))
			defer model.Close()

			root, repo := t.TempDir(), filepath.Join(t.TempDir(), "repo")
			cfg := config.Defaults(root)
			profile := cfg.Servers[0]
			profile.ID, profile.BaseURL = "main", model.URL
			profile.Context.NCtx, profile.Context.ReserveOutput = 32768, 8192
			profile.Capabilities.Tokenize, profile.Capabilities.Streaming, profile.Capabilities.ToolCalls = true, true, true
			profile.Capabilities.OverflowBehavior = "error"
			cfg.Servers = []config.Profile{profile}
			eventCh := make(chan events.Event, 8)
			bus := events.NewBus()
			bus.SetSink(func(event events.Event) error {
				if event.Type == events.ApprovalRequired {
					eventCh <- event
				}
				return nil
			})
			repos := []string{}
			registrations := 0
			item := &session.Session{
				ID: "plan-chat", ServerID: profile.ID, Role: "b", Workspace: root, Runnable: true,
				Run: session.RunState{Status: "running", MaxTurns: 4}, ToolsEnabled: map[string]bool{},
				ToolCalls: map[string]int{}, SchemaTokens: map[string]int{}, MarginalTokens: map[string]int{},
				PlanRepos: func() []string { return append([]string(nil), repos...) },
				RegisterPlan: func(path string) (session.Plan, bool, error) {
					registrations++
					repos = append(repos, path)
					return session.Plan{ID: "alpha", Name: "repo", Repo: path}, true, nil
				},
			}
			runner := NewRunner(bus, tools.New(), &PromptRenderer{text: "system"}, func(id string) (*config.Profile, bool) { return &profile, id == profile.ID }, func() config.Config { return cfg })
			if _, err := runner.AddUser(context.Background(), item, "Add "+repo+" as a plan"); err != nil {
				t.Fatal(err)
			}
			type runResult struct{ reason, detail string }
			done := make(chan runResult, 1)
			go func() {
				reason, detail, _ := runner.Run(context.Background(), item, "run-plan")
				done <- runResult{reason, detail}
			}()
			var required events.Event
			select {
			case required = <-eventCh:
			case <-time.After(2 * time.Second):
				t.Fatal("timed out waiting for plan-registration approval")
			}
			data := required.Data.(map[string]any)
			args := data["args"].(map[string]any)
			if data["name"] != "plan registration" || data["boundary_escape"] != false || args["path"] != filepath.Clean(repo) {
				t.Fatalf("approval=%#v", data)
			}
			if err := runner.Gate().Decide(item.ID, data["call_id"].(string), tc.decision); err != nil {
				t.Fatal(err)
			}
			result := <-done
			if result.reason != "done" || (registrations == 1) != tc.wantRegistered {
				t.Fatalf("run=%+v registrations=%d", result, registrations)
			}
			if tc.wantRegistered {
				if root, err := item.WriteRoot(filepath.Join(repo, "now-allowed.txt")); err != nil || root != filepath.Clean(repo) {
					t.Fatalf("registered repo not immediately in union: root=%q err=%v", root, err)
				}
			} else if len(repos) != 0 {
				t.Fatalf("declined registration changed repos=%v", repos)
			}
			if got := modelRequests.Load(); got != 0 {
				t.Fatalf("model requests=%d", got)
			}
		})
	}
}
