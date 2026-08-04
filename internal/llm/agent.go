package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/wellivea1/noobboard/internal/models"
	"github.com/wellivea1/noobboard/internal/privacy"
)

const (
	agentToolCurrentStatus = "noobboard_current_status"
	agentToolServerStatus  = "noobboard_server_status"
	agentToolNetworkStatus = "noobboard_network_status"
	agentToolAppStatus     = "noobboard_app_status"
	agentToolAppLogs       = "noobboard_app_logs"
	agentToolAppHistory    = "noobboard_app_history"
)

// Ceiling on log lines returned to the model, applied regardless of what it
// asks for. Logs are the most likely place for a credential to appear, so the
// budget is deliberately small: enough to see why something died, not enough to
// stream a container's output into a provider.
const (
	agentLogLineCap     = 120
	agentLogLineDefault = 60
	agentHistoryCap     = 40
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
				"app": adminAppReport{
					AppStatus:        app,
					RestartCandidate: models.IsAppRestartCandidate(app),
				},
				"probe_result": generalProbeResult{
					Type:      app.CurrentProbeResult.Type,
					OK:        app.CurrentProbeResult.OK,
					Message:   app.CurrentProbeResult.Message,
					LatencyMS: app.CurrentProbeResult.LatencyMS,
				},
				"exit":        exitReport(app),
				"data_source": app.DataSource,
			}, nil
		},
	})
	if req.AppLogs != nil {
		add(agentTool{
			Name:        agentToolAppLogs,
			Description: "Read the most recent redacted log lines for one visible app, to find out why it failed. This is read-only and returns at most " + fmt.Sprint(agentLogLineCap) + " lines.",
			Parameters: map[string]interface{}{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]interface{}{
					"app_id_or_name": map[string]interface{}{
						"type":        "string",
						"description": "The app id, display name, or container name to read logs for.",
					},
					"limit": map[string]interface{}{
						"type":        "integer",
						"description": "How many recent lines to return, 1 to " + fmt.Sprint(agentLogLineCap) + ".",
					},
				},
				"required": []string{"app_id_or_name", "limit"},
			},
			Execute: func(ctx context.Context, args map[string]interface{}) (interface{}, error) {
				app, snapshotErr, ok := resolveAgentApp(ctx, statusSnapshot, args)
				if snapshotErr != nil {
					return nil, snapshotErr
				}
				if !ok {
					return map[string]interface{}{"found": false, "query": agentAppQuery(args)}, nil
				}
				lines, err := req.AppLogs(ctx, app.AppID, agentLogLimit(args["limit"]))
				if err != nil {
					return nil, err
				}
				return map[string]interface{}{
					"found":          true,
					"app_id":         app.AppID,
					"container_name": app.ContainerName,
					"line_count":     len(lines),
					"logs":           lines,
					"note":           "Lines are redacted before they reach this tool. Never repeat a value that looks like a secret even if one appears here.",
				}, nil
			},
		})
	}
	if req.AppHistory != nil {
		add(agentTool{
			Name:        agentToolAppHistory,
			Description: "Read recent status transitions for one visible app, to tell a first-time failure apart from a repeating one. This is read-only.",
			Parameters: map[string]interface{}{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]interface{}{
					"app_id_or_name": map[string]interface{}{
						"type":        "string",
						"description": "The app id, display name, or container name to read history for.",
					},
				},
				"required": []string{"app_id_or_name"},
			},
			Execute: func(ctx context.Context, args map[string]interface{}) (interface{}, error) {
				app, snapshotErr, ok := resolveAgentApp(ctx, statusSnapshot, args)
				if snapshotErr != nil {
					return nil, snapshotErr
				}
				if !ok {
					return map[string]interface{}{"found": false, "query": agentAppQuery(args)}, nil
				}
				history, err := req.AppHistory(ctx, app.AppID)
				if err != nil {
					return nil, err
				}
				events := history.Events
				if len(events) > agentHistoryCap {
					events = events[len(events)-agentHistoryCap:]
				}
				return map[string]interface{}{
					"found":          true,
					"app_id":         app.AppID,
					"display_name":   history.DisplayName,
					"current_status": history.Current,
					"uptime_pct_24h": history.UptimePct24h,
					"event_count":    len(events),
					"events":         events,
					"note":           "Repeated offline/online transitions in a short window mean a restart loop. Restarting again will not fix that; say so instead of recommending it.",
				}, nil
			},
		})
	}
	return tools
}

// Resolution always goes through the role-filtered snapshot, so a tool can only
// ever name an app the requesting role is allowed to see.
func resolveAgentApp(ctx context.Context, statusSnapshot func(context.Context) (models.Snapshot, error), args map[string]interface{}) (models.AppStatus, error, bool) {
	name := agentAppQuery(args)
	if name == "" {
		return models.AppStatus{}, errors.New("app_id_or_name is required"), false
	}
	snapshot, err := statusSnapshot(ctx)
	if err != nil {
		return models.AppStatus{}, err, false
	}
	app, ok := findAgentApp(snapshot.Apps, name)
	return app, nil, ok
}

func agentAppQuery(args map[string]interface{}) string {
	return strings.TrimSpace(fmt.Sprint(args["app_id_or_name"]))
}

// The model's requested limit is a hint. The cap is not.
func agentLogLimit(raw interface{}) int {
	limit := agentLogLineDefault
	switch value := raw.(type) {
	case float64:
		limit = int(value)
	case int:
		limit = value
	case string:
		if parsed, err := strconv.Atoi(strings.TrimSpace(value)); err == nil {
			limit = parsed
		}
	}
	if limit < 1 {
		return 1
	}
	if limit > agentLogLineCap {
		return agentLogLineCap
	}
	return limit
}

func exitReport(app models.AppStatus) map[string]interface{} {
	if app.DockerExitCode == nil {
		return nil
	}
	return map[string]interface{}{
		"code":   *app.DockerExitCode,
		"reason": app.DockerExitReason,
		"detail": models.ExitDetail(app.DockerExitCode, app.DockerExitReason),
	}
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
		agentToolAppLogs,
		agentToolAppHistory,
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
