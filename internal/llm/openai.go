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
	return runOpenAIResponsesDiagnosis(ctx, c.http, "https://api.openai.com/v1/responses", headers, c.model, contextText, req, c.builder.redactor, "openai responses api")
}

func openAIResponsesBody(model, contextText string) ([]byte, error) {
	return openAIResponsesBodyWithInput(model, BuildPrompt(contextText), []interface{}{})
}

func openAIResponsesBodyWithInput(model string, input interface{}, tools []interface{}) ([]byte, error) {
	instructions := Instructions()
	if len(tools) > 0 {
		instructions = AgentInstructions()
	}
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
	return json.Marshal(body)
}

func runOpenAIResponsesDiagnosis(ctx context.Context, client *http.Client, endpoint string, headers http.Header, model, contextText string, req Request, redactor *privacy.Redactor, label string) (Diagnosis, error) {
	agentTools := agentToolsForRequest(req, redactor)
	toolDefinitions := openAIToolDefinitions(agentTools)
	input := interface{}(BuildPrompt(contextText))
	var conversation []interface{}
	if len(toolDefinitions) > 0 {
		conversation = []interface{}{map[string]interface{}{
			"role":    "user",
			"content": BuildPrompt(contextText),
		}}
		input = conversation
	}
	maxToolCalls := req.Policy.AgentMaxToolCalls
	if maxToolCalls <= 0 {
		maxToolCalls = 1
	}
	toolCallsUsed := 0
	for {
		data, err := openAIResponsesBodyWithInput(model, input, toolDefinitions)
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
		return nil, fmt.Errorf("%s returned %d: %s", label, resp.StatusCode, string(respData))
	}
	return respData, nil
}

func diagnosisFromResponsesBody(respData []byte) (Diagnosis, error) {
	jsonText, err := firstJSONString(respData)
	if err != nil {
		return Diagnosis{}, err
	}
	return ValidateDiagnosis([]byte(jsonText))
}
