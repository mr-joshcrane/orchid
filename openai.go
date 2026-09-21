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
	contextLimit   int
}

type OpenAIOption func(*OpenAIGenerator)

// OpenAIWithContextLimit sets the model's context window in tokens, overriding
// the library's lookup table. Local and self-hosted endpoints serve models the
// table cannot know about, and a limit of zero silently disables the
// prompt-pressure events.
func OpenAIWithContextLimit(tokens int) OpenAIOption {
	return func(g *OpenAIGenerator) {
		g.contextLimit = tokens
	}
}

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
	if g.contextLimit > 0 {
		return g.contextLimit
	}
	return lookupModelLimit(g.model)
}

func (g *OpenAIGenerator) EstimateTokens(ctx Context) int {
	return defaultEstimateTokens(ctx)
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
	purpose, history := applyContextPolicy(ctx)
	messages := append(
		[]openAIMessage{{Role: "system", Content: purpose}},
		toOpenAIMessages(history)...,
	)

	body := map[string]any{
		"model":       g.model,
		"messages":    messages,
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
		return Proposal{Err: err, PromptID: trace.id}
	}

	trace.save(payload, raw)
	p := g.parseResponse(raw)
	p.PromptID = trace.id
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

// toOpenAIMessages converts the derived history into chat-completions messages.
func toOpenAIMessages(messages []Message) []openAIMessage {
	var out []openAIMessage
	for _, m := range messages {
		out = append(out, convertOpenAIMessage(m)...)
	}
	return out
}

// convertOpenAIMessage splits one neutral message into the messages the
// chat-completions format needs: tool results become their own `tool`-role
// entries, and any remaining text or tool calls become a single entry.
func convertOpenAIMessage(m Message) []openAIMessage {
	var out []openAIMessage
	for _, b := range m.Content {
		if b.ToolResult == nil {
			continue
		}
		out = append(out, openAIMessage{
			Role:       "tool",
			Content:    toolResultText(b.ToolResult),
			ToolCallID: b.ToolResult.ToolUseID,
		})
	}
	text := messageText(m)
	calls := openAICalls(m)
	if text == "" && len(calls) == 0 {
		return out
	}
	return append(out, openAIMessage{Role: m.Role, Content: text, ToolCalls: calls})
}

func openAICalls(m Message) []openAICall {
	var calls []openAICall
	for _, b := range m.Content {
		if b.ToolUse == nil {
			continue
		}
		args, _ := json.Marshal(b.ToolUse.Input)
		calls = append(calls, openAICall{
			ID:   b.ToolUse.ToolUseID,
			Type: "function",
			Function: openAIFunction{
				Name:      b.ToolUse.Name,
				Arguments: string(args),
			},
		})
	}
	return calls
}
