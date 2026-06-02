package unraid

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/wellivea1/noobboard/internal/models"
)

type LiveClient struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

func NewLiveClient(baseURL, apiKey string) LiveClient {
	return LiveClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		http:    &http.Client{Timeout: 10 * time.Second},
	}
}

func (c LiveClient) Status(ctx context.Context) (models.InfrastructureStatus, []models.LogLine, error) {
	if c.baseURL == "" || c.apiKey == "" {
		return models.InfrastructureStatus{}, nil, errors.New("unraid base URL and API key are required")
	}
	query := `query DashboardStatus {
  info { os { platform distro release uptime } }
  array {
    state
    capacity { disks { free used total } }
    disks { name status temp size }
  }
}`
	var out struct {
		Data struct {
			Info struct {
				OS struct {
					Distro  string `json:"distro"`
					Release string `json:"release"`
				} `json:"os"`
			} `json:"info"`
			Array struct {
				State    string `json:"state"`
				Capacity struct {
					Disks struct {
						Free  flexInt64 `json:"free"`
						Used  flexInt64 `json:"used"`
						Total flexInt64 `json:"total"`
					} `json:"disks"`
				} `json:"capacity"`
				Disks []struct {
					Name   string    `json:"name"`
					Status string    `json:"status"`
					Temp   flexInt64 `json:"temp"`
					Size   flexInt64 `json:"size"`
				} `json:"disks"`
			} `json:"array"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := c.graphql(ctx, query, &out); err != nil {
		return c.apiFailureStatus(ctx, err)
	}
	if len(out.Errors) > 0 {
		return c.apiFailureStatus(ctx, fmt.Errorf("unraid graphql error: %s", out.Errors[0].Message))
	}
	arrayHealthy := true
	var warnings []string
	for _, disk := range out.Data.Array.Disks {
		if !diskStatusOK(disk.Status) {
			arrayHealthy = false
			warnings = append(warnings, fmt.Sprintf("%s status %s", disk.Name, disk.Status))
		}
		if int64(disk.Temp) >= 55 {
			warnings = append(warnings, fmt.Sprintf("%s temperature %dC", disk.Name, disk.Temp))
		}
	}
	state := strings.ToLower(out.Data.Array.State)
	total := int64(out.Data.Array.Capacity.Disks.Total)
	used := int64(out.Data.Array.Capacity.Disks.Used)
	free := int64(out.Data.Array.Capacity.Disks.Free)
	usedPct := 0.0
	if total > 0 {
		usedPct = float64(used) / float64(total) * 100
		if usedPct >= 95 {
			arrayHealthy = false
			warnings = append(warnings, fmt.Sprintf("array capacity %.1f%% used", usedPct))
		}
	}
	version := strings.TrimSpace(out.Data.Info.OS.Distro + " " + out.Data.Info.OS.Release)
	parityState, _ := c.parityCheckState(ctx)
	infra := models.InfrastructureStatus{
		NASReachable:            true,
		UnraidAPIReachable:      true,
		UnraidVersion:           version,
		UnraidArrayState:        state,
		UnraidArrayHealthy:      arrayHealthy && state == "started",
		ArrayDiskCount:          len(out.Data.Array.Disks),
		ArrayDiskWarningCount:   len(warnings),
		ArrayCapacityTotalBytes: total,
		ArrayCapacityUsedBytes:  used,
		ArrayCapacityFreeBytes:  free,
		ArrayCapacityUsedPct:    usedPct,
		StorageWarnings:         warnings,
		ParityCheckState:        parityState,
		LastCheckedAt:           time.Now().UTC(),
		SourceHealth: models.SourceHealth{
			Unraid: version,
		},
	}
	return infra, nil, nil
}

func (c LiveClient) parityCheckState(ctx context.Context) (string, error) {
	query := `query ParityStatus {
  vars {
    mdResync
    mdResyncPos
    mdResyncAction
    mdResyncCorr
    mdResyncDt
    mdResyncDb
  }
}`
	var out struct {
		Data struct {
			Vars struct {
				MDResync       flexInt64 `json:"mdResync"`
				MDResyncPos    flexInt64 `json:"mdResyncPos"`
				MDResyncAction string    `json:"mdResyncAction"`
				MDResyncCorr   flexInt64 `json:"mdResyncCorr"`
				MDResyncDt     flexInt64 `json:"mdResyncDt"`
				MDResyncDb     flexInt64 `json:"mdResyncDb"`
			} `json:"vars"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := c.graphql(ctx, query, &out); err != nil {
		return "", err
	}
	if len(out.Errors) > 0 {
		return "", fmt.Errorf("unraid parity graphql error: %s", out.Errors[0].Message)
	}
	vars := out.Data.Vars
	if int64(vars.MDResync) <= 0 && int64(vars.MDResyncPos) <= 0 && int64(vars.MDResyncDt) <= 0 && int64(vars.MDResyncDb) <= 0 {
		return "idle", nil
	}
	action := strings.TrimSpace(vars.MDResyncAction)
	if action == "" {
		action = "running"
	}
	state := strings.ToLower(action)
	if int64(vars.MDResyncCorr) > 0 && !strings.Contains(state, "correct") {
		state += " correcting"
	}
	return state, nil
}

func (c LiveClient) apiFailureStatus(ctx context.Context, apiErr error) (models.InfrastructureStatus, []models.LogLine, error) {
	if !c.webGUIReachable(ctx) {
		return models.InfrastructureStatus{}, nil, apiErr
	}
	return models.InfrastructureStatus{
		NASReachable:       true,
		UnraidAPIReachable: false,
		UnraidArrayState:   "unknown",
		UnraidArrayHealthy: false,
		LastCheckedAt:      time.Now().UTC(),
		SourceHealth: models.SourceHealth{
			Unraid: "web gui reachable; graphql unavailable: " + apiErr.Error(),
		},
	}, nil, nil
}

func (c LiveClient) webGUIReachable(ctx context.Context) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, c.baseURL, nil)
	if err != nil {
		return false
	}
	resp, err := c.http.Do(req)
	if err == nil {
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusMethodNotAllowed {
			return resp.StatusCode >= 200 && resp.StatusCode < 500
		}
	}
	req, err = http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL, nil)
	if err != nil {
		return false
	}
	resp, err = c.http.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode >= 200 && resp.StatusCode < 500
}

func diskStatusOK(status string) bool {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "", "DISK_OK", "OK", "ONLINE", "ACTIVE", "HEALTHY":
		return true
	default:
		return false
	}
}

func (c LiveClient) graphql(ctx context.Context, query string, out interface{}) error {
	body, err := json.Marshal(map[string]string{"query": query})
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
		return fmt.Errorf("unraid graphql returned %d: %s", resp.StatusCode, string(data))
	}
	return json.Unmarshal(data, out)
}

type flexInt64 int64

func (n *flexInt64) UnmarshalJSON(data []byte) error {
	text := strings.TrimSpace(string(data))
	if text == "" || text == "null" {
		*n = 0
		return nil
	}
	text = strings.Trim(text, `"`)
	if text == "" {
		*n = 0
		return nil
	}
	if i, err := strconv.ParseInt(text, 10, 64); err == nil {
		*n = flexInt64(i)
		return nil
	}
	f, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return err
	}
	*n = flexInt64(f)
	return nil
}
