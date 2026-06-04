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
					Distro  string    `json:"distro"`
					Release string    `json:"release"`
					Uptime  flexInt64 `json:"uptime"`
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
	details, _ := c.systemDetails(ctx)
	if details.UnraidVersion != "" {
		version = details.UnraidVersion
	}
	infra := models.InfrastructureStatus{
		NASReachable:            true,
		UnraidAPIReachable:      true,
		UnraidVersion:           version,
		UnraidUptimeSeconds:     int64(out.Data.Info.OS.Uptime),
		UnraidCPUBrand:          details.CPUBrand,
		UnraidCPUCores:          details.CPUCores,
		UnraidCPUThreads:        details.CPUThreads,
		UnraidMemoryTotalBytes:  details.MemoryTotalBytes,
		UnraidMemoryUsedBytes:   details.MemoryUsedBytes,
		UnraidMemoryUsedPct:     details.MemoryUsedPct,
		UnraidNotificationCount: details.NotificationCount,
		UnraidAlertCount:        details.AlertCount,
		UnraidWarningCount:      details.WarningCount,
		UnraidVMCount:           details.VMCount,
		UnraidVMRunningCount:    details.VMRunningCount,
		UnraidVMStoppedCount:    details.VMStoppedCount,
		UnraidVMNames:           details.VMNames,
		UnraidShareCount:        details.ShareCount,
		UnraidShareNames:        details.ShareNames,
		UnraidArrayState:        state,
		UnraidArrayHealthy:      arrayHealthy && state == "started",
		ArrayDiskCount:          len(out.Data.Array.Disks),
		ArrayDiskWarningCount:   len(warnings),
		ArrayCapacityTotalBytes: total,
		ArrayCapacityUsedBytes:  used,
		ArrayCapacityFreeBytes:  free,
		ArrayCapacityUsedPct:    usedPct,
		DockerNetworkCount:      details.DockerNetworkCount,
		DockerNetworkNames:      details.DockerNetworkNames,
		StorageWarnings:         warnings,
		ParityCheckState:        parityState,
		LastCheckedAt:           time.Now().UTC(),
		SourceHealth: models.SourceHealth{
			Unraid: version,
		},
	}
	return infra, nil, nil
}

type systemDetails struct {
	UnraidVersion      string
	CPUBrand           string
	CPUCores           int
	CPUThreads         int
	MemoryTotalBytes   int64
	MemoryUsedBytes    int64
	MemoryUsedPct      float64
	NotificationCount  int
	AlertCount         int
	WarningCount       int
	VMCount            int
	VMRunningCount     int
	VMStoppedCount     int
	VMNames            []string
	ShareCount         int
	ShareNames         []string
	DockerNetworkCount int
	DockerNetworkNames []string
}

func (c LiveClient) systemDetails(ctx context.Context) (systemDetails, error) {
	var details systemDetails
	var errs []error
	if value, err := c.cpuDetails(ctx); err == nil {
		details.CPUBrand = value.CPUBrand
		details.CPUCores = value.CPUCores
		details.CPUThreads = value.CPUThreads
	} else {
		errs = append(errs, err)
	}
	if value, err := c.memoryDetails(ctx); err == nil {
		details.MemoryTotalBytes = value.MemoryTotalBytes
		details.MemoryUsedBytes = value.MemoryUsedBytes
		details.MemoryUsedPct = value.MemoryUsedPct
	} else {
		errs = append(errs, err)
	}
	if version, err := c.versionDetails(ctx); err == nil {
		details.UnraidVersion = version
	} else {
		errs = append(errs, err)
	}
	if value, err := c.notificationDetails(ctx); err == nil {
		details.NotificationCount = value.NotificationCount
		details.AlertCount = value.AlertCount
		details.WarningCount = value.WarningCount
	} else {
		errs = append(errs, err)
	}
	if value, err := c.dockerNetworkDetails(ctx); err == nil {
		details.DockerNetworkCount = value.DockerNetworkCount
		details.DockerNetworkNames = value.DockerNetworkNames
	} else {
		errs = append(errs, err)
	}
	if value, err := c.vmDetails(ctx); err == nil {
		details.VMCount = value.VMCount
		details.VMRunningCount = value.VMRunningCount
		details.VMStoppedCount = value.VMStoppedCount
		details.VMNames = value.VMNames
	} else {
		errs = append(errs, err)
	}
	if value, err := c.shareDetails(ctx); err == nil {
		details.ShareCount = value.ShareCount
		details.ShareNames = value.ShareNames
	} else {
		errs = append(errs, err)
	}
	if !details.hasData() && len(errs) > 0 {
		return details, errors.Join(errs...)
	}
	return details, nil
}

