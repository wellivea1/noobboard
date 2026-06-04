package unifi

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/wellivea1/noobboard/internal/models"
)

type LiveClient struct {
	baseURL             string
	apiKey              string
	siteID              string
	nasClientHint       string
	expectedNASLinkMbps int
	http                *http.Client
}

const unifiMaxPages = 20

type LiveOption func(*LiveClient)

func WithNASLinkMonitoring(clientHint string, expectedMbps int) LiveOption {
	return func(c *LiveClient) {
		c.nasClientHint = strings.TrimSpace(clientHint)
		c.expectedNASLinkMbps = expectedMbps
	}
}

func NewLiveClient(baseURL, apiKey, siteID string, insecureTLS bool, opts ...LiveOption) LiveClient {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if insecureTLS {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	if siteID == "" {
		siteID = "default"
	}
	client := LiveClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		siteID:  siteID,
		http:    &http.Client{Timeout: 10 * time.Second, Transport: transport},
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&client)
		}
	}
	return client
}

func (c LiveClient) Status(ctx context.Context) (models.InfrastructureStatus, error) {
	if c.baseURL == "" || c.apiKey == "" {
		return models.InfrastructureStatus{}, errors.New("unifi base URL and API key are required")
	}
	if err := c.get(ctx, "/proxy/network/integration/v1/info", nil); err != nil {
		return models.InfrastructureStatus{
			UniFiWANUp:            false,
			UniFiGatewayReachable: false,
			LastCheckedAt:         time.Now().UTC(),
			SourceHealth:          models.SourceHealth{UniFi: err.Error()},
		}, nil
	}
	now := time.Now().UTC()
	infra := models.InfrastructureStatus{
		UniFiWANUp:            true,
		UniFiGatewayReachable: true,
		LastCheckedAt:         now,
		SourceHealth:          models.SourceHealth{UniFi: "integration api reachable"},
	}
	site, warnings := c.site(ctx)
	infra.UniFiWarnings = append(infra.UniFiWarnings, warnings...)
	if site.ID != "" {
		infra.UniFiSiteID = site.ID
		infra.UniFiSiteName = site.Name
	}
	if site.ID == "" {
		infra.SourceHealth.UniFi = "integration api reachable; site not discovered"
		return infra, nil
	}

	devices, truncated, err := c.devices(ctx, site.ID)
	if err != nil {
		infra.UniFiWarnings = append(infra.UniFiWarnings, "devices: "+err.Error())
	} else {
		if truncated {
			infra.UniFiWarnings = append(infra.UniFiWarnings, "devices: result may be truncated")
		}
		infra.UniFiDeviceCount = len(devices)
		gatewayKnown := false
		gatewayOnline := false
		for _, device := range devices {
			online := isUniFiOnline(device.State)
			if !online {
				infra.UniFiOfflineDeviceCount++
				infra.UniFiWarnings = append(infra.UniFiWarnings, fmt.Sprintf("%s is %s", firstNonEmpty(device.Name, device.Model, device.ID), firstNonEmpty(device.State, "unknown")))
			}
			if device.FirmwareUpdatable {
				infra.UniFiFirmwareUpdates++
			}
			if isGatewayDevice(device) {
				gatewayKnown = true
				gatewayOnline = gatewayOnline || online
			}
		}
		if gatewayKnown {
			infra.UniFiGatewayReachable = gatewayOnline
			infra.UniFiWANUp = gatewayOnline
		}
	}

	clients, truncated, err := c.clients(ctx, site.ID)
	if err != nil {
		infra.UniFiWarnings = append(infra.UniFiWarnings, "clients: "+err.Error())
	} else {
		if truncated {
			infra.UniFiWarnings = append(infra.UniFiWarnings, "clients: result may be truncated")
		}
		infra.UniFiClientCount = len(clients)
		c.applyNASLinkTelemetry(&infra, clients)
	}

	wans, truncated, err := c.wans(ctx, site.ID)
	if err != nil {
		infra.UniFiWarnings = append(infra.UniFiWarnings, "wans: "+err.Error())
	} else {
		if truncated {
			infra.UniFiWarnings = append(infra.UniFiWarnings, "wans: result may be truncated")
		}
		infra.UniFiWANCount = len(wans)
		applyWANStatus(&infra, wans)
	}
	infra.SourceHealth.UniFi = fmt.Sprintf("site %s; %d device(s), %d client(s), %d WAN definition(s)", firstNonEmpty(site.Name, site.ID), infra.UniFiDeviceCount, infra.UniFiClientCount, infra.UniFiWANCount)
	return infra, nil
}

