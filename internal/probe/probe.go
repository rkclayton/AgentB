package probe

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"harness/internal/config"
	"harness/internal/llm"
)

func Probe(ctx context.Context, profile *config.Profile) (config.Capabilities, []string, error) {
	if profile.ProbeMode == "off" {
		findings := []string{"probe mode off: all capabilities assumed", "server: assumed openai-compatible", "n_ctx: taken from n_ctx_override", "tokenize/apply-template/cached tokens/timings/prompt progress: assumed unavailable", "streaming: assumed available", "tool calls: assumed available", "overflow: assumed unknown"}
		caps := config.Capabilities{Server: "openai-compatible", NCtx: profile.Context.NCtxOverride, Streaming: true, ToolCalls: true, ReasoningControl: "none", ValidEfforts: []string{}, OverflowBehavior: "unknown", Findings: findings, ProbedAt: time.Now().UTC().Format(time.RFC3339)}
		return caps, findings, nil
	}
	caps := config.Capabilities{Server: "unknown", ReasoningControl: "none", OverflowBehavior: "unknown", ValidEfforts: []string{}}
	findings := []string{}
	client := llm.New(profile)

	check, cancel := context.WithTimeout(ctx, 20*time.Second)
	props, propsErr := client.Props(check)
	cancel()
	if propsErr == nil {
		caps.Props, caps.Server = true, "llama.cpp"
		caps.NCtx = props.DefaultGenerationSettings.NCtx
		if caps.NCtx == 0 {
			caps.NCtx = props.NCtx
		}
		findings = append(findings, fmt.Sprintf("props: available; server llama.cpp; n_ctx %d", caps.NCtx))
	} else {
		check, cancel = context.WithTimeout(ctx, 20*time.Second)
		models, modelsErr := client.Models(check)
		cancel()
		if modelsErr != nil {
			return profile.Capabilities, nil, fmt.Errorf("server identity: props: %v; models: %v", propsErr, modelsErr)
		}
		caps.Server = "openai-compatible"
		listed := false
		for _, model := range models {
			if model == profile.Model {
				listed = true
			}
		}
		if listed {
			findings = append(findings, "models: profile model listed")
		} else {
			findings = append(findings, "models: profile model not listed")
		}
	}

	check, cancel = context.WithTimeout(ctx, 20*time.Second)
	_, err := client.Tokenize(check, "The quick brown fox", false)
	cancel()
	caps.Tokenize = err == nil
	findings = append(findings, "tokenize: "+availability(caps.Tokenize))

	messages := []llm.Message{{Role: "system", Content: "Be concise."}, {Role: "user", Content: "Say OK."}}
	check, cancel = context.WithTimeout(ctx, 20*time.Second)
	prompt, err := client.ApplyTemplate(check, messages, nil)
	cancel()
	caps.ApplyTemplate = err == nil && prompt != ""
	findings = append(findings, "apply-template: "+availability(caps.ApplyTemplate))
	dummy := []any{map[string]any{"type": "function", "function": map[string]any{"name": "probe_tool", "description": "Probe.", "parameters": map[string]any{"type": "object", "properties": map[string]any{}}}}}
	check, cancel = context.WithTimeout(ctx, 20*time.Second)
	prompt, err = client.ApplyTemplate(check, messages, dummy)
	cancel()
	caps.ApplyTemplateTools = err == nil && strings.Contains(prompt, "probe_tool")
	findings = append(findings, "apply-template tools: "+availability(caps.ApplyTemplateTools))

	if profile.ProbeMode == "full" {
		check, cancel = context.WithTimeout(ctx, 20*time.Second)
		response, chatErr := client.Chat(check, llm.Request{Messages: []llm.Message{{Role: "user", Content: "Say OK."}}, MaxTokens: 16})
		cancel()
		if chatErr == nil {
			caps.CachedTokens = response.Usage.CachedTokens >= 0
			caps.Timings = response.Timings != nil
		}
		findings = append(findings, "cached tokens: "+availability(caps.CachedTokens), "timings: "+availability(caps.Timings))
	} else {
		findings = append(findings, "cached tokens: not probed in minimal mode; assumed unavailable", "timings: not probed in minimal mode; assumed unavailable")
	}

	check, cancel = context.WithTimeout(ctx, 20*time.Second)
	streamed, streamErr := client.ChatStream(check, llm.Request{Messages: []llm.Message{{Role: "user", Content: "Say OK."}}, MaxTokens: 16}, func(llm.Delta) {})
	cancel()
	caps.Streaming = streamErr == nil && streamed.Usage.PromptTokens > 0
	caps.PromptProgress = streamErr == nil && streamed.PromptProgress
	findings = append(findings, "streaming: "+availability(caps.Streaming), "prompt progress: "+availability(caps.PromptProgress))

	if profile.ProbeMode == "minimal" {
		caps.ToolCalls = true
		findings = append(findings, "tool calls: not probed in minimal mode; assumed available", "reasoning control: not probed in minimal mode; assumed none", "valid efforts: not probed in minimal mode; assumed empty", "overflow: not probed in minimal mode; assumed unknown")
		return finish(caps, findings)
	}

	check, cancel = context.WithTimeout(ctx, 20*time.Second)
	toolResponse, toolErr := client.Chat(check, llm.Request{Messages: []llm.Message{{Role: "user", Content: "read main.go"}}, Tools: []any{map[string]any{"type": "function", "function": map[string]any{"name": "read_file", "description": "Read a file.", "parameters": map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}, "required": []string{"path"}}}}}, ToolChoice: "required", MaxTokens: 512, Thinking: true})
	cancel()
	if toolErr == nil && len(toolResponse.ToolCalls) > 0 && toolResponse.ToolCalls[0].Function.Name == "read_file" {
		var args any
		caps.ToolCalls = json.Unmarshal([]byte(toolResponse.ToolCalls[0].Function.Arguments), &args) == nil
	}
	caps.GrammarConstrained = caps.Server == "llama.cpp" && caps.ToolCalls
	findings = append(findings, "tool calls: "+availability(caps.ToolCalls), "grammar constrained: "+availability(caps.GrammarConstrained))

	probeReasoning(ctx, client, profile, &caps, &findings)
	probeOverflow(ctx, client, profile, &caps, &findings)
	return finish(caps, findings)
}

