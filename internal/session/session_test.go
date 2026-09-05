package session

import "testing"

func TestSnapshotCarriesToolCallCounts(t *testing.T) {
	s := &Session{
		ToolsEnabled:   map[string]bool{"read_file": true},
		ToolCalls:      map[string]int{},
		SchemaTokens:   map[string]int{"read_file": 42},
		MarginalTokens: map[string]int{"read_file": 17},
	}
	s.IncrementToolCall("read_file")
	s.IncrementToolCall("read_file")
	s.IncrementToolCall("unknown")
	snapshot := s.Snapshot(nil)
	if len(snapshot.Tools) != 1 || snapshot.Tools[0].Calls != 2 || snapshot.Tools[0].SchemaTokens != 42 || snapshot.Tools[0].MarginalTokens != 17 {
		t.Fatalf("snapshot tools=%+v", snapshot.Tools)
	}
}

func TestSnapshotCarriesRunAggregates(t *testing.T) {
	s := &Session{}
	s.RecordModelTurn()
	s.RecordModelTurn()
	s.RecordCompaction(-123)
	snapshot := s.Snapshot(nil)
	if snapshot.ModelTurns != 2 || snapshot.CompactionCount != 1 || snapshot.CompactionTokenDelta != -123 {
		t.Fatalf("snapshot aggregates=%+v", snapshot)
	}
}
