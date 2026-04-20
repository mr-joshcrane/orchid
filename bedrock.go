package orchid

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
)

// BedrockGenerator implements Generator using the AWS Bedrock Converse API.
type BedrockGenerator struct {
	model  string
	region string
	creds  aws.CredentialsProvider
}

// NewBedrockGenerator creates a generator that calls Bedrock Converse.
// Loads AWS credentials and region from the environment.
// For ARN-style model IDs, the region is extracted from the ARN if not set.
func NewBedrockGenerator(model string) (*BedrockGenerator, error) {
	cfg, err := awsconfig.LoadDefaultConfig(context.Background())
	if err != nil {
		return nil, fmt.Errorf("loading aws config: %w", err)
	}
	region := cfg.Region
	if region == "" {
		region = regionFromARN(model)
	}
	if region == "" {
		return nil, fmt.Errorf("no AWS region: set AWS_REGION or use an ARN-style model ID")
	}
	return &BedrockGenerator{
		model:  model,
		region: region,
		creds:  cfg.Credentials,
	}, nil
}

func (g *BedrockGenerator) Generate(ctx Context) Proposal {
	messages := g.buildMessages(ctx)
	tools := g.buildTools(ctx.Tools)

	body := bedrockRequest{
		Messages: messages,
		System:   []bedrockTextBlock{{Text: ctx.Spec.SystemPrompt}},
		InferenceConfig: &bedrockInferenceConfig{
			Temperature: floatPtr(0),
		},
	}
	if len(tools) > 0 {
		body.ToolConfig = &bedrockToolConfig{Tools: tools}
	}

	payload, _ := json.Marshal(body)
	trace := newPromptTrace()
	raw, err := g.call(payload)
	if err != nil {
		trace.save(payload, []byte(err.Error()))
		return Proposal{Text: fmt.Sprintf("error: %s", err), PromptID: trace.id}
	}

	trace.save(payload, raw)
	p := g.parseResponse(raw)
	p.PromptID = trace.id
	return p
}

func (g *BedrockGenerator) buildMessages(ctx Context) []bedrockMessage {
	var msgs []bedrockMessage

	for _, e := range ctx.Events {
		switch e.Type {
		case EventUserInput:
			msgs = append(msgs, bedrockMessage{
				Role:    "user",
				Content: []bedrockContentBlock{{Text: strPtr(e.Data["content"])}},
			})
		case EventAssistantOutput:
			content := e.Data["content"]
			if content == "" {
				continue
			}
			msgs = append(msgs, bedrockMessage{
				Role:    "assistant",
				Content: []bedrockContentBlock{{Text: &content}},
			})
		case EventEffectObserved:
			msgs = append(msgs, bedrockMessage{
				Role: "user",
				Content: []bedrockContentBlock{{Text: strPtr(
					fmt.Sprintf("[tool %s result]: %s", e.Data["tool"], e.Data["result"]),
				)}},
			})
		case EventEffectRejected:
			msgs = append(msgs, bedrockMessage{
				Role: "user",
				Content: []bedrockContentBlock{{Text: strPtr(
					fmt.Sprintf("[tool %s rejected]: %s", e.Data["tool"], e.Data["reason"]),
				)}},
			})
		}
	}

	return msgs
}

func (g *BedrockGenerator) buildTools(tools []ToolDefinition) []bedrockTool {
	if len(tools) == 0 {
		return nil
	}
	var out []bedrockTool
	for _, t := range tools {
		out = append(out, bedrockTool{
			ToolSpec: &bedrockToolSpec{
				Name:        t.Name,
				Description: t.Description,
				InputSchema: &bedrockInputSchema{
					JSON: map[string]any{
						"type":       "object",
						"properties": map[string]any{},
					},
				},
			},
		})
	}
	return out
}

