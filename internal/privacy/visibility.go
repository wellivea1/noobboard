package privacy

import (
	"strings"

	"github.com/wellivea1/noobboard/internal/models"
)

func FilterSnapshotForRole(snapshot models.Snapshot, role models.Role, redactor *Redactor) models.Snapshot {
	if role == models.RoleAdmin {
		for i := range snapshot.Apps {
			logs, _ := redactor.RedactLogs(snapshot.Apps[i].RecentLogs)
			snapshot.Apps[i].RecentLogs = logs
		}
		return snapshot
	}

	roleVisibility := visibilityForRole(snapshot.Visibility, role)
	snapshot.Visibility.GeneralUserCanUseLLM = roleVisibility.CanUseLLM
	snapshot.Visibility.ShowNASStatusToUsers = roleVisibility.ShowNASStatusToUsers
	snapshot.Visibility.ShowWANStatusToUsers = roleVisibility.ShowWANStatusToUsers
	snapshot.Visibility.ShowIncidentIDsToUsers = roleVisibility.ShowIncidentIDsToUsers
	snapshot.Visibility.HiddenAppIDs = combinedList(snapshot.Visibility.HiddenAppIDs, roleVisibility.HiddenAppIDs)
	snapshot.Visibility.HiddenContainerNames = combinedList(snapshot.Visibility.HiddenContainerNames, roleVisibility.HiddenContainerNames)

	filteredApps := make([]models.AppStatus, 0, len(snapshot.Apps))
	for _, app := range snapshot.Apps {
		if !isAppVisibleToRole(app, snapshot.Visibility, role, redactor) {
			continue
		}
		app.ContainerName = ""
		app.ImageRef = ""
		app.WebURL = ""
		app.TemplatePath = ""
		app.AdminSummary = ""
		app.AllowedLogSources = nil
		app.RecentLogs = nil
		app.LLMVisibleAdmin = false
		app.RestartAllowedAdminOnly = false
		app.AgentRepairAllowed = false
		filteredApps = append(filteredApps, app)
	}
	snapshot.Apps = filteredApps
	filteredIncidents := make([]models.Incident, 0, len(snapshot.Incidents))
	for _, incident := range snapshot.Incidents {
		if isIncidentVisible(incident, filteredApps) {
			if !snapshot.Visibility.ShowIncidentIDsToUsers {
				incident.ID = ""
			}
			incident.AdminSummary = ""
			incident.Evidence = nil
			filteredIncidents = append(filteredIncidents, incident)
		}
	}
	snapshot.Incidents = filteredIncidents
	filteredFacts := make([]models.IncidentFact, 0, len(snapshot.Facts))
	for _, fact := range snapshot.Facts {
		if fact.VisibleToUsers && isFactVisible(fact, filteredApps) {
			if !snapshot.Visibility.ShowIncidentIDsToUsers {
				fact.ID = ""
			}
			fact.Evidence = nil
			filteredFacts = append(filteredFacts, fact)
		}
	}
	snapshot.Facts = filteredFacts
	applyVisibleRollup(&snapshot)
	snapshot.AdminSummary = ""
	snapshot.AuditTail = nil
	snapshot.LLMPolicies = nil
	snapshot.RepairAutomation.AdminAutoRepairAvailable = false
	snapshot.Infrastructure.UnraidUptimeSeconds = 0
	snapshot.Infrastructure.UnraidCPUBrand = ""
	snapshot.Infrastructure.UnraidCPUCores = 0
	snapshot.Infrastructure.UnraidCPUThreads = 0
	snapshot.Infrastructure.UnraidMemoryTotalBytes = 0
	snapshot.Infrastructure.UnraidMemoryUsedBytes = 0
	snapshot.Infrastructure.UnraidMemoryUsedPct = 0
	snapshot.Infrastructure.UnraidNotificationCount = 0
	snapshot.Infrastructure.UnraidAlertCount = 0
	snapshot.Infrastructure.UnraidWarningCount = 0
	snapshot.Infrastructure.UnraidVMCount = 0
	snapshot.Infrastructure.UnraidVMRunningCount = 0
	snapshot.Infrastructure.UnraidVMStoppedCount = 0
	snapshot.Infrastructure.UnraidVMNames = nil
	snapshot.Infrastructure.UnraidShareCount = 0
	snapshot.Infrastructure.UnraidShareNames = nil
	snapshot.Infrastructure.DockerNetworkCount = 0
	snapshot.Infrastructure.DockerNetworkNames = nil
	return snapshot
}

