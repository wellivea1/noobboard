package llm

import (
	"bufio"
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
	prefix := e.Label
	if e.StatusCode > 0 {
		prefix = fmt.Sprintf("%s returned %d", e.Label, e.StatusCode)
	} else {
		prefix = e.Label + " stream error"
	}
	if e.Message != "" {
		return fmt.Sprintf("%s: %s", prefix, e.Message)
	}
	return fmt.Sprintf("%s: %s", prefix, e.Body)
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
	Stream                    bool
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

func (c OpenAIClient) ReviewAction(ctx context.Context, req ActionReviewRequest) (ActionReviewDecision, error) {
	if c.apiKey == "" {
		return ActionReviewDecision{}, errors.New("OPENAI_API_KEY is not set")
	}
	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+c.apiKey)
	prompt := BuildActionReviewPrompt(req)
	data, err := openAIActionReviewBody(c.model, prompt, openAIResponsesOptions{ReasoningEffort: openAIReviewReasoning(req.Reasoning)})
	if err != nil {
		return ActionReviewDecision{}, err
	}
	respData, err := postOpenAIResponses(ctx, c.http, "https://api.openai.com/v1/responses", headers, data, "openai action review api", openAIResponsesOptions{})
	if err != nil {
		return ActionReviewDecision{}, err
	}
	return actionReviewFromResponsesBody(respData)
}

func openAIResponsesBody(model, contextText string) ([]byte, error) {
	return openAIResponsesBodyWithInput(model, BuildPrompt(contextText), []interface{}{}, openAIResponsesOptions{})
}

func openAIActionReviewBody(model, prompt string, opts openAIResponsesOptions) ([]byte, error) {
	input := interface{}(prompt)
	if opts.InputAsList {
		input = []interface{}{map[string]interface{}{
			"role":    "user",
			"content": prompt,
		}}
	}
	body := map[string]interface{}{
		"model":        model,
		"instructions": "You review a proposed NoobBoard repair action. Return only the structured JSON review decision.",
		"input":        input,
		"text": map[string]interface{}{
			"format": map[string]interface{}{
				"type":   "json_schema",
				"name":   "noobboard_action_review",
				"strict": true,
				"schema": ActionReviewJSONSchema(),
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
	if opts.Stream {
		body["stream"] = true
	}
	return json.Marshal(body)
}

func openAIReviewReasoning(value string) string {
	switch strings.TrimSpace(value) {
	case "low", "medium", "high":
		return strings.TrimSpace(value)
	case "xhigh":
		return "high"
	default:
		return ""
	}
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
	if opts.Stream {
		body["stream"] = true
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
		respData, err := postOpenAIResponses(ctx, client, endpoint, headers, data, label, opts)
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

func postOpenAIResponses(ctx context.Context, client *http.Client, endpoint string, headers http.Header, data []byte, label string, opts openAIResponsesOptions) ([]byte, error) {
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
	if opts.Stream {
		return normalizeOpenAIResponsesStream(respData, label)
	}
	return respData, nil
}

func normalizeOpenAIResponsesStream(respData []byte, label string) ([]byte, error) {
	trimmed := bytes.TrimSpace(respData)
	if len(trimmed) == 0 {
		return nil, errors.New("empty OpenAI response stream")
	}
	if trimmed[0] == '{' || trimmed[0] == '[' {
		return respData, nil
	}
	scanner := bufio.NewScanner(bytes.NewReader(respData))
	scanner.Buffer(make([]byte, 0, 64*1024), 2<<20)
	var eventName string
	var dataLines []string
	acc := openAIStreamAccumulator{}
	flush := func() error {
		if len(dataLines) == 0 {
			eventName = ""
			return nil
		}
		payload := strings.TrimSpace(strings.Join(dataLines, "\n"))
		event := eventName
		eventName = ""
		dataLines = nil
		if payload == "" || payload == "[DONE]" {
			return nil
		}
		return acc.add(event, []byte(payload), label)
	}
	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if line == "" {
			if err := flush(); err != nil {
				return nil, err
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		switch {
		case strings.HasPrefix(line, "event:"):
			eventName = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if err := flush(); err != nil {
		return nil, err
	}
	return acc.body()
}

type openAIStreamAccumulator struct {
	output       []interface{}
	outputText   strings.Builder
	completed    map[string]interface{}
	completedSet bool
}

func (a *openAIStreamAccumulator) add(eventName string, payload []byte, label string) error {
	var event map[string]interface{}
	if err := json.Unmarshal(payload, &event); err != nil {
		return err
	}
	eventType := strings.TrimSpace(fmt.Sprint(event["type"]))
	if eventType == "" {
		eventType = eventName
	}
	if eventType == "error" || strings.HasSuffix(eventType, ".failed") {
		return openAIProviderError(label, 0, payload)
	}
	if errorValue, ok := event["error"]; ok && errorValue != nil {
		return openAIProviderError(label, 0, payload)
	}
	if response, ok := event["response"].(map[string]interface{}); ok {
		a.completed = response
		a.completedSet = true
	}
	if delta, ok := event["delta"].(string); ok && strings.Contains(eventType, "output_text.delta") {
		a.outputText.WriteString(delta)
	}
	if text, ok := event["text"].(string); ok && strings.Contains(eventType, "output_text.done") && a.outputText.Len() == 0 {
		a.outputText.WriteString(text)
	}
	if item, ok := event["item"].(map[string]interface{}); ok && strings.Contains(eventType, "output_item.done") {
		a.output = append(a.output, item)
	}
	return nil
}

func (a *openAIStreamAccumulator) body() ([]byte, error) {
	if a.completedSet {
		if value, ok := a.completed["output_text"].(string); (!ok || strings.TrimSpace(value) == "") && a.outputText.Len() > 0 {
			a.completed["output_text"] = a.outputText.String()
		}
		if value, ok := a.completed["output"].([]interface{}); (!ok || len(value) == 0) && len(a.output) > 0 {
			a.completed["output"] = a.output
		}
		return json.Marshal(a.completed)
	}
	body := map[string]interface{}{}
	if a.outputText.Len() > 0 {
		body["output_text"] = a.outputText.String()
	}
	if len(a.output) > 0 {
		body["output"] = a.output
	}
	if len(body) == 0 {
		return nil, errors.New("no OpenAI response data found in response stream")
	}
	return json.Marshal(body)
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
	if response, ok := body["response"].(map[string]interface{}); ok {
		if nested, ok := response["error"].(map[string]interface{}); ok {
			if value := stringFromMap(nested, "code"); value != "" {
				code = value
			}
			if value := stringFromMap(nested, "message"); value != "" {
				message = value
			}
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
		strings.Contains(text, "quota") ||
		strings.Contains(text, "rate_limit") ||
		strings.Contains(text, "rate limit") ||
		strings.Contains(text, "too many requests") {
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

func actionReviewFromResponsesBody(respData []byte) (ActionReviewDecision, error) {
	jsonText, err := firstJSONString(respData)
	if err != nil {
		return ActionReviewDecision{}, err
	}
	return ValidateActionReviewDecision([]byte(jsonText))
}
