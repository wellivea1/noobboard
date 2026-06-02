package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/wellivea1/server-status/internal/config"
	"github.com/wellivea1/server-status/internal/models"
)

type UnraidLiveClient struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

func NewUnraidLiveClient(baseURL, apiKey string) UnraidLiveClient {
	return UnraidLiveClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		http:    &http.Client{Timeout: 10 * time.Second},
	}
}

func (c UnraidLiveClient) Apps(ctx context.Context) ([]models.AppStatus, error) {
	if c.baseURL == "" || c.apiKey == "" {
		return nil, errors.New("unraid base URL and API key are required for docker live client")
	}
	containers, err := c.fetchContainers(ctx, `query DockerStatus {
  dockerContainers {
    id
    names
    state
    status
    autoStart
    image
    labels
    webUiUrl
    templatePath
  }
}`, true)
	if err != nil {
		containers, err = c.fetchContainers(ctx, `query DockerStatus {
  docker {
    containers {
      id
      names
      state
      status
      autoStart
      image
      labels
      webUiUrl
      templatePath
    }
  }
}`, false)
	}
	if err != nil {
		return nil, err
	}
	return appsFromContainers(containers), nil
}

func (c UnraidLiveClient) ControlContainer(ctx context.Context, app models.AppStatus, action ContainerAction) (ControlResult, error) {
	if c.baseURL == "" || c.apiKey == "" {
		return ControlResult{}, errors.New("unraid base URL and API key are required for docker live client")
	}
	if _, err := ParseContainerAction(string(action)); err != nil {
		return ControlResult{}, err
	}
	targetID := containerActionID(app)
	if targetID == "" {
		return ControlResult{}, errors.New("docker container id or name is required")
	}
	var result ControlResult
	var err error
	switch action {
	case ActionStart:
		result, err = c.runContainerMutation(ctx, "StartContainer", "start", targetID)
	case ActionStop:
		result, err = c.runContainerMutation(ctx, "StopContainer", "stop", targetID)
	case ActionRestart:
		if _, err = c.runContainerMutation(ctx, "StopContainer", "stop", targetID); err != nil {
			return ControlResult{}, err
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ControlResult{}, ctx.Err()
		case <-timer.C:
		}
		result, err = c.runContainerMutation(ctx, "StartContainer", "start", targetID)
	}
	if err != nil {
		return ControlResult{}, err
	}
	result.Action = action
	result.AppID = app.AppID
	result.ContainerName = app.ContainerName
	if result.ContainerID == "" {
		result.ContainerID = targetID
	}
	return result, nil
}

func (c UnraidLiveClient) Logs(ctx context.Context, app models.AppStatus, opts LogOptions) ([]models.LogLine, error) {
	if c.baseURL == "" || c.apiKey == "" {
		return nil, errors.New("unraid base URL and API key are required for docker live client")
	}
	targetID := containerActionID(app)
	if targetID == "" {
		return nil, errors.New("docker container id or name is required")
	}
	source := firstNonEmpty(app.ContainerName, app.DisplayName, app.AppID, targetID)
	var lastErr error
	for _, field := range []string{"lines", "logs", "entries", "stdout", "stderr", "content", "text", "log"} {
		raw, err := c.fetchLogField(ctx, targetID, field)
		if err != nil {
			lastErr = err
			continue
		}
		lines := logLinesFromRaw(source, raw)
		return trimLogs(lines, opts.Limit), nil
	}
	if lastErr != nil {
		return nil, fmt.Errorf("unraid docker logs are unavailable: %w", lastErr)
	}
	return nil, errors.New("unraid docker logs are unavailable")
}

