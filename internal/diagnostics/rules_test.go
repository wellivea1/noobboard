package diagnostics

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wellivea1/noobboard/internal/adapters/fixture"
	"github.com/wellivea1/noobboard/internal/models"
)

func loadFixture(t *testing.T, name string) models.Snapshot {
	t.Helper()
	snapshot, err := fixture.LoadSnapshot(filepath.Join("..", "..", "fixtures"), name)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func TestIncidentRuleEngineDistinguishesNASOutage(t *testing.T) {
	result := NewRuleEngine().Evaluate(loadFixture(t, "nas_unreachable"))
	if result.OverallStatus != models.StatusOffline {
		t.Fatalf("overall status = %s", result.OverallStatus)
	}
	if len(result.Facts) == 0 || result.Facts[0].Type != models.IncidentNASUnreachable {
		t.Fatalf("expected NAS outage fact, got %#v", result.Facts)
	}
}

func TestIncidentRuleEngineDistinguishesDockerServiceOutage(t *testing.T) {
	result := NewRuleEngine().Evaluate(loadFixture(t, "multiple_apps_down_due_to_docker_service"))
	if len(result.Facts) == 0 || result.Facts[0].Type != models.IncidentDockerServiceDown {
		t.Fatalf("expected docker service fact, got %#v", result.Facts)
	}
	for _, fact := range result.Facts {
		if fact.Type == models.IncidentAppDown {
			t.Fatalf("app-down fact should be suppressed during docker outage: %#v", result.Facts)
		}
	}
}

func TestAppStatusNormalization(t *testing.T) {
	result := NewRuleEngine().Evaluate(loadFixture(t, "container_running_http_failed"))
	if result.Apps[0].CurrentStatus != models.StatusDegraded {
		t.Fatalf("expected degraded app, got %s", result.Apps[0].CurrentStatus)
	}
	if len(result.Facts) == 0 || result.Facts[0].Type != models.IncidentAppDegraded {
		t.Fatalf("expected app degraded fact, got %#v", result.Facts)
	}
	evidence := strings.Join(result.Facts[0].Evidence, "; ")
	if !strings.Contains(evidence, "HTTP probe to http://emby.local failed: 503 Service Unavailable") {
		t.Fatalf("expected specific HTTP probe evidence, got %q", evidence)
	}
	if strings.Contains(evidence, "HTTP/TCP probe failed") {
		t.Fatalf("generic HTTP/TCP evidence should not be emitted: %q", evidence)
	}
}

func TestStorageWarningDoesNotRecommendAppRestartFact(t *testing.T) {
	result := NewRuleEngine().Evaluate(loadFixture(t, "disk_smart_warning"))
	found := false
	for _, fact := range result.Facts {
		if fact.Type == models.IncidentStorageWarning {
			found = true
		}
		if fact.Type == models.IncidentAppDown {
			t.Fatal("storage warning should not become app down")
		}
	}
	if !found {
		t.Fatalf("expected storage warning fact, got %#v", result.Facts)
	}
}

func TestUniFiOfflineDevicesProduceNetworkFact(t *testing.T) {
	snapshot := loadFixture(t, "all_systems_online")
	snapshot.Infrastructure.UniFiOfflineDeviceCount = 1
	snapshot.Infrastructure.UniFiWarnings = []string{"Office AP is OFFLINE"}
	result := NewRuleEngine().Evaluate(snapshot)
	found := false
	for _, fact := range result.Facts {
		if fact.ID == "unifi_devices_offline" {
			found = true
			if fact.Type != models.IncidentUnifiIssue || fact.Severity != models.SeverityMedium {
				t.Fatalf("unexpected UniFi fact: %#v", fact)
			}
		}
	}
	if !found {
		t.Fatalf("expected UniFi offline-device fact, got %#v", result.Facts)
	}
}

func TestNASLinkSpeedProducesAdminOnlyUniFiFact(t *testing.T) {
	snapshot := loadFixture(t, "all_systems_online")
	snapshot.Infrastructure.NASLinkSpeedMbps = 100
	snapshot.Infrastructure.ExpectedNASLinkMbps = 1000
	result := NewRuleEngine().Evaluate(snapshot)
	found := false
	for _, fact := range result.Facts {
		if fact.ID == "nas_link_speed_degraded" {
			found = true
			if fact.Type != models.IncidentUnifiIssue || fact.Severity != models.SeverityMedium || fact.VisibleToUsers {
				t.Fatalf("unexpected NAS link fact: %#v", fact)
			}
			if !strings.Contains(strings.Join(fact.Evidence, "; "), "NAS link 100 Mbps expected 1000 Mbps") {
				t.Fatalf("unexpected evidence: %#v", fact.Evidence)
			}
		}
	}
	if !found {
		t.Fatalf("expected NAS link-speed fact, got %#v", result.Facts)
	}
}

func TestUnraidUnreadNotificationsProduceAdminOnlyFact(t *testing.T) {
	snapshot := loadFixture(t, "all_systems_online")
	snapshot.Infrastructure.UnraidNotificationCount = 3
	snapshot.Infrastructure.UnraidAlertCount = 1
	snapshot.Infrastructure.UnraidWarningCount = 2
	result := NewRuleEngine().Evaluate(snapshot)
	found := false
	for _, fact := range result.Facts {
		if fact.ID == "unraid_notifications" {
			found = true
			if fact.Type != models.IncidentStorageWarning || fact.Severity != models.SeverityHigh || fact.VisibleToUsers {
				t.Fatalf("unexpected Unraid notification fact: %#v", fact)
			}
			if !strings.Contains(strings.Join(fact.Evidence, "; "), "1 alert(s), 2 warning(s)") {
				t.Fatalf("unexpected evidence: %#v", fact.Evidence)
			}
		}
	}
	if !found {
		t.Fatalf("expected Unraid notification fact, got %#v", result.Facts)
	}
}

func healthyUnraidSnapshot() models.Snapshot {
	return models.Snapshot{
		GeneratedAt: time.Now().UTC(),
		Infrastructure: models.InfrastructureStatus{
			NASReachable:           true,
			UnraidAPIReachable:     true,
			UnraidArrayState:       "started",
			UnraidArrayHealthy:     true,
			DockerServiceAvailable: true,
			SourceHealth:           models.SourceHealth{Unraid: "ok", Docker: "ok", UniFi: "ok", Probes: "ok"},
		},
	}
}

func factByID(result Result, id string) (models.IncidentFact, bool) {
	for _, fact := range result.Facts {
		if fact.ID == id {
			return fact, true
		}
	}
	return models.IncidentFact{}, false
}

func TestArrayCapacityRuleEscalatesWhenAlmostFull(t *testing.T) {
	// A full array is a common cause of containers failing to start and was
	// silent: the percentage was collected on every poll and read by nothing.
	engine := NewRuleEngine()

	snapshot := healthyUnraidSnapshot()
	snapshot.Infrastructure.ArrayCapacityTotalBytes = 8 << 40
	snapshot.Infrastructure.ArrayCapacityUsedPct = 80
	if _, ok := factByID(engine.Evaluate(snapshot), "array_capacity_high"); ok {
		t.Fatal("capacity fact fired at 80% used")
	}

	snapshot.Infrastructure.ArrayCapacityUsedPct = 91
	fact, ok := factByID(engine.Evaluate(snapshot), "array_capacity_high")
	if !ok || fact.Severity != models.SeverityMedium {
		t.Fatalf("fact at 91%% = %#v, want medium severity", fact)
	}

	snapshot.Infrastructure.ArrayCapacityUsedPct = 98
	fact, ok = factByID(engine.Evaluate(snapshot), "array_capacity_high")
	if !ok || fact.Severity != models.SeverityHigh {
		t.Fatalf("fact at 98%% = %#v, want high severity", fact)
	}
}

func TestCapacityAndMemoryRulesStaySilentWithoutData(t *testing.T) {
	// Zero is "not collected", not "empty array" or "no memory". Firing on a
	// missing reading would make every fixture and every partial API look broken.
	engine := NewRuleEngine()
	result := engine.Evaluate(healthyUnraidSnapshot())
	if _, ok := factByID(result, "array_capacity_high"); ok {
		t.Fatal("capacity fact fired with no capacity reading")
	}
	if _, ok := factByID(result, "memory_pressure"); ok {
		t.Fatal("memory fact fired with no memory reading")
	}
}

func TestMemoryPressureRuleFiresOnlyWhenHigh(t *testing.T) {
	engine := NewRuleEngine()
	snapshot := healthyUnraidSnapshot()
	snapshot.Infrastructure.UnraidMemoryTotalBytes = 32 << 30

	snapshot.Infrastructure.UnraidMemoryUsedPct = 70
	if _, ok := factByID(engine.Evaluate(snapshot), "memory_pressure"); ok {
		t.Fatal("memory fact fired at 70% used")
	}

	snapshot.Infrastructure.UnraidMemoryUsedPct = 95
	if _, ok := factByID(engine.Evaluate(snapshot), "memory_pressure"); !ok {
		t.Fatal("memory fact did not fire at 95% used")
	}
}

func TestRestartLoopIsADistinctIncidentFromBeingDown(t *testing.T) {
	// Restarting a crash loop restarts the loop, so a flapping container must not
	// be reported as an ordinary app_down that invites a restart recommendation.
	engine := NewRuleEngine()
	snapshot := healthyUnraidSnapshot()
	app := models.AppStatus{
		AppID:         "emby",
		DisplayName:   "Emby",
		CurrentStatus: models.StatusOffline,
		DockerState:   models.DockerExited,
		ServerSummary: "Emby is offline.",
		AdminSummary:  "emby state=exited",
	}

	snapshot.Apps = []models.AppStatus{app}
	result := engine.Evaluate(snapshot)
	fact, ok := factByID(result, "app_emby_offline")
	if !ok || fact.Type != models.IncidentAppDown {
		t.Fatalf("steady failure = %#v, want app_down", fact)
	}

	app.RecentStatusChanges = restartLoopChanges
	snapshot.Apps = []models.AppStatus{app}
	result = engine.Evaluate(snapshot)
	fact, ok = factByID(result, "app_emby_offline")
	if !ok || fact.Type != models.IncidentAppRestartLoop || fact.Severity != models.SeverityHigh {
		t.Fatalf("flapping app = %#v, want a high-severity restart loop", fact)
	}
	joined := strings.Join(fact.Evidence, " ")
	if !strings.Contains(joined, "will not fix") {
		t.Fatalf("evidence %q does not warn against restarting again", joined)
	}
}

func TestExitDetailReachesAppEvidence(t *testing.T) {
	// The parsed exit code has to survive into the evidence the model reads, not
	// stop at a struct field.
	engine := NewRuleEngine()
	snapshot := healthyUnraidSnapshot()
	code := 137
	snapshot.Apps = []models.AppStatus{{
		AppID:            "emby",
		DisplayName:      "Emby",
		CurrentStatus:    models.StatusOffline,
		DockerState:      models.DockerExited,
		DockerExitCode:   &code,
		DockerExitReason: models.ExitKilled,
		ServerSummary:    "Emby is offline.",
	}}
	fact, ok := factByID(engine.Evaluate(snapshot), "app_emby_offline")
	if !ok {
		t.Fatal("no fact for the offline app")
	}
	if !strings.Contains(strings.Join(fact.Evidence, " "), "exit 137") {
		t.Fatalf("evidence %#v does not carry the exit detail", fact.Evidence)
	}
}
