package docker

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/wellivea1/noobboard/internal/adapters/fixture"
	"github.com/wellivea1/noobboard/internal/models"
)

type Client interface {
	Apps(context.Context) ([]models.AppStatus, error)
	ControlContainer(context.Context, models.AppStatus, ContainerAction) (ControlResult, error)
	Logs(context.Context, models.AppStatus, LogOptions) ([]models.LogLine, error)
}

type LargestListClient struct {
	primary  Client
	fallback Client
}

func NewLargestListClient(primary, fallback Client) LargestListClient {
	return LargestListClient{primary: primary, fallback: fallback}
}

func (c LargestListClient) Apps(ctx context.Context) ([]models.AppStatus, error) {
	primaryApps, primaryErr := c.primary.Apps(ctx)
	fallbackApps, fallbackErr := c.fallback.Apps(ctx)
	if primaryErr != nil {
		if fallbackErr == nil {
			return fallbackApps, nil
		}
		return nil, primaryErr
	}
	if fallbackErr == nil && len(fallbackApps) > len(primaryApps) {
		return fallbackApps, nil
	}
	return primaryApps, nil
}

func (c LargestListClient) ControlContainer(ctx context.Context, app models.AppStatus, action ContainerAction) (ControlResult, error) {
	if app.DataSource == "unraid-ssh-docker" {
		return c.fallback.ControlContainer(ctx, app, action)
	}
	result, err := c.primary.ControlContainer(ctx, app, action)
	if err == nil {
		return result, nil
	}
	if !allowsSSHFallback(err) {
		return ControlResult{}, err
	}
	return c.fallback.ControlContainer(ctx, app, action)
}

func (c LargestListClient) Logs(ctx context.Context, app models.AppStatus, opts LogOptions) ([]models.LogLine, error) {
	if app.DataSource == "unraid-ssh-docker" {
		return c.fallback.Logs(ctx, app, opts)
	}
	lines, err := c.primary.Logs(ctx, app, opts)
	if err == nil {
		return lines, nil
	}
	if !allowsSSHFallback(err) {
		return nil, err
	}
	return c.fallback.Logs(ctx, app, opts)
}

type fallbackableDockerError struct {
	err error
}

func (e fallbackableDockerError) Error() string {
	return e.err.Error()
}

func (e fallbackableDockerError) Unwrap() error {
	return e.err
}

func markFallbackable(err error) error {
	if err == nil {
		return nil
	}
	return fallbackableDockerError{err: err}
}

func allowsSSHFallback(err error) bool {
	var fallbackable fallbackableDockerError
	return errors.As(err, &fallbackable)
}

type dockerGraphQLError struct {
	Message string
}

func (e dockerGraphQLError) Error() string {
	return "unraid docker graphql error: " + e.Message
}

func graphQLSchemaError(message string) bool {
	normalized := strings.ToLower(strings.TrimSpace(message))
	return strings.Contains(normalized, "cannot query field") ||
		strings.Contains(normalized, "unknown field") ||
		strings.Contains(normalized, "is not defined") ||
		strings.Contains(normalized, "unknown argument")
}

func graphQLFieldUnsupported(err error, field string) bool {
	var graphErr dockerGraphQLError
	if !errors.As(err, &graphErr) {
		return false
	}
	message := strings.ToLower(graphErr.Message)
	field = strings.ToLower(strings.TrimSpace(field))
	return graphQLSchemaError(message) && strings.Contains(message, strings.ToLower(field))
}

type LogOptions struct {
	Limit int
	Since time.Time
}

type ContainerAction string

const (
	ActionStart   ContainerAction = "start"
	ActionStop    ContainerAction = "stop"
	ActionRestart ContainerAction = "restart"
)

type ControlResult struct {
	Action        ContainerAction    `json:"action"`
	AppID         string             `json:"app_id"`
	ContainerID   string             `json:"container_id,omitempty"`
	ContainerName string             `json:"container_name,omitempty"`
	DockerState   models.DockerState `json:"docker_state,omitempty"`
	Status        string             `json:"status,omitempty"`
}

func ParseContainerAction(value string) (ContainerAction, error) {
	switch ContainerAction(strings.ToLower(strings.TrimSpace(value))) {
	case ActionStart:
		return ActionStart, nil
	case ActionStop:
		return ActionStop, nil
	case ActionRestart:
		return ActionRestart, nil
	default:
		return "", errors.New("unsupported docker container action")
	}
}

type FixtureClient struct {
	dir      string
	scenario string
}

func NewFixtureClient(dir, scenario string) FixtureClient {
	return FixtureClient{dir: dir, scenario: scenario}
}

func (c FixtureClient) Apps(context.Context) ([]models.AppStatus, error) {
	snapshot, err := fixture.LoadSnapshot(c.dir, c.scenario)
	if err != nil {
		return nil, err
	}
	return snapshot.Apps, nil
}

func (c FixtureClient) ControlContainer(_ context.Context, app models.AppStatus, action ContainerAction) (ControlResult, error) {
	if _, err := ParseContainerAction(string(action)); err != nil {
		return ControlResult{}, err
	}
	return ControlResult{
		Action:        action,
		AppID:         app.AppID,
		ContainerID:   app.ContainerID,
		ContainerName: app.ContainerName,
		DockerState:   app.DockerState,
		Status:        "fixture action accepted",
	}, nil
}

func (c FixtureClient) Logs(ctx context.Context, app models.AppStatus, opts LogOptions) ([]models.LogLine, error) {
	snapshot, err := fixture.LoadSnapshot(c.dir, c.scenario)
	if err != nil {
		return nil, err
	}
	for _, candidate := range snapshot.Apps {
		if appMatches(candidate, app) {
			return trimLogs(candidate.RecentLogs, opts.Limit), nil
		}
	}
	return nil, errors.New("fixture docker app logs not found")
}

func appMatches(candidate, target models.AppStatus) bool {
	for _, left := range []string{candidate.AppID, candidate.ContainerName, candidate.DisplayName} {
		for _, right := range []string{target.AppID, target.ContainerName, target.DisplayName} {
			if strings.TrimSpace(left) != "" && strings.EqualFold(strings.TrimSpace(left), strings.TrimSpace(right)) {
				return true
			}
		}
	}
	return false
}

func trimLogs(lines []models.LogLine, limit int) []models.LogLine {
	if limit > 0 && len(lines) > limit {
		lines = lines[len(lines)-limit:]
	}
	return append([]models.LogLine(nil), lines...)
}
