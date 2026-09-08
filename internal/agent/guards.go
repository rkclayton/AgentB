package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

type guardPair struct {
	argsHash, resultHash string
	callID               string
}
type runGuards struct {
	window     int
	maxErrors  int
	recent     []guardPair
	errorCount int
}

func newRunGuards(window, maxErrors int) *runGuards {
	return &runGuards{window: window, maxErrors: maxErrors}
}
func (g *runGuards) Observe(callID, name string, args map[string]any, result string, ok bool) (reason, detail, priorCallID string) {
	if !ok {
		g.errorCount++
	} else {
		g.errorCount = 0
	}
	argsJSON, _ := json.Marshal(args)
	argsHash := digest(name + string(argsJSON))
	resultHash := digest(result)
	if g.window > 0 {
		matches := 0
		priorCallID := ""
		for _, prior := range g.recent {
			if prior.argsHash == argsHash && prior.resultHash == resultHash {
				matches++
				priorCallID = prior.callID
			}
		}
		consecutive := len(g.recent) > 0 && g.recent[len(g.recent)-1].argsHash == argsHash && g.recent[len(g.recent)-1].resultHash == resultHash
		g.recent = append(g.recent, guardPair{argsHash: argsHash, resultHash: resultHash, callID: callID})
		if len(g.recent) > g.window {
			g.recent = append([]guardPair(nil), g.recent[len(g.recent)-g.window:]...)
		}
		if consecutive || matches >= 2 {
			return "cycle", name + " repeated with identical arguments and identical result", priorCallID
		}
	}
	if g.maxErrors > 0 && g.errorCount >= g.maxErrors {
		return "tool_errors", result, ""
	}
	return "", "", ""
}
func (g *runGuards) ResetCycle() { g.recent = nil }
func digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
