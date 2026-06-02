package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/websocket"
)

type options struct {
	port      int
	debugPort int
	scenario  string
	goExe     string
	edgePath  string
	keep      bool
}

type visualResult struct {
	OK          bool              `json:"ok"`
	Error       string            `json:"error,omitempty"`
	URL         string            `json:"url"`
	Scenario    string            `json:"scenario"`
	Screenshots screenshots       `json:"screenshots"`
	Flags       map[string]flags  `json:"flags,omitempty"`
	Artifacts   map[string]string `json:"artifacts,omitempty"`
}

type screenshots struct {
	DesktopOverview string `json:"desktopOverview"`
	DesktopServer   string `json:"desktopServer"`
	DesktopRouter   string `json:"desktopRouter"`
	DesktopApps     string `json:"desktopApps"`
	DesktopSettings string `json:"desktopSettings"`
	MobileOverview  string `json:"mobileOverview"`
	MobileRouter    string `json:"mobileRouter"`
	MobileApps      string `json:"mobileApps"`
	MobileSettings  string `json:"mobileSettings"`
	MobileUserHome  string `json:"mobileUserHome"`
}

type flags struct {
	DashboardVisible        bool     `json:"dashboardVisible,omitempty"`
	LoginHidden             bool     `json:"loginHidden,omitempty"`
	PageTitle               string   `json:"pageTitle,omitempty"`
	StatusRowCount          int      `json:"statusRowCount,omitempty"`
	OverviewCardCount       int      `json:"overviewCardCount,omitempty"`
	OverviewMoveButtonCount int      `json:"overviewMoveButtonCount,omitempty"`
	OverviewRearrangeReady  bool     `json:"overviewRearrangeReady,omitempty"`
	ServerHealthRowCount    int      `json:"serverHealthRowCount,omitempty"`
	RouterStatusRowCount    int      `json:"routerStatusRowCount,omitempty"`
	AppCardCount            int      `json:"appCardCount,omitempty"`
	AppLogoCount            int      `json:"appLogoCount,omitempty"`
	SettingsEditorCount     int      `json:"settingsEditorCount,omitempty"`
	SettingsControlCount    int      `json:"settingsControlCount,omitempty"`
	SettingsMenuButtonCount int      `json:"settingsMenuButtonCount,omitempty"`
	VisibleSettingsSections int      `json:"visibleSettingsSections,omitempty"`
	UserHomeVisible         bool     `json:"userHomeVisible,omitempty"`
	UserHeroVisible         bool     `json:"userHeroVisible,omitempty"`
	UserStatusCardCount     int      `json:"userStatusCardCount,omitempty"`
	UserAppCardCount        int      `json:"userAppCardCount,omitempty"`
	UserAppLogoCount        int      `json:"userAppLogoCount,omitempty"`
	UserChatVisible         bool     `json:"userChatVisible,omitempty"`
	BannedTermCount         int      `json:"bannedTermCount,omitempty"`
	IconOnlyPrimaryActions  int      `json:"iconOnlyPrimaryActionCount,omitempty"`
	AdminTabsVisible        bool     `json:"adminTabsVisible,omitempty"`
	ViewportFitCover        bool     `json:"viewportFitCover,omitempty"`
	AppleMobileCapable      bool     `json:"appleMobileCapable,omitempty"`
	ManifestIconCount       int      `json:"manifestIconCount,omitempty"`
	SmallTouchTargetCount   int      `json:"smallTouchTargetCount,omitempty"`
	SmallTouchTargets       []string `json:"smallTouchTargets,omitempty"`
	SourcePillText          string   `json:"sourcePillText,omitempty"`
	DetailSectionCount      int      `json:"detailSectionCount,omitempty"`
	MonitorHideButtonCount  int      `json:"monitorHideButtonCount,omitempty"`
	NormalRemoveButtonCount int      `json:"normalRemoveButtonCount,omitempty"`
	MonitorRestoreVisible   bool     `json:"monitorRestoreVisible,omitempty"`
	MonitorHideRestored     bool     `json:"monitorHideRestored,omitempty"`
	BodyHorizontalOverflow  bool     `json:"bodyHorizontalOverflow"`
	ButtonTextOverflow      bool     `json:"buttonTextOverflow"`
	ElementBoundsOverflow   bool     `json:"elementBoundsOverflow"`
	ElementBoundsOffender   string   `json:"elementBoundsOffender,omitempty"`
}

func main() {
	opts := parseFlags()
	result, err := run(opts)
	if err != nil {
		result.OK = false
		result.Error = err.Error()
	}
	_ = json.NewEncoder(os.Stdout).Encode(result)
	if err != nil {
		os.Exit(1)
	}
}

func parseFlags() options {
	opts := options{}
	flag.IntVar(&opts.port, "port", 8791, "dashboard port")
	flag.IntVar(&opts.debugPort, "debug-port", 9232, "Edge DevTools port")
	flag.StringVar(&opts.scenario, "scenario", "single_container_exited", "fixture scenario")
	flag.StringVar(&opts.goExe, "go", defaultGoExe(), "Go executable")
	flag.StringVar(&opts.edgePath, "edge", "", "Microsoft Edge executable")
	flag.BoolVar(&opts.keep, "keep-server", false, "leave the dashboard server running")
	flag.Parse()
	return opts
}

