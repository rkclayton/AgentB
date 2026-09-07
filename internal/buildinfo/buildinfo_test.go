package buildinfo

import "testing"

func TestCurrentUsesInjectedCleanAndDirtyIdentity(t *testing.T) {
	oldCommit, oldDirty, oldTag := Commit, Dirty, Tag
	t.Cleanup(func() { Commit, Dirty, Tag = oldCommit, oldDirty, oldTag })
	Commit = "0123456789abcdef0123456789abcdef01234567"
	Tag = "v0.7.0"

	Dirty = "false"
	clean := Current()
	if clean.Tag != "v0.7.0" || clean.Display != "0123456789ab" || clean.Dirty || !clean.Known || clean.Source != "ldflags" {
		t.Fatalf("clean identity = %+v", clean)
	}

	Dirty = "true"
	dirty := Current()
	if dirty.Display != "0123456789ab+dirty" || !dirty.Dirty || !dirty.Known || dirty.Source != "ldflags" {
		t.Fatalf("dirty identity = %+v", dirty)
	}
}
