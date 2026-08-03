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
	DesktopOverview        string `json:"desktopOverview"`
	DesktopServer          string `json:"desktopServer"`
	DesktopRouter          string `json:"desktopRouter"`
	DesktopApps            string `json:"desktopApps"`
	DesktopActivity        string `json:"desktopActivity"`
	DesktopDiagnostics     string `json:"desktopDiagnostics"`
	DesktopQueue           string `json:"desktopQueue"`
	DesktopAdvanced        string `json:"desktopAdvanced"`
	DesktopSettings        string `json:"desktopSettings"`
	DesktopAgentRepair     string `json:"desktopAgentRepair"`
	DesktopUserAppDetail   string `json:"desktopUserAppDetail"`
	DesktopUserInfraDetail string `json:"desktopUserInfraDetail"`
	MobileOverview         string `json:"mobileOverview"`
	MobileRouter           string `json:"mobileRouter"`
	MobileApps             string `json:"mobileApps"`
	MobileActivity         string `json:"mobileActivity"`
	MobileDiagnostics      string `json:"mobileDiagnostics"`
	MobileQueue            string `json:"mobileQueue"`
	MobileAdvanced         string `json:"mobileAdvanced"`
	MobileSettings         string `json:"mobileSettings"`
	MobileUserHome         string `json:"mobileUserHome"`
	MobileUserChatKeyboard string `json:"mobileUserChatKeyboard"`
	MobileUserAppDetail    string `json:"mobileUserAppDetail"`
	MobileUserInfraDetail  string `json:"mobileUserInfraDetail"`
	MobileUserDrawer       string `json:"mobileUserDrawer"`
	MobileUserSettings     string `json:"mobileUserSettings"`
}

