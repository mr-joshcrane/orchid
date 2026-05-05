package orchid

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/mr-joshcrane/orchid/internal/overflow"
)

type OpenAIGenerator struct {
	endpoint       string
	model          string
	bearerToken    string
	retryConfig    *overflow.RetryConfig
	overflowPolicy overflow.OverflowPolicy
}

type OpenAIOption func(*OpenAIGenerator)

func OpenAIWithModel(model string) OpenAIOption {
	return func(g *OpenAIGenerator) { g.model = model }
}

func OpenAIWithBearerToken(token string) OpenAIOption {
	return func(g *OpenAIGenerator) { g.bearerToken = token }
}

func OpenAIWithRetry(attempts int, backoff overflow.BackoffFunc) OpenAIOption {
	return func(g *OpenAIGenerator) {
		g.retryConfig = &overflow.RetryConfig{Attempts: attempts, Backoff: backoff}
	}
}

func OpenAIWithOverflowPolicy(p overflow.OverflowPolicy) OpenAIOption {
	return func(g *OpenAIGenerator) {
		g.overflowPolicy = p
	}
}

func NewOpenAIGenerator(endpoint string, opts ...OpenAIOption) *OpenAIGenerator {
	g := &OpenAIGenerator{endpoint: endpoint}
	for _, opt := range opts {
		opt(g)
	}
	return g
}

func (g *OpenAIGenerator) Model() string { return g.model }

func (g *OpenAIGenerator) ModelLimit() int {
	return lookupModelLimit(g.model)
}

func (g *OpenAIGenerator) EstimateTokens(ctx Context) int {
	return defaultEstimateTokens(ctx)
}

func (g *OpenAIGenerator) MarshalHistory(h any) ([]byte, error) {
	msgs, ok := h.([]openAIMessage)
	if !ok {
		return json.Marshal(h)
	}
	return json.Marshal(msgs)
}

func (g *OpenAIGenerator) UnmarshalHistory(b []byte) (any, error) {
	var msgs []openAIMessage
	err := json.Unmarshal(b, &msgs)
	return msgs, err
}

type openAIMessage struct {
	Role       string       `json:"role"`
	Content    string       `json:"content,omitempty"`
	ToolCalls  []openAICall `json:"tool_calls,omitempty"`
	ToolCallID string       `json:"tool_call_id,omitempty"`
}

type openAICall struct {
	ID       string         `json:"id"`
	Type     string         `json:"type"`
	Function openAIFunction `json:"function"`
}

type openAIFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

func (g *OpenAIGenerator) Generate(ctx Context) Proposal {
	var history []openAIMessage
	if h, ok := ctx.History.([]openAIMessage); ok {
		history = h
	}

	if len(history) == 0 {
		history = append(history, openAIMessage{Role: "system", Content: ctx.Purpose})
	}

	for _, r := range ctx.PendingResults {
		content := r.Result
		if r.Error != "" {
			content = r.Error
		}
		history = append(history, openAIMessage{
			Role:       "tool",
			Content:    content,
			ToolCallID: r.ToolUseID,
		})
	}

	if ctx.LastInput != "" {
		lastEvent := lastUserInput(ctx.Events)
		if lastEvent != "" && !openAIAlreadyInHistory(history, lastEvent) {
			history = append(history, openAIMessage{Role: "user", Content: lastEvent})
		}
	}

	body := map[string]any{
		"model":       g.model,
		"messages":    history,
		"temperature": 0,
	}

	tools := g.buildTools(ctx.Tools)
	if len(tools) > 0 {
		body["tools"] = tools
	}

	payload, _ := json.Marshal(body)
	trace := newPromptTrace()

	raw, err := g.call(ctx, payload)
	if err != nil {
		trace.save(payload, []byte(err.Error()))
		return Proposal{Err: err, PromptID: trace.id, History: history}
	}

	trace.save(payload, raw)
	p := g.parseResponse(raw)
	p.PromptID = trace.id

	history = append(history, resp2openAIMsg(p))
	p.History = history
	return p
}

func (g *OpenAIGenerator) call(ctx Context, payload []byte) ([]byte, error) {
	doRequest := func() ([]byte, int, error) {
		endpoint := strings.TrimRight(g.endpoint, "/") + "/chat/completions"

		requestCtx := ctx.Ctx
		if requestCtx == nil {
			requestCtx = context.Background()
		}
		req, err := http.NewRequestWithContext(requestCtx, "POST", endpoint, bytes.NewReader(payload))
		if err != nil {
			return nil, 0, err
		}
		req.Header.Set("Content-Type", "application/json")
		if g.bearerToken != "" {
			req.Header.Set("Authorization", "Bearer "+g.bearerToken)
		}

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, 0, err
		}
		defer resp.Body.Close()

		raw, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, 0, err
		}

		if resp.StatusCode != http.StatusOK {
			return nil, resp.StatusCode, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(raw))
		}
		return raw, resp.StatusCode, nil
	}

	if g.retryConfig != nil {
		return overflow.Retry(*g.retryConfig, doRequest)
	}
	data, _, err := doRequest()
	return data, err
}

func (g *OpenAIGenerator) parseResponse(raw []byte) Proposal {
	var result struct {
		Choices []struct {
			Message struct {
				Content   string       `json:"content"`
				ToolCalls []openAICall `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}

	if err := json.Unmarshal(raw, &result); err != nil || len(result.Choices) == 0 {
		return Proposal{Err: fmt.Errorf("parsing response: %s", string(raw))}
	}

	msg := result.Choices[0].Message

	var effects []ProposedEffect
	for _, tc := range msg.ToolCalls {
		var args map[string]string
		json.Unmarshal([]byte(tc.Function.Arguments), &args)
		effects = append(effects, ProposedEffect{
			Tool:      tc.Function.Name,
			Args:      args,
			ToolUseID: tc.ID,
		})
	}

	return Proposal{
		Text:         strings.TrimSpace(msg.Content),
		Effects:      effects,
		InputTokens:  result.Usage.PromptTokens,
		OutputTokens: result.Usage.CompletionTokens,
	}
}

func (g *OpenAIGenerator) buildTools(tools []ToolDefinition) []map[string]any {
	if len(tools) == 0 {
		return nil
	}
	var out []map[string]any
	for _, t := range tools {
		out = append(out, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        t.Name,
				"description": t.Description,
				"parameters":  paramSchema(t.Params),
			},
		})
	}
	return out
}

func resp2openAIMsg(p Proposal) openAIMessage {
	msg := openAIMessage{Role: "assistant", Content: p.Text}
	for _, e := range p.Effects {
		args, _ := json.Marshal(e.Args)
		msg.ToolCalls = append(msg.ToolCalls, openAICall{
			ID:   e.ToolUseID,
			Type: "function",
			Function: openAIFunction{
				Name:      e.Tool,
				Arguments: string(args),
			},
		})
	}
	return msg
}

func openAIAlreadyInHistory(history []openAIMessage, text string) bool {
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role == "user" && history[i].Content == text {
			return true
		}
	}
	return false
}
