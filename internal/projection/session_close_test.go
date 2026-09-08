package projection

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSessionClosePinReconstructsClosedStateChatAndRoleLabels(t *testing.T) {
	path := filepath.Join("testdata", "pins", "sources", "session-close.events")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := NewCache().ProjectFile(path, info.Size())
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.Complete || !snapshot.Closed || snapshot.AgentName != "Coder" || snapshot.BProfile != "Home API" || snapshot.CreatedAt == "" {
		t.Fatalf("lifecycle fields=%+v", snapshot)
	}
	if len(snapshot.Chat) < 3 || snapshot.Chat[0].Type != "user" || snapshot.Chat[1].AgentRole != "b" {
		t.Fatalf("chat=%+v", snapshot.Chat)
	}
	auxFound := false
	for _, entry := range snapshot.Chat {
		if entry.Type == "notice" && entry.AgentRole == "c" {
			auxFound = true
		}
	}
	if !auxFound {
		t.Fatalf("aux-produced notice label missing: %+v", snapshot.Chat)
	}
	if got := snapshot.Timeline[len(snapshot.Timeline)-1].Type; got != "session.closed" {
		t.Fatalf("last event=%q", got)
	}
}
