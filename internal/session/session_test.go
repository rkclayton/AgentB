package session

import "testing"

func TestSnapshotCarriesToolCallCounts(t *testing.T) {
	s := &Session{
		ToolsEnabled: map[string]bool{"read_file": true},
		ToolCalls:    map[string]int{},
		SchemaTokens: map[string]int{"read_file": 42},
	}
	s.IncrementToolCall("read_file")
	s.IncrementToolCall("read_file")
	s.IncrementToolCall("unknown")
	snapshot := s.Snapshot(nil)
	if len(snapshot.Tools) != 1 || snapshot.Tools[0].Calls != 2 || snapshot.Tools[0].SchemaTokens != 42 {
		t.Fatalf("snapshot tools=%+v", snapshot.Tools)
	}
}
