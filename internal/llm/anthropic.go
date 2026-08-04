package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/wellivea1/noobboard/internal/config"
	"github.com/wellivea1/noobboard/internal/privacy"
)

type AnthropicClient struct {
	apiKey  string
	model   string
	http    *http.Client
	builder ContextBuilder
}

func NewAnthropicClient(cfg config.LLMConfig, redactor *privacy.Redactor) AnthropicClient {
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 45 * time.Second
	}
	return AnthropicClient{
		apiKey:  firstConfigured(cfg.AnthropicAPIKey, os.Getenv("ANTHROPIC_API_KEY")),
		model:   cfg.AnthropicModel,
		http:    &http.Client{Timeout: timeout},
		builder: NewContextBuilder(redactor),
	}
}

func (c AnthropicClient) Diagnose(ctx context.Context, req Request) (Diagnosis, error) {
	if c.apiKey == "" {
		return Diagnosis{}, errors.New("ANTHROPIC_API_KEY is not set")
	}
	contextText, err := c.builder.Build(req)
	if err != nil {
		return Diagnosis{}, err
	}
	body := map[string]interface{}{
		"model":      c.model,
		"max_tokens": anthropicMaxTokens,
		"system":     Instructions(),
		"messages": []map[string]interface{}{
			{"role": "user", "content": BuildPrompt(contextText)},
		},
		"tools": []map[string]interface{}{
			{
				"name":         "record_diagnosis",
				"description":  "Return the final server status diagnosis as structured JSON.",
				"input_schema": JSONSchema(),
			},
		},
		"tool_choice": map[string]interface{}{
			"type": "tool",
			"name": "record_diagnosis",
		},
	}
	data, err := json.Marshal(body)
	if err != nil {
		return Diagnosis{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.anthropic.com/v1/messages", bytes.NewReader(data))
	if err != nil {
		return Diagnosis{}, err
	}
	httpReq.Header.Set("x-api-key", c.apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(httpReq)
	if err != nil {
		return Diagnosis{}, err
	}
	defer resp.Body.Close()
	respData, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return Diagnosis{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return Diagnosis{}, fmt.Errorf("anthropic messages api returned %d: %s", resp.StatusCode, string(respData))
	}
	return diagnosisFromAnthropic(respData)
}

func (c AnthropicClient) ReviewAction(ctx context.Context, req ActionReviewRequest) (ActionReviewDecision, error) {
	if c.apiKey == "" {
		return ActionReviewDecision{}, errors.New("ANTHROPIC_API_KEY is not set")
	}
	body := map[string]interface{}{
		"model":      c.model,
		"max_tokens": anthropicMaxTokens,
		"system":     "You review a proposed NoobBoard repair action. Return only the structured JSON review decision.",
		"messages": []map[string]interface{}{
			{"role": "user", "content": BuildActionReviewPrompt(req)},
		},
		"tools": []map[string]interface{}{
			{
				"name":         "record_action_review",
				"description":  "Return the final NoobBoard action auto-review decision as structured JSON.",
				"input_schema": ActionReviewJSONSchema(),
			},
		},
		"tool_choice": map[string]interface{}{
			"type": "tool",
			"name": "record_action_review",
		},
	}
	data, err := json.Marshal(body)
	if err != nil {
		return ActionReviewDecision{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.anthropic.com/v1/messages", bytes.NewReader(data))
	if err != nil {
		return ActionReviewDecision{}, err
	}
	httpReq.Header.Set("x-api-key", c.apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(httpReq)
	if err != nil {
		return ActionReviewDecision{}, err
	}
	defer resp.Body.Close()
	respData, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return ActionReviewDecision{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return ActionReviewDecision{}, fmt.Errorf("anthropic action review api returned %d: %s", resp.StatusCode, string(respData))
	}
	return actionReviewFromAnthropic(respData)
}

// The Anthropic max_tokens budget covers thinking as well as the response, and
// current models think by default. The old 1200/900 budgets were sized for the
// tool call alone, so on a thinking model the JSON could be cut off mid-object
// and surface as a parse error rather than as a truncation. The calls here are
// a single structured tool use, so a generous cap costs nothing when it is not
// reached — max_tokens is a ceiling, not a reservation.
const anthropicMaxTokens = 8000

// A refusal or a truncation comes back as HTTP 200 with no usable tool call, so
// the transport-level check above cannot catch either. Without this both fall
// through to scanning the raw body for JSON, which fails with something that
// reads like a bug in NoobBoard rather than an answer the provider declined to
// give. Infrastructure questions name ports, SSH and networking, so a
// cyber-category refusal is a realistic outcome here, not a hypothetical.
func anthropicStopReasonError(data []byte) error {
	var raw struct {
		StopReason  string `json:"stop_reason"`
		StopDetails *struct {
			Category string `json:"category"`
		} `json:"stop_details"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		// This only inspects a response for a refusal. A body that will not parse
		// carries no refusal to report, and the caller's own decode reports the
		// parse failure with better context.
		//nolint:nilerr // absence of a refusal, not a swallowed error
		return nil
	}
	switch raw.StopReason {
	case "refusal":
		if raw.StopDetails != nil && raw.StopDetails.Category != "" {
			return fmt.Errorf("anthropic declined this request (%s); try a different provider or rephrase the question", raw.StopDetails.Category)
		}
		return errors.New("anthropic declined this request; try a different provider or rephrase the question")
	case "max_tokens":
		return errors.New("anthropic response hit the token limit before returning a complete answer")
	}
	return nil
}

func diagnosisFromAnthropic(data []byte) (Diagnosis, error) {
	var raw struct {
		Content []struct {
			Type  string          `json:"type"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
			Text  string          `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return Diagnosis{}, err
	}
	for _, block := range raw.Content {
		if block.Type == "tool_use" && block.Name == "record_diagnosis" && len(block.Input) > 0 {
			return ValidateDiagnosis(block.Input)
		}
	}
	if err := anthropicStopReasonError(data); err != nil {
		return Diagnosis{}, err
	}
	jsonText, err := firstJSONString(data)
	if err != nil {
		return Diagnosis{}, err
	}
	return ValidateDiagnosis([]byte(jsonText))
}

func actionReviewFromAnthropic(data []byte) (ActionReviewDecision, error) {
	var raw struct {
		Content []struct {
			Type  string          `json:"type"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
			Text  string          `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return ActionReviewDecision{}, err
	}
	for _, block := range raw.Content {
		if block.Type == "tool_use" && block.Name == "record_action_review" && len(block.Input) > 0 {
			return ValidateActionReviewDecision(block.Input)
		}
	}
	if err := anthropicStopReasonError(data); err != nil {
		return ActionReviewDecision{}, err
	}
	jsonText, err := firstJSONString(data)
	if err != nil {
		return ActionReviewDecision{}, err
	}
	return ValidateActionReviewDecision([]byte(jsonText))
}