func (c UnraidLiveClient) fetchLogField(ctx context.Context, targetID, field string) (json.RawMessage, error) {
	query := fmt.Sprintf(`query ContainerLogs($id: PrefixedID!) {
  docker {
    logs(id: $id) {
      %s
    }
  }
}`, field)
	var out struct {
		Data struct {
			Docker struct {
				Logs map[string]json.RawMessage `json:"logs"`
			} `json:"docker"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := c.graphqlVariables(ctx, query, map[string]interface{}{"id": targetID}, &out); err != nil {
		return nil, err
	}
	if len(out.Errors) > 0 {
		return nil, fmt.Errorf("unraid docker graphql error: %s", out.Errors[0].Message)
	}
	raw, ok := out.Data.Docker.Logs[field]
	if !ok {
		return nil, fmt.Errorf("unraid docker logs field %q was not returned", field)
	}
	return raw, nil
}

func (c UnraidLiveClient) runContainerMutation(ctx context.Context, operation, field, targetID string) (ControlResult, error) {
	query := fmt.Sprintf(`mutation %s($id: PrefixedID!) {
  docker {
    %s(id: $id) {
      id
      state
      status
    }
  }
}`, operation, field)
	var out struct {
		Data struct {
			Docker map[string]struct {
				ID     string `json:"id"`
				State  string `json:"state"`
				Status string `json:"status"`
			} `json:"docker"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := c.graphqlVariables(ctx, query, map[string]interface{}{"id": targetID}, &out); err != nil {
		return ControlResult{}, err
	}
	if len(out.Errors) > 0 {
		return ControlResult{}, fmt.Errorf("unraid docker graphql error: %s", out.Errors[0].Message)
	}
	container := out.Data.Docker[field]
	return ControlResult{
		ContainerID: container.ID,
		DockerState: dockerState(container.State),
		Status:      container.Status,
	}, nil
}

type dockerContainer struct {
	ID           string            `json:"id"`
	Names        stringList        `json:"names"`
	State        string            `json:"state"`
	Status       string            `json:"status"`
	AutoStart    flexBool          `json:"autoStart"`
	Image        string            `json:"image"`
	Labels       map[string]string `json:"labels"`
	WebUIURL     string            `json:"webUiUrl"`
	TemplatePath string            `json:"templatePath"`
}

func (c UnraidLiveClient) fetchContainers(ctx context.Context, query string, topLevel bool) ([]dockerContainer, error) {
	var out struct {
		Data struct {
			DockerContainers []dockerContainer `json:"dockerContainers"`
			Docker           struct {
				Containers []dockerContainer `json:"containers"`
			} `json:"docker"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := c.graphql(ctx, query, &out); err != nil {
		return nil, err
	}
	if len(out.Errors) > 0 {
		return nil, fmt.Errorf("unraid docker graphql error: %s", out.Errors[0].Message)
	}
	if topLevel {
		return out.Data.DockerContainers, nil
	}
	return out.Data.Docker.Containers, nil
}

func appsFromContainers(containers []dockerContainer) []models.AppStatus {
	now := time.Now().UTC()
	apps := make([]models.AppStatus, 0, len(containers))
	for _, container := range containers {
		name := displayName([]string(container.Names))
		state := dockerState(container.State)
		health := dockerHealth(container.Status)
		endpoint := models.EndpointSkipped
		current := currentStatusFromDocker(state, health)
		apps = append(apps, models.AppStatus{
			AppID:                    appID(name),
			DisplayName:              name,
			ContainerID:              prefixedContainerID(container.ID, name),
			ContainerName:            name,
			Category:                 "docker",
			IconURL:                  iconURL(container.Labels),
			IconSource:               iconSource(container.Labels),
			ImageRef:                 container.Image,
			WebURL:                   container.WebUIURL,
			TemplatePath:             container.TemplatePath,
			DataSource:               "unraid-docker",
			VisibleToGeneralUsers:    true,
			ProbeType:                models.ProbeDockerState,
			DockerState:              state,
			DockerHealth:             health,
			EndpointStatus:           endpoint,
			CurrentStatus:            current,
			Severity:                 severity(current),
			ServerSummary:            fmt.Sprintf("%s is %s.", name, current),
			AdminSummary:             fmt.Sprintf("%s state=%s status=%q auto_start=%t", name, container.State, container.Status, bool(container.AutoStart)),
			LLMVisibleAdmin:          true,
			LLMVisibleGeneral:        true,
			NotificationOptInAllowed: true,
			CurrentProbeResult: models.ProbeResult{
				Type:      models.ProbeDockerState,
				Target:    name,
				OK:        current == models.StatusOnline,
				Message:   container.Status,
				CheckedAt: now,
			},
		})
	}
	return apps
}

func iconURL(labels map[string]string) string {
	for _, key := range []string{
		"net.unraid.docker.icon",
		"org.opencontainers.image.icon",
		"com.docker.desktop.extension.icon",
	} {
		if value := strings.TrimSpace(labels[key]); value != "" {
			normalized, err := config.NormalizeIconURL(value)
			if err == nil {
				return normalized
			}
		}
	}
	return ""
}

func iconSource(labels map[string]string) string {
	if iconURL(labels) == "" {
		return ""
	}
	return "docker-label"
}

func (c UnraidLiveClient) graphql(ctx context.Context, query string, out interface{}) error {
	return c.graphqlVariables(ctx, query, nil, out)
}

func (c UnraidLiveClient) graphqlVariables(ctx context.Context, query string, variables map[string]interface{}, out interface{}) error {
	payload := map[string]interface{}{"query": query}
	if len(variables) > 0 {
		payload["variables"] = variables
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/graphql", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.apiKey)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("unraid docker graphql returned %d: %s", resp.StatusCode, string(data))
	}
	return json.Unmarshal(data, out)
}

func containerActionID(app models.AppStatus) string {
	for _, value := range []string{app.ContainerID, app.ContainerName, app.DisplayName, app.AppID} {
		if id := prefixedContainerID(value, ""); id != "" {
			return id
		}
	}
	return ""
}

func prefixedContainerID(id, fallbackName string) string {
	id = strings.TrimSpace(strings.TrimPrefix(id, "/"))
	if id == "" {
		id = strings.TrimSpace(strings.TrimPrefix(fallbackName, "/"))
	}
	if id == "" {
		return ""
	}
	if strings.Contains(id, ":") {
		return id
	}
	return "container:" + id
}

func logLinesFromRaw(source string, raw json.RawMessage) []models.LogLine {
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return logLinesFromText(source, text)
	}
	var stringsValue []string
	if err := json.Unmarshal(raw, &stringsValue); err == nil {
		lines := make([]models.LogLine, 0, len(stringsValue))
		for _, line := range stringsValue {
			lines = append(lines, logLinesFromText(source, line)...)
		}
		return lines
	}
	var entries []map[string]interface{}
	if err := json.Unmarshal(raw, &entries); err == nil {
		lines := make([]models.LogLine, 0, len(entries))
		for _, entry := range entries {
			line := firstMapString(entry, "line", "message", "text", "log", "content", "value", "stdout", "stderr")
			if strings.TrimSpace(line) == "" {
				continue
			}
			lines = append(lines, models.LogLine{
				Source:    source,
				Timestamp: firstMapTime(entry, "timestamp", "time", "createdAt", "created_at", "date"),
				Line:      line,
			})
		}
		return lines
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err == nil {
		var lines []models.LogLine
		for _, key := range []string{"lines", "logs", "entries", "stdout", "stderr", "content", "text", "log"} {
			if value, ok := object[key]; ok {
				lines = append(lines, logLinesFromRaw(source, value)...)
			}
		}
		return lines
	}
	return nil
}

func logLinesFromText(source, text string) []models.LogLine {
	parts := strings.FieldsFunc(text, func(r rune) bool { return r == '\n' || r == '\r' })
	lines := make([]models.LogLine, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		lines = append(lines, models.LogLine{Source: source, Line: part})
	}
	return lines
}

func firstMapString(values map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		if value, ok := values[key].(string); ok && strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func firstMapTime(values map[string]interface{}, keys ...string) time.Time {
	for _, key := range keys {
		value, ok := values[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case string:
			for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
				if parsed, err := time.Parse(layout, typed); err == nil {
					return parsed.UTC()
				}
			}
		case float64:
			if typed > 0 {
				return time.Unix(int64(typed), 0).UTC()
			}
		}
	}
	return time.Time{}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func displayName(names []string) string {
	if len(names) == 0 {
		return "unknown-container"
	}
	name := strings.TrimPrefix(names[0], "/")
	if name == "" {
		return "unknown-container"
	}
	return name
}

type stringList []string

func (s *stringList) UnmarshalJSON(data []byte) error {
	var many []string
	if err := json.Unmarshal(data, &many); err == nil {
		*s = many
		return nil
	}
	var one string
	if err := json.Unmarshal(data, &one); err != nil {
		return err
	}
	if one == "" {
		*s = nil
		return nil
	}
	*s = []string{one}
	return nil
}

type flexBool bool

func (b *flexBool) UnmarshalJSON(data []byte) error {
	var value bool
	if err := json.Unmarshal(data, &value); err == nil {
		*b = flexBool(value)
		return nil
	}
	var text string
	if err := json.Unmarshal(data, &text); err != nil {
		return err
	}
	switch strings.ToLower(strings.TrimSpace(text)) {
	case "1", "true", "yes", "on":
		*b = true
	default:
		*b = false
	}
	return nil
}

func appID(name string) string {
	value := strings.ToLower(strings.TrimSpace(name))
	value = strings.Trim(regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(value, "-"), "-")
	if value == "" {
		return "unknown-container"
	}
	return value
}

func dockerState(state string) models.DockerState {
	switch strings.ToLower(state) {
	case "running", "started", "up":
		return models.DockerRunning
	case "exited", "dead", "created", "paused", "restarting", "removing", "stopped", "down":
		return models.DockerExited
	default:
		return models.DockerUnknown
	}
}

func dockerHealth(status string) models.DockerHealth {
	lower := strings.ToLower(status)
	switch {
	case strings.Contains(lower, "unhealthy"):
		return models.HealthUnhealthy
	case strings.Contains(lower, "healthy"):
		return models.HealthHealthy
	case lower == "":
		return models.HealthUnknown
	default:
		return models.HealthNone
	}
}

func currentStatusFromDocker(state models.DockerState, health models.DockerHealth) models.CurrentStatus {
	if state == models.DockerExited {
		return models.StatusOffline
	}
	if state == models.DockerRunning && health == models.HealthUnhealthy {
		return models.StatusDegraded
	}
	if state == models.DockerRunning {
		return models.StatusOnline
	}
	return models.StatusUnknown
}

func severity(status models.CurrentStatus) models.Severity {
	switch status {
	case models.StatusOnline:
		return models.SeverityNone
	case models.StatusDegraded, models.StatusUnknown:
		return models.SeverityLow
	case models.StatusOffline:
		return models.SeverityMedium
	default:
		return models.SeverityNone
	}
}
