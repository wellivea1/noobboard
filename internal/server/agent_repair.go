package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/wellivea1/noobboard/internal/adapters/docker"
	"github.com/wellivea1/noobboard/internal/adapters/unraid"
	"github.com/wellivea1/noobboard/internal/config"
	"github.com/wellivea1/noobboard/internal/models"
)

// Executing an approved repair and verifying it worked. Approval tokens, the
// per-app and global rate limits, and the outcome verification that decides
// whether a fix is reported as recovered.

func (a *App) recordAgentApproval(w http.ResponseWriter, r *http.Request) {
	if err := requireCSRF(r); err != nil {
		writeError(w, http.StatusForbidden, err)
		return
	}
	var body struct {
		ApprovalToken string `json:"approval_token"`
		Choice        string `json:"choice"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	choice := strings.TrimSpace(body.Choice)
	payload, err := a.verifyAgentApprovalToken(strings.TrimSpace(body.ApprovalToken), mustUser(r).ID)
	if err != nil {
		writeError(w, http.StatusForbidden, err)
		return
	}
	action, ok := agentActionDefinition(payload.RecommendedActionID)
	if !ok || !action.ApprovalEligible {
		writeError(w, http.StatusBadRequest, errors.New("approval plan is not eligible for approval"))
		return
	}
	if action.RequiresAppTarget && (payload.TargetKind != "app" || strings.TrimSpace(payload.TargetID) == "") {
		writeError(w, http.StatusBadRequest, errors.New("approval plan target is missing or invalid"))
		return
	}
	if !validAgentApprovalChoice(choice) {
		writeError(w, http.StatusBadRequest, errors.New("unsupported approval choice"))
		return
	}
	a.settingsMu.RLock()
	llmCfg := a.deps.Config.LLM
	a.settingsMu.RUnlock()
	details := map[string]interface{}{
		"plan_id":               payload.PlanID,
		"choice":                choice,
		"recommended_action_id": action.ID,
		"target_kind":           payload.TargetKind,
		"target_id":             payload.TargetID,
		"can_execute":           false,
	}
	if choice == "deny" {
		a.deps.Audit.Record(mustUser(r).ID, "llm.agent_plan.denied", details)
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "denied"})
		return
	}
	if !llmCfg.AgentControlEnabled {
		a.deps.Audit.Record(mustUser(r).ID, "llm.agent_plan.control_disabled", details)
		writeError(w, http.StatusConflict, errors.New("admin-approved app fixes are disabled in LLM settings"))
		return
	}
	if !action.Executable || action.DockerAction != docker.ActionRestart {
		a.deps.Audit.Record(mustUser(r).ID, "llm.agent_plan.non_executable", details)
		writeError(w, http.StatusConflict, errors.New("this recommendation does not have an executable repair action"))
		return
	}
	snapshot, err := a.readOnlySnapshot(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	app, ok := findAppByID(snapshot.Apps, payload.TargetID)
	if !ok {
		details["reason"] = "target_unresolved"
		a.deps.Audit.Record(mustUser(r).ID, "llm.agent_plan.refused", details)
		writeError(w, http.StatusConflict, errors.New("approval target is no longer present in the current app snapshot"))
		return
	}
	details["app_id"] = app.AppID
	details["container_name"] = app.ContainerName
	executionAction := agentRepairActionForApp(action, app)
	details["docker_action"] = string(executionAction.DockerAction)
	if a.redactorSnapshot().IsBlacklistedApp(app) {
		details["reason"] = "privacy_blacklisted"
		a.deps.Audit.Record(mustUser(r).ID, "llm.agent_plan.refused", details)
		writeError(w, http.StatusConflict, errors.New("app fixes are unavailable for privacy-blacklisted apps"))
		return
	}
	if !app.AgentRepairAllowed {
		details["reason"] = "app_not_opted_in"
		a.deps.Audit.Record(mustUser(r).ID, "llm.agent_plan.refused", details)
		writeError(w, http.StatusConflict, errors.New("admin/AI app fix is not enabled for this app"))
		return
	}
	if strings.TrimSpace(payload.Nonce) == "" {
		writeError(w, http.StatusForbidden, errors.New("approval token is missing a replay nonce"))
		return
	}
	reviewDecision, reviewEnabled, err := a.reviewAgentAction(r.Context(), mustUser(r), snapshot, app, executionAction, "agent_plan")
	if reviewEnabled {
		details["auto_review_allow"] = reviewDecision.Allow
		details["auto_review_confidence"] = reviewDecision.Confidence
		details["auto_review_summary"] = reviewDecision.Summary
	}
	if err != nil {
		details["reason"] = "auto_review_refused"
		details["error"] = err.Error()
		a.deps.Audit.Record(mustUser(r).ID, "llm.agent_plan.auto_review_refused", auditDetailsCopy(details))
		writeError(w, http.StatusConflict, err)
		return
	}
	if !a.consumeAgentApproval(payload) {
		details["reason"] = "approval_replay"
		a.deps.Audit.Record(mustUser(r).ID, "llm.agent_plan.replay_blocked", details)
		writeError(w, http.StatusConflict, errors.New("approval token has already been used"))
		return
	}
	limit := a.reserveAgentRepair(app.AppID, time.Now().UTC())
	if !limit.Allowed {
		details["reason"] = limit.Reason
		details["retry_after_seconds"] = limit.RetryAfterSeconds
		a.deps.Audit.Record(mustUser(r).ID, "llm.agent_plan.rate_limited", auditDetailsCopy(details))
		writeError(w, http.StatusConflict, errors.New(limit.Message))
		return
	}
	details["can_execute"] = true
	a.deps.Audit.Record(mustUser(r).ID, "llm.agent_plan.approved", auditDetailsCopy(details))
	result, err := a.deps.Collectors.Docker.ControlContainer(r.Context(), app, executionAction.DockerAction)
	if err != nil {
		details["error"] = err.Error()
		a.deps.Audit.Record(mustUser(r).ID, "llm.agent_plan.execute_failed", auditDetailsCopy(details))
		writeError(w, http.StatusBadGateway, err)
		return
	}
	details["via"] = "agent_plan"
	a.invalidateSnapshot()
	a.deps.Audit.Record(mustUser(r).ID, "llm.agent_plan.executed", auditDetailsCopy(details))
	a.deps.Audit.Record(mustUser(r).ID, "app.container.action", map[string]interface{}{"app_id": app.AppID, "action": string(executionAction.DockerAction), "container_name": app.ContainerName, "via": "agent_plan", "plan_id": payload.PlanID, "recommended_action_id": action.ID})
	outcome := a.verifyAgentRepairOutcome(r.Context(), app, executionAction, result)
	verifyDetails := auditDetailsCopy(details)
	verifyDetails["verified"] = outcome.Verified
	verifyDetails["recovered"] = outcome.Recovered
	verifyDetails["before_status"] = string(outcome.BeforeStatus)
	verifyDetails["after_status"] = string(outcome.AfterStatus)
	verifyDetails["history_event_id"] = outcome.HistoryEventID
	if outcome.Verified {
		a.deps.Audit.Record(mustUser(r).ID, "llm.agent_plan.verified", verifyDetails)
	} else {
		a.deps.Audit.Record(mustUser(r).ID, "llm.agent_plan.verify_failed", verifyDetails)
	}
	writeJSON(w, http.StatusAccepted, map[string]interface{}{
		"status":  "executed",
		"result":  result,
		"outcome": outcome,
	})
}

func (a *App) verifyAgentRepairOutcome(ctx context.Context, before models.AppStatus, action llmAgentActionDefinition, result docker.ControlResult) llmAgentRepairOutcomeView {
	return a.verifyRepairOutcome(ctx, before, action, result, "Auto-repair")
}

func (a *App) verifyRepairOutcome(ctx context.Context, before models.AppStatus, action llmAgentActionDefinition, result docker.ControlResult, label string) llmAgentRepairOutcomeView {
	beforeStatus := currentStatusOrUnknown(before.CurrentStatus)
	targetLabel := firstNonEmpty(before.DisplayName, before.ContainerName, before.AppID)
	messagePrefix := strings.TrimSpace(label)
	if messagePrefix == "" {
		messagePrefix = "Auto-repair"
	}
	outcome := llmAgentRepairOutcomeView{
		Action:       string(action.DockerAction),
		TargetID:     before.AppID,
		TargetLabel:  targetLabel,
		BeforeStatus: beforeStatus,
		AfterStatus:  models.StatusUnknown,
		CheckedAt:    time.Now().UTC(),
		Message:      dockerActionDisplayName(action.DockerAction) + " was sent, but NoobBoard could not verify the app status yet.",
		ResultStatus: strings.TrimSpace(result.Status),
	}
	var afterSnapshot models.Snapshot
	var afterSnapshotSet bool
	attempts := agentRepairVerificationAttempts
	if attempts <= 0 || agentRepairVerificationDelay <= 0 {
		attempts = 1
	}
	for attempt := 0; attempt < attempts; attempt++ {
		if delay := agentRepairVerificationDelay; delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				outcome.Message = dockerActionDisplayName(action.DockerAction) + " was sent, but verification was cancelled before NoobBoard could refresh status."
				return outcome
			case <-timer.C:
			}
		}
		a.invalidateSnapshot()
		refreshed, err := a.refreshSnapshot(ctx, false)
		outcome.CheckedAt = time.Now().UTC()
		if err != nil {
			outcome.Message = dockerActionDisplayName(action.DockerAction) + " was sent, but status verification failed: " + err.Error()
			return outcome
		}
		afterSnapshot = refreshed
		afterSnapshotSet = true
		afterApp, ok := findAppBySameIdentity(afterSnapshot.Apps, before)
		if !ok {
			outcome.Verified = true
			outcome.AfterStatus = models.StatusUnknown
			outcome.Recovered = action.DockerAction == docker.ActionStop
			outcome.Message = messagePrefix + ": " + dockerActionPastTense(action.DockerAction) + " - target app was not present after refresh."
			break
		}
		outcome.Verified = true
		outcome.TargetID = firstNonEmpty(afterApp.AppID, outcome.TargetID)
		outcome.AfterStatus = currentStatusOrUnknown(afterApp.CurrentStatus)
		outcome.TargetLabel = firstNonEmpty(afterApp.DisplayName, afterApp.ContainerName, afterApp.AppID, targetLabel)
		outcome.Recovered = dockerActionReachedExpectedState(action.DockerAction, afterApp)
		if outcome.Recovered {
			outcome.Message = messagePrefix + ": " + dockerActionSuccessPhrase(action.DockerAction) + "."
			break
		}
		if attempt == attempts-1 {
			outcome.Message = messagePrefix + ": " + dockerActionFinalWaitingPhrase(action.DockerAction) + "."
		} else {
			outcome.Message = messagePrefix + ": " + dockerActionWaitingPhrase(action.DockerAction) + "."
		}
	}
	if historyEventID, err := a.appendAgentRepairHistoryEvent(outcome); err == nil {
		outcome.HistoryEventID = historyEventID
		if a.historyRecorder != nil && afterSnapshotSet {
			a.historyRecorder.Observe(afterSnapshot)
		}
	} else {
		outcome.HistoryError = err.Error()
	}
	return outcome
}

func (a *App) verifyArrayStartOutcome(ctx context.Context, before models.InfrastructureStatus, result unraid.ArrayControlResult) llmAgentRepairOutcomeView {
	outcome := llmAgentRepairOutcomeView{
		Action:       "start_array",
		TargetID:     arrayTargetID,
		TargetLabel:  "Unraid array",
		BeforeStatus: arrayHistoryStatus(before),
		AfterStatus:  models.StatusUnknown,
		CheckedAt:    time.Now().UTC(),
		Message:      "Start array was sent, but NoobBoard could not verify the array status yet.",
		ResultStatus: strings.TrimSpace(result.Status),
	}
	var afterSnapshot models.Snapshot
	var afterSnapshotSet bool
	attempts := agentRepairVerificationAttempts
	if attempts <= 0 || agentRepairVerificationDelay <= 0 {
		attempts = 1
	}
	for attempt := 0; attempt < attempts; attempt++ {
		if delay := agentRepairVerificationDelay; delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				outcome.Message = "Start array was sent, but verification was cancelled before NoobBoard could refresh status."
				return outcome
			case <-timer.C:
			}
		}
		a.invalidateSnapshot()
		refreshed, err := a.refreshSnapshot(ctx, false)
		outcome.CheckedAt = time.Now().UTC()
		if err != nil {
			outcome.Message = "Start array was sent, but status verification failed: " + err.Error()
			return outcome
		}
		afterSnapshot = refreshed
		afterSnapshotSet = true
		after := refreshed.Infrastructure
		outcome.Verified = true
		outcome.AfterStatus = arrayHistoryStatus(after)
		outcome.Recovered = strings.EqualFold(strings.TrimSpace(after.UnraidArrayState), "started")
		if outcome.Recovered {
			outcome.Message = "Unraid array started successfully. Send another message or try again if you continue to have issues."
			break
		}
		if attempt == attempts-1 {
			outcome.Message = "Start array was sent, but the array still does not report started."
		} else {
			outcome.Message = "Start array was sent. Waiting for the array to report started."
		}
	}
	if historyEventID, err := a.appendArrayActionHistoryEvent(outcome); err == nil {
		outcome.HistoryEventID = historyEventID
		if a.historyRecorder != nil && afterSnapshotSet {
			a.historyRecorder.Observe(afterSnapshot)
		}
	} else {
		outcome.HistoryError = err.Error()
	}
	return outcome
}

func (a *App) appendArrayActionHistoryEvent(outcome llmAgentRepairOutcomeView) (string, error) {
	if a.deps.History == nil {
		return "", nil
	}
	eventID := agentRepairHistoryEventID(outcome)
	event := models.StatusEvent{
		ID:          eventID,
		SubjectType: models.SubjectInfra,
		SubjectID:   arrayTargetID,
		DisplayName: "Unraid array",
		From:        outcome.BeforeStatus,
		To:          outcome.AfterStatus,
		At:          outcome.CheckedAt,
		Note:        outcome.Message,
	}
	if event.At.IsZero() {
		event.At = time.Now().UTC()
	}
	if err := a.deps.History.Append([]models.StatusEvent{event}); err != nil {
		return "", err
	}
	return eventID, nil
}

func (a *App) appendAgentRepairHistoryEvent(outcome llmAgentRepairOutcomeView) (string, error) {
	if a.deps.History == nil || strings.TrimSpace(outcome.TargetID) == "" {
		return "", nil
	}
	eventID := agentRepairHistoryEventID(outcome)
	event := models.StatusEvent{
		ID:          eventID,
		SubjectType: models.SubjectApp,
		SubjectID:   outcome.TargetID,
		DisplayName: outcome.TargetLabel,
		From:        outcome.BeforeStatus,
		To:          outcome.AfterStatus,
		At:          outcome.CheckedAt,
		Note:        outcome.Message,
	}
	if event.DisplayName == "" {
		event.DisplayName = outcome.TargetID
	}
	if event.At.IsZero() {
		event.At = time.Now().UTC()
	}
	if err := a.deps.History.Append([]models.StatusEvent{event}); err != nil {
		return "", err
	}
	return eventID, nil
}

func agentRepairHistoryEventID(outcome llmAgentRepairOutcomeView) string {
	return fmt.Sprintf("agent-repair-%s-%d-%s", sanitizeAgentRepairIDPart(outcome.TargetID), outcome.CheckedAt.UnixNano(), outcome.AfterStatus)
}

func sanitizeAgentRepairIDPart(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

func currentStatusOrUnknown(status models.CurrentStatus) models.CurrentStatus {
	if status == "" {
		return models.StatusUnknown
	}
	return status
}

func auditDetailsCopy(details map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(details))
	for key, value := range details {
		out[key] = value
	}
	return out
}

func (a *App) setAgentArm(w http.ResponseWriter, r *http.Request) {
	if err := requireCSRF(r); err != nil {
		writeError(w, http.StatusForbidden, err)
		return
	}
	var body struct {
		Armed           bool `json:"armed"`
		DurationSeconds int  `json:"duration_seconds"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	a.settingsMu.RLock()
	cfg := a.deps.Config.LLM
	a.settingsMu.RUnlock()
	duration := cfg.AgentArmDuration
	if body.DurationSeconds > 0 {
		duration = time.Duration(body.DurationSeconds) * time.Second
	}
	if duration <= 0 {
		duration = config.Defaults().LLM.AgentArmDuration
	}
	if duration > time.Hour {
		duration = time.Hour
	}
	var until time.Time
	action := "llm.agent.disarmed"
	if body.Armed {
		if !cfg.AgentControlEnabled {
			writeError(w, http.StatusConflict, errors.New("agent action approval gate is disabled in LLM settings"))
			return
		}
		until = time.Now().UTC().Add(duration)
		action = "llm.agent.armed"
	}
	updated, ok := a.sessions.setAgentArmed(mustSession(r).Token, until)
	if !ok {
		writeError(w, http.StatusUnauthorized, errors.New("authentication required"))
		return
	}
	a.deps.Audit.Record(mustUser(r).ID, action, map[string]interface{}{
		"armed":            body.Armed,
		"armed_until":      until,
		"duration_seconds": int(duration / time.Second),
	})
	writeJSON(w, http.StatusOK, llmAgentReadinessResponse(cfg, updated))
}

func (a *App) signAgentApprovalToken(payload agentApprovalTokenPayload) string {
	data, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	encoded := base64.RawURLEncoding.EncodeToString(data)
	mac := hmac.New(sha256.New, a.agentApprovalSecret)
	_, _ = mac.Write([]byte(encoded))
	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return encoded + "." + signature
}

func (a *App) verifyAgentApprovalToken(token, actorID string) (agentApprovalTokenPayload, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return agentApprovalTokenPayload{}, errors.New("valid approval token is required")
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return agentApprovalTokenPayload{}, errors.New("valid approval token is required")
	}
	mac := hmac.New(sha256.New, a.agentApprovalSecret)
	_, _ = mac.Write([]byte(parts[0]))
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return agentApprovalTokenPayload{}, errors.New("approval token is invalid")
	}
	data, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return agentApprovalTokenPayload{}, errors.New("approval token is invalid")
	}
	var payload agentApprovalTokenPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return agentApprovalTokenPayload{}, errors.New("approval token is invalid")
	}
	if payload.PlanID != agentApprovalPlanID || strings.TrimSpace(payload.RecommendedActionID) == "" {
		return agentApprovalTokenPayload{}, errors.New("approval token is invalid")
	}
	if _, ok := agentActionDefinition(payload.RecommendedActionID); !ok {
		return agentApprovalTokenPayload{}, errors.New("approval token is invalid")
	}
	if payload.ActorID != actorID {
		return agentApprovalTokenPayload{}, errors.New("approval token is not valid for this user")
	}
	if payload.ExpiresAt <= 0 || time.Now().UTC().After(time.Unix(payload.ExpiresAt, 0)) {
		return agentApprovalTokenPayload{}, errors.New("approval token has expired")
	}
	return payload, nil
}

func validAgentApprovalChoice(choice string) bool {
	switch choice {
	case "deny", "allow_once":
		return true
	default:
		return false
	}
}

func (a *App) consumeAgentApproval(payload agentApprovalTokenPayload) bool {
	nonce := strings.TrimSpace(payload.Nonce)
	if nonce == "" {
		return false
	}
	expiresAt := time.Unix(payload.ExpiresAt, 0).UTC()
	now := time.Now().UTC()
	a.agentApprovalMu.Lock()
	defer a.agentApprovalMu.Unlock()
	for key, expiry := range a.consumedAgentApprovals {
		if !expiry.After(now) {
			delete(a.consumedAgentApprovals, key)
		}
	}
	key := payload.ActorID + "|" + payload.PlanID + "|" + payload.RecommendedActionID + "|" + payload.TargetKind + "|" + payload.TargetID + "|" + nonce
	if _, exists := a.consumedAgentApprovals[key]; exists {
		return false
	}
	a.consumedAgentApprovals[key] = expiresAt
	return true
}
