package agent

import (
	"strings"
	"testing"

	"harness/internal/delivery"
)

func TestTurnCeilingDetailReportsProgressDeliveryAndContinuation(t *testing.T) {
	detail := turnCeilingDetail(417, delivery.Result{Items: []delivery.Item{{
		SourcePath: "reports/final.txt", DeliveredPath: `C:\Users\operator\Agent_b\final.txt`, Status: "copied",
	}}})
	for _, want := range []string{"completed 417 turns", "reports/final.txt copied to", "send Continue in this chat", "session history and delivered work are retained"} {
		if !strings.Contains(detail, want) {
			t.Fatalf("detail %q does not contain %q", detail, want)
		}
	}
}
