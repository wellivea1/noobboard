package fixture

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/wellivea1/noobboard/internal/models"
)

func LoadSnapshot(dir, scenario string) (models.Snapshot, error) {
	if scenario == "" {
		scenario = os.Getenv("NOOBBOARD_FIXTURE_SCENARIO")
		if scenario == "" {
			scenario = os.Getenv("HSD_FIXTURE_SCENARIO")
		}
	}
	if scenario == "" {
		scenario = "all_systems_online"
	}
	path := filepath.Join(dir, "incidents", scenario+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return models.Snapshot{}, err
	}
	var snapshot models.Snapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return models.Snapshot{}, err
	}
	now := time.Now().UTC()
	if snapshot.GeneratedAt.IsZero() {
		snapshot.GeneratedAt = now
	}
	if snapshot.Infrastructure.LastCheckedAt.IsZero() {
		snapshot.Infrastructure.LastCheckedAt = now
	}
	if snapshot.Infrastructure.SourceHealth.Unraid == "" {
		snapshot.Infrastructure.SourceHealth.Unraid = "fixture: " + scenario
	}
	if snapshot.Infrastructure.SourceHealth.Docker == "" {
		snapshot.Infrastructure.SourceHealth.Docker = "fixture: " + scenario
	}
	if snapshot.Infrastructure.SourceHealth.UniFi == "" {
		snapshot.Infrastructure.SourceHealth.UniFi = "fixture: " + scenario
	}
	if snapshot.Infrastructure.SourceHealth.Probes == "" {
		snapshot.Infrastructure.SourceHealth.Probes = "fixture: " + scenario
	}
	for i := range snapshot.Apps {
		if snapshot.Apps[i].DataSource == "" {
			snapshot.Apps[i].DataSource = "fixture: " + scenario
		}
		if snapshot.Apps[i].CurrentProbeResult.CheckedAt.IsZero() {
			snapshot.Apps[i].CurrentProbeResult.CheckedAt = now
		}
	}
	return snapshot, nil
}
