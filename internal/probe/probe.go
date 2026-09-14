package probe

import (
	"bytes"
	"context"
	cryptorand "crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math/big"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"harness/internal/config"
	"harness/internal/llm"
)

func Probe(ctx context.Context, profile *config.Profile) (config.Capabilities, []string, error) {
	if profile.ProbeMode == "off" {
		findings := []string{"probe mode off: all capabilities assumed", "server: assumed openai-compatible", "n_ctx: taken from profile context", "tokenize/apply-template/cached tokens/timings/prompt progress: assumed unavailable", "streaming/tool calls/document input/image input: assumed available", "vision: reads images (assumed; probe mode off)", "overflow: assumed unknown"}
		caps := config.Capabilities{Server: "openai-compatible", NCtx: profile.Context.NCtx, Streaming: true, ToolCalls: true, DocumentInput: true, ImageInput: true, Vision: config.VisionReadsImages, ReasoningControl: "none", ValidEfforts: []string{}, OverflowBehavior: "unknown", Findings: findings, ProbedAt: time.Now().UTC().Format(time.RFC3339)}
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
			return profile.Capabilities, nil, connectionProbeError(propsErr, modelsErr)
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
	working := *profile
	working.Capabilities.Server = caps.Server
	client = llm.New(&working)

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
		caps.Vision = config.VisionRejected
		findings = append(findings, "tool calls: not probed in minimal mode; assumed available", "document input: not probed in minimal mode; assumed unavailable", "image input: not probed in minimal mode; assumed unavailable", "vision: rejected (assumed; not probed in minimal mode)", "reasoning control: not probed in minimal mode; assumed none", "valid efforts: not probed in minimal mode; assumed empty", "overflow: not probed in minimal mode; assumed unknown")
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

	caps.DocumentInput = probeContentPart(ctx, client, map[string]any{"type": "file", "file": map[string]any{"filename": "probe.pdf", "file_data": "data:application/pdf;base64," + base64.StdEncoding.EncodeToString(probePDF())}})
	caps.Vision, caps.ImageInput = probeVision(ctx, client)
	findings = append(findings, "document input: "+availability(caps.DocumentInput), "image input: "+availability(caps.ImageInput), "vision: "+caps.Vision)

	probeReasoning(ctx, client, profile, &caps, &findings)
	probeOverflow(ctx, client, profile, &caps, &findings)
	return finish(caps, findings)
}

type ConnectionError struct {
	Friendly string
	Detail   string
}

func (e *ConnectionError) Error() string           { return e.Friendly }
func (e *ConnectionError) OperatorMessage() string { return e.Friendly }
func (e *ConnectionError) Diagnostic() string      { return e.Detail }

func connectionProbeError(propsErr, modelsErr error) error {
	detail := fmt.Sprintf("server identity: props: %v; models: %v", propsErr, modelsErr)
	var shape *llm.ResponseShapeError
	if errors.As(modelsErr, &shape) || errors.As(propsErr, &shape) {
		lowerURL := strings.ToLower(shape.FinalURL)
		lowerType := strings.ToLower(shape.ContentType)
		lowerPrefix := strings.ToLower(shape.Prefix)
		switch {
		case shape.Status == http.StatusUnauthorized || shape.Status == http.StatusForbidden:
			return &ConnectionError{Friendly: "Connection requires credentials. Add the API credential, then Test again.", Detail: detail}
		case strings.Contains(lowerURL, "login") || strings.Contains(lowerURL, "signin") || strings.Contains(lowerPrefix, "sign in") || strings.Contains(lowerPrefix, "log in"):
			return &ConnectionError{Friendly: "Connection was redirected to a sign-in page. Use the model API URL and configure its credential.", Detail: detail}
		case strings.Contains(lowerType, "text/html") || strings.Contains(lowerPrefix, "<html") || strings.Contains(lowerPrefix, "<!doctype html"):
			return &ConnectionError{Friendly: "Connection returned a web page, not model API JSON. Add the API path to base_url.", Detail: detail}
		}
	}
	return &ConnectionError{Friendly: "Connection test failed. Check base_url and the model server.", Detail: detail}
}

func probePDF() []byte {
	objects := []string{
		"1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n",
		"2 0 obj\n<< /Type /Pages /Kids [3 0 R] /Count 1 >>\nendobj\n",
		"3 0 obj\n<< /Type /Page /Parent 2 0 R /MediaBox [0 0 10 10] /Contents 4 0 R >>\nendobj\n",
		"4 0 obj\n<< /Length 0 >>\nstream\n\nendstream\nendobj\n",
	}
	var value bytes.Buffer
	value.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objects))
	for index, object := range objects {
		offsets[index] = value.Len()
		value.WriteString(object)
	}
	xref := value.Len()
	fmt.Fprintf(&value, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for _, offset := range offsets {
		fmt.Fprintf(&value, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&value, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xref)
	return value.Bytes()
}

func probeContentPart(ctx context.Context, client *llm.Client, part map[string]any) bool {
	check, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	_, err := client.Chat(check, llm.Request{Messages: []llm.Message{{Role: "user", Content: []any{map[string]any{"type": "text", "text": "Say OK."}, part}}}, MaxTokens: 16})
	return err == nil
}

func probeVision(ctx context.Context, client *llm.Client) (string, bool) {
	chosen, err := cryptorand.Int(cryptorand.Reader, big.NewInt(10))
	if err != nil {
		return config.VisionRejected, false
	}
	digit := int(chosen.Int64())
	pngBytes := probeDigitPNG(digit)
	part := map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64," + base64.StdEncoding.EncodeToString(pngBytes)}}
	check, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	response, err := client.Chat(check, llm.Request{Messages: []llm.Message{{Role: "user", Content: []any{map[string]any{"type": "text", "text": "What single digit is shown in this image? Reply with the digit only."}, part}}}, MaxTokens: 8})
	if err != nil {
		return config.VisionRejected, false
	}
	if strings.TrimSpace(response.Content) == strconv.Itoa(digit) {
		return config.VisionReadsImages, true
	}
	return config.VisionAcceptsUnreadable, true
}

func probeDigitPNG(digit int) []byte {
	canvas := image.NewRGBA(image.Rect(0, 0, 96, 96))
	draw.Draw(canvas, canvas.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	segments := []image.Rectangle{
		image.Rect(24, 8, 72, 18), image.Rect(68, 14, 78, 47), image.Rect(68, 49, 78, 82),
		image.Rect(24, 78, 72, 88), image.Rect(18, 49, 28, 82), image.Rect(18, 14, 28, 47), image.Rect(24, 43, 72, 53),
	}
	masks := [...]byte{0x3f, 0x06, 0x5b, 0x4f, 0x66, 0x6d, 0x7d, 0x07, 0x7f, 0x6f}
	if digit < 0 || digit >= len(masks) {
		digit = 0
	}
	for index, rectangle := range segments {
		if masks[digit]&(1<<index) != 0 {
			draw.Draw(canvas, rectangle, image.NewUniform(color.Black), image.Point{}, draw.Src)
		}
	}
	var encoded bytes.Buffer
	_ = png.Encode(&encoded, canvas)
	return encoded.Bytes()
}

func probeReasoning(ctx context.Context, client *llm.Client, profile *config.Profile, caps *config.Capabilities, findings *[]string) {
	emission := func(body map[string]any) (string, int) {
		check, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		raw, status, err := client.DoJSON(check, http.MethodPost, "/v1/chat/completions", body)
		if err != nil {
			return "none", 0
		}
		var parsed struct {
			Choices []struct {
				Message struct {
					ReasoningContent string `json:"reasoning_content"`
					Reasoning        string `json:"reasoning"`
					Content          string `json:"content"`
				} `json:"message"`
			} `json:"choices"`
		}
		_ = json.Unmarshal(raw, &parsed)
		if len(parsed.Choices) == 0 {
			return "none", status
		}
		message := parsed.Choices[0].Message
		if message.ReasoningContent != "" || message.Reasoning != "" {
			return "structured", status
		}
		if strings.HasPrefix(strings.TrimSpace(message.Content), "<think>") {
			return "inline", status
		}
		return "none", status
	}
	base := map[string]any{"model": profile.Model, "messages": []any{map[string]any{"role": "user", "content": "What is 17×23? Think briefly."}}, "max_tokens": 256, "temperature": .6, "stream": false}
	shape, _ := emission(clone(base))
	caps.ReasoningEmission = shape
	disabled := clone(base)
	disabled["chat_template_kwargs"] = map[string]any{"enable_thinking": false}
	disabledShape, _ := emission(disabled)
	if shape == "structured" && disabledShape != "structured" {
		caps.ReasoningControl = "chat_template_kwargs"
	} else {
		top := clone(base)
		top["reasoning_effort"] = "low"
		_, status := emission(top)
		if shape == "structured" && status == 200 {
			caps.ReasoningControl = "top_level"
		} else if shape == "structured" {
			caps.ReasoningControl = "server_flag"
		}
	}
	if caps.ReasoningControl == "chat_template_kwargs" {
		for _, effort := range []string{"low", "medium", "high", "xhigh"} {
			body := clone(base)
			body["chat_template_kwargs"] = map[string]any{"enable_thinking": true, "reasoning_effort": effort}
			_, status := emission(body)
			// This template silently maps the unsupported "high" spelling instead of
			// rejecting it, so only the model's documented discrete efforts are valid.
			if status == 200 && effort != "high" {
				caps.ValidEfforts = append(caps.ValidEfforts, effort)
			}
		}
	} else if caps.ReasoningControl == "top_level" {
		for _, effort := range []string{"minimal", "low", "medium", "high"} {
			body := clone(base)
			body["reasoning_effort"] = effort
			_, status := emission(body)
			if status == 200 {
				caps.ValidEfforts = append(caps.ValidEfforts, effort)
			}
		}
	}
	emissionFinding := "reasoning emission: " + shape
	if shape == "inline" {
		emissionFinding += "; server fix: set llama-server --reasoning-format deepseek so thoughts are returned as reasoning_content"
	}
	*findings = append(*findings, emissionFinding, "reasoning control: "+caps.ReasoningControl, "valid efforts: "+strings.Join(caps.ValidEfforts, ", "))
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
