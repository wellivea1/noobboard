package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/wellivea1/noobboard/internal/db"
	"github.com/wellivea1/noobboard/internal/diagnostics"
	"github.com/wellivea1/noobboard/internal/models"
)

// Recorded-data management. Status history and latency series feed the
// diagnosis rules, so an admin needs to see what has been recorded and be able
// to clear it when the record has stopped describing reality.

func (a *App) adminDataSummary(w http.ResponseWriter, r *http.Request) {
	summary := adminDataSummaryView{
		StatusHistoryAvailable: a.deps.History != nil,
		LatencyAvailable:       a.deps.Metrics != nil,
		RestartLoopWindow:      diagnostics.RestartLoopWindow.String(),
	}
	if a.deps.History != nil {
		events, err := a.deps.History.Query(db.HistoryFilter{})
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		summary.StatusEventCount = len(events)
		recentSince := time.Now().UTC().Add(-diagnostics.RestartLoopWindow)
		bySubject := map[string]*adminDataSubjectView{}
		order := make([]string, 0, len(events))
		for _, event := range events {
			key := string(event.SubjectType) + "\x00" + event.SubjectID
			entry, ok := bySubject[key]
			if !ok {
				entry = &adminDataSubjectView{
					SubjectType: string(event.SubjectType),
					SubjectID:   event.SubjectID,
					Label:       firstNonEmpty(event.DisplayName, event.SubjectID),
				}
				bySubject[key] = entry
				order = append(order, key)
			}
			entry.EventCount++
			// The recent count is the number the restart-loop rule actually reads,
			// so show that rather than making the admin infer it from the total.
			if !event.At.Before(recentSince) {
				entry.RecentEventCount++
			}
			if entry.LatestAt.IsZero() || event.At.After(entry.LatestAt) {
				entry.LatestAt = event.At
			}
		}
		summary.Subjects = make([]adminDataSubjectView, 0, len(order))
		for _, key := range order {
			summary.Subjects = append(summary.Subjects, *bySubject[key])
		}
		sort.Slice(summary.Subjects, func(i, j int) bool {
			if summary.Subjects[i].RecentEventCount != summary.Subjects[j].RecentEventCount {
				return summary.Subjects[i].RecentEventCount > summary.Subjects[j].RecentEventCount
			}
			return summary.Subjects[i].EventCount > summary.Subjects[j].EventCount
		})
	}
	if a.deps.Metrics != nil {
		buckets, err := a.deps.Metrics.QueryLatency(db.MetricFilter{})
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		summary.LatencyBucketCount = len(buckets)
		seen := map[string]int{}
		for _, bucket := range buckets {
			seen[bucket.Subject]++
		}
		summary.LatencySubjects = make([]adminDataSubjectView, 0, len(seen))
		for subject, count := range seen {
			summary.LatencySubjects = append(summary.LatencySubjects, adminDataSubjectView{
				SubjectType: "latency",
				SubjectID:   subject,
				Label:       subject,
				EventCount:  count,
			})
		}
		sort.Slice(summary.LatencySubjects, func(i, j int) bool {
			return summary.LatencySubjects[i].SubjectID < summary.LatencySubjects[j].SubjectID
		})
	}
	writeJSON(w, http.StatusOK, summary)
}

type adminDataSummaryView struct {
	StatusHistoryAvailable bool                   `json:"status_history_available"`
	StatusEventCount       int                    `json:"status_event_count"`
	Subjects               []adminDataSubjectView `json:"subjects"`
	LatencyAvailable       bool                   `json:"latency_available"`
	LatencyBucketCount     int                    `json:"latency_bucket_count"`
	LatencySubjects        []adminDataSubjectView `json:"latency_subjects"`
	RestartLoopWindow      string                 `json:"restart_loop_window"`
}

type adminDataSubjectView struct {
	SubjectType      string    `json:"subject_type"`
	SubjectID        string    `json:"subject_id"`
	Label            string    `json:"label"`
	EventCount       int       `json:"event_count"`
	RecentEventCount int       `json:"recent_event_count,omitempty"`
	LatestAt         time.Time `json:"latest_at,omitempty"`
}

type adminDataClearRequest struct {
	// Scope is what to clear: "status_history" or "latency". SubjectType and
	// SubjectID narrow it; both empty clears the whole store.
	Scope       string `json:"scope"`
	SubjectType string `json:"subject_type"`
	SubjectID   string `json:"subject_id"`
}

// adminDataClear deletes recorded history. Every clear is audited before it is
// reported, because losing the record of an outage is itself worth a record.
func (a *App) adminDataClear(w http.ResponseWriter, r *http.Request) {
	if err := requireCSRF(r); err != nil {
		writeError(w, http.StatusForbidden, err)
		return
	}
	var body adminDataClearRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	scope := strings.ToLower(strings.TrimSpace(body.Scope))
	subjectID := strings.TrimSpace(body.SubjectID)
	subjectType := strings.ToLower(strings.TrimSpace(body.SubjectType))
	switch scope {
	case "status_history":
		if a.deps.History == nil {
			writeError(w, http.StatusServiceUnavailable, errors.New("status history is not configured"))
			return
		}
		// A subject id without a type would clear an app and an infrastructure
		// probe that happen to share a name. Refuse rather than guess.
		if subjectID != "" && subjectType == "" {
			writeError(w, http.StatusBadRequest, errors.New("subject_type is required when subject_id is set"))
			return
		}
		removed, err := a.deps.History.Clear(db.HistoryFilter{
			SubjectType: models.StatusSubjectType(subjectType),
			SubjectID:   subjectID,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		// The recorder's in-memory "last seen status" is deliberately left alone.
		// It only emits on change, so history stays clear until something actually
		// happens; resetting it would write a fresh baseline event on the next poll
		// and put a row back immediately.
		a.deps.Audit.Record(mustUser(r).ID, "history.cleared", map[string]interface{}{
			"scope": scope, "subject_type": subjectType, "subject_id": subjectID, "removed": removed,
		})
		writeJSON(w, http.StatusOK, map[string]interface{}{"scope": scope, "removed": removed})
	case "latency":
		if a.deps.Metrics == nil {
			writeError(w, http.StatusServiceUnavailable, errors.New("latency history is not configured"))
			return
		}
		removed, err := a.deps.Metrics.ClearLatency(subjectID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		a.deps.Audit.Record(mustUser(r).ID, "history.cleared", map[string]interface{}{
			"scope": scope, "subject_id": subjectID, "removed": removed,
		})
		writeJSON(w, http.StatusOK, map[string]interface{}{"scope": scope, "removed": removed})
	default:
		writeError(w, http.StatusBadRequest, fmt.Errorf("unknown scope %q", body.Scope))
	}
}