func applyVisibleRollup(snapshot *models.Snapshot) {
	status := models.StatusOnline
	for _, fact := range snapshot.Facts {
		switch fact.Severity {
		case models.SeverityCritical, models.SeverityHigh:
			status = models.StatusOffline
		case models.SeverityMedium, models.SeverityLow:
			if status != models.StatusOffline {
				status = models.StatusDegraded
			}
		}
	}
	for _, app := range snapshot.Apps {
		switch app.CurrentStatus {
		case models.StatusOffline:
			status = models.StatusOffline
		case models.StatusDegraded:
			if status != models.StatusOffline {
				status = models.StatusDegraded
			}
		case models.StatusUnknown:
			if status == models.StatusOnline {
				status = models.StatusUnknown
			}
		}
	}
	snapshot.OverallStatus = status
	switch status {
	case models.StatusOnline:
		snapshot.ServerSummary = "Everything visible is working."
	case models.StatusOffline:
		snapshot.ServerSummary = "A visible service needs attention."
	case models.StatusDegraded:
		snapshot.ServerSummary = "A visible service has a problem."
	default:
		snapshot.ServerSummary = "Visible status is still loading."
	}
}

func visibilityForRole(settings models.VisibilitySettings, role models.Role) models.RoleVisibility {
	for _, item := range settings.Roles {
		if item.Role == role {
			return item
		}
	}
	return models.RoleVisibility{
		Role:                   role,
		DisplayName:            strings.ReplaceAll(string(role), "_", " "),
		CanUseLLM:              settings.GeneralUserCanUseLLM,
		ShowNASStatusToUsers:   settings.ShowNASStatusToUsers,
		ShowWANStatusToUsers:   settings.ShowWANStatusToUsers,
		ShowIncidentIDsToUsers: settings.ShowIncidentIDsToUsers,
		HiddenAppIDs:           settings.HiddenAppIDs,
		HiddenContainerNames:   settings.HiddenContainerNames,
	}
}

func isAppVisibleToRole(app models.AppStatus, settings models.VisibilitySettings, role models.Role, redactor *Redactor) bool {
	if role == models.RoleGeneralUser && !app.VisibleToGeneralUsers {
		return false
	}
	if redactor.IsBlacklistedApp(app) {
		return false
	}
	if containsVisibilityValue(settings.HiddenAppIDs, app.AppID) || containsVisibilityValue(settings.HiddenContainerNames, app.ContainerName) {
		return false
	}
	return true
}

func isFactVisible(fact models.IncidentFact, apps []models.AppStatus) bool {
	if len(fact.AffectedServices) == 0 {
		return true
	}
	for _, affected := range fact.AffectedServices {
		for _, app := range apps {
			if strings.EqualFold(app.AppID, affected) || strings.EqualFold(app.DisplayName, affected) || strings.EqualFold(app.ContainerName, affected) {
				return true
			}
		}
	}
	return false
}

func isIncidentVisible(incident models.Incident, apps []models.AppStatus) bool {
	if len(incident.AffectedServices) == 0 {
		return true
	}
	for _, affected := range incident.AffectedServices {
		for _, app := range apps {
			if strings.EqualFold(app.AppID, affected) || strings.EqualFold(app.DisplayName, affected) || strings.EqualFold(app.ContainerName, affected) {
				return true
			}
		}
	}
	return false
}

func combinedList(first, second []string) []string {
	if len(first) == 0 {
		return append([]string(nil), second...)
	}
	if len(second) == 0 {
		return append([]string(nil), first...)
	}
	out := append([]string(nil), first...)
	for _, value := range second {
		if !containsVisibilityValue(out, value) {
			out = append(out, value)
		}
	}
	return out
}

func containsVisibilityValue(values []string, needle string) bool {
	needle = strings.TrimSpace(needle)
	if needle == "" {
		return false
	}
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), needle) {
			return true
		}
	}
	return false
}
