package agent

import (
	"context"
	"fmt"

	"harness/internal/config"
)

// Leave room for the assistant tool-call message and request framing that are
// not part of the pre-turn budget. An oversized byte window is retried rather
// than appended and allowed to make the next model turn impossible.
const toolResultContextMargin = 1024

func (r *Runner) fitWindowResult(
	ctx context.Context,
	profile *config.Profile,
	name string,
	args map[string]any,
	content string,
	ok bool,
	metadata map[string]any,
	resultTokens int,
	availableTokens int,
) (string, bool, map[string]any, int) {
	if !ok || (name != "read_file" && name != "fetch_url") || availableTokens < 1 || resultTokens <= availableTokens {
		return content, ok, metadata, resultTokens
	}

	cfg := r.cfg()
	defaultLimit := cfg.Tools.ReadFile.DefaultLimit
	if name == "fetch_url" {
		defaultLimit = cfg.Tools.Fetch.DefaultLimit
	}
	requestedLimit := integerArgument(args["limit"], defaultLimit)
	retryLimit := requestedLimit / 2
	if defaultLimit > 0 && retryLimit > defaultLimit {
		retryLimit = defaultLimit
	}
	if retryLimit < 1 {
		retryLimit = 1
	}
	offset := integerArgument(args["offset"], 1)

	bounded := fmt.Sprintf(
		"error: %s returned a window too large for the current model context (%d tokens; %d available before the output reserve). Retry %s with the same offset=%d and limit no greater than %d. Do not advance to next_offset until this window is read.",
		name, resultTokens, availableTokens, name, offset, retryLimit,
	)
	boundedTokens := r.textTokens(ctx, profile, bounded)
	boundedMetadata := cloneMetadata(metadata)
	boundedMetadata["result_too_large"] = true
	boundedMetadata["original_result_tokens"] = resultTokens
	boundedMetadata["result_token_limit"] = availableTokens
	boundedMetadata["retry_offset"] = offset
	boundedMetadata["retry_limit"] = retryLimit
	return bounded, false, boundedMetadata, boundedTokens
}

func integerArgument(value any, fallback int) int {
	switch number := value.(type) {
	case int:
		return number
	case float64:
		return int(number)
	default:
		return fallback
	}
}

func cloneMetadata(source map[string]any) map[string]any {
	clone := make(map[string]any, len(source)+5)
	for key, value := range source {
		clone[key] = value
	}
	return clone
}
