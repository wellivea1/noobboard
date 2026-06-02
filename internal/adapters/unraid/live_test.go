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
		if !strings.Contains(body.Query, "capacity") {
			t.Fatalf("query did not request capacity: %s", body.Query)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"data": {
				"info": {"os": {"distro": "Unraid", "release": "7.2.0"}},
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
	if infra.UnraidVersion != "Unraid 7.2.0" {
		t.Fatalf("unraid version = %q", infra.UnraidVersion)
	}
	if infra.ParityCheckState != "idle" {
		t.Fatalf("parity check state = %q", infra.ParityCheckState)
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
