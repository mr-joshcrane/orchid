package orchid

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type OpenAIGenerator struct {
	endpoint string
	model    string
}

func NewOpenAIGenerator(endpoint, model string) *OpenAIGenerator {
	return &OpenAIGenerator{endpoint: endpoint, model: model}
}

func (g *OpenAIGenerator) Model() string { return g.model }

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
		history = append(history, openAIMessage{Role: "system", Content: ctx.Spec.SystemPrompt})
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
	url := strings.TrimRight(g.endpoint, "/") + "/v1/chat/completions"

	requestCtx := ctx.Ctx
	if requestCtx == nil {
		requestCtx = context.Background()
	}
	req, err := http.NewRequestWithContext(requestCtx, "POST", url, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(raw))
	}
	return raw, nil
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
				"parameters": map[string]any{
					"type":       "object",
					"properties": map[string]any{},
				},
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
