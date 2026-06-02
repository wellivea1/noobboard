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

	"github.com/wellivea1/server-status/internal/config"
	"github.com/wellivea1/server-status/internal/privacy"
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
		apiKey:  os.Getenv("ANTHROPIC_API_KEY"),
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
		"max_tokens": 1200,
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
	jsonText, err := firstJSONString(data)
	if err != nil {
		return Diagnosis{}, err
	}
	return ValidateDiagnosis([]byte(jsonText))
}