func (g *BedrockGenerator) call(payload []byte) ([]byte, error) {
	endpoint := fmt.Sprintf("https://bedrock-runtime.%s.amazonaws.com/model/%s/converse", g.region, url.PathEscape(g.model))

	req, err := http.NewRequest("POST", endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	hash := sha256.Sum256(payload)
	payloadHash := hex.EncodeToString(hash[:])

	ctx := context.Background()
	creds, err := g.creds.Retrieve(ctx)
	if err != nil {
		return nil, fmt.Errorf("retrieving credentials: %w", err)
	}

	signer := v4.NewSigner()
	err = signer.SignHTTP(ctx, creds, req, payloadHash, "bedrock", g.region, time.Now())
	if err != nil {
		return nil, fmt.Errorf("signing request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("bedrock returned %d: %s", resp.StatusCode, string(raw))
	}

	return raw, nil
}

func (g *BedrockGenerator) parseResponse(raw []byte) Proposal {
	var resp bedrockResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return Proposal{Text: fmt.Sprintf("error parsing response: %s", string(raw))}
	}

	msg := resp.Output.Message
	var text string
	var effects []ProposedEffect

	for _, block := range msg.Content {
		if block.Text != nil {
			text += *block.Text
		}
		if block.ToolUse != nil {
			args := make(map[string]string)
			for k, v := range block.ToolUse.Input {
				args[k] = fmt.Sprint(v)
			}
			effects = append(effects, ProposedEffect{
				Tool: block.ToolUse.Name,
				Args: args,
			})
		}
	}

	return Proposal{Text: text, Effects: effects}
}

// Bedrock Converse API types.

type bedrockRequest struct {
	Messages        []bedrockMessage        `json:"messages"`
	System          []bedrockTextBlock      `json:"system,omitempty"`
	InferenceConfig *bedrockInferenceConfig `json:"inferenceConfig,omitempty"`
	ToolConfig      *bedrockToolConfig      `json:"toolConfig,omitempty"`
}

type bedrockMessage struct {
	Role    string                `json:"role"`
	Content []bedrockContentBlock `json:"content"`
}

type bedrockContentBlock struct {
	Text       *string            `json:"text,omitempty"`
	ToolUse    *bedrockToolUse    `json:"toolUse,omitempty"`
	ToolResult *bedrockToolResult `json:"toolResult,omitempty"`
}

type bedrockToolUse struct {
	ToolUseID string         `json:"toolUseId"`
	Name      string         `json:"name"`
	Input     map[string]any `json:"input"`
}

type bedrockToolResult struct {
	ToolUseID string             `json:"toolUseId"`
	Content   []bedrockTextBlock `json:"content"`
	Status    string             `json:"status"`
}

type bedrockTextBlock struct {
	Text string `json:"text"`
}

type bedrockInferenceConfig struct {
	MaxTokens   *int     `json:"maxTokens,omitempty"`
	Temperature *float64 `json:"temperature,omitempty"`
	TopP        *float64 `json:"topP,omitempty"`
}

type bedrockToolConfig struct {
	Tools []bedrockTool `json:"tools"`
}

type bedrockTool struct {
	ToolSpec *bedrockToolSpec `json:"toolSpec,omitempty"`
}

type bedrockToolSpec struct {
	Name        string              `json:"name"`
	Description string              `json:"description"`
	InputSchema *bedrockInputSchema `json:"inputSchema"`
}

type bedrockInputSchema struct {
	JSON map[string]any `json:"json"`
}

type bedrockResponse struct {
	Output     bedrockOutput `json:"output"`
	StopReason string        `json:"stopReason"`
}

type bedrockOutput struct {
	Message bedrockMessage `json:"message"`
}

func strPtr(s string) *string     { return &s }
func floatPtr(f float64) *float64 { return &f }

// regionFromARN extracts the region from an ARN like arn:aws:bedrock:us-west-2:1234:...
func regionFromARN(arn string) string {
	parts := strings.SplitN(arn, ":", 5)
	if len(parts) < 4 {
		return ""
	}
	return parts[3]
}