type siteOverview struct {
	ID                string `json:"id"`
	InternalReference string `json:"internalReference"`
	Name              string `json:"name"`
}

type deviceOverview struct {
	ID                string   `json:"id"`
	Name              string   `json:"name"`
	Model             string   `json:"model"`
	State             string   `json:"state"`
	FirmwareUpdatable bool     `json:"firmwareUpdatable"`
	Features          []string `json:"features"`
	Interfaces        []string `json:"interfaces"`
}

type clientOverview struct {
	ID            string                 `json:"id"`
	Name          string                 `json:"name"`
	IPAddress     string                 `json:"ipAddress"`
	MACAddress    string                 `json:"macAddress"`
	Type          string                 `json:"type"`
	LinkSpeedMbps int                    `json:"linkSpeedMbps"`
	Raw           map[string]interface{} `json:"-"`
}

func (c *clientOverview) UnmarshalJSON(data []byte) error {
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*c = clientOverview{
		ID:            firstStringValue(raw, "id", "_id", "clientId", "client_id"),
		Name:          firstStringValue(raw, "name", "displayName", "display_name", "hostname", "hostName"),
		IPAddress:     firstStringValue(raw, "ipAddress", "ip_address", "ip", "address"),
		MACAddress:    firstStringValue(raw, "macAddress", "mac_address", "mac", "hwaddr"),
		Type:          firstStringValue(raw, "type", "networkType", "network_type"),
		LinkSpeedMbps: firstMbpsValue(raw, "linkSpeedMbps", "link_speed_mbps", "linkSpeed", "link_speed", "speedMbps", "speed_mbps", "speed", "networkSpeed", "network_speed", "wiredRate", "wired_rate", "uplinkSpeed", "uplink_speed", "uplinkLinkSpeed", "uplink_link_speed"),
		Raw:           raw,
	}
	return nil
}

type wanOverview struct {
	ID               string       `json:"id"`
	Name             string       `json:"name"`
	State            string       `json:"state"`
	Status           string       `json:"status"`
	Health           string       `json:"health"`
	LinkState        string       `json:"linkState"`
	ConnectionState  string       `json:"connectionState"`
	OperationalState string       `json:"operationalState"`
	Enabled          optionalBool `json:"enabled"`
	Disabled         optionalBool `json:"disabled"`
	Up               optionalBool `json:"up"`
	Connected        optionalBool `json:"connected"`
	Online           optionalBool `json:"online"`
	Active           optionalBool `json:"active"`
}

func (c LiveClient) site(ctx context.Context) (siteOverview, []string) {
	sites, truncated, err := c.sites(ctx)
	if err != nil {
		return siteOverview{}, []string{"sites: " + err.Error()}
	}
	warnings := []string{}
	if truncated {
		warnings = append(warnings, "sites: result may be truncated")
	}
	if len(sites) == 0 {
		return siteOverview{}, []string{"sites: no sites returned"}
	}
	for _, site := range sites {
		if siteMatches(site, c.siteID) {
			return site, warnings
		}
	}
	return sites[0], append(warnings, fmt.Sprintf("configured site %q not found; using %q", c.siteID, firstNonEmpty(sites[0].Name, sites[0].ID)))
}

func (c LiveClient) sites(ctx context.Context) ([]siteOverview, bool, error) {
	return getPages[siteOverview](ctx, c, "/proxy/network/integration/v1/sites", 100)
}

func (c LiveClient) devices(ctx context.Context, siteID string) ([]deviceOverview, bool, error) {
	return getPages[deviceOverview](ctx, c, "/proxy/network/integration/v1/sites/"+url.PathEscape(siteID)+"/devices", 250)
}

func (c LiveClient) clients(ctx context.Context, siteID string) ([]clientOverview, bool, error) {
	return getPages[clientOverview](ctx, c, "/proxy/network/integration/v1/sites/"+url.PathEscape(siteID)+"/clients", 250)
}

func (c LiveClient) wans(ctx context.Context, siteID string) ([]wanOverview, bool, error) {
	return getPages[wanOverview](ctx, c, "/proxy/network/integration/v1/sites/"+url.PathEscape(siteID)+"/wans", 25)
}

