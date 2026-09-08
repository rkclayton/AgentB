package web

import (
	"context"
	"testing"

	"harness/internal/config"
	"harness/internal/events"
)

func TestStopAllCancelsEveryInFlightProbe(t *testing.T) {
	cfg := config.Defaults(t.TempDir())
	server := New(&cfg, "", "", RuntimeRoots{}, events.NewBus())
	firstContext, firstCancel := context.WithCancel(context.Background())
	secondContext, secondCancel := context.WithCancel(context.Background())
	server.probeCancels["first"] = &probeRun{cancel: firstCancel}
	server.probeCancels["second"] = &probeRun{cancel: secondCancel}
	server.cancelProbes("", true)
	for name, ctx := range map[string]context.Context{"first": firstContext, "second": secondContext} {
		select {
		case <-ctx.Done():
		default:
			t.Fatalf("%s probe was not canceled", name)
		}
	}
}

func TestFinishingOldProbeCannotDropNewProbeCancellation(t *testing.T) {
	cfg := config.Defaults(t.TempDir())
	server := New(&cfg, "", "", RuntimeRoots{}, events.NewBus())
	old := &probeRun{cancel: func() {}}
	current := &probeRun{cancel: func() {}}
	server.probeCancels["local"] = current
	server.clearProbe("local", old)
	if server.probeCancels["local"] != current {
		t.Fatal("old probe completion removed the current probe")
	}
}
