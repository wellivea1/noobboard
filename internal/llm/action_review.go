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
	ActionID    string
	ActionTitle string
	// Operation is the concrete thing the server will run: start, stop or
	// restart. Without it the reviewer only saw a recommendation id, and it
	// refused correctly — "action_id suggests asking an admin to restart a
	// container, while action_title says start recommendation" — because
	// nothing in the prompt named an allowlisted operation.
	Operation     string
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
	// A review with no concrete operation is not reviewable. Failing closed here
	// beats sending a prompt that invites the reviewer to guess.
	operation := strings.ToLower(strings.TrimSpace(req.Operation))
	if operation == "" {
		operation = "(none supplied - refuse this review)"
	}
	appSummary := compactActionReviewApps(req.Snapshot.Apps, req.TargetID)
	infraSummary := compactActionReviewInfra(req.Snapshot.Infrastructure)
	return fmt.Sprintf(`ACTION AUTO-REVIEW

You are a separate reviewer model deciding whether NoobBoard may execute one proposed server-side repair.
The LLM that diagnosed the issue cannot execute anything. The server will only run the fixed allowlisted action below if you allow it.

What will run if you allow it:
NoobBoard will execute exactly one Docker operation — "%s" — against the single target below. It cannot run anything else.
The operation is already restricted to the server allowlist (start, stop, restart); you are not being asked to verify that.

Your job is to decide whether that operation is *warranted* by the evidence:
- Does the target's current state justify this operation?
- Is the target unambiguous and present in the snapshot below?
- Do the references (when relevant) forbid it?

Rules:
- Judge the operation named above. The recommendation_id and title are provenance only — they describe where the suggestion came from, not what will run, and their wording may differ from the operation.
- Set allow=false if the evidence does not justify the operation, the target is unclear, or policy forbids it.
- Do not invent extra actions, shell commands, Docker names, or infrastructure changes.
- Return only the JSON object required by the schema.

Operation to review:
- operation: %s
- target_id: %s
- target_label: %s
- current_status: %s
- actor_role: %s
- via: %s

Provenance (not the action):
- recommendation_id: %s
- recommendation_title: %s

Current app evidence:
%s

Infrastructure summary:
%s

References:
%s`, operation, operation, req.TargetID, req.TargetLabel, req.CurrentStatus, req.ActorRole, req.Via, req.ActionID, req.ActionTitle, appSummary, infraSummary, refs)
}

func compactActionReviewApps(apps []models.AppStatus, targetID string) string {
	var b strings.Builder
	targetKey := strings.ToLower(strings.TrimSpace(targetID))
	for _, app := range apps {
		isTarget := strings.EqualFold(app.AppID, targetKey) || strings.EqualFold(app.ContainerName, targetKey) || strings.EqualFold(app.DisplayName, targetKey)
		if !isTarget && app.CurrentStatus == models.StatusOnline {
			continue
		}
		// The reviewer is asked whether the operation is *warranted*, so it needs
		// the evidence, not just the status word. A live denial once read "no
		// evidence is provided that a restart is needed" because this line
		// carried no docker state, health, or exit detail.
		marker := "-"
		if isTarget {
			marker = "*"
		}
		fmt.Fprintf(&b, "%s app_id=%s name=%s status=%s docker_state=%s health=%s visible=%t agent_repair_allowed=%t user_control_allowed=%t",
			marker,
			app.AppID,
			firstNonEmpty(app.DisplayName, app.ContainerName, app.AppID),
			app.CurrentStatus,
			firstNonEmpty(string(app.DockerState), "unknown"),
			firstNonEmpty(string(app.DockerHealth), "none"),
			app.VisibleToGeneralUsers,
			app.AgentRepairAllowed,
			app.RestartAllowedGeneralUser,
		)
		if detail := models.ExitDetail(app.DockerExitCode, app.DockerExitReason); detail != "" {
			fmt.Fprintf(&b, " last_exit=%q", detail)
		}
		if app.RecentStatusChanges > 0 {
			fmt.Fprintf(&b, " recent_status_changes=%d", app.RecentStatusChanges)
		}
		b.WriteString("\n")
	}
	if strings.TrimSpace(b.String()) == "" {
		return "(no relevant app evidence)"
	}
	return "Lines marked * are the target of the proposed operation.\n" + b.String()
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
