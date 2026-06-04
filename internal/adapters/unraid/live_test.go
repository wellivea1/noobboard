package unraid

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLiveStatusCollectsArrayCapacityAndWarnings(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("x-api-key"); got != "test-key" {
			t.Fatalf("x-api-key header = %q", got)
		}
		var body struct {
			Query string `json:"query"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(body.Query, "ParityStatus") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"data": {
					"vars": {
						"mdResync": 0,
						"mdResyncPos": "0",
						"mdResyncAction": "check P Q",
						"mdResyncCorr": "0",
						"mdResyncDt": "0",
						"mdResyncDb": "0"
					}
				}
			}`))
			return
		}
		if strings.Contains(body.Query, "SystemCPUDetails") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"data": {
					"info": {
						"cpu": {"manufacturer": "Intel", "brand": "Intel N100", "cores": 4, "threads": 4}
					}
				}
			}`))
			return
		}
		if strings.Contains(body.Query, "SystemMemoryDetails") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"data": {
					"info": {
						"memory": {"total": "16000", "used": "4000", "free": "12000", "available": "11000"}
					}
				}
			}`))
			return
		}
		if strings.Contains(body.Query, "SystemVersionDetails") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"data": {
					"info": {
						"versions": {"unraid": "7.2.1"}
					}
				}
			}`))
			return
		}
		if strings.Contains(body.Query, "UnraidNotifications") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"data": {
					"notifications": {
						"overview": {
							"unread": {"total": 3, "alert": 1, "warning": 2}
						}
					}
				}
			}`))
			return
		}
		if strings.Contains(body.Query, "DockerNetworkDetails") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"data": {
					"docker": {
						"networks": [
							{"id": "bridge-id", "name": "bridge"},
							{"id": "media-id", "name": "media"}
						]
					}
				}
			}`))
			return
		}
		if strings.Contains(body.Query, "VMDetails") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"data": {
					"vms": {
						"domains": [
							{"id": "vm-1", "name": "Windows", "state": "running"},
							{"id": "vm-2", "name": "Linux", "state": "shutoff"}
						]
					}
				}
			}`))
			return
		}
		if strings.Contains(body.Query, "ShareDetails") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"data": {
					"shares": [
						{"name": "media"},
						{"name": "backups"}
					]
				}
			}`))
			return
		}
		if !strings.Contains(body.Query, "capacity") {
			t.Fatalf("query did not request capacity: %s", body.Query)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"data": {
				"info": {"os": {"distro": "Unraid", "release": "7.2.0", "uptime": "12345"}},
				"array": {
					"state": "STARTED",
					"capacity": {"disks": {"free": "100", "used": "900", "total": "1000"}},
					"disks": [
						{"name": "disk1", "status": "DISK_OK", "temp": null, "size": "1000"},
						{"name": "disk2", "status": "DISK_DSBL", "temp": 56, "size": "1000"}
					]
				}
			}
		}`))
	}))
	defer server.Close()

	client := NewLiveClient(server.URL, "test-key")
	client.http = server.Client()
	infra, _, err := client.Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !infra.UnraidAPIReachable || infra.UnraidArrayState != "started" {
		t.Fatalf("unexpected API/array state: %#v", infra)
	}
	if infra.UnraidArrayHealthy {
		t.Fatal("array should not be healthy with disabled/hot disk warnings")
	}
	if infra.ArrayDiskCount != 2 || infra.ArrayDiskWarningCount < 2 {
		t.Fatalf("disk warning counts = %d/%d, warnings=%#v", infra.ArrayDiskCount, infra.ArrayDiskWarningCount, infra.StorageWarnings)
	}
	if infra.ArrayCapacityTotalBytes != 1000 || infra.ArrayCapacityUsedPct != 90 {
		t.Fatalf("capacity = total %d pct %.1f", infra.ArrayCapacityTotalBytes, infra.ArrayCapacityUsedPct)
	}
	if infra.UnraidVersion != "7.2.1" {
		t.Fatalf("unraid version = %q", infra.UnraidVersion)
	}
	if infra.UnraidCPUBrand != "Intel N100" || infra.UnraidCPUCores != 4 || infra.UnraidCPUThreads != 4 {
		t.Fatalf("unraid cpu details = %#v", infra)
	}
	if infra.UnraidMemoryTotalBytes != 16000 || infra.UnraidMemoryUsedBytes != 4000 || infra.UnraidMemoryUsedPct != 25 {
		t.Fatalf("unraid memory details = total %d used %d pct %.1f", infra.UnraidMemoryTotalBytes, infra.UnraidMemoryUsedBytes, infra.UnraidMemoryUsedPct)
	}
	if infra.UnraidNotificationCount != 3 || infra.UnraidAlertCount != 1 || infra.UnraidWarningCount != 2 {
		t.Fatalf("unraid notification counts = %d/%d/%d", infra.UnraidNotificationCount, infra.UnraidAlertCount, infra.UnraidWarningCount)
	}
	if infra.UnraidUptimeSeconds != 12345 {
		t.Fatalf("unraid uptime = %d", infra.UnraidUptimeSeconds)
	}
	if infra.DockerNetworkCount != 2 || len(infra.DockerNetworkNames) != 2 || infra.DockerNetworkNames[1] != "media" {
		t.Fatalf("docker network details = %d %#v", infra.DockerNetworkCount, infra.DockerNetworkNames)
	}
	if infra.UnraidVMCount != 2 || infra.UnraidVMRunningCount != 1 || infra.UnraidVMStoppedCount != 1 || infra.UnraidVMNames[0] != "Windows" {
		t.Fatalf("unraid vm details = count %d running %d stopped %d names %#v", infra.UnraidVMCount, infra.UnraidVMRunningCount, infra.UnraidVMStoppedCount, infra.UnraidVMNames)
	}
	if infra.UnraidShareCount != 2 || len(infra.UnraidShareNames) != 2 || infra.UnraidShareNames[1] != "backups" {
		t.Fatalf("unraid share details = count %d names %#v", infra.UnraidShareCount, infra.UnraidShareNames)
	}
	if infra.ParityCheckState != "idle" {
		t.Fatalf("parity check state = %q", infra.ParityCheckState)
	}
}

