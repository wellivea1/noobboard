package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/wellivea1/noobboard/internal/adapters/docker"
	"github.com/wellivea1/noobboard/internal/db"
	"github.com/wellivea1/noobboard/internal/models"
	"github.com/wellivea1/noobboard/internal/users"
)

// The general-user path: asking an admin for a repair, and the narrower set of
// app actions a non-admin may run directly on an opted-in app.

func (a *App) notifyAdmin(w http.ResponseWriter, r *http.Request) {
	if err := requireCSRF(r); err != nil {
		writeError(w, http.StatusForbidden, err)
		return
	}
	var body struct {
		Message string `json:"message"`
		AppID   string `json:"app_id"`
		Context string `json:"context"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	message := strings.TrimSpace(body.Message)
	if message == "" {
		message = "A standard user reported a problem."
	}
	if contextText := strings.TrimSpace(body.Context); contextText != "" {
		message += " " + contextText
	}
	if len(message) > 1000 {
		message = message[:1000]
	}
	appID := strings.TrimSpace(body.AppID)
	dedupe := "notify-admin:" + mustUser(r).ID + ":" + appID
	sent, err := a.deps.Notifications.NotifyAdmins(r.Context(), "NoobBoard user report", message, appID, dedupe)
	if err != nil {
		a.deps.Audit.Record(mustUser(r).ID, "user.notify_admin.failed", map[string]interface{}{"app_id": appID, "error": err.Error()})
		writeError(w, http.StatusBadGateway, err)
		return
	}
	a.deps.Audit.Record(mustUser(r).ID, "user.notify_admin", map[string]interface{}{"message": message, "app_id": appID, "admin_notifications": sent})
	writeJSON(w, http.StatusAccepted, map[string]interface{}{"status": "queued", "admin_notifications": sent})
}

func (a *App) createRepairRequest(w http.ResponseWriter, r *http.Request) {
	if err := requireCSRF(r); err != nil {
		writeError(w, http.StatusForbidden, err)
		return
	}
	var body struct {
		AppID            string `json:"app_id"`
		ActionID         string `json:"action_id"`
		DiagnosisSummary string `json:"diagnosis_summary"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	appID := strings.TrimSpace(body.AppID)
	if appID == "" {
		writeError(w, http.StatusBadRequest, errors.New("app_id is required"))
		return
	}
	actionID := strings.TrimSpace(body.ActionID)
	if actionID == "" {
		actionID = "ask_admin_to_restart_container"
	}
	action, ok := agentActionDefinition(actionID)
	if !ok || !action.Executable || action.DockerAction != docker.ActionRestart {
		writeError(w, http.StatusBadRequest, errors.New("requested repair action is not supported"))
		return
	}
	visibleSnapshot, err := a.Snapshot(r.Context(), mustUser(r).Role)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	visibleApp, ok := findAppByID(visibleSnapshot.Apps, appID)
	if !ok {
		writeError(w, http.StatusNotFound, errors.New("app is not visible to this user"))
		return
	}
	fullSnapshot, err := a.readOnlySnapshot(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	fullApp, ok := findAppByID(fullSnapshot.Apps, appID)
	if !ok {
		writeError(w, http.StatusNotFound, errors.New("app is not available in the current snapshot"))
		return
	}
	if !isDockerRepairTarget(fullApp) {
		writeError(w, http.StatusBadRequest, errors.New("this app cannot be restarted by NoobBoard"))
		return
	}
	if existing, ok, err := a.pendingRepairRequestFor(mustUser(r).ID, fullApp.AppID, action.ID); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	} else if ok {
		writeJSON(w, http.StatusOK, map[string]interface{}{"status": "pending", "request": existing, "duplicate": true})
		return
	}
	requestID, err := randomToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	user := mustUser(r)
	now := time.Now().UTC()
	request := models.RepairRequest{
		ID:               "repair-" + sanitizeAgentRepairIDPart(requestID),
		RequesterID:      user.ID,
		RequesterName:    firstNonEmpty(user.DisplayName, user.Username, user.ID),
		RequesterRole:    user.Role,
		AppID:            fullApp.AppID,
		AppLabel:         firstNonEmpty(visibleApp.DisplayName, fullApp.DisplayName, fullApp.ContainerName, fullApp.AppID),
		ActionID:         action.ID,
		DiagnosisSummary: strings.TrimSpace(body.DiagnosisSummary),
		Status:           models.RepairRequestPending,
		CreatedAt:        now,
	}
	if request.DiagnosisSummary == "" {
		request.DiagnosisSummary = compactAppSummaryForRequest(visibleApp)
	}
	if err := a.deps.Store.UpsertRepairRequest(request); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	message := fmt.Sprintf("%s asked for help with %s. %s", request.RequesterName, request.AppLabel, request.DiagnosisSummary)
	sent, err := a.deps.Notifications.NotifyAdmins(r.Context(), "Repair requested: "+request.AppLabel, message, request.AppID, "repair-request:"+request.ID)
	if err != nil {
		a.deps.Audit.Record(user.ID, "user.repair_request.notify_failed", map[string]interface{}{"request_id": request.ID, "app_id": request.AppID, "error": err.Error()})
	}
	a.deps.Audit.Record(user.ID, "user.repair_request.created", map[string]interface{}{"request_id": request.ID, "app_id": request.AppID, "action_id": request.ActionID, "admin_notifications": sent})
	writeJSON(w, http.StatusAccepted, map[string]interface{}{"status": "pending", "request": request, "admin_notifications": sent})
}

func (a *App) userRepairRequests(w http.ResponseWriter, r *http.Request) {
	requests, err := a.deps.Store.RepairRequestsForUser(mustUser(r).ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, repairRequestsNewestFirst(requests))
}

func (a *App) restartUserApp(w http.ResponseWriter, r *http.Request) {
	if err := requireCSRF(r); err != nil {
		writeError(w, http.StatusForbidden, err)
		return
	}
	var body struct {
		Confirmed    bool   `json:"confirmed"`
		ConfirmAppID string `json:"confirm_app_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	a.executeUserAppAction(w, r, docker.ActionRestart, body.Confirmed, body.ConfirmAppID)
}

func (a *App) controlUserApp(w http.ResponseWriter, r *http.Request) {
	if err := requireCSRF(r); err != nil {
		writeError(w, http.StatusForbidden, err)
		return
	}
	var body struct {
		Action       string `json:"action"`
		Confirmed    bool   `json:"confirmed"`
		ConfirmAppID string `json:"confirm_app_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	action, err := docker.ParseContainerAction(body.Action)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	a.executeUserAppAction(w, r, action, body.Confirmed, body.ConfirmAppID)
}

type userAppActionExecution struct {
	Result  docker.ControlResult
	Outcome llmAgentRepairOutcomeView
}

type userAppActionFailure struct {
	HTTPStatus int
	PlanStatus string
	Err        error
}

func (f *userAppActionFailure) Error() string {
	if f == nil || f.Err == nil {
		return ""
	}
	return f.Err.Error()
}

func newUserAppActionFailure(httpStatus int, planStatus string, err error) *userAppActionFailure {
	if err == nil {
		err = errors.New("app action failed")
	}
	return &userAppActionFailure{HTTPStatus: httpStatus, PlanStatus: strings.TrimSpace(planStatus), Err: err}
}

func (a *App) executeUserAppAction(w http.ResponseWriter, r *http.Request, action docker.ContainerAction, confirmed bool, confirmAppID string) {
	appID := strings.TrimSpace(r.PathValue("id"))
	if appID == "" {
		writeError(w, http.StatusBadRequest, errors.New("app id is required"))
		return
	}
	if _, ok := userAppControlActionDefinition(action); !ok {
		writeError(w, http.StatusBadRequest, errors.New("app action is not supported"))
		return
	}
	user := mustUser(r)
	visibleSnapshot, err := a.Snapshot(r.Context(), user.Role)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	visibleApp, ok := findAppByID(visibleSnapshot.Apps, appID)
	if !ok {
		writeError(w, http.StatusNotFound, errors.New("app is not visible to this user"))
		return
	}
	if !sameAppIdentifier(confirmAppID, visibleApp.AppID) || !confirmed {
		writeError(w, http.StatusBadRequest, fmt.Errorf("%s requires confirmed=true with a matching confirm_app_id", action))
		return
	}
	execution, failure := a.executeGeneralUserAppAction(r.Context(), user, visibleApp.AppID, action, "general_user_direct", dockerActionDisplayName(action))
	if failure != nil {
		writeError(w, failure.HTTPStatus, failure.Err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]interface{}{
		"status":  "executed",
		"result":  execution.Result,
		"outcome": execution.Outcome,
	})
}

func (a *App) executeGeneralUserAppAction(ctx context.Context, user users.User, appID string, action docker.ContainerAction, via string, outcomeLabel string) (userAppActionExecution, *userAppActionFailure) {
	var empty userAppActionExecution
	appID = strings.TrimSpace(appID)
	if appID == "" {
		return empty, newUserAppActionFailure(http.StatusBadRequest, "target_unresolved", errors.New("app id is required"))
	}
	requestedAction := action
	actionDef, ok := userAppControlActionDefinition(action)
	if !ok {
		return empty, newUserAppActionFailure(http.StatusBadRequest, "not_actionable", errors.New("app action is not supported"))
	}
	via = strings.TrimSpace(via)
	if via == "" {
		via = "general_user_direct"
	}
	visibleSnapshot, err := a.Snapshot(ctx, user.Role)
	if err != nil {
		return empty, newUserAppActionFailure(http.StatusInternalServerError, "snapshot_failed", err)
	}
	visibleApp, ok := findAppByID(visibleSnapshot.Apps, appID)
	if !ok {
		return empty, newUserAppActionFailure(http.StatusNotFound, "target_unresolved", errors.New("app is not visible to this user"))
	}
	if !visibleApp.RestartAllowedGeneralUser {
		a.deps.Audit.Record(user.ID, userAppActionAudit(action, "refused"), map[string]interface{}{"app_id": visibleApp.AppID, "reason": "app_not_opted_in", "action": string(action), "via": via})
		return empty, newUserAppActionFailure(http.StatusConflict, "not_opted_in", errors.New("user app controls are not enabled for this app"))
	}
	snapshot, err := a.readOnlySnapshot(ctx)
	if err != nil {
		return empty, newUserAppActionFailure(http.StatusInternalServerError, "snapshot_failed", err)
	}
	app, ok := findAppByID(snapshot.Apps, visibleApp.AppID)
	if !ok {
		return empty, newUserAppActionFailure(http.StatusNotFound, "target_unresolved", errors.New("app is not available in the current snapshot"))
	}
	if action == docker.ActionRestart && app.DockerState == models.DockerExited {
		action = docker.ActionStart
		actionDef, _ = userAppControlActionDefinition(action)
	}
	details := map[string]interface{}{
		"app_id":           app.AppID,
		"requester_id":     user.ID,
		"via":              via,
		"action":           string(action),
		"requested_action": string(requestedAction),
	}
	if !isDockerRepairTarget(app) {
		details["reason"] = "not_docker_target"
		a.deps.Audit.Record(user.ID, userAppActionAudit(action, "refused"), details)
		return empty, newUserAppActionFailure(http.StatusConflict, "not_actionable", errors.New("this app cannot be controlled by NoobBoard"))
	}
	if a.redactorSnapshot().IsBlacklistedApp(app) {
		details["reason"] = "privacy_blacklisted"
		a.deps.Audit.Record(user.ID, userAppActionAudit(action, "refused"), details)
		return empty, newUserAppActionFailure(http.StatusConflict, "not_actionable", errors.New("app controls are unavailable for this app"))
	}
	if !app.RestartAllowedGeneralUser {
		details["reason"] = "app_not_opted_in"
		a.deps.Audit.Record(user.ID, userAppActionAudit(action, "refused"), details)
		return empty, newUserAppActionFailure(http.StatusConflict, "not_opted_in", errors.New("user app controls are not enabled for this app"))
	}
	if err := validateGeneralUserAppActionState(action, app); err != nil {
		details["reason"] = "action_state_refused"
		a.deps.Audit.Record(user.ID, userAppActionAudit(action, "refused"), details)
		return empty, newUserAppActionFailure(http.StatusConflict, "not_actionable", err)
	}
	reviewDecision, reviewEnabled, err := a.reviewAgentAction(ctx, user, snapshot, app, actionDef, via)
	if reviewEnabled {
		details["auto_review_allow"] = reviewDecision.Allow
		details["auto_review_confidence"] = reviewDecision.Confidence
		details["auto_review_summary"] = reviewDecision.Summary
	}
	if err != nil {
		details["reason"] = "auto_review_refused"
		details["error"] = err.Error()
		a.deps.Audit.Record(user.ID, userAppActionAudit(action, "auto_review_refused"), auditDetailsCopy(details))
		return empty, newUserAppActionFailure(http.StatusConflict, "auto_review_refused", err)
	}
	limit := a.reserveAgentRepair(app.AppID, time.Now().UTC())
	if !limit.Allowed {
		details["reason"] = limit.Reason
		details["retry_after_seconds"] = limit.RetryAfterSeconds
		a.deps.Audit.Record(user.ID, userAppActionAudit(action, "rate_limited"), auditDetailsCopy(details))
		return empty, newUserAppActionFailure(http.StatusConflict, "approval_rate_limited", errors.New(limit.Message))
	}
	result, err := a.deps.Collectors.Docker.ControlContainer(ctx, app, action)
	if err != nil {
		details["error"] = err.Error()
		a.deps.Audit.Record(user.ID, userAppActionAudit(action, "execute_failed"), auditDetailsCopy(details))
		return empty, newUserAppActionFailure(http.StatusBadGateway, "auto_execute_failed", err)
	}
	details["container_name"] = app.ContainerName
	a.invalidateSnapshot()
	a.deps.Audit.Record(user.ID, userAppActionAudit(action, "executed"), auditDetailsCopy(details))
	a.deps.Audit.Record(user.ID, "app.container.action", map[string]interface{}{"app_id": app.AppID, "action": string(action), "container_name": app.ContainerName, "via": via})
	outcome := a.verifyRepairOutcome(ctx, app, actionDef, result, outcomeLabel)
	verifyDetails := auditDetailsCopy(details)
	verifyDetails["verified"] = outcome.Verified
	verifyDetails["recovered"] = outcome.Recovered
	verifyDetails["before_status"] = string(outcome.BeforeStatus)
	verifyDetails["after_status"] = string(outcome.AfterStatus)
	verifyDetails["history_event_id"] = outcome.HistoryEventID
	if outcome.Verified {
		a.deps.Audit.Record(user.ID, userAppActionAudit(action, "verified"), verifyDetails)
	} else {
		a.deps.Audit.Record(user.ID, userAppActionAudit(action, "verify_failed"), verifyDetails)
	}
	return userAppActionExecution{Result: result, Outcome: outcome}, nil
}

func (a *App) executeUserAgentAction(w http.ResponseWriter, r *http.Request) {
	if err := requireCSRF(r); err != nil {
		writeError(w, http.StatusForbidden, err)
		return
	}
	var body struct {
		ExecutionToken string `json:"execution_token"`
		Choice         string `json:"choice"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	choice := strings.TrimSpace(body.Choice)
	if choice != "start_array" {
		writeError(w, http.StatusBadRequest, errors.New("unsupported LLM action choice"))
		return
	}
	user := mustUser(r)
	payload, err := a.verifyAgentApprovalToken(strings.TrimSpace(body.ExecutionToken), user.ID)
	if err != nil {
		writeError(w, http.StatusForbidden, err)
		return
	}
	if payload.RecommendedActionID != arrayStartActionID || payload.TargetKind != "storage" || payload.TargetID != arrayTargetID {
		writeError(w, http.StatusBadRequest, errors.New("execution token is not valid for starting the array"))
		return
	}
	if strings.TrimSpace(payload.Nonce) == "" {
		writeError(w, http.StatusForbidden, errors.New("execution token is missing a replay nonce"))
		return
	}
	a.settingsMu.RLock()
	visibility := a.deps.Config.Visibility
	role := compactDiagnosisRole(user.Role, visibility.DefaultRole)
	allowed := roleCanUseLLM(visibility, role)
	a.settingsMu.RUnlock()
	if !allowed {
		a.deps.Audit.Record(user.ID, "llm.array_start.refused", map[string]interface{}{"reason": "llm_disabled_for_role", "via": "general_user_llm"})
		writeError(w, http.StatusForbidden, errors.New("status chat is disabled for this role"))
		return
	}
	snapshot, err := a.readOnlySnapshot(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	details := map[string]interface{}{
		"recommended_action_id": payload.RecommendedActionID,
		"target_kind":           payload.TargetKind,
		"target_id":             payload.TargetID,
		"requester_id":          user.ID,
		"via":                   "general_user_llm",
		"array_state":           snapshot.Infrastructure.UnraidArrayState,
		"can_execute":           false,
	}
	if !arrayStartNeeded(snapshot.Infrastructure) {
		details["reason"] = "array_not_stopped"
		a.deps.Audit.Record(user.ID, "llm.array_start.refused", details)
		writeError(w, http.StatusConflict, errors.New("the array is not currently stopped"))
		return
	}
	if !a.consumeAgentApproval(payload) {
		details["reason"] = "execution_replay"
		a.deps.Audit.Record(user.ID, "llm.array_start.replay_blocked", details)
		writeError(w, http.StatusConflict, errors.New("execution token has already been used"))
		return
	}
	limit := a.reserveAgentRepair(arrayTargetID, time.Now().UTC())
	if !limit.Allowed {
		details["reason"] = limit.Reason
		details["retry_after_seconds"] = limit.RetryAfterSeconds
		a.deps.Audit.Record(user.ID, "llm.array_start.rate_limited", auditDetailsCopy(details))
		writeError(w, http.StatusConflict, errors.New(limit.Message))
		return
	}
	details["can_execute"] = true
	a.deps.Audit.Record(user.ID, "llm.array_start.approved", auditDetailsCopy(details))
	result, err := a.deps.Collectors.Unraid.StartArray(r.Context())
	if err != nil {
		details["error"] = err.Error()
		a.deps.Audit.Record(user.ID, "llm.array_start.execute_failed", auditDetailsCopy(details))
		writeError(w, http.StatusBadGateway, err)
		return
	}
	a.invalidateSnapshot()
	a.deps.Audit.Record(user.ID, "llm.array_start.executed", auditDetailsCopy(details))
	a.deps.Audit.Record(user.ID, "infra.unraid_array.action", map[string]interface{}{"action": "start_array", "via": "general_user_llm", "recommended_action_id": payload.RecommendedActionID})
	outcome := a.verifyArrayStartOutcome(r.Context(), snapshot.Infrastructure, result)
	verifyDetails := auditDetailsCopy(details)
	verifyDetails["verified"] = outcome.Verified
	verifyDetails["recovered"] = outcome.Recovered
	verifyDetails["before_status"] = string(outcome.BeforeStatus)
	verifyDetails["after_status"] = string(outcome.AfterStatus)
	verifyDetails["history_event_id"] = outcome.HistoryEventID
	if outcome.Verified {
		a.deps.Audit.Record(user.ID, "llm.array_start.verified", verifyDetails)
	} else {
		a.deps.Audit.Record(user.ID, "llm.array_start.verify_failed", verifyDetails)
	}
	writeJSON(w, http.StatusAccepted, map[string]interface{}{
		"status":  "executed",
		"result":  result,
		"outcome": outcome,
	})
}

func validateGeneralUserAppActionState(action docker.ContainerAction, app models.AppStatus) error {
	status := currentStatusOrUnknown(app.CurrentStatus)
	switch action {
	case docker.ActionStart:
		if app.DockerState == models.DockerRunning || status == models.StatusOnline || status == models.StatusDegraded {
			return errors.New("this app is already running")
		}
	case docker.ActionStop:
		if app.DockerState == models.DockerExited || (status == models.StatusOffline && app.DockerState != models.DockerRunning) {
			return errors.New("this app is already stopped")
		}
	case docker.ActionRestart:
	default:
		return errors.New("app action is not supported")
	}
	return nil
}

func preferredGeneralUserRepairAction(app models.AppStatus) docker.ContainerAction {
	if app.DockerState == models.DockerExited {
		return docker.ActionStart
	}
	return docker.ActionRestart
}

func userAppActionAudit(action docker.ContainerAction, suffix string) string {
	actionName := strings.TrimSpace(string(action))
	if actionName == "" {
		actionName = "unknown"
	}
	return "user.app." + actionName + "." + suffix
}

func dockerActionDisplayName(action docker.ContainerAction) string {
	switch action {
	case docker.ActionStart:
		return "Start"
	case docker.ActionStop:
		return "Stop"
	case docker.ActionRestart:
		return "Restart"
	default:
		return "Action"
	}
}

func dockerActionPastTense(action docker.ContainerAction) string {
	switch action {
	case docker.ActionStart:
		return "started"
	case docker.ActionStop:
		return "stopped"
	case docker.ActionRestart:
		return "restarted"
	default:
		return "sent"
	}
}

func dockerActionSuccessPhrase(action docker.ContainerAction) string {
	switch action {
	case docker.ActionStart:
		return "started - running"
	case docker.ActionStop:
		return "stopped - stopped"
	case docker.ActionRestart:
		return "restarted - recovered"
	default:
		return "completed"
	}
}

func dockerActionWaitingPhrase(action docker.ContainerAction) string {
	switch action {
	case docker.ActionStart:
		return "started - waiting for the app to report online"
	case docker.ActionStop:
		return "stopped - waiting for the app to stop"
	case docker.ActionRestart:
		return "restarted - waiting for recovery"
	default:
		return "sent - waiting for status"
	}
}

func dockerActionFinalWaitingPhrase(action docker.ContainerAction) string {
	switch action {
	case docker.ActionStart:
		return "started - still coming up or not responding"
	case docker.ActionStop:
		return "stopped - still appears to be running"
	case docker.ActionRestart:
		return "restarted - still coming up or not responding"
	default:
		return "sent - status did not settle"
	}
}

func dockerActionReachedExpectedState(action docker.ContainerAction, app models.AppStatus) bool {
	status := currentStatusOrUnknown(app.CurrentStatus)
	switch action {
	case docker.ActionStart, docker.ActionRestart:
		return status == models.StatusOnline
	case docker.ActionStop:
		return app.DockerState == models.DockerExited || status == models.StatusOffline
	default:
		return false
	}
}

func (a *App) adminRepairRequests(w http.ResponseWriter, _ *http.Request) {
	requests, err := a.deps.Store.RepairRequests()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, repairRequestsNewestFirst(requests))
}

func (a *App) decideRepairRequest(w http.ResponseWriter, r *http.Request) {
	if err := requireCSRF(r); err != nil {
		writeError(w, http.StatusForbidden, err)
		return
	}
	requestID := strings.TrimSpace(r.PathValue("id"))
	var body struct {
		Choice string `json:"choice"`
		Note   string `json:"note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	choice := strings.TrimSpace(body.Choice)
	if choice != "approve" && choice != "deny" {
		writeError(w, http.StatusBadRequest, errors.New("choice must be approve or deny"))
		return
	}
	request, err := a.deps.Store.RepairRequestByID(requestID)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, db.ErrNotFound) {
			status = http.StatusNotFound
		}
		writeError(w, status, err)
		return
	}
	if request.Status != models.RepairRequestPending {
		writeError(w, http.StatusConflict, errors.New("repair request is no longer pending"))
		return
	}
	if choice == "deny" {
		resolved, err := a.resolveRepairRequest(r.Context(), request, models.RepairRequestDenied, mustUser(r).ID, strings.TrimSpace(body.Note), nil)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		a.deps.Audit.Record(mustUser(r).ID, "repair_request.denied", map[string]interface{}{"request_id": request.ID, "app_id": request.AppID, "requester_id": request.RequesterID})
		writeJSON(w, http.StatusAccepted, map[string]interface{}{"status": "denied", "request": resolved})
		return
	}
	a.approveRepairRequest(w, r, request)
}

func (a *App) approveRepairRequest(w http.ResponseWriter, r *http.Request, request models.RepairRequest) {
	action, ok := agentActionDefinition(request.ActionID)
	if !ok || !action.Executable || action.DockerAction != docker.ActionRestart {
		if _, err := a.resolveRepairRequest(r.Context(), request, models.RepairRequestFailed, mustUser(r).ID, "Unsupported repair action.", nil); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeError(w, http.StatusConflict, errors.New("repair request action is not executable"))
		return
	}
	a.settingsMu.RLock()
	llmCfg := a.deps.Config.LLM
	a.settingsMu.RUnlock()
	details := map[string]interface{}{
		"request_id":            request.ID,
		"requester_id":          request.RequesterID,
		"choice":                "approve",
		"recommended_action_id": action.ID,
		"target_kind":           "app",
		"target_id":             request.AppID,
		"can_execute":           false,
	}
	if !llmCfg.AgentControlEnabled {
		a.deps.Audit.Record(mustUser(r).ID, "repair_request.control_disabled", details)
		writeError(w, http.StatusConflict, errors.New("admin-approved app fixes are disabled in LLM settings"))
		return
	}
	snapshot, err := a.readOnlySnapshot(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	app, ok := findAppByID(snapshot.Apps, request.AppID)
	if !ok {
		details["reason"] = "target_unresolved"
		a.deps.Audit.Record(mustUser(r).ID, "repair_request.refused", details)
		writeError(w, http.StatusConflict, errors.New("repair request target is no longer present in the current app snapshot"))
		return
	}
	details["app_id"] = app.AppID
	details["container_name"] = app.ContainerName
	executionAction := agentRepairActionForApp(action, app)
	details["docker_action"] = string(executionAction.DockerAction)
	if a.redactorSnapshot().IsBlacklistedApp(app) {
		details["reason"] = "privacy_blacklisted"
		a.deps.Audit.Record(mustUser(r).ID, "repair_request.refused", details)
		writeError(w, http.StatusConflict, errors.New("app fixes are unavailable for privacy-blacklisted apps"))
		return
	}
	if !app.AgentRepairAllowed {
		details["reason"] = "app_not_opted_in"
		a.deps.Audit.Record(mustUser(r).ID, "repair_request.refused", details)
		writeError(w, http.StatusConflict, errors.New("admin/AI app fix is not enabled for this app"))
		return
	}
	reviewDecision, reviewEnabled, err := a.reviewAgentAction(r.Context(), mustUser(r), snapshot, app, executionAction, "repair_request")
	if reviewEnabled {
		details["auto_review_allow"] = reviewDecision.Allow
		details["auto_review_confidence"] = reviewDecision.Confidence
		details["auto_review_summary"] = reviewDecision.Summary
	}
	if err != nil {
		details["reason"] = "auto_review_refused"
		details["error"] = err.Error()
		a.deps.Audit.Record(mustUser(r).ID, "repair_request.auto_review_refused", auditDetailsCopy(details))
		writeError(w, http.StatusConflict, err)
		return
	}
	limit := a.reserveAgentRepair(app.AppID, time.Now().UTC())
	if !limit.Allowed {
		details["reason"] = limit.Reason
		details["retry_after_seconds"] = limit.RetryAfterSeconds
		a.deps.Audit.Record(mustUser(r).ID, "repair_request.rate_limited", auditDetailsCopy(details))
		writeError(w, http.StatusConflict, errors.New(limit.Message))
		return
	}
	details["can_execute"] = true
	a.deps.Audit.Record(mustUser(r).ID, "repair_request.approved", auditDetailsCopy(details))
	result, err := a.deps.Collectors.Docker.ControlContainer(r.Context(), app, executionAction.DockerAction)
	if err != nil {
		details["error"] = err.Error()
		a.deps.Audit.Record(mustUser(r).ID, "repair_request.execute_failed", auditDetailsCopy(details))
		if _, resolveErr := a.resolveRepairRequest(r.Context(), request, models.RepairRequestFailed, mustUser(r).ID, err.Error(), nil); resolveErr != nil {
			writeError(w, http.StatusInternalServerError, resolveErr)
			return
		}
		writeError(w, http.StatusBadGateway, err)
		return
	}
	details["via"] = "repair_request"
	a.invalidateSnapshot()
	a.deps.Audit.Record(mustUser(r).ID, "repair_request.executed", auditDetailsCopy(details))
	a.deps.Audit.Record(mustUser(r).ID, "app.container.action", map[string]interface{}{"app_id": app.AppID, "action": string(executionAction.DockerAction), "container_name": app.ContainerName, "via": "repair_request", "request_id": request.ID, "recommended_action_id": action.ID})
	outcome := a.verifyAgentRepairOutcome(r.Context(), app, executionAction, result)
	repairOutcome := repairRequestOutcomeFromAgent(outcome)
	resolved, err := a.resolveRepairRequest(r.Context(), request, models.RepairRequestExecuted, mustUser(r).ID, outcome.Message, repairOutcome)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	verifyDetails := auditDetailsCopy(details)
	verifyDetails["verified"] = outcome.Verified
	verifyDetails["recovered"] = outcome.Recovered
	verifyDetails["before_status"] = string(outcome.BeforeStatus)
	verifyDetails["after_status"] = string(outcome.AfterStatus)
	verifyDetails["history_event_id"] = outcome.HistoryEventID
	if outcome.Verified {
		a.deps.Audit.Record(mustUser(r).ID, "repair_request.verified", verifyDetails)
	} else {
		a.deps.Audit.Record(mustUser(r).ID, "repair_request.verify_failed", verifyDetails)
	}
	writeJSON(w, http.StatusAccepted, map[string]interface{}{
		"status":  "executed",
		"request": resolved,
		"result":  result,
		"outcome": outcome,
	})
}

func (a *App) pendingRepairRequestFor(userID, appID, actionID string) (models.RepairRequest, bool, error) {
	requests, err := a.deps.Store.RepairRequestsForUser(userID)
	if err != nil {
		return models.RepairRequest{}, false, err
	}
	for _, request := range requests {
		if request.Status == models.RepairRequestPending && request.AppID == appID && request.ActionID == actionID {
			return request, true, nil
		}
	}
	return models.RepairRequest{}, false, nil
}

func (a *App) resolveRepairRequest(ctx context.Context, request models.RepairRequest, status models.RepairRequestStatus, actorID, note string, outcome *models.RepairRequestOutcome) (models.RepairRequest, error) {
	now := time.Now().UTC()
	request.Status = status
	request.ResolvedAt = &now
	request.ResolvedBy = actorID
	request.ResolutionNote = strings.TrimSpace(note)
	request.Outcome = outcome
	if err := a.deps.Store.UpsertRepairRequest(request); err != nil {
		return request, err
	}
	subject := "Repair request updated"
	body := request.ResolutionNote
	if body == "" && outcome != nil {
		body = outcome.Message
	}
	if body == "" {
		body = "An admin reviewed your request."
	}
	if err := a.deps.Notifications.NotifyUser(ctx, request.RequesterID, subject, body, request.AppID, "repair-request:"+request.ID+":resolved"); err != nil {
		a.deps.Audit.Record(actorID, "repair_request.notify_user_failed", map[string]interface{}{"request_id": request.ID, "requester_id": request.RequesterID, "app_id": request.AppID, "error": err.Error()})
	}
	return request, nil
}

func repairRequestOutcomeFromAgent(outcome llmAgentRepairOutcomeView) *models.RepairRequestOutcome {
	return &models.RepairRequestOutcome{
		Verified:       outcome.Verified,
		Recovered:      outcome.Recovered,
		BeforeStatus:   outcome.BeforeStatus,
		AfterStatus:    outcome.AfterStatus,
		Message:        outcome.Message,
		HistoryEventID: outcome.HistoryEventID,
		CheckedAt:      outcome.CheckedAt,
	}
}

func repairRequestsNewestFirst(requests []models.RepairRequest) []models.RepairRequest {
	out := append([]models.RepairRequest(nil), requests...)
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out
}

func isDockerRepairTarget(app models.AppStatus) bool {
	return strings.TrimSpace(app.ContainerID) != "" || strings.TrimSpace(app.ContainerName) != "" || app.DockerState != models.DockerUnknown || app.DataSource == "unraid-docker"
}

func compactAppSummaryForRequest(app models.AppStatus) string {
	summary := strings.TrimSpace(app.ServerSummary)
	if summary != "" {
		return summary
	}
	status := currentStatusOrUnknown(app.CurrentStatus)
	label := firstNonEmpty(app.DisplayName, app.AppID, "This app")
	if status == models.StatusOnline {
		return label + " was reported as working, but a user requested help."
	}
	return fmt.Sprintf("%s is %s.", label, status)
}
