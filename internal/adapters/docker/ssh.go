package docker

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/wellivea1/server-status/internal/models"
)

const sshDefaultLogLimit = 80

type SSHOptions struct {
	Host    string
	Port    int
	User    string
	KeyFile string
	Command string
	Timeout time.Duration
}

type SSHClient struct {
	opts SSHOptions
}

func NewSSHClient(opts SSHOptions) SSHClient {
	if opts.Port == 0 {
		opts.Port = 22
	}
	if opts.Command == "" {
		opts.Command = "ssh"
	}
	if opts.Timeout == 0 {
		opts.Timeout = 15 * time.Second
	}
	return SSHClient{opts: opts}
}

func (c SSHClient) Apps(ctx context.Context) ([]models.AppStatus, error) {
	out, err := c.run(ctx, "docker", "ps", "-a", "--no-trunc", "--format", "{{json .}}")
	if err != nil {
		return nil, err
	}
	var containers []cliContainer
	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var item cliContainer
		if err := json.Unmarshal([]byte(line), &item); err != nil {
			return nil, fmt.Errorf("parse ssh docker ps: %w", err)
		}
		containers = append(containers, item)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return appsFromCLIContainers(containers), nil
}

func (c SSHClient) ControlContainer(ctx context.Context, app models.AppStatus, action ContainerAction) (ControlResult, error) {
	if _, err := ParseContainerAction(string(action)); err != nil {
		return ControlResult{}, err
	}
	target := sshContainerTarget(app)
	if target == "" {
		return ControlResult{}, errors.New("docker container id or name is required")
	}
	if _, err := c.run(ctx, "docker", string(action), target); err != nil {
		return ControlResult{}, err
	}
	return ControlResult{
		Action:        action,
		AppID:         app.AppID,
		ContainerID:   app.ContainerID,
		ContainerName: app.ContainerName,
		DockerState:   app.DockerState,
		Status:        "ssh docker command accepted",
	}, nil
}

func (c SSHClient) Logs(ctx context.Context, app models.AppStatus, opts LogOptions) ([]models.LogLine, error) {
	target := sshContainerTarget(app)
	if target == "" {
		return nil, errors.New("docker container id or name is required")
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = sshDefaultLogLimit
	}
	out, err := c.run(ctx, "docker", "logs", "--tail", strconv.Itoa(limit), target)
	if err != nil {
		return nil, err
	}
	return logLinesFromText(firstNonEmpty(app.ContainerName, app.DisplayName, app.AppID, target), string(out)), nil
}

func (c SSHClient) run(ctx context.Context, remoteArgs ...string) ([]byte, error) {
	if strings.TrimSpace(c.opts.Host) == "" || strings.TrimSpace(c.opts.User) == "" {
		return nil, errors.New("unraid ssh host and user are required")
	}
	timeoutCtx, cancel := context.WithTimeout(ctx, c.opts.Timeout)
	defer cancel()
	args := []string{
		"-o", "BatchMode=yes",
		"-o", "PasswordAuthentication=no",
		"-p", strconv.Itoa(c.opts.Port),
	}
	if strings.TrimSpace(c.opts.KeyFile) != "" {
		args = append(args, "-i", c.opts.KeyFile)
	}
	args = append(args, c.opts.User+"@"+c.opts.Host)
	args = append(args, remoteArgs...)
	cmd := exec.CommandContext(timeoutCtx, c.opts.Command, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("unraid ssh docker command failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

type cliContainer struct {
	ID     string `json:"ID"`
	Image  string `json:"Image"`
	Names  string `json:"Names"`
	State  string `json:"State"`
	Status string `json:"Status"`
	Labels string `json:"Labels"`
}

func appsFromCLIContainers(containers []cliContainer) []models.AppStatus {
	converted := make([]dockerContainer, 0, len(containers))
	for _, container := range containers {
		converted = append(converted, dockerContainer{
			ID:     container.ID,
			Names:  stringList{container.Names},
			State:  container.State,
			Status: container.Status,
			Image:  container.Image,
			Labels: dockerCLILabels(container.Labels),
		})
	}
	apps := appsFromContainers(converted)
	for i := range apps {
		apps[i].DataSource = "unraid-ssh-docker"
		apps[i].AdminSummary = strings.TrimSpace(apps[i].AdminSummary + " source=ssh")
	}
	return apps
}

func dockerCLILabels(value string) map[string]string {
	labels := map[string]string{}
	for _, part := range strings.Split(value, ",") {
		key, val, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok || strings.TrimSpace(key) == "" {
			continue
		}
		labels[strings.TrimSpace(key)] = strings.TrimSpace(val)
	}
	return labels
}

func sshContainerTarget(app models.AppStatus) string {
	for _, value := range []string{app.ContainerID, app.ContainerName, app.DisplayName, app.AppID} {
		value = strings.TrimSpace(strings.TrimPrefix(value, "container:"))
		if value != "" {
			return value
		}
	}
	return ""
}
