package llm

import "harness/internal/config"

func buildRequest(profile *config.Profile, request Request, stream bool) map[string]any {
	sampling := profile.Sampling.Nonthinking
	if request.Thinking {
		sampling = profile.Sampling.Thinking
	}
	body := map[string]any{"model": profile.Model, "messages": request.Messages, "temperature": sampling.Temperature, "top_p": sampling.TopP, "presence_penalty": sampling.PresencePenalty, "max_tokens": request.MaxTokens, "stream": stream}
	if request.MaxTokens == 0 {
		body["max_tokens"] = 8192
	}
	if len(request.Tools) > 0 {
		body["tools"] = request.Tools
		if request.ToolChoice != nil {
			body["tool_choice"] = request.ToolChoice
		}
	}
	if stream {
		body["stream_options"] = map[string]any{"include_usage": true}
	}
	if profile.Capabilities.Server == "llama.cpp" {
		body["top_k"] = sampling.TopK
		body["min_p"] = sampling.MinP
		body["repeat_penalty"] = sampling.RepeatPenalty
		body["cache_prompt"] = true
		body["return_progress"] = true
	}
	control := profile.Reasoning.Control
	if control == "auto" {
		control = profile.Capabilities.ReasoningControl
	}
	effortAllowed := len(profile.Reasoning.ValidEfforts) > 0 && contains(profile.Reasoning.ValidEfforts, profile.Reasoning.Effort)
	switch control {
	case "chat_template_kwargs":
		kwargs := map[string]any{"enable_thinking": profile.Reasoning.Enabled, "preserve_thinking": profile.Reasoning.Preserve}
		if effortAllowed {
			kwargs["reasoning_effort"] = profile.Reasoning.Effort
		}
		body["chat_template_kwargs"] = kwargs
	case "top_level":
		if effortAllowed {
			body["reasoning_effort"] = profile.Reasoning.Effort
		}
	}
	return body
}

func BuildRequest(profile *config.Profile, request Request, stream bool) map[string]any {
	return buildRequest(profile, request, stream)
}
func contains(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}
