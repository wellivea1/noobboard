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
	"strings"
	"time"

	"github.com/wellivea1/noobboard/internal/config"
	"github.com/wellivea1/noobboard/internal/privacy"
)

const OpenAIUsageLimitCode = "openai_usage_limit"

type ProviderError struct {
	Label      string
	StatusCode int
	Code       string
	Message    string
	Body       string
}

func (e *ProviderError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("%s returned %d: %s", e.Label, e.StatusCode, e.Message)
	}
	return fmt.Sprintf("%s returned %d: %s", e.Label, e.StatusCode, e.Body)
}

func IsOpenAIUsageLimitError(err error) bool {
	var providerErr *ProviderError
	if !errors.As(err, &providerErr) {
		return false
	}
	if providerErr.Code == OpenAIUsageLimitCode {
		return true
	}
	text := strings.ToLower(providerErr.Message + " " + providerErr.Body)
	return providerErr.StatusCode == http.StatusTooManyRequests &&
		(strings.Contains(text, "usage limit") ||
			strings.Contains(text, "rate limit") ||
			strings.Contains(text, "quota") ||
			strings.Contains(text, "billing") ||
			strings.Contains(text, "too many requests"))
}

type OpenAIClient struct {
	apiKey  string
	model   string
	http    *http.Client
	builder ContextBuilder
}

type openAIResponsesOptions struct {
	ReasoningEffort           string
	IncludeEncryptedReasoning bool
	InputAsList               bool
	StoreFalse                bool
}

func NewOpenAIClient(cfg config.LLMConfig, redactor *privacy.Redactor) OpenAIClient {
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 45 * time.Second
	}
	return OpenAIClient{
		apiKey:  firstConfigured(cfg.OpenAIAPIKey, os.Getenv("OPENAI_API_KEY")),
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
	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+c.apiKey)
	return runOpenAIResponsesDiagnosis(ctx, c.http, "https://api.openai.com/v1/responses", headers, c.model, contextText, req, c.builder.redactor, "openai responses api", openAIResponsesOptions{})
}

func openAIResponsesBody(model, contextText string) ([]byte, error) {
	return openAIResponsesBodyWithInput(model, BuildPrompt(contextText), []interface{}{}, openAIResponsesOptions{})
}

func openAIResponsesBodyWithInput(model string, input interface{}, tools []interface{}, opts openAIResponsesOptions) ([]byte, error) {
	instructions := Instructions()
	if len(tools) > 0 {
		instructions = AgentInstructions()
	}
	input = responsesInputWithoutIDs(input)
	body := map[string]interface{}{
		"model":        model,
		"instructions": instructions,
		"input":        input,
		"tools":        tools,
		"text": map[string]interface{}{
			"format": map[string]interface{}{
				"type":   "json_schema",
				"name":   "server_status_diagnosis",
				"strict": true,
				"schema": JSONSchema(),
			},
		},
	}
	if opts.ReasoningEffort != "" {
		body["reasoning"] = map[string]interface{}{"effort": opts.ReasoningEffort}
	}
	if opts.IncludeEncryptedReasoning {
		body["include"] = []string{"reasoning.encrypted_content"}
	}
	if opts.StoreFalse {
		body["store"] = false
	}
	return json.Marshal(body)
}

func responsesInputWithoutIDs(input interface{}) interface{} {
	items, ok := input.([]interface{})
	if !ok {
		return input
	}
	cleaned := make([]interface{}, 0, len(items))
	for _, item := range items {
		object, ok := item.(map[string]interface{})
		if !ok {
			cleaned = append(cleaned, item)
			continue
		}
		copyObject := make(map[string]interface{}, len(object))
		for key, value := range object {
			if key == "id" {
				continue
			}
			copyObject[key] = value
		}
		cleaned = append(cleaned, copyObject)
	}
	return cleaned
}