func TestLiveStatusKeepsPartialSystemDetailsWhenFieldsAreUnsupported(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Query string `json:"query"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(body.Query, "ParityStatus"):
			_, _ = w.Write([]byte(`{"data":{"vars":{"mdResync":0,"mdResyncPos":"0","mdResyncAction":"","mdResyncCorr":"0","mdResyncDt":"0","mdResyncDb":"0"}}}`))
		case strings.Contains(body.Query, "SystemCPUDetails"):
			_, _ = w.Write([]byte(`{"data":{"info":{"cpu":{"manufacturer":"Intel","brand":"Intel N100","cores":4,"threads":4}}}}`))
		case strings.Contains(body.Query, "UnraidNotifications"):
			_, _ = w.Write([]byte(`{"data":{"notifications":{"overview":{"unread":{"total":2,"alert":1,"warning":1}}}}}`))
		case strings.Contains(body.Query, "SystemMemoryDetails"), strings.Contains(body.Query, "SystemVersionDetails"):
			http.Error(w, "", http.StatusBadRequest)
		case strings.Contains(body.Query, "DockerNetworkDetails"):
			_, _ = w.Write([]byte(`{"data":{"docker":{"networks":[{"id":"bridge-id","name":"bridge"}]}}}`))
		case strings.Contains(body.Query, "VMDetails"):
			_, _ = w.Write([]byte(`{"data":{"vms":{"domains":[{"id":"vm-1","name":"Windows","state":"running"}]}}}`))
		case strings.Contains(body.Query, "ShareDetails"):
			_, _ = w.Write([]byte(`{"data":{"shares":[{"name":"media"}]}}`))
		default:
			_, _ = w.Write([]byte(`{
				"data": {
					"info": {"os": {"distro": "Unraid", "release": "7.2.0", "uptime": "100"}},
					"array": {
						"state": "STARTED",
						"capacity": {"disks": {"free": "100", "used": "100", "total": "200"}},
						"disks": [{"name": "disk1", "status": "DISK_OK", "temp": 30, "size": "1000"}]
					}
				}
			}`))
		}
	}))
	defer server.Close()

	client := NewLiveClient(server.URL, "test-key")
	client.http = server.Client()
	infra, _, err := client.Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if infra.UnraidCPUBrand != "Intel N100" || infra.UnraidCPUCores != 4 || infra.UnraidNotificationCount != 2 {
		t.Fatalf("partial details were not retained: %#v", infra)
	}
	if infra.UnraidMemoryTotalBytes != 0 || infra.UnraidVersion != "Unraid 7.2.0" {
		t.Fatalf("unsupported optional details should not override base status: %#v", infra)
	}
	if infra.DockerNetworkCount != 1 || infra.DockerNetworkNames[0] != "bridge" {
		t.Fatalf("docker network details = %d %#v", infra.DockerNetworkCount, infra.DockerNetworkNames)
	}
	if infra.UnraidVMCount != 1 || infra.UnraidVMRunningCount != 1 || infra.UnraidVMNames[0] != "Windows" {
		t.Fatalf("vm details = %#v", infra)
	}
	if infra.UnraidShareCount != 1 || infra.UnraidShareNames[0] != "media" {
		t.Fatalf("share details = %#v", infra)
	}
}

func TestLiveStatusCollectsParityStateFromSafeVars(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Query string `json:"query"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(body.Query, "ParityStatus") {
			if strings.Contains(body.Query, "mdResyncSize") {
				t.Fatalf("parity query requested unsafe mdResyncSize field: %s", body.Query)
			}
			_, _ = w.Write([]byte(`{
				"data": {
					"vars": {
						"mdResync": 1,
						"mdResyncPos": "125",
						"mdResyncAction": "check",
						"mdResyncCorr": "1",
						"mdResyncDt": "4",
						"mdResyncDb": "100"
					}
				}
			}`))
			return
		}
		if optionalSystemDetailsQuery(body.Query) {
			_, _ = w.Write([]byte(`{"errors":[{"message":"Cannot query field \"notifications\""}]}`))
			return
		}
		_, _ = w.Write([]byte(`{
			"data": {
				"info": {"os": {"distro": "Unraid", "release": "7.2.0"}},
				"array": {
					"state": "STARTED",
					"capacity": {"disks": {"free": "100", "used": "100", "total": "200"}},
					"disks": [{"name": "disk1", "status": "DISK_OK", "temp": 30, "size": "1000"}]
				}
			}
		}`))
	}))
	defer server.Close()

	client := NewLiveClient(server.URL, "test-key")
	client.http = server.Client()
	infra, _, err := client.Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if infra.ParityCheckState != "check correcting" {
		t.Fatalf("parity check state = %q", infra.ParityCheckState)
	}
}

func TestLiveStatusIgnoresParityQueryErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Query string `json:"query"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(body.Query, "ParityStatus") {
			_, _ = w.Write([]byte(`{"errors":[{"message":"Cannot query field"}]}`))
			return
		}
		if optionalSystemDetailsQuery(body.Query) {
			_, _ = w.Write([]byte(`{"errors":[{"message":"Cannot query field"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{
			"data": {
				"info": {"os": {"distro": "Unraid", "release": "7.2.0"}},
				"array": {
					"state": "STARTED",
					"capacity": {"disks": {"free": "100", "used": "100", "total": "200"}},
					"disks": [{"name": "disk1", "status": "DISK_OK", "temp": 30, "size": "1000"}]
				}
			}
		}`))
	}))
	defer server.Close()

	client := NewLiveClient(server.URL, "test-key")
	client.http = server.Client()
	infra, _, err := client.Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if infra.ParityCheckState != "" {
		t.Fatalf("optional parity state should stay empty on query error, got %q", infra.ParityCheckState)
	}
}

func TestLiveStatusFallsBackToWebGUIReachabilityWhenGraphQLFails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/graphql" {
			http.Error(w, "api unavailable", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewLiveClient(server.URL, "test-key")
	client.http = server.Client()
	infra, _, err := client.Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !infra.NASReachable || infra.UnraidAPIReachable {
		t.Fatalf("expected reachable NAS with unavailable API: %#v", infra)
	}
	if !strings.Contains(infra.SourceHealth.Unraid, "web gui reachable") {
		t.Fatalf("source health did not record web GUI fallback: %#v", infra.SourceHealth)
	}
}

func optionalSystemDetailsQuery(query string) bool {
	for _, marker := range []string{
		"SystemCPUDetails",
		"SystemMemoryDetails",
		"SystemVersionDetails",
		"UnraidNotifications",
		"DockerNetworkDetails",
	} {
		if strings.Contains(query, marker) {
			return true
		}
	}
	return false
}