func (d systemDetails) hasData() bool {
	return d.UnraidVersion != "" ||
		d.CPUBrand != "" ||
		d.CPUCores != 0 ||
		d.CPUThreads != 0 ||
		d.MemoryTotalBytes != 0 ||
		d.MemoryUsedBytes != 0 ||
		d.MemoryUsedPct != 0 ||
		d.NotificationCount != 0 ||
		d.AlertCount != 0 ||
		d.WarningCount != 0 ||
		d.VMCount != 0 ||
		d.VMRunningCount != 0 ||
		d.VMStoppedCount != 0 ||
		len(d.VMNames) != 0 ||
		d.ShareCount != 0 ||
		len(d.ShareNames) != 0 ||
		d.DockerNetworkCount != 0 ||
		len(d.DockerNetworkNames) != 0
}

func (c LiveClient) cpuDetails(ctx context.Context) (systemDetails, error) {
	query := `query SystemCPUDetails {
  info {
    cpu { manufacturer brand cores threads }
  }
}`
	var out struct {
		Data struct {
			Info struct {
				CPU struct {
					Manufacturer string    `json:"manufacturer"`
					Brand        string    `json:"brand"`
					Cores        flexInt64 `json:"cores"`
					Threads      flexInt64 `json:"threads"`
				} `json:"cpu"`
			} `json:"info"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := c.graphql(ctx, query, &out); err != nil {
		return systemDetails{}, err
	}
	if len(out.Errors) > 0 {
		return systemDetails{}, fmt.Errorf("unraid cpu graphql error: %s", out.Errors[0].Message)
	}
	brand := strings.TrimSpace(out.Data.Info.CPU.Brand)
	if brand == "" {
		brand = strings.TrimSpace(out.Data.Info.CPU.Manufacturer)
	}
	return systemDetails{
		CPUBrand:   brand,
		CPUCores:   int(out.Data.Info.CPU.Cores),
		CPUThreads: int(out.Data.Info.CPU.Threads),
	}, nil
}

func (c LiveClient) memoryDetails(ctx context.Context) (systemDetails, error) {
	query := `query SystemMemoryDetails {
  info {
    memory { total used free available }
  }
}`
	var out struct {
		Data struct {
			Info struct {
				Memory struct {
					Total     flexInt64 `json:"total"`
					Used      flexInt64 `json:"used"`
					Free      flexInt64 `json:"free"`
					Available flexInt64 `json:"available"`
				} `json:"memory"`
			} `json:"info"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := c.graphql(ctx, query, &out); err != nil {
		return systemDetails{}, err
	}
	if len(out.Errors) > 0 {
		return systemDetails{}, fmt.Errorf("unraid memory graphql error: %s", out.Errors[0].Message)
	}
	memoryTotal := int64(out.Data.Info.Memory.Total)
	memoryUsed := int64(out.Data.Info.Memory.Used)
	if memoryUsed == 0 && memoryTotal > 0 {
		available := int64(out.Data.Info.Memory.Available)
		free := int64(out.Data.Info.Memory.Free)
		switch {
		case available > 0 && available <= memoryTotal:
			memoryUsed = memoryTotal - available
		case free > 0 && free <= memoryTotal:
			memoryUsed = memoryTotal - free
		}
	}
	memoryUsedPct := 0.0
	if memoryTotal > 0 {
		memoryUsedPct = float64(memoryUsed) / float64(memoryTotal) * 100
	}
	return systemDetails{
		MemoryTotalBytes: memoryTotal,
		MemoryUsedBytes:  memoryUsed,
		MemoryUsedPct:    memoryUsedPct,
	}, nil
}

func (c LiveClient) versionDetails(ctx context.Context) (string, error) {
	query := `query SystemVersionDetails {
  info {
    versions { unraid }
  }
}`
	var out struct {
		Data struct {
			Info struct {
				Versions struct {
					Unraid string `json:"unraid"`
				} `json:"versions"`
			} `json:"info"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := c.graphql(ctx, query, &out); err != nil {
		return "", err
	}
	if len(out.Errors) > 0 {
		return "", fmt.Errorf("unraid version graphql error: %s", out.Errors[0].Message)
	}
	return strings.TrimSpace(out.Data.Info.Versions.Unraid), nil
}

func (c LiveClient) notificationDetails(ctx context.Context) (systemDetails, error) {
	query := `query UnraidNotifications {
  notifications {
    overview {
      unread { total alert warning }
    }
  }
}`
	var out struct {
		Data struct {
			Notifications struct {
				Overview struct {
					Unread struct {
						Total   flexInt64 `json:"total"`
						Alert   flexInt64 `json:"alert"`
						Warning flexInt64 `json:"warning"`
					} `json:"unread"`
				} `json:"overview"`
			} `json:"notifications"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := c.graphql(ctx, query, &out); err != nil {
		return systemDetails{}, err
	}
	if len(out.Errors) > 0 {
		return systemDetails{}, fmt.Errorf("unraid notification graphql error: %s", out.Errors[0].Message)
	}
	return systemDetails{
		NotificationCount: int(out.Data.Notifications.Overview.Unread.Total),
		AlertCount:        int(out.Data.Notifications.Overview.Unread.Alert),
		WarningCount:      int(out.Data.Notifications.Overview.Unread.Warning),
	}, nil
}

func (c LiveClient) dockerNetworkDetails(ctx context.Context) (systemDetails, error) {
	query := `query DockerNetworkDetails {
  docker {
    networks { id name }
  }
}`
	var out struct {
		Data struct {
			Docker struct {
				Networks []struct {
					ID   string `json:"id"`
					Name string `json:"name"`
				} `json:"networks"`
			} `json:"docker"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := c.graphql(ctx, query, &out); err != nil {
		return systemDetails{}, err
	}
	if len(out.Errors) > 0 {
		return systemDetails{}, fmt.Errorf("unraid docker networks graphql error: %s", out.Errors[0].Message)
	}
	names := make([]string, 0, len(out.Data.Docker.Networks))
	for _, network := range out.Data.Docker.Networks {
		if name := strings.TrimSpace(network.Name); name != "" {
			names = append(names, name)
		}
	}
	return systemDetails{
		DockerNetworkCount: len(out.Data.Docker.Networks),
		DockerNetworkNames: names,
	}, nil
}

func (c LiveClient) vmDetails(ctx context.Context) (systemDetails, error) {
	query := `query VMDetails {
  vms {
    domains { id name state }
  }
}`
	var out struct {
		Data struct {
			VMs struct {
				Domains []struct {
					ID    string `json:"id"`
					Name  string `json:"name"`
					State string `json:"state"`
				} `json:"domains"`
			} `json:"vms"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := c.graphql(ctx, query, &out); err != nil {
		return systemDetails{}, err
	}
	if len(out.Errors) > 0 {
		return systemDetails{}, fmt.Errorf("unraid vm graphql error: %s", out.Errors[0].Message)
	}
	names := make([]string, 0, len(out.Data.VMs.Domains))
	var running, stopped int
	for _, domain := range out.Data.VMs.Domains {
		if name := strings.TrimSpace(domain.Name); name != "" {
			names = append(names, name)
		}
		switch vmState(domain.State) {
		case "running":
			running++
		case "stopped":
			stopped++
		}
	}
	return systemDetails{
		VMCount:        len(out.Data.VMs.Domains),
		VMRunningCount: running,
		VMStoppedCount: stopped,
		VMNames:        names,
	}, nil
}

func (c LiveClient) shareDetails(ctx context.Context) (systemDetails, error) {
	query := `query ShareDetails {
  shares { name }
}`
	var out struct {
		Data struct {
			Shares []struct {
				Name string `json:"name"`
			} `json:"shares"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := c.graphql(ctx, query, &out); err != nil {
		return systemDetails{}, err
	}
	if len(out.Errors) > 0 {
		return systemDetails{}, fmt.Errorf("unraid share graphql error: %s", out.Errors[0].Message)
	}
	names := make([]string, 0, len(out.Data.Shares))
	for _, share := range out.Data.Shares {
		if name := strings.TrimSpace(share.Name); name != "" {
			names = append(names, name)
		}
	}
	return systemDetails{
		ShareCount: len(out.Data.Shares),
		ShareNames: names,
	}, nil
}

func vmState(state string) string {
	normalized := strings.ToLower(strings.TrimSpace(state))
	switch normalized {
	case "running", "started", "active":
		return "running"
	case "stopped", "shutoff", "shutdown", "off":
		return "stopped"
	default:
		return "unknown"
	}
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