type flags struct {
	DashboardVisible            bool     `json:"dashboardVisible,omitempty"`
	LoginHidden                 bool     `json:"loginHidden,omitempty"`
	PageTitle                   string   `json:"pageTitle,omitempty"`
	StatusRowCount              int      `json:"statusRowCount,omitempty"`
	OverviewCardCount           int      `json:"overviewCardCount,omitempty"`
	OverviewMoveButtonCount     int      `json:"overviewMoveButtonCount,omitempty"`
	OverviewRearrangeReady      bool     `json:"overviewRearrangeReady,omitempty"`
	PageSubtitle                string   `json:"pageSubtitle,omitempty"`
	ServerHealthRowCount        int      `json:"serverHealthRowCount,omitempty"`
	RouterStatusRowCount        int      `json:"routerStatusRowCount,omitempty"`
	AppCardCount                int      `json:"appCardCount,omitempty"`
	AppDetailCount              int      `json:"appDetailCount,omitempty"`
	AppDetailBodyText           string   `json:"appDetailBodyText,omitempty"`
	AppDetailClearVisible       bool     `json:"appDetailClearVisible,omitempty"`
	DataStoreCount              int      `json:"dataStoreCount,omitempty"`
	DataClearControlCount       int      `json:"dataClearControlCount,omitempty"`
	AppLogoCount                int      `json:"appLogoCount,omitempty"`
	IncidentCardCount           int      `json:"incidentCardCount,omitempty"`
	DiagnosticPanelCount        int      `json:"diagnosticPanelCount,omitempty"`
	ReviewQueueVisible          bool     `json:"reviewQueueVisible,omitempty"`
	ReviewQueueOutputVisible    bool     `json:"reviewQueueOutputVisible,omitempty"`
	ReviewQueueCopyAuditOnly    bool     `json:"reviewQueueCopyAuditOnly,omitempty"`
	ActiveTabCount              int      `json:"activeTabCount,omitempty"`
	ActivityRowCount            int      `json:"activityRowCount,omitempty"`
	ActivityFilterNarrowed      bool     `json:"activityFilterNarrowed,omitempty"`
	ActivitySearchNarrowed      bool     `json:"activitySearchNarrowed,omitempty"`
	RawSnapshotCollapsed        bool     `json:"rawSnapshotCollapsed,omitempty"`
	AssistantOverlapCount       int      `json:"assistantOverlapCount,omitempty"`
	AssistantDockVisible        bool     `json:"assistantDockVisible,omitempty"`
	AssistantPanelUsable        bool     `json:"assistantPanelUsable,omitempty"`
	SettingsEditorCount         int      `json:"settingsEditorCount,omitempty"`
	SettingsControlCount        int      `json:"settingsControlCount,omitempty"`
	SettingsMenuButtonCount     int      `json:"settingsMenuButtonCount,omitempty"`
	SettingsSearchNarrowed      bool     `json:"settingsSearchNarrowed,omitempty"`
	SettingsSearchRestored      bool     `json:"settingsSearchRestored,omitempty"`
	VisibleSettingsSections     int      `json:"visibleSettingsSections,omitempty"`
	SettingsDirtyPromptBlocked  bool     `json:"settingsDirtyPromptBlocked,omitempty"`
	SettingsDirtyCleared        bool     `json:"settingsDirtyCleared,omitempty"`
	AgentRepairToggleCount      int      `json:"agentRepairToggleCount,omitempty"`
	AgentReadinessLimitVisible  bool     `json:"agentReadinessLimitVisible,omitempty"`
	AgentApprovalDialogVisible  bool     `json:"agentApprovalDialogVisible,omitempty"`
	AgentApprovalClosedAfterRun bool     `json:"agentApprovalClosedAfterRun,omitempty"`
	AgentRepairProgressVisible  bool     `json:"agentRepairProgressVisible,omitempty"`
	AgentRepairStatusText       string   `json:"agentRepairStatusText,omitempty"`
	AgentRepairOutcomeVisible   bool     `json:"agentRepairOutcomeVisible,omitempty"`
	AgentRepairOutcomeRecovered bool     `json:"agentRepairOutcomeRecovered,omitempty"`
	AgentApprovalActionVisible  bool     `json:"agentApprovalActionVisible,omitempty"`
	UserHomeVisible             bool     `json:"userHomeVisible,omitempty"`
	UserHeroVisible             bool     `json:"userHeroVisible,omitempty"`
	UserStatusCardCount         int      `json:"userStatusCardCount,omitempty"`
	UserAppCardCount            int      `json:"userAppCardCount,omitempty"`
	UserAppLogoCount            int      `json:"userAppLogoCount,omitempty"`
	UserChatVisible             bool     `json:"userChatVisible,omitempty"`
	UserDetailVisible           bool     `json:"userDetailVisible,omitempty"`
	UserDetailTitle             string   `json:"userDetailTitle,omitempty"`
	UserDetailHistoryVisible    bool     `json:"userDetailHistoryVisible,omitempty"`
	UserDetailEmptyStateVisible bool     `json:"userDetailEmptyStateVisible,omitempty"`
	UserDetailBackReturned      bool     `json:"userDetailBackReturned,omitempty"`
	UserDetailFocusReturned     bool     `json:"userDetailFocusReturned,omitempty"`
	UserRepairActionVisible     bool     `json:"userRepairActionVisible,omitempty"`
	UserRepairDirectActionCount int      `json:"userRepairDirectActionCount,omitempty"`
	UserRepairActionLabel       string   `json:"userRepairActionLabel,omitempty"`
	DetailActionTextWrapped     bool     `json:"detailActionTextWrapped,omitempty"`
	UserPromptTryAgainVisible   bool     `json:"userPromptTryAgainVisible,omitempty"`
	UserPromptInlineSuccess     bool     `json:"userPromptInlineSuccess,omitempty"`
	UserChatKeyboardMode        bool     `json:"userChatKeyboardMode,omitempty"`
	UserChatComposerVisible     bool     `json:"userChatComposerVisible,omitempty"`
	UserChatOutputVisible       bool     `json:"userChatOutputVisible,omitempty"`
	UserChatChromeCollapsed     bool     `json:"userChatChromeCollapsed,omitempty"`
	UserMenuToggleVisible       bool     `json:"userMenuToggleVisible,omitempty"`
	AdminPageTitleVisible       bool     `json:"adminPageTitleVisible,omitempty"`
	UserDrawerOpen              bool     `json:"userDrawerOpen,omitempty"`
	UserDrawerHidden            bool     `json:"userDrawerHidden,omitempty"`
	UserDrawerFocusReturned     bool     `json:"userDrawerFocusReturned,omitempty"`
	UserDrawerBodyUnlocked      bool     `json:"userDrawerBodyUnlocked,omitempty"`
	DrawerBannedTermCount       int      `json:"drawerBannedTermCount,omitempty"`
	DrawerSmallTargetCount      int      `json:"drawerSmallTouchTargetCount,omitempty"`
	DrawerAdminControlCount     int      `json:"drawerAdminControlCount,omitempty"`
	DrawerNotificationRows      int      `json:"drawerNotificationRows,omitempty"`
	DrawerEmptyStateVisible     bool     `json:"drawerEmptyStateVisible,omitempty"`
	DrawerSignOutVisible        bool     `json:"drawerSignOutVisible,omitempty"`
	NotificationSignupVisible   bool     `json:"notificationSignupVisible,omitempty"`
	NotificationSignupTitle     string   `json:"notificationSignupTitle,omitempty"`
	NotificationSignupPrimary   bool     `json:"notificationSignupPrimary,omitempty"`
	BannedTermCount             int      `json:"bannedTermCount,omitempty"`
	IconOnlyPrimaryActions      int      `json:"iconOnlyPrimaryActionCount,omitempty"`
	AdminTabsVisible            bool     `json:"adminTabsVisible,omitempty"`
	ViewportFitCover            bool     `json:"viewportFitCover,omitempty"`
	AppleMobileCapable          bool     `json:"appleMobileCapable,omitempty"`
	ManifestIconCount           int      `json:"manifestIconCount,omitempty"`
	SmallTouchTargetCount       int      `json:"smallTouchTargetCount,omitempty"`
	SmallTouchTargets           []string `json:"smallTouchTargets,omitempty"`
	SourcePillText              string   `json:"sourcePillText,omitempty"`
	DetailSectionCount          int      `json:"detailSectionCount,omitempty"`
	MonitorHideButtonCount      int      `json:"monitorHideButtonCount,omitempty"`
	NormalRemoveButtonCount     int      `json:"normalRemoveButtonCount,omitempty"`
	MonitorRestoreVisible       bool     `json:"monitorRestoreVisible,omitempty"`
	MonitorHideRestored         bool     `json:"monitorHideRestored,omitempty"`
	BodyHorizontalOverflow      bool     `json:"bodyHorizontalOverflow"`
	ButtonTextOverflow          bool     `json:"buttonTextOverflow"`
	ButtonTextOverflowOffender  string   `json:"buttonTextOverflowOffender,omitempty"`
	NoticeTitleOverlap          bool     `json:"noticeTitleOverlap"`
	RawTransportErrorVisible    bool     `json:"rawTransportErrorVisible"`
	ElementBoundsOverflow       bool     `json:"elementBoundsOverflow"`
	ElementBoundsOffender       string   `json:"elementBoundsOffender,omitempty"`
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
			"config":    filepath.Join(cache, "visual-check-"+runID+".config.yaml"),
			"serverLog": filepath.Join(cache, "visual-check-"+runID+".server.log"),
			"edgeLog":   filepath.Join(cache, "visual-check-"+runID+".edge.log"),
		},
	}
	if err := os.WriteFile(result.Artifacts["config"], []byte(strings.Join([]string{
		"llm:",
		"  provider: openai",
		"  openai_auth_method: api_key",
		"  openai_api_key: sk-visual-local",
		"  agent_control_enabled: true",
		"app_catalog:",
		"  general_user_restarts_enabled: true",
		"  general_user_auto_repair_enabled: true",
		"  restart_allowed_general_user: emby",
		"",
	}, "\n")), 0o600); err != nil {
		return result, err
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

	server := exec.Command(binary, "serve", "-config", result.Artifacts["config"])
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

	activity, err := evalFlags(cdp, activityExpression)
	if err != nil {
		return result, err
	}
	result.Screenshots.DesktopActivity = filepath.Join(cache, "visual-desktop-activity-"+runID+".png")
	if err := captureScreenshot(cdp, result.Screenshots.DesktopActivity); err != nil {
		return result, err
	}

	diagnostics, err := evalFlags(cdp, diagnosticsExpression)
	if err != nil {
		return result, err
	}
	result.Screenshots.DesktopDiagnostics = filepath.Join(cache, "visual-desktop-diagnostics-"+runID+".png")
	if err := captureScreenshot(cdp, result.Screenshots.DesktopDiagnostics); err != nil {
		return result, err
	}

	queue, err := evalFlags(cdp, queueExpression)
	if err != nil {
		return result, err
	}
	result.Screenshots.DesktopQueue = filepath.Join(cache, "visual-desktop-queue-"+runID+".png")
	if err := captureScreenshot(cdp, result.Screenshots.DesktopQueue); err != nil {
		return result, err
	}

	advanced, err := evalFlags(cdp, advancedExpression)
	if err != nil {
		return result, err
	}
	result.Screenshots.DesktopAdvanced = filepath.Join(cache, "visual-desktop-advanced-"+runID+".png")
	if err := captureScreenshot(cdp, result.Screenshots.DesktopAdvanced); err != nil {
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

	agentRepair, err := evalFlags(cdp, agentRepairExpression)
	if err != nil {
		return result, err
	}
	result.Screenshots.DesktopAgentRepair = filepath.Join(cache, "visual-desktop-agent-repair-"+runID+".png")
	if err := captureScreenshot(cdp, result.Screenshots.DesktopAgentRepair); err != nil {
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
	// Emulate a touch device so `@media (pointer: coarse)` matches, which is what a real
	// iPhone/Android sees. Without this the viewport is mobile-sized but reports a fine
	// pointer, so coarse-only touch sizing (min 44px) is skipped and small-target checks
	// produce false failures.
	if _, err := cdp.call("Emulation.setTouchEmulationEnabled", map[string]any{
		"enabled":        true,
		"maxTouchPoints": 5,
	}); err != nil {
		return result, err
	}
	if _, err := cdp.call("Emulation.setEmulatedMedia", map[string]any{
		"features": []map[string]any{
			{"name": "pointer", "value": "coarse"},
			{"name": "any-pointer", "value": "coarse"},
		},
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
	mobileActivity, err := evalFlags(cdp, activityExpression)
	if err != nil {
		return result, err
	}
	result.Screenshots.MobileActivity = filepath.Join(cache, "visual-mobile-activity-"+runID+".png")
	if err := captureScreenshot(cdp, result.Screenshots.MobileActivity); err != nil {
		return result, err
	}
	mobileDiagnostics, err := evalFlags(cdp, diagnosticsExpression)
	if err != nil {
		return result, err
	}
	result.Screenshots.MobileDiagnostics = filepath.Join(cache, "visual-mobile-diagnostics-"+runID+".png")
	if err := captureScreenshot(cdp, result.Screenshots.MobileDiagnostics); err != nil {
		return result, err
	}
	mobileQueue, err := evalFlags(cdp, queueExpression)
	if err != nil {
		return result, err
	}
	result.Screenshots.MobileQueue = filepath.Join(cache, "visual-mobile-queue-"+runID+".png")
	if err := captureScreenshot(cdp, result.Screenshots.MobileQueue); err != nil {
		return result, err
	}
	mobileAdvanced, err := evalFlags(cdp, advancedExpression)
	if err != nil {
		return result, err
	}
	result.Screenshots.MobileAdvanced = filepath.Join(cache, "visual-mobile-advanced-"+runID+".png")
	if err := captureScreenshot(cdp, result.Screenshots.MobileAdvanced); err != nil {
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

	if _, err := cdp.call("Emulation.setDeviceMetricsOverride", map[string]any{
		"width":             1440,
		"height":            1000,
		"deviceScaleFactor": 1,
		"mobile":            false,
	}); err != nil {
		return result, err
	}
	if _, err := cdp.call("Emulation.setTouchEmulationEnabled", map[string]any{
		"enabled": false,
	}); err != nil {
		return result, err
	}
	if _, err := cdp.call("Emulation.setEmulatedMedia", map[string]any{
		"features": []map[string]any{
			{"name": "pointer", "value": "fine"},
			{"name": "any-pointer", "value": "fine"},
		},
	}); err != nil {
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
	if _, err := evalFlags(cdp, generalUserExpression); err != nil {
		return result, err
	}
	desktopUserPromptAction, err := evalFlags(cdp, userPromptActionExpression)
	if err != nil {
		return result, err
	}
	desktopUserAppDetail, err := evalFlags(cdp, userAppDetailExpression)
	if err != nil {
		return result, err
	}
	result.Screenshots.DesktopUserAppDetail = filepath.Join(cache, "visual-desktop-user-app-detail-"+runID+".png")
	if err := captureScreenshot(cdp, result.Screenshots.DesktopUserAppDetail); err != nil {
		return result, err
	}
	desktopUserAppBack, err := evalFlags(cdp, userDetailBackExpression)
	if err != nil {
		return result, err
	}
	desktopUserAppDetail.UserDetailBackReturned = desktopUserAppBack.UserDetailBackReturned
	desktopUserAppDetail.UserDetailFocusReturned = desktopUserAppBack.UserDetailFocusReturned
	desktopUserInfraDetail, err := evalFlags(cdp, userInfraDetailExpression)
	if err != nil {
		return result, err
	}
	result.Screenshots.DesktopUserInfraDetail = filepath.Join(cache, "visual-desktop-user-infra-detail-"+runID+".png")
	if err := captureScreenshot(cdp, result.Screenshots.DesktopUserInfraDetail); err != nil {
		return result, err
	}
	desktopUserInfraBack, err := evalFlags(cdp, userDetailBackExpression)
	if err != nil {
		return result, err
	}
	desktopUserInfraDetail.UserDetailBackReturned = desktopUserInfraBack.UserDetailBackReturned
	desktopUserInfraDetail.UserDetailFocusReturned = desktopUserInfraBack.UserDetailFocusReturned

	if _, err := cdp.call("Emulation.setDeviceMetricsOverride", map[string]any{
		"width":             390,
		"height":            844,
		"deviceScaleFactor": 2,
		"mobile":            true,
	}); err != nil {
		return result, err
	}
	if _, err := cdp.call("Emulation.setTouchEmulationEnabled", map[string]any{
		"enabled":        true,
		"maxTouchPoints": 5,
	}); err != nil {
		return result, err
	}
	if _, err := cdp.call("Emulation.setEmulatedMedia", map[string]any{
		"features": []map[string]any{
			{"name": "pointer", "value": "coarse"},
			{"name": "any-pointer", "value": "coarse"},
		},
	}); err != nil {
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
	if _, err := cdp.call("Emulation.setDeviceMetricsOverride", map[string]any{
		"width":             390,
		"height":            430,
		"deviceScaleFactor": 2,
		"mobile":            true,
	}); err != nil {
		return result, err
	}
	mobileUserChatKeyboard, err := evalFlags(cdp, userChatKeyboardExpression)
	if err != nil {
		return result, err
	}
	result.Screenshots.MobileUserChatKeyboard = filepath.Join(cache, "visual-mobile-user-chat-keyboard-"+runID+".png")
	if err := captureScreenshot(cdp, result.Screenshots.MobileUserChatKeyboard); err != nil {
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
	mobileUserAppDetail, err := evalFlags(cdp, userAppDetailExpression)
	if err != nil {
		return result, err
	}
	result.Screenshots.MobileUserAppDetail = filepath.Join(cache, "visual-mobile-user-app-detail-"+runID+".png")
	if err := captureScreenshot(cdp, result.Screenshots.MobileUserAppDetail); err != nil {
		return result, err
	}
	mobileUserAppBack, err := evalFlags(cdp, userDetailBackExpression)
	if err != nil {
		return result, err
	}
	mobileUserAppDetail.UserDetailBackReturned = mobileUserAppBack.UserDetailBackReturned
	mobileUserAppDetail.UserDetailFocusReturned = mobileUserAppBack.UserDetailFocusReturned
	mobileUserInfraDetail, err := evalFlags(cdp, userInfraDetailExpression)
	if err != nil {
		return result, err
	}
	result.Screenshots.MobileUserInfraDetail = filepath.Join(cache, "visual-mobile-user-infra-detail-"+runID+".png")
	if err := captureScreenshot(cdp, result.Screenshots.MobileUserInfraDetail); err != nil {
		return result, err
	}
	mobileUserInfraBack, err := evalFlags(cdp, userDetailBackExpression)
	if err != nil {
		return result, err
	}
	mobileUserInfraDetail.UserDetailBackReturned = mobileUserInfraBack.UserDetailBackReturned
	mobileUserInfraDetail.UserDetailFocusReturned = mobileUserInfraBack.UserDetailFocusReturned
	mobileUserDrawer, err := evalFlags(cdp, userDrawerOpenExpression)
	if err != nil {
		return result, err
	}
	result.Screenshots.MobileUserDrawer = filepath.Join(cache, "visual-mobile-user-drawer-"+runID+".png")
	if err := captureScreenshot(cdp, result.Screenshots.MobileUserDrawer); err != nil {
		return result, err
	}
	result.Screenshots.MobileUserSettings = filepath.Join(cache, "visual-mobile-user-settings-"+runID+".png")
	if err := captureScreenshot(cdp, result.Screenshots.MobileUserSettings); err != nil {
		return result, err
	}
	mobileUserDrawerClose, err := evalFlags(cdp, userDrawerCloseExpression)
	if err != nil {
		return result, err
	}

	result.Flags = map[string]flags{
		"overview":                overview,
		"server":                  serverFlags,
		"router":                  router,
		"apps":                    apps,
		"activity":                activity,
		"diagnostics":             diagnostics,
		"queue":                   queue,
		"advanced":                advanced,
		"settings":                settings,
		"agentRepair":             agentRepair,
		"customization":           monitorCustomization,
		"mobileOverview":          mobileOverview,
		"mobileRouter":            mobileRouter,
		"mobileApps":              mobileApps,
		"mobileActivity":          mobileActivity,
		"mobileDiagnostics":       mobileDiagnostics,
		"mobileQueue":             mobileQueue,
		"mobileAdvanced":          mobileAdvanced,
		"mobileSettings":          mobileSettings,
		"desktopUserPromptAction": desktopUserPromptAction,
		"desktopUserAppDetail":    desktopUserAppDetail,
		"desktopUserInfraDetail":  desktopUserInfraDetail,
		"mobileUserHome":          mobileUserHome,
		"mobileUserChatKeyboard":  mobileUserChatKeyboard,
		"mobileUserAppDetail":     mobileUserAppDetail,
		"mobileUserInfraDetail":   mobileUserInfraDetail,
		"mobileUserDrawer":        mobileUserDrawer,
		"mobileDrawerClose":       mobileUserDrawerClose,
	}
	if err := assertVisualFlags(overview, serverFlags, router, apps, activity, diagnostics, queue, advanced, settings, agentRepair, monitorCustomization, mobileOverview, mobileRouter, mobileApps, mobileActivity, mobileDiagnostics, mobileQueue, mobileAdvanced, mobileSettings, desktopUserPromptAction, desktopUserAppDetail, desktopUserInfraDetail, mobileUserHome, mobileUserChatKeyboard, mobileUserAppDetail, mobileUserInfraDetail, mobileUserDrawer, mobileUserDrawerClose); err != nil {
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

func assertVisualFlags(overview, server, router, apps, activity, diagnostics, queue, advanced, settings, agentRepair, customization, mobileOverview, mobileRouter, mobileApps, mobileActivity, mobileDiagnostics, mobileQueue, mobileAdvanced, mobileSettings, desktopUserPromptAction, desktopUserAppDetail, desktopUserInfraDetail, mobileUserHome, mobileUserChatKeyboard, mobileUserAppDetail, mobileUserInfraDetail, mobileUserDrawer, mobileUserDrawerClose flags) error {
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
	if activity.IncidentCardCount < 1 || mobileActivity.IncidentCardCount < 1 {
		failures = append(failures, "activity stream did not expand an incident into its evidence")
	}
	if diagnostics.DiagnosticPanelCount < 2 || mobileDiagnostics.DiagnosticPanelCount < 2 {
		failures = append(failures, "diagnostics tab did not render diagnosis panels")
	}
	if !queue.ReviewQueueVisible || !mobileQueue.ReviewQueueVisible {
		failures = append(failures, "review queue tab did not render")
	}
	if !queue.ReviewQueueOutputVisible || !mobileQueue.ReviewQueueOutputVisible {
		failures = append(failures, "review queue output did not render")
	}
	if !queue.ReviewQueueCopyAuditOnly || !mobileQueue.ReviewQueueCopyAuditOnly {
		failures = append(failures, "review queue did not explain direct actions are audit-only")
	}
	if queue.ActiveTabCount != 1 || mobileQueue.ActiveTabCount != 1 {
		failures = append(failures, "review queue navigation did not keep exactly one active tab")
	}
	if activity.ActivityRowCount < 2 || mobileActivity.ActivityRowCount < 2 {
		failures = append(failures, "activity stream did not merge incidents and audit entries")
	}
	if !activity.ActivityFilterNarrowed || !mobileActivity.ActivityFilterNarrowed {
		failures = append(failures, "activity kind filter did not narrow the stream")
	}
	if !activity.ActivitySearchNarrowed || !mobileActivity.ActivitySearchNarrowed {
		failures = append(failures, "activity search did not filter and then restore the stream")
	}
	if !advanced.RawSnapshotCollapsed || !mobileAdvanced.RawSnapshotCollapsed {
		failures = append(failures, "raw debug snapshot was not present and collapsed in settings > advanced")
	}
	// The page name is navigation, not content: it must not render beside a
	// permanently visible sidebar, and it must render when the sidebar is a drawer.
	if overview.AdminPageTitleVisible || settings.AdminPageTitleVisible {
		failures = append(failures, "admin page title rendered while the sidebar was permanently visible")
	}
	if !mobileOverview.AdminPageTitleVisible || !mobileSettings.AdminPageTitleVisible {
		failures = append(failures, "admin page title did not render while the sidebar was a drawer")
	}
	if advanced.PageSubtitle == overview.PageSubtitle || settings.PageSubtitle == overview.PageSubtitle || diagnostics.PageSubtitle == overview.PageSubtitle {
		failures = append(failures, "settings/diagnostics accessible description repeated overall status")
	}
	if overview.AssistantOverlapCount > 0 || settings.AssistantOverlapCount > 0 || mobileSettings.AssistantOverlapCount > 0 {
		failures = append(failures, "assistant launcher overlapped interactive controls")
	}
	if !overview.AssistantDockVisible || !settings.AssistantDockVisible {
		failures = append(failures, "assistant launcher was not available with a configured diagnosis provider")
	}
	if !overview.AssistantPanelUsable {
		failures = append(failures, "assistant popout was too narrow or off-screen")
	}
	if apps.BodyHorizontalOverflow {
		failures = append(failures, "apps tab horizontal overflow detected")
	}
	if apps.ButtonTextOverflow {
		failures = append(failures, "apps tab button text overflow detected")
	}
	if overview.ElementBoundsOverflow || server.ElementBoundsOverflow || router.ElementBoundsOverflow || apps.ElementBoundsOverflow || activity.ElementBoundsOverflow || diagnostics.ElementBoundsOverflow || queue.ElementBoundsOverflow || advanced.ElementBoundsOverflow || settings.ElementBoundsOverflow {
		failures = append(failures, "desktop component bounds overflow detected: "+firstNonEmpty(overview.ElementBoundsOffender, server.ElementBoundsOffender, router.ElementBoundsOffender, apps.ElementBoundsOffender, activity.ElementBoundsOffender, diagnostics.ElementBoundsOffender, queue.ElementBoundsOffender, advanced.ElementBoundsOffender, settings.ElementBoundsOffender))
	}
	if settings.SettingsControlCount < 12 {
		failures = append(failures, "structured settings controls did not render")
	}
	if settings.SettingsMenuButtonCount < 8 {
		failures = append(failures, "settings submenu did not render")
	}
	// Every recorded store an admin can see must also be clearable from here.
	if settings.DataStoreCount < 2 {
		failures = append(failures, "data and logs section did not render both stores")
	}
	if settings.DataClearControlCount < settings.DataStoreCount {
		failures = append(failures, "a recorded data store rendered with no way to clear it")
	}
	// The admin surface must not show less per-app detail than the compact one.
	if apps.AppDetailCount < apps.AppCardCount {
		failures = append(failures, "admin app rows are missing the history and logs disclosure")
	}
	// Lowercased before matching: these labels are uppercased by CSS, so innerText
	// returns them in caps.
	detailBody := strings.ToLower(apps.AppDetailBodyText)
	if !strings.Contains(detailBody, "changes, 7 days") || !strings.Contains(detailBody, "restart loop") {
		failures = append(failures, "admin app detail did not show recorded history and the restart-loop count")
	}
	if !apps.AppDetailClearVisible {
		failures = append(failures, "admin app detail did not offer a clear-history control")
	}
	if !settings.SettingsSearchNarrowed || !mobileSettings.SettingsSearchNarrowed {
		failures = append(failures, "settings search did not narrow the section list")
	}
	if !settings.SettingsSearchRestored || !mobileSettings.SettingsSearchRestored {
		failures = append(failures, "settings search did not restore the section list when cleared")
	}
	if settings.VisibleSettingsSections != 1 {
		failures = append(failures, "settings should show exactly one active submenu")
	}
	if !settings.SettingsDirtyPromptBlocked || !settings.SettingsDirtyCleared || !mobileSettings.SettingsDirtyPromptBlocked || !mobileSettings.SettingsDirtyCleared {
		failures = append(failures, "settings dirty-state navigation guard failed")
	}
	if settings.AgentRepairToggleCount == 0 {
		failures = append(failures, "app repair opt-in toggle did not render in settings")
	}
	if !settings.AgentReadinessLimitVisible {
		failures = append(failures, "agent readiness cooldown/rate limit text did not render")
	}
	if settings.ButtonTextOverflow {
		failures = append(failures, "desktop button text overflow detected")
	}
	if !agentRepair.AgentApprovalDialogVisible {
		failures = append(failures, "agent approval dialog did not render")
	}
	if !agentRepair.AgentApprovalClosedAfterRun {
		failures = append(failures, "agent approval dialog did not close after allow")
	}
	if !agentRepair.AgentRepairProgressVisible || !strings.Contains(agentRepair.AgentRepairStatusText, "Running") {
		failures = append(failures, "agent repair progress did not render while fix was running")
	}
	if !agentRepair.AgentRepairOutcomeVisible || !agentRepair.AgentRepairOutcomeRecovered {
		failures = append(failures, "agent repair outcome did not render as recovered")
	}
	if agentRepair.AgentApprovalActionVisible {
		failures = append(failures, "agent repair kept an approval action visible after completion")
	}
	if agentRepair.NoticeTitleOverlap {
		failures = append(failures, "agent repair notice overlapped the title block")
	}
	if agentRepair.BodyHorizontalOverflow || agentRepair.ButtonTextOverflow || agentRepair.ElementBoundsOverflow {
		failures = append(failures, "agent repair UI layout overflow detected: "+agentRepair.ElementBoundsOffender)
	}
	if !desktopUserPromptAction.UserPromptTryAgainVisible || !desktopUserPromptAction.UserPromptInlineSuccess {
		failures = append(failures, "compact chat direct app action did not show persistent try-again success state")
	}
	if mobileSettings.SettingsMenuButtonCount < 7 || mobileSettings.VisibleSettingsSections != 1 {
		failures = append(failures, "mobile settings submenu did not render correctly")
	}
	if mobileSettings.AgentRepairToggleCount == 0 {
		failures = append(failures, "mobile app repair opt-in toggle did not render in settings")
	}
	if !mobileSettings.AgentReadinessLimitVisible {
		failures = append(failures, "mobile agent readiness cooldown/rate limit text did not render")
	}
	if mobileOverview.UserMenuToggleVisible || mobileQueue.UserMenuToggleVisible || mobileAdvanced.UserMenuToggleVisible || mobileSettings.UserMenuToggleVisible {
		failures = append(failures, "compact user menu was visible on admin mobile screens")
	}
	if mobileSettings.BodyHorizontalOverflow || mobileSettings.ButtonTextOverflow {
		failures = append(failures, "mobile settings layout overflow detected: "+firstNonEmpty(mobileSettings.ButtonTextOverflowOffender, "body scroll width"))
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
	if mobileOverview.ElementBoundsOverflow || mobileRouter.ElementBoundsOverflow || mobileApps.ElementBoundsOverflow || mobileActivity.ElementBoundsOverflow || mobileDiagnostics.ElementBoundsOverflow || mobileQueue.ElementBoundsOverflow || mobileAdvanced.ElementBoundsOverflow || mobileSettings.ElementBoundsOverflow {
		failures = append(failures, "mobile component bounds overflow detected: "+firstNonEmpty(mobileOverview.ElementBoundsOffender, mobileRouter.ElementBoundsOffender, mobileApps.ElementBoundsOffender, mobileActivity.ElementBoundsOffender, mobileDiagnostics.ElementBoundsOffender, mobileQueue.ElementBoundsOffender, mobileAdvanced.ElementBoundsOffender, mobileSettings.ElementBoundsOffender))
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
	if !mobileUserHome.UserMenuToggleVisible {
		failures = append(failures, "general user menu trigger was not visible")
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
	if !mobileUserChatKeyboard.UserChatKeyboardMode {
		failures = append(failures, "general user chat keyboard mode did not activate")
	}
	if !mobileUserChatKeyboard.UserChatComposerVisible {
		failures = append(failures, "general user chat composer was not visible in keyboard mode")
	}
	if !mobileUserChatKeyboard.UserChatOutputVisible {
		failures = append(failures, "general user chat answer area was not visible in keyboard mode")
	}
	if !mobileUserChatKeyboard.UserChatChromeCollapsed {
		failures = append(failures, "general user chat header/tabs did not collapse in keyboard mode")
	}
	if mobileUserChatKeyboard.BodyHorizontalOverflow || mobileUserChatKeyboard.ButtonTextOverflow {
		failures = append(failures, "general user chat keyboard layout overflow detected")
	}
	if mobileUserChatKeyboard.ElementBoundsOverflow {
		failures = append(failures, "general user chat keyboard component bounds overflow detected: "+mobileUserChatKeyboard.ElementBoundsOffender)
	}
	for name, detail := range map[string]flags{
		"desktop app detail":   desktopUserAppDetail,
		"desktop infra detail": desktopUserInfraDetail,
		"mobile app detail":    mobileUserAppDetail,
		"mobile infra detail":  mobileUserInfraDetail,
	} {
		if !detail.UserDetailVisible {
			failures = append(failures, name+" did not render")
		}
		if strings.TrimSpace(detail.UserDetailTitle) == "" {
			failures = append(failures, name+" title did not render")
		}
		if !detail.UserDetailHistoryVisible {
			failures = append(failures, name+" history timeline or empty state did not render")
		}
		if detail.BannedTermCount != 0 {
			failures = append(failures, name+" leaked technical terms")
		}
		if detail.BodyHorizontalOverflow || detail.ButtonTextOverflow {
			failures = append(failures, name+" layout overflow detected")
		}
		if detail.DetailActionTextWrapped {
			failures = append(failures, name+" action label wrapped across lines")
		}
		if detail.NoticeTitleOverlap {
			failures = append(failures, name+" notice overlapped the title block")
		}
		if detail.RawTransportErrorVisible {
			failures = append(failures, name+" exposed raw HTTP error wording")
		}
		if detail.ElementBoundsOverflow {
			failures = append(failures, name+" component bounds overflow detected: "+detail.ElementBoundsOffender)
		}
		if !detail.UserDetailBackReturned || !detail.UserDetailFocusReturned {
			failures = append(failures, name+" back control did not return to status with focus restored")
		}
	}
	if mobileUserAppDetail.SmallTouchTargetCount > 0 || mobileUserInfraDetail.SmallTouchTargetCount > 0 {
		failures = append(failures, "mobile detail touch targets below 44px detected")
	}
	if !desktopUserAppDetail.UserRepairActionVisible || !mobileUserAppDetail.UserRepairActionVisible {
		failures = append(failures, "compact app detail repair affordance did not render")
	}
	if desktopUserAppDetail.UserRepairDirectActionCount < 3 || mobileUserAppDetail.UserRepairDirectActionCount < 3 {
		failures = append(failures, "compact app detail did not render Start, Restart, and Stop controls")
	}
	if !mobileUserDrawer.UserDrawerOpen {
		failures = append(failures, "general user settings drawer did not open")
	}
	if mobileUserDrawer.DrawerBannedTermCount != 0 {
		failures = append(failures, "general user settings drawer leaked technical terms")
	}
	if mobileUserDrawer.DrawerSmallTargetCount != 0 {
		failures = append(failures, "general user settings drawer has touch targets below 44px")
	}
	if mobileUserDrawer.DrawerAdminControlCount != 0 {
		failures = append(failures, "general user settings drawer exposed admin controls")
	}
	if mobileUserDrawer.DrawerNotificationRows < 1 && !mobileUserDrawer.DrawerEmptyStateVisible {
		failures = append(failures, "general user settings drawer did not render notification rows or empty state")
	}
	if mobileUserDrawer.DrawerNotificationRows > 0 && (!mobileUserDrawer.NotificationSignupVisible || !mobileUserDrawer.NotificationSignupPrimary || mobileUserDrawer.NotificationSignupTitle != "Turn on alerts?") {
		failures = append(failures, "general user notification toggle did not open the signup dialog")
	}
	if !mobileUserDrawer.DrawerSignOutVisible {
		failures = append(failures, "general user settings drawer did not render sign out")
	}
	if !mobileUserDrawerClose.UserDrawerHidden || !mobileUserDrawerClose.UserDrawerFocusReturned || !mobileUserDrawerClose.UserDrawerBodyUnlocked {
		failures = append(failures, "general user settings drawer did not close cleanly with focus/body restored")
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
  const assistantDock = document.querySelector('#assistant-dock');
  const assistantPanel = document.querySelector('#assistant-panel');
  let assistantPanelUsable = true;
  if (assistantDock && assistantPanel && visibleElement(assistantDock)) {
    const wasHidden = assistantPanel.hidden;
    assistantPanel.hidden = false;
    await new Promise((resolve) => requestAnimationFrame(resolve));
    const panelRect = assistantPanel.getBoundingClientRect();
    assistantPanelUsable = panelRect.width >= 320 && panelRect.left >= 0 && panelRect.right <= window.innerWidth + 1;
    assistantPanel.hidden = wasHidden;
  }
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
    assistantDockVisible: !!assistantDock && visibleElement(assistantDock),
    assistantPanelUsable,
    assistantOverlapCount: assistantControlOverlapCount(),
    bodyHorizontalOverflow: document.documentElement.scrollWidth > window.innerWidth + 2,
    buttonTextOverflow,
    buttonTextOverflowOffender: buttonTextOverflowOffender(),
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
    pageSubtitle: document.querySelector('#summary')?.textContent || '',
    overviewCardCount: document.querySelectorAll('#overview-cards .overview-card').length,
    overviewMoveButtonCount: document.querySelectorAll('.overview-move').length,
    overviewRearrangeReady,
    bodyHorizontalOverflow: document.documentElement.scrollWidth > window.innerWidth + 2,
    buttonTextOverflow,
    buttonTextOverflowOffender: buttonTextOverflowOffender(),
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
    pageSubtitle: document.querySelector('#summary')?.textContent || '',
    serverHealthRowCount: document.querySelectorAll('#server-health-grid .status-list-row').length,
    detailSectionCount: document.querySelectorAll('#server-detail-grid .detail-section').length,
    bodyHorizontalOverflow: document.documentElement.scrollWidth > window.innerWidth + 2,
    buttonTextOverflow,
    buttonTextOverflowOffender: buttonTextOverflowOffender(),
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
    pageSubtitle: document.querySelector('#summary')?.textContent || '',
    routerStatusRowCount: document.querySelectorAll('#router-status-grid .status-list-row').length,
    detailSectionCount: document.querySelectorAll('#router-detail-grid .detail-section').length,
    bodyHorizontalOverflow: document.documentElement.scrollWidth > window.innerWidth + 2,
    buttonTextOverflow,
    buttonTextOverflowOffender: buttonTextOverflowOffender(),
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
  // The admin surface must not carry less per-app information than the compact
  // one. Expand the first card and confirm the recorded history and its clear
  // control are actually there.
  let appDetailBodyText = '';
  let appDetailClearVisible = false;
  const firstDetail = document.querySelector('#apps .app-card .app-detail');
  if (firstDetail) {
    firstDetail.open = true;
    firstDetail.dispatchEvent(new Event('toggle'));
    const detailStarted = Date.now();
    while (!document.querySelector('#apps .app-detail .app-detail-stack') && Date.now() - detailStarted < 4000) {
      await new Promise((resolve) => setTimeout(resolve, 100));
    }
    const stack = document.querySelector('#apps .app-detail .app-detail-stack');
    appDetailBodyText = stack ? stack.innerText : '';
    const clear = document.querySelector('#apps .app-detail .app-detail-head .command');
    appDetailClearVisible = !!clear && visibleElement(clear);
  }
  return {
    pageTitle: document.querySelector('#page-title')?.textContent || '',
    pageSubtitle: document.querySelector('#summary')?.textContent || '',
    appCardCount: document.querySelectorAll('#apps .app-card').length,
    appLogoCount: document.querySelectorAll('#apps .app-logo').length,
    appDetailCount: document.querySelectorAll('#apps .app-card .app-detail').length,
    appDetailBodyText: appDetailBodyText,
    appDetailClearVisible: appDetailClearVisible,
    detailSectionCount: document.querySelectorAll('.detail-section').length,
    bodyHorizontalOverflow: document.documentElement.scrollWidth > window.innerWidth + 2,
    buttonTextOverflow,
    buttonTextOverflowOffender: buttonTextOverflowOffender(),
    ...mobileAudit
  };
})()`

// Activity replaced the separate Incidents and Admin destinations: incidents,
// repair decisions and audit entries are one typed stream, so the checks that
// used to live on two pages are asserted here.
const activityExpression = `(async () => {
  document.querySelector('[data-tab="activity"]')?.click();
  const started = Date.now();
  while (document.querySelectorAll('#activity-stream .activity-row').length < 2 && Date.now() - started < 5000) {
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  const rowCount = () => document.querySelectorAll('#activity-stream .activity-row').length;
  const allCount = rowCount();
  const incidentEntry = [...document.querySelectorAll('#activity-stream .activity-entry.kind-problem')][0];
  incidentEntry?.setAttribute('open', '');
  await new Promise((resolve) => setTimeout(resolve, 60));
  const incidentCardCount = document.querySelectorAll('#activity-stream .incident-card').length;
  incidentEntry?.removeAttribute('open');
  document.querySelector('#activity-filters [data-activity-filter="change"]')?.click();
  await new Promise((resolve) => setTimeout(resolve, 60));
  const changeCount = rowCount();
  document.querySelector('#activity-filters [data-activity-filter="all"]')?.click();
  await new Promise((resolve) => setTimeout(resolve, 60));
  const search = document.querySelector('#activity-search');
  if (search) {
    search.value = 'zzzzznomatch';
    search.dispatchEvent(new Event('input', { bubbles: true }));
  }
  await new Promise((resolve) => setTimeout(resolve, 60));
  const searchFiltered = rowCount() === 0;
  if (search) {
    search.value = '';
    search.dispatchEvent(new Event('input', { bubbles: true }));
  }
  await new Promise((resolve) => setTimeout(resolve, 60));
  const mobileAudit = await auditMobileShell();
  return {
    pageTitle: document.querySelector('#page-title')?.textContent || '',
    pageSubtitle: document.querySelector('#summary')?.textContent || '',
    activityRowCount: allCount,
    activityFilterNarrowed: changeCount > 0 && changeCount < allCount,
    activitySearchNarrowed: searchFiltered && rowCount() === allCount,
    incidentCardCount,
    bodyHorizontalOverflow: document.documentElement.scrollWidth > window.innerWidth + 2,
    buttonTextOverflow: hasButtonTextOverflow(),
    buttonTextOverflowOffender: buttonTextOverflowOffender(),
    ...mobileAudit
  };
})()`

const diagnosticsExpression = `(async () => {
  document.querySelector('[data-tab="diagnostics"]')?.click();
  const started = Date.now();
  while (document.querySelectorAll('#tab-diagnostics .panel').length < 2 && Date.now() - started < 5000) {
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  const mobileAudit = await auditMobileShell();
  return {
    pageTitle: document.querySelector('#page-title')?.textContent || '',
    pageSubtitle: document.querySelector('#summary')?.textContent || '',
    diagnosticPanelCount: document.querySelectorAll('#tab-diagnostics .panel').length,
    bodyHorizontalOverflow: document.documentElement.scrollWidth > window.innerWidth + 2,
    buttonTextOverflow: hasButtonTextOverflow(),
    buttonTextOverflowOffender: buttonTextOverflowOffender(),
    ...mobileAudit
  };
})()`

const queueExpression = `(async () => {
  document.querySelector('[data-tab="queue"]')?.click();
  const started = Date.now();
  while (document.querySelector('#tab-queue')?.hidden && Date.now() - started < 5000) {
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  const panel = document.querySelector('#tab-queue .review-queue-panel');
  const output = document.querySelector('#repair-requests-output');
  const copy = document.querySelector('#tab-queue .section-head-copy')?.textContent || '';
  const mobileAudit = await auditMobileShell();
  return {
    pageTitle: document.querySelector('#page-title')?.textContent || '',
    pageSubtitle: document.querySelector('#summary')?.textContent || '',
    reviewQueueVisible: !!panel && visibleElement(panel),
    reviewQueueOutputVisible: !!output && visibleElement(output),
    reviewQueueCopyAuditOnly: copy.includes('Direct admin or LLM app actions') && copy.includes('Activity'),
    activeTabCount: document.querySelectorAll('.tabs button.active').length,
    bodyHorizontalOverflow: document.documentElement.scrollWidth > window.innerWidth + 2,
    buttonTextOverflow: hasButtonTextOverflow(),
    buttonTextOverflowOffender: buttonTextOverflowOffender(),
    ...mobileAudit
  };
})()`

// The debug snapshot moved from its own Admin page into Settings > Advanced.
const advancedExpression = `(async () => {
  document.querySelector('[data-tab="settings"]')?.click();
  const started = Date.now();
  while (document.querySelectorAll('#settings-menu [data-settings-section]').length < 8 && Date.now() - started < 5000) {
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  document.querySelector('#settings-menu [data-settings-section="advanced"]')?.click();
  await new Promise((resolve) => setTimeout(resolve, 120));
  const debug = document.querySelector('.settings-advanced .debug-disclosure');
  const mobileAudit = await auditMobileShell();
  return {
    pageTitle: document.querySelector('#page-title')?.textContent || '',
    pageSubtitle: document.querySelector('#summary')?.textContent || '',
    rawSnapshotCollapsed: !!debug && !debug.open && visibleElement(debug),
    bodyHorizontalOverflow: document.documentElement.scrollWidth > window.innerWidth + 2,
    buttonTextOverflow: hasButtonTextOverflow(),
    buttonTextOverflowOffender: buttonTextOverflowOffender(),
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
    buttonTextOverflowOffender: buttonTextOverflowOffender(),
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
    buttonTextOverflowOffender: buttonTextOverflowOffender(),
    ...mobileAudit
  };
})()`

const userChatKeyboardExpression = `(async () => {
  const waitFor = async (predicate, timeout = 3000) => {
    const started = Date.now();
    let value = predicate();
    while (!value && Date.now() - started < timeout) {
      await new Promise((resolve) => setTimeout(resolve, 50));
      value = predicate();
    }
    return value;
  };
  const chatPanel = document.querySelector('.panel.user-chat');
  const chatTab = document.querySelector('#user-chat-open');
  const input = document.querySelector('#user-chat-input');
  const send = document.querySelector('#user-chat-send');
  chatPanel?.setAttribute('data-chat-available', 'true');
  if (chatTab) chatTab.disabled = false;
  if (input) input.disabled = false;
  if (send) send.disabled = false;
  if (typeof setCompactView === 'function') {
    setCompactView('chat');
  } else {
    chatTab?.click();
  }
  chatTab?.click();
  await waitFor(() => document.body.dataset.compactView === 'chat');
  const root = document.documentElement;
  root.classList.add('is-keyboard-open');
  root.classList.add('visual-keyboard-test');
  root.style.setProperty('--visual-viewport-height', '430px');
  root.style.setProperty('--visual-viewport-top', '0px');
  root.style.setProperty('--keyboard-inset-bottom', '414px');
  const output = document.querySelector('#user-chat-output');
  if (output) {
    output.classList.remove('chat-empty', 'muted');
    output.classList.add('chat-result');
    output.replaceChildren(
      Object.assign(document.createElement('strong'), { textContent: 'Answer' }),
      Object.assign(document.createElement('p'), { textContent: 'The server storage needs attention. This is a long enough answer to prove the chat text remains visible while the keyboard is open.' })
    );
  }
  if (input) input.value = 'What is wrong right now?';
  input?.focus({ preventScroll: true });
  if (typeof scheduleCompactChatIntoView === 'function') scheduleCompactChatIntoView();
  await new Promise((resolve) => setTimeout(resolve, 180));
  root.classList.add('is-keyboard-open');
  root.classList.add('visual-keyboard-test');
  root.style.setProperty('--visual-viewport-height', '430px');
  root.style.setProperty('--visual-viewport-top', '0px');
  root.style.setProperty('--keyboard-inset-bottom', '414px');
  await new Promise((resolve) => requestAnimationFrame(resolve));
  const panel = document.querySelector('.panel.user-chat');
  const topbar = [...document.querySelectorAll('.topbar')].find((element) => element.closest('#user-home') || visibleElement(element));
  const tabs = document.querySelector('#user-view-tabs');
  const inputRect = input?.getBoundingClientRect() || { top: 9999, bottom: 9999, width: 0, height: 0 };
  const sendRect = send?.getBoundingClientRect() || { top: 9999, bottom: 9999, width: 0, height: 0 };
  const outputRect = output?.getBoundingClientRect() || { top: 9999, bottom: 9999, width: 0, height: 0 };
  const panelRect = panel?.getBoundingClientRect() || { top: 9999, bottom: 9999, width: 0, height: 0 };
  const visibleBottom = 430;
  const topbarCollapsed = !topbar || getComputedStyle(topbar).display === 'none';
  const tabsCollapsed = !tabs || getComputedStyle(tabs).display === 'none';
  const mobileAudit = await auditMobileShell();
  return {
    pageTitle: document.querySelector('#page-title')?.textContent || '',
    userChatVisible: !!(panel && visibleElement(panel)),
    userChatKeyboardMode: (root.classList.contains('is-keyboard-open') || root.classList.contains('visual-keyboard-test')) && document.body.dataset.compactView === 'chat',
    userChatComposerVisible: inputRect.height >= 44 && sendRect.height >= 44 && inputRect.bottom <= visibleBottom + 1 && sendRect.bottom <= visibleBottom + 1 && inputRect.top >= 0 && sendRect.top >= 0,
    userChatOutputVisible: outputRect.height >= 72 && outputRect.top >= 0 && outputRect.bottom <= inputRect.top,
    userChatChromeCollapsed: topbarCollapsed && tabsCollapsed && panelRect.bottom <= visibleBottom + 1,
    bodyHorizontalOverflow: document.documentElement.scrollWidth > window.innerWidth + 2,
    buttonTextOverflow: hasButtonTextOverflow(),
    buttonTextOverflowOffender: buttonTextOverflowOffender(),
    ...mobileAudit
  };
})()`

const userPromptActionExpression = `(async () => {
  const waitFor = async (predicate, timeout = 3000) => {
    const started = Date.now();
    let value = predicate();
    while (!value && Date.now() - started < timeout) {
      await new Promise((resolve) => setTimeout(resolve, 50));
      value = predicate();
    }
    return value;
  };
  document.querySelector('#user-chat-open')?.click();
  await waitFor(() => document.body.dataset.compactView === 'chat');
  const output = document.querySelector('#user-chat-output');
  const plan = {
    can_request_repair: true,
    can_execute: true,
    direct_action: 'start',
    recommended_action_id: 'ask_admin_to_restart_container',
    target: { kind: 'app', id: 'emby', label: 'Emby', resolved: true }
  };
  if (output && typeof renderUserRepairRequestPrompt === 'function') {
    output.classList.remove('muted', 'chat-empty');
    output.classList.add('chat-result');
    output.replaceChildren(renderUserRepairRequestPrompt(plan, { general_user_summary: 'Emby is stopped.' }));
  }
  const prompt = document.querySelector('.user-repair-prompt');
  const button = [...(prompt?.querySelectorAll('button') || [])].find((element) => /start now/i.test(element.textContent || ''));
  const originalApi = window.api;
  const originalConfirm = window.confirm;
  const mockApi = async (path) => {
    if (path === '/api/user/apps/emby/action') {
      return {
        status: 'executed',
        outcome: {
          action: 'start',
          target_id: 'emby',
          target_label: 'Emby',
          before_status: 'offline',
          after_status: 'online',
          recovered: true,
          verified: true,
          message: 'Emby started successfully.'
        }
      };
    }
    return originalApi(path);
  };
  window.api = mockApi;
  window.confirm = () => true;
  try {
    api = mockApi;
  } catch (error) {}
  try {
    button?.click();
    await waitFor(() => /try again/i.test(button?.textContent || '') && !!document.querySelector('.user-repair-prompt .user-action-result'));
  } finally {
    window.api = originalApi;
    window.confirm = originalConfirm;
    try {
      api = originalApi;
    } catch (error) {}
  }
  const result = document.querySelector('.user-repair-prompt .user-action-result');
  const message = result?.textContent || '';
  const buttonText = button?.textContent || '';
  const chatPanel = document.querySelector('.panel.user-chat');
  if (chatPanel) chatPanel.dataset.chatAvailable = 'true';
  if (typeof setCompactView === 'function') setCompactView('chat');
  await new Promise((resolve) => requestAnimationFrame(resolve));
  const resultVisible = !!result && visibleElement(result);
  document.querySelector('#user-status-open')?.click();
  const mobileAudit = await auditMobileShell();
  return {
    pageTitle: document.querySelector('#page-title')?.textContent || '',
    userPromptTryAgainVisible: /try again/i.test(buttonText),
    userPromptInlineSuccess: resultVisible && message.includes('Emby started successfully.') && message.includes('Send another message or try again'),
    bodyHorizontalOverflow: document.documentElement.scrollWidth > window.innerWidth + 2,
    buttonTextOverflow: hasButtonTextOverflow(),
    buttonTextOverflowOffender: buttonTextOverflowOffender(),
    ...mobileAudit
  };
})()`

const userAppDetailExpression = `(async () => {
  document.documentElement.classList.remove('is-keyboard-open');
  document.documentElement.classList.remove('visual-keyboard-test');
  document.documentElement.style.setProperty('--visual-viewport-height', window.innerHeight + 'px');
  document.documentElement.style.setProperty('--visual-viewport-top', '0px');
  document.documentElement.style.setProperty('--keyboard-inset-bottom', '0px');
  document.querySelector('#user-status-open')?.click();
  const statusStarted = Date.now();
  while (document.body.dataset.compactView !== 'status' && Date.now() - statusStarted < 3000) {
    await new Promise((resolve) => setTimeout(resolve, 50));
  }
  const card = document.querySelector('#user-apps .user-app-card');
  window.__visualLastDetailTrigger = card;
  card?.focus();
  card?.click();
  const started = Date.now();
  const loaded = () => {
    const panel = document.querySelector('#user-app-detail');
    const loading = [...document.querySelectorAll('#user-app-detail .detail-empty')]
      .some((element) => /loading/i.test(element.textContent || ''));
    return document.body.dataset.compactView === 'app-detail' && panel && !panel.hidden && !loading;
  };
  while (!loaded() && Date.now() - started < 5000) {
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  const panel = document.querySelector('#user-app-detail');
  const visibleText = visibleCompactText(panel);
  const bannedMatches = visibleText.match(/\b(container|docker|unraid|array|parity|endpoint|graphql|probe|wan|lan|api|ssh|telemetry|smart|syslog|filesystem|cache pool|gateway|https|dns|unifi)\b/gi) || [];
  const historyVisible = !!panel?.querySelector('.history-list, .detail-history .detail-empty');
  const repairActions = panel ? [...panel.querySelectorAll('.user-repair-actions button')].filter((element) => visibleElement(element)) : [];
  const repairActionLabel = repairActions.map((element) => (element.textContent || element.getAttribute('aria-label') || '').trim()).filter(Boolean).join('|');
  const directActionNames = new Set(repairActions.map((element) => (element.textContent || element.getAttribute('aria-label') || '').trim().toLowerCase())
    .filter((text) => ['start', 'restart', 'stop'].includes(text)));
  const detailActionTextWrapped = repairActions.some((element) => elementTextWraps(element));
  const emptyNodes = panel ? [...panel.querySelectorAll('.detail-empty')] : [];
  const emptyStateVisible = emptyNodes
    .some((element) => visibleElement(element) && /no changes recorded yet/i.test(element.textContent || ''));
  const mobileAudit = await auditMobileShell();
  return {
    pageTitle: document.querySelector('#page-title')?.textContent || '',
    userDetailVisible: !!(panel && !panel.hidden && visibleElement(panel)),
    userDetailTitle: panel?.querySelector('.detail-title h2')?.textContent || '',
    userDetailHistoryVisible: historyVisible,
    userDetailEmptyStateVisible: emptyStateVisible,
    userRepairActionVisible: /\b(ask admin|restart now|start|restart|stop)\b/i.test(repairActionLabel),
    userRepairDirectActionCount: directActionNames.size,
    userRepairActionLabel: repairActionLabel,
    detailActionTextWrapped,
    bannedTermCount: bannedMatches.length,
    noticeTitleOverlap: noticeOverlapsTitle(),
    rawTransportErrorVisible: rawTransportErrorVisible(),
    bodyHorizontalOverflow: document.documentElement.scrollWidth > window.innerWidth + 2,
    buttonTextOverflow: hasButtonTextOverflow(),
    buttonTextOverflowOffender: buttonTextOverflowOffender(),
    ...mobileAudit
  };
})()`

const userInfraDetailExpression = `(async () => {
  const technical = document.querySelector('.user-technical-details');
  if (technical && !technical.open) technical.open = true;
  const cards = [...document.querySelectorAll('#user-status-grid .user-status-card.has-detail')];
  const card = cards.find((element) => /router|internet/i.test(element.getAttribute('aria-label') || element.textContent || '')) || cards[0];
  window.__visualLastDetailTrigger = card;
  card?.focus();
  card?.click();
  const started = Date.now();
  const loaded = () => {
    const panel = document.querySelector('#user-infra-detail');
    const loading = [...document.querySelectorAll('#user-infra-detail .detail-empty')]
      .some((element) => /loading/i.test(element.textContent || ''));
    return document.body.dataset.compactView === 'infra-detail' && panel && !panel.hidden && !loading;
  };
  while (!loaded() && Date.now() - started < 5000) {
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  const panel = document.querySelector('#user-infra-detail');
  const visibleText = visibleCompactText(panel);
  const bannedMatches = visibleText.match(/\b(container|docker|unraid|array|parity|endpoint|graphql|probe|wan|lan|api|ssh|telemetry|smart|syslog|filesystem|cache pool|gateway|https|dns|unifi)\b/gi) || [];
  const historyVisible = !!panel?.querySelector('.history-list, .detail-history .detail-empty');
  const emptyNodes = panel ? [...panel.querySelectorAll('.detail-empty')] : [];
  const emptyStateVisible = emptyNodes
    .some((element) => visibleElement(element) && /no changes recorded yet/i.test(element.textContent || ''));
  const mobileAudit = await auditMobileShell();
  return {
    pageTitle: document.querySelector('#page-title')?.textContent || '',
    userDetailVisible: !!(panel && !panel.hidden && visibleElement(panel)),
    userDetailTitle: panel?.querySelector('.detail-title h2')?.textContent || '',
    userDetailHistoryVisible: historyVisible,
    userDetailEmptyStateVisible: emptyStateVisible,
    bannedTermCount: bannedMatches.length,
    bodyHorizontalOverflow: document.documentElement.scrollWidth > window.innerWidth + 2,
    buttonTextOverflow: hasButtonTextOverflow(),
    buttonTextOverflowOffender: buttonTextOverflowOffender(),
    ...mobileAudit
  };
})()`

const userDetailBackExpression = `(async () => {
  const trigger = window.__visualLastDetailTrigger;
  const back = document.querySelector('#user-app-detail:not([hidden]) .detail-back, #user-infra-detail:not([hidden]) .detail-back');
  back?.click();
  const started = Date.now();
  while (document.body.dataset.compactView !== 'status' && Date.now() - started < 5000) {
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  const mobileAudit = await auditMobileShell();
  return {
    pageTitle: document.querySelector('#page-title')?.textContent || '',
    userDetailBackReturned: document.body.dataset.compactView === 'status' &&
      document.querySelector('#user-app-detail')?.hidden &&
      document.querySelector('#user-infra-detail')?.hidden,
    userDetailFocusReturned: !!trigger && document.activeElement === trigger,
    bodyHorizontalOverflow: document.documentElement.scrollWidth > window.innerWidth + 2,
    buttonTextOverflow: hasButtonTextOverflow(),
    buttonTextOverflowOffender: buttonTextOverflowOffender(),
    ...mobileAudit
  };
})()`

const userDrawerOpenExpression = `(async () => {
  const toggle = document.querySelector('#user-menu-toggle');
  if (toggle && !visibleElement(toggle)) {
    return {
      userMenuToggleVisible: false,
      userDrawerOpen: false,
      bodyHorizontalOverflow: document.documentElement.scrollWidth > window.innerWidth + 2,
      buttonTextOverflow: hasButtonTextOverflow(),
    buttonTextOverflowOffender: buttonTextOverflowOffender(),
      elementBoundsOverflow: componentBoundsOverflow(),
      elementBoundsOffender: componentBoundsOffender()
    };
  }
  toggle?.click();
  const started = Date.now();
  while ((!document.body.classList.contains('user-menu-open') || document.querySelector('#user-drawer')?.hidden) && Date.now() - started < 5000) {
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  const listStarted = Date.now();
  while (document.querySelector('#user-drawer .user-settings-loading') && Date.now() - listStarted < 5000) {
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  const drawer = document.querySelector('#user-drawer');
  if (!drawer) {
    return {
      userMenuToggleVisible: !!toggle && visibleElement(toggle),
      userDrawerOpen: false,
      bodyHorizontalOverflow: document.documentElement.scrollWidth > window.innerWidth + 2,
      buttonTextOverflow: hasButtonTextOverflow(),
    buttonTextOverflowOffender: buttonTextOverflowOffender(),
      elementBoundsOverflow: componentBoundsOverflow(),
      elementBoundsOffender: componentBoundsOffender()
    };
  }
  const drawerText = visibleCompactText(drawer);
  const bannedMatches = drawerText.match(/\b(container|docker|unraid|array|parity|endpoint|graphql|probe|wan|lan|api|ssh|telemetry|smart|syslog|filesystem|cache pool|gateway|https|dns|unifi)\b/gi) || [];
  const visibleControls = [...drawer.querySelectorAll('button,input,textarea,select')]
    .filter((element) => visibleElement(element));
  const smallDrawerTargets = visibleControls.filter((element) => {
    const rect = element.getBoundingClientRect();
    return Math.round(rect.width) < 44 || Math.round(rect.height) < 44;
  });
  let notificationSignupVisible = false;
  let notificationSignupTitle = '';
  let notificationSignupPrimary = false;
  const firstNotificationToggle = drawer.querySelector('.user-notification-row input[type="checkbox"]');
  if (firstNotificationToggle && !firstNotificationToggle.checked) {
    firstNotificationToggle.click();
    const signupStarted = Date.now();
    while (!document.querySelector('.compact-notification-signup') && Date.now() - signupStarted < 3000) {
      await new Promise((resolve) => setTimeout(resolve, 100));
    }
    const signup = document.querySelector('.compact-notification-signup');
    notificationSignupVisible = !!(signup && visibleElement(signup));
    notificationSignupTitle = signup?.querySelector('h2')?.textContent?.trim() || '';
    notificationSignupPrimary = !![...(signup?.querySelectorAll('button') || [])].find((button) => button.textContent.trim() === 'Turn on alerts' && visibleElement(button));
    const cancel = [...(signup?.querySelectorAll('button') || [])].find((button) => button.textContent.trim() === 'Not now');
    cancel?.click();
    const closeStarted = Date.now();
    while (document.querySelector('.compact-notification-signup') && Date.now() - closeStarted < 3000) {
      await new Promise((resolve) => setTimeout(resolve, 100));
    }
  }
  return {
    userMenuToggleVisible: !!toggle && visibleElement(toggle),
    userDrawerOpen: !!(drawer && !drawer.hidden && visibleElement(drawer) && toggle?.getAttribute('aria-expanded') === 'true'),
    drawerBannedTermCount: bannedMatches.length,
    drawerSmallTouchTargetCount: smallDrawerTargets.length,
    drawerAdminControlCount: drawer.querySelectorAll('[data-admin-only], [data-admin-detail]').length,
    drawerNotificationRows: drawer.querySelectorAll('.user-notification-row').length,
    drawerEmptyStateVisible: !!drawer.querySelector('.user-settings-empty') && visibleElement(drawer.querySelector('.user-settings-empty')),
    drawerSignOutVisible: !!drawer.querySelector('#user-drawer-logout') && visibleElement(drawer.querySelector('#user-drawer-logout')),
    notificationSignupVisible,
    notificationSignupTitle,
    notificationSignupPrimary,
    bodyHorizontalOverflow: document.documentElement.scrollWidth > window.innerWidth + 2,
    buttonTextOverflow: hasButtonTextOverflow(),
    buttonTextOverflowOffender: buttonTextOverflowOffender(),
    elementBoundsOverflow: componentBoundsOverflow(),
    elementBoundsOffender: componentBoundsOffender()
  };
})()`

const userDrawerCloseExpression = `(async () => {
  const toggle = document.querySelector('#user-menu-toggle');
  const drawer = document.querySelector('#user-drawer');
  document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
  const started = Date.now();
  while (document.body.classList.contains('user-menu-open') && Date.now() - started < 5000) {
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  const hiddenStarted = Date.now();
  while (drawer && !drawer.hidden && Date.now() - hiddenStarted < 5000) {
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  const shell = document.querySelector('.shell');
  return {
    userDrawerHidden: !!(drawer && drawer.hidden && toggle?.getAttribute('aria-expanded') === 'false'),
    userDrawerFocusReturned: document.activeElement === toggle,
    userDrawerBodyUnlocked: !document.body.classList.contains('user-menu-open') && !(shell && shell.inert) && (!shell || shell.getAttribute('aria-hidden') !== 'true'),
    bodyHorizontalOverflow: document.documentElement.scrollWidth > window.innerWidth + 2,
    buttonTextOverflow: hasButtonTextOverflow(),
    buttonTextOverflowOffender: buttonTextOverflowOffender(),
    elementBoundsOverflow: componentBoundsOverflow(),
    elementBoundsOffender: componentBoundsOffender()
  };
})()`

const auditMobileShellExpression = `async function auditMobileShell() {
  const visibleControls = [...document.querySelectorAll('button,input,textarea,select')]
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
    userMenuToggleVisible: !!document.querySelector('#user-menu-toggle') && visibleElement(document.querySelector('#user-menu-toggle')),
    adminPageTitleVisible: document.body.classList.contains('admin-view') &&
      !!document.querySelector('.title-block') && visibleElement(document.querySelector('.title-block')),
    sourcePillText: document.querySelector('#source-pill')?.textContent || '',
    detailSectionCount: document.querySelectorAll('.detail-section').length,
    assistantDockVisible: !!document.querySelector('#assistant-dock') && visibleElement(document.querySelector('#assistant-dock')),
    assistantOverlapCount: assistantControlOverlapCount(),
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
      // App display names are user/Docker-chosen proper nouns (e.g. "UniFi Controller") and
      // may legitimately contain otherwise-banned substrings; don't scan them.
      if (parent.closest('[data-app-name]')) return NodeFilter.FILTER_REJECT;
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

function overflowingButton() {
  return [...document.querySelectorAll('button')].find((element) => {
    if (!visibleElement(element)) return false;
    const style = getComputedStyle(element);
    const iconOnly = style.fontSize === '0px' ||
      element.classList.contains('monitor-hide') ||
      element.classList.contains('app-control');
    if (iconOnly) return false;
    return element.scrollWidth > element.clientWidth + 1;
  }) || null;
}

function hasButtonTextOverflow() {
  return !!overflowingButton();
}

// Naming the offender turns "something clips" into an address. Reported
// alongside the boolean so a failure is actionable without a repro run.
function buttonTextOverflowOffender() {
  const element = overflowingButton();
  if (!element) return '';
  return [
    element.id ? '#' + element.id : '',
    element.className ? '.' + String(element.className).trim().replace(/\s+/g, '.') : '',
    (element.getAttribute('aria-label') || element.textContent || '').trim().slice(0, 40),
    element.scrollWidth + '>' + element.clientWidth
  ].filter(Boolean).join(' ');
}

function elementTextWraps(element) {
  if (!element || !visibleElement(element)) return false;
  const range = document.createRange();
  range.selectNodeContents(element);
  const tops = [...range.getClientRects()]
    .filter((rect) => rect.width > 0 && rect.height > 0)
    .map((rect) => Math.round(rect.top));
  return new Set(tops).size > 1;
}

function noticeOverlapsTitle() {
  const notice = document.querySelector('#notice');
  const title = document.querySelector('.title-block');
  return !!(notice && title && visibleElement(notice) && visibleElement(title) && rectsIntersect(notice.getBoundingClientRect(), title.getBoundingClientRect()));
}

function rawTransportErrorVisible() {
  const notice = document.querySelector('#notice');
  if (!notice || !visibleElement(notice)) return false;
  return /\b(not found|forbidden|conflict|unauthorized|bad request|internal server error|service unavailable|gateway timeout)\b/i.test(notice.textContent || '');
}

function componentBoundsOverflow() {
  return componentBoundsOffender() !== '';
}

function assistantControlOverlapCount() {
  const dock = document.querySelector('#assistant-dock');
  if (!dock || !visibleElement(dock)) return 0;
  const dockRect = dock.getBoundingClientRect();
  return [...document.querySelectorAll('button,input,textarea,select,summary,a[href]')]
    .filter((element) => visibleElement(element) && !dock.contains(element))
    .filter((element) => rectsIntersect(dockRect, element.getBoundingClientRect()))
    .length;
}

function rectsIntersect(a, b) {
  return a.left < b.right && a.right > b.left && a.top < b.bottom && a.bottom > b.top;
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
  document.querySelector('#settings-menu [data-settings-section="apps"]')?.click();
  const appsStarted = Date.now();
  while (document.querySelectorAll('.settings-app-controls .setting-toggle').length < 1 && Date.now() - appsStarted < 5000) {
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  const agentRepairToggleCount = document.querySelectorAll('.settings-app-controls .setting-toggle').length;
  document.querySelector('#settings-menu [data-settings-section="llm"]')?.click();
  const llmStarted = Date.now();
  while (!document.querySelector('.agent-readiness') && Date.now() - llmStarted < 5000) {
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  const visibleMenuButtons = () => [...document.querySelectorAll('#settings-menu [data-settings-section]')]
    .filter((element) => visibleElement(element)).length;
  const search = document.querySelector('#settings-search');
  const menuBeforeSearch = visibleMenuButtons();
  let settingsSearchNarrowed = false;
  let settingsSearchRestored = false;
  if (search) {
    search.value = 'notification';
    search.dispatchEvent(new Event('input', { bubbles: true }));
    await new Promise((resolve) => setTimeout(resolve, 80));
    settingsSearchNarrowed = visibleMenuButtons() > 0 && visibleMenuButtons() < menuBeforeSearch;
    search.value = '';
    search.dispatchEvent(new Event('input', { bubbles: true }));
    await new Promise((resolve) => setTimeout(resolve, 80));
    settingsSearchRestored = visibleMenuButtons() === menuBeforeSearch;
  }
  document.querySelector('#settings-menu [data-settings-section="llm"]')?.click();
  await new Promise((resolve) => setTimeout(resolve, 80));
  const visibleSettingsSections = [...document.querySelectorAll('#tab-settings .settings-section')]
    .filter((element) => !element.hidden && getComputedStyle(element).display !== 'none').length;
  const settingsControlCount = document.querySelectorAll('.settings-card input,.settings-card select,.settings-card button').length;
  const readinessText = document.querySelector('.agent-readiness')?.textContent || '';
  const dirtyControl = document.querySelector('#tab-settings .settings-section:not([hidden]) select');
  let settingsDirtyPromptBlocked = false;
  let settingsDirtyCleared = false;
  if (dirtyControl) {
    const originalConfirm = window.confirm;
    const originalValue = dirtyControl.value;
    const alternate = [...dirtyControl.options].map((option) => option.value).find((value) => value !== originalValue);
    if (alternate) {
      dirtyControl.value = alternate;
      dirtyControl.dispatchEvent(new Event('change', { bubbles: true }));
      window.confirm = () => false;
      document.querySelector('[data-tab="overview"]')?.click();
      settingsDirtyPromptBlocked = document.querySelector('#tab-settings')?.hidden === false;
      window.confirm = () => true;
      dirtyControl.value = originalValue;
      dirtyControl.dispatchEvent(new Event('change', { bubbles: true }));
      document.querySelector('#settings-refresh')?.click();
      const reloadStarted = Date.now();
      while (document.querySelector('#settings-grid .settings-footer.is-dirty') && Date.now() - reloadStarted < 5000) {
        await new Promise((resolve) => setTimeout(resolve, 100));
      }
      settingsDirtyCleared = !document.querySelector('#settings-grid .settings-footer.is-dirty');
    }
    window.confirm = originalConfirm;
  }
  // Data and logs is loaded on open, so open it before counting.
  document.querySelector('#settings-menu [data-settings-section="data"]')?.click();
  const dataStarted = Date.now();
  while (!document.querySelector('#data-settings .data-store') && Date.now() - dataStarted < 5000) {
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  const dataStoreCount = document.querySelectorAll('#data-settings .data-store').length;
  // Every store must offer a clear, whether or not it currently holds anything —
  // the fixture scenario starts with no recorded history, so counting rows would
  // only prove the fixture is empty.
  const dataClearControlCount = document.querySelectorAll('#data-settings .data-store-head .command').length;
  document.querySelector('#settings-menu [data-settings-section="llm"]')?.click();
  await new Promise((resolve) => setTimeout(resolve, 80));
  const buttonTextOverflow = hasButtonTextOverflow();
  const mobileAudit = await auditMobileShell();
  return {
    pageTitle: document.querySelector('#page-title')?.textContent || '',
    pageSubtitle: document.querySelector('#summary')?.textContent || '',
    dataStoreCount,
    dataClearControlCount,
    settingsEditorCount: document.querySelectorAll('.settings-editor').length,
    settingsControlCount,
    settingsMenuButtonCount: document.querySelectorAll('#settings-menu [data-settings-section]').length,
    settingsSearchNarrowed,
    settingsSearchRestored,
    visibleSettingsSections,
    settingsDirtyPromptBlocked,
    settingsDirtyCleared,
    agentRepairToggleCount,
    agentReadinessLimitVisible: readinessText.includes('1 per app') && readinessText.includes('5 total'),
    bodyHorizontalOverflow: document.documentElement.scrollWidth > window.innerWidth + 2,
    buttonTextOverflow,
    buttonTextOverflowOffender: buttonTextOverflowOffender(),
    ...mobileAudit
  };
})()`

const agentRepairExpression = `(async () => {
  const waitFor = async (predicate, timeout = 2500) => {
    const started = Date.now();
    let value = predicate();
    while (!value && Date.now() - started < timeout) {
      await new Promise((resolve) => setTimeout(resolve, 25));
      value = predicate();
    }
    return value;
  };
  document.querySelector('[data-tab="diagnostics"]')?.click();
  const started = Date.now();
  while (document.querySelector('#tab-diagnostics')?.hidden && Date.now() - started < 5000) {
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  const output = document.querySelector('#diagnostic-output');
  const plan = {
    id: 'visual-repair-plan',
    title: 'Restart recommendation',
    summary: 'The model suggested restarting one app. NoobBoard can run one restart only after admin approval and per-app opt-in.',
    recommended_action_id: 'ask_admin_to_restart_container',
    requires_admin_approval: true,
    can_execute: true,
    status: 'approval_ready',
    approval_token: 'visual-token',
    target: { kind: 'app', id: 'emby', label: 'Emby', resolved: true },
    options: [
      { id: 'deny', label: 'Do not allow', description: 'Keep the diagnosis and do not permit an automatic fix.', enabled: true, selected: true },
      { id: 'allow_once', label: 'Allow fix', description: 'Permit this single fix attempt.', enabled: true }
    ]
  };
  if (output && typeof renderAgentPlanPrompt === 'function') {
    output.classList.remove('muted', 'chat-empty');
    output.classList.add('chat-result');
    output.replaceChildren(renderAgentPlanPrompt(plan));
  }
  const originalApi = window.api;
  const mockApi = async (path) => {
    if (path !== '/api/admin/agent/approval') return originalApi(path);
    await new Promise((resolve) => setTimeout(resolve, 160));
    return {
      status: 'executed',
      outcome: {
        action: 'restart',
        target_id: 'emby',
        target_label: 'Emby',
        before_status: 'offline',
        after_status: 'online',
        recovered: true,
        verified: true,
        message: 'Auto-repair: restarted - recovered.'
      }
    };
  };
  window.api = mockApi;
  try {
    api = mockApi;
  } catch (error) {}
  let dialogVisible = false;
  let closedAfterRun = false;
  let progressVisible = false;
  let statusText = '';
  try {
    if (typeof openAgentApprovalDialog === 'function') {
      openAgentApprovalDialog(plan);
    }
    const dialog = document.querySelector('.agent-approval-dialog');
    dialogVisible = !!dialog && visibleElement(dialog);
    // One click, not select-then-submit: the approval button carries the
    // operation and its target, so pressing it IS the approval.
    document.querySelector('.agent-approval-actions [data-choice="allow_once"]')?.click();
    await waitFor(() => document.querySelector('.agent-repair-progress.pending'));
    const progress = document.querySelector('.agent-repair-progress.pending');
    progressVisible = !!progress && visibleElement(progress);
    closedAfterRun = !document.querySelector('.agent-approval-dialog');
    statusText = document.querySelector('.agent-plan-head .settings-state-pill')?.textContent || '';
    await waitFor(() => document.querySelector('.agent-repair-outcome'), 3000);
  } finally {
    window.api = originalApi;
    try {
      api = originalApi;
    } catch (error) {}
  }
  const outcome = document.querySelector('.agent-repair-outcome');
  const mobileAudit = await auditMobileShell();
  return {
    pageTitle: document.querySelector('#page-title')?.textContent || '',
    agentApprovalDialogVisible: dialogVisible,
    agentApprovalClosedAfterRun: closedAfterRun,
    agentRepairProgressVisible: progressVisible,
    agentRepairStatusText: statusText,
    agentRepairOutcomeVisible: !!outcome && visibleElement(outcome),
    agentRepairOutcomeRecovered: !!outcome && outcome.classList.contains('recovered') && (outcome.textContent || '').includes('offline → online'),
    agentApprovalActionVisible: [...document.querySelectorAll('.agent-plan-actions button')].some((element) => visibleElement(element) && /open approval/i.test(element.textContent || '')),
    noticeTitleOverlap: noticeOverlapsTitle(),
    bodyHorizontalOverflow: document.documentElement.scrollWidth > window.innerWidth + 2,
    buttonTextOverflow: hasButtonTextOverflow(),
    buttonTextOverflowOffender: buttonTextOverflowOffender(),
    ...mobileAudit
  };
})()`
