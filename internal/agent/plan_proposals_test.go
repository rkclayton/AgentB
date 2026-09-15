package agent

import (
	"fmt"
	"testing"

	"harness/internal/events"
)

func TestFakePlannerProposalCorpusParsesEveryContractKind(t *testing.T) {
	kinds := []string{"add", "reword", "reorder", "drop", "agent_b_addition"}
	parsed := 0
	for index, kind := range kinds {
		path := "plan.md"
		if kind == "agent_b_addition" {
			path = "AGENT_B.md"
		}
		body := fmt.Sprintf("Planner note.\n```agentb-plan-proposals\n{\"version\":1,\"proposals\":[{\"id\":\"p%d\",\"kind\":%q,\"path\":%q,\"old_text\":\"anchor\",\"new_text\":\"replacement\",\"item_id\":\"2t\",\"source_message_ids\":[\"m-1\"]}]}\n```", index, kind, path)
		visible, proposals := parsePlanProposals(body)
		if visible != "Planner note." || len(proposals) != 1 || proposals[0].Kind != kind {
			t.Fatalf("kind %s: visible=%q proposals=%+v", kind, visible, proposals)
		}
		parsed++
	}
	if parsed != len(kinds) {
		t.Fatalf("fake parse rate %d/%d", parsed, len(kinds))
	}
}

func TestProposalSettlementSourcesComeFromTheObservedTurn(t *testing.T) {
	proposals := []events.PlanProposal{{ID: "p", SourceMessageIDs: []string{"model-chosen", "older-turn"}}}
	messages := []events.Message{{ID: "u1", Role: "user"}, {ID: "a1", Role: "assistant"}, {ID: "u2", Role: "user"}}
	got := bindPlanProposalSources(messages, proposals, "a2")
	if fmt.Sprint(got[0].SourceMessageIDs) != "[u2 a2]" {
		t.Fatalf("sources=%v", got[0].SourceMessageIDs)
	}
}

func TestMalformedProposalBlockStaysVisibleAndInert(t *testing.T) {
	input := "```agentb-plan-proposals\n{not-json}\n```"
	visible, proposals := parsePlanProposals(input)
	if visible != input || len(proposals) != 0 {
		t.Fatalf("visible=%q proposals=%+v", visible, proposals)
	}
}
