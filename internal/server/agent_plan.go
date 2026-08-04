package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/wellivea1/noobboard/internal/adapters/docker"
	"github.com/wellivea1/noobboard/internal/config"
	"github.com/wellivea1/noobboard/internal/llm"
	"github.com/wellivea1/noobboard/internal/models"
	"github.com/wellivea1/noobboard/internal/privacy"
	"github.com/wellivea1/noobboard/internal/users"
)

// Diagnosis and the agent plan derived from it. The model chooses only an
// allow-listed action id and a target hint; everything that decides what will
// actually run - the action registry, target resolution, the deterministic
// backstops - is here, on the server.

func (a *App) adminDiagnose(w http.ResponseWriter, r *http.Request) {
	a.diagnose(w, r, llm.ModeAdminRequested, models.RoleAdmin)
}

func (a *App) userDiagnose(w http.ResponseWriter, r *http.Request) {
	a.settingsMu.RLock()
	visibility := a.deps.Config.Visibility
	role := compactDiagnosisRole(mustUser(r).Role, visibility.DefaultRole)
	allowed := roleCanUseLLM(visibility, role)
	a.settingsMu.RUnlock()
	if !allowed {
		writeError(w, http.StatusForbidden, errors.New("status chat is disabled for this role"))
		return
	}
	a.diagnose(w, r, llm.ModeGeneralUserRequested, role)
}

