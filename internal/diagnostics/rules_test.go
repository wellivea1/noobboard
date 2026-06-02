package diagnostics

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/wellivea1/server-status/internal/adapters/fixture"
	"github.com/wellivea1/server-status/internal/models"
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
