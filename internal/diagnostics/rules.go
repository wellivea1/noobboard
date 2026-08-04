package diagnostics

import (
	"fmt"
	"strings"
	"time"

	"github.com/wellivea1/noobboard/internal/models"
)

type RuleEngine struct{}

type Result struct {
	OverallStatus models.CurrentStatus
	ServerSummary string
	AdminSummary  string
	Facts         []models.IncidentFact
	Incidents     []models.Incident
	Apps          []models.AppStatus
}

func NewRuleEngine() RuleEngine {
	return RuleEngine{}
}

func (RuleEngine) Evaluate(snapshot models.Snapshot) Result {
	now := snapshot.GeneratedAt
	if now.IsZero() {
		now = time.Now().UTC()
	}
	apps := normalizeApps(snapshot.Apps, now)
	facts := infrastructureFacts(snapshot.Infrastructure, now)
	facts = append(facts, appFacts(snapshot.Infrastructure, apps, now)...)
	incidents := incidentsFromFacts(facts, now)
	overall := overallStatus(facts, apps)
	return Result{
		OverallStatus: overall,
		ServerSummary: serverSummary(overall, facts),
		AdminSummary:  adminSummary(overall, facts),
		Facts:         facts,
		Incidents:     incidents,
		Apps:          apps,
	}
}

func normalizeApps(apps []models.AppStatus, now time.Time) []models.AppStatus {
	out := make([]models.AppStatus, 0, len(apps))
	for _, app := range apps {
		if app.CurrentStatus == "" || app.CurrentStatus == models.StatusUnknown {
			app.CurrentStatus = normalizedAppStatus(app)
		}
		if app.Severity == "" {
			app.Severity = severityForStatus(app.CurrentStatus)
		}
		if app.CurrentStatus == models.StatusOnline && app.LastSeenOnline == nil {
			t := now
			app.LastSeenOnline = &t
		}
		if (app.CurrentStatus == models.StatusOffline || app.CurrentStatus == models.StatusDegraded) && app.LastSeenOffline == nil {
			t := now
			app.LastSeenOffline = &t
		}
		if app.ServerSummary == "" {
			app.ServerSummary = fmt.Sprintf("%s is %s.", app.DisplayName, app.CurrentStatus)
		}
		if app.AdminSummary == "" {
			app.AdminSummary = fmt.Sprintf("%s container=%s docker=%s health=%s endpoint=%s", app.DisplayName, app.ContainerName, app.DockerState, app.DockerHealth, app.EndpointStatus)
		}
		out = append(out, app)
	}
	return out
}

func normalizedAppStatus(app models.AppStatus) models.CurrentStatus {
	if !app.VisibleToGeneralUsers && app.CurrentStatus == models.StatusHidden {
		return models.StatusHidden
	}
	if app.DockerState == models.DockerExited {
		return models.StatusOffline
	}
	if app.DockerHealth == models.HealthUnhealthy {
		return models.StatusDegraded
	}
	if app.EndpointStatus == models.EndpointFailed {
		if app.DockerState == models.DockerRunning {
			return models.StatusDegraded
		}
		return models.StatusOffline
	}
	if app.DockerState == models.DockerRunning && (app.DockerHealth == models.HealthHealthy || app.DockerHealth == models.HealthNone) {
		return models.StatusOnline
	}
	return models.StatusUnknown
}

