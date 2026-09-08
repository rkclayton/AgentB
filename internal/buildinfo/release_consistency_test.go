package buildinfo

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestReleaseTagMatchesInstallerDisplayVersion(t *testing.T) {
	installer, err := os.ReadFile("../../scripts/install-Agent_b.ps1")
	if err != nil {
		t.Fatal(err)
	}
	match := regexp.MustCompile(`(?m)^\$displayVersion = '([^']+)'\r?$`).FindStringSubmatch(string(installer))
	if len(match) != 2 {
		t.Fatal("installer displayVersion not found")
	}
	if strings.TrimPrefix(Tag, "v") != match[1] {
		t.Fatalf("build tag %q != installer version %q", Tag, match[1])
	}
}
