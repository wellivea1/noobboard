package unifi

import (
	"context"

	"github.com/wellivea1/noobboard/internal/adapters/fixture"
	"github.com/wellivea1/noobboard/internal/models"
)

type Client interface {
	Status(context.Context) (models.InfrastructureStatus, error)
}

type FixtureClient struct {
	dir      string
	scenario string
}

func NewFixtureClient(dir, scenario string) FixtureClient {
	return FixtureClient{dir: dir, scenario: scenario}
}

func (c FixtureClient) Status(context.Context) (models.InfrastructureStatus, error) {
	snapshot, err := fixture.LoadSnapshot(c.dir, c.scenario)
	if err != nil {
		return models.InfrastructureStatus{}, err
	}
	return snapshot.Infrastructure, nil
}
