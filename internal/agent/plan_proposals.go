package agent

import (
	"encoding/json"
	"strings"

	"harness/internal/events"
)

const planProposalFence = "```agentb-plan-proposals"

type planProposalEnvelope struct {
	Version   int                   `json:"version"`
	Proposals []events.PlanProposal `json:"proposals"`
}

// parsePlanProposals removes only well-formed version-one proposal blocks.
// Malformed model output remains ordinary visible prose and cannot enter the tray.
func parsePlanProposals(content string) (string, []events.PlanProposal) {
	visible := content
	proposals := []events.PlanProposal{}
	for {
		start := strings.Index(visible, planProposalFence)
		if start < 0 {
			break
		}
		bodyStart := start + len(planProposalFence)
		if bodyStart < len(visible) && visible[bodyStart] == '\r' {
			bodyStart++
		}
		if bodyStart >= len(visible) || visible[bodyStart] != '\n' {
			break
		}
		bodyStart++
		endOffset := strings.Index(visible[bodyStart:], "\n```")
		if endOffset < 0 {
			break
		}
		end := bodyStart + endOffset
		var envelope planProposalEnvelope
		if json.Unmarshal([]byte(visible[bodyStart:end]), &envelope) != nil || envelope.Version != 1 || len(envelope.Proposals) == 0 {
			break
		}
		valid := true
		for _, proposal := range envelope.Proposals {
			if proposal.ID == "" || proposal.Kind == "" || proposal.Path == "" || proposal.OldText == "" || proposal.ItemID == "" {
				valid = false
				break
			}
		}
		if !valid {
			break
		}
		proposals = append(proposals, envelope.Proposals...)
		visible = visible[:start] + visible[end+4:]
	}
	return strings.TrimSpace(visible), proposals
}

// bindPlanProposalSources makes settlement an observed-transcript decision, not
// a model-controlled list of arbitrary message ids.
func bindPlanProposalSources(messages []events.Message, proposals []events.PlanProposal, assistantID string) []events.PlanProposal {
	userID := ""
	for index := len(messages) - 1; index >= 0; index-- {
		if messages[index].Role == "user" && !messages[index].Elided {
			userID = messages[index].ID
			break
		}
	}
	for index := range proposals {
		proposals[index].SourceMessageIDs = proposals[index].SourceMessageIDs[:0]
		if userID != "" {
			proposals[index].SourceMessageIDs = append(proposals[index].SourceMessageIDs, userID)
		}
		proposals[index].SourceMessageIDs = append(proposals[index].SourceMessageIDs, assistantID)
	}
	return proposals
}
