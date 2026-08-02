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

func probeSnapshot(readings ...models.ProbeLatency) models.Snapshot {
	return models.Snapshot{
		GeneratedAt: time.Now().UTC(),
		Infrastructure: models.InfrastructureStatus{
			NASReachable:       true,
			UnraidAPIReachable: true,
			UnraidArrayState:   "started",
			UnraidArrayHealthy: true,
			ProbeLatencies:     readings,
			SourceHealth:       models.SourceHealth{Unraid: "ok", Probes: "ok"},
		},
	}
}

func hasFact(result Result, id string) bool {
	for _, fact := range result.Facts {
		if fact.ID == id {
			return true
		}
	}
	return false
}

func TestSlowRuleComparesAgainstTheLinksOwnBaseline(t *testing.T) {
	// The point of the rule: 200ms is normal on some connections and terrible on
	// others, so the threshold has to be the link's own history.
	engine := NewRuleEngine()

	fast := models.ProbeLatency{Subject: "internet", OK: true, LatencyMS: 210, BaselineMS: 200, SampleCount: 60}
	if hasFact(engine.Evaluate(probeSnapshot(fast)), "probe_slow_internet") {
		t.Fatal("fired at 210ms on a link whose usual latency is 200ms")
	}

	slow := models.ProbeLatency{Subject: "internet", OK: true, LatencyMS: 900, BaselineMS: 200, SampleCount: 60}
	if !hasFact(engine.Evaluate(probeSnapshot(slow)), "probe_slow_internet") {
		t.Fatal("did not fire at 900ms against a 200ms baseline")
	}

	// Same absolute latency, different link: 210ms is a fault on a 2ms LAN hop
	// only if the baseline says so, and the floor keeps tiny baselines quiet.
	lan := models.ProbeLatency{Subject: "router", OK: true, LatencyMS: 210, BaselineMS: 2, SampleCount: 60}
	if hasFact(engine.Evaluate(probeSnapshot(lan)), "probe_slow_router") {
		t.Fatal("fired against a sub-floor baseline where the ratio is meaningless")
	}
}

func TestSlowRuleStaysSilentWithoutEnoughHistory(t *testing.T) {
	// A freshly restarted NoobBoard has no window. It must not judge a link it
	// has barely measured, and a zero baseline means "unknown", not "0ms".
	engine := NewRuleEngine()
	cold := models.ProbeLatency{Subject: "internet", OK: true, LatencyMS: 5000, BaselineMS: 0, SampleCount: 3}
	if hasFact(engine.Evaluate(probeSnapshot(cold)), "probe_slow_internet") {
		t.Fatal("fired with no baseline; a missing baseline must suppress the rule")
	}
}

func TestFlakyRuleFiresWhileTheProbeIsCurrentlyUp(t *testing.T) {
	// Intermittent failure is invisible to a snapshot: the probe is reachable
	// right now. Only the failure rate across the window can see it.
	engine := NewRuleEngine()
	flaky := models.ProbeLatency{Subject: "internet", OK: true, LatencyMS: 30, BaselineMS: 30, SampleCount: 60, FailureRate: 0.35}
	if !hasFact(engine.Evaluate(probeSnapshot(flaky)), "probe_flaky_internet") {
		t.Fatal("did not fire for a probe failing 35% of recent checks")
	}

	steady := models.ProbeLatency{Subject: "internet", OK: true, LatencyMS: 30, BaselineMS: 30, SampleCount: 60, FailureRate: 0.01}
	if hasFact(engine.Evaluate(probeSnapshot(steady)), "probe_flaky_internet") {
		t.Fatal("fired for a probe that is essentially reliable")
	}
}