func run(opts options) (visualResult, error) {
	root, err := os.Getwd()
	if err != nil {
		return visualResult{}, err
	}
	cache := filepath.Join(root, ".cache")
	dist := filepath.Join(root, "dist")
	runID := time.Now().Format("20060102-150405")
	if err := os.MkdirAll(cache, 0o755); err != nil {
		return visualResult{}, err
	}
	if err := os.MkdirAll(dist, 0o755); err != nil {
		return visualResult{}, err
	}
	if opts.port >= 65535 {
		return visualResult{}, fmt.Errorf("dashboard port %d leaves no room for compact port", opts.port)
	}

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", opts.port)
	compactPort := opts.port + 1
	result := visualResult{
		URL:      baseURL,
		Scenario: opts.scenario,
		Artifacts: map[string]string{
			"database":  filepath.Join(cache, "visual-check-"+runID+".db.json"),
			"serverLog": filepath.Join(cache, "visual-check-"+runID+".server.log"),
			"edgeLog":   filepath.Join(cache, "visual-check-"+runID+".edge.log"),
		},
	}

	binary := filepath.Join(dist, "visual-check-dashboard.exe")
	if err := runCommand(root, opts.goExe, "build", "-o", binary, ".\\cmd\\dashboard"); err != nil {
		return result, err
	}

	serverLog, err := os.Create(result.Artifacts["serverLog"])
	if err != nil {
		return result, err
	}
	defer serverLog.Close()

	server := exec.Command(binary, "serve")
	server.Dir = root
	server.Env = append(os.Environ(),
		fmt.Sprintf("NOOBBOARD_PORT=%d", opts.port),
		fmt.Sprintf("NOOBBOARD_COMPACT_PORT=%d", compactPort),
		"NOOBBOARD_INTEGRATION_MODE=fixture",
		"NOOBBOARD_DATABASE_PATH="+result.Artifacts["database"],
		"NOOBBOARD_FIXTURE_DIR="+filepath.Join(root, "fixtures"),
		"NOOBBOARD_FIXTURE_SCENARIO="+opts.scenario,
	)
	server.Stdout = serverLog
	server.Stderr = serverLog
	if err := server.Start(); err != nil {
		return result, err
	}
	defer func() {
		if !opts.keep {
			killProcessTree(server.Process)
		}
	}()

	if err := waitHTTP(baseURL+"/healthz", 20*time.Second); err != nil {
		return result, err
	}

	edgePath, err := resolveEdgePath(opts.edgePath)
	if err != nil {
		return result, err
	}
	edgeProfile := filepath.Join(cache, "edge-visual-"+runID)
	if err := os.MkdirAll(edgeProfile, 0o755); err != nil {
		return result, err
	}
	edgeLog, err := os.Create(result.Artifacts["edgeLog"])
	if err != nil {
		return result, err
	}
	defer edgeLog.Close()
	edge := exec.Command(edgePath,
		"--headless=new",
		"--disable-gpu",
		"--disable-gpu-compositing",
		"--disable-gpu-sandbox",
		"--in-process-gpu",
		"--use-gl=swiftshader",
		"--use-angle=swiftshader",
		"--enable-unsafe-swiftshader",
		"--disable-dev-shm-usage",
		"--disable-features=VizDisplayCompositor,UseSkiaRenderer,CanvasOopRasterization",
		"--disable-extensions",
		"--disable-background-networking",
		"--hide-scrollbars",
		"--no-first-run",
		"--remote-allow-origins=*",
		fmt.Sprintf("--remote-debugging-port=%d", opts.debugPort),
		"--user-data-dir="+edgeProfile,
		"about:blank",
	)
	edge.Dir = root
	edge.Stdout = edgeLog
	edge.Stderr = edgeLog
	if err := edge.Start(); err != nil {
		return result, err
	}
	defer killProcessTree(edge.Process)

	if err := waitHTTP(fmt.Sprintf("http://127.0.0.1:%d/json/version", opts.debugPort), 20*time.Second); err != nil {
		return result, err
	}
	wsURL, err := firstPageWebSocket(opts.debugPort)
	if err != nil {
		return result, err
	}
	cdp, err := newCDP(wsURL, opts.debugPort)
	if err != nil {
		return result, err
	}
	defer cdp.close()

	if _, err := cdp.call("Page.enable", nil); err != nil {
		return result, err
	}
	if _, err := cdp.call("Runtime.enable", nil); err != nil {
		return result, err
	}
	if _, err := cdp.call("Emulation.setDeviceMetricsOverride", map[string]any{
		"width":             1440,
		"height":            1000,
		"deviceScaleFactor": 1,
		"mobile":            false,
	}); err != nil {
		return result, err
	}
	if _, err := cdp.call("Page.navigate", map[string]any{"url": baseURL}); err != nil {
		return result, err
	}
	if err := waitDocumentReady(cdp); err != nil {
		return result, err
	}

	overview, err := evalFlags(cdp, loginExpression)
	if err != nil {
		return result, err
	}
	result.Screenshots.DesktopOverview = filepath.Join(cache, "visual-desktop-overview-"+runID+".png")
	if err := captureScreenshot(cdp, result.Screenshots.DesktopOverview); err != nil {
		return result, err
	}

	serverFlags, err := evalFlags(cdp, serverExpression)
	if err != nil {
		return result, err
	}
	result.Screenshots.DesktopServer = filepath.Join(cache, "visual-desktop-server-"+runID+".png")
	if err := captureScreenshot(cdp, result.Screenshots.DesktopServer); err != nil {
		return result, err
	}

	router, err := evalFlags(cdp, routerExpression)
	if err != nil {
		return result, err
	}
	result.Screenshots.DesktopRouter = filepath.Join(cache, "visual-desktop-router-"+runID+".png")
	if err := captureScreenshot(cdp, result.Screenshots.DesktopRouter); err != nil {
		return result, err
	}

	apps, err := evalFlags(cdp, appsExpression)
	if err != nil {
		return result, err
	}
	result.Screenshots.DesktopApps = filepath.Join(cache, "visual-desktop-apps-"+runID+".png")
	if err := captureScreenshot(cdp, result.Screenshots.DesktopApps); err != nil {
		return result, err
	}

	settings, err := evalFlags(cdp, settingsExpression)
	if err != nil {
		return result, err
	}
	result.Screenshots.DesktopSettings = filepath.Join(cache, "visual-desktop-settings-"+runID+".png")
	if err := captureScreenshot(cdp, result.Screenshots.DesktopSettings); err != nil {
		return result, err
	}

	monitorCustomization, err := evalFlags(cdp, monitorCustomizationExpression)
	if err != nil {
		return result, err
	}

	if _, err := cdp.call("Emulation.setDeviceMetricsOverride", map[string]any{
		"width":             390,
		"height":            844,
		"deviceScaleFactor": 2,
		"mobile":            true,
	}); err != nil {
		return result, err
	}
	time.Sleep(500 * time.Millisecond)
	mobileOverview, err := evalFlags(cdp, overviewExpression)
	if err != nil {
		return result, err
	}
	result.Screenshots.MobileOverview = filepath.Join(cache, "visual-mobile-overview-"+runID+".png")
	if err := captureScreenshot(cdp, result.Screenshots.MobileOverview); err != nil {
		return result, err
	}
	mobileRouter, err := evalFlags(cdp, routerExpression)
	if err != nil {
		return result, err
	}
	result.Screenshots.MobileRouter = filepath.Join(cache, "visual-mobile-router-"+runID+".png")
	if err := captureScreenshot(cdp, result.Screenshots.MobileRouter); err != nil {
		return result, err
	}
	mobileApps, err := evalFlags(cdp, appsExpression)
	if err != nil {
		return result, err
	}
	result.Screenshots.MobileApps = filepath.Join(cache, "visual-mobile-apps-"+runID+".png")
	if err := captureScreenshot(cdp, result.Screenshots.MobileApps); err != nil {
		return result, err
	}
	mobileSettings, err := evalFlags(cdp, settingsExpression)
	if err != nil {
		return result, err
	}
	result.Screenshots.MobileSettings = filepath.Join(cache, "visual-mobile-settings-"+runID+".png")
	if err := captureScreenshot(cdp, result.Screenshots.MobileSettings); err != nil {
		return result, err
	}
	if _, err := cdp.call("Network.clearBrowserCookies", nil); err != nil {
		return result, err
	}
	if _, err := cdp.call("Page.navigate", map[string]any{"url": baseURL}); err != nil {
		return result, err
	}
	if err := waitDocumentReady(cdp); err != nil {
		return result, err
	}
	mobileUserHome, err := evalFlags(cdp, generalUserExpression)
	if err != nil {
		return result, err
	}
	result.Screenshots.MobileUserHome = filepath.Join(cache, "visual-mobile-user-home-"+runID+".png")
	if err := captureScreenshot(cdp, result.Screenshots.MobileUserHome); err != nil {
		return result, err
	}

	result.Flags = map[string]flags{
		"overview":       overview,
		"server":         serverFlags,
		"router":         router,
		"apps":           apps,
		"settings":       settings,
		"customization":  monitorCustomization,
		"mobileOverview": mobileOverview,
		"mobileRouter":   mobileRouter,
		"mobileApps":     mobileApps,
		"mobileSettings": mobileSettings,
		"mobileUserHome": mobileUserHome,
	}
	if err := assertVisualFlags(overview, serverFlags, router, apps, settings, monitorCustomization, mobileOverview, mobileRouter, mobileApps, mobileSettings, mobileUserHome); err != nil {
		return result, err
	}

	result.OK = true
	return result, nil
}

