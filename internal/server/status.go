package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/wellivea1/noobboard/internal/adapters/docker"
	"github.com/wellivea1/noobboard/internal/db"
	"github.com/wellivea1/noobboard/internal/models"
	"github.com/wellivea1/noobboard/internal/privacy"
)

// Read-only status: the snapshot endpoints, per-app and per-subject history,
// and the uptime arithmetic behind them. Nothing here mutates.

func (a *App) statusSummary(w http.ResponseWriter, r *http.Request) {
	snapshot, err := a.latestSnapshot(r.Context(), mustUser(r).Role)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}

func (a *App) refreshStatus(w http.ResponseWriter, r *http.Request) {
	if err := requireCSRF(r); err != nil {
		writeError(w, http.StatusForbidden, err)
		return
	}
	full, err := a.refreshSnapshot(r.Context(), true)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if role := mustUser(r).Role; role != models.RoleAdmin {
		writeJSON(w, http.StatusOK, privacy.FilterSnapshotForRole(full, role, a.redactorSnapshot()))
		return
	}
	writeJSON(w, http.StatusOK, full)
}

func (a *App) apps(w http.ResponseWriter, r *http.Request) {
	snapshot, err := a.latestSnapshot(r.Context(), mustUser(r).Role)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, snapshot.Apps)
}

func (a *App) appByID(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/apps/")
	app, ok, err := a.visibleAppByID(r.Context(), mustUser(r).Role, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if ok {
		writeJSON(w, http.StatusOK, app)
		return
	}
	writeError(w, http.StatusNotFound, db.ErrNotFound)
}

func (a *App) appHistory(w http.ResponseWriter, r *http.Request) {
	if a.deps.History == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("status history is not configured"))
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeError(w, http.StatusBadRequest, errors.New("app id is required"))
		return
	}
	role := mustUser(r).Role
	app, ok, err := a.visibleAppByID(r.Context(), role, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, db.ErrNotFound)
		return
	}
	window := parseHistoryWindow(r.URL.Query().Get("window"))
	limit := parseHistoryLimit(r.URL.Query().Get("limit"))
	history, err := a.statusHistory(models.SubjectApp, app.AppID, app.DisplayName, app.CurrentStatus, app.LastSeenOnline, app.LastSeenOffline, window, limit, role)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, history)
}

func (a *App) infrastructureHistory(w http.ResponseWriter, r *http.Request) {
	if a.deps.History == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("status history is not configured"))
		return
	}
	subject := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("subject")))
	if subject == "" {
		writeError(w, http.StatusBadRequest, errors.New("history subject is required"))
		return
	}
	role := mustUser(r).Role
	snapshot, err := a.latestSnapshot(r.Context(), role)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	current, displayName, ok := visibleInfraHistorySubject(subject, snapshot, role)
	if !ok {
		writeError(w, http.StatusNotFound, db.ErrNotFound)
		return
	}
	window := parseHistoryWindow(r.URL.Query().Get("window"))
	limit := parseHistoryLimit(r.URL.Query().Get("limit"))
	history, err := a.statusHistory(models.SubjectInfra, subject, displayName, current, nil, nil, window, limit, role)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, history)
}

// agentAppLogs backs the noobboard_app_logs tool.
//
// Three things have to hold before a line reaches a provider, and each is
// checked here rather than trusted from the caller:
//   - the app must be visible to the requesting role (visibleAppByID),
//   - the redactor must exist — no redactor means no logs, not raw logs,
//   - the read is audited like the equivalent admin endpoint.
//
// Returns nil when logs cannot be served, which makes the tool unavailable
// rather than silently empty.
func (a *App) agentAppLogs(role models.Role) func(context.Context, string, int) ([]models.LogLine, error) {
	if a.deps.Redactor == nil || a.deps.Collectors.Docker == nil {
		return nil
	}
	return func(ctx context.Context, appID string, limit int) ([]models.LogLine, error) {
		app, ok, err := a.visibleAppByID(ctx, role, appID)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, db.ErrNotFound
		}
		lines, err := a.deps.Collectors.Docker.Logs(ctx, app, docker.LogOptions{Limit: limit})
		if err != nil {
			return nil, err
		}
		redacted, changed := a.deps.Redactor.RedactLogs(lines)
		a.deps.Audit.Record("llm", "app.container.logs", map[string]interface{}{
			"app_id":         app.AppID,
			"container_name": app.ContainerName,
			"line_count":     len(redacted),
			"redacted":       changed,
			"via":            "agent_tool",
		})
		return redacted, nil
	}
}

