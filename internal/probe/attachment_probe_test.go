package probe

import (
	"context"
	"testing"

	"harness/internal/config"
)

func TestProbeOffReportsAssumedDocumentAndImageInput(t *testing.T) {
	profile := config.Defaults(t.TempDir()).Servers[0]
	profile.ProbeMode = "off"
	capabilities, findings, err := Probe(context.Background(), &profile)
	if err != nil {
		t.Fatal(err)
	}
	if !capabilities.DocumentInput || !capabilities.ImageInput || capabilities.Vision != config.VisionReadsImages {
		t.Fatalf("capabilities=%+v", capabilities)
	}
	if len(findings) == 0 {
		t.Fatal("probe recorded no findings")
	}
}