func probeReasoning(ctx context.Context, client *llm.Client, profile *config.Profile, caps *config.Capabilities, findings *[]string) {
	present := func(body map[string]any) (bool, int) {
		check, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		raw, status, err := client.DoJSON(check, http.MethodPost, "/v1/chat/completions", body)
		if err != nil {
			return false, 0
		}
		var parsed struct {
			Choices []struct {
				Message struct {
					ReasoningContent string `json:"reasoning_content"`
					Reasoning        string `json:"reasoning"`
				} `json:"message"`
			} `json:"choices"`
		}
		_ = json.Unmarshal(raw, &parsed)
		return len(parsed.Choices) > 0 && (parsed.Choices[0].Message.ReasoningContent != "" || parsed.Choices[0].Message.Reasoning != ""), status
	}
	base := map[string]any{"model": profile.Model, "messages": []any{map[string]any{"role": "user", "content": "What is 17×23? Think briefly."}}, "max_tokens": 256, "temperature": .6, "stream": false}
	a, _ := present(clone(base))
	disabled := clone(base)
	disabled["chat_template_kwargs"] = map[string]any{"enable_thinking": false}
	b, _ := present(disabled)
	if a && !b {
		caps.ReasoningControl = "chat_template_kwargs"
	} else {
		top := clone(base)
		top["reasoning_effort"] = "low"
		_, status := present(top)
		if a && status == 200 {
			caps.ReasoningControl = "top_level"
		} else if a {
			caps.ReasoningControl = "server_flag"
		}
	}
	if caps.ReasoningControl == "chat_template_kwargs" {
		for _, effort := range []string{"low", "medium", "high", "xhigh"} {
			body := clone(base)
			body["chat_template_kwargs"] = map[string]any{"enable_thinking": true, "reasoning_effort": effort}
			_, status := present(body)
			if status == 200 {
				caps.ValidEfforts = append(caps.ValidEfforts, effort)
			}
		}
	} else if caps.ReasoningControl == "top_level" {
		for _, effort := range []string{"minimal", "low", "medium", "high"} {
			body := clone(base)
			body["reasoning_effort"] = effort
			_, status := present(body)
			if status == 200 {
				caps.ValidEfforts = append(caps.ValidEfforts, effort)
			}
		}
	}
	*findings = append(*findings, "reasoning control: "+caps.ReasoningControl, "valid efforts: "+strings.Join(caps.ValidEfforts, ", "))
}

func probeOverflow(ctx context.Context, client *llm.Client, profile *config.Profile, caps *config.Capabilities, findings *[]string) {
	parsed, _ := url.Parse(profile.BaseURL)
	loopback := parsed != nil && (parsed.Hostname() == "127.0.0.1" || parsed.Hostname() == "localhost" || parsed.Hostname() == "::1")
	if caps.NCtx == 0 || (!loopback && !caps.Props) {
		*findings = append(*findings, "overflow: not probed on a remote endpoint without /props; set capabilities.overflow_behavior by hand if you know it")
		return
	}
	text := strings.Repeat("abcd ", caps.NCtx+1024)
	check, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	raw, status, err := client.DoJSON(check, http.MethodPost, "/v1/chat/completions", map[string]any{"model": profile.Model, "messages": []any{map[string]any{"role": "user", "content": text}}, "max_tokens": 1})
	lower := strings.ToLower(string(raw))
	if err == nil && status >= 400 && (strings.Contains(lower, "context") || strings.Contains(lower, "token") || strings.Contains(lower, "length")) {
		caps.OverflowBehavior = "error"
	} else if err == nil && status == 200 {
		caps.OverflowBehavior = "truncate"
	}
	*findings = append(*findings, "overflow: "+caps.OverflowBehavior)
}

func finish(caps config.Capabilities, findings []string) (config.Capabilities, []string, error) {
	caps.ProbedAt = time.Now().UTC().Format(time.RFC3339)
	caps.Findings = findings
	return caps, findings, nil
}
func clone(source map[string]any) map[string]any {
	out := map[string]any{}
	for key, value := range source {
		out[key] = value
	}
	return out
}
func availability(value bool) string {
	if value {
		return "available"
	}
	return "unavailable"
}
