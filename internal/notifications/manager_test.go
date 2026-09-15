package notifications

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"harness/internal/events"
)

func TestTwoEventsProduceTwoHumanPostsWithoutDuplicates(t *testing.T) {
	var mu sync.Mutex
	var messages []string
	mentionsDisabled := true
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Content         string `json:"content"`
			AllowedMentions struct {
				Parse []string `json:"parse"`
			} `json:"allowed_mentions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		mu.Lock()
		messages = append(messages, body.Content)
		mentionsDisabled = mentionsDisabled && body.AllowedMentions.Parse != nil && len(body.AllowedMentions.Parse) == 0
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer receiver.Close()

	bus := events.NewBus()
	manager := New(bus, func(string) string { return "Build chat" }, "http://127.0.0.1:8790")
	manager.allowLocal = true
	if err := manager.Configure(receiver.URL); err != nil {
		t.Fatal(err)
	}
	manager.Start(context.Background())
	defer manager.Close()
	bus.Publish(events.New(events.ApprovalRequired, "s one", "r1", events.WithHuman(events.ApprovalRequired, map[string]any{"name": "shell"})))
	bus.Publish(events.New(events.RunStopped, "s one", "r1", events.WithHuman(events.RunStopped, map[string]any{"reason": "done"})))

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		count := len(messages)
		mu.Unlock()
		if count == 2 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(messages) != 2 {
		t.Fatalf("posts=%d messages=%q", len(messages), messages)
	}
	if !mentionsDisabled {
		t.Fatal("Discord allowed_mentions was not explicitly disabled")
	}
	if !strings.Contains(messages[0], "shell needs your approval") || !strings.Contains(messages[0], "http://127.0.0.1:8790/chat?session=s+one") {
		t.Fatalf("approval message=%q", messages[0])
	}
	if !strings.Contains(messages[1], "The run finished.") {
		t.Fatalf("stopped message=%q", messages[1])
	}
}

func TestChangingConfigurationCancelsWaitingRetry(t *testing.T) {
	bus := events.NewBus()
	manager := New(bus, nil, "http://127.0.0.1:8790")
	manager.allowLocal = true
	if err := manager.Configure("http://127.0.0.1:1/first"); err != nil {
		t.Fatal(err)
	}
	attempted := make(chan struct{}, 1)
	manager.client = roundTripFunc(func(*http.Request) (*http.Response, error) {
		attempted <- struct{}{}
		return nil, context.DeadlineExceeded
	})
	manager.wait = func(ctx context.Context, _ time.Duration) error {
		<-ctx.Done()
		return ctx.Err()
	}
	done := make(chan error, 1)
	go func() { done <- manager.deliver(context.Background(), events.RunStopped, "old event") }()
	<-attempted
	if err := manager.Configure("http://127.0.0.1:2/second"); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("delivery error=%v", err)
	}
	select {
	case <-attempted:
		t.Fatal("old event retried after its configured destination changed")
	default:
	}
}

func TestNoURLProducesNoPostsAndNoFailureEvent(t *testing.T) {
	bus := events.NewBus()
	stream, unsubscribe := bus.Subscribe()
	defer unsubscribe()
	manager := New(bus, nil, "http://127.0.0.1:8790")
	manager.Start(context.Background())
	defer manager.Close()
	bus.Publish(events.New(events.RunStopped, "s1", "r1", map[string]any{"reason": "done"}))
	time.Sleep(20 * time.Millisecond)
	for {
		select {
		case event := <-stream:
			if event.Type == events.NotificationFailed {
				t.Fatalf("unexpected failure event: %+v", event)
			}
		default:
			return
		}
	}
}

func TestFailureRetriesOnceAfterThirtySecondsAndLogsSafeReason(t *testing.T) {
	bus := events.NewBus()
	stream, unsubscribe := bus.Subscribe()
	defer unsubscribe()
	manager := New(bus, nil, "http://127.0.0.1:8790")
	manager.allowLocal = true
	if err := manager.Configure("http://127.0.0.1:1/hook-secret"); err != nil {
		t.Fatal(err)
	}
	attempts := 0
	manager.client = roundTripFunc(func(*http.Request) (*http.Response, error) {
		attempts++
		return nil, context.DeadlineExceeded
	})
	var waited time.Duration
	manager.wait = func(_ context.Context, delay time.Duration) error { waited = delay; return nil }
	err := manager.deliver(context.Background(), events.RunStopped, "message")
	if err == nil || attempts != 2 || waited != 30*time.Second {
		t.Fatalf("err=%v attempts=%d waited=%s", err, attempts, waited)
	}
	for {
		event := <-stream
		if event.Type != events.NotificationFailed {
			continue
		}
		encoded, _ := json.Marshal(event.Data)
		data := event.Data.(map[string]any)
		if strings.Contains(string(encoded), "hook-secret") || !strings.Contains(data["message"].(string), "Settings > Notifications") {
			t.Fatalf("failure data=%s", encoded)
		}
		break
	}
}

func TestWebhookGuardRejectsRedirectableOrLookalikeTargets(t *testing.T) {
	for _, raw := range []string{
		"http://discord.com/api/webhooks/1/token",
		"https://discord.com.evil.example/api/webhooks/1/token",
		"https://discord.com/login",
		"https://user:secret@discord.com/api/webhooks/1/token",
		"https://discord.com/api/webhooks/1/token?wait=true",
	} {
		if _, err := parseWebhook(raw, false); err == nil {
			t.Fatalf("accepted %q", raw)
		}
	}
	if _, err := parseWebhook("https://discord.com/api/webhooks/1/token", false); err != nil {
		t.Fatal(err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) Do(request *http.Request) (*http.Response, error) { return fn(request) }