func (a *App) diagnose(w http.ResponseWriter, r *http.Request, mode llm.Mode, role models.Role) {
	cfg, llmClient := a.llmRuntimeSnapshot()
	if !cfg.LLM.Enabled {
		writeError(w, http.StatusForbidden, errors.New("llm is disabled"))
		return
	}
	if !llm.ProviderAvailable(cfg.LLM) {
		writeError(w, http.StatusForbidden, errors.New("diagnostics require NOOBBOARD_LLM_PROVIDER=openai or anthropic with a matching API key, or an OpenAI ChatGPT connector configured in LLM settings"))
		return
	}
	if err := requireCSRF(r); err != nil {
		writeError(w, http.StatusForbidden, err)
		return
	}
	var body struct {
		Question   string `json:"question"`
		AutoRepair bool   `json:"auto_repair"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	body.Question = strings.TrimSpace(body.Question)
	if len(body.Question) > 1000 {
		body.Question = body.Question[:1000]
	}
	full, err := a.readOnlySnapshot(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	policy, ok := cfg.LLM.Policies[string(mode)]
	if !ok {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("missing llm policy %s", mode))
		return
	}
	policy.RecipientRole = role
	diagnosis, err := llmClient.Diagnose(r.Context(), llm.Request{
		Mode:         mode,
		Policy:       policy,
		Snapshot:     full,
		Question:     body.Question,
		ActorID:      mustUser(r).ID,
		LiveSnapshot: a.readOnlySnapshot,
		AppLogs:      a.agentAppLogs(role),
		AppHistory:   a.agentAppHistory(role),
		ToolAudit: func(name string, ok bool, errText string) {
			details := map[string]interface{}{"mode": string(mode), "tool": name, "ok": ok}
			if errText != "" {
				details["error"] = errText
			}
			a.deps.Audit.Record(mustUser(r).ID, "llm.agent_tool", details)
		},
	})
	if err != nil {
		a.deps.Audit.Record(mustUser(r).ID, "llm.failed", map[string]interface{}{"mode": string(mode), "error": err.Error()})
		if llm.IsOpenAIUsageLimitError(err) {
			writeJSON(w, http.StatusTooManyRequests, map[string]string{
				"error": "OpenAI usage limit reached. Wait for the limit to reset or check the OpenAI plan in LLM settings.",
				"code":  llm.OpenAIUsageLimitCode,
			})
			return
		}
		writeError(w, http.StatusBadGateway, err)
		return
	}
	a.deps.Audit.Record(mustUser(r).ID, "llm.diagnosis", map[string]interface{}{"mode": string(mode), "incident_type": string(diagnosis.IncidentType), "admin_message": diagnosis.AdminMessage})
	response := diagnosisResponse{Diagnosis: diagnosis}
	if mode == llm.ModeAdminRequested && role == models.RoleAdmin {
		planDiagnosis, arraySuggested := arrayStartBackstopDiagnosis(diagnosis, full)
		if arraySuggested {
			response.Diagnosis = planDiagnosis
		}
		planDiagnosis, suggested := adminRestartBackstopDiagnosis(planDiagnosis, full, a.deps.Config.LLM.RestartSuggestionEnabled())
		response.AgentPlan = a.llmAgentPlanResponse(planDiagnosis, full, mustUser(r).ID)
		if arraySuggested {
			markSuggestedArrayStartPlan(response.AgentPlan)
			a.auditSuggestedArrayStartPlan(mustUser(r).ID, string(mode), response.AgentPlan)
		}
		if suggested {
			markSuggestedRestartPlan(response.AgentPlan, "NoobBoard found one repair-eligible app that is down, so this restart affordance is suggested even though the model did not request it. Existing approval and safety gates still apply.")
			a.auditSuggestedRestartPlan(mustUser(r).ID, string(mode), response.AgentPlan)
		}
		if body.AutoRepair {
			a.maybeExecuteAgentAutoRepair(r.Context(), mustUser(r), full, response.AgentPlan)
		}
	} else if mode == llm.ModeGeneralUserRequested {
		filtered := privacy.FilterSnapshotForRole(full, role, a.redactorSnapshot())
		planDiagnosis, arraySuggested := arrayStartBackstopDiagnosis(diagnosis, filtered)
		if arraySuggested {
			response.Diagnosis = planDiagnosis
		}
		planDiagnosis, suggested := generalUserRestartBackstopDiagnosis(planDiagnosis, filtered, a.deps.Config.LLM.RestartSuggestionEnabled())
		response.AgentPlan = a.llmUserRepairPlanResponse(planDiagnosis, filtered, mustUser(r).ID)
		if arraySuggested {
			markSuggestedArrayStartPlan(response.AgentPlan)
			a.auditSuggestedArrayStartPlan(mustUser(r).ID, string(mode), response.AgentPlan)
		}
		if suggested {
			markSuggestedRestartPlan(response.AgentPlan, "NoobBoard found one visible app that is not working, so this fix option is suggested even though the model did not request it.")
			a.auditSuggestedRestartPlan(mustUser(r).ID, string(mode), response.AgentPlan)
		}
		if body.AutoRepair {
			a.maybeExecuteGeneralUserAutoRepair(r.Context(), mustUser(r), response.AgentPlan)
		}
	}
	writeJSON(w, http.StatusOK, response)
}

func (a *App) llmAgentPlanResponse(diagnosis llm.Diagnosis, snapshot models.Snapshot, actorID string) *llmAgentPlanView {
	action, known := agentActionDefinition(diagnosis.RecommendedActionID)
	target := resolveAgentPlanTarget(action, diagnosis, snapshot)
	requiresApproval := known && action.ApprovalEligible && (!action.RequiresAppTarget || target.Resolved)
	a.settingsMu.RLock()
	llmCfg := a.deps.Config.LLM
	redactor := a.deps.Redactor
	a.settingsMu.RUnlock()
	status, canExecute, allowReason := a.agentPlanExecutionState(action, target, snapshot, llmCfg, redactor)
	planAction := action
	var limit agentRepairLimitDecision
	if action.Executable && target.Resolved {
		if app, ok := findAppByID(snapshot.Apps, target.ID); ok {
			planAction = agentRepairActionForApp(action, app)
			limit = a.agentRepairLimitState(app.AppID, time.Now().UTC(), false)
		}
	}
	expiresAt := time.Now().UTC().Add(5 * time.Minute)
	approvalToken := ""
	if requiresApproval {
		nonce, err := randomToken()
		if err != nil {
			nonce = ""
		}
		approvalToken = a.signAgentApprovalToken(agentApprovalTokenPayload{
			PlanID:              agentApprovalPlanID,
			ActorID:             actorID,
			RecommendedActionID: action.ID,
			TargetKind:          target.Kind,
			TargetID:            target.ID,
			Nonce:               nonce,
			ExpiresAt:           expiresAt.Unix(),
		})
	}
	allowLabel := "Allow fix"
	allowDescription := "Permit this single fix attempt."
	if strings.TrimSpace(string(planAction.DockerAction)) != "" {
		actionText := strings.ToLower(dockerActionDisplayName(planAction.DockerAction))
		allowLabel = "Allow " + actionText
		allowDescription = "Permit this single " + actionText + " attempt."
	}
	response := &llmAgentPlanView{
		ID:                    agentApprovalPlanID,
		Title:                 planAction.Title,
		Summary:               planAction.Summary,
		RecommendedActionID:   action.ID,
		DirectAction:          string(planAction.DockerAction),
		ActionKnown:           known,
		ApprovalToken:         approvalToken,
		ApprovalExpiresAt:     expiresAt,
		RequiresAdminApproval: requiresApproval,
		CanExecute:            canExecute,
		Status:                status,
		RepairCooldownSeconds: int(agentRepairPerAppCooldown / time.Second),
		RetryAfterSeconds:     limit.RetryAfterSeconds,
		RateLimitReason:       limit.Reason,
		Target:                target,
		Options: []llmAgentPlanOptionView{
			{
				ID:          "deny",
				Label:       "Do not allow",
				Description: "Keep the diagnosis and do not permit an automatic fix.",
				Enabled:     true,
				Selected:    true,
			},
			{
				ID:          "allow_once",
				Label:       allowLabel,
				Description: allowDescription,
				Enabled:     canExecute,
				Reason:      allowReason,
			},
		},
	}
	if requiresApproval {
		if !limit.Allowed && limit.RetryAfterSeconds > 0 {
			retryAt := time.Now().UTC().Add(limit.RetryAfter)
			response.RetryAt = &retryAt
		}
		a.deps.Audit.Record(actorID, "llm.agent_plan.proposed", map[string]interface{}{
			"plan_id":               response.ID,
			"recommended_action_id": action.ID,
			"target_kind":           target.Kind,
			"target_id":             target.ID,
			"target_resolved":       target.Resolved,
			"status":                status,
			"can_execute":           canExecute,
		})
	}
	return response
}

func (a *App) maybeExecuteAgentAutoRepair(ctx context.Context, actor users.User, snapshot models.Snapshot, plan *llmAgentPlanView) {
	if plan == nil || !plan.RequiresAdminApproval || !plan.CanExecute {
		return
	}
	a.settingsMu.RLock()
	cfg := a.deps.Config.LLM
	redactor := a.deps.Redactor
	a.settingsMu.RUnlock()
	if !cfg.AgentControlEnabled || !cfg.ActionAutoReviewEnabled {
		return
	}
	action, ok := agentActionDefinition(plan.RecommendedActionID)
	if !ok || !action.Executable || action.DockerAction != docker.ActionRestart || plan.Target.Kind != "app" || !plan.Target.Resolved {
		return
	}
	status, canExecute, reason := a.agentPlanExecutionState(action, plan.Target, snapshot, cfg, redactor)
	if !canExecute {
		return
	}
	app, ok := findAppByID(snapshot.Apps, plan.Target.ID)
	if !ok {
		return
	}
	executionAction := agentRepairActionForApp(action, app)
	if currentStatusOrUnknown(app.CurrentStatus) == models.StatusOnline {
		return
	}
	details := map[string]interface{}{
		"plan_id":                 plan.ID,
		"recommended_action_id":   action.ID,
		"target_kind":             plan.Target.Kind,
		"target_id":               plan.Target.ID,
		"app_id":                  app.AppID,
		"container_name":          app.ContainerName,
		"docker_action":           string(executionAction.DockerAction),
		"can_execute":             canExecute,
		"pre_execution_status":    status,
		"pre_execution_reason":    reason,
		"current_status":          string(currentStatusOrUnknown(app.CurrentStatus)),
		"action_auto_review_used": true,
	}
	reviewDecision, reviewEnabled, err := a.reviewAgentAction(ctx, actor, snapshot, app, executionAction, "agent_auto_repair")
	if reviewEnabled {
		details["auto_review_allow"] = reviewDecision.Allow
		details["auto_review_confidence"] = reviewDecision.Confidence
		details["auto_review_summary"] = reviewDecision.Summary
	}
	if err != nil {
		details["reason"] = "auto_review_refused"
		details["error"] = err.Error()
		a.deps.Audit.Record(actor.ID, "llm.agent_auto_repair.auto_review_refused", auditDetailsCopy(details))
		markAgentPlanAutoRepairRefused(plan, "auto_review_refused", err.Error())
		return
	}
	limit := a.reserveAgentRepair(app.AppID, time.Now().UTC())
	if !limit.Allowed {
		details["reason"] = limit.Reason
		details["retry_after_seconds"] = limit.RetryAfterSeconds
		a.deps.Audit.Record(actor.ID, "llm.agent_auto_repair.rate_limited", auditDetailsCopy(details))
		markAgentPlanAutoRepairRefused(plan, "approval_rate_limited", limit.Message)
		return
	}
	a.deps.Audit.Record(actor.ID, "llm.agent_auto_repair.approved", auditDetailsCopy(details))
	result, err := a.deps.Collectors.Docker.ControlContainer(ctx, app, executionAction.DockerAction)
	if err != nil {
		details["error"] = err.Error()
		a.deps.Audit.Record(actor.ID, "llm.agent_auto_repair.execute_failed", auditDetailsCopy(details))
		markAgentPlanAutoRepairRefused(plan, "auto_execute_failed", err.Error())
		return
	}
	details["via"] = "agent_auto_repair"
	a.invalidateSnapshot()
	a.deps.Audit.Record(actor.ID, "llm.agent_auto_repair.executed", auditDetailsCopy(details))
	a.deps.Audit.Record(actor.ID, "app.container.action", map[string]interface{}{"app_id": app.AppID, "action": string(executionAction.DockerAction), "container_name": app.ContainerName, "via": "agent_auto_repair", "plan_id": plan.ID, "recommended_action_id": action.ID})
	outcome := a.verifyAgentRepairOutcome(ctx, app, executionAction, result)
	verifyDetails := auditDetailsCopy(details)
	verifyDetails["verified"] = outcome.Verified
	verifyDetails["recovered"] = outcome.Recovered
	verifyDetails["before_status"] = string(outcome.BeforeStatus)
	verifyDetails["after_status"] = string(outcome.AfterStatus)
	verifyDetails["history_event_id"] = outcome.HistoryEventID
	if outcome.Verified {
		a.deps.Audit.Record(actor.ID, "llm.agent_auto_repair.verified", verifyDetails)
	} else {
		a.deps.Audit.Record(actor.ID, "llm.agent_auto_repair.verify_failed", verifyDetails)
	}
	plan.AutoRepairAttempted = true
	plan.AutoExecuted = true
	plan.AutoRepairMessage = outcome.Message
	plan.Outcome = &outcome
	plan.Status = "auto_executed"
	plan.CanExecute = false
	plan.RequiresAdminApproval = false
	plan.ApprovalToken = ""
	plan.ApprovalExpiresAt = time.Time{}
	disableAgentPlanAllowOption(plan, outcome.Message)
}

func markAgentPlanAutoRepairRefused(plan *llmAgentPlanView, status, message string) {
	plan.AutoRepairAttempted = true
	plan.AutoRepairMessage = strings.TrimSpace(message)
	plan.Status = status
	plan.CanExecute = false
	plan.RequiresAdminApproval = false
	plan.ApprovalToken = ""
	plan.ApprovalExpiresAt = time.Time{}
	disableAgentPlanAllowOption(plan, plan.AutoRepairMessage)
}

func disableAgentPlanAllowOption(plan *llmAgentPlanView, reason string) {
	for i := range plan.Options {
		if plan.Options[i].ID != "allow_once" {
			continue
		}
		plan.Options[i].Enabled = false
		plan.Options[i].Reason = strings.TrimSpace(reason)
	}
}

func (a *App) llmUserRepairPlanResponse(diagnosis llm.Diagnosis, snapshot models.Snapshot, actorID string) *llmAgentPlanView {
	action, known := agentActionDefinition(diagnosis.RecommendedActionID)
	target := resolveAgentPlanTarget(action, diagnosis, snapshot)
	if action.ID == arrayStartActionID {
		return a.llmUserArrayStartPlanResponse(action, known, target, snapshot, actorID)
	}
	canRequest := known && action.Executable && action.DockerAction == docker.ActionRestart && target.Resolved
	canExecute := false
	directAction := docker.ActionRestart
	status := "not_actionable"
	reason := ""
	if canRequest {
		status = "request_available"
		if app, ok := findAppByID(snapshot.Apps, target.ID); ok {
			switch {
			case app.RestartAllowedGeneralUser && currentStatusOrUnknown(app.CurrentStatus) != models.StatusOnline:
				directAction = preferredGeneralUserRepairAction(app)
				canExecute = true
				status = "direct_" + string(directAction) + "_available"
			case app.RestartAllowedGeneralUser:
				reason = "This app is currently working."
			default:
				reason = "Ask an admin to review this app."
			}
		}
	} else if known && action.RequiresAppTarget && !target.Resolved {
		status = "target_unresolved"
		reason = target.Reason
	}
	title := action.Title
	summary := action.Summary
	if canExecute {
		actionLabel := dockerActionDisplayName(directAction)
		title = actionLabel + " app"
		summary = fmt.Sprintf("NoobBoard can %s this opted-in app from the standard-user view.", strings.ToLower(actionLabel))
	}
	directActionValue := ""
	if canExecute {
		directActionValue = string(directAction)
	}
	return &llmAgentPlanView{
		ID:                    agentApprovalPlanID,
		Title:                 title,
		Summary:               summary,
		RecommendedActionID:   action.ID,
		DirectAction:          directActionValue,
		ActionKnown:           known,
		RequiresAdminApproval: false,
		CanExecute:            canExecute,
		CanRequestRepair:      canRequest,
		Status:                status,
		Target:                target,
		Options: []llmAgentPlanOptionView{
			{
				ID:          string(directAction) + "_now",
				Label:       dockerActionDisplayName(directAction) + " now",
				Description: dockerActionDisplayName(directAction) + " this app from NoobBoard.",
				Enabled:     canExecute,
				Selected:    canExecute,
				Reason:      reason,
			},
			{
				ID:          "request_admin",
				Label:       "Ask admin",
				Description: "Ask an admin to review and fix this app.",
				Enabled:     canRequest,
				Selected:    canRequest && !canExecute,
				Reason:      reason,
			},
		},
	}
}

func (a *App) llmUserArrayStartPlanResponse(action llmAgentActionDefinition, known bool, target llmAgentPlanTargetView, snapshot models.Snapshot, actorID string) *llmAgentPlanView {
	canExecute := known && arrayStartNeeded(snapshot.Infrastructure)
	status := "not_actionable"
	reason := ""
	if canExecute {
		status = "direct_array_start_available"
	} else {
		reason = "The array is not currently stopped."
	}
	expiresAt := time.Now().UTC().Add(5 * time.Minute)
	executionToken := ""
	if canExecute {
		nonce, err := randomToken()
		if err != nil {
			canExecute = false
			status = "approval_locked"
			reason = "NoobBoard could not create a safe one-use action token."
		} else {
			executionToken = a.signAgentApprovalToken(agentApprovalTokenPayload{
				PlanID:              agentApprovalPlanID,
				ActorID:             actorID,
				RecommendedActionID: action.ID,
				TargetKind:          target.Kind,
				TargetID:            target.ID,
				Nonce:               nonce,
				ExpiresAt:           expiresAt.Unix(),
			})
		}
	}
	return &llmAgentPlanView{
		ID:                  agentApprovalPlanID,
		Title:               action.Title,
		Summary:             action.Summary,
		RecommendedActionID: action.ID,
		DirectAction:        "start_array",
		ActionKnown:         known,
		CanExecute:          canExecute,
		Status:              status,
		ExecutionToken:      executionToken,
		ApprovalExpiresAt:   expiresAt,
		Target:              target,
		Options: []llmAgentPlanOptionView{
			{
				ID:          "start_array_now",
				Label:       "Start array",
				Description: "Start the server storage array after checking with the admin first when possible.",
				Enabled:     canExecute,
				Selected:    canExecute,
				Reason:      reason,
			},
		},
	}
}

func adminRestartBackstopDiagnosis(diagnosis llm.Diagnosis, snapshot models.Snapshot, enabled bool) (llm.Diagnosis, bool) {
	if !enabled {
		return diagnosis, false
	}
	if !canBackstopRestartAction(diagnosis.RecommendedActionID) {
		return diagnosis, false
	}
	app, ok := exactlyOneRestartBackstopCandidate(snapshot.Apps, func(app models.AppStatus) bool {
		return app.AgentRepairAllowed
	})
	if !ok {
		return diagnosis, false
	}
	return restartBackstopDiagnosis(diagnosis, app), true
}

func arrayStartBackstopDiagnosis(diagnosis llm.Diagnosis, snapshot models.Snapshot) (llm.Diagnosis, bool) {
	if !arrayStartNeeded(snapshot.Infrastructure) {
		return diagnosis, false
	}
	return arrayStartGuidedDiagnosis(diagnosis, snapshot.Infrastructure.UnraidArrayState), true
}

func generalUserRestartBackstopDiagnosis(diagnosis llm.Diagnosis, snapshot models.Snapshot, enabled bool) (llm.Diagnosis, bool) {
	if !enabled {
		return diagnosis, false
	}
	if !canBackstopRestartAction(diagnosis.RecommendedActionID) {
		return diagnosis, false
	}
	app, ok := exactlyOneRestartBackstopCandidate(snapshot.Apps, func(app models.AppStatus) bool {
		return true
	})
	if !ok {
		return diagnosis, false
	}
	return restartBackstopDiagnosis(diagnosis, app), true
}

func canBackstopRestartAction(actionID string) bool {
	switch strings.TrimSpace(actionID) {
	case "", "none", "unknown", "ask_admin_to_check":
		return true
	default:
		return false
	}
}

func arrayStartNeeded(infra models.InfrastructureStatus) bool {
	if !infra.UnraidAPIReachable {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(infra.UnraidArrayState)) {
	case "stopped", "offline", "off", "down":
		return true
	default:
		return false
	}
}

func exactlyOneRestartBackstopCandidate(apps []models.AppStatus, eligible func(models.AppStatus) bool) (models.AppStatus, bool) {
	var found models.AppStatus
	count := 0
	for _, app := range apps {
		if !models.IsAppRestartCandidate(app) || !eligible(app) {
			continue
		}
		found = app
		count++
		if count > 1 {
			return models.AppStatus{}, false
		}
	}
	return found, count == 1
}

func restartBackstopDiagnosis(diagnosis llm.Diagnosis, app models.AppStatus) llm.Diagnosis {
	diagnosis.RecommendedActionID = "ask_admin_to_restart_container"
	diagnosis.RecommendedTarget = llm.ActionTarget{Kind: "app", IDOrName: app.AppID}
	if len(diagnosis.AffectedServices) == 0 {
		diagnosis.AffectedServices = []string{firstNonEmpty(app.DisplayName, app.AppID)}
	}
	return diagnosis
}

func arrayStartGuidedDiagnosis(diagnosis llm.Diagnosis, state string) llm.Diagnosis {
	state = strings.TrimSpace(state)
	if state == "" {
		state = "stopped"
	}
	diagnosis.Severity = models.SeverityHigh
	if diagnosis.Confidence <= 0 {
		diagnosis.Confidence = 0.9
	}
	diagnosis.IncidentType = models.IncidentArrayStopped
	diagnosis.AffectedServices = []string{"Unraid array"}
	diagnosis.Diagnosis = "The Unraid array is " + state + ", so apps that depend on server storage may not be able to run."
	diagnosis.GeneralUserSummary = "The server storage is stopped. Contact the admin first to make sure it was not stopped on purpose. If the admin is unavailable or asleep and service needs to be restored, it is okay to start the array."
	diagnosis.AdminMessage = "The Unraid array is " + state + ". Confirm it was not intentionally stopped; if service needs to be restored, start the array."
	diagnosis.RecommendedActionID = arrayStartActionID
	diagnosis.RecommendedTarget = llm.ActionTarget{Kind: "storage", IDOrName: arrayTargetID}
	diagnosis.ShouldNotifyAdmin = true
	evidence := "Unraid API reports array state " + state
	for _, existing := range diagnosis.Evidence {
		if strings.EqualFold(strings.TrimSpace(existing), evidence) {
			return diagnosis
		}
	}
	diagnosis.Evidence = append(diagnosis.Evidence, evidence)
	return diagnosis
}

func markSuggestedRestartPlan(plan *llmAgentPlanView, summary string) {
	if plan == nil || plan.RecommendedActionID != "ask_admin_to_restart_container" {
		return
	}
	plan.Title = "Suggested restart"
	plan.Summary = summary
}

func markSuggestedArrayStartPlan(plan *llmAgentPlanView) {
	if plan == nil || plan.RecommendedActionID != arrayStartActionID {
		return
	}
	plan.Title = "Start server storage"
	plan.Summary = "Server storage is stopped. Contact the admin first to confirm it was not stopped intentionally; if the admin is unavailable or asleep and service needs to be restored, starting it is okay."
}

func (a *App) auditSuggestedRestartPlan(actorID, mode string, plan *llmAgentPlanView) {
	if plan == nil {
		return
	}
	a.deps.Audit.Record(actorID, "llm.agent_plan.suggested", map[string]interface{}{
		"mode":                  mode,
		"plan_id":               plan.ID,
		"recommended_action_id": plan.RecommendedActionID,
		"target_kind":           plan.Target.Kind,
		"target_id":             plan.Target.ID,
		"target_resolved":       plan.Target.Resolved,
		"status":                plan.Status,
		"can_execute":           plan.CanExecute,
		"can_request_repair":    plan.CanRequestRepair,
	})
}

func (a *App) auditSuggestedArrayStartPlan(actorID, mode string, plan *llmAgentPlanView) {
	if plan == nil {
		return
	}
	a.deps.Audit.Record(actorID, "llm.agent_plan.array_start_suggested", map[string]interface{}{
		"mode":                  mode,
		"plan_id":               plan.ID,
		"recommended_action_id": plan.RecommendedActionID,
		"target_kind":           plan.Target.Kind,
		"target_id":             plan.Target.ID,
		"status":                plan.Status,
		"can_execute":           plan.CanExecute,
	})
}

func (a *App) maybeExecuteGeneralUserAutoRepair(ctx context.Context, actor users.User, plan *llmAgentPlanView) {
	if plan == nil || !plan.CanExecute || !plan.CanRequestRepair || strings.TrimSpace(plan.Target.ID) == "" {
		return
	}
	a.settingsMu.RLock()
	enabled := a.deps.Config.AppCatalog.GeneralUserAutoRepairEnabled
	a.settingsMu.RUnlock()
	if !enabled {
		return
	}
	action := docker.ContainerAction(strings.TrimSpace(plan.DirectAction))
	if action == "" {
		action = docker.ActionRestart
	}
	execution, failure := a.executeGeneralUserAppAction(ctx, actor, plan.Target.ID, action, "general_user_auto_repair", "Auto-fix")
	plan.AutoRepairAttempted = true
	plan.CanExecute = false
	plan.RequiresAdminApproval = false
	plan.ApprovalToken = ""
	plan.ApprovalExpiresAt = time.Time{}
	if failure != nil {
		status := strings.TrimSpace(failure.PlanStatus)
		if status == "" {
			status = "auto_execute_failed"
		}
		plan.Status = status
		plan.AutoRepairMessage = failure.Error()
		disableAgentPlanAllowOption(plan, plan.AutoRepairMessage)
		return
	}
	plan.AutoExecuted = true
	plan.Status = "auto_executed"
	plan.AutoRepairMessage = execution.Outcome.Message
	plan.Outcome = &execution.Outcome
	disableAgentPlanAllowOption(plan, execution.Outcome.Message)
}

type llmAgentActionDefinition struct {
	ID                string
	Title             string
	Summary           string
	ApprovalEligible  bool
	RequiresAppTarget bool
	Executable        bool
	DockerAction      docker.ContainerAction
}

var llmAgentActionRegistry = map[string]llmAgentActionDefinition{
	"none": {
		ID:      "none",
		Title:   "No action recommended",
		Summary: "The model did not recommend an admin action.",
	},
	"unknown": {
		ID:      "unknown",
		Title:   "Unclear recommendation",
		Summary: "The model did not return a specific action that NoobBoard can place behind an approval popup.",
	},
	"ask_admin_to_check": {
		ID:      "ask_admin_to_check",
		Title:   "Manual check recommendation",
		Summary: "The model suggested an admin check. NoobBoard will not run a mutating action for this recommendation.",
	},
	"ask_admin_to_restart_container": {
		ID:                "ask_admin_to_restart_container",
		Title:             "App fix recommendation",
		Summary:           "The model suggested repairing one app. NoobBoard can start a stopped app or restart a running app only after admin approval, safety review, and per-app opt-in.",
		ApprovalEligible:  true,
		RequiresAppTarget: true,
		Executable:        true,
		DockerAction:      docker.ActionRestart,
	},
	arrayStartActionID: {
		ID:         arrayStartActionID,
		Title:      "Start array",
		Summary:    "The model identified that the Unraid array is stopped. NoobBoard can start the array from compact chat only after the signed LLM plan is used.",
		Executable: true,
	},
	unifiRestartActionID: {
		ID:      unifiRestartActionID,
		Title:   "Restart offline network device",
		Summary: "The model identified an offline UniFi device. NoobBoard can restart it from the Router page after an admin confirms; only offline, non-gateway devices are eligible.",
	},
	"ask_admin_to_check_unifi": {
		ID:      "ask_admin_to_check_unifi",
		Title:   "Network check recommendation",
		Summary: "The model suggested checking router or network status. NoobBoard executes no other network repair action.",
	},
	"ask_admin_to_check_storage": {
		ID:      "ask_admin_to_check_storage",
		Title:   "Storage check recommendation",
		Summary: "The model suggested checking server storage. Chat cannot run Unraid storage actions.",
	},
}

func agentActionDefinition(id string) (llmAgentActionDefinition, bool) {
	actionID := strings.TrimSpace(id)
	if actionID == "" {
		actionID = "unknown"
	}
	action, ok := llmAgentActionRegistry[actionID]
	if ok {
		return action, true
	}
	return llmAgentActionRegistry["unknown"], false
}

func userAppControlActionDefinition(action docker.ContainerAction) (llmAgentActionDefinition, bool) {
	switch action {
	case docker.ActionStart:
		return llmAgentActionDefinition{
			ID:                "general_user_start_container",
			Title:             "Start app",
			Summary:           "A standard user requested that NoobBoard start one opted-in app.",
			RequiresAppTarget: true,
			Executable:        true,
			DockerAction:      docker.ActionStart,
		}, true
	case docker.ActionStop:
		return llmAgentActionDefinition{
			ID:                "general_user_stop_container",
			Title:             "Stop app",
			Summary:           "A standard user requested that NoobBoard stop one opted-in app.",
			RequiresAppTarget: true,
			Executable:        true,
			DockerAction:      docker.ActionStop,
		}, true
	case docker.ActionRestart:
		action, _ := agentActionDefinition("ask_admin_to_restart_container")
		return action, true
	default:
		return llmAgentActionDefinition{}, false
	}
}

func agentRepairActionForApp(action llmAgentActionDefinition, app models.AppStatus) llmAgentActionDefinition {
	if action.ID != "ask_admin_to_restart_container" || !action.Executable {
		return action
	}
	if app.DockerState == models.DockerExited {
		action.Title = "Start recommendation"
		action.Summary = "The target app is stopped. NoobBoard can start it after admin approval, safety review, and per-app opt-in."
		action.DockerAction = docker.ActionStart
		return action
	}
	action.Title = "Restart recommendation"
	action.Summary = "The model suggested repairing one app. NoobBoard can restart it after admin approval, safety review, and per-app opt-in."
	action.DockerAction = docker.ActionRestart
	return action
}

func resolveAgentPlanTarget(action llmAgentActionDefinition, diagnosis llm.Diagnosis, snapshot models.Snapshot) llmAgentPlanTargetView {
	target := llmAgentPlanTargetView{
		Kind:   firstNonEmpty(strings.TrimSpace(diagnosis.RecommendedTarget.Kind), "none"),
		Query:  strings.TrimSpace(diagnosis.RecommendedTarget.IDOrName),
		Reason: "No specific target is needed for this recommendation.",
	}
	if action.ID == arrayStartActionID {
		target.Kind = "storage"
		target.ID = arrayTargetID
		target.Label = "Unraid array"
		target.Query = firstNonEmpty(target.Query, arrayTargetID)
		target.Resolved = true
		target.Reason = ""
		return target
	}
	if !action.RequiresAppTarget {
		if target.Kind == "none" || target.Query == "" {
			return target
		}
		target.Reason = "Target was provided by the model but this action does not require an app target."
		return target
	}
	target.Kind = "app"
	candidates := make([]string, 0, len(diagnosis.AffectedServices)+1)
	if strings.TrimSpace(diagnosis.RecommendedTarget.IDOrName) != "" {
		candidates = append(candidates, diagnosis.RecommendedTarget.IDOrName)
	}
	candidates = append(candidates, diagnosis.AffectedServices...)
	for _, candidate := range candidates {
		app, ok := findAppByID(snapshot.Apps, candidate)
		if !ok {
			continue
		}
		target.ID = app.AppID
		target.Label = firstNonEmpty(app.DisplayName, app.AppID)
		target.Query = strings.TrimSpace(candidate)
		target.Resolved = true
		target.Reason = ""
		return target
	}
	target.Reason = "No exact app target from the model recommendation matched the current admin app snapshot."
	return target
}

func (a *App) agentPlanExecutionState(action llmAgentActionDefinition, target llmAgentPlanTargetView, snapshot models.Snapshot, cfg config.LLMConfig, redactor *privacy.Redactor) (string, bool, string) {
	if !action.ApprovalEligible {
		return "not_actionable", false, ""
	}
	if action.RequiresAppTarget && !target.Resolved {
		return "target_unresolved", false, target.Reason
	}
	if !action.Executable {
		return "approval_locked", false, "This recommendation is informational; NoobBoard only executes app start/restart repairs in this version."
	}
	if !cfg.AgentControlEnabled {
		return "approval_locked", false, "Enable the action approval gate in LLM settings before a fix can run."
	}
	app, ok := findAppByID(snapshot.Apps, target.ID)
	if !ok {
		return "target_unresolved", false, "The target app is no longer present in the current app snapshot."
	}
	if redactor != nil && redactor.IsBlacklistedApp(app) {
		return "approval_locked", false, "This app is privacy-blacklisted, so app fixes are unavailable."
	}
	if !app.AgentRepairAllowed {
		return "approval_locked", false, "Turn on admin/AI app fix for this app in app settings before a fix can run."
	}
	if limit := a.agentRepairLimitState(app.AppID, time.Now().UTC(), false); !limit.Allowed {
		return "approval_rate_limited", false, limit.Message
	}
	return "approval_ready", true, ""
}

func (a *App) reviewAgentAction(ctx context.Context, actor users.User, snapshot models.Snapshot, app models.AppStatus, action llmAgentActionDefinition, via string) (llm.ActionReviewDecision, bool, error) {
	a.settingsMu.RLock()
	cfg := a.deps.Config.LLM
	redactor := a.deps.Redactor
	sameClient := a.deps.LLM
	a.settingsMu.RUnlock()
	if !cfg.ActionAutoReviewEnabled {
		return llm.ActionReviewDecision{}, false, nil
	}
	reviewClient, model, err := actionAutoReviewClient(cfg, redactor, sameClient)
	if err != nil {
		return llm.ActionReviewDecision{}, true, err
	}
	filtered := privacy.FilterSnapshotForRole(snapshot, models.RoleAdmin, redactor)
	refs := loadActionReviewReferences(cfg.ActionAutoReviewReferencePaths)
	// Fail closed rather than asking the reviewer to judge an unnamed operation.
	// This is what the prompt used to do implicitly, and the reviewer refused
	// every time — correctly, but as an unexplained "auto-review did not allow".
	if strings.TrimSpace(string(action.DockerAction)) == "" {
		return llm.ActionReviewDecision{}, true, errors.New("no concrete app operation was resolved for this recommendation, so it cannot be safety reviewed")
	}
	request := llm.ActionReviewRequest{
		ActionID:      action.ID,
		ActionTitle:   action.Title,
		Operation:     string(action.DockerAction),
		TargetID:      app.AppID,
		TargetLabel:   firstNonEmpty(app.DisplayName, app.ContainerName, app.AppID),
		CurrentStatus: currentStatusOrUnknown(app.CurrentStatus),
		ActorRole:     actor.Role,
		Via:           via,
		Reasoning:     cfg.ActionAutoReviewReasoning,
		References:    refs,
		Snapshot:      filtered,
	}
	decision, err := reviewClient.ReviewAction(ctx, request)
	if err != nil {
		return llm.ActionReviewDecision{}, true, err
	}
	a.deps.Audit.Record(actor.ID, "llm.agent_plan.auto_reviewed", map[string]interface{}{
		"app_id":                app.AppID,
		"recommended_action_id": action.ID,
		"via":                   via,
		"review_model":          model,
		"allow":                 decision.Allow,
		"confidence":            decision.Confidence,
		"summary":               decision.Summary,
		"issues":                decision.Issues,
		"reference_count":       len(refs),
	})
	if !decision.Allow {
		return decision, true, errors.New("auto-review did not allow this repair: " + decision.Summary)
	}
	return decision, true, nil
}

func actionAutoReviewClient(cfg config.LLMConfig, redactor *privacy.Redactor, sameClient llm.Client) (llm.Client, string, error) {
	reviewCfg := cfg
	model := strings.TrimSpace(cfg.ActionAutoReviewModel)
	if model == "" || model == "same" {
		if sameClient != nil {
			return sameClient, "same", nil
		}
		return llm.NewClient(reviewCfg, redactor), "same", nil
	}
	provider, modelID, ok := strings.Cut(model, "/")
	if !ok || strings.TrimSpace(provider) == "" || strings.TrimSpace(modelID) == "" {
		return nil, "", fmt.Errorf("invalid action auto-review model %q", model)
	}
	provider = strings.TrimSpace(provider)
	modelID = strings.TrimSpace(modelID)
	switch provider {
	case "openai":
		reviewCfg.Provider = "openai"
		reviewCfg.OpenAIAuthMethod = config.OpenAIAuthMethodAPIKey
		reviewCfg.OpenAIModel = modelID
	case "chatgpt":
		reviewCfg.Provider = "openai"
		if reviewCfg.OpenAIAuthMethod == config.OpenAIAuthMethodAPIKey {
			reviewCfg.OpenAIAuthMethod = config.OpenAIAuthMethodChatGPTHeadless
		}
		reviewCfg.OpenAIModel = modelID
	case "anthropic":
		reviewCfg.Provider = "anthropic"
		reviewCfg.AnthropicModel = modelID
	default:
		return nil, "", fmt.Errorf("unsupported action auto-review provider %q", provider)
	}
	if !llm.ProviderAvailable(reviewCfg) {
		return nil, "", fmt.Errorf("action auto-review provider %s is not available", model)
	}
	return llm.NewClient(reviewCfg, redactor), model, nil
}

func loadActionReviewReferences(paths []string) []llm.ActionReviewReference {
	var refs []llm.ActionReviewReference
	total := 0
	for _, rawPath := range compactStrings(paths) {
		if len(refs) >= actionReviewReferenceLimit || total >= actionReviewReferenceBytes {
			break
		}
		resolved, ok := safeActionReviewReferencePath(rawPath)
		if !ok {
			continue
		}
		data, err := os.ReadFile(resolved)
		if err != nil {
			continue
		}
		if len(data) > actionReviewReferenceFileBytes {
			data = data[:actionReviewReferenceFileBytes]
		}
		remaining := actionReviewReferenceBytes - total
		if remaining <= 0 {
			break
		}
		if len(data) > remaining {
			data = data[:remaining]
		}
		content := strings.TrimSpace(string(data))
		if content == "" {
			continue
		}
		refs = append(refs, llm.ActionReviewReference{Path: filepath.ToSlash(filepath.Clean(rawPath)), Content: content})
		total += len(data)
	}
	return refs
}

func safeActionReviewReferencePath(rawPath string) (string, bool) {
	path := strings.TrimSpace(rawPath)
	if path == "" || filepath.IsAbs(path) {
		return "", false
	}
	clean := filepath.Clean(path)
	if clean == "." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || clean == ".." {
		return "", false
	}
	base := filepath.Base(clean)
	if clean == "README.md" || clean == "AGENTS.md" || strings.HasPrefix(clean, "docs"+string(filepath.Separator)) {
		return filepath.Join(".", clean), true
	}
	if strings.EqualFold(base, "README.md") || strings.EqualFold(base, "AGENTS.md") {
		return filepath.Join(".", clean), true
	}
	return "", false
}

type agentRepairLimitDecision struct {
	Allowed           bool
	Reason            string
	Message           string
	RetryAfter        time.Duration
	RetryAfterSeconds int
}

func (a *App) reserveAgentRepair(appID string, now time.Time) agentRepairLimitDecision {
	return a.agentRepairs.decide(appID, now, true)
}

func (a *App) agentRepairLimitState(appID string, now time.Time, reserve bool) agentRepairLimitDecision {
	return a.agentRepairs.decide(appID, now, reserve)
}

// agentRepairLimiter enforces the two repair limits: a per-app cooldown so a
// flapping app cannot be restarted in a loop, and a global hourly cap so a
// misfiring rule cannot restart the whole estate. It owns its own lock, so the
// window arithmetic and the expiry sweep cannot be read or reordered from
// elsewhere.
type agentRepairLimiter struct {
	mu        sync.Mutex
	lastByApp map[string]time.Time
	global    []time.Time
}

func newAgentRepairLimiter() *agentRepairLimiter {
	return &agentRepairLimiter{lastByApp: map[string]time.Time{}}
}

// decide reports whether a repair may run now, reserving the slot when reserve
// is set. Callers that only want to display the current cooldown pass false.
func (l *agentRepairLimiter) decide(appID string, now time.Time, reserve bool) agentRepairLimitDecision {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	key := strings.ToLower(strings.TrimSpace(appID))
	if key == "" {
		key = "unknown"
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.lastByApp == nil {
		l.lastByApp = map[string]time.Time{}
	}
	l.expireLocked(now)
	if last := l.lastByApp[key]; !last.IsZero() {
		if retryAfter := last.Add(agentRepairPerAppCooldown).Sub(now); retryAfter > 0 {
			return agentRepairRefusal("per_app_cooldown",
				"Automatic repair is cooling down for this app. Try again in "+shortDurationText(retryAfter)+".", retryAfter)
		}
	}
	if len(l.global) >= agentRepairGlobalLimit {
		retryAfter := max(l.global[0].Add(agentRepairGlobalWindow).Sub(now), 0)
		return agentRepairRefusal("global_rate_limit",
			"The repair rate limit has been reached. Try again in "+shortDurationText(retryAfter)+".", retryAfter)
	}
	if reserve {
		l.lastByApp[key] = now
		l.global = append(l.global, now)
	}
	return agentRepairLimitDecision{Allowed: true}
}

// expireLocked drops entries that have aged out of their window. Sweeping on
// every decision keeps both structures bounded without a background timer.
func (l *agentRepairLimiter) expireLocked(now time.Time) {
	for appKey, at := range l.lastByApp {
		if !at.IsZero() && !at.Add(agentRepairPerAppCooldown).After(now) {
			delete(l.lastByApp, appKey)
		}
	}
	kept := l.global[:0]
	for _, at := range l.global {
		if !at.IsZero() && at.Add(agentRepairGlobalWindow).After(now) {
			kept = append(kept, at)
		}
	}
	l.global = kept
}

// seedGlobalForTest fills the global window so a test can exercise the
// rate-limited path without performing five real repairs. Exists so the fields
// can stay unexported and the lock stays inside the type.
func (l *agentRepairLimiter) seedGlobalForTest(at ...time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.global = append([]time.Time(nil), at...)
}

func agentRepairRefusal(reason, message string, retryAfter time.Duration) agentRepairLimitDecision {
	return agentRepairLimitDecision{
		Allowed:           false,
		Reason:            reason,
		Message:           message,
		RetryAfter:        retryAfter,
		RetryAfterSeconds: int((retryAfter + time.Second - 1) / time.Second),
	}
}

func shortDurationText(duration time.Duration) string {
	if duration <= 0 {
		return "a moment"
	}
	if duration < time.Minute {
		seconds := int((duration + time.Second - 1) / time.Second)
		if seconds == 1 {
			return "1 second"
		}
		return fmt.Sprintf("%d seconds", seconds)
	}
	minutes := int((duration + time.Minute - 1) / time.Minute)
	if minutes == 1 {
		return "1 minute"
	}
	return fmt.Sprintf("%d minutes", minutes)
}
