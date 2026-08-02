package unifi

import (
	"context"
	"errors"

	"github.com/wellivea1/noobboard/internal/adapters/fixture"
	"github.com/wellivea1/noobboard/internal/models"
)

type Client interface {
	Status(context.Context) (models.InfrastructureStatus, error)
	// RestartableDevices lists devices the safety rule permits restarting:
	// offline, and not a gateway. See LiveClient.RestartableDevices for why.
	RestartableDevices(context.Context) ([]RestartableDevice, error)
	RestartDevice(context.Context, string) (DeviceControlResult, error)
	// DeviceOnline is for verification after a restart. The bool is meaningful
	// only when err is nil — an unreachable UniFi is not a healthy device.
	DeviceOnline(context.Context, string) (bool, error)
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

// Fixture control is refused rather than simulated. A fixture that reported a
// successful restart would let demo data present as a real device action, which
// the source-honesty rule exists to prevent.
func (c FixtureClient) RestartableDevices(ctx context.Context) ([]RestartableDevice, error) {
	snapshot, err := fixture.LoadSnapshot(c.dir, c.scenario)
	if err != nil {
		return nil, err
	}
	if snapshot.Infrastructure.UniFiOfflineDeviceCount == 0 {
		return nil, nil
	}
	return []RestartableDevice{{
		ID:    "fixture-offline-device",
		Name:  "Fixture offline device",
		Model: "fixture",
		State: "OFFLINE",
	}}, nil
}

func (c FixtureClient) RestartDevice(context.Context, string) (DeviceControlResult, error) {
	return DeviceControlResult{}, errors.New("device restart is not available with fixture data")
}

func (c FixtureClient) DeviceOnline(context.Context, string) (bool, error) {
	return false, errors.New("device state is not available with fixture data")
}
