package web

import (
	"testing"
	"time"
)

type reachabilityTestTimer struct{ stopped bool }

func (timer *reachabilityTestTimer) Stop() bool {
	wasActive := !timer.stopped
	timer.stopped = true
	return wasActive
}

func TestReachabilityProbeBackoffIsBoundedAndStopsOnSuccess(t *testing.T) {
	server := &Server{reachability: map[string]*reachabilityRetry{}}
	var delays []time.Duration
	var timers []*reachabilityTestTimer
	server.reachabilityAfter = func(delay time.Duration, _ func()) operatorTimer {
		timer := &reachabilityTestTimer{}
		delays = append(delays, delay)
		timers = append(timers, timer)
		return timer
	}

	server.scheduleReachabilityProbe("local")
	server.scheduleReachabilityProbe("local")
	if len(delays) != 1 || delays[0] != time.Second {
		t.Fatalf("initial delays=%v", delays)
	}
	for index := 0; index < len(reachabilityBackoff)+2; index++ {
		server.cancelScheduledReachabilityProbe("local")
		server.completeReachabilityProbe("local", false)
	}
	if got := delays[len(delays)-1]; got != 30*time.Second {
		t.Fatalf("backoff did not cap at 30s: %v", delays)
	}
	server.completeReachabilityProbe("local", true)
	if _, ok := server.reachability["local"]; ok {
		t.Fatal("successful probe left retry state active")
	}
	if !timers[len(timers)-1].stopped {
		t.Fatal("successful probe did not stop pending retry")
	}
}
