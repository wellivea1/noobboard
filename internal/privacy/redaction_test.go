package privacy

import (
	"strings"
	"testing"

	"github.com/wellivea1/noobboard/internal/config"
	"github.com/wellivea1/noobboard/internal/models"
)

func TestRedactsSecretsTokensEmailsAndBlacklistedPaths(t *testing.T) {
	redactor := NewRedactor(config.PrivacyConfig{
		BlacklistFolderPaths:   []string{`C:\SensitiveShare\private`},
		BlacklistEnvNames:      []string{"*_SECRET"},
		BlacklistFilenameGlobs: []string{"*.key"},
		RedactEmails:           true,
	})
	result := redactor.RedactString(`api_key=secret token=abc password=hunter2 user test@example.com c:\sensitiveshare\private\file.mkv MY_SECRET=abc C:\tmp\id_rsa.key`)
	for _, leaked := range []string{"secret", "abc", "hunter2", "test@example.com", `sensitiveshare\private`, "MY_SECRET=abc", "id_rsa.key"} {
		if strings.Contains(result.Text, leaked) {
			t.Fatalf("redacted text leaked %q: %s", leaked, result.Text)
		}
	}
	if !result.Changed {
		t.Fatal("expected redaction to report a change")
	}
}

func TestBlacklistAppMatch(t *testing.T) {
	redactor := NewRedactor(config.PrivacyConfig{
		BlacklistAppIDs:         []string{"secret-app"},
		BlacklistContainerNames: []string{"secret-container"},
	})
	if !redactor.IsBlacklistedApp(models.AppStatus{AppID: "secret-app"}) {
		t.Fatal("expected app id blacklist match")
	}
	if !redactor.IsBlacklistedApp(models.AppStatus{ContainerName: "SECRET-CONTAINER"}) {
		t.Fatal("expected case-insensitive container blacklist match")
	}
}

func TestGeneralUserVisibilityFiltersHiddenAppsAndLogs(t *testing.T) {
	redactor := NewRedactor(config.PrivacyConfig{})
	snapshot := models.Snapshot{
		Visibility: models.VisibilitySettings{ShowIncidentIDsToUsers: false},
		Apps: []models.AppStatus{
			{AppID: "visible", DisplayName: "Visible", ContainerName: "visible", ImageRef: "repo/private:latest", WebURL: "http://nas.local:32400", TemplatePath: "/boot/template.xml", VisibleToGeneralUsers: true, CurrentStatus: models.StatusOnline, AdminSummary: "admin", RecentLogs: []models.LogLine{{Line: "secret"}}},
			{AppID: "hidden", DisplayName: "Hidden", ContainerName: "hidden", VisibleToGeneralUsers: false, CurrentStatus: models.StatusOffline},
		},
		Facts:     []models.IncidentFact{{ID: "fact-1", Type: models.IncidentAppDown, Summary: "Visible down", VisibleToUsers: true}},
		Incidents: []models.Incident{{ID: "incident-1", Type: models.IncidentAppDown, Summary: "Visible down", AffectedServices: []string{"visible"}}},
	}
	filtered := FilterSnapshotForRole(snapshot, models.RoleGeneralUser, redactor)
	if len(filtered.Apps) != 1 || filtered.Apps[0].AppID != "visible" {
		t.Fatalf("unexpected apps after filtering: %#v", filtered.Apps)
	}
	if filtered.Apps[0].ContainerName != "" || filtered.Apps[0].ImageRef != "" || filtered.Apps[0].WebURL != "" || filtered.Apps[0].TemplatePath != "" || filtered.Apps[0].AdminSummary != "" || len(filtered.Apps[0].RecentLogs) != 0 {
		t.Fatalf("general user app leaked admin-only fields: %#v", filtered.Apps[0])
	}
	if filtered.Facts[0].ID != "" || filtered.Incidents[0].ID != "" {
		t.Fatalf("general user IDs were not hidden: facts=%#v incidents=%#v", filtered.Facts, filtered.Incidents)
	}
}

func TestCustomRoleVisibilityFiltersAppsFactsAndIncidents(t *testing.T) {
	redactor := NewRedactor(config.PrivacyConfig{})
	snapshot := models.Snapshot{
		Visibility: models.VisibilitySettings{
			Roles: []models.RoleVisibility{
				{
					Role:                   "kids",
					DisplayName:            "Kids",
					CanUseLLM:              true,
					ShowNASStatusToUsers:   true,
					ShowWANStatusToUsers:   true,
					ShowIncidentIDsToUsers: false,
					HiddenAppIDs:           []string{"hidden"},
				},
			},
		},
		Apps: []models.AppStatus{
			{AppID: "visible", DisplayName: "Visible", ContainerName: "visible", VisibleToGeneralUsers: true, CurrentStatus: models.StatusOnline},
			{AppID: "hidden", DisplayName: "Hidden", ContainerName: "hidden", VisibleToGeneralUsers: true, CurrentStatus: models.StatusOffline},
		},
		Facts: []models.IncidentFact{
			{ID: "visible-fact", Type: models.IncidentAppDown, Summary: "Visible down", AffectedServices: []string{"visible"}, VisibleToUsers: true},
			{ID: "hidden-fact", Type: models.IncidentAppDown, Summary: "Hidden down", AffectedServices: []string{"hidden"}, VisibleToUsers: true},
		},
		Incidents: []models.Incident{
			{ID: "visible-incident", Type: models.IncidentAppDown, Summary: "Visible down", AffectedServices: []string{"visible"}},
			{ID: "hidden-incident", Type: models.IncidentAppDown, Summary: "Hidden down", AffectedServices: []string{"hidden"}},
		},
	}
	filtered := FilterSnapshotForRole(snapshot, "kids", redactor)
	if len(filtered.Apps) != 1 || filtered.Apps[0].AppID != "visible" {
		t.Fatalf("unexpected apps after custom role filtering: %#v", filtered.Apps)
	}
	if len(filtered.Facts) != 1 || strings.Contains(filtered.Facts[0].Summary, "Hidden") {
		t.Fatalf("hidden app fact leaked: %#v", filtered.Facts)
	}
	if len(filtered.Incidents) != 1 || strings.Contains(filtered.Incidents[0].Summary, "Hidden") {
		t.Fatalf("hidden app incident leaked: %#v", filtered.Incidents)
	}
}