func infrastructureFacts(infra models.InfrastructureStatus, now time.Time) []models.IncidentFact {
	var facts []models.IncidentFact
	internetProbeData := probeHasData(infra.SourceHealth.Probes, "internet")
	dnsProbeData := probeHasData(infra.SourceHealth.Probes, "dns")
	routerProbeData := probeHasData(infra.SourceHealth.Probes, "router")
	unifiData := sourceHasData(infra.SourceHealth.UniFi)
	unraidData := sourceHasData(infra.SourceHealth.Unraid)
	dockerData := sourceHasData(infra.SourceHealth.Docker)
	add := func(id string, typ models.IncidentType, severity models.Severity, summary string, evidence []string, visible bool) {
		facts = append(facts, models.IncidentFact{
			ID:             id,
			Type:           typ,
			Severity:       severity,
			Summary:        summary,
			Evidence:       evidence,
			CreatedAt:      now,
			VisibleToUsers: visible,
		})
	}

	if routerProbeData && !infra.RouterReachable {
		add("router_unreachable", models.IncidentInternetDown, models.SeverityHigh, "Router is unreachable from the dashboard host.", []string{"router probe failed"}, true)
	}
	if internetProbeData && (!routerProbeData || infra.RouterReachable) && !infra.InternetReachable {
		add("internet_down", models.IncidentInternetDown, models.SeverityHigh, "Router is reachable but external HTTPS checks failed.", []string{"router reachable", "external HTTPS probe failed"}, true)
	}
	if dnsProbeData && !infra.DNSOK {
		add("dns_failure", models.IncidentDNSIssue, models.SeverityMedium, "DNS resolution failed.", []string{"DNS probe failed"}, true)
	}
	if unifiData && !infra.UniFiWANUp {
		add("unifi_wan_down", models.IncidentUnifiIssue, models.SeverityHigh, "UniFi reports the WAN is down.", []string{"UniFi WAN status down"}, true)
	}
	if unifiData && infra.UniFiOfflineDeviceCount > 0 {
		evidence := append([]string(nil), infra.UniFiWarnings...)
		if len(evidence) == 0 {
			evidence = []string{fmt.Sprintf("%d UniFi device(s) offline", infra.UniFiOfflineDeviceCount)}
		}
		add("unifi_devices_offline", models.IncidentUnifiIssue, models.SeverityMedium, fmt.Sprintf("UniFi reports %d network device(s) offline.", infra.UniFiOfflineDeviceCount), evidence, true)
	}
	if unifiData && infra.UniFiFirmwareUpdates > 0 {
		add("unifi_firmware_updates", models.IncidentUnifiIssue, models.SeverityLow, fmt.Sprintf("%d UniFi device firmware update(s) are available.", infra.UniFiFirmwareUpdates), []string{"UniFi firmware update available"}, false)
	}
	if unraidData && !infra.NASReachable {
		add("nas_unreachable", models.IncidentNASUnreachable, models.SeverityHigh, "NAS is unreachable from the dashboard host.", []string{"NAS network probe failed"}, true)
		return facts
	}
	if unraidData && infra.NASReachable && !infra.UnraidAPIReachable {
		add("unraid_api_unavailable", models.IncidentUnraidAPIUnavailable, models.SeverityMedium, "NAS responds but the Unraid API is unavailable.", []string{"NAS reachable", "Unraid API probe failed"}, false)
	}
	if unraidData && infra.UnraidAPIReachable && infra.UnraidArrayState == "stopped" {
		add("array_stopped", models.IncidentArrayStopped, models.SeverityHigh, "Unraid array is stopped.", []string{"Unraid API reports array stopped"}, true)
	}
	if unraidData && infra.UnraidAPIReachable && !infra.UnraidArrayHealthy {
		add("array_degraded", models.IncidentStorageWarning, models.SeverityHigh, "Unraid array is degraded or warning.", []string{"Unraid API reports array health warning"}, true)
	}
	if unraidData && infra.UnraidAPIReachable && (infra.UnraidAlertCount > 0 || infra.UnraidWarningCount > 0) {
		severity := models.SeverityMedium
		if infra.UnraidAlertCount > 0 {
			severity = models.SeverityHigh
		}
		add("unraid_notifications", models.IncidentStorageWarning, severity, "Unraid has unread alerts or warnings.", []string{fmt.Sprintf("%d alert(s), %d warning(s), %d total unread notification(s)", infra.UnraidAlertCount, infra.UnraidWarningCount, infra.UnraidNotificationCount)}, false)
	}
	if unraidData && dockerData && infra.NASReachable && infra.UnraidAPIReachable && !infra.DockerServiceAvailable {
		add("docker_service_down", models.IncidentDockerServiceDown, models.SeverityHigh, "Docker service is unavailable on the NAS.", []string{"Unraid reachable", "Docker service unavailable"}, true)
	}
	if unifiData && infra.NASLinkSpeedMbps > 0 && infra.ExpectedNASLinkMbps > 0 && infra.NASLinkSpeedMbps < infra.ExpectedNASLinkMbps {
		add("nas_link_speed_degraded", models.IncidentUnifiIssue, models.SeverityMedium, "NAS switch port link speed is lower than expected.", []string{fmt.Sprintf("NAS link %d Mbps expected %d Mbps", infra.NASLinkSpeedMbps, infra.ExpectedNASLinkMbps)}, false)
	}
	// "The internet is slow" was undetectable: probes returned booleans, so
	// everything read green at 400ms and 5% loss. These two rules compare a link
	// against its own recent history rather than a fixed threshold, because a
	// 200ms baseline is normal on some connections and terrible on others.
	for _, probe := range infra.ProbeLatencies {
		if probe.SampleCount >= probeMinSamplesForRules && probe.FailureRate >= probeFlakyFailureRate && probe.OK {
			add("probe_flaky_"+probe.Subject, models.IncidentDNSIssue, models.SeverityMedium,
				fmt.Sprintf("%s checks are failing intermittently.", probeDisplayName(probe.Subject)),
				[]string{fmt.Sprintf("%.0f%% of recent %s checks failed, but it is reachable right now", probe.FailureRate*100, probe.Subject)}, false)
			continue
		}
		// A baseline of zero means "not enough history yet", so the rule stays
		// silent rather than comparing against nothing. The floor stops a link
		// whose normal latency is 1ms reporting a fault at 5ms.
		if !probe.OK || probe.BaselineMS < probeBaselineFloorMS || probe.SampleCount < probeMinSamplesForRules {
			continue
		}
		if probe.LatencyMS >= probe.BaselineMS*probeSlowMultiplier {
			add("probe_slow_"+probe.Subject, models.IncidentInternetDown, models.SeverityMedium,
				fmt.Sprintf("%s is responding much slower than usual.", probeDisplayName(probe.Subject)),
				[]string{fmt.Sprintf("%dms now against a usual %dms over the last %d checks", probe.LatencyMS, probe.BaselineMS, probe.SampleCount)}, true)
		}
	}
	if unraidData {
		for i, warning := range infra.StorageWarnings {
			add(fmt.Sprintf("storage_warning_%d", i+1), models.IncidentStorageWarning, models.SeverityHigh, "Storage warning requires admin attention.", []string{warning}, false)
		}
	}
	// Capacity and memory were collected on every poll and read by nothing. A
	// full array is one of the most common causes of containers failing to
	// start on a home server, and it is silent until something breaks.
	if unraidData && infra.UnraidAPIReachable && infra.ArrayCapacityTotalBytes > 0 && infra.ArrayCapacityUsedPct >= arrayNearlyFullPct {
		severity := models.SeverityMedium
		summary := "Storage array is nearly full."
		if infra.ArrayCapacityUsedPct >= arrayCriticallyFullPct {
			severity = models.SeverityHigh
			summary = "Storage array is almost out of space."
		}
		add("array_capacity_high", models.IncidentStorageWarning, severity, summary,
			[]string{fmt.Sprintf("array %.1f%% used, %s free", infra.ArrayCapacityUsedPct, humanBytes(infra.ArrayCapacityFreeBytes))}, false)
	}
	// Unraid counts cache as used, so a high number is not automatically a
	// problem. The threshold is set where it stops being explainable by cache
	// and the wording stays descriptive rather than diagnostic.
	if unraidData && infra.UnraidAPIReachable && infra.UnraidMemoryTotalBytes > 0 && infra.UnraidMemoryUsedPct >= memoryPressurePct {
		add("memory_pressure", models.IncidentMemoryPressure, models.SeverityMedium, "Server memory use is high.",
			[]string{fmt.Sprintf("%.1f%% of %s in use", infra.UnraidMemoryUsedPct, humanBytes(infra.UnraidMemoryTotalBytes))}, false)
	}
	return facts
}

