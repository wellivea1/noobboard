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

type OpenAIClient struct {
	apiKey  string
	model   string
	http    *http.Client
	builder ContextBuilder
}

func NewOpenAIClient(cfg config.LLMConfig, redactor *privacy.Redactor) OpenAIClient {
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 45 * time.Second
	}
	return OpenAIClient{
		apiKey:  os.Getenv("OPENAI_API_KEY"),
		model:   cfg.OpenAIModel,
		http:    &http.Client{Timeout: timeout},
		builder: NewContextBuilder(redactor),
	}
}

func (c OpenAIClient) Diagnose(ctx context.Context, req Request) (Diagnosis, error) {
	if c.apiKey == "" {
		return Diagnosis{}, errors.New("OPENAI_API_KEY is not set")
	}
	contextText, err := c.builder.Build(req)
	if err != nil {
		return Diagnosis{}, err
	}
	body := map[string]interface{}{
		"model":        c.model,
		"instructions": Instructions(),
		"input":        BuildPrompt(contextText),
		"tools":        []interface{}{},
		"text": map[string]interface{}{
			"format": map[string]interface{}{
				"type":   "json_schema",
				"name":   "server_status_diagnosis",
				"strict": true,
				"schema": JSONSchema(),
			},
		},
	}
	data, err := json.Marshal(body)
	if err != nil {
		return Diagnosis{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.openai.com/v1/responses", bytes.NewReader(data))
	if err != nil {
		return Diagnosis{}, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
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
		return Diagnosis{}, fmt.Errorf("openai responses api returned %d: %s", resp.StatusCode, string(respData))
	}
	jsonText, err := firstJSONString(respData)
	if err != nil {
		return Diagnosis{}, err
	}
	return ValidateDiagnosis([]byte(jsonText))
}