func (c LiveClient) applyNASLinkTelemetry(infra *models.InfrastructureStatus, clients []clientOverview) {
	if c.expectedNASLinkMbps > 0 {
		infra.ExpectedNASLinkMbps = c.expectedNASLinkMbps
	}
	hint := normalizedClientHint(c.nasClientHint)
	if hint == "" {
		return
	}
	for _, client := range clients {
		if !clientMatchesHint(client, hint) {
			continue
		}
		if client.LinkSpeedMbps > 0 {
			infra.NASLinkSpeedMbps = client.LinkSpeedMbps
		}
		return
	}
}

type optionalBool struct {
	Value bool
	Set   bool
}

func (b *optionalBool) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" || trimmed == "null" {
		return nil
	}
	var raw interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	switch value := raw.(type) {
	case bool:
		b.Value = value
	case string:
		parsed, err := strconv.ParseBool(strings.TrimSpace(value))
		if err != nil {
			return err
		}
		b.Value = parsed
	case float64:
		b.Value = value != 0
	default:
		return fmt.Errorf("unsupported boolean value %T", raw)
	}
	b.Set = true
	return nil
}

func (b optionalBool) knownTrue() bool {
	return b.Set && b.Value
}

func (b optionalBool) knownFalse() bool {
	return b.Set && !b.Value
}

func applyWANStatus(infra *models.InfrastructureStatus, wans []wanOverview) {
	if len(wans) == 0 {
		return
	}
	anyUp := false
	anyDown := false
	anyKnown := false
	disabledCount := 0
	downNames := []string{}
	for _, wan := range wans {
		switch wanSignal(wan) {
		case "up":
			anyKnown = true
			anyUp = true
		case "down":
			anyKnown = true
			anyDown = true
			downNames = append(downNames, firstNonEmpty(wan.Name, wan.ID, "WAN"))
		case "disabled":
			anyKnown = true
			disabledCount++
		}
	}
	if anyUp {
		if infra.UniFiGatewayReachable {
			infra.UniFiWANUp = true
		}
		if anyDown {
			infra.UniFiWarnings = append(infra.UniFiWarnings, "WAN issue: "+strings.Join(downNames, ", ")+" reports down")
		}
		return
	}
	if anyDown {
		infra.UniFiWANUp = false
		infra.UniFiWarnings = append(infra.UniFiWarnings, "WAN issue: "+strings.Join(downNames, ", ")+" reports down")
		return
	}
	if anyKnown && disabledCount == len(wans) {
		infra.UniFiWANUp = false
		infra.UniFiWarnings = append(infra.UniFiWarnings, "WAN issue: no enabled WAN definitions reported")
	}
}

func clientMatchesHint(client clientOverview, hint string) bool {
	if hint == "" {
		return false
	}
	values := []string{
		client.ID,
		client.Name,
		client.IPAddress,
		client.MACAddress,
		firstStringValue(client.Raw, "hostname", "hostName", "displayName", "display_name", "localDnsRecord"),
	}
	for _, value := range values {
		normalized := normalizedClientHint(value)
		if normalized == hint || shortClientHint(normalized) == shortClientHint(hint) {
			return true
		}
	}
	return false
}

func normalizedClientHint(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if parsed, err := url.Parse(value); err == nil && parsed.Host != "" {
		value = parsed.Hostname()
	}
	value = strings.Trim(strings.ToLower(value), "[]")
	if host, _, err := net.SplitHostPort(value); err == nil {
		value = host
	}
	return strings.Trim(strings.TrimSpace(value), "[]")
}

func shortClientHint(value string) string {
	value = normalizedClientHint(value)
	if value == "" || net.ParseIP(value) != nil || !strings.Contains(value, ".") {
		return value
	}
	return strings.SplitN(value, ".", 2)[0]
}