// agentAppHistory backs the noobboard_app_history tool. Same visibility rule as
// the logs tool; history carries no free text from the container, so it needs
// no redaction beyond what statusHistory already applies for the role.
func (a *App) agentAppHistory(role models.Role) func(context.Context, string) (models.StatusHistory, error) {
	if a.deps.History == nil {
		return nil
	}
	return func(ctx context.Context, appID string) (models.StatusHistory, error) {
		app, ok, err := a.visibleAppByID(ctx, role, appID)
		if err != nil {
			return models.StatusHistory{}, err
		}
		if !ok {
			return models.StatusHistory{}, db.ErrNotFound
		}
		// A 24h window is what answers "is this a loop or a one-off"; a wider one
		// mostly adds noise the model has to pay for in tokens.
		return a.statusHistory(models.SubjectApp, app.AppID, app.DisplayName, app.CurrentStatus, app.LastSeenOnline, app.LastSeenOffline, 24*time.Hour, agentHistoryEventLimit, role)
	}
}

func (a *App) statusHistory(subjectType models.StatusSubjectType, subjectID, displayName string, current models.CurrentStatus, lastOnline, lastOffline *time.Time, window time.Duration, limit int, role models.Role) (models.StatusHistory, error) {
	now := time.Now().UTC()
	since := now.Add(-window)
	allEvents, err := a.deps.History.Query(db.HistoryFilter{SubjectType: subjectType, SubjectID: subjectID})
	if err != nil {
		return models.StatusHistory{}, err
	}
	responseEvents := make([]models.StatusEvent, 0, len(allEvents))
	for _, event := range allEvents {
		if event.At.Before(since) {
			continue
		}
		event.Note = a.deps.Redactor.RedactString(event.Note).Text
		if role != models.RoleAdmin && subjectType == models.SubjectInfra {
			event = plainGeneralInfraEvent(event)
		}
		responseEvents = append(responseEvents, event)
		if limit > 0 && len(responseEvents) >= limit {
			break
		}
	}
	uptime24h := uptimePct(allEvents, current, 24*time.Hour, now)
	uptime7d := uptimePct(allEvents, current, 7*24*time.Hour, now)
	return models.StatusHistory{
		SubjectType:     subjectType,
		SubjectID:       subjectID,
		DisplayName:     displayName,
		Current:         current,
		LastSeenOnline:  lastOnline,
		LastSeenOffline: lastOffline,
		UptimePct24h:    uptime24h,
		UptimePct7d:     uptime7d,
		Events:          responseEvents,
	}, nil
}

func (a *App) adminStatus(w http.ResponseWriter, r *http.Request) {
	snapshot, err := a.latestSnapshot(r.Context(), models.RoleAdmin)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}

func (a *App) adminIncidents(w http.ResponseWriter, r *http.Request) {
	snapshot, err := a.latestSnapshot(r.Context(), models.RoleAdmin)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, snapshot.Incidents)
}

func (a *App) adminAudit(w http.ResponseWriter, _ *http.Request) {
	tail, err := a.deps.Store.AuditTail(200)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, tail)
}

// adminDataSummary reports what recorded data exists so an admin can decide what
// to clear without guessing. Counts are per store, plus a per-app breakdown for
// status history, because the usual reason to clear anything is that one app's
// recorded churn is skewing its diagnosis.
