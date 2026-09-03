package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"harness/internal/config"
)

type Client struct {
	profile *config.Profile
	http    *http.Client
}

func New(profile *config.Profile) *Client {
	return &Client{profile: profile, http: &http.Client{Timeout: time.Duration(profile.RequestTimeoutS) * time.Second}}
}

func (c *Client) Chat(ctx context.Context, request Request) (Response, error) {
	body := buildRequest(c.profile, request, false)
	start := time.Now()
	raw, status, err := c.DoJSON(ctx, http.MethodPost, "/v1/chat/completions", body)
	if err != nil {
		return Response{}, err
	}
	if status != 200 {
		return Response{}, fmt.Errorf("chat HTTP %d: %s", status, raw)
	}
	result, err := parseResponse(raw)
	result.DurationMS = time.Since(start).Milliseconds()
	return result, err
}

func (c *Client) ChatStream(ctx context.Context, request Request, onDelta func(Delta)) (Response, error) {
	body := buildRequest(c.profile, request, true)
	encoded, _ := json.Marshal(body)
	req, err := c.newRequest(ctx, http.MethodPost, "/v1/chat/completions", bytes.NewReader(encoded))
	if err != nil {
		return Response{}, err
	}
	start := time.Now()
	response, err := c.http.Do(req)
	if err != nil {
		return Response{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		raw, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		return Response{}, fmt.Errorf("chat stream HTTP %d: %s", response.StatusCode, raw)
	}
	result := Response{Usage: Usage{CachedTokens: -1}}
	var rawStream bytes.Buffer
	arguments := map[int]*strings.Builder{}
	calls := map[int]*ToolCall{}
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") || line == "data: [DONE]" {
			continue
		}
		rawStream.WriteString(line)
		rawStream.WriteByte('\n')
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content          string `json:"content"`
					ReasoningContent string `json:"reasoning_content"`
					Reasoning        string `json:"reasoning"`
					ToolCalls        []struct {
						Index    int          `json:"index"`
						ID       string       `json:"id"`
						Type     string       `json:"type"`
						Function FunctionCall `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
			Usage *struct {
				PromptTokens        int `json:"prompt_tokens"`
				CompletionTokens    int `json:"completion_tokens"`
				PromptTokensDetails *struct {
					CachedTokens int `json:"cached_tokens"`
				} `json:"prompt_tokens_details"`
			} `json:"usage"`
			Timings        Timings `json:"timings"`
			PromptProgress *struct {
				Total     int `json:"total"`
				Cache     int `json:"cache"`
				Processed int `json:"processed"`
			} `json:"prompt_progress"`
		}
		if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &chunk) != nil {
			continue
		}
		if chunk.Usage != nil {
			result.Usage.PromptTokens = chunk.Usage.PromptTokens
			result.Usage.CompletionTokens = chunk.Usage.CompletionTokens
			if chunk.Usage.PromptTokensDetails != nil {
				result.Usage.CachedTokens = chunk.Usage.PromptTokensDetails.CachedTokens
			}
		}
		if chunk.Timings != nil {
			result.Timings = chunk.Timings
		}
		if chunk.PromptProgress != nil {
			result.PromptProgress = true
			onDelta(Delta{Kind: "progress", Total: chunk.PromptProgress.Total, Cache: chunk.PromptProgress.Cache, Processed: chunk.PromptProgress.Processed})
		}
		for _, choice := range chunk.Choices {
			if choice.Delta.ReasoningContent != "" {
				result.Reasoning += choice.Delta.ReasoningContent
				onDelta(Delta{Kind: "reasoning", Text: choice.Delta.ReasoningContent})
			} else if choice.Delta.Reasoning != "" {
				result.Reasoning += choice.Delta.Reasoning
				onDelta(Delta{Kind: "reasoning", Text: choice.Delta.Reasoning})
			}
			if choice.Delta.Content != "" {
				result.Content += choice.Delta.Content
				onDelta(Delta{Kind: "content", Text: choice.Delta.Content})
			}
			for _, part := range choice.Delta.ToolCalls {
				call := calls[part.Index]
				if call == nil {
					call = &ToolCall{}
					calls[part.Index] = call
					arguments[part.Index] = &strings.Builder{}
				}
				if part.ID != "" {
					call.ID = part.ID
				}
				if part.Type != "" {
					call.Type = part.Type
				}
				if part.Function.Name != "" {
					call.Function.Name = part.Function.Name
				}
				if part.Function.Arguments != "" {
					arguments[part.Index].WriteString(part.Function.Arguments)
					onDelta(Delta{Kind: "tool_call", Index: part.Index, Text: part.Function.Arguments})
				}
			}
			if choice.FinishReason != "" {
				result.FinishReason = choice.FinishReason
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return Response{}, err
	}
	for index := 0; index < len(calls); index++ {
		call := calls[index]
		call.Function.Arguments = arguments[index].String()
		result.ToolCalls = append(result.ToolCalls, *call)
	}
	result.DurationMS = time.Since(start).Milliseconds()
	result.Raw = rawStream.Bytes()
	return result, nil
}

func parseResponse(raw []byte) (Response, error) {
	var parsed chatResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return Response{}, err
	}
	if len(parsed.Choices) == 0 {
		return Response{}, fmt.Errorf("response has no choices")
	}
	m := parsed.Choices[0].Message
	reasoning := m.ReasoningContent
	if reasoning == "" {
		reasoning = m.Reasoning
	}
	cached := -1
	if parsed.Usage.PromptTokensDetails != nil {
		cached = parsed.Usage.PromptTokensDetails.CachedTokens
	}
	return Response{Content: contentString(m.Content), Reasoning: reasoning, ToolCalls: m.ToolCalls, FinishReason: parsed.Choices[0].FinishReason, Usage: Usage{PromptTokens: parsed.Usage.PromptTokens, CompletionTokens: parsed.Usage.CompletionTokens, CachedTokens: cached}, Timings: parsed.Timings, Raw: append(json.RawMessage(nil), raw...)}, nil
}
func contentString(value any) string {
	if s, ok := value.(string); ok {
		return s
	}
	data, _ := json.Marshal(value)
	return string(data)
}

func (c *Client) DoJSON(ctx context.Context, method, path string, body any) ([]byte, int, error) {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, 0, err
		}
		reader = bytes.NewReader(data)
	}
	req, err := c.newRequest(ctx, method, path, reader)
	if err != nil {
		return nil, 0, err
	}
	response, err := c.http.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 64<<20))
	return raw, response.StatusCode, err
}
func (c *Client) newRequest(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.profile.BaseURL, "/")+path, body)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.profile.APIKey != "" {
		value := c.profile.APIKey
		if !strings.HasPrefix(value, "Bearer ") {
			value = "Bearer " + value
		}
		req.Header.Set("Authorization", value)
	}
	return req, nil
}

func (c *Client) Tokenize(ctx context.Context, text string, addSpecial bool) (int, error) {
	raw, status, err := c.DoJSON(ctx, http.MethodPost, "/tokenize", map[string]any{"content": text, "add_special": addSpecial, "parse_special": true})
	if err != nil {
		return 0, err
	}
	if status != 200 {
		return 0, fmt.Errorf("tokenize HTTP %d", status)
	}
	var out struct {
		Tokens []any `json:"tokens"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return 0, err
	}
	return len(out.Tokens), nil
}
func (c *Client) ApplyTemplate(ctx context.Context, messages []Message, tools []any) (string, error) {
	body := map[string]any{"messages": messages}
	if tools != nil {
		body["tools"] = tools
	}
	raw, status, err := c.DoJSON(ctx, http.MethodPost, "/apply-template", body)
	if err != nil {
		return "", err
	}
	if status != 200 {
		return "", fmt.Errorf("apply-template HTTP %d", status)
	}
	var out struct {
		Prompt string `json:"prompt"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", err
	}
	return out.Prompt, nil
}
func (c *Client) Props(ctx context.Context) (Props, error) {
	raw, status, err := c.DoJSON(ctx, http.MethodGet, "/props", nil)
	if err != nil {
		return Props{}, err
	}
	if status != 200 {
		return Props{}, fmt.Errorf("props HTTP %d", status)
	}
	var out Props
	err = json.Unmarshal(raw, &out)
	return out, err
}
func (c *Client) Models(ctx context.Context) ([]string, error) {
	raw, status, err := c.DoJSON(ctx, http.MethodGet, "/v1/models", nil)
	if err != nil {
		return nil, err
	}
	if status != 200 {
		return nil, fmt.Errorf("models HTTP %d", status)
	}
	var out struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	values := make([]string, 0, len(out.Data))
	for _, v := range out.Data {
		values = append(values, v.ID)
	}
	return values, nil
}
