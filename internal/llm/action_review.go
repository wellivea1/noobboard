package llm

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/wellivea1/noobboard/internal/models"
)

type ActionReviewReference struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type ActionReviewRequest struct {
	ActionID      string
	ActionTitle   string
	TargetID      string
	TargetLabel   string
	CurrentStatus models.CurrentStatus
	ActorRole     models.Role
	Via           string
	Reasoning     string
	References    []ActionReviewReference
	Snapshot      models.Snapshot
}

type ActionReviewDecision struct {
	Allow      bool      `json:"allow"`
	Confidence float64   `json:"confidence"`
	Summary    string    `json:"summary"`
	Issues     []string  `json:"issues"`
	CheckedAt  time.Time `json:"checked_at"`
}

func ValidateActionReviewDecision(data []byte) (ActionReviewDecision, error) {
	var decision ActionReviewDecision
	if err := json.Unmarshal(data, &decision); err != nil {
		return ActionReviewDecision{}, err
	}
	if decision.Confidence < 0 || decision.Confidence > 1 {
		return ActionReviewDecision{}, errors.New("confidence must be between 0 and 1")
	}
	decision.Summary = strings.TrimSpace(decision.Summary)
	if decision.Summary == "" {
		return ActionReviewDecision{}, errors.New("summary is required")
	}
	decision.Issues = compactStrings(decision.Issues, 6, 240)
	if decision.CheckedAt.IsZero() {
		decision.CheckedAt = time.Now().UTC()
	}
	return decision, nil
}

func ActionReviewJSONSchema() map[string]interface{} {
	return map[string]interface{}{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]interface{}{
			"allow": map[string]interface{}{
				"type": "boolean",
			},
			"confidence": map[string]interface{}{
				"type":    "number",
				"minimum": 0,
				"maximum": 1,
			},
			"summary": map[string]interface{}{
				"type": "string",
			},
			"issues": map[string]interface{}{
				"type":  "array",
				"items": map[string]interface{}{"type": "string"},
			},
			"checked_at": map[string]interface{}{
				"type":   "string",
				"format": "date-time",
			},
		},
		"required": []string{"allow", "confidence", "summary", "issues", "checked_at"},
	}
}

func BuildActionReviewPrompt(req ActionReviewRequest) string {
	refs := "(no reference files were provided)"
	if len(req.References) > 0 {
		var b strings.Builder
		for _, ref := range req.References {
			path := strings.TrimSpace(ref.Path)
			content := strings.TrimSpace(ref.Content)
			if path == "" || content == "" {
				continue
			}
			fmt.Fprintf(&b, "\n--- reference: %s ---\n%s\n", path, content)
		}
		if strings.TrimSpace(b.String()) != "" {
			refs = b.String()
		}
	}
	appSummary := compactActionReviewApps(req.Snapshot.Apps, req.TargetID)
	infraSummary := compactActionReviewInfra(req.Snapshot.Infrastructure)
	return fmt.Sprintf(`ACTION AUTO-REVIEW

You are a separate reviewer model deciding whether NoobBoard may execute one proposed server-side repair.
The LLM that diagnosed the issue cannot execute anything. The server will only run the fixed allowlisted action below if you allow it.

Rules:
- Use the references as policy context when they are relevant.
- Approve only if the action is a server-allowlisted app start, stop, or restart; target-specific; visible in the current snapshot; and consistent with the evidence.
- Do not invent extra actions, shell commands, Docker names, or infrastructure changes.
- If the evidence is ambiguous, the target is unclear, or the action conflicts with policy, set allow=false.
- Return only the JSON object required by the schema.

Proposed action:
- action_id: %s
- action_title: %s
- target_id: %s
- target_label: %s
- current_status: %s
- actor_role: %s
- via: %s

Current app evidence:
%s

Infrastructure summary:
%s

References:
%s`, req.ActionID, req.ActionTitle, req.TargetID, req.TargetLabel, req.CurrentStatus, req.ActorRole, req.Via, appSummary, infraSummary, refs)
}

func compactActionReviewApps(apps []models.AppStatus, targetID string) string {
	var b strings.Builder
	targetKey := strings.ToLower(strings.TrimSpace(targetID))
	for _, app := range apps {
		isTarget := strings.EqualFold(app.AppID, targetKey) || strings.EqualFold(app.ContainerName, targetKey) || strings.EqualFold(app.DisplayName, targetKey)
		if !isTarget && app.CurrentStatus == models.StatusOnline {
			continue
		}
		fmt.Fprintf(&b, "- app_id=%s name=%s status=%s visible=%t agent_repair_allowed=%t user_control_allowed=%t\n",
			app.AppID,
			firstNonEmpty(app.DisplayName, app.ContainerName, app.AppID),
			app.CurrentStatus,
			app.VisibleToGeneralUsers,
			app.AgentRepairAllowed,
			app.RestartAllowedGeneralUser,
		)
	}
	if strings.TrimSpace(b.String()) == "" {
		return "(no relevant app evidence)"
	}
	return b.String()
}

func compactActionReviewInfra(infra models.InfrastructureStatus) string {
	return fmt.Sprintf("- internet=%t router=%t server=%t docker_service=%t checked_at=%s",
		infra.InternetReachable,
		infra.RouterReachable,
		infra.NASReachable,
		infra.DockerServiceAvailable,
		infra.LastCheckedAt.UTC().Format(time.RFC3339),
	)
}

func compactStrings(values []string, limit, maxLen int) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if maxLen > 0 && len(trimmed) > maxLen {
			trimmed = trimmed[:maxLen]
		}
		out = append(out, trimmed)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
