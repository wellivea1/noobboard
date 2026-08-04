package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/wellivea1/noobboard/internal/adapters/docker"
	"github.com/wellivea1/noobboard/internal/config"
	"github.com/wellivea1/noobboard/internal/db"
	"github.com/wellivea1/noobboard/internal/models"
	"github.com/wellivea1/noobboard/internal/users"
)

// App identity, control, and logs. The identity helpers are the fiddly part:
// Unraid, Docker and the app catalogue each name the same container
// differently, and a control action that resolves the wrong one is unsafe.

func (a *App) listUsers(w http.ResponseWriter, _ *http.Request) {
	usersList, err := a.deps.Users.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, usersList)
}

func (a *App) saveUser(w http.ResponseWriter, r *http.Request) {
	if err := requireCSRF(r); err != nil {
		writeError(w, http.StatusForbidden, err)
		return
	}
	var body users.SaveUserRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	defaultRole := a.defaultRole()
	user, err := a.deps.Users.Save(body, defaultRole)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	a.deps.Audit.Record(mustUser(r).ID, "user.saved", map[string]interface{}{"username": user.Username, "role": string(user.Role), "disabled": user.Disabled})
	writeJSON(w, http.StatusOK, user)
}

func (a *App) adminAppMutation(w http.ResponseWriter, r *http.Request) {
	switch {
	case strings.HasSuffix(r.URL.Path, "/icon"):
		a.updateAppIcon(w, r)
	case strings.HasSuffix(r.URL.Path, "/action"):
		a.controlApp(w, r)
	default:
		writeError(w, http.StatusNotFound, db.ErrNotFound)
	}
}

func (a *App) adminAppRead(w http.ResponseWriter, r *http.Request) {
	switch {
	case strings.HasSuffix(r.URL.Path, "/logs"):
		a.appLogs(w, r)
	default:
		writeError(w, http.StatusNotFound, db.ErrNotFound)
	}
}

