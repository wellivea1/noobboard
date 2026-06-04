package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/wellivea1/noobboard/internal/models"
	"github.com/wellivea1/noobboard/internal/privacy"
)

const (
	agentToolCurrentStatus = "noobboard_current_status"
	agentToolServerStatus  = "noobboard_server_status"
	agentToolNetworkStatus = "noobboard_network_status"
	agentToolAppStatus     = "noobboard_app_status"
)

type agentTool struct {
	Name        string
	Description string
	Parameters  map[string]interface{}
	Execute     func(context.Context, map[string]interface{}) (interface{}, error)
}

type agentToolCall struct {
	CallID    string
	Name      string
	Arguments map[string]interface{}
}

func agentToolsForRequest(req Request, redactor *privacy.Redactor) map[string]agentTool {
	if !req.Policy.AgentToolsEnabled || req.Policy.RecipientRole != models.RoleAdmin || req.LiveSnapshot == nil {
		return nil
	}
	allowed := allowedAgentTools(req.Policy)
	if len(allowed) == 0 {
		return nil
	}
	tools := map[string]agentTool{}
	add := func(tool agentTool) {
		if allowed[tool.Name] {
			tools[tool.Name] = tool
		}
	}
	statusSnapshot := func(ctx context.Context) (models.Snapshot, error) {
		snapshot, err := req.LiveSnapshot(ctx)
		if err != nil {
			return models.Snapshot{}, err
		}
		return privacy.FilterSnapshotForRole(snapshot, req.Policy.RecipientRole, redactor), nil
	}
	add(agentTool{
		Name:        agentToolCurrentStatus,
		Description: "Fetch the latest sanitized NoobBoard status snapshot and return a compact read-only API report.",
		Parameters:  noArgToolParameters(),
		Execute: func(ctx context.Context, _ map[string]interface{}) (interface{}, error) {
			snapshot, err := statusSnapshot(ctx)
			if err != nil {
				return nil, err
			}
			return map[string]interface{}{
				"api_report": buildAPIReport(snapshot),
			}, nil
		},
	})
	add(agentTool{
		Name:        agentToolServerStatus,
		Description: "Fetch the latest sanitized NoobBoard server/NAS status fields. This is read-only.",
		Parameters:  noArgToolParameters(),
		Execute: func(ctx context.Context, _ map[string]interface{}) (interface{}, error) {
			snapshot, err := statusSnapshot(ctx)
			if err != nil {
				return nil, err
			}
			report := buildAPIReport(snapshot)
			return map[string]interface{}{
				"generated_at":      report.GeneratedAt,
				"integration_mode":  report.IntegrationMode,
				"source_health":     report.SourceHealth,
				"overall_status":    report.OverallStatus,
				"server_summary":    report.ServerSummary,
				"server_api_status": report.Unraid,
				"app_summary": map[string]interface{}{
					"service_available": report.Docker.ServiceAvailable,
					"app_count":         report.Docker.AppCount,
					"online":            report.Docker.OnlineAppCount,
					"degraded":          report.Docker.DegradedAppCount,
					"offline":           report.Docker.OfflineAppCount,
					"unknown":           report.Docker.UnknownAppCount,
				},
			}, nil
		},
	})
	add(agentTool{
		Name:        agentToolNetworkStatus,
		Description: "Fetch the latest sanitized NoobBoard network, internet, DNS, and router status fields. This is read-only.",
		Parameters:  noArgToolParameters(),
		Execute: func(ctx context.Context, _ map[string]interface{}) (interface{}, error) {
			snapshot, err := statusSnapshot(ctx)
			if err != nil {
				return nil, err
			}
			report := buildAPIReport(snapshot)
			return map[string]interface{}{
				"generated_at":     report.GeneratedAt,
				"integration_mode": report.IntegrationMode,
				"source_health":    report.SourceHealth,
				"network_api_status": map[string]interface{}{
					"unifi":  report.UniFi,
					"probes": report.Probes,
				},
			}, nil
		},
	})
	add(agentTool{
		Name:        agentToolAppStatus,
		Description: "Fetch the latest sanitized status for one visible app by app id, display name, or container name. This is read-only.",
		Parameters: map[string]interface{}{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]interface{}{
				"app_id_or_name": map[string]interface{}{
					"type":        "string",
					"description": "The app id, display name, or container name to look up.",
				},
			},
			"required": []string{"app_id_or_name"},
		},
		Execute: func(ctx context.Context, args map[string]interface{}) (interface{}, error) {
			name := strings.TrimSpace(fmt.Sprint(args["app_id_or_name"]))
			if name == "" {
				return nil, errors.New("app_id_or_name is required")
			}
			snapshot, err := statusSnapshot(ctx)
			if err != nil {
				return nil, err
			}
			app, ok := findAgentApp(snapshot.Apps, name)
			if !ok {
				return map[string]interface{}{
					"found": false,
					"query": name,
				}, nil
			}
			return map[string]interface{}{
				"found": true,
				"app": generalAppReport{
					AppID:          app.AppID,
					DisplayName:    app.DisplayName,
					CurrentStatus:  app.CurrentStatus,
					Severity:       app.Severity,
					EndpointStatus: app.EndpointStatus,
					DockerState:    app.DockerState,
					Summary:        app.ServerSummary,
				},
				"probe_result": generalProbeResult{
					Type:      app.CurrentProbeResult.Type,
					OK:        app.CurrentProbeResult.OK,
					Message:   app.CurrentProbeResult.Message,
					LatencyMS: app.CurrentProbeResult.LatencyMS,
				},
				"data_source": app.DataSource,
			}, nil
		},
	})
	return tools
}

