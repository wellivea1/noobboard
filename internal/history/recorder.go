package history

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/wellivea1/noobboard/internal/models"
)

type Recorder struct {
	mu       sync.Mutex
	previous map[string]subjectState
}

type subjectState struct {
	SubjectType models.StatusSubjectType
	SubjectID   string
	DisplayName string
	Status      models.CurrentStatus
	Note        string
}

func NewRecorder() *Recorder {
	return &Recorder{previous: map[string]subjectState{}}
}

func (r *Recorder) Observe(snapshot models.Snapshot) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.previous = subjectsFromSnapshot(snapshot)
}

func (r *Recorder) Record(snapshot models.Snapshot) []models.StatusEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.previous == nil {
		r.previous = map[string]subjectState{}
	}
	at := snapshot.GeneratedAt
	if at.IsZero() {
		at = time.Now().UTC()
	}
	current := subjectsFromSnapshot(snapshot)
	events := make([]models.StatusEvent, 0)
	for key, state := range current {
		previous, ok := r.previous[key]
		if !ok {
			r.previous[key] = state
			continue
		}
		if previous.Status != state.Status {
			events = append(events, statusEvent(previous, state, at))
		}
		r.previous[key] = state
	}
	for key, previous := range r.previous {
		if previous.SubjectType != models.SubjectApp {
			continue
		}
		if _, ok := current[key]; ok {
			continue
		}
		state := previous
		state.Status = models.StatusUnknown
		state.Note = "App is no longer present in the latest snapshot."
		if previous.Status != models.StatusUnknown {
			events = append(events, statusEvent(previous, state, at))
		}
		delete(r.previous, key)
	}
	return events
}

func subjectsFromSnapshot(snapshot models.Snapshot) map[string]subjectState {
	subjects := map[string]subjectState{}
	for _, app := range snapshot.Apps {
		id := strings.TrimSpace(app.AppID)
		if id == "" {
			continue
		}
		status := app.CurrentStatus
		if status == "" {
			status = models.StatusUnknown
		}
		state := subjectState{
			SubjectType: models.SubjectApp,
			SubjectID:   id,
			DisplayName: firstNonEmpty(app.DisplayName, app.ContainerName, id),
			Status:      status,
			Note:        strings.TrimSpace(app.ServerSummary),
		}
		subjects[subjectKey(state.SubjectType, state.SubjectID)] = state
	}
	for _, state := range infraSubjects(snapshot.Infrastructure) {
		subjects[subjectKey(state.SubjectType, state.SubjectID)] = state
	}
	return subjects
}

func infraSubjects(infra models.InfrastructureStatus) []subjectState {
	return []subjectState{
		{
			SubjectType: models.SubjectInfra,
			SubjectID:   "internet",
			DisplayName: "Internet",
			Status:      reachableStatus(infra.InternetReachable),
			Note:        boolNote(infra.InternetReachable, "Internet is reachable.", "Internet is not reachable."),
		},
		{
			SubjectType: models.SubjectInfra,
			SubjectID:   "dns",
			DisplayName: "DNS",
			Status:      reachableStatus(infra.DNSOK),
			Note:        boolNote(infra.DNSOK, "DNS is resolving.", "DNS is not resolving."),
		},
		{
			SubjectType: models.SubjectInfra,
			SubjectID:   "wan",
			DisplayName: "WAN",
			Status:      reachableStatus(infra.UniFiWANUp),
			Note:        boolNote(infra.UniFiWANUp, "WAN is up.", "WAN is down."),
		},
		{
			SubjectType: models.SubjectInfra,
			SubjectID:   "nas",
			DisplayName: "NAS",
			Status:      reachableStatus(infra.NASReachable),
			Note:        boolNote(infra.NASReachable, "NAS is reachable.", "NAS is not reachable."),
		},
		{
			SubjectType: models.SubjectInfra,
			SubjectID:   "unraid_array",
			DisplayName: "Unraid array",
			Status:      arrayStatus(infra),
			Note:        arrayNote(infra),
		},
	}
}

func statusEvent(previous, current subjectState, at time.Time) models.StatusEvent {
	return models.StatusEvent{
		ID:          eventID(current, at),
		SubjectType: current.SubjectType,
		SubjectID:   current.SubjectID,
		DisplayName: firstNonEmpty(current.DisplayName, previous.DisplayName, current.SubjectID),
		From:        previous.Status,
		To:          current.Status,
		At:          at,
		Note:        firstNonEmpty(current.Note, previous.Note),
	}
}

func eventID(state subjectState, at time.Time) string {
	return fmt.Sprintf("%s-%s-%d-%s", state.SubjectType, sanitizeIDPart(state.SubjectID), at.UnixNano(), state.Status)
}

func subjectKey(subjectType models.StatusSubjectType, id string) string {
	return string(subjectType) + "|" + strings.ToLower(strings.TrimSpace(id))
}

func reachableStatus(ok bool) models.CurrentStatus {
	if ok {
		return models.StatusOnline
	}
	return models.StatusOffline
}

func arrayStatus(infra models.InfrastructureStatus) models.CurrentStatus {
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

func arrayNote(infra models.InfrastructureStatus) string {
	state := strings.TrimSpace(infra.UnraidArrayState)
	if state == "" {
		return "Array state is unknown."
	}
	if infra.UnraidArrayHealthy {
		return "Array is healthy."
	}
	return "Array state is " + state + "."
}

func boolNote(ok bool, okText, failText string) string {
	if ok {
		return okText
	}
	return failText
}

func sanitizeIDPart(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