func runOpenAIResponsesDiagnosis(ctx context.Context, client *http.Client, endpoint string, headers http.Header, model, contextText string, req Request, redactor *privacy.Redactor, label string, opts openAIResponsesOptions) (Diagnosis, error) {
	agentTools := agentToolsForRequest(req, redactor)
	toolDefinitions := openAIToolDefinitions(agentTools)
	prompt := BuildPrompt(contextText)
	input := interface{}(prompt)
	var conversation []interface{}
	if opts.InputAsList || len(toolDefinitions) > 0 {
		conversation = []interface{}{map[string]interface{}{
			"role":    "user",
			"content": prompt,
		}}
		input = conversation
	}
	maxToolCalls := req.Policy.AgentMaxToolCalls
	if maxToolCalls <= 0 {
		maxToolCalls = 1
	}
	toolCallsUsed := 0
	for {
		data, err := openAIResponsesBodyWithInput(model, input, toolDefinitions, opts)
		if err != nil {
			return Diagnosis{}, err
		}
		respData, err := postOpenAIResponses(ctx, client, endpoint, headers, data, label)
		if err != nil {
			return Diagnosis{}, err
		}
		calls, output, err := parseAgentToolCalls(respData)
		if err != nil {
			return Diagnosis{}, err
		}
		if len(calls) == 0 {
			return diagnosisFromResponsesBody(respData)
		}
		if len(agentTools) == 0 {
			return Diagnosis{}, errors.New("model requested a tool, but agent tools are disabled")
		}
		if toolCallsUsed+len(calls) > maxToolCalls {
			return Diagnosis{}, fmt.Errorf("agent tool call limit exceeded: %d > %d", toolCallsUsed+len(calls), maxToolCalls)
		}
		toolCallsUsed += len(calls)
		conversation = append(conversation, output...)
		for _, call := range calls {
			result, err := executeAgentTool(ctx, agentTools, call)
			if req.ToolAudit != nil {
				errText := ""
				if err != nil {
					errText = err.Error()
				}
				req.ToolAudit(call.Name, err == nil, errText)
			}
			if err != nil {
				return Diagnosis{}, err
			}
			conversation = append(conversation, result)
		}
		input = conversation
	}
}

func postOpenAIResponses(ctx context.Context, client *http.Client, endpoint string, headers http.Header, data []byte, label string) ([]byte, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	for key, values := range headers {
		for _, value := range values {
			httpReq.Header.Add(key, value)
		}
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respData, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, openAIProviderError(label, resp.StatusCode, respData)
	}
	return respData, nil
}

func openAIProviderError(label string, statusCode int, respData []byte) error {
	body := string(respData)
	code, message := openAIErrorCodeAndMessage(respData)
	err := &ProviderError{
		Label:      label,
		StatusCode: statusCode,
		Code:       code,
		Message:    message,
		Body:       body,
	}
	if isOpenAIUsageLimitStatus(statusCode, code, message, body) {
		err.Code = OpenAIUsageLimitCode
		if err.Message == "" {
			err.Message = "OpenAI usage limit reached."
		}
	}
	return err
}

func openAIErrorCodeAndMessage(respData []byte) (string, string) {
	var body map[string]interface{}
	if err := json.Unmarshal(respData, &body); err != nil {
		return "", ""
	}
	code := stringFromMap(body, "code")
	message := stringFromMap(body, "message")
	if detail := stringFromMap(body, "detail"); message == "" {
		message = detail
	}
	if nested, ok := body["error"].(map[string]interface{}); ok {
		if value := stringFromMap(nested, "code"); value != "" {
			code = value
		}
		if value := stringFromMap(nested, "message"); value != "" {
			message = value
		}
	}
	return code, message
}

func stringFromMap(values map[string]interface{}, key string) string {
	value, _ := values[key].(string)
	return strings.TrimSpace(value)
}

func isOpenAIUsageLimitStatus(statusCode int, code, message, body string) bool {
	text := strings.ToLower(code + " " + message + " " + body)
	if strings.Contains(text, "insufficient_quota") ||
		strings.Contains(text, "usage limit") ||
		strings.Contains(text, "billing") ||
		strings.Contains(text, "quota") {
		return true
	}
	return statusCode == http.StatusTooManyRequests &&
		(strings.Contains(text, "rate_limit") ||
			strings.Contains(text, "rate limit") ||
			strings.Contains(text, "too many requests") ||
			strings.Contains(text, "limit"))
}

func diagnosisFromResponsesBody(respData []byte) (Diagnosis, error) {
	jsonText, err := firstJSONString(respData)
	if err != nil {
		return Diagnosis{}, err
	}
	return ValidateDiagnosis([]byte(jsonText))
}