// Slow is relative. 4x the link's own median is well outside normal jitter
// without firing on it; the floor keeps a 2ms LAN hop from reporting a fault at
// 8ms; and the sample minimum stops a freshly restarted NoobBoard judging a link
// it has barely measured.
const (
	probeSlowMultiplier     = 4
	probeBaselineFloorMS    = 8
	probeFlakyFailureRate   = 0.2
	probeMinSamplesForRules = 20
)

func probeDisplayName(subject string) string {
	switch subject {
	case "internet":
		return "The internet connection"
	case "dns":
		return "DNS"
	case "router":
		return "The router"
	case "nas":
		return "The server"
	default:
		return subject
	}
}

// RestartLoopWindow is how far back the server counts status changes when
// deciding whether an app is flapping. Exported because the server does the
// counting — the rule engine stays a pure function of the snapshot.
const RestartLoopWindow = 30 * time.Minute

const (
	arrayNearlyFullPct     = 90
	arrayCriticallyFullPct = 96
	memoryPressurePct      = 92
	// A container that changes state this many times inside the window is
	// flapping rather than simply down.
	restartLoopChanges = 4
)

func humanBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit && exp < 4; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(bytes)/float64(div), "KMGTP"[exp])
}

func appFacts(infra models.InfrastructureStatus, apps []models.AppStatus, now time.Time) []models.IncidentFact {
	if !sourceHasData(infra.SourceHealth.Docker) || !infra.NASReachable || !infra.DockerServiceAvailable {
		return nil
	}
	var facts []models.IncidentFact
	for _, app := range apps {
		if app.CurrentStatus == models.StatusOnline || app.CurrentStatus == models.StatusHidden {
			continue
		}
		typ := models.IncidentAppDown
		severity := models.SeverityMedium
		if app.CurrentStatus == models.StatusDegraded {
			typ = models.IncidentAppDegraded
			severity = models.SeverityLow
		}
		// A container that keeps changing state is a different problem from one
		// that is simply down, and it needs the opposite response: restarting a
		// crash loop restarts the loop. Reported as its own incident type so the
		// recommendation can differ.
		looping := app.RecentStatusChanges >= restartLoopChanges
		if looping {
			typ = models.IncidentAppRestartLoop
			severity = models.SeverityHigh
		}
		evidence := []string{app.AdminSummary}
		if looping {
			evidence = append(evidence, fmt.Sprintf("%d status changes in the last %s; restarting again will not fix a crash loop", app.RecentStatusChanges, RestartLoopWindow))
		}
		if app.DockerState == models.DockerExited {
			evidence = append(evidence, "container exited")
		}
		if detail := models.ExitDetail(app.DockerExitCode, app.DockerExitReason); detail != "" {
			evidence = append(evidence, detail)
		}
		if app.DockerHealth == models.HealthUnhealthy {
			evidence = append(evidence, "container unhealthy")
		}
		if app.EndpointStatus == models.EndpointFailed && app.DockerState != models.DockerExited && hasEndpointProbe(app) {
			evidence = append(evidence, endpointEvidence(app))
		}
		summary := app.ServerSummary
		if looping {
			summary = fmt.Sprintf("%s keeps stopping and starting.", app.DisplayName)
		}
		facts = append(facts, models.IncidentFact{
			ID:               "app_" + app.AppID + "_" + string(app.CurrentStatus),
			Type:             typ,
			Severity:         severity,
			Summary:          summary,
			Evidence:         evidence,
			AffectedServices: []string{app.AppID},
			CreatedAt:        now,
			VisibleToUsers:   app.VisibleToGeneralUsers,
		})
	}
	return facts
}

