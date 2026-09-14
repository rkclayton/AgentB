package web

import (
	"net/netip"
	"reflect"
	"testing"
)

func TestValidateConfirmedLocalSubnetsRequiresDetectedPrivatePrefixes(t *testing.T) {
	detected := []string{"10.20.0.0/16", "192.168.50.0/24"}
	got, err := validateConfirmedLocalSubnets([]string{"192.168.50.0/24", "10.20.0.0/16"}, detected)
	if err != nil || !reflect.DeepEqual(got, detected) {
		t.Fatalf("confirmed=%v err=%v", got, err)
	}
	for _, values := range [][]string{{}, {"192.168.51.0/24"}, {"169.254.0.0/16"}, {"127.0.0.0/8"}, {"100.64.0.0/10"}} {
		if _, err := validateConfirmedLocalSubnets(values, detected); err == nil {
			t.Fatalf("unsafe/unconfirmed subnet accepted: %v", values)
		}
	}
}

func TestAllowedLANPrefixIsIPv4RFC1918Only(t *testing.T) {
	for _, raw := range []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.1.0/24"} {
		if !allowedLANPrefix(netip.MustParsePrefix(raw)) {
			t.Fatalf("private prefix refused: %s", raw)
		}
	}
	for _, raw := range []string{"127.0.0.0/8", "169.254.0.0/16", "100.64.0.0/10", "fd00::/8"} {
		if allowedLANPrefix(netip.MustParsePrefix(raw)) {
			t.Fatalf("non-LAN prefix accepted: %s", raw)
		}
	}
}
