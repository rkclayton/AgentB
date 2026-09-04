package contextmgr

import "testing"

func TestReadFileRangesUseByteUnitsAndConfiguredDefault(t *testing.T) {
	const defaultLimit = 16 << 10
	older := `{"path":"source.go","offset":10000}`
	current := `{"path":"source.go","offset":20000,"limit":1}`
	if !supersedes("read_file", older, current, defaultLimit) {
		t.Fatal("configured default byte window should overlap the current byte")
	}
	if got := keyArgs("read_file", `{"path":"source.go"}`, defaultLimit); got != "source.go bytes 1–16384" {
		t.Fatalf("keyArgs=%q", got)
	}
}