func firstStringValue(values map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		if value, ok := values[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func firstMbpsValue(values map[string]interface{}, keys ...string) int {
	for _, key := range keys {
		if mbps := mbpsValue(values[key]); mbps > 0 {
			return mbps
		}
	}
	return 0
}

func mbpsValue(value interface{}) int {
	switch v := value.(type) {
	case float64:
		if v > 0 {
			return int(v)
		}
	case int:
		if v > 0 {
			return v
		}
	case json.Number:
		if parsed, err := strconv.ParseFloat(string(v), 64); err == nil && parsed > 0 {
			return int(parsed)
		}
	case string:
		return parseMbpsText(v)
	}
	return 0
}

func parseMbpsText(value string) int {
	normalized := strings.ToLower(strings.TrimSpace(value))
	if normalized == "" {
		return 0
	}
	multiplier := 1.0
	if strings.Contains(normalized, "gb") || strings.Contains(normalized, "gbit") {
		multiplier = 1000
	}
	replacer := strings.NewReplacer(",", "", "mbps", " ", "mbit/s", " ", "mbit", " ", "mps", " ", "gbps", " ", "gbit/s", " ", "gbit", " ", "gb", " ", "fdx", " ", "hdx", " ")
	parts := strings.Fields(replacer.Replace(normalized))
	for _, part := range parts {
		if number, err := strconv.ParseFloat(part, 64); err == nil && number > 0 {
			return int(number * multiplier)
		}
	}
	return 0
}

func wanSignal(wan wanOverview) string {
	if wan.Disabled.knownTrue() || wan.Enabled.knownFalse() {
		return "disabled"
	}
	if wan.Up.knownFalse() || wan.Connected.knownFalse() || wan.Online.knownFalse() {
		return "down"
	}
	for _, value := range wanStatusText(wan) {
		if isNegativeStatusText(value) {
			return "down"
		}
	}
	if wan.Up.knownTrue() || wan.Connected.knownTrue() || wan.Online.knownTrue() || wan.Active.knownTrue() {
		return "up"
	}
	for _, value := range wanStatusText(wan) {
		if isPositiveStatusText(value) {
			return "up"
		}
	}
	return "unknown"
}

func wanStatusText(wan wanOverview) []string {
	return []string{wan.State, wan.Status, wan.Health, wan.LinkState, wan.ConnectionState, wan.OperationalState}
}

func isNegativeStatusText(value string) bool {
	normalized := normalizeStatusText(value)
	if normalized == "" {
		return false
	}
	for _, marker := range []string{"down", "offline", "disconnected", "disconnect", "failed", "failure", "error", "unavailable", "link down", "no link", "not connected"} {
		if statusHasMarker(normalized, marker) {
			return true
		}
	}
	return false
}

func isPositiveStatusText(value string) bool {
	normalized := normalizeStatusText(value)
	if normalized == "" {
		return false
	}
	for _, marker := range []string{"up", "online", "connected", "ready", "ok", "healthy", "available", "active", "good"} {
		if statusHasMarker(normalized, marker) {
			return true
		}
	}
	return false
}

func statusHasMarker(normalized, marker string) bool {
	if strings.Contains(marker, " ") {
		return normalized == marker || strings.Contains(normalized, marker)
	}
	for _, part := range strings.Fields(normalized) {
		if part == marker {
			return true
		}
	}
	return false
}

func normalizeStatusText(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	for _, old := range []string{"_", "-", ".", "/"} {
		value = strings.ReplaceAll(value, old, " ")
	}
	return strings.Join(strings.Fields(value), " ")
}

func getPages[T any](ctx context.Context, client LiveClient, resourcePath string, limit int) ([]T, bool, error) {
	if limit <= 0 {
		limit = 100
	}
	var all []T
	for page := 0; page < unifiMaxPages; page++ {
		var response struct {
			Data []T `json:"data"`
		}
		path := fmt.Sprintf("%s?offset=%d&limit=%d", resourcePath, page*limit, limit)
		if err := client.get(ctx, path, &response); err != nil {
			return all, false, err
		}
		all = append(all, response.Data...)
		if len(response.Data) < limit {
			return all, false, nil
		}
	}
	return all, true, nil
}

func (c LiveClient) get(ctx context.Context, path string, out interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-API-KEY", c.apiKey)
	req.Header.Set("Accept", "application/json")
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
		return fmt.Errorf("unifi api returned %d", resp.StatusCode)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(data, out)
}

func firstString(values map[string]interface{}, keys ...string) (string, bool) {
	for _, key := range keys {
		if value, ok := values[key].(string); ok && value != "" {
			return value, true
		}
	}
	return "", false
}

func siteMatches(site siteOverview, configured string) bool {
	if configured == "" {
		return false
	}
	return strings.EqualFold(site.ID, configured) ||
		strings.EqualFold(site.InternalReference, configured) ||
		strings.EqualFold(site.Name, configured)
}

func isUniFiOnline(state string) bool {
	switch strings.ToUpper(strings.TrimSpace(state)) {
	case "", "ONLINE", "CONNECTED", "READY":
		return true
	default:
		return false
	}
}

func isGatewayDevice(device deviceOverview) bool {
	value := strings.ToLower(device.Model + " " + device.Name + " " + strings.Join(device.Features, " ") + " " + strings.Join(device.Interfaces, " "))
	for _, marker := range []string{"gateway", "routing", "udm", "uxg", "usg", "ucg"} {
		if strings.Contains(value, marker) {
			return true
		}
	}
	return false
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
