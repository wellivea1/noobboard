package server

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/wellivea1/noobboard/internal/adapters/unifi"
	"github.com/wellivea1/noobboard/internal/db"
	"github.com/wellivea1/noobboard/internal/models"
)

// Probe latency: rolling per-subject samples, the baseline the diagnosis rules
// compare against, and the five-minute buckets persisted to the metric store.

// probeTracker owns everything the probe pipeline remembers between polls: the
// rolling window each baseline is computed from, and the five-minute bucket
// being accumulated for the metric store. Both are guarded by one lock because
// they advance together on every poll — splitting them would allow a bucket to
// roll over between the two halves of a single reading.
type probeTracker struct {
	mu sync.Mutex

	// samples is the rolling window per subject, used for the baseline the
	// diagnosis rules compare against. In memory only: it resets on restart,
	// which suppresses the rule rather than firing it.
	samples map[string][]probeSample

	// The bucket currently being filled, flushed to the metric store when a
	// reading lands in a later one.
	bucketAt       time.Time
	bucketLatency  map[string][]int64
	bucketFailures map[string]int

	lastPrune time.Time
}

func newProbeTracker() *probeTracker {
	return &probeTracker{
		samples:        map[string][]probeSample{},
		bucketLatency:  map[string][]int64{},
		bucketFailures: map[string]int{},
	}
}

type probeSample struct {
	latencyMS int64
	ok        bool
}

// annotateProbeBaselines fills each probe reading with a baseline drawn from the
// server's recent window, so the rules can ask "is this slow *for this link*"
// rather than comparing against a fixed threshold. A 200ms baseline is normal on
// some connections and terrible on others; only the link's own history says
// which.
//
// The window is in memory and resets when NoobBoard restarts. That is a real
// limitation — it means no baseline for the first few minutes after a restart —
// but it is the honest version of what is available without a metrics store, and
// a missing baseline suppresses the rule rather than firing it.
func (a *App) annotateProbeBaselines(infra *models.InfrastructureStatus) {
	if len(infra.ProbeLatencies) == 0 {
		return
	}
	a.probes.mu.Lock()
	defer a.probes.mu.Unlock()
	if a.probes.samples == nil {
		a.probes.samples = map[string][]probeSample{}
	}
	for i := range infra.ProbeLatencies {
		reading := &infra.ProbeLatencies[i]
		window := append(a.probes.samples[reading.Subject], probeSample{latencyMS: reading.LatencyMS, ok: reading.OK})
		if len(window) > probeWindowSamples {
			window = window[len(window)-probeWindowSamples:]
		}
		a.probes.samples[reading.Subject] = window
		reading.SampleCount = len(window)
		reading.FailureRate = probeFailureRate(window)
		reading.BaselineMS = probeBaselineMS(window)
	}
}

// probeBaselineMS is the median latency of successful samples. Median, not mean:
// one 3-second timeout would drag a mean far enough to hide the next real spike.
// Failed samples are excluded because a timeout measures the timeout, not the
// link.
func probeBaselineMS(window []probeSample) int64 {
	values := make([]int64, 0, len(window))
	for _, sample := range window {
		if sample.ok {
			values = append(values, sample.latencyMS)
		}
	}
	if len(values) < probeBaselineMinSamples {
		return 0
	}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	middle := len(values) / 2
	if len(values)%2 == 1 {
		return values[middle]
	}
	return (values[middle-1] + values[middle]) / 2
}

func probeFailureRate(window []probeSample) float64 {
	if len(window) == 0 {
		return 0
	}
	failed := 0
	for _, sample := range window {
		if !sample.ok {
			failed++
		}
	}
	return float64(failed) / float64(len(window))
}

// --- UniFi device control ----------------------------------------------------

