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
	"github.com/mr-joshcrane/orchid/internal/overflow"
)

// BedrockGenerator implements Generator using the AWS Bedrock Converse API.
type BedrockGenerator struct {
	model          string
	region         string
	creds          aws.CredentialsProvider
	retryConfig    *overflow.RetryConfig
	overflowPolicy overflow.OverflowPolicy
}

type BedrockOption func(*BedrockGenerator)

func BedrockWithRetry(attempts int, backoff overflow.BackoffFunc) BedrockOption {
	return func(g *BedrockGenerator) {
		g.retryConfig = &overflow.RetryConfig{Attempts: attempts, Backoff: backoff}
	}
}

func BedrockWithOverflowPolicy(p overflow.OverflowPolicy) BedrockOption {
	return func(g *BedrockGenerator) {
		g.overflowPolicy = p
	}
}

// NewBedrockGenerator creates a generator that calls Bedrock Converse.
// Loads AWS credentials and region from the environment.
// For ARN-style model IDs, the region is extracted from the ARN if not set.
func NewBedrockGenerator(model string, opts ...BedrockOption) (*BedrockGenerator, error) {
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
	g := &BedrockGenerator{
		model:  model,
		region: region,
		creds:  cfg.Credentials,
	}
	for _, opt := range opts {
		opt(g)
	}
	return g, nil
}

func (g *BedrockGenerator) Model() string { return g.model }

func (g *BedrockGenerator) ModelLimit() int {
	return lookupModelLimit(g.model)
}

func (g *BedrockGenerator) EstimateTokens(ctx Context) int {
	return defaultEstimateTokens(ctx)
}

func (g *BedrockGenerator) Generate(ctx Context) Proposal {
	purpose, history := applyContextPolicy(ctx)

	body := bedrockRequest{
		Messages: toBedrockMessages(history),
		System:   []bedrockTextBlock{{Text: purpose}},
		InferenceConfig: &bedrockInferenceConfig{
			Temperature: floatPtr(0),
		},
	}
	tools := g.buildTools(ctx.Tools)
	if len(tools) > 0 {
		body.ToolConfig = &bedrockToolConfig{Tools: tools}
	}

	payload, _ := json.Marshal(body)
	trace := newPromptTrace()
	requestCtx := ctx.Ctx
	if requestCtx == nil {
		requestCtx = context.Background()
	}
	raw, err := g.call(requestCtx, payload)
	if err != nil {
		trace.save(payload, []byte(err.Error()))
		return Proposal{Err: err, PromptID: trace.id}
	}

	trace.save(payload, raw)
	p := g.parseResponse(raw)
	p.PromptID = trace.id
	return p
}

// toBedrockMessages converts the derived history into Converse wire messages.
func toBedrockMessages(messages []Message) []bedrockMessage {
	out := make([]bedrockMessage, 0, len(messages))
	for _, m := range messages {
		out = append(out, bedrockMessage{Role: m.Role, Content: toBedrockContent(m.Content)})
	}
	return out
}

func toBedrockContent(blocks []ContentBlock) []bedrockContentBlock {
	out := make([]bedrockContentBlock, 0, len(blocks))
	for _, b := range blocks {
		out = append(out, bedrockContentBlock{
			Text:       b.Text,
			ToolUse:    toBedrockToolUse(b.ToolUse),
			ToolResult: toBedrockToolResult(b.ToolResult),
		})
	}
	return out
}

// toBedrockToolUse widens the neutral string arguments to the Converse input
// map. Converse accepts any JSON value; orchid carries tool arguments as
// strings end to end, so every value marshals as a string.
func toBedrockToolUse(u *ToolUse) *bedrockToolUse {
	if u == nil {
		return nil
	}
	input := make(map[string]any, len(u.Input))
	for k, v := range u.Input {
		input[k] = v
	}
	return &bedrockToolUse{ToolUseID: u.ToolUseID, Name: u.Name, Input: input}
}

func toBedrockToolResult(r *ToolResult) *bedrockToolResult {
	if r == nil {
		return nil
	}
	content := make([]bedrockTextBlock, len(r.Content))
	for i, t := range r.Content {
		content[i] = bedrockTextBlock{Text: t.Text}
	}
	return &bedrockToolResult{ToolUseID: r.ToolUseID, Content: content, Status: r.Status}
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
					JSON: paramSchema(t.Params),
				},
			},
		})
	}
	return out
}

func (g *BedrockGenerator) call(ctx context.Context, payload []byte) ([]byte, error) {
	doRequest := func() ([]byte, int, error) {
		endpoint := fmt.Sprintf("https://bedrock-runtime.%s.amazonaws.com/model/%s/converse", g.region, url.PathEscape(g.model))

		req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(payload))
		if err != nil {
			return nil, 0, err
		}
		req.Header.Set("Content-Type", "application/json")

		hash := sha256.Sum256(payload)
		payloadHash := hex.EncodeToString(hash[:])

		creds, err := g.creds.Retrieve(ctx)
		if err != nil {
			return nil, 0, fmt.Errorf("retrieving credentials: %w", err)
		}

		signer := v4.NewSigner()
		err = signer.SignHTTP(ctx, creds, req, payloadHash, "bedrock", g.region, time.Now())
		if err != nil {
			return nil, 0, fmt.Errorf("signing request: %w", err)
		}

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, 0, err
		}
		defer resp.Body.Close()

		raw, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			return nil, resp.StatusCode, fmt.Errorf("bedrock returned %d: %s", resp.StatusCode, string(raw))
		}

		return raw, resp.StatusCode, nil
	}

	if g.retryConfig != nil {
		return overflow.Retry(*g.retryConfig, doRequest)
	}
	data, _, err := doRequest()
	return data, err
}

func (g *BedrockGenerator) parseResponse(raw []byte) Proposal {
	var resp bedrockResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return Proposal{Err: fmt.Errorf("parsing response: %s", string(raw))}
	}

	msg := resp.Output.Message
	rawMsg, _ := json.Marshal(msg)

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
				Tool:      block.ToolUse.Name,
				Args:      args,
				ToolUseID: block.ToolUse.ToolUseID,
			})
		}
	}

	return Proposal{Text: text, Effects: effects, RawMessage: string(rawMsg), InputTokens: resp.Usage.InputTokens, OutputTokens: resp.Usage.OutputTokens}
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
	Usage      bedrockUsage  `json:"usage"`
}

type bedrockUsage struct {
	InputTokens  int `json:"inputTokens"`
	OutputTokens int `json:"outputTokens"`
	TotalTokens  int `json:"totalTokens"`
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