func sourceHasData(value string) bool {
	health := strings.ToLower(strings.TrimSpace(value))
	if health == "" {
		return false
	}
	missingSignals := []string{
		"not configured",
		"credentials are not configured",
		"collector is not configured",
		"no collector detail",
	}
	for _, signal := range missingSignals {
		if strings.Contains(health, signal) {
			return false
		}
	}
	return true
}

func probeHasData(source, name string) bool {
	health := strings.ToLower(strings.TrimSpace(source))
	if !sourceHasData(health) {
		return false
	}
	return !strings.Contains(health, strings.ToLower(name)+" skipped")
}

func hasEndpointProbe(app models.AppStatus) bool {
	return isEndpointProbe(app.ProbeType) || isEndpointProbe(app.CurrentProbeResult.Type)
}

func isEndpointProbe(probe models.ProbeType) bool {
	return probe == models.ProbeHTTP || probe == models.ProbeTCP || probe == models.ProbeCustom
}

func endpointEvidence(app models.AppStatus) string {
	probeType := app.CurrentProbeResult.Type
	if probeType == "" {
		probeType = app.ProbeType
	}
	label := strings.ToUpper(string(probeType))
	if label == "" {
		label = "ENDPOINT"
	}
	target := strings.TrimSpace(app.CurrentProbeResult.Target)
	message := strings.TrimSpace(app.CurrentProbeResult.Message)
	switch {
	case target != "" && message != "":
		return fmt.Sprintf("%s probe to %s failed: %s", label, target, message)
	case target != "":
		return fmt.Sprintf("%s probe to %s failed", label, target)
	case message != "":
		return fmt.Sprintf("%s probe failed: %s", label, message)
	default:
		return fmt.Sprintf("%s probe failed", label)
	}
}