// unifiRestartableDevices lists the devices an admin may restart. The safety
// rule lives in the adapter so the list the UI offers and the check the mutation
// runs cannot drift apart.
func (a *App) unifiRestartableDevices(w http.ResponseWriter, r *http.Request) {
	devices, err := a.deps.Collectors.UniFi.RestartableDevices(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	if devices == nil {
		devices = []unifi.RestartableDevice{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"devices": devices,
		"note":    "Only offline, non-gateway devices can be restarted. Restarting a device that is still passing traffic could drop the NAS or this dashboard.",
	})
}

// unifiRestartDevice is the first mutating network action in NoobBoard. It adds
// a write surface, not a trust level: same CSRF + admin gate, same rate limit,
// same audit-then-execute-then-verify shape as the Docker and array paths.
func (a *App) unifiRestartDevice(w http.ResponseWriter, r *http.Request) {
	if err := requireCSRF(r); err != nil {
		writeError(w, http.StatusForbidden, err)
		return
	}
	deviceID := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/admin/unifi/devices/"), "/restart")
	deviceID = strings.Trim(deviceID, "/")
	if deviceID == "" {
		writeError(w, http.StatusBadRequest, errors.New("unifi device id is required"))
		return
	}
	user := mustUser(r)
	details := map[string]interface{}{"device_id": deviceID, "action": "restart"}

	// Rate limited on the same budget as app repairs. A restart takes a device
	// off the network for a minute or two; retrying it in a loop is never the
	// right answer, and the cooldown is what stops a stuck caller doing that.
	limit := a.reserveAgentRepair("unifi:"+deviceID, time.Now().UTC())
	if !limit.Allowed {
		details["reason"] = limit.Reason
		details["retry_after_seconds"] = limit.RetryAfterSeconds
		a.deps.Audit.Record(user.ID, "unifi.device.rate_limited", auditDetailsCopy(details))
		writeError(w, http.StatusConflict, errors.New(limit.Message))
		return
	}

	result, err := a.deps.Collectors.UniFi.RestartDevice(r.Context(), deviceID)
	if err != nil {
		details["error"] = err.Error()
		if errors.Is(err, unifi.ErrDeviceNotRestartable) {
			// Refused by the safety rule, not a transport failure. Distinct audit
			// action and a 409 so the caller can tell "not allowed" from "broken".
			a.deps.Audit.Record(user.ID, "unifi.device.refused", auditDetailsCopy(details))
			writeError(w, http.StatusConflict, errors.New("that device is not eligible for restart: only offline, non-gateway devices can be restarted"))
			return
		}
		a.deps.Audit.Record(user.ID, "unifi.device.action_failed", auditDetailsCopy(details))
		writeError(w, http.StatusBadGateway, err)
		return
	}
	details["device_name"] = result.DeviceName
	a.deps.Audit.Record(user.ID, "unifi.device.action", auditDetailsCopy(details))
	a.invalidateSnapshot()

	outcome := a.verifyUniFiRestartOutcome(r.Context(), result)
	verifyDetails := auditDetailsCopy(details)
	verifyDetails["verified"] = outcome.Verified
	verifyDetails["recovered"] = outcome.Recovered
	if outcome.Verified {
		a.deps.Audit.Record(user.ID, "unifi.device.verified", verifyDetails)
	} else {
		a.deps.Audit.Record(user.ID, "unifi.device.verify_failed", verifyDetails)
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"result":  result,
		"outcome": outcome,
	})
}

// verifyUniFiRestartOutcome re-polls UniFi after a restart.
//
// Two failure modes this must not confuse with success:
//
//   - UniFi itself unreachable. A restart can drop the API path, so an error
//     here means "unknown", never "recovered".
//   - The device still offline. A restart is not a repair: a device that was
//     offline because it lost power comes back offline. Reporting that honestly
//     is the point of verifying at all.
func (a *App) verifyUniFiRestartOutcome(ctx context.Context, result unifi.DeviceControlResult) llmAgentRepairOutcomeView {
	outcome := llmAgentRepairOutcomeView{
		Action:       "unifi_restart_device",
		TargetID:     result.DeviceID,
		TargetLabel:  result.DeviceName,
		BeforeStatus: models.StatusOffline,
		AfterStatus:  models.StatusUnknown,
		CheckedAt:    time.Now().UTC(),
		Message:      "Restart was sent, but NoobBoard could not confirm the device came back yet.",
	}
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
				outcome.Message = "Restart was sent, but verification was cancelled before NoobBoard could re-check the device."
				return outcome
			case <-timer.C:
			}
		}
		online, err := a.deps.Collectors.UniFi.DeviceOnline(ctx, result.DeviceID)
		outcome.CheckedAt = time.Now().UTC()
		if err != nil {
			// Unreachable UniFi is unknown, not failure and not success.
			outcome.Message = "Restart was sent, but NoobBoard could not re-check the device: " + err.Error()
			continue
		}
		outcome.Verified = true
		if online {
			outcome.AfterStatus = models.StatusOnline
			outcome.Recovered = true
			outcome.Message = result.DeviceName + " came back online after the restart."
			return outcome
		}
		outcome.AfterStatus = models.StatusOffline
		outcome.Message = result.DeviceName + " is still offline after the restart. A restart does not fix a device that has lost power or its uplink."
	}
	return outcome
}

// --- persisted latency series ------------------------------------------------