func (a *App) updateAppIcon(w http.ResponseWriter, r *http.Request) {
	if err := requireCSRF(r); err != nil {
		writeError(w, http.StatusForbidden, err)
		return
	}
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/admin/apps/"), "/icon")
	id = strings.Trim(id, "/")
	if id == "" {
		writeError(w, http.StatusBadRequest, errors.New("app id is required"))
		return
	}
	var body struct {
		IconURL string `json:"icon_url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	iconURL, err := config.NormalizeIconURL(body.IconURL)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	a.settingsMu.Lock()
	if a.deps.Config.AppCatalog.IconOverrides == nil {
		a.deps.Config.AppCatalog.IconOverrides = map[string]string{}
	}
	if iconURL == "" {
		delete(a.deps.Config.AppCatalog.IconOverrides, id)
		delete(a.deps.Config.AppCatalog.IconOverrides, strings.ToLower(id))
	} else {
		a.deps.Config.AppCatalog.IconOverrides[id] = iconURL
		a.deps.Config.AppCatalog.IconOverrides[strings.ToLower(id)] = iconURL
	}
	settings := a.currentRuntimeSettingsLocked()
	a.settingsMu.Unlock()
	if err := a.deps.Store.SaveRuntimeSettings(settings); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	a.invalidateSnapshot()
	a.deps.Audit.Record(mustUser(r).ID, "app.icon.saved", map[string]interface{}{"app_id": id, "has_icon": iconURL != ""})
	writeJSON(w, http.StatusOK, a.configSnapshot().AppCatalog)
}

func (a *App) controlApp(w http.ResponseWriter, r *http.Request) {
	if err := requireCSRF(r); err != nil {
		writeError(w, http.StatusForbidden, err)
		return
	}
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/admin/apps/"), "/action")
	id = strings.Trim(id, "/")
	if id == "" {
		writeError(w, http.StatusBadRequest, errors.New("app id is required"))
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
	snapshot, err := a.readOnlySnapshot(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	app, ok := findAppByID(snapshot.Apps, id)
	if !ok {
		writeError(w, http.StatusNotFound, db.ErrNotFound)
		return
	}
	if dockerActionRequiresConfirmation(action) && (!body.Confirmed || !sameAppIdentifier(body.ConfirmAppID, app.AppID)) {
		writeError(w, http.StatusBadRequest, errors.New("stop and restart require confirmed=true with a matching confirm_app_id"))
		return
	}
	result, err := a.deps.Collectors.Docker.ControlContainer(r.Context(), app, action)
	if err != nil {
		a.deps.Audit.Record(mustUser(r).ID, "app.container.action_failed", map[string]interface{}{"app_id": id, "action": string(action), "error": err.Error()})
		writeError(w, http.StatusBadGateway, err)
		return
	}
	a.deps.Audit.Record(mustUser(r).ID, "app.container.action", map[string]interface{}{"app_id": id, "action": string(action), "container_name": app.ContainerName})
	writeJSON(w, http.StatusAccepted, result)
}

func dockerActionRequiresConfirmation(action docker.ContainerAction) bool {
	return action == docker.ActionStop || action == docker.ActionRestart
}

func sameAppIdentifier(left, right string) bool {
	return strings.EqualFold(strings.TrimSpace(left), strings.TrimSpace(right)) && strings.TrimSpace(right) != ""
}

func (a *App) appLogs(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/admin/apps/"), "/logs")
	id = strings.Trim(id, "/")
	if id == "" {
		writeError(w, http.StatusBadRequest, errors.New("app id is required"))
		return
	}
	limit := parseLogLimit(r.URL.Query().Get("limit"))
	snapshot, err := a.readOnlySnapshot(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	app, ok := findAppByID(snapshot.Apps, id)
	if !ok {
		writeError(w, http.StatusNotFound, db.ErrNotFound)
		return
	}
	lines, err := a.deps.Collectors.Docker.Logs(r.Context(), app, docker.LogOptions{Limit: limit})
	if err != nil {
		a.deps.Audit.Record(mustUser(r).ID, "app.container.logs_failed", map[string]interface{}{"app_id": id, "container_name": app.ContainerName, "error": err.Error()})
		writeError(w, http.StatusBadGateway, err)
		return
	}
	redacted, changed := a.deps.Redactor.RedactLogs(lines)
	a.deps.Audit.Record(mustUser(r).ID, "app.container.logs", map[string]interface{}{"app_id": id, "container_name": app.ContainerName, "line_count": len(redacted), "redacted": changed})
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"app_id":         app.AppID,
		"container_name": app.ContainerName,
		"limit":          limit,
		"redacted":       changed,
		"logs":           redacted,
	})
}

func parseLogLimit(value string) int {
	limit := defaultLogLimit
	if strings.TrimSpace(value) != "" {
		if parsed, err := strconv.Atoi(strings.TrimSpace(value)); err == nil {
			limit = parsed
		}
	}
	if limit < 1 {
		return 1
	}
	if limit > maxLogLimit {
		return maxLogLimit
	}
	return limit
}

func parseHistoryLimit(value string) int {
	limit := 100
	if strings.TrimSpace(value) != "" {
		if parsed, err := strconv.Atoi(strings.TrimSpace(value)); err == nil {
			limit = parsed
		}
	}
	if limit < 1 {
		return 1
	}
	if limit > 500 {
		return 500
	}
	return limit
}

func parseNotificationLimit(value string) int {
	limit := 20
	if strings.TrimSpace(value) != "" {
		if parsed, err := strconv.Atoi(strings.TrimSpace(value)); err == nil {
			limit = parsed
		}
	}
	if limit < 1 {
		return 1
	}
	if limit > 100 {
		return 100
	}
	return limit
}

func parseHistoryWindow(value string) time.Duration {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return 7 * 24 * time.Hour
	}
	if strings.HasSuffix(value, "d") {
		days, err := strconv.Atoi(strings.TrimSpace(strings.TrimSuffix(value, "d")))
		if err == nil && days > 0 {
			return time.Duration(days) * 24 * time.Hour
		}
	}
	if duration, err := time.ParseDuration(value); err == nil && duration > 0 {
		return duration
	}
	return 7 * 24 * time.Hour
}

func findAppByID(apps []models.AppStatus, id string) (models.AppStatus, bool) {
	id = normalizeAppIdentifier(id)
	if id == "" {
		return models.AppStatus{}, false
	}
	for _, app := range apps {
		for _, candidate := range appIdentityCandidates(app) {
			if normalizeAppIdentifier(candidate) == id {
				return app, true
			}
		}
	}
	return models.AppStatus{}, false
}

func (a *App) visibleAppByID(ctx context.Context, role models.Role, id string) (models.AppStatus, bool, error) {
	visibleSnapshot, err := a.latestSnapshot(ctx, role)
	if err != nil {
		return models.AppStatus{}, false, err
	}
	if app, ok := findAppByID(visibleSnapshot.Apps, id); ok {
		return app, true, nil
	}
	if role == models.RoleAdmin {
		return models.AppStatus{}, false, nil
	}
	fullSnapshot, err := a.latestFullSnapshot(ctx)
	if err != nil {
		return models.AppStatus{}, false, err
	}
	fullApp, ok := findAppByID(fullSnapshot.Apps, id)
	if !ok {
		return models.AppStatus{}, false, nil
	}
	app, ok := findAppBySameIdentity(visibleSnapshot.Apps, fullApp)
	return app, ok, nil
}

func findAppBySameIdentity(apps []models.AppStatus, target models.AppStatus) (models.AppStatus, bool) {
	for _, candidate := range appIdentityCandidates(target) {
		if app, ok := findAppByID(apps, candidate); ok {
			return app, true
		}
	}
	return models.AppStatus{}, false
}

func appIdentityCandidates(app models.AppStatus) []string {
	candidates := []string{
		app.AppID,
		app.ContainerID,
		app.ContainerName,
		app.DisplayName,
	}
	seen := make(map[string]struct{}, len(candidates))
	out := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		normalized := normalizeAppIdentifier(candidate)
		if normalized == "" {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, candidate)
	}
	return out
}

func normalizeAppIdentifier(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "/")
	return strings.ToLower(value)
}

func visibleInfraHistorySubject(subject string, snapshot models.Snapshot, role models.Role) (models.CurrentStatus, string, bool) {
	infra := snapshot.Infrastructure
	if role != models.RoleAdmin {
		switch subject {
		case "internet":
			return boolHistoryStatus(infra.InternetReachable), "Internet", true
		case "nas":
			if !snapshot.Visibility.ShowNASStatusToUsers {
				return "", "", false
			}
			return boolHistoryStatus(infra.NASReachable), "Server", true
		default:
			return "", "", false
		}
	}
	switch subject {
	case "internet":
		return boolHistoryStatus(infra.InternetReachable), "Internet", true
	case "dns":
		return boolHistoryStatus(infra.DNSOK), "DNS", true
	case "wan":
		if role != models.RoleAdmin && !snapshot.Visibility.ShowWANStatusToUsers {
			return "", "", false
		}
		return boolHistoryStatus(infra.UniFiWANUp), "WAN", true
	case "nas":
		if role != models.RoleAdmin && !snapshot.Visibility.ShowNASStatusToUsers {
			return "", "", false
		}
		return boolHistoryStatus(infra.NASReachable), "NAS", true
	case "unraid_array":
		if role != models.RoleAdmin && !snapshot.Visibility.ShowNASStatusToUsers {
			return "", "", false
		}
		return arrayHistoryStatus(infra), "Unraid array", true
	default:
		return "", "", false
	}
}

func plainGeneralInfraEvent(event models.StatusEvent) models.StatusEvent {
	event.DisplayName = plainGeneralInfraDisplayName(event.SubjectID)
	event.Note = plainGeneralInfraNote(event.SubjectID, event.To)
	return event
}

func plainGeneralInfraDisplayName(subjectID string) string {
	switch strings.ToLower(strings.TrimSpace(subjectID)) {
	case "nas", "unraid_array":
		return "Server"
	default:
		return "Internet"
	}
}

func plainGeneralInfraNote(subjectID string, status models.CurrentStatus) string {
	name := plainGeneralInfraDisplayName(subjectID)
	switch status {
	case models.StatusOnline:
		return name + " is working."
	case models.StatusDegraded:
		return name + " has a problem."
	case models.StatusOffline:
		if name == "Server" {
			return "Server is not responding."
		}
		return "Internet is not working."
	default:
		return name + " status changed."
	}
}

func boolHistoryStatus(ok bool) models.CurrentStatus {
	if ok {
		return models.StatusOnline
	}
	return models.StatusOffline
}

func arrayHistoryStatus(infra models.InfrastructureStatus) models.CurrentStatus {
	if !infra.UnraidAPIReachable || strings.TrimSpace(infra.UnraidArrayState) == "" {
		return models.StatusUnknown
	}
	if infra.UnraidArrayHealthy {
		return models.StatusOnline
	}
	if strings.EqualFold(strings.TrimSpace(infra.UnraidArrayState), "started") {
		return models.StatusDegraded
	}
	return models.StatusOffline
}

func uptimePct(events []models.StatusEvent, current models.CurrentStatus, window time.Duration, now time.Time) *float64 {
	if window <= 0 {
		return nil
	}
	start := now.Add(-window)
	cursor := now
	status := current
	online := time.Duration(0)
	for _, event := range events {
		if event.At.After(now) {
			continue
		}
		if !event.At.After(start) {
			break
		}
		if status == models.StatusOnline {
			online += cursor.Sub(event.At)
		}
		status = event.From
		cursor = event.At
	}
	if cursor.After(start) && status == models.StatusOnline {
		online += cursor.Sub(start)
	}
	pct := float64(online) / float64(window) * 100
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	return &pct
}
