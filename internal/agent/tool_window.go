package agent

import (
	"context"
	"fmt"
	"strings"

	"harness/internal/config"
	"harness/internal/session"
)

// Leave room for the assistant tool-call message and request framing that are
// not part of the pre-turn budget. An oversized byte window is retried rather
// than appended and allowed to make the next model turn impossible.
const toolResultContextMargin = 1024

func (r *Runner) fitWindowResult(
	ctx context.Context,
	s *session.Session,
	profile *config.Profile,
	name string,
	args map[string]any,
	content string,
	ok bool,
	metadata map[string]any,
	resultTokens int,
	availableTokens int,
	operatorContext bool,
) (string, bool, map[string]any, int) {
	if !ok || (name != "read_file" && name != "fetch_url" && name != "call_service") || availableTokens < 1 || resultTokens <= availableTokens {
		return content, ok, metadata, resultTokens
	}
	if name == "read_file" && s != nil {
		if clamped, clampedMetadata, clampedTokens, found := r.clampReadFileResult(ctx, s, profile, args, metadata, availableTokens, operatorContext); found {
			return clamped, true, clampedMetadata, clampedTokens
		}
	}

	cfg := r.cfg()
	defaultLimit := cfg.Tools.ReadFile.DefaultLimit
	if name == "fetch_url" {
		defaultLimit = cfg.Tools.Fetch.DefaultLimit
	} else if name == "call_service" {
		serviceName, _ := args["service"].(string)
		if service, found := cfg.Services[serviceName]; found {
			defaultLimit = service.MaxBodyKB << 10
		}
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

func (r *Runner) clampReadFileResult(ctx context.Context, s *session.Session, profile *config.Profile, args, metadata map[string]any, availableTokens int, operatorContext bool) (string, map[string]any, int, bool) {
	cfg := r.cfg().Tools.ReadFile
	field, unit, cursor := "limit", "bytes", "next_offset"
	requested := integerArgument(args[field], cfg.DefaultLimit)
	if _, lineMode := args["line"]; lineMode {
		field, unit, cursor = "lines", "lines", "next_line"
		requested = integerArgument(args[field], 200)
		requested = min(requested, 2000)
	} else {
		requested = min(requested, cfg.MaxLimit)
	}
	if requested < 1 {
		return "", nil, 0, false
	}

	low, high := 1, requested
	bestContent, bestTokens, bestLimit := "", 0, 0
	bestRemaining, bestNext := 0, 0
	for low <= high {
		limit := low + (high-low)/2
		candidateArgs := cloneMetadata(args)
		candidateArgs[field] = limit
		candidate, ok := r.repeatReadFile(ctx, s, candidateArgs, operatorContext)
		if !ok {
			return "", nil, 0, false
		}
		returned := headerInteger(candidate, unit)
		totalKey := "total"
		startKey := "offset"
		if unit == "lines" {
			totalKey, startKey = "total_lines", "line"
		}
		total := headerInteger(candidate, totalKey)
		start := headerInteger(candidate, startKey)
		next := headerInteger(candidate, cursor)
		remaining := max(0, total-(start-1+returned))
		note := fmt.Sprintf("[context clamp: requested_%s=%d returned_%s=%d remaining_%s=%d", unit, requested, unit, returned, unit, remaining)
		if next > 0 {
			note += fmt.Sprintf(" %s=%d", cursor, next)
		}
		note += "]\n"
		candidate = note + candidate
		tokens := r.textTokens(ctx, profile, candidate)
		if tokens <= availableTokens {
			bestContent, bestTokens, bestLimit = candidate, tokens, limit
			bestRemaining, bestNext = remaining, next
			low = limit + 1
		} else {
			high = limit - 1
		}
	}
	if bestLimit == 0 {
		return "", nil, 0, false
	}
	resultMetadata := cloneMetadata(metadata)
	resultMetadata["result_clamped"] = true
	resultMetadata["requested_"+field] = requested
	resultMetadata["returned_"+field] = bestLimit
	resultMetadata["remaining_"+unit] = bestRemaining
	if bestNext > 0 {
		resultMetadata[cursor] = bestNext
	}
	return bestContent, resultMetadata, bestTokens, true
}

func (r *Runner) repeatReadFile(ctx context.Context, s *session.Session, args map[string]any, operatorContext bool) (string, bool) {
	if operatorContext {
		return r.tools.CallAsOperator(ctx, s, "read_file", args)
	}
	outcome := r.tools.CallDetailed(ctx, s, "read_file", args)
	return outcome.Content, outcome.OK
}

func headerInteger(content, key string) int {
	header, _, _ := strings.Cut(content, "\n")
	for _, field := range strings.Fields(strings.Trim(header, "[]")) {
		name, value, found := strings.Cut(strings.TrimSuffix(field, "]"), "=")
		if found && name == key {
			var parsed int
			if _, err := fmt.Sscanf(value, "%d", &parsed); err == nil {
				return parsed
			}
		}
	}
	return 0
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