// recordLatencyBucket accumulates this poll's probe timings and flushes a
// completed bucket to the metric store.
//
// Flushing on boundary crossing rather than on a timer means a NoobBoard that is
// stopped mid-bucket loses at most one partial bucket, and one that runs for
// months writes a predictable number of rows.
func (a *App) recordLatencyBucket(infra models.InfrastructureStatus) {
	if a.deps.Metrics == nil || len(infra.ProbeLatencies) == 0 {
		return
	}
	now := time.Now().UTC()
	bucketAt := db.BucketStart(now)

	a.probes.mu.Lock()
	if a.probes.bucketLatency == nil {
		a.probes.bucketLatency = map[string][]int64{}
		a.probes.bucketFailures = map[string]int{}
		a.probes.bucketAt = bucketAt
	}
	var completed []models.LatencyBucket
	if !bucketAt.Equal(a.probes.bucketAt) {
		for subject, latencies := range a.probes.bucketLatency {
			completed = append(completed, db.SummariseLatency(subject, a.probes.bucketAt, latencies, a.probes.bucketFailures[subject]))
		}
		for subject := range a.probes.bucketFailures {
			if _, ok := a.probes.bucketLatency[subject]; ok {
				continue
			}
			// A subject that failed every sample in the bucket still deserves a
			// row: an all-failure gap is the most interesting thing on the chart.
			completed = append(completed, db.SummariseLatency(subject, a.probes.bucketAt, nil, a.probes.bucketFailures[subject]))
		}
		a.probes.bucketLatency = map[string][]int64{}
		a.probes.bucketFailures = map[string]int{}
		a.probes.bucketAt = bucketAt
	}
	for _, probe := range infra.ProbeLatencies {
		if probe.OK {
			a.probes.bucketLatency[probe.Subject] = append(a.probes.bucketLatency[probe.Subject], probe.LatencyMS)
			continue
		}
		a.probes.bucketFailures[probe.Subject]++
	}
	shouldPrune := a.probes.lastPrune.IsZero() || now.Sub(a.probes.lastPrune) >= time.Hour
	if shouldPrune {
		a.probes.lastPrune = now
	}
	a.probes.mu.Unlock()

	if len(completed) > 0 {
		_ = a.deps.Metrics.AppendLatency(completed)
	}
	if shouldPrune {
		_ = a.deps.Metrics.PruneLatency(a.configSnapshot().Retention)
	}
}

// seedProbeWindowFromMetrics warms the in-memory baseline window from persisted
// buckets at startup.
//
// Without this a restart left every latency rule blind for ~10 minutes while the
// window refilled — the limitation recorded when the window was added. A bucket
// median is a reasonable stand-in for the samples it summarises, which is all the
// baseline needs.
func (a *App) seedProbeWindowFromMetrics() {
	if a.deps.Metrics == nil {
		return
	}
	buckets, err := a.deps.Metrics.QueryLatency(db.MetricFilter{Since: time.Now().UTC().Add(-probeSeedWindow)})
	if err != nil || len(buckets) == 0 {
		return
	}
	a.probes.mu.Lock()
	defer a.probes.mu.Unlock()
	if a.probes.samples == nil {
		a.probes.samples = map[string][]probeSample{}
	}
	for _, bucket := range buckets {
		if bucket.Samples == 0 {
			continue
		}
		window := a.probes.samples[bucket.Subject]
		if bucket.MedianMS > 0 {
			window = append(window, probeSample{latencyMS: bucket.MedianMS, ok: true})
		}
		if bucket.Failures > 0 {
			window = append(window, probeSample{ok: false})
		}
		if len(window) > probeWindowSamples {
			window = window[len(window)-probeWindowSamples:]
		}
		a.probes.samples[bucket.Subject] = window
	}
}

func (a *App) latencySeries(w http.ResponseWriter, r *http.Request) {
	if a.deps.Metrics == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("latency history is not configured"))
		return
	}
	subject := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("subject")))
	window := parseHistoryWindow(r.URL.Query().Get("window"))
	if window > maxLatencyWindow {
		window = maxLatencyWindow
	}
	buckets, err := a.deps.Metrics.QueryLatency(db.MetricFilter{
		Subject: subject,
		Since:   time.Now().UTC().Add(-window),
		Limit:   maxLatencyBuckets,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if buckets == nil {
		buckets = []models.LatencyBucket{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"subject":        subject,
		"window_seconds": int64(window.Seconds()),
		"bucket_seconds": int64(db.LatencyBucketWindow.Seconds()),
		"buckets":        buckets,
	})
}
