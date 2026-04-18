package orchid

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type llmGenerator struct {
	endpoint string
	model    string
}

func (g *llmGenerator) Generate(ctx Context) Proposal {
	messages := g.buildMessages(ctx)
	tools := g.buildTools(ctx.Tools)

	body := map[string]any{
		"model":       g.model,
		"messages":    messages,
		"temperature": 0,
	}
	if len(tools) > 0 {
		body["tools"] = tools
	}
	payload, _ := json.Marshal(body)

	resp, err := http.Post(g.endpoint+"/v1/chat/completions", "application/json", bytes.NewReader(payload))
	if err != nil {
		return Proposal{Text: fmt.Sprintf("error: %s", err)}
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	return g.parseResponse(raw)
}

func (g *llmGenerator) buildMessages(ctx Context) []map[string]string {
	msgs := []map[string]string{
		{"role": "system", "content": ctx.Spec.SystemPrompt},
	}

	for _, e := range ctx.Events {
		switch e.Type {
		case EventUserInput:
			msgs = append(msgs, map[string]string{"role": "user", "content": e.Data["content"]})
		case EventAssistantOutput:
			if content := e.Data["content"]; content != "" {
				msgs = append(msgs, map[string]string{"role": "assistant", "content": content})
			}
		case EventEffectObserved:
			msgs = append(msgs, map[string]string{"role": "user", "content": fmt.Sprintf("[tool %s result]: %s", e.Data["tool"], e.Data["result"])})
		case EventEffectRejected:
			msgs = append(msgs, map[string]string{"role": "user", "content": fmt.Sprintf("[tool %s rejected]: %s", e.Data["tool"], e.Data["reason"])})
		}
	}

	return msgs
}

func (g *llmGenerator) buildTools(tools []ToolDefinition) []map[string]any {
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
			},
		})
	}
	return out
}

func (g *llmGenerator) parseResponse(raw []byte) Proposal {
	var result struct {
		Choices []struct {
			Message struct {
				Content   string `json:"content"`
				ToolCalls []struct {
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
	}

	if err := json.Unmarshal(raw, &result); err != nil || len(result.Choices) == 0 {
		return Proposal{Text: fmt.Sprintf("error parsing response: %s", string(raw))}
	}

	msg := result.Choices[0].Message

	var effects []ProposedEffect
	for _, tc := range msg.ToolCalls {
		var args map[string]string
		json.Unmarshal([]byte(tc.Function.Arguments), &args)
		effects = append(effects, ProposedEffect{
			Tool: tc.Function.Name,
			Args: args,
		})
	}

	text := strings.TrimSpace(msg.Content)

	return Proposal{
		Text:    text,
		Effects: effects,
	}
}