func runCommand(dir, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s failed: %w\n%s", name, strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return nil
}

func waitHTTP(url string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	client := http.Client{Timeout: 2 * time.Second}
	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 500 {
				return nil
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	return fmt.Errorf("timed out waiting for %s", url)
}

func firstPageWebSocket(debugPort int) (string, error) {
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/json", debugPort))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var pages []struct {
		Type                 string `json:"type"`
		WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&pages); err != nil {
		return "", err
	}
	for _, page := range pages {
		if page.Type == "page" && page.WebSocketDebuggerURL != "" {
			return page.WebSocketDebuggerURL, nil
		}
	}
	return "", errors.New("no page target exposed a CDP websocket URL")
}

func waitDocumentReady(cdp *cdpClient) error {
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		value, err := evalRaw(cdp, "document.readyState", false)
		if err == nil && strings.Trim(string(value), `"`) == "complete" {
			return nil
		}
		time.Sleep(250 * time.Millisecond)
	}
	return errors.New("timed out waiting for document.readyState complete")
}

func evalFlags(cdp *cdpClient, expression string) (flags, error) {
	value, err := evalRaw(cdp, auditMobileShellExpression+expression, true)
	if err != nil {
		return flags{}, err
	}
	var out flags
	if err := json.Unmarshal(value, &out); err != nil {
		return flags{}, err
	}
	return out, nil
}

