package agent

import (
	"testing"

	"harness/internal/config"
)

func TestCompactionUsesHighWatermarkAndSkipsColdPrefill(t *testing.T) {
	cfg := config.GlobalContext{SoftPct: .75, SummaryPct: .85}
	if shouldBatchElide(8400, 10000, cfg, false) {
		t.Fatal("elided below high watermark")
	}
	if !shouldBatchElide(8500, 10000, cfg, false) {
		t.Fatal("did not elide at high watermark")
	}
	if shouldBatchElide(9000, 10000, cfg, true) {
		t.Fatal("elided immediately after cold prefill")
	}
}
