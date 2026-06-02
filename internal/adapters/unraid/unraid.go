package unraid

import (
	"context"

	"github.com/wellivea1/server-status/internal/adapters/fixture"
	"github.com/wellivea1/server-status/internal/models"
)

type Client interface {
	Status(context.Context) (models.InfrastructureStatus, []models.LogLine, error)
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