func allowedAgentTools(policy models.LLMPolicy) map[string]bool {
	allowed := map[string]bool{}
	for _, rule := range policy.AgentToolRules {
		tool := strings.TrimSpace(rule.Tool)
		action := strings.TrimSpace(rule.Action)
		if tool == "" || action == "" {
			continue
		}
		if tool == "*" {
			for _, name := range allReadOnlyAgentToolNames() {
				allowed[name] = action == "allow"
			}
			continue
		}
		allowed[tool] = action == "allow"
	}
	return allowed
}

func allReadOnlyAgentToolNames() []string {
	return []string{
		agentToolCurrentStatus,
		agentToolServerStatus,
		agentToolNetworkStatus,
		agentToolAppStatus,
	}
}

func ReadOnlyAgentToolNames() []string {
	return allReadOnlyAgentToolNames()
}

func noArgToolParameters() map[string]interface{} {
	return map[string]interface{}{
		"type":                 "object",
		"additionalProperties": false,
		"properties":           map[string]interface{}{},
		"required":             []string{},
	}
}

func openAIToolDefinitions(tools map[string]agentTool) []interface{} {
	if len(tools) == 0 {
		return []interface{}{}
	}
	definitions := make([]interface{}, 0, len(tools))
	for _, name := range allReadOnlyAgentToolNames() {
		tool, ok := tools[name]
		if !ok {
			continue
		}
		definitions = append(definitions, map[string]interface{}{
			"type":        "function",
			"name":        tool.Name,
			"description": tool.Description,
			"parameters":  tool.Parameters,
			"strict":      true,
		})
	}
	return definitions
}

func parseAgentToolCalls(respData []byte) ([]agentToolCall, []interface{}, error) {
	var body map[string]interface{}
	if err := json.Unmarshal(respData, &body); err != nil {
		return nil, nil, err
	}
	output, _ := body["output"].([]interface{})
	calls := make([]agentToolCall, 0)
	for _, item := range output {
		object, ok := item.(map[string]interface{})
		if !ok || object["type"] != "function_call" {
			continue
		}
		callID := strings.TrimSpace(fmt.Sprint(object["call_id"]))
		name := strings.TrimSpace(fmt.Sprint(object["name"]))
		if callID == "" || name == "" {
			continue
		}
		args := map[string]interface{}{}
		if rawArgs, ok := object["arguments"].(string); ok && strings.TrimSpace(rawArgs) != "" {
			if err := json.Unmarshal([]byte(rawArgs), &args); err != nil {
				return nil, nil, fmt.Errorf("tool %s returned invalid arguments: %w", name, err)
			}
		}
		calls = append(calls, agentToolCall{CallID: callID, Name: name, Arguments: args})
	}
	return calls, output, nil
}

func executeAgentTool(ctx context.Context, tools map[string]agentTool, call agentToolCall) (map[string]interface{}, error) {
	tool, ok := tools[call.Name]
	if !ok {
		return nil, fmt.Errorf("tool %s is not allowed", call.Name)
	}
	result, err := tool.Execute(ctx, call.Arguments)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{
		"type":    "function_call_output",
		"call_id": call.CallID,
		"output":  string(data),
	}, nil
}

func findAgentApp(apps []models.AppStatus, query string) (models.AppStatus, bool) {
	needle := strings.ToLower(strings.TrimSpace(query))
	for _, app := range apps {
		for _, candidate := range []string{app.AppID, app.DisplayName, app.ContainerName} {
			if strings.ToLower(strings.TrimSpace(candidate)) == needle {
				return app, true
			}
		}
	}
	return models.AppStatus{}, false
}
