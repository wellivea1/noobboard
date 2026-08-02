package unifi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLiveStatusCollectsSitesDevicesClientsAndWANs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-API-KEY"); got != "test-key" {
			t.Fatalf("X-API-KEY header = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/proxy/network/integration/v1/info":
			_, _ = w.Write([]byte(`{"application":"network"}`))
		case "/proxy/network/integration/v1/sites":
			_, _ = w.Write([]byte(`{"data":[{"id":"site-1","internalReference":"default","name":"Home"}]}`))
		case "/proxy/network/integration/v1/sites/site-1/devices":
			_, _ = w.Write([]byte(`{"data":[
				{"id":"gateway-1","name":"Dream Machine","model":"UDM","state":"ONLINE","firmwareUpdatable":true,"features":["gateway"]},
				{"id":"ap-1","name":"Office AP","model":"U7","state":"OFFLINE","firmwareUpdatable":false,"features":["wifi"]}
			]}`))
		case "/proxy/network/integration/v1/sites/site-1/clients":
			_, _ = w.Write([]byte(`{"data":[{"id":"client-1","name":"NAS","ipAddress":"192.168.0.214","type":"WIRED"}]}`))
		case "/proxy/network/integration/v1/sites/site-1/wans":
			_, _ = w.Write([]byte(`{"data":[{"id":"wan-1","name":"Primary"}]}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client := NewLiveClient(server.URL, "test-key", "default", false)
	client.http = server.Client()
	infra, err := client.Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if infra.UniFiSiteID != "site-1" || infra.UniFiSiteName != "Home" {
		t.Fatalf("site = %q/%q", infra.UniFiSiteID, infra.UniFiSiteName)
	}
	if infra.UniFiDeviceCount != 2 || infra.UniFiOfflineDeviceCount != 1 || infra.UniFiFirmwareUpdates != 1 {
		t.Fatalf("device telemetry = %#v", infra)
	}
	if !infra.UniFiGatewayReachable || !infra.UniFiWANUp {
		t.Fatalf("gateway/WAN should stay online with online gateway: %#v", infra)
	}
	if infra.UniFiClientCount != 1 || infra.UniFiWANCount != 1 {
		t.Fatalf("client/WAN counts = %d/%d", infra.UniFiClientCount, infra.UniFiWANCount)
	}
	if len(infra.UniFiWarnings) == 0 {
		t.Fatal("expected offline device warning")
	}
}

func TestLiveStatusUsesExplicitWANDownState(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/proxy/network/integration/v1/info":
			_, _ = w.Write([]byte(`{"application":"network"}`))
		case "/proxy/network/integration/v1/sites":
			_, _ = w.Write([]byte(`{"data":[{"id":"site-1","internalReference":"default","name":"Home"}]}`))
		case "/proxy/network/integration/v1/sites/site-1/devices":
			_, _ = w.Write([]byte(`{"data":[{"id":"gateway-1","name":"Dream Machine","model":"UDM","state":"ONLINE","features":["gateway"]}]}`))
		case "/proxy/network/integration/v1/sites/site-1/clients":
			_, _ = w.Write([]byte(`{"data":[]}`))
		case "/proxy/network/integration/v1/sites/site-1/wans":
			_, _ = w.Write([]byte(`{"data":[{"id":"wan-1","name":"Primary","state":"DISCONNECTED"}]}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client := NewLiveClient(server.URL, "test-key", "default", false)
	client.http = server.Client()
	infra, err := client.Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !infra.UniFiGatewayReachable {
		t.Fatalf("gateway should remain reachable: %#v", infra)
	}
	if infra.UniFiWANUp {
		t.Fatalf("WAN should be down when WAN endpoint reports DISCONNECTED: %#v", infra)
	}
	if !containsString(infra.UniFiWarnings, "Primary") {
		t.Fatalf("WAN warning should name the down WAN: %#v", infra.UniFiWarnings)
	}
}

func TestLiveStatusKeepsWANUpWhenAnotherWANReportsUp(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/proxy/network/integration/v1/info":
			_, _ = w.Write([]byte(`{"application":"network"}`))
		case "/proxy/network/integration/v1/sites":
			_, _ = w.Write([]byte(`{"data":[{"id":"site-1","internalReference":"default","name":"Home"}]}`))
		case "/proxy/network/integration/v1/sites/site-1/devices":
			_, _ = w.Write([]byte(`{"data":[{"id":"gateway-1","name":"Dream Machine","model":"UDM","state":"ONLINE","features":["gateway"]}]}`))
		case "/proxy/network/integration/v1/sites/site-1/clients":
			_, _ = w.Write([]byte(`{"data":[]}`))
		case "/proxy/network/integration/v1/sites/site-1/wans":
			_, _ = w.Write([]byte(`{"data":[
				{"id":"wan-1","name":"Primary","status":"failed"},
				{"id":"wan-2","name":"Cell Backup","connected":true}
			]}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client := NewLiveClient(server.URL, "test-key", "default", false)
	client.http = server.Client()
	infra, err := client.Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !infra.UniFiWANUp {
		t.Fatalf("WAN should stay up when another WAN reports connected: %#v", infra)
	}
	if !containsString(infra.UniFiWarnings, "Primary") {
		t.Fatalf("expected warning for failed primary WAN: %#v", infra.UniFiWarnings)
	}
}

func TestLiveStatusReportsNASLinkSpeedFromClientHint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/proxy/network/integration/v1/info":
			_, _ = w.Write([]byte(`{"application":"network"}`))
		case "/proxy/network/integration/v1/sites":
			_, _ = w.Write([]byte(`{"data":[{"id":"site-1","internalReference":"default","name":"Home"}]}`))
		case "/proxy/network/integration/v1/sites/site-1/devices":
			_, _ = w.Write([]byte(`{"data":[{"id":"gateway-1","name":"Dream Machine","model":"UDM","state":"ONLINE","features":["gateway"]}]}`))
		case "/proxy/network/integration/v1/sites/site-1/clients":
			_, _ = w.Write([]byte(`{"data":[
				{"id":"client-1","name":"Laptop","ipAddress":"192.168.0.20","type":"WIRED","linkSpeedMbps":1000},
				{"id":"client-2","name":"Tower","ipAddress":"192.168.0.214","macAddress":"aa:bb:cc:dd:ee:ff","type":"WIRED","linkSpeed":"100 Mbps"}
			]}`))
		case "/proxy/network/integration/v1/sites/site-1/wans":
			_, _ = w.Write([]byte(`{"data":[]}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client := NewLiveClient(server.URL, "test-key", "default", false, WithNASLinkMonitoring("http://192.168.0.214", 1000))
	client.http = server.Client()
	infra, err := client.Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if infra.NASLinkSpeedMbps != 100 || infra.ExpectedNASLinkMbps != 1000 {
		t.Fatalf("NAS link telemetry = %d/%d, want 100/1000", infra.NASLinkSpeedMbps, infra.ExpectedNASLinkMbps)
	}
}

func TestLiveStatusParsesNASGigabitTextSpeed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/proxy/network/integration/v1/info":
			_, _ = w.Write([]byte(`{"application":"network"}`))
		case "/proxy/network/integration/v1/sites":
			_, _ = w.Write([]byte(`{"data":[{"id":"site-1","internalReference":"default","name":"Home"}]}`))
		case "/proxy/network/integration/v1/sites/site-1/devices":
			_, _ = w.Write([]byte(`{"data":[{"id":"gateway-1","name":"Dream Machine","model":"UDM","state":"ONLINE","features":["gateway"]}]}`))
		case "/proxy/network/integration/v1/sites/site-1/clients":
			_, _ = w.Write([]byte(`{"data":[{"id":"client-1","name":"Tower","ipAddress":"192.168.0.214","type":"WIRED","speed":"1 Gbps"}]}`))
		case "/proxy/network/integration/v1/sites/site-1/wans":
			_, _ = w.Write([]byte(`{"data":[]}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client := NewLiveClient(server.URL, "test-key", "default", false, WithNASLinkMonitoring("tower.local", 2500))
	client.http = server.Client()
	infra, err := client.Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if infra.NASLinkSpeedMbps != 1000 || infra.ExpectedNASLinkMbps != 2500 {
		t.Fatalf("NAS link telemetry = %d/%d, want 1000/2500", infra.NASLinkSpeedMbps, infra.ExpectedNASLinkMbps)
	}
}

func TestWANStatusTextDoesNotTreatBackupAsUp(t *testing.T) {
	if got := wanSignal(wanOverview{Status: "backup"}); got != "unknown" {
		t.Fatalf("wanSignal backup = %q, want unknown", got)
	}
}

func TestLiveStatusPaginatesUniFiResources(t *testing.T) {
	seen := map[string]bool{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-API-KEY"); got != "test-key" {
			t.Fatalf("X-API-KEY header = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/proxy/network/integration/v1/info":
			_, _ = w.Write([]byte(`{"application":"network"}`))
		case "/proxy/network/integration/v1/sites":
			seen["sites:"+r.URL.Query().Get("offset")] = true
			if got := r.URL.Query().Get("limit"); got != "100" {
				t.Fatalf("sites limit = %q", got)
			}
			switch r.URL.Query().Get("offset") {
			case "0":
				writeUniFiJSON(t, w, map[string]interface{}{"data": makeSites(100)})
			case "100":
				writeUniFiJSON(t, w, map[string]interface{}{"data": []siteOverview{{ID: "site-target", InternalReference: "default", Name: "Home"}}})
			default:
				writeUniFiJSON(t, w, map[string]interface{}{"data": []siteOverview{}})
			}
		case "/proxy/network/integration/v1/sites/site-target/devices":
			seen["devices:"+r.URL.Query().Get("offset")] = true
			if got := r.URL.Query().Get("limit"); got != "250" {
				t.Fatalf("devices limit = %q", got)
			}
			switch r.URL.Query().Get("offset") {
			case "0":
				writeUniFiJSON(t, w, map[string]interface{}{"data": makeDevices(250)})
			case "250":
				writeUniFiJSON(t, w, map[string]interface{}{"data": []deviceOverview{
					{ID: "dev-250", Name: "Office AP", Model: "U7", State: "OFFLINE"},
					{ID: "dev-251", Name: "Switch", Model: "USW", State: "ONLINE"},
				}})
			default:
				writeUniFiJSON(t, w, map[string]interface{}{"data": []deviceOverview{}})
			}
		case "/proxy/network/integration/v1/sites/site-target/clients":
			seen["clients:"+r.URL.Query().Get("offset")] = true
			if got := r.URL.Query().Get("limit"); got != "250" {
				t.Fatalf("clients limit = %q", got)
			}
			switch r.URL.Query().Get("offset") {
			case "0":
				writeUniFiJSON(t, w, map[string]interface{}{"data": makeClients(250, 0)})
			case "250":
				writeUniFiJSON(t, w, map[string]interface{}{"data": makeClients(3, 250)})
			default:
				writeUniFiJSON(t, w, map[string]interface{}{"data": []clientOverview{}})
			}
		case "/proxy/network/integration/v1/sites/site-target/wans":
			seen["wans:"+r.URL.Query().Get("offset")] = true
			if got := r.URL.Query().Get("limit"); got != "25" {
				t.Fatalf("wans limit = %q", got)
			}
			switch r.URL.Query().Get("offset") {
			case "0":
				writeUniFiJSON(t, w, map[string]interface{}{"data": makeWANs(25, 0)})
			case "25":
				writeUniFiJSON(t, w, map[string]interface{}{"data": makeWANs(2, 25)})
			default:
				writeUniFiJSON(t, w, map[string]interface{}{"data": []wanOverview{}})
			}
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client := NewLiveClient(server.URL, "test-key", "default", false)
	client.http = server.Client()
	infra, err := client.Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if infra.UniFiSiteID != "site-target" {
		t.Fatalf("site id = %q", infra.UniFiSiteID)
	}
	if infra.UniFiDeviceCount != 252 || infra.UniFiOfflineDeviceCount != 1 || infra.UniFiFirmwareUpdates != 1 {
		t.Fatalf("device telemetry = %#v", infra)
	}
	if infra.UniFiClientCount != 253 || infra.UniFiWANCount != 27 {
		t.Fatalf("client/WAN counts = %d/%d", infra.UniFiClientCount, infra.UniFiWANCount)
	}
	if !infra.UniFiGatewayReachable || !infra.UniFiWANUp {
		t.Fatalf("gateway/WAN should stay online with paginated gateway data: %#v", infra)
	}
	for _, key := range []string{"sites:100", "devices:250", "clients:250", "wans:25"} {
		if !seen[key] {
			t.Fatalf("expected paginated request %q, saw %#v", key, seen)
		}
	}
}

func writeUniFiJSON(t *testing.T, w http.ResponseWriter, value interface{}) {
	t.Helper()
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatal(err)
	}
}

func makeSites(count int) []siteOverview {
	sites := make([]siteOverview, count)
	for i := range sites {
		sites[i] = siteOverview{ID: fmt.Sprintf("site-%d", i), InternalReference: fmt.Sprintf("site-ref-%d", i), Name: fmt.Sprintf("Site %d", i)}
	}
	return sites
}

func makeDevices(count int) []deviceOverview {
	devices := make([]deviceOverview, count)
	for i := range devices {
		devices[i] = deviceOverview{ID: fmt.Sprintf("dev-%d", i), Name: fmt.Sprintf("Device %d", i), Model: "U7", State: "ONLINE"}
	}
	if count > 0 {
		devices[0] = deviceOverview{ID: "gateway-0", Name: "Dream Machine", Model: "UDM", State: "ONLINE", FirmwareUpdatable: true, Features: []string{"gateway"}}
	}
	return devices
}

func makeClients(count, start int) []clientOverview {
	clients := make([]clientOverview, count)
	for i := range clients {
		id := start + i
		clients[i] = clientOverview{ID: fmt.Sprintf("client-%d", id), Name: fmt.Sprintf("Client %d", id), IPAddress: fmt.Sprintf("192.168.1.%d", id%255), Type: "WIRED"}
	}
	return clients
}

func makeWANs(count, start int) []map[string]string {
	wans := make([]map[string]string, count)
	for i := range wans {
		id := start + i
		wans[i] = map[string]string{"id": fmt.Sprintf("wan-%d", id), "name": fmt.Sprintf("WAN %d", id)}
	}
	return wans
}

func containsString(values []string, needle string) bool {
	for _, value := range values {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}

// deviceActionServer serves a site with one online gateway, one offline gateway,
// one offline access point and one online access point, and records any action
// POST it receives.
func deviceActionServer(t *testing.T, onRestart func(deviceID string)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/proxy/network/integration/v1/info":
			_, _ = w.Write([]byte(`{"application":"network"}`))
		case r.URL.Path == "/proxy/network/integration/v1/sites":
			_, _ = w.Write([]byte(`{"data":[{"id":"site-1","internalReference":"default","name":"Home"}]}`))
		case r.URL.Path == "/proxy/network/integration/v1/sites/site-1/devices":
			_, _ = w.Write([]byte(`{"data":[
				{"id":"gw-online","name":"Dream Machine","model":"UDM","state":"ONLINE","features":["gateway"]},
				{"id":"gw-offline","name":"Backup Gateway","model":"UXG","state":"OFFLINE","features":["gateway"]},
				{"id":"ap-offline","name":"Office AP","model":"U7","state":"OFFLINE","features":["wifi"]},
				{"id":"ap-online","name":"Lounge AP","model":"U7","state":"ONLINE","features":["wifi"]}
			]}`))
		case strings.HasSuffix(r.URL.Path, "/actions") && r.Method == http.MethodPost:
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body["action"] != "RESTART" {
				t.Fatalf("action body = %#v, want RESTART", body)
			}
			parts := strings.Split(strings.TrimSuffix(r.URL.Path, "/actions"), "/")
			if onRestart != nil {
				onRestart(parts[len(parts)-1])
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func TestRestartableDevicesOnlyOffersOfflineNonGateways(t *testing.T) {
	// The safety rule in one test. Restarting a device that is still passing
	// traffic can drop the NAS, the dashboard host or the admin's own
	// connection; restarting a gateway takes down the WAN for everyone. Only a
	// device that is already offline and is not a gateway is eligible.
	server := deviceActionServer(t, nil)
	defer server.Close()
	client := NewLiveClient(server.URL, "test-key", "default", false)

	devices, err := client.RestartableDevices(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 1 {
		t.Fatalf("restartable devices = %#v, want exactly the offline access point", devices)
	}
	if devices[0].ID != "ap-offline" {
		t.Fatalf("restartable device = %q, want ap-offline", devices[0].ID)
	}
}

func TestRestartDeviceRefusesEverythingOutsideTheRule(t *testing.T) {
	var restarted []string
	server := deviceActionServer(t, func(id string) { restarted = append(restarted, id) })
	defer server.Close()
	client := NewLiveClient(server.URL, "test-key", "default", false)

	for _, id := range []string{"gw-online", "gw-offline", "ap-online", "does-not-exist"} {
		if _, err := client.RestartDevice(t.Context(), id); !errors.Is(err, ErrDeviceNotRestartable) {
			t.Fatalf("RestartDevice(%q) error = %v, want ErrDeviceNotRestartable", id, err)
		}
	}
	if len(restarted) != 0 {
		t.Fatalf("refused devices were still sent an action: %#v", restarted)
	}
}

func TestRestartDeviceSendsRestartForAnEligibleDevice(t *testing.T) {
	var restarted []string
	server := deviceActionServer(t, func(id string) { restarted = append(restarted, id) })
	defer server.Close()
	client := NewLiveClient(server.URL, "test-key", "default", false)

	result, err := client.RestartDevice(t.Context(), "ap-offline")
	if err != nil {
		t.Fatal(err)
	}
	if !result.Accepted || result.DeviceID != "ap-offline" || result.DeviceName != "Office AP" {
		t.Fatalf("result = %#v", result)
	}
	if len(restarted) != 1 || restarted[0] != "ap-offline" {
		t.Fatalf("restarted = %#v, want one call for ap-offline", restarted)
	}
}

func TestDeviceOnlineDistinguishesOfflineFromUnreachable(t *testing.T) {
	// Verification must never read an unreachable UniFi as a healthy device, so
	// the bool is only meaningful when the error is nil.
	server := deviceActionServer(t, nil)
	client := NewLiveClient(server.URL, "test-key", "default", false)

	online, err := client.DeviceOnline(t.Context(), "ap-online")
	if err != nil || !online {
		t.Fatalf("online device = %t/%v, want true/nil", online, err)
	}
	online, err = client.DeviceOnline(t.Context(), "ap-offline")
	if err != nil || online {
		t.Fatalf("offline device = %t/%v, want false/nil", online, err)
	}

	server.Close()
	if _, err = client.DeviceOnline(t.Context(), "ap-offline"); err == nil {
		t.Fatal("an unreachable UniFi returned no error; verification would read it as a healthy device")
	}
}
