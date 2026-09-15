package notifications

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"harness/internal/events"
)

const retryDelay = 30 * time.Second

type doer interface {
	Do(*http.Request) (*http.Response, error)
}

type State struct {
	Configured bool   `json:"configured"`
	Host       string `json:"host,omitempty"`
}

type Manager struct {
	bus        *events.Bus
	client     doer
	label      func(string) string
	baseURL    string
	allowLocal bool
	wait       func(context.Context, time.Duration) error

	mu             sync.RWMutex
	webhook        *url.URL
	webhookContext context.Context
	webhookCancel  context.CancelFunc
	cancel         context.CancelFunc
	unsub          func()
	started        bool
}

func New(bus *events.Bus, label func(string) string, baseURL string) *Manager {
	client := &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return errors.New("redirects are refused")
		},
	}
	return &Manager{
		bus: bus, client: client, label: label, baseURL: strings.TrimRight(baseURL, "/"),
		wait: func(ctx context.Context, delay time.Duration) error {
			timer := time.NewTimer(delay)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-timer.C:
				return nil
			}
		},
	}
}

func (m *Manager) Configure(raw string) error {
	value := strings.TrimSpace(raw)
	if value == "" {
		m.mu.Lock()
		if m.webhookCancel != nil {
			m.webhookCancel()
		}
		m.webhook = nil
		m.webhookContext, m.webhookCancel = nil, nil
		m.mu.Unlock()
		return nil
	}
	endpoint, err := parseWebhook(value, m.allowLocal)
	if err != nil {
		return err
	}
	m.mu.Lock()
	if m.webhookCancel != nil {
		m.webhookCancel()
	}
	m.webhook = endpoint
	m.webhookContext, m.webhookCancel = context.WithCancel(context.Background())
	m.mu.Unlock()
	return nil
}

func (m *Manager) Validate(raw string) error {
	_, err := parseWebhook(strings.TrimSpace(raw), m.allowLocal)
	return err
}

func (m *Manager) State() State {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.webhook == nil {
		return State{}
	}
	return State{Configured: true, Host: m.webhook.Hostname()}
}

func (m *Manager) Start(ctx context.Context) {
	m.mu.Lock()
	if m.started {
		m.mu.Unlock()
		return
	}
	stream, unsubscribe := m.bus.Subscribe()
	workerContext, cancel := context.WithCancel(ctx)
	m.started, m.unsub, m.cancel = true, unsubscribe, cancel
	m.mu.Unlock()
	jobs := make(chan events.Event, 128)
	go func() {
		defer close(jobs)
		for {
			select {
			case <-workerContext.Done():
				return
			case event, ok := <-stream:
				if !ok {
					return
				}
				if !notifiable(event.Type) || !m.State().Configured {
					continue
				}
				select {
				case jobs <- event:
				case <-workerContext.Done():
					return
				}
			}
		}
	}()
	go func() {
		for event := range jobs {
			_ = m.deliver(workerContext, event.Type, m.message(event))
		}
	}()
}

func (m *Manager) Close() {
	m.mu.Lock()
	if m.cancel != nil {
		m.cancel()
	}
	if m.unsub != nil {
		m.unsub()
	}
	m.cancel, m.unsub, m.started = nil, nil, false
	m.mu.Unlock()
}

func (m *Manager) SendTest(ctx context.Context) error {
	if !m.State().Configured {
		return errors.New("Discord webhook is not configured")
	}
	return m.deliver(ctx, "test", "Agent_b test notification.\nThe harness sent this from Settings without starting a run.\n"+m.baseURL)
}

func (m *Manager) deliver(ctx context.Context, eventType, message string) error {
	m.mu.RLock()
	endpoint, configuredContext := m.webhook, m.webhookContext
	m.mu.RUnlock()
	if endpoint == nil || configuredContext == nil {
		return errors.New("webhook is not configured")
	}
	deliveryContext, cancel := context.WithCancel(ctx)
	stopCancel := context.AfterFunc(configuredContext, cancel)
	defer func() { stopCancel(); cancel() }()
	err := m.post(deliveryContext, endpoint, message)
	if err == nil {
		return nil
	}
	if waitErr := m.wait(deliveryContext, retryDelay); waitErr != nil {
		return waitErr
	}
	err = m.post(deliveryContext, endpoint, message)
	if err == nil {
		return nil
	}
	safe := safeFailure(err)
	m.bus.Publish(events.New(events.NotificationFailed, "", "", map[string]any{
		"sink": "discord", "event_type": eventType, "attempts": 2,
		"message": "Discord notification failed after one retry: " + safe + "; check Settings > Notifications and the webhook destination",
	}))
	return fmt.Errorf("Discord notification failed after one retry: %s", safe)
}

func (m *Manager) post(ctx context.Context, endpoint *url.URL, message string) error {
	payload := struct {
		Content         string `json:"content"`
		AllowedMentions struct {
			Parse []string `json:"parse"`
		} `json:"allowed_mentions"`
	}{Content: message}
	payload.AllowedMentions.Parse = []string{}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return errors.New("could not prepare the webhook request")
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := m.client.Do(request)
	if err != nil {
		return errors.New("the webhook host could not be reached")
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("the webhook returned HTTP %d", response.StatusCode)
	}
	return nil
}

func (m *Manager) message(event events.Event) string {
	data, _ := event.Data.(map[string]any)
	notice := humanNotice(data, event.Type)
	label := "chat"
	if m.label != nil {
		if value := strings.TrimSpace(m.label(event.SessionID)); value != "" {
			label = value
		}
	}
	parts := []string{"Agent_b · " + label, notice.Happened, notice.HarnessAction}
	if notice.Question != "" {
		parts = append(parts, notice.Question)
	}
	if len(notice.Actions) > 0 {
		parts = append(parts, "Actions: "+strings.Join(notice.Actions, " / "))
	}
	link := m.baseURL
	if event.SessionID != "" {
		link += "/chat?session=" + url.QueryEscape(event.SessionID)
	}
	parts = append(parts, link)
	return strings.Join(parts, "\n")
}

func humanNotice(data map[string]any, eventType string) events.HumanNotice {
	if raw, ok := data["human"]; ok {
		encoded, _ := json.Marshal(raw)
		var notice events.HumanNotice
		if json.Unmarshal(encoded, &notice) == nil && notice.Happened != "" {
			return notice
		}
	}
	return events.HumanNoticeFor(eventType, data)
}

func notifiable(eventType string) bool {
	switch eventType {
	case events.ApprovalRequired, events.RunStopped, events.ItemDone, events.PlanDone:
		return true
	default:
		return false
	}
}

func parseWebhook(raw string, allowLocal bool) (*url.URL, error) {
	endpoint, err := url.Parse(raw)
	if err != nil || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return nil, errors.New("Discord URL must be one HTTPS webhook URL without credentials, query, or fragment")
	}
	host := strings.ToLower(endpoint.Hostname())
	if allowLocal && endpoint.Scheme == "http" && (host == "localhost" || net.ParseIP(host).IsLoopback()) {
		return endpoint, nil
	}
	if endpoint.Scheme != "https" || !(host == "discord.com" || host == "canary.discord.com" || host == "ptb.discord.com") || !strings.HasPrefix(endpoint.EscapedPath(), "/api/webhooks/") {
		return nil, errors.New("Discord URL must be an https://discord.com/api/webhooks/... endpoint")
	}
	return endpoint, nil
}

func safeFailure(err error) string {
	if err == nil {
		return "unknown failure"
	}
	return strings.TrimSpace(err.Error())
}
