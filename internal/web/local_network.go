package web

import (
	"fmt"
	"net"
	"net/netip"
	"sort"
	"strings"

	"harness/internal/events"
)

func detectLocalSubnets() []string {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	for _, item := range interfaces {
		if item.Flags&net.FlagUp == 0 || item.Flags&net.FlagLoopback != 0 {
			continue
		}
		addresses, err := item.Addrs()
		if err != nil {
			continue
		}
		for _, raw := range addresses {
			prefix, err := netip.ParsePrefix(raw.String())
			if err != nil || !allowedLANPrefix(prefix) {
				continue
			}
			seen[prefix.Masked().String()] = true
		}
	}
	result := make([]string, 0, len(seen))
	for value := range seen {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func allowedLANPrefix(prefix netip.Prefix) bool {
	if !prefix.IsValid() || !prefix.Addr().Is4() || prefix.Bits() < 8 || prefix.Bits() > 32 {
		return false
	}
	address := prefix.Masked().Addr()
	return address.IsPrivate() && !netip.MustParsePrefix("169.254.0.0/16").Contains(address) && !netip.MustParsePrefix("100.64.0.0/10").Contains(address)
}

func validateConfirmedLocalSubnets(values, detected []string) ([]string, error) {
	available := map[string]bool{}
	for _, value := range detected {
		available[value] = true
	}
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(value))
		if err != nil || !allowedLANPrefix(prefix) {
			return nil, fmt.Errorf("local subnet %q is not an allowed private IPv4 prefix", value)
		}
		canonical := prefix.Masked().String()
		if !available[canonical] {
			return nil, fmt.Errorf("local subnet %q was not detected on this machine", canonical)
		}
		if !seen[canonical] {
			seen[canonical] = true
			result = append(result, canonical)
		}
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("select at least one detected local subnet")
	}
	sort.Strings(result)
	return result, nil
}

func (s *Server) saveAppliedNetworkPolicy(enabled bool, subnets []string) error {
	s.mu.Lock()
	next := *s.cfg
	next.Shell.AllowLocalNetwork = enabled
	next.Shell.ConfirmedLocalSubnets = append([]string(nil), subnets...)
	if err := next.Save(s.configPath); err != nil {
		s.mu.Unlock()
		return err
	}
	s.cfg = &next
	masked := next.Masked()
	s.mu.Unlock()
	if s.runner != nil {
		s.runner.Configure(s.ConfigSnapshot())
	}
	s.bus.Publish(events.New(events.ConfigChanged, "", "", map[string]any{"config": masked}))
	return nil
}