func evalRaw(cdp *cdpClient, expression string, awaitPromise bool) (json.RawMessage, error) {
	raw, err := cdp.call("Runtime.evaluate", map[string]any{
		"expression":    expression,
		"awaitPromise":  awaitPromise,
		"returnByValue": true,
	})
	if err != nil {
		return nil, err
	}
	var response struct {
		Result struct {
			Value json.RawMessage `json:"value"`
		} `json:"result"`
		ExceptionDetails *struct {
			Text string `json:"text"`
		} `json:"exceptionDetails"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return nil, err
	}
	if response.ExceptionDetails != nil {
		return nil, fmt.Errorf("javascript evaluation failed: %s", response.ExceptionDetails.Text)
	}
	if len(response.Result.Value) == 0 {
		return nil, errors.New("javascript evaluation returned no value")
	}
	return response.Result.Value, nil
}

func captureScreenshot(cdp *cdpClient, path string) error {
	raw, err := cdp.call("Page.captureScreenshot", map[string]any{
		"format":                "png",
		"fromSurface":           true,
		"captureBeyondViewport": true,
	})
	if err != nil {
		return err
	}
	var response struct {
		Data string `json:"data"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return err
	}
	data, err := base64.StdEncoding.DecodeString(response.Data)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func assertVisualFlags(overview, server, router, apps, settings, customization, mobileOverview, mobileRouter, mobileApps, mobileSettings, mobileUserHome flags) error {
	var failures []string
	if !overview.DashboardVisible {
		failures = append(failures, "dashboard was not visible after login")
	}
	if !overview.LoginHidden {
		failures = append(failures, "login view was still visible after login")
	}
	if overview.OverviewCardCount < 4 {
		failures = append(failures, "too few overview cards rendered")
	}
	if overview.OverviewMoveButtonCount != 0 || mobileOverview.OverviewMoveButtonCount != 0 {
		failures = append(failures, "overview still rendered discrete move buttons")
	}
	if !overview.OverviewRearrangeReady || !mobileOverview.OverviewRearrangeReady {
		failures = append(failures, "overview rearrange mode did not enable draggable cards")
	}
	if server.ServerHealthRowCount < 5 {
		failures = append(failures, "server tab did not render expected server status rows")
	}
	if router.RouterStatusRowCount < 5 {
		failures = append(failures, "router tab did not render expected UniFi/router status rows")
	}
	if apps.AppCardCount < 1 {
		failures = append(failures, "apps tab did not render app cards")
	}
	if apps.AppLogoCount < 1 {
		failures = append(failures, "apps tab did not render app logos")
	}
	if apps.BodyHorizontalOverflow {
		failures = append(failures, "apps tab horizontal overflow detected")
	}
	if apps.ButtonTextOverflow {
		failures = append(failures, "apps tab button text overflow detected")
	}
	if overview.ElementBoundsOverflow || server.ElementBoundsOverflow || router.ElementBoundsOverflow || apps.ElementBoundsOverflow || settings.ElementBoundsOverflow {
		failures = append(failures, "desktop component bounds overflow detected: "+firstNonEmpty(overview.ElementBoundsOffender, server.ElementBoundsOffender, router.ElementBoundsOffender, apps.ElementBoundsOffender, settings.ElementBoundsOffender))
	}
	if settings.SettingsControlCount < 12 {
		failures = append(failures, "structured settings controls did not render")
	}
	if settings.SettingsMenuButtonCount < 7 {
		failures = append(failures, "settings submenu did not render")
	}
	if settings.VisibleSettingsSections != 1 {
		failures = append(failures, "settings should show exactly one active submenu")
	}
	if settings.ButtonTextOverflow {
		failures = append(failures, "desktop button text overflow detected")
	}
	if mobileSettings.SettingsMenuButtonCount < 6 || mobileSettings.VisibleSettingsSections != 1 {
		failures = append(failures, "mobile settings submenu did not render correctly")
	}
	if mobileSettings.BodyHorizontalOverflow || mobileSettings.ButtonTextOverflow {
		failures = append(failures, "mobile settings layout overflow detected")
	}
	if customization.NormalRemoveButtonCount != 0 {
		failures = append(failures, "overview remove controls were visible outside rearrange mode")
	}
	if customization.MonitorHideButtonCount < 1 {
		failures = append(failures, "overview remove controls did not render in rearrange mode")
	}
	if !customization.MonitorRestoreVisible || !customization.MonitorHideRestored {
		failures = append(failures, "monitor hide/restore interaction failed")
	}
	if customization.BodyHorizontalOverflow || customization.ButtonTextOverflow {
		failures = append(failures, "monitor customization controls overflowed")
	}
	if customization.ElementBoundsOverflow {
		failures = append(failures, "monitor customization component bounds overflowed: "+customization.ElementBoundsOffender)
	}
	if mobileOverview.SourcePillText != "Fixture data" {
		failures = append(failures, "fixture source-mode label was not visible")
	}
	if server.DetailSectionCount < 2 || router.DetailSectionCount < 2 || mobileRouter.DetailSectionCount < 2 {
		failures = append(failures, "persistent detail sections did not render")
	}
	if mobileOverview.OverviewCardCount < 4 {
		failures = append(failures, "mobile overview cards did not render")
	}
	if mobileRouter.RouterStatusRowCount < 5 {
		failures = append(failures, "mobile router tab did not render expected status rows")
	}
	if mobileOverview.BodyHorizontalOverflow || mobileRouter.BodyHorizontalOverflow {
		failures = append(failures, "mobile body horizontal overflow detected")
	}
	if mobileOverview.ButtonTextOverflow || mobileRouter.ButtonTextOverflow {
		failures = append(failures, "mobile button text overflow detected")
	}
	if mobileOverview.ElementBoundsOverflow || mobileRouter.ElementBoundsOverflow || mobileApps.ElementBoundsOverflow || mobileSettings.ElementBoundsOverflow {
		failures = append(failures, "mobile component bounds overflow detected: "+firstNonEmpty(mobileOverview.ElementBoundsOffender, mobileRouter.ElementBoundsOffender, mobileApps.ElementBoundsOffender, mobileSettings.ElementBoundsOffender))
	}
	if !mobileOverview.ViewportFitCover || !mobileUserHome.ViewportFitCover {
		failures = append(failures, "mobile viewport-fit=cover metadata missing")
	}
	if !mobileOverview.AppleMobileCapable || !mobileUserHome.AppleMobileCapable {
		failures = append(failures, "iOS standalone web app metadata missing")
	}
	if mobileOverview.ManifestIconCount < 3 || mobileUserHome.ManifestIconCount < 3 {
		failures = append(failures, "PWA manifest icons missing")
	}
	if mobileOverview.SmallTouchTargetCount > 0 || mobileRouter.SmallTouchTargetCount > 0 || mobileApps.SmallTouchTargetCount > 0 || mobileUserHome.SmallTouchTargetCount > 0 {
		failures = append(failures, "mobile touch targets below 44px detected")
	}
	if mobileApps.AppCardCount < 1 {
		failures = append(failures, "mobile apps tab did not render app cards")
	}
	if mobileApps.AppLogoCount < 1 {
		failures = append(failures, "mobile apps tab did not render app logos")
	}
	if mobileApps.BodyHorizontalOverflow {
		failures = append(failures, "mobile apps tab horizontal overflow detected")
	}
	if mobileApps.ButtonTextOverflow {
		failures = append(failures, "mobile apps tab button text overflow detected")
	}
	if !mobileUserHome.UserHomeVisible {
		failures = append(failures, "general user simplified home was not visible")
	}
	if mobileUserHome.AdminTabsVisible {
		failures = append(failures, "general user can see detailed admin tabs")
	}
	if mobileUserHome.PageTitle != "Home status" {
		failures = append(failures, "general user page title was not plain")
	}
	if !mobileUserHome.UserHeroVisible {
		failures = append(failures, "general user hero status did not render")
	}
	if mobileUserHome.BannedTermCount != 0 {
		failures = append(failures, "general user default view leaked technical terms")
	}
	if mobileUserHome.IconOnlyPrimaryActions != 0 {
		failures = append(failures, "general user primary actions lacked visible or accessible labels")
	}
	if mobileUserHome.UserAppCardCount < 1 {
		failures = append(failures, "general user selected app cards did not render")
	}
	if mobileUserHome.UserAppLogoCount < 1 {
		failures = append(failures, "general user app logos did not render")
	}
	if mobileUserHome.BodyHorizontalOverflow {
		failures = append(failures, "general user mobile body horizontal overflow detected")
	}
	if mobileUserHome.ButtonTextOverflow {
		failures = append(failures, "general user mobile button text overflow detected")
	}
	if mobileUserHome.ElementBoundsOverflow {
		failures = append(failures, "general user mobile component bounds overflow detected: "+mobileUserHome.ElementBoundsOffender)
	}
	if len(failures) > 0 {
		return errors.New(strings.Join(failures, "; "))
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return "unknown"
}

type cdpClient struct {
	conn *websocket.Conn
	id   int
}

type cdpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func newCDP(url string, debugPort int) (*cdpClient, error) {
	conn, err := websocket.Dial(url, "", fmt.Sprintf("http://127.0.0.1:%d", debugPort))
	if err != nil {
		return nil, err
	}
	return &cdpClient{conn: conn}, nil
}

func (c *cdpClient) call(method string, params any) (json.RawMessage, error) {
	c.id++
	_ = c.conn.SetDeadline(time.Now().Add(30 * time.Second))
	defer func() {
		_ = c.conn.SetDeadline(time.Time{})
	}()
	request := map[string]any{
		"id":     c.id,
		"method": method,
	}
	if params != nil {
		request["params"] = params
	}
	data, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	if err := websocket.Message.Send(c.conn, string(data)); err != nil {
		return nil, err
	}
	for {
		var message string
		if err := websocket.Message.Receive(c.conn, &message); err != nil {
			return nil, err
		}
		var response struct {
			ID     int             `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *cdpError       `json:"error"`
		}
		if err := json.Unmarshal([]byte(message), &response); err != nil {
			return nil, err
		}
		if response.ID != c.id {
			continue
		}
		if response.Error != nil {
			return nil, fmt.Errorf("%s failed: %s", method, response.Error.Message)
		}
		return response.Result, nil
	}
}

func (c *cdpClient) close() {
	_, _ = c.call("Browser.close", nil)
	_ = c.conn.Close()
}

func resolveEdgePath(requested string) (string, error) {
	if requested != "" && fileExists(requested) {
		return requested, nil
	}
	if runtime.GOOS == "windows" {
		candidates := []string{
			filepath.Join(os.Getenv("ProgramFiles"), "Microsoft", "Edge", "Application", "msedge.exe"),
			filepath.Join(os.Getenv("ProgramFiles(x86)"), "Microsoft", "Edge", "Application", "msedge.exe"),
			filepath.Join(os.Getenv("LOCALAPPDATA"), "Microsoft", "Edge", "Application", "msedge.exe"),
		}
		for _, candidate := range candidates {
			if fileExists(candidate) {
				return candidate, nil
			}
		}
	}
	if path, err := exec.LookPath("msedge"); err == nil {
		return path, nil
	}
	if path, err := exec.LookPath("microsoft-edge"); err == nil {
		return path, nil
	}
	return "", errors.New("Microsoft Edge executable was not found")
}

func fileExists(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func defaultGoExe() string {
	if runtime.GOOS == "windows" {
		if fileExists(`C:\Program Files\Go\bin\go.exe`) {
			return `C:\Program Files\Go\bin\go.exe`
		}
	}
	return "go"
}

func killProcessTree(process *os.Process) {
	if process == nil {
		return
	}
	if runtime.GOOS == "windows" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = exec.CommandContext(ctx, "taskkill", "/PID", strconv.Itoa(process.Pid), "/T", "/F").Run()
		return
	}
	_ = process.Kill()
}

const loginExpression = `(async () => {
  const user = document.querySelector('[name="username"]');
  const pass = document.querySelector('[name="password"]');
  if (user && pass) {
    user.value = 'admin';
    pass.value = 'change-me-now';
    document.querySelector('#login-form').dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
  }
  const visible = () => !!(document.querySelector('#dashboard') && !document.querySelector('#dashboard').hidden && getComputedStyle(document.querySelector('#dashboard')).display !== 'none');
  const started = Date.now();
  while (!visible() && Date.now() - started < 5000) {
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  const rearrange = document.querySelector('#overview-rearrange');
  if (rearrange?.getAttribute('aria-pressed') !== 'true') rearrange?.click();
  await new Promise((resolve) => setTimeout(resolve, 100));
  const overviewRearrangeReady = rearrange?.getAttribute('aria-pressed') === 'true' &&
    [...document.querySelectorAll('#overview-cards .overview-card')].every((card) => card.getAttribute('draggable') === 'true');
  rearrange?.click();
  const buttonTextOverflow = hasButtonTextOverflow();
  return {
    dashboardVisible: visible(),
    loginHidden: !!(document.querySelector('#login') && (document.querySelector('#login').hidden || getComputedStyle(document.querySelector('#login')).display === 'none')),
    pageTitle: document.querySelector('#page-title')?.textContent || '',
    statusRowCount: document.querySelectorAll('.status-list-row').length,
    overviewCardCount: document.querySelectorAll('#overview-cards .overview-card').length,
    overviewMoveButtonCount: document.querySelectorAll('.overview-move').length,
    overviewRearrangeReady,
    sourcePillText: document.querySelector('#source-pill')?.textContent || '',
    bodyHorizontalOverflow: document.documentElement.scrollWidth > window.innerWidth + 2,
    buttonTextOverflow,
    elementBoundsOverflow: componentBoundsOverflow(),
    elementBoundsOffender: componentBoundsOffender()
  };
})()`

const overviewExpression = `(async () => {
  document.querySelector('[data-tab="overview"]')?.click();
  const started = Date.now();
  while (document.querySelectorAll('#overview-cards .overview-card').length < 4 && Date.now() - started < 5000) {
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  const rearrange = document.querySelector('#overview-rearrange');
  if (rearrange?.getAttribute('aria-pressed') !== 'true') rearrange?.click();
  await new Promise((resolve) => setTimeout(resolve, 100));
  const overviewRearrangeReady = rearrange?.getAttribute('aria-pressed') === 'true' &&
    [...document.querySelectorAll('#overview-cards .overview-card')].every((card) => card.getAttribute('draggable') === 'true');
  rearrange?.click();
  const mobileAudit = await auditMobileShell();
  const buttonTextOverflow = hasButtonTextOverflow();
  return {
    pageTitle: document.querySelector('#page-title')?.textContent || '',
    overviewCardCount: document.querySelectorAll('#overview-cards .overview-card').length,
    overviewMoveButtonCount: document.querySelectorAll('.overview-move').length,
    overviewRearrangeReady,
    bodyHorizontalOverflow: document.documentElement.scrollWidth > window.innerWidth + 2,
    buttonTextOverflow,
    ...mobileAudit
  };
})()`

const serverExpression = `(async () => {
  document.querySelector('[data-tab="server"]')?.click();
  const started = Date.now();
  while (document.querySelectorAll('#server-health-grid .status-list-row').length < 5 && Date.now() - started < 5000) {
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  const buttonTextOverflow = hasButtonTextOverflow();
  return {
    pageTitle: document.querySelector('#page-title')?.textContent || '',
    serverHealthRowCount: document.querySelectorAll('#server-health-grid .status-list-row').length,
    detailSectionCount: document.querySelectorAll('#server-detail-grid .detail-section').length,
    bodyHorizontalOverflow: document.documentElement.scrollWidth > window.innerWidth + 2,
    buttonTextOverflow,
    elementBoundsOverflow: componentBoundsOverflow(),
    elementBoundsOffender: componentBoundsOffender()
  };
})()`

const routerExpression = `(async () => {
  document.querySelector('[data-tab="router"]')?.click();
  const started = Date.now();
  while (document.querySelectorAll('#router-status-grid .status-list-row').length < 5 && Date.now() - started < 5000) {
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  const mobileAudit = await auditMobileShell();
  const buttonTextOverflow = hasButtonTextOverflow();
  return {
    pageTitle: document.querySelector('#page-title')?.textContent || '',
    routerStatusRowCount: document.querySelectorAll('#router-status-grid .status-list-row').length,
    detailSectionCount: document.querySelectorAll('#router-detail-grid .detail-section').length,
    bodyHorizontalOverflow: document.documentElement.scrollWidth > window.innerWidth + 2,
    buttonTextOverflow,
    ...mobileAudit
  };
})()`

const appsExpression = `(async () => {
  document.querySelector('[data-tab="apps"]')?.click();
  const started = Date.now();
  while (document.querySelectorAll('#apps .app-card').length < 1 && Date.now() - started < 5000) {
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  const mobileAudit = await auditMobileShell();
  const buttonTextOverflow = hasButtonTextOverflow();
  return {
    pageTitle: document.querySelector('#page-title')?.textContent || '',
    appCardCount: document.querySelectorAll('#apps .app-card').length,
    appLogoCount: document.querySelectorAll('#apps .app-logo').length,
    detailSectionCount: document.querySelectorAll('.detail-section').length,
    bodyHorizontalOverflow: document.documentElement.scrollWidth > window.innerWidth + 2,
    buttonTextOverflow,
    ...mobileAudit
  };
})()`

const monitorCustomizationExpression = `(async () => {
  document.querySelector('[data-tab="overview"]')?.click();
  const started = Date.now();
  while (document.querySelectorAll('#overview-cards .overview-card').length < 4 && Date.now() - started < 5000) {
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  const visibleButtons = (selector) => [...document.querySelectorAll(selector)]
    .filter((element) => element.getClientRects().length && getComputedStyle(element).visibility !== 'hidden' && getComputedStyle(element).display !== 'none');
  const normalRemoveButtonCount = visibleButtons('#overview-cards .monitor-hide').length;
  const rearrange = document.querySelector('#overview-rearrange');
  if (rearrange?.getAttribute('aria-pressed') !== 'true') rearrange?.click();
  const rearrangeStarted = Date.now();
  while (visibleButtons('#overview-cards .monitor-hide').length < 1 && Date.now() - rearrangeStarted < 3000) {
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  const rearrangeRemoveButtonCount = visibleButtons('#overview-cards .monitor-hide').length;
  const initialCount = document.querySelectorAll('#overview-cards .overview-card').length;
  const hide = visibleButtons('#overview-cards .monitor-hide')[0];
  hide?.click();
  const hiddenStarted = Date.now();
  while (document.querySelectorAll('#overview-cards .overview-card').length >= initialCount && Date.now() - hiddenStarted < 3000) {
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  const restore = document.querySelector('#restore-monitors');
  const monitorRestoreVisible = !!(restore && !restore.hidden && getComputedStyle(restore).display !== 'none');
  restore?.click();
  const restoreStarted = Date.now();
  while (document.querySelectorAll('#overview-cards .overview-card').length < initialCount && Date.now() - restoreStarted < 3000) {
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  if (rearrange?.getAttribute('aria-pressed') === 'true') rearrange?.click();
  return {
    monitorHideButtonCount: rearrangeRemoveButtonCount,
    normalRemoveButtonCount,
    monitorRestoreVisible,
    monitorHideRestored: document.querySelectorAll('#overview-cards .overview-card').length === initialCount,
    bodyHorizontalOverflow: document.documentElement.scrollWidth > window.innerWidth + 2,
    buttonTextOverflow: hasButtonTextOverflow(),
    elementBoundsOverflow: componentBoundsOverflow(),
    elementBoundsOffender: componentBoundsOffender()
  };
})()`

const generalUserExpression = `(async () => {
  const loginStarted = Date.now();
  while (!document.querySelector('#login-form') && Date.now() - loginStarted < 5000) {
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  const user = document.querySelector('[name="username"]');
  const pass = document.querySelector('[name="password"]');
  if (user && pass) {
    user.value = 'viewer';
    pass.value = 'change-me-now';
    document.querySelector('#login-form').dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
  }
  const visible = () => !!(document.querySelector('#user-home') && !document.querySelector('#user-home').hidden && getComputedStyle(document.querySelector('#user-home')).display !== 'none');
  const started = Date.now();
  while (!visible() && Date.now() - started < 5000) {
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  const buttonTextOverflow = hasButtonTextOverflow();
  const adminTabsVisible = !!(document.querySelector('#tabs') && !document.querySelector('#tabs').hidden && getComputedStyle(document.querySelector('#tabs')).display !== 'none');
  const chat = document.querySelector('#user-chat-input');
  const chatVisible = !!(chat && chat.getClientRects().length && getComputedStyle(chat).visibility !== 'hidden' && getComputedStyle(chat).display !== 'none');
  const hero = document.querySelector('#user-hero');
  const userHeroVisible = !!(hero && visibleElement(hero) && hero.textContent.trim());
  const visibleText = visibleCompactText(document.querySelector('#user-home'));
  const bannedMatches = visibleText.match(/\b(container|docker|unraid|array|parity|endpoint|graphql|probe|wan|lan|api|ssh|telemetry|smart|syslog|filesystem|cache pool|gateway|https|dns|unifi)\b/gi) || [];
  const primaryActions = [...document.querySelectorAll('#user-primary-actions button, body.compact-view .topbar-actions button.command')]
    .filter((element) => visibleElement(element));
  const iconOnlyPrimaryActionCount = primaryActions.filter((element) => {
    const text = (element.textContent || '').trim();
    const aria = (element.getAttribute('aria-label') || element.getAttribute('title') || '').trim();
    if (element.closest('#user-primary-actions')) {
      return !text || getComputedStyle(element).fontSize === '0px';
    }
    return !text && !aria;
  }).length;
  const mobileAudit = await auditMobileShell();
  return {
    pageTitle: document.querySelector('#page-title')?.textContent || '',
    userHomeVisible: visible(),
    userHeroVisible,
    userStatusCardCount: document.querySelectorAll('#user-status-grid .user-status-card').length,
    userAppCardCount: document.querySelectorAll('#user-apps .user-app-card').length,
    userAppLogoCount: document.querySelectorAll('#user-apps .app-logo').length,
    userChatVisible: chatVisible,
    bannedTermCount: bannedMatches.length,
    iconOnlyPrimaryActionCount,
    adminTabsVisible,
    sourcePillText: document.querySelector('#source-pill')?.textContent || '',
    bodyHorizontalOverflow: document.documentElement.scrollWidth > window.innerWidth + 2,
    buttonTextOverflow,
    ...mobileAudit
  };
})()`

const auditMobileShellExpression = `async function auditMobileShell() {
  const visibleControls = [...document.querySelectorAll('button,input,textarea')]
    .filter((element) => element.getClientRects().length && getComputedStyle(element).visibility !== 'hidden' && getComputedStyle(element).display !== 'none');
  const smallTouchTargets = visibleControls.filter((element) => {
    const rect = element.getBoundingClientRect();
    return Math.round(rect.width) < 44 || Math.round(rect.height) < 44;
  }).map((element) => {
    const rect = element.getBoundingClientRect();
    return [
      element.tagName.toLowerCase(),
      element.id ? '#' + element.id : '',
      element.className ? '.' + String(element.className).trim().replace(/\s+/g, '.') : '',
      element.getAttribute('aria-label') || element.textContent.trim(),
      Math.round(rect.width) + 'x' + Math.round(rect.height)
    ].filter(Boolean).join(' ');
  });
  const manifest = await fetch('/manifest.json', { cache: 'no-store' }).then((response) => response.json()).catch(() => ({}));
  return {
    viewportFitCover: (document.querySelector('meta[name="viewport"]')?.content || '').includes('viewport-fit=cover'),
    appleMobileCapable: document.querySelector('meta[name="apple-mobile-web-app-capable"]')?.content === 'yes',
    manifestIconCount: Array.isArray(manifest.icons) ? manifest.icons.length : 0,
    smallTouchTargetCount: smallTouchTargets.length,
    smallTouchTargets,
    sourcePillText: document.querySelector('#source-pill')?.textContent || '',
    detailSectionCount: document.querySelectorAll('.detail-section').length,
    elementBoundsOverflow: componentBoundsOverflow(),
    elementBoundsOffender: componentBoundsOffender()
  };
}

function visibleElement(element) {
  const style = getComputedStyle(element);
  return element.getClientRects().length > 0 && style.visibility !== 'hidden' && style.display !== 'none';
}

function visibleCompactText(root) {
  if (!root) return '';
  const parts = [];
  const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT, {
    acceptNode(textNode) {
      const parent = textNode.parentElement;
      if (!parent) return NodeFilter.FILTER_REJECT;
      if (parent.closest('details[data-technical]:not([open])')) return NodeFilter.FILTER_REJECT;
      if (!visibleElement(parent)) return NodeFilter.FILTER_REJECT;
      return NodeFilter.FILTER_ACCEPT;
    }
  });
  while (walker.nextNode()) {
    const text = walker.currentNode.nodeValue.trim();
    if (text) parts.push(text);
  }
  return parts.join(' ');
}

function hasButtonTextOverflow() {
  return [...document.querySelectorAll('button')].some((element) => {
    if (!visibleElement(element)) return false;
    const style = getComputedStyle(element);
    const iconOnly = style.fontSize === '0px' ||
      element.classList.contains('monitor-hide') ||
      element.classList.contains('app-control');
    if (iconOnly) return false;
    return element.scrollWidth > element.clientWidth + 1;
  });
}

function componentBoundsOverflow() {
  return componentBoundsOffender() !== '';
}

function componentBoundsOffender() {
  const parents = [...document.querySelectorAll('.overview-card,.status-list-row,.detail-section,.user-status-card,.user-app-card,.app-card,.role-detail,.role-nav-item,.user-editor,.settings-card,.fact-row,.incident-card')];
  for (const parent of parents) {
    if (!visibleElement(parent)) continue;
    const parentRect = parent.getBoundingClientRect();
    if (parentRect.width <= 0 || parentRect.height <= 0) continue;
    for (const child of [...parent.children]) {
      if (parent.tagName === 'DETAILS' && !parent.open && child.tagName !== 'SUMMARY') continue;
      if (!visibleElement(child)) continue;
      const rect = child.getBoundingClientRect();
      if (rect.width <= 0 || rect.height <= 0) continue;
      const overflow = rect.left < parentRect.left - 1 ||
        rect.right > parentRect.right + 1 ||
        rect.top < parentRect.top - 1 ||
        rect.bottom > parentRect.bottom + 1;
      if (overflow) {
        return (parent.className || parent.tagName) + ' > ' + (child.className || child.tagName);
      }
    }
  }
  return '';
}
`

const settingsExpression = `(async () => {
  document.querySelector('[data-tab="settings"]')?.click();
  const started = Date.now();
  while (document.querySelectorAll('.settings-card input,.settings-card select,.settings-card button').length < 12 && Date.now() - started < 5000) {
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  const visibleSettingsSections = [...document.querySelectorAll('#tab-settings .settings-section')]
    .filter((element) => !element.hidden && getComputedStyle(element).display !== 'none').length;
  const settingsControlCount = document.querySelectorAll('.settings-card input,.settings-card select,.settings-card button').length;
  const buttonTextOverflow = hasButtonTextOverflow();
  return {
    pageTitle: document.querySelector('#page-title')?.textContent || '',
    settingsEditorCount: document.querySelectorAll('.settings-editor').length,
    settingsControlCount,
    settingsMenuButtonCount: document.querySelectorAll('#settings-menu [data-settings-section]').length,
    visibleSettingsSections,
    bodyHorizontalOverflow: document.documentElement.scrollWidth > window.innerWidth + 2,
    buttonTextOverflow,
    elementBoundsOverflow: componentBoundsOverflow(),
    elementBoundsOffender: componentBoundsOffender()
  };
})()`
