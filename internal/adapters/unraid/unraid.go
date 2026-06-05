package unraid

import (
	"context"
	"strings"

	"github.com/wellivea1/noobboard/internal/adapters/fixture"
	"github.com/wellivea1/noobboard/internal/models"
)

type Client interface {
	Status(context.Context) (models.InfrastructureStatus, []models.LogLine, error)
	StartArray(context.Context) (ArrayControlResult, error)
}

type ArrayControlResult struct {
	Action string `json:"action"`
	State  string `json:"state,omitempty"`
	Status string `json:"status,omitempty"`
}

type FixtureClient struct {
	dir      string
	scenario string
}

func NewFixtureClient(dir, scenario string) FixtureClient {
	return FixtureClient{dir: dir, scenario: scenario}
}

func (c FixtureClient) Status(context.Context) (models.InfrastructureStatus, []models.LogLine, error) {
	snapshot, err := fixture.LoadSnapshot(c.dir, c.scenario)
	if err != nil {
		return models.InfrastructureStatus{}, nil, err
	}
	var logs []models.LogLine
	for _, app := range snapshot.Apps {
		for _, line := range app.RecentLogs {
			if line.Source == "unraid" || line.Source == "syslog" {
				logs = append(logs, line)
			}
		}
	}
	return snapshot.Infrastructure, logs, nil
}

func (c FixtureClient) StartArray(context.Context) (ArrayControlResult, error) {
	snapshot, err := fixture.LoadSnapshot(c.dir, c.scenario)
	if err != nil {
		return ArrayControlResult{}, err
	}
	state := "started"
	if strings.EqualFold(strings.TrimSpace(snapshot.Infrastructure.UnraidArrayState), "started") {
		state = "started"
	}
	return ArrayControlResult{Action: "start_array", State: state, Status: "fixture array start accepted"}, nil
}
