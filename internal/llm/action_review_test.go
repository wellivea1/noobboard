package llm

import (
	"strings"
	"testing"

	"github.com/wellivea1/noobboard/internal/models"
)

func intPtr(v int) *int { return &v }

// The live reviewer denied every auto-repair with "action_id suggests asking an
// admin to restart a container, while action_title says start recommendation" —
// the prompt never named the operation the server would actually run, so the
// reviewer judged the recommendation id instead. These tests pin the fix.
func TestBuildActionReviewPromptNamesTheOperation(t *testing.T) {
	prompt := BuildActionReviewPrompt(ActionReviewRequest{
		ActionID:      "ask_admin_to_restart_container",
		ActionTitle:   "Start recommendation",
		Operation:     "start",
		TargetID:      "emby",
		TargetLabel:   "EmbyServer",
		CurrentStatus: models.StatusOffline,
		ActorRole:     models.RoleAdmin,
		Via:           "chat",
		Snapshot: models.Snapshot{
			Apps: []models.AppStatus{{
				AppID:              "emby",
				DisplayName:        "EmbyServer",
				CurrentStatus:      models.StatusOffline,
				AgentRepairAllowed: true,
			}},
		},
	})

	if !strings.Contains(prompt, "operation: start") {
		t.Fatalf("prompt does not name the operation:\n%s", prompt)
	}
	// The provenance ids must still be present but explicitly demoted, so the
	// reviewer stops treating their wording as the thing under review.
	if !strings.Contains(prompt, "Provenance (not the action)") {
		t.Fatalf("prompt does not demote the recommendation id:\n%s", prompt)
	}
	if !strings.Contains(prompt, "recommendation_id: ask_admin_to_restart_container") {
		t.Fatalf("prompt dropped the recommendation id:\n%s", prompt)
	}
}

func TestBuildActionReviewPromptRefusesWithoutOperation(t *testing.T) {
	prompt := BuildActionReviewPrompt(ActionReviewRequest{
		ActionID:    "ask_admin_to_restart_container",
		ActionTitle: "Start recommendation",
		TargetID:    "emby",
	})
	if !strings.Contains(prompt, "refuse this review") {
		t.Fatalf("prompt with no operation must fail closed:\n%s", prompt)
	}
}

// A reviewer asked whether an operation is *warranted* needs the evidence for
// it. An earlier live denial read "no evidence is provided that a restart is
// needed" because the app line carried only a status word.
func TestCompactActionReviewAppsCarriesFailureEvidence(t *testing.T) {
	summary := compactActionReviewApps([]models.AppStatus{
		{
			AppID:               "emby",
			DisplayName:         "EmbyServer",
			CurrentStatus:       models.StatusOffline,
			DockerState:         "exited",
			DockerExitCode:      intPtr(137),
			DockerExitReason:    "OOMKilled",
			RecentStatusChanges: 3,
			AgentRepairAllowed:  true,
		},
		{AppID: "plex", DisplayName: "Plex", CurrentStatus: models.StatusOnline},
	}, "emby")

	for _, want := range []string{"*", "docker_state=exited", "recent_status_changes=3", "last_exit="} {
		if !strings.Contains(summary, want) {
			t.Fatalf("app evidence missing %q:\n%s", want, summary)
		}
	}
	// Healthy unrelated apps stay out; the reviewer only needs the target and
	// anything else that is misbehaving.
	if strings.Contains(summary, "plex") {
		t.Fatalf("healthy non-target app should be omitted:\n%s", summary)
	}
}
