package web

import "time"

type reachabilityRetry struct {
	attempt int
	timer   operatorTimer
}

var reachabilityBackoff = [...]time.Duration{
	time.Second,
	2 * time.Second,
	4 * time.Second,
	8 * time.Second,
	15 * time.Second,
	30 * time.Second,
}

func (s *Server) scheduleReachabilityProbe(profileID string) {
	if profileID == "" {
		return
	}
	s.reachabilityMu.Lock()
	state := s.reachability[profileID]
	if state == nil {
		state = &reachabilityRetry{}
		s.reachability[profileID] = state
	}
	if state.timer == nil {
		s.scheduleReachabilityProbeLocked(profileID, state)
	}
	s.reachabilityMu.Unlock()
}

func (s *Server) scheduleReachabilityProbeLocked(profileID string, state *reachabilityRetry) {
	index := min(state.attempt, len(reachabilityBackoff)-1)
	delay := reachabilityBackoff[index]
	state.attempt++
	state.timer = s.reachabilityAfter(delay, func() {
		s.reachabilityMu.Lock()
		current := s.reachability[profileID]
		if current != state {
			s.reachabilityMu.Unlock()
			return
		}
		state.timer = nil
		s.reachabilityMu.Unlock()
		profile, ok := s.Profile(profileID)
		if !ok {
			s.clearReachabilityProbe(profileID)
			return
		}
		s.startProbe(profile)
	})
}

func (s *Server) cancelScheduledReachabilityProbe(profileID string) {
	s.reachabilityMu.Lock()
	if state := s.reachability[profileID]; state != nil && state.timer != nil {
		state.timer.Stop()
		state.timer = nil
	}
	s.reachabilityMu.Unlock()
}

func (s *Server) completeReachabilityProbe(profileID string, succeeded bool) {
	s.reachabilityMu.Lock()
	state := s.reachability[profileID]
	if state == nil {
		s.reachabilityMu.Unlock()
		return
	}
	if succeeded {
		if state.timer != nil {
			state.timer.Stop()
		}
		delete(s.reachability, profileID)
	} else if state.timer == nil {
		s.scheduleReachabilityProbeLocked(profileID, state)
	}
	s.reachabilityMu.Unlock()
}

func (s *Server) clearReachabilityProbe(profileID string) {
	s.reachabilityMu.Lock()
	if state := s.reachability[profileID]; state != nil && state.timer != nil {
		state.timer.Stop()
	}
	delete(s.reachability, profileID)
	s.reachabilityMu.Unlock()
}