func incidentsFromFacts(facts []models.IncidentFact, now time.Time) []models.Incident {
	incidents := make([]models.Incident, 0, len(facts))
	for i, fact := range facts {
		incidents = append(incidents, models.Incident{
			ID:               fmt.Sprintf("%s-%03d", now.Format("2006-01-02"), i+1),
			Type:             fact.Type,
			Severity:         fact.Severity,
			Status:           models.StatusOffline,
			Summary:          fact.Summary,
			AdminSummary:     fact.Summary,
			AffectedServices: fact.AffectedServices,
			Evidence:         fact.Evidence,
			StartedAt:        now,
			UpdatedAt:        now,
		})
	}
	return incidents
}

func overallStatus(facts []models.IncidentFact, apps []models.AppStatus) models.CurrentStatus {
	for _, fact := range facts {
		if fact.Severity == models.SeverityCritical || fact.Severity == models.SeverityHigh {
			return models.StatusOffline
		}
	}
	for _, app := range apps {
		if app.CurrentStatus == models.StatusOffline {
			return models.StatusOffline
		}
	}
	for _, fact := range facts {
		if fact.Severity == models.SeverityMedium || fact.Severity == models.SeverityLow {
			return models.StatusDegraded
		}
	}
	for _, app := range apps {
		if app.CurrentStatus == models.StatusDegraded || app.CurrentStatus == models.StatusUnknown {
			return models.StatusDegraded
		}
	}
	return models.StatusOnline
}

func severityForStatus(status models.CurrentStatus) models.Severity {
	switch status {
	case models.StatusOnline:
		return models.SeverityNone
	case models.StatusDegraded, models.StatusUnknown:
		return models.SeverityLow
	case models.StatusOffline:
		return models.SeverityMedium
	default:
		return models.SeverityNone
	}
}

func serverSummary(status models.CurrentStatus, facts []models.IncidentFact) string {
	if len(facts) == 0 && status == models.StatusOnline {
		return "Server services look online."
	}
	for _, fact := range facts {
		if fact.VisibleToUsers {
			return fact.Summary
		}
	}
	return "A service needs admin attention."
}

func adminSummary(status models.CurrentStatus, facts []models.IncidentFact) string {
	if len(facts) == 0 && status == models.StatusOnline {
		return "All monitored infrastructure and apps are online."
	}
	return fmt.Sprintf("%d diagnostic fact(s) produced; overall status %s.", len(facts), status)
}
