const state = {
  user: null,
  csrf: "",
  snapshot: null,
  activeTab: "overview",
  appFilter: "all",
  appSearch: "",
  hiddenMonitors: new Set(),
  overviewOrder: [],
  overviewRearrange: false,
  overviewTouchDrag: null,
  settingsSection: "roles",
  roleVisibility: null,
  roleApps: [],
  roleUsers: [],
  roleUsersOriginal: [],
  selectedRole: "",
  auditEntries: [],
  notificationPreferences: new Map(),
  notificationPreferencesLoaded: false,
  notificationPreferencesLoading: false,
  userDrawerActiveSection: "settings",
  userDrawerLastFocus: null,
};

const $ = (id) => document.getElementById(id);
const MONITOR_STORAGE_KEY = "noobboard.hiddenMonitors.v1";
const OVERVIEW_ORDER_STORAGE_KEY = "noobboard.overviewOrder.v1";
const OVERVIEW_MONITOR_IDS = ["overview.overall", "overview.server", "overview.router", "overview.apps"];
const SITE_MODE = String(window.__NOOBBOARD_SITE_MODE__ || window.__HSD_SITE_MODE__ || "admin").toLowerCase() === "compact" ? "compact" : "admin";
const SETTINGS_ENDPOINTS = [
  { title: "Visibility", section: "visibility", path: "/api/admin/settings/visibility" },
  { title: "Blacklist", section: "blacklist", path: "/api/admin/settings/blacklist" },
  { title: "App Images", section: "apps", path: "/api/admin/settings/apps" },
  { title: "LLM", section: "llm", path: "/api/admin/settings/llm" },
  { title: "Integrations", section: "integrations", path: "/api/admin/settings/integrations" },
  { title: "Notifications", section: "notifications", path: "/api/admin/settings/notifications" },
];
const compactDrawerSections = [
  { id: "settings", label: "Settings", glyph: "\u2699", render: renderCompactSettings },
];
const BUILTIN_APP_ICON_RULES = [
  { icon: "media-server", label: "media server", patterns: ["emby", "jellyfin", "plex"] },
  { icon: "media-automation", label: "media automation", patterns: ["sonarr", "radarr", "lidarr", "readarr", "bazarr", "prowlarr", "tautulli", "overseerr", "ombi"] },
  { icon: "download-client", label: "download client", patterns: ["qbittorrent", "transmission", "deluge", "sabnzbd", "nzbget", "jdownloader"] },
  { icon: "smart-home", label: "smart home", patterns: ["homeassistant", "home-assistant", "zwave", "zigbee", "mqtt", "mosquitto", "esphome"] },
  { icon: "dns-filter", label: "DNS filter", patterns: ["pihole", "pi-hole", "adguard", "unbound", "blocky", "technitium"] },
  { icon: "cloud-storage", label: "storage", patterns: ["nextcloud", "syncthing", "filebrowser", "minio", "seafile", "paperless", "immich"] },
  { icon: "database", label: "database", patterns: ["postgres", "postgresql", "mariadb", "mysql", "redis", "mongo", "influx", "prometheus", "grafana"] },
  { icon: "network", label: "network", patterns: ["nginx", "traefik", "caddy", "swag", "wireguard", "tailscale", "cloudflared", "netboot", "vpn"] },
];

function loadHiddenMonitors() {
  try {
    const values = JSON.parse(localStorage.getItem(MONITOR_STORAGE_KEY) || "[]");
    state.hiddenMonitors = new Set(Array.isArray(values) ? values.filter(Boolean) : []);
  } catch {
    state.hiddenMonitors = new Set();
  }
}

function saveHiddenMonitors() {
  try {
    localStorage.setItem(MONITOR_STORAGE_KEY, JSON.stringify([...state.hiddenMonitors].sort()));
  } catch {
    // Local storage can be unavailable in private browsing; hiding still works until reload.
  }
}

function loadOverviewOrder() {
  try {
    const values = JSON.parse(localStorage.getItem(OVERVIEW_ORDER_STORAGE_KEY) || "[]");
    state.overviewOrder = Array.isArray(values) ? normalizeOverviewOrder(values) : [...OVERVIEW_MONITOR_IDS];
  } catch {
    state.overviewOrder = [...OVERVIEW_MONITOR_IDS];
  }
}

function saveOverviewOrder() {
  try {
    localStorage.setItem(OVERVIEW_ORDER_STORAGE_KEY, JSON.stringify(normalizeOverviewOrder(state.overviewOrder)));
  } catch {
    // Local storage can be unavailable in private browsing; ordering still works until reload.
  }
}

function normalizeOverviewOrder(values) {
  const allowed = new Set(OVERVIEW_MONITOR_IDS);
  const order = [];
  for (const value of values || []) {
    if (allowed.has(value) && !order.includes(value)) order.push(value);
  }
  for (const value of OVERVIEW_MONITOR_IDS) {
    if (!order.includes(value)) order.push(value);
  }
  return order;
}

function hideMonitor(id) {
  if (!id) return;
  state.hiddenMonitors.add(id);
  saveHiddenMonitors();
  renderSnapshot(state.snapshot || {});
  renderMonitorRestore();
  showNotice("Monitor hidden.");
}

function restoreMonitors() {
  state.hiddenMonitors.clear();
  saveHiddenMonitors();
  renderSnapshot(state.snapshot || {});
  renderMonitorRestore();
  showNotice("Monitors restored.");
}

function dropOverviewCard(sourceID, targetID) {
  if (!sourceID || !targetID || sourceID === targetID) return;
  const fullOrder = normalizeOverviewOrder(state.overviewOrder).filter((item) => item !== sourceID);
  const targetIndex = fullOrder.indexOf(targetID);
  if (targetIndex < 0) return;
  fullOrder.splice(targetIndex, 0, sourceID);
  state.overviewOrder = normalizeOverviewOrder(fullOrder);
  saveOverviewOrder();
  renderOverviewCards(state.snapshot || {});
}

function setOverviewRearrange(enabled) {
  state.overviewRearrange = !!enabled;
  state.overviewTouchDrag = null;
  document.body.classList.toggle("overview-rearrange-active", state.overviewRearrange);
  renderOverviewRearrangeButton();
  renderOverviewCards(state.snapshot || {});
}

function renderOverviewRearrangeButton() {
  const button = $("overview-rearrange");
  if (!button) return;
  button.classList.toggle("active", state.overviewRearrange);
  button.setAttribute("aria-pressed", String(state.overviewRearrange));
  button.textContent = state.overviewRearrange ? "Done" : "Rearrange";
}

function startOverviewTouchDrag(event, id) {
  if (!state.overviewRearrange || event.pointerType === "mouse" || event.target.closest("button")) return;
  event.preventDefault();
  state.overviewTouchDrag = { id, pointerId: event.pointerId, x: event.clientX, y: event.clientY };
  event.currentTarget.classList.add("dragging");
  event.currentTarget.setPointerCapture?.(event.pointerId);
}

function moveOverviewTouchDrag(event, id) {
  if (!state.overviewTouchDrag || state.overviewTouchDrag.id !== id || state.overviewTouchDrag.pointerId !== event.pointerId) return;
  state.overviewTouchDrag.x = event.clientX;
  state.overviewTouchDrag.y = event.clientY;
}

function finishOverviewTouchDrag(event, id) {
  const drag = state.overviewTouchDrag;
  if (!drag || drag.id !== id || drag.pointerId !== event.pointerId) return;
  event.preventDefault();
  event.currentTarget.classList.remove("dragging");
  event.currentTarget.releasePointerCapture?.(event.pointerId);
  state.overviewTouchDrag = null;
  const target = document.elementFromPoint(drag.x, drag.y)?.closest?.(".overview-card");
  const targetID = target?.dataset.overviewId;
  if (targetID && targetID !== drag.id) {
    dropOverviewCard(drag.id, targetID);
    showNotice("Overview order saved.");
  }
}

function renderMonitorRestore() {
  const button = $("restore-monitors");
  if (!button) return;
  const count = state.hiddenMonitors.size;
  button.hidden = !hasAdminSurface() || count === 0;
  button.textContent = count === 1 ? "Restore monitor" : `Restore ${count} monitors`;
}

function openNav() {
  document.body.classList.add("nav-open");
  $("nav-toggle").setAttribute("aria-expanded", "true");
  $("nav-backdrop").hidden = false;
}

function closeNav() {
  document.body.classList.remove("nav-open");
  $("nav-toggle").setAttribute("aria-expanded", "false");
  $("nav-backdrop").hidden = true;
}

function openUserMenu() {
  if (!document.body.classList.contains("compact-view")) return;
  clearTimeout(closeUserMenu.hideTimer);
  const drawer = $("user-drawer");
  const backdrop = $("user-drawer-backdrop");
  const trigger = $("user-menu-toggle");
  if (!drawer || !backdrop || !trigger) return;
  state.userDrawerLastFocus = document.activeElement instanceof HTMLElement ? document.activeElement : trigger;
  renderUserDrawer();
  drawer.hidden = false;
  backdrop.hidden = false;
  trigger.setAttribute("aria-expanded", "true");
  document.body.classList.add("user-menu-open");
  const closeButton = $("user-drawer-close");
  closeButton?.focus({ preventScroll: true });
  lockUserMenuBackground(true);
  if (!state.notificationPreferencesLoaded && !state.notificationPreferencesLoading) {
    loadCompactNotificationPreferences();
  }
}

function closeUserMenu(options = {}) {
  const { returnFocus = true } = options;
  const drawer = $("user-drawer");
  const backdrop = $("user-drawer-backdrop");
  const trigger = $("user-menu-toggle");
  if (!drawer || !backdrop || !trigger) return;
  document.body.classList.remove("user-menu-open");
  trigger.setAttribute("aria-expanded", "false");
  lockUserMenuBackground(false);
  const finish = () => {
    if (!document.body.classList.contains("user-menu-open")) {
      drawer.hidden = true;
      backdrop.hidden = true;
    }
  };
  clearTimeout(closeUserMenu.hideTimer);
  closeUserMenu.hideTimer = setTimeout(finish, prefersReducedMotion() ? 0 : 210);
  if (returnFocus) {
    // Return focus to the control that opened the drawer (the hamburger). A tap/programmatic
    // open can leave document.activeElement on <body>, so prefer the trigger explicitly.
    const target = trigger?.isConnected ? trigger : (state.userDrawerLastFocus?.isConnected ? state.userDrawerLastFocus : null);
    target?.focus?.({ preventScroll: true });
  }
}

function lockUserMenuBackground(locked) {
  const shell = document.querySelector(".shell");
  if (!shell) return;
  if (locked) {
    shell.inert = true;
    shell.setAttribute("aria-hidden", "true");
    return;
  }
  shell.inert = false;
  shell.removeAttribute("aria-hidden");
}

function prefersReducedMotion() {
  return window.matchMedia?.("(prefers-reduced-motion: reduce)")?.matches === true;
}

function isUserMenuOpen() {
  const drawer = $("user-drawer");
  return !!(drawer && document.body.classList.contains("user-menu-open") && !drawer.hidden);
}

function drawerFocusableElements() {
  const drawer = $("user-drawer");
  if (!drawer || drawer.hidden) return [];
  return [...drawer.querySelectorAll("a[href],button:not([disabled]),input:not([disabled]),select:not([disabled]),textarea:not([disabled]),summary,[tabindex]:not([tabindex='-1'])")]
    .filter((element) => element.getClientRects().length && getComputedStyle(element).visibility !== "hidden");
}

function handleUserDrawerKeydown(event) {
  if (!isUserMenuOpen()) return;
  if (event.key === "Escape") {
    event.preventDefault();
    closeUserMenu();
    return;
  }
  if (event.key !== "Tab") return;
  const focusable = drawerFocusableElements();
  if (!focusable.length) return;
  const first = focusable[0];
  const last = focusable[focusable.length - 1];
  if (event.shiftKey && document.activeElement === first) {
    event.preventDefault();
    last.focus();
    return;
  }
  if (!event.shiftKey && document.activeElement === last) {
    event.preventDefault();
    first.focus();
  }
}

function visibleMonitor(id, element) {
  if (!element || state.hiddenMonitors.has(id)) return null;
  element.dataset.monitorId = id;
  return element;
}

function monitorHideButton(id) {
  return node("button", {
    type: "button",
    class: "command monitor-hide",
    "data-glyph": "x",
    title: "Hide monitor",
    "aria-label": "Hide monitor",
    onclick: (event) => {
      event.preventDefault();
      event.stopPropagation();
      hideMonitor(id);
    },
  });
}

function configureMobileRuntime() {
  const root = document.documentElement;
  const userAgent = navigator.userAgent || "";
  const isIOS = /iPad|iPhone|iPod/.test(userAgent) || (navigator.platform === "MacIntel" && navigator.maxTouchPoints > 1);
  const isAndroid = /Android/.test(userAgent);
  const standaloneQuery = window.matchMedia?.("(display-mode: standalone)");
  const isStandalone = standaloneQuery?.matches || window.navigator.standalone === true;
  root.classList.toggle("is-ios", isIOS);
  root.classList.toggle("is-android", isAndroid);
  root.classList.toggle("is-standalone", isStandalone);

  const setVisualViewportHeight = () => {
    const viewport = window.visualViewport;
    const height = Math.round(viewport?.height || window.innerHeight || 0);
    if (height > 0) root.style.setProperty("--visual-viewport-height", `${height}px`);
  };
  setVisualViewportHeight();
  window.addEventListener("resize", setVisualViewportHeight, { passive: true });
  window.visualViewport?.addEventListener("resize", setVisualViewportHeight, { passive: true });
  window.visualViewport?.addEventListener("scroll", setVisualViewportHeight, { passive: true });

  if (window.matchMedia?.("(pointer: coarse)").matches) {
    document.addEventListener("focusin", (event) => {
      const target = event.target;
      if (!(target instanceof HTMLInputElement || target instanceof HTMLTextAreaElement)) return;
      setTimeout(() => {
        target.scrollIntoView({ block: "center", inline: "nearest", behavior: "smooth" });
      }, 250);
    });
  }
}

function node(tag, attrs = {}, ...children) {
  const element = document.createElement(tag);
  for (const [key, value] of Object.entries(attrs)) {
    if (key === "class") element.className = value;
    else if (key === "text") element.textContent = value;
    else if (key.startsWith("on") && typeof value === "function") element.addEventListener(key.slice(2), value);
    else if (value !== false && value !== null && value !== undefined) element.setAttribute(key, value === true ? "" : value);
  }
  for (const child of children.flat()) {
    if (child === null || child === undefined) continue;
    element.append(child.nodeType ? child : document.createTextNode(String(child)));
  }
  return element;
}

function svgNode(tag, attrs = {}, ...children) {
  const element = document.createElementNS("http://www.w3.org/2000/svg", tag);
  for (const [key, value] of Object.entries(attrs)) {
    if (value !== false && value !== null && value !== undefined) {
      element.setAttribute(key, value === true ? "" : value);
    }
  }
  for (const child of children.flat()) {
    if (child === null || child === undefined) continue;
    element.append(child.nodeType ? child : document.createTextNode(String(child)));
  }
  return element;
}

function showNotice(message, tone = "info") {
  const notice = $("notice");
  notice.textContent = message;
  notice.dataset.tone = tone;
  notice.hidden = false;
  clearTimeout(showNotice.timer);
  showNotice.timer = setTimeout(() => {
    notice.hidden = true;
  }, 4200);
}

async function api(path, options = {}) {
  const headers = { "Content-Type": "application/json", ...(options.headers || {}) };
  if (state.csrf && options.method && options.method !== "GET") {
    headers["X-CSRF-Token"] = state.csrf;
  }
  const response = await fetch(path, { credentials: "same-origin", ...options, headers });
  const data = await response.json().catch(() => ({}));
  if (!response.ok) throw new Error(data.error || response.statusText);
  return data;
}

async function login(event) {
  event.preventDefault();
  const form = new FormData(event.currentTarget);
  try {
    const data = await api("/api/auth/login", {
      method: "POST",
      body: JSON.stringify({
        username: form.get("username"),
        password: form.get("password"),
      }),
    });
    state.user = data.user;
    state.csrf = data.csrf_token;
    showDashboard();
    await refresh();
  } catch (error) {
    showNotice(error.message, "error");
  }
}

async function restoreSession() {
  try {
    const data = await api("/api/auth/me");
    state.user = data.user;
    state.csrf = data.csrf_token;
    showDashboard();
    await refresh();
  } catch {
    $("login").hidden = false;
    $("dashboard").hidden = true;
  }
}

function showDashboard() {
  const admin = hasAdminSurface();
  document.body.classList.toggle("compact-site", isCompactSite());
  document.body.classList.toggle("admin-view", admin);
  document.body.classList.toggle("compact-view", !admin);
  if (admin) closeUserMenu({ returnFocus: false });
  $("login").hidden = true;
  $("dashboard").hidden = false;
  $("role-pill").textContent = state.user.role.replace("_", " ");
  $("user-home").hidden = admin;
  document.querySelectorAll("[data-admin-detail]").forEach((element) => {
    element.hidden = !admin;
  });
  document.querySelectorAll("[data-admin-only]").forEach((element) => {
    element.hidden = !admin;
  });
  $("refresh").textContent = admin ? "Refresh" : "Check again";
  $("refresh").setAttribute("aria-label", admin ? "Refresh status" : "Check again");
  $("logout").setAttribute("aria-label", "Sign out");
  $("user-menu-toggle").setAttribute("aria-expanded", "false");
  if (admin) {
    setActiveTab(state.activeTab);
  } else {
    state.activeTab = "user-home";
    $("page-title").textContent = "Home status";
  }
  renderMonitorRestore();
}

function setActiveTab(tabName) {
  if (!hasAdminSurface()) {
    state.activeTab = "user-home";
    $("page-title").textContent = "Home status";
    return;
  }
  state.activeTab = tabName;
  document.querySelectorAll("#tabs button").forEach((button) => {
    button.classList.toggle("active", button.dataset.tab === tabName);
  });
  document.querySelectorAll(".tab-panel").forEach((panel) => {
    panel.hidden = panel.id !== `tab-${tabName}`;
  });
  const titles = {
    overview: "System overview",
    server: "Server health",
    router: "Router and UniFi",
    apps: "Application inventory",
    incidents: "Incidents and facts",
    diagnostics: "Diagnostics",
    admin: "Admin workspace",
    settings: "Runtime settings",
  };
  $("page-title").textContent = titles[tabName] || "System overview";
  renderPageSubtitle();
  closeNav();
  if (tabName === "admin") loadAudit();
  if (tabName === "settings") loadSettings();
}

async function refresh() {
  if (refresh.inFlight) return;
  refresh.inFlight = true;
  const button = $("refresh");
  const startedAt = Date.now();
  if (button) {
    button.classList.add("is-refreshing");
    button.setAttribute("aria-busy", "true");
  }
  try {
    const snapshot = await api("/api/status/summary");
    state.snapshot = snapshot;
    renderSourcePill(snapshot);
    renderSnapshot(snapshot);
    renderPageSubtitle();
    renderMonitorRestore();
  } catch (error) {
    showNotice(error.message, "error");
  } finally {
    // Keep the spinner visible long enough to read, even on a fast LAN.
    const minSpinMs = 500;
    const remaining = minSpinMs - (Date.now() - startedAt);
    const stop = () => {
      refresh.inFlight = false;
      if (button) {
        button.classList.remove("is-refreshing");
        button.removeAttribute("aria-busy");
      }
    };
    if (remaining > 0) setTimeout(stop, remaining);
    else stop();
  }
}

function renderPageSubtitle() {
  if (!hasAdminSurface()) return;
  const snapshot = state.snapshot || {};
  const copy = {
    overview: snapshot.server_summary || "Current status, incidents, and diagnostic facts.",
    server: "Storage, service, and collector health for the server.",
    router: "Internet, DNS, router, and UniFi health.",
    apps: "Search visible apps, review metadata, and use admin-only controls.",
    incidents: "Current incidents and the facts behind them.",
    diagnostics: "Ask the configured diagnosis provider for a structured explanation.",
    admin: "Audit events and a collapsed debug snapshot for troubleshooting.",
    settings: "Configure roles, visibility, integrations, providers, and notifications.",
  };
  $("summary").textContent = copy[state.activeTab] || "Status loaded.";
}

function renderSourcePill(snapshot) {
  const pill = $("source-pill");
  const mode = snapshot.integration_mode || "";
  const scenario = snapshot.fixture_scenario || "";
  if (!mode) {
    pill.hidden = true;
    return;
  }
  pill.hidden = false;
  pill.dataset.mode = mode;
  if (mode === "fixture") {
    pill.textContent = "Fixture data";
    pill.title = scenario ? `Fixture scenario: ${scenario.replace(/_/g, " ")}` : "Fixture data";
    return;
  }
  pill.textContent = mode === "mixed" ? "Mixed live data" : "Live data";
  pill.title = pill.textContent;
}

function renderSnapshot(snapshot) {
  syncAssistant(snapshot);
  if (!hasAdminSurface()) {
    renderUserHome(snapshot);
    return;
  }
  renderOverviewCards(snapshot);
  renderServerHealth(snapshot);
  renderRouterStatus(snapshot);
  renderFacts(snapshot.facts || []);
  renderIncidentStrip(snapshot.incidents || []);
  renderIncidents(snapshot.incidents || []);
  renderApps(snapshot.apps || []);
  $("admin-output").textContent = JSON.stringify(snapshot, null, 2);
}

function syncAssistant(snapshot) {
  const available = diagnosticsAvailable(snapshot);
  const canChat = available && (hasAdminSurface() || canUseCompactChat(snapshot));
  const dock = $("assistant-dock");
  dock.hidden = !canChat;
  document.body.classList.toggle("assistant-available", canChat && hasAdminSurface());
  if (!canChat) $("assistant-panel").hidden = true;
  $("assistant-input").disabled = !canChat;
  $("assistant-send").disabled = !canChat;
  if (!canChat) {
    setChatNotice($("assistant-output"), available ? "Status chat is disabled for this role." : diagnosticsUnavailableMessage(snapshot));
  } else if ($("assistant-output").classList.contains("chat-unavailable")) {
    resetChatPlaceholder($("assistant-output"), "Ask me for help!");
  }
}

function renderUserHome(snapshot) {
  const infra = snapshot.infrastructure || {};
  const apps = snapshot.apps || [];
  const hero = compactHero(snapshot);
  const available = diagnosticsAvailable(snapshot);
  const canChat = available && canUseCompactChat(snapshot);
  const userChatPanel = document.querySelector(".panel.user-chat");
  renderUserHero(hero);
  $("summary").textContent = hero.explanation || hero.headline;
  $("user-primary-actions").dataset.chatAvailable = canChat ? "true" : "false";
  if ($("user-chat-open")) $("user-chat-open").hidden = !canChat;
  if (userChatPanel) {
    userChatPanel.hidden = !canChat;
    userChatPanel.dataset.chatAvailable = canChat ? "true" : "false";
  }
  $("user-chat-input").placeholder = "Ask what's wrong or whether an app is working.";
  $("user-chat-input").disabled = !canChat;
  $("user-chat-send").disabled = !canChat;
  if (!canChat) {
    if (!$("user-chat-input").value.trim()) $("user-chat-input").value = "What is wrong right now?";
    setChatNotice($("user-chat-output"), "Status chat is not available.");
  } else if ($("user-chat-output").classList.contains("chat-unavailable")) {
    if (!$("user-chat-input").value.trim()) $("user-chat-input").value = "What is wrong right now?";
    resetChatPlaceholder($("user-chat-output"), "Ask what's wrong or whether an app is working.");
  }
  const statusCards = [
    userStatusCard("Overall", snapshot.overall_status || "unknown", compactOverallSummary(snapshot)),
  ];
  if (snapshot.visibility?.show_nas_status_to_users !== false) {
    statusCards.push(userStatusCard("Server", serverRollupStatus(infra), compactServerSummary(infra)));
  }
  if (snapshot.visibility?.show_wan_status_to_users !== false) {
    statusCards.push(userStatusCard("Router", routerRollupStatus(infra), compactRouterSummary(infra)));
  }
  $("user-status-grid").replaceChildren(...statusCards);
  $("user-app-count").textContent = `${apps.length} app${apps.length === 1 ? "" : "s"}`;
  if (!apps.length) {
    $("user-apps").replaceChildren(node("div", { class: "empty", text: "No selected apps are visible right now." }));
    if (isUserMenuOpen()) renderUserDrawer();
    return;
  }
  $("user-apps").replaceChildren(...apps.map(renderUserAppCard));
  if (isUserMenuOpen()) renderUserDrawer();
}

function renderUserDrawer() {
  const drawer = $("user-drawer");
  const body = $("user-drawer-body");
  const title = $("user-drawer-title");
  if (!drawer || !body || !title) return;
  const active = compactDrawerSections.find((section) => section.id === state.userDrawerActiveSection) || compactDrawerSections[0];
  state.userDrawerActiveSection = active?.id || "";
  title.textContent = compactDrawerSections.length === 1 ? (active?.label || "Settings") : "Menu";
  if (!active) {
    body.replaceChildren();
    return;
  }
  if (compactDrawerSections.length === 1) {
    const content = node("div", { class: "user-drawer-content", "data-drawer-section": active.id });
    active.render(content);
    body.replaceChildren(content);
    return;
  }
  const nav = node("nav", { class: "user-drawer-nav", "aria-label": "Menu" },
    compactDrawerSections.map((section) => node("button", {
      type: "button",
      class: section.id === active.id ? "active" : "",
      "aria-current": section.id === active.id ? "page" : null,
      onclick: () => {
        state.userDrawerActiveSection = section.id;
        renderUserDrawer();
      },
    },
      node("span", { "aria-hidden": "true", text: section.glyph }),
      node("span", { text: section.label }),
    )),
  );
  const content = node("div", { class: "user-drawer-content", "data-drawer-section": active.id });
  active.render(content);
  body.replaceChildren(nav, content);
}

function renderCompactSettings(container) {
  const apps = state.snapshot?.apps || [];
  const notificationSection = node("section", { class: "user-settings-section" },
    node("h3", { text: "Notifications" }),
    node("p", { class: "user-settings-intro", text: "Get a heads-up when something stops working." }),
    renderCompactNotificationList(apps),
  );
  const displayName = state.user?.display_name || state.user?.username || "this account";
  const accountSection = node("section", { class: "user-settings-section" },
    node("h3", { text: "Account" }),
    node("p", { class: "user-account-line" },
      document.createTextNode("Signed in as "),
      node("strong", { text: displayName }),
    ),
    node("button", {
      id: "user-drawer-logout",
      type: "button",
      class: "command user-signout",
      "data-glyph": "x",
      onclick: () => {
        closeUserMenu({ returnFocus: false });
        logout();
      },
      text: "Sign out",
    }),
  );
  container.replaceChildren(notificationSection, accountSection);
}

function renderCompactNotificationList(apps) {
  if (!apps.length) {
    return node("div", { class: "user-notification-list" },
      node("p", { class: "empty user-settings-empty", text: "No apps to notify about yet." }),
    );
  }
  const rows = apps.map((app, index) => renderCompactNotificationRow(app, index));
  return node("div", { class: "user-notification-list" },
    state.notificationPreferencesLoading ? node("p", { class: "user-settings-loading", text: "Loading settings..." }) : null,
    ...rows,
  );
}

function renderCompactNotificationRow(app, index) {
  const appID = String(app.app_id || "").trim();
  const appName = appDisplayName(app);
  const pref = state.notificationPreferences.get(appID);
  const enabled = !!(pref?.notify_on_down || pref?.notify_on_recovery);
  const inputID = `user-notify-${index}`;
  const input = node("input", {
    id: inputID,
    type: "checkbox",
    "aria-label": `Tell me if ${appName} has a problem`,
  });
  input.checked = enabled;
  input.disabled = !appID || state.notificationPreferencesLoading;
  const stateText = node("span", { class: "user-switch-state", text: enabled ? "On" : "Off" });
  const row = node("div", { class: "user-notification-row" },
    node("label", { class: "user-notification-label", for: inputID },
      renderAppLogo(app),
      node("span", { class: "user-notification-copy" },
        document.createTextNode("Tell me if "),
        node("span", { "data-app-name": "", text: appName }),
        document.createTextNode(" has a problem"),
      ),
      node("span", { class: "user-switch" },
        input,
        node("span", { class: "user-switch-track", "aria-hidden": "true" },
          node("span", { class: "user-switch-knob" }),
          stateText,
        ),
      ),
    ),
    node("p", { class: "user-settings-error", role: "alert" }),
  );
  input.addEventListener("change", () => updateCompactNotificationPreference(app, input, row, stateText));
  return row;
}

async function loadCompactNotificationPreferences() {
  state.notificationPreferencesLoading = true;
  if (isUserMenuOpen()) renderUserDrawer();
  try {
    const prefs = await api("/api/user/notification-preferences");
    state.notificationPreferences = new Map((Array.isArray(prefs) ? prefs : []).map((pref) => [String(pref.app_id || ""), pref]));
    state.notificationPreferencesLoaded = true;
  } catch (error) {
    showNotice(error.message, "error");
  } finally {
    state.notificationPreferencesLoading = false;
    if (isUserMenuOpen()) renderUserDrawer();
  }
}

async function updateCompactNotificationPreference(app, input, row, stateText) {
  const appID = String(app.app_id || "").trim();
  if (!appID) return;
  const enabled = input.checked;
  const previous = !enabled;
  const error = row.querySelector(".user-settings-error");
  input.disabled = true;
  row.dataset.saving = "true";
  if (error) error.textContent = "";
  setCompactSwitchState(input, stateText, enabled);
  try {
    const saved = await api("/api/user/notification-preferences", {
      method: "POST",
      body: JSON.stringify({ app_id: appID, notify_on_down: enabled, notify_on_recovery: enabled }),
    });
    state.notificationPreferences.set(appID, saved);
  } catch {
    input.checked = previous;
    setCompactSwitchState(input, stateText, previous);
    if (error) error.textContent = "Couldn't save that \u2014 try again.";
  } finally {
    input.disabled = false;
    delete row.dataset.saving;
  }
}

function setCompactSwitchState(input, stateText, enabled) {
  input.checked = enabled;
  if (stateText) stateText.textContent = enabled ? "On" : "Off";
}

function renderUserHero(hero) {
  const element = $("user-hero");
  element.className = `user-hero ${hero.tone}`;
  element.setAttribute("aria-label", `${hero.headline}. ${hero.explanation} ${hero.nextStep || ""}`.trim());
  element.replaceChildren(
    node("div", { class: "user-hero-art" }, statusArtwork(hero.icon)),
    node("div", { class: "user-hero-copy" },
      node("span", { class: "user-hero-state", text: hero.status }),
      node("h2", {}, ...heroNameNodes(hero.headline, hero.appName)),
      node("p", { text: hero.explanation }),
    ),
    hero.nextStep ? node("p", { class: "user-hero-next" }, ...heroNameNodes(hero.nextStep, hero.appName)) : null,
  );
}

// Render text that may contain an app display name (a proper noun), wrapping the name in a
// [data-app-name] span so the literacy audit skips it (names can contain banned substrings,
// e.g. "UniFi Controller").
function heroNameNodes(text, appName) {
  if (!appName || !text.includes(appName)) return [document.createTextNode(text)];
  const segments = text.split(appName);
  const out = [];
  segments.forEach((segment, index) => {
    if (segment) out.push(document.createTextNode(segment));
    if (index < segments.length - 1) out.push(node("span", { "data-app-name": "", text: appName }));
  });
  return out;
}

function compactHero(snapshot) {
  const infra = snapshot.infrastructure || {};
  const apps = snapshot.apps || [];
  const appProblems = apps.filter(isProblemApp);
  if (infra.nas_reachable === false) {
    return {
      tone: "problem",
      status: "Problem",
      icon: "server",
      headline: "Can't reach the home server",
      explanation: "The home server isn't responding right now.",
      nextStep: "Wait a few minutes; if it doesn't come back, tell the admin.",
    };
  }
  if (infra.internet_reachable === false && infra.nas_reachable === true && !hasHomeServerProblem(infra)) {
    return {
      tone: "warning",
      status: "Problem",
      icon: "router",
      headline: "The internet looks down",
      explanation: "Your home server is fine - the internet connection isn't working.",
      nextStep: "This is usually your internet provider. Check the modem/router or wait. You don't need to touch the server.",
    };
  }
  if (hasHomeServerProblem(infra)) {
    return {
      tone: "problem",
      status: "Problem",
      icon: "server",
      headline: "The home server has a problem",
      explanation: "Storage or apps aren't running correctly right now.",
      nextStep: "Tell the admin. Avoid changing settings.",
    };
  }
  if (appProblems.length === 1) {
    const name = appDisplayName(appProblems[0]);
    return {
      tone: "warning",
      status: "Problem",
      icon: "apps",
      headline: `${name} isn't working`,
      explanation: "Everything else looks fine.",
      nextStep: `Tell the admin if you need ${name}.`,
      appName: name,
    };
  }
  if (appProblems.length > 1) {
    return {
      tone: "warning",
      status: "Problem",
      icon: "apps",
      headline: "Some apps aren't working",
      explanation: `${appProblems.length} apps are down; the rest are fine.`,
      nextStep: "Tell the admin if you need them.",
    };
  }
  // No problems detected. If the snapshot has loaded, reassure; only show "Checking..."
  // before the first status arrives (so an app with an unknown status doesn't get stuck on
  // a transient-looking message).
  const loaded = Boolean(snapshot.overall_status) || apps.length > 0 || Boolean(infra.last_checked_at);
  if (loaded) {
    return {
      tone: "ok",
      status: "Working",
      icon: "overall",
      headline: "Everything's working",
      explanation: "Nothing is reporting a problem right now.",
      nextStep: "Nothing to do.",
    };
  }
  return {
    tone: "neutral",
    status: "Checking",
    icon: "overall",
    headline: "Checking...",
    explanation: "Getting the latest status.",
    nextStep: "",
  };
}

function hasHomeServerProblem(infra) {
  const arrayState = String(infra.unraid_array_state || "").toLowerCase();
  return infra.unraid_api_reachable === false ||
    (arrayState && arrayState !== "started") ||
    infra.unraid_array_healthy === false ||
    infra.docker_service_available === false;
}

function isProblemApp(app) {
  const status = normalizeStatus(app.current_status);
  return status === "offline" || status === "degraded";
}

function appDisplayName(app) {
  return String(app.display_name || app.app_id || "An app").trim();
}

function diagnosticsAvailable(snapshot) {
  return snapshot.diagnostics_available !== false;
}

function diagnosticsUnavailableMessage(snapshot) {
  const provider = snapshot.diagnostics_provider || "disabled";
  if (provider === "openai") return "Status chat requires OPENAI_API_KEY.";
  if (provider === "anthropic") return "Status chat requires ANTHROPIC_API_KEY.";
  return "Status chat requires OpenAI or Anthropic setup.";
}

function setChatNotice(output, message) {
  output.classList.remove("chat-empty", "chat-result");
  output.classList.add("chat-unavailable", "muted");
  output.textContent = message;
}

function resetChatPlaceholder(output, message) {
  output.classList.remove("chat-unavailable", "chat-result");
  output.classList.add("chat-empty", "muted");
  output.textContent = message;
}

function userStatusCard(label, value, summary) {
  const status = normalizeStatus(value);
  return node("article", {
    class: `user-status-card status-row ${status}`,
    "data-status-kind": statusIconClass(label),
    "aria-label": `${label}: ${value}. ${summary || ""}`,
  },
    statusArtwork(label),
    node("div", { class: "status-copy" },
      node("span", { class: "status-title-line" },
        statusIndicator(value, status, "status-dot-only"),
        node("span", { class: "overview-label", text: label }),
      ),
      node("p", { class: "status-note", text: summary || "" }),
    ),
  );
}

function renderUserAppCard(app) {
  const status = normalizeStatus(app.current_status);
  const statusText = plainAppStatus(app);
  return node("article", { class: `user-app-card app-row ${status}`, "aria-label": `${appDisplayName(app)}: ${statusText}` },
    renderAppLogo(app),
    node("div", { class: "user-app-main" },
      node("div", { class: "app-title-line" },
        node("h3", { "data-app-name": "", text: appDisplayName(app) }),
        statusIndicator(statusText, status, "compact-app-status"),
      ),
      node("p", { class: "muted", text: plainAppSummary(app) }),
    ),
  );
}

function plainAppStatus(app) {
  const status = normalizeStatus(app.current_status);
  if (status === "online") return "Working";
  if (status === "offline") return "Not working";
  if (status === "degraded") return "Problem";
  return "Unknown";
}

function plainAppSummary(app) {
  const status = normalizeStatus(app.current_status);
  if (status === "online") return "Working normally.";
  if (status === "offline") return "Not responding.";
  if (status === "degraded") return "Having a problem.";
  return "Status unknown.";
}

function compactAppSummary(app) {
  const name = String(app.display_name || app.app_id || "").trim();
  const fallback = app.current_status ? `Status ${app.current_status}.` : "No summary available.";
  let summary = String(app.server_summary || app.admin_summary || fallback).trim();
  if (!summary) return fallback;
  if (name) {
    const lower = summary.toLowerCase();
    const nameLower = name.toLowerCase();
    for (const prefix of [`${nameLower} is `, `${nameLower}: `, `${nameLower} `]) {
      if (lower.startsWith(prefix)) {
        summary = summary.slice(prefix.length).trim();
        break;
      }
    }
  }
  if (!summary) return fallback;
  return summary.charAt(0).toUpperCase() + summary.slice(1);
}

function compactOverallSummary(snapshot) {
  return snapshot.server_summary || "Status loaded.";
}

function compactServerSummary(infra) {
  if (!infra.nas_reachable) return "Not reachable from this dashboard.";
  if (!infra.unraid_api_reachable) return "Reachable; Unraid API unavailable.";
  if (infra.unraid_array_state && infra.unraid_array_healthy === false) return `Array ${infra.unraid_array_state}; storage needs attention.`;
  if (infra.docker_service_available === false) return "Reachable; Docker unavailable.";
  const array = infra.unraid_array_state ? `Array ${infra.unraid_array_state}` : "Array status unknown";
  const docker = infra.docker_service_available ? "Docker running" : "Docker status unknown";
  return `${array}; ${docker}.`;
}

function compactRouterSummary(infra) {
  const routerProbeKnown = hasProbeData(infra, "router");
  const internetProbeKnown = hasProbeData(infra, "internet");
  const dnsProbeKnown = hasProbeData(infra, "dns");
  const unifiKnown = hasCollectorData(infra, "unifi");
  if (!routerProbeKnown && !internetProbeKnown && !dnsProbeKnown && !unifiKnown) return "No live network probe data.";
  if (routerProbeKnown && !infra.router_reachable) return "Gateway unreachable from this dashboard.";
  if (internetProbeKnown && !infra.internet_reachable) return "Local gateway reachable; internet check failed.";
  if (dnsProbeKnown && !infra.dns_ok) return "DNS check failed.";
  if (unifiKnown && !infra.unifi_gateway_reachable) return "UniFi gateway unavailable.";
  if (unifiKnown && infra.unifi_wan_up === false) return "UniFi reports WAN down.";
  return "Internet, DNS, and UniFi responding.";
}

function renderOverviewCards(snapshot) {
  const infra = snapshot.infrastructure || {};
  const apps = snapshot.apps || [];
  const offlineApps = apps.filter((app) => app.current_status === "offline").length;
  const degradedApps = apps.filter((app) => app.current_status === "degraded").length;
  const serverStatus = serverRollupStatus(infra);
  const routerStatus = routerRollupStatus(infra);
  const items = [
    {
      title: "Overall",
      icon: "status-icon-overall",
      value: snapshot.overall_status || "unknown",
      summary: snapshot.server_summary || "Status loaded.",
      rows: [["Incidents", `${(snapshot.incidents || []).length}`, "metric"], ["Facts", `${(snapshot.facts || []).length}`, "metric"]],
      tab: "incidents",
      monitorID: "overview.overall",
    },
    {
      title: "Server",
      icon: "status-icon-server",
      value: serverStatus,
      summary: serverSummary(infra),
      rows: [["Array", infra.unraid_array_state || "unknown", "status"], ["Docker", boolStatus(infra.docker_service_available), "status"], ["NAS Link", linkStatus(infra), "status"]],
      tab: "server",
      monitorID: "overview.server",
    },
    {
      title: "Router",
      icon: "status-icon-router",
      value: routerStatus,
      summary: routerSummary(infra),
      rows: [["Internet", probeStatus(infra, "internet", infra.internet_reachable), "status"], ["UniFi WAN", boolStatus(infra.unifi_wan_up), "status"], ["DNS", probeStatus(infra, "dns", infra.dns_ok), "status"]],
      tab: "router",
      monitorID: "overview.router",
    },
    {
      title: "Apps",
      icon: "status-icon-apps",
      value: appsRollupStatus(apps),
      summary: appsSummary(apps, offlineApps, degradedApps),
      rows: [["Total", `${apps.length}`, "metric"], ["Offline", `${offlineApps}`, "metric"], ["Degraded", `${degradedApps}`, "metric"]],
      tab: "apps",
      monitorID: "overview.apps",
    },
  ];
  const byID = new Map(items.map((item) => [item.monitorID, item]));
  const visibleItems = normalizeOverviewOrder(state.overviewOrder)
    .map((id) => byID.get(id))
    .filter((item) => item && !state.hiddenMonitors.has(item.monitorID));
  renderOverviewRearrangeButton();
  $("overview-cards").replaceChildren(...visibleItems.map((item) => overviewCard(item)));
}

function renderServerHealth(snapshot) {
  const infra = snapshot.infrastructure || {};
  const rows = [
    ["server.nas", "NAS", boolStatus(infra.nas_reachable), "Dashboard host can reach the server"],
    ["server.unraid-api", "Unraid API", boolStatus(infra.unraid_api_reachable), "GraphQL read API"],
    ["server.array", "Array", infra.unraid_array_state || "unknown", infra.unraid_array_healthy ? "Storage array healthy" : "Storage array needs attention"],
    ["server.docker", "Docker", boolStatus(infra.docker_service_available), "Container service"],
    ["server.nas-link", "NAS Link", linkStatus(infra), linkNote(infra)],
  ];
  $("server-health-grid").replaceChildren(...rows.map(([id, label, value, note]) => statusListRow(id, label, value, note)).filter(Boolean));
  const warnings = infra.storage_warnings?.length ? infra.storage_warnings.join("; ") : "No storage warnings";
  $("server-detail-grid").replaceChildren(...[
    detailSection("server.collectors", "Collectors", [
      detailRow("Unraid", sourceHealth(infra, "unraid")),
      detailRow("Docker", sourceHealth(infra, "docker")),
      detailRow("Checked", formatTime(infra.last_checked_at)),
    ]),
    detailSection("server.storage", "Storage", [
      detailRow("Version", infra.unraid_version || "No version data"),
      detailRow("Disks", infra.array_disk_count ? `${infra.array_disk_count} disk${infra.array_disk_count === 1 ? "" : "s"} (${infra.array_disk_warning_count || 0} warning${infra.array_disk_warning_count === 1 ? "" : "s"})` : "No disk data"),
      detailRow("Capacity", infra.array_capacity_total_bytes ? `${formatBytes(infra.array_capacity_used_bytes)} used of ${formatBytes(infra.array_capacity_total_bytes)} (${formatPercent(infra.array_capacity_used_pct)})` : "No capacity data"),
      detailRow("Parity", infra.parity_check_state || "No parity check data"),
      detailRow("Warnings", warnings),
    ]),
  ].filter(Boolean));
}

function renderRouterStatus(snapshot) {
  const infra = snapshot.infrastructure || {};
  const probesKnown = hasCollectorData(infra, "probes");
  const rows = [
    ["router.internet", "Internet", probeStatus(infra, "internet", infra.internet_reachable), probesKnown ? "External HTTPS probe" : "No network probe data"],
    ["router.gateway", "Router", probeStatus(infra, "router", infra.router_reachable), probesKnown ? "Gateway reachability" : "No network probe data"],
    ["router.unifi-gateway", "UniFi Gateway", boolStatus(infra.unifi_gateway_reachable), "UniFi integration API"],
    ["router.unifi-wan", "UniFi WAN", boolStatus(infra.unifi_wan_up), "WAN state reported by UniFi"],
    ["router.dns", "DNS", probeStatus(infra, "dns", infra.dns_ok), probesKnown ? "Name resolution" : "No network probe data"],
    ["router.nas-link-status", "NAS Link", linkStatus(infra), linkNote(infra)],
  ];
  $("router-status-grid").replaceChildren(...rows.map(([id, label, value, note]) => statusListRow(id, label, value, note)).filter(Boolean));
  $("router-detail-grid").replaceChildren(...[
    detailSection("router.collectors", "Collectors", [
      detailRow("UniFi", sourceHealth(infra, "unifi")),
      detailRow("Probes", sourceHealth(infra, "probes")),
      detailRow("Checked", formatTime(infra.last_checked_at)),
    ]),
    detailSection("router.unifi", "UniFi", [
      detailRow("Site", infra.unifi_site_name || infra.unifi_site_id || "No site data"),
      detailRow("Devices", infra.unifi_device_count ? `${infra.unifi_device_count} device${infra.unifi_device_count === 1 ? "" : "s"} (${infra.unifi_offline_device_count || 0} offline)` : "No device data"),
      detailRow("Clients", Number.isFinite(Number(infra.unifi_client_count)) ? `${infra.unifi_client_count}` : "No client data"),
      detailRow("Updates", Number.isFinite(Number(infra.unifi_firmware_updates)) ? `${infra.unifi_firmware_updates}` : "No firmware data"),
      detailRow("WANs", Number.isFinite(Number(infra.unifi_wan_count)) ? `${infra.unifi_wan_count}` : "No WAN data"),
      detailRow("Warnings", infra.unifi_warnings?.length ? infra.unifi_warnings.join("; ") : "No UniFi warnings"),
    ]),
    detailSection("router.nas-link", "NAS Link", [
      detailRow("Expected", infra.expected_nas_link_mbps ? `${infra.expected_nas_link_mbps} Mbps` : "No expectation configured"),
      detailRow("Negotiated", infra.nas_link_speed_mbps ? `${infra.nas_link_speed_mbps} Mbps` : "No link data"),
    ]),
  ].filter(Boolean));
}

function overviewCard(item) {
  const status = normalizeStatus(item.value);
  const attrs = {
    class: `overview-card action-row monitor-shell ${status}${state.overviewRearrange ? " rearrange-ready" : ""}`,
    "data-overview-id": item.monitorID,
    "aria-label": `${item.title}: ${item.value}. ${item.summary || ""}`,
    draggable: state.overviewRearrange ? "true" : "false",
    role: state.overviewRearrange ? "listitem" : "button",
    tabindex: "0",
    onpointerdown: (event) => startOverviewTouchDrag(event, item.monitorID),
    onpointermove: (event) => moveOverviewTouchDrag(event, item.monitorID),
    onpointerup: (event) => finishOverviewTouchDrag(event, item.monitorID),
    onpointercancel: (event) => finishOverviewTouchDrag(event, item.monitorID),
    ondragstart: (event) => {
      if (!state.overviewRearrange) {
        event.preventDefault();
        return;
      }
      event.dataTransfer?.setData("text/plain", item.monitorID);
      event.dataTransfer.effectAllowed = "move";
      event.currentTarget.classList.add("dragging");
    },
    ondragend: (event) => {
      event.currentTarget.classList.remove("dragging");
    },
    ondragover: (event) => {
      if (!state.overviewRearrange) return;
      event.preventDefault();
    },
    ondrop: (event) => {
      if (!state.overviewRearrange) return;
      event.preventDefault();
      dropOverviewCard(event.dataTransfer?.getData("text/plain"), item.monitorID);
      showNotice("Overview order saved.");
    },
  };
  if (!state.overviewRearrange) attrs["data-tab-jump"] = item.tab;
  return visibleMonitor(item.monitorID, node("article", attrs,
    statusArtwork(item.icon || item.title),
    node("div", { class: "overview-main" },
      node("span", { class: "status-title-line" },
        statusIndicator(item.value, status, "status-dot-only"),
        node("span", { class: "overview-label", text: item.title }),
      ),
      node("p", { class: "overview-summary status-note", text: item.summary }),
    ),
    node("div", { class: "overview-meta" }, ...item.rows.map(([label, rowValue, type]) => overviewMetric(label, rowValue, type))),
    node("div", { class: "overview-actions" },
      node("span", { class: "overview-drag-handle", "aria-hidden": "true", text: "Drag" }),
      monitorHideButton(item.monitorID),
    ),
  ));
}

function overviewMetric(label, value, type = "metric") {
  const status = normalizeStatus(value);
  const statusish = type === "status";
  const showValue = !statusish || statusValueNeedsText(value);
  return node("span", { class: `overview-metric${statusish ? ` overview-metric-status ${status}` : ""}` },
    statusish ? statusIndicator(value, status, "status-dot-only") : null,
    node("span", { text: label }),
    showValue ? node("strong", { text: value }) : null,
  );
}

function statusListRow(id, label, value, note) {
  const status = normalizeStatus(value);
  return visibleMonitor(id, node("article", { class: `status-list-row status-row monitor-shell ${status}`, "aria-label": `${label}: ${value}. ${note || ""}` },
    node("div", { class: "status-row-main" },
      statusIndicator(value, status, "status-dot-only"),
      node("div", { class: "status-copy" },
        node("span", { class: "status-row-label", text: label }),
        node("p", { class: "status-note", text: note || "" }),
      ),
    ),
    node("span", { class: "status-value", text: value || "unknown" }),
  ));
}

function detailRow(label, value) {
  return node("div", { class: "detail-row" },
    node("span", { class: "meta-label", text: label }),
    node("strong", { text: value || "unknown" }),
  );
}

function detailSection(id, title, children) {
  const childNodes = children.filter(Boolean);
  return visibleMonitor(id, node("section", { class: "detail-section monitor-shell" },
    node("h3", { text: title }),
    node("div", { class: "detail-list" }, childNodes),
  ));
}

function renderFacts(facts) {
  $("fact-count").textContent = `${facts.length} fact${facts.length === 1 ? "" : "s"}`;
  if (!facts.length) {
    $("facts").replaceChildren(node("div", { class: "empty", text: "No diagnostic facts are active." }));
    return;
  }
  $("facts").replaceChildren(...facts.map((fact) => node("article", { class: "fact-row" },
    node("header", {},
      node("h3", { text: fact.summary || fact.type }),
      node("span", { class: `severity ${fact.severity || "none"}`, text: fact.severity || "none" }),
    ),
    fact.affected_services?.length ? node("p", { class: "muted", text: `Affected: ${fact.affected_services.join(", ")}` }) : null,
  )));
}

function renderIncidentStrip(incidents) {
  if (!incidents.length) {
    $("incident-strip").replaceChildren(node("div", { class: "empty", text: "No current incidents." }));
    return;
  }
  $("incident-strip").replaceChildren(...incidents.slice(0, 3).map(renderIncidentSummaryRow));
}

function renderIncidents(incidents) {
  $("incident-count").textContent = `${incidents.length} incident${incidents.length === 1 ? "" : "s"}`;
  if (!incidents.length) {
    $("incident-list").replaceChildren(node("div", { class: "empty", text: "No incidents are active in this snapshot." }));
    return;
  }
  $("incident-list").replaceChildren(...incidents.map(renderIncidentCard));
}

function renderIncidentCard(incident) {
  return node("article", { class: "incident-card" },
    node("header", {},
      node("div", {},
        node("h3", { text: incident.summary || incident.type }),
        node("p", { class: "muted", text: incident.id || incident.type }),
      ),
      node("span", { class: `severity ${incident.severity || "none"}`, text: incident.severity || "none" }),
    ),
    incident.affected_services?.length ? node("p", { text: `Affected services: ${incident.affected_services.join(", ")}` }) : null,
    incident.evidence?.length ? evidenceChips(incident.evidence) : null,
  );
}

function evidenceChips(evidence) {
  const chips = [];
  for (const item of evidence || []) {
    const parts = String(item || "").split(/\s+/).filter(Boolean);
    for (const part of parts) {
      const index = part.indexOf("=");
      if (index <= 0) {
        chips.push(["Evidence", part]);
      } else {
        chips.push([part.slice(0, index), part.slice(index + 1)]);
      }
    }
  }
  return node("div", { class: "evidence-chips" }, chips.map(([label, value]) => node("span", { class: "evidence-chip" },
    node("strong", { text: label }),
    node("span", { text: value || "unknown" }),
  )));
}

function renderIncidentSummaryRow(incident) {
  const details = [
    incident.id || incident.type,
    incident.affected_services?.length ? `Affected: ${incident.affected_services.join(", ")}` : "",
    incident.evidence?.length ? incident.evidence[0] : "",
  ].filter(Boolean).join(" - ");
  return node("article", { class: "incident-card incident-row" },
    node("header", {},
      node("h3", { text: incident.summary || incident.type }),
      node("span", { class: `severity ${incident.severity || "none"}`, text: incident.severity || "none" }),
    ),
    details ? node("p", { class: "muted", text: details }) : null,
  );
}

function renderApps(apps) {
  const search = state.appSearch.trim().toLowerCase();
  const visible = apps.filter((app) => {
    const statusMatch = state.appFilter === "all" || app.current_status === state.appFilter;
    const text = [app.display_name, app.app_id, app.current_status, app.image_ref].join(" ").toLowerCase();
    return statusMatch && (!search || text.includes(search));
  });
  if (!visible.length) {
    $("apps").replaceChildren(node("div", { class: "empty", text: "No apps match the current filter." }));
    return;
  }
  $("apps").replaceChildren(...visible.map(renderAppCard));
}

function renderAppCard(app) {
  const meta = appMeta(app);
  const status = normalizeStatus(app.current_status);
  const actions = [];
  if (state.user.role === "admin" && isDockerApp(app)) {
    actions.push(...dockerControlButtons(app));
  }
  actions.push(node("button", {
    type: "button",
    class: "command app-image-action",
    "data-glyph": app.icon_url ? "i" : "+",
    title: app.icon_url ? "Change app image" : "Add app image",
    "aria-label": app.icon_url ? "Change app image" : "Add app image",
    onclick: () => setAppIcon(app),
    text: app.icon_url ? "Image" : "Add image",
  }));
  if (app.notification_opt_in_allowed && state.user.role !== "admin") {
    actions.push(node("button", { type: "button", class: "command", "data-glyph": "!", onclick: () => savePreference(app.app_id), text: "Notify me" }));
  }
  return node("article", { class: `app-card app-row ${status}` },
    node("header", {},
      node("div", { class: "app-card-title" },
        renderAppLogo(app),
        node("div", {},
          node("div", { class: "app-title-line" },
            statusIndicator(app.current_status || "unknown", status, "status-dot-only"),
            node("h3", { text: app.display_name || app.app_id }),
          ),
          node("p", { class: "muted", text: appSubtitle(app) }),
        ),
      ),
      node("div", { class: "app-row-actions" },
        ...actions,
      ),
    ),
    node("p", { class: "app-summary", text: compactAppSummary(app) }),
    meta.length ? node("dl", { class: "app-meta-list" }, meta.map(([label, value]) => node("div", { class: "app-meta-item" },
      node("dt", { text: label }),
      node("dd", { title: value, text: value }),
    ))) : null,
  );
}

function appSubtitle(app) {
  const image = (app.image_ref || "").trim();
  if (image) return image;
  if (app.docker_state && app.docker_state !== "unknown") return `Docker ${app.docker_state}`;
  if (app.web_url) return "Web UI configured";
  if (app.data_source === "unraid-docker") return "Docker app";
  return app.current_status ? `Status ${app.current_status}` : "App";
}

function appMeta(app) {
  const values = [];
  if (app.docker_state && app.docker_state !== "unknown") values.push(["Docker", app.docker_state]);
  if (app.docker_health && app.docker_health !== "unknown") values.push(["Health", app.docker_health]);
  if (app.endpoint_status && !["unknown", "skipped"].includes(app.endpoint_status)) values.push(["Endpoint", app.endpoint_status]);
  if (state.user.role === "admin" && app.image_ref) values.push(["Image", app.image_ref]);
  if (state.user.role === "admin" && app.web_url) values.push(["Web UI", app.web_url]);
  if (state.user.role === "admin" && app.data_source && app.data_source !== "unraid-docker") values.push(["Source", app.data_source]);
  return values;
}

function isDockerApp(app) {
  return !!(app.container_name || app.container_id || app.docker_state || app.data_source === "unraid-docker");
}

function dockerControlButtons(app) {
  const running = app.docker_state === "running";
  return [
    appControlButton(app, "start", "Start", running),
    appControlButton(app, "restart", "Restart", !running),
    appControlButton(app, "stop", "Stop", !running),
  ];
}

function appControlButton(app, action, label, disabled) {
  return node("button", {
    type: "button",
    class: `command app-control app-control-${action}`,
    "data-control": action,
    title: `${label} ${app.display_name || app.app_id}`,
    "aria-label": `${label} ${app.display_name || app.app_id}`,
    disabled,
    onclick: (event) => {
      event.preventDefault();
      if (["restart", "stop"].includes(action) && !confirm(`${label} ${app.display_name || app.app_id}?`)) return;
      runAppAction(app, action, event.currentTarget);
    },
  }, controlIcon(action));
}

function controlIcon(action) {
  const attrs = { viewBox: "0 0 24 24", "aria-hidden": "true", focusable: "false" };
  if (action === "start") {
    return svgNode("svg", attrs, svgNode("path", { class: "control-fill", d: "M7 4.8v14.4L19 12 7 4.8Z" }));
  }
  if (action === "stop") {
    return svgNode("svg", attrs, svgNode("rect", { class: "control-fill", x: "6", y: "6", width: "12", height: "12", rx: "1.8" }));
  }
  return svgNode("svg", attrs,
    svgNode("path", { class: "control-stroke", d: "M7.8 8.2a7 7 0 1 1-1.2 7.6" }),
    svgNode("path", { class: "control-fill", d: "M5.2 4.8v6h6L5.2 4.8Z" }),
  );
}

function statusIndicator(value, status, extraClass = "") {
  return node("span", {
    class: `status ${status}${extraClass ? ` ${extraClass}` : ""}`,
    title: value || "unknown",
    "aria-label": `Status ${value || "unknown"}`,
  }, node("span", { class: "status-text", text: value || "unknown" }));
}

function statusArtwork(label) {
  const icon = statusIconClass(label);
  const attrs = {
    class: `status-artwork ${icon}`,
    viewBox: "0 0 24 24",
    fill: "none",
    stroke: "currentColor",
    "stroke-width": "1.75",
    "stroke-linecap": "round",
    "stroke-linejoin": "round",
    "aria-hidden": "true",
    focusable: "false",
  };
  const children = {
    "status-icon-server": [
      svgNode("rect", { x: "3", y: "4", width: "18", height: "7", rx: "1.8" }),
      svgNode("rect", { x: "3", y: "13", width: "18", height: "7", rx: "1.8" }),
      svgNode("path", { d: "M7 7.5h.01M10.5 7.5H17M7 16.5h.01M10.5 16.5H17" }),
    ],
    "status-icon-router": [
      svgNode("rect", { x: "3.5", y: "14", width: "17", height: "6.5", rx: "1.8" }),
      svgNode("path", { d: "M7.3 17.3h.01M10.7 17.3h.01M15 11v3M8.8 10.3a8 8 0 0 1 8.4 0M6.2 7.1a12 12 0 0 1 15.6 0" }),
    ],
    "status-icon-apps": [
      svgNode("rect", { x: "4", y: "4", width: "7", height: "7", rx: "1.6" }),
      svgNode("rect", { x: "13", y: "4", width: "7", height: "7", rx: "1.6" }),
      svgNode("rect", { x: "4", y: "13", width: "7", height: "7", rx: "1.6" }),
      svgNode("rect", { x: "13", y: "13", width: "7", height: "7", rx: "1.6" }),
    ],
    "status-icon-overall": [
      svgNode("circle", { cx: "12", cy: "12", r: "8.5" }),
      svgNode("path", { d: "m8.4 12.1 2.5 2.5L16.8 9" }),
    ],
  }[icon] || [];
  return svgNode("svg", attrs, ...children);
}

function statusValueNeedsText(value) {
  const normalized = String(value || "").toLowerCase();
  return !["online", "offline", "ok", "failed"].includes(normalized);
}

function statusIconClass(label) {
  const normalized = String(label || "").toLowerCase();
  if (normalized.includes("router")) return "status-icon-router";
  if (normalized.includes("server") || normalized.includes("unraid") || normalized.includes("nas")) return "status-icon-server";
  if (normalized.includes("app")) return "status-icon-apps";
  return "status-icon-overall";
}

async function runAppAction(app, action, button) {
  const appID = app.app_id || app.container_name || app.display_name;
  if (!appID) {
    showNotice("App id is required.", "error");
    return;
  }
  const previous = button?.disabled;
  if (button) button.disabled = true;
  try {
    await api(`/api/admin/apps/${encodeURIComponent(appID)}/action`, {
      method: "POST",
      body: JSON.stringify({ action }),
    });
    showNotice(`${action} requested for ${app.display_name || appID}.`);
    await refresh();
  } catch (error) {
    showNotice(error.message, "error");
  } finally {
    if (button) button.disabled = previous;
  }
}

function renderAppLogo(app) {
  const builtIn = builtInAppIcon(app);
  const iconURL = app.icon_url || builtIn.url;
  const logoTitle = hasAdminSurface()
    ? (app.icon_url ? `Logo from ${app.icon_source || "app metadata"}` : `Built-in ${builtIn.label} icon`)
    : (app.icon_url ? "App image" : "App icon");
  const root = node("div", {
    class: `app-logo logo-missing${app.icon_url ? "" : " app-logo-built-in"}`,
    title: logoTitle,
  });
  const fallback = node("span", { class: "app-logo-fallback", text: appInitials(app.display_name || app.app_id) });
  if (!iconURL) {
    root.append(fallback);
    return root;
  }
  const image = node("img", {
    src: iconURL,
    alt: "",
    loading: "lazy",
    referrerpolicy: "no-referrer",
    onload: () => root.classList.remove("logo-missing"),
    onerror: () => {
      image.hidden = true;
      root.classList.add("logo-missing");
    },
  });
  root.append(image, fallback);
  return root;
}

function builtInAppIcon(app) {
  const text = [
    app.app_id,
    app.display_name,
    app.container_name,
    app.image_ref,
    app.web_url,
    app.template_path,
  ].filter(Boolean).join(" ").toLowerCase();
  for (const rule of BUILTIN_APP_ICON_RULES) {
    if (rule.patterns.some((pattern) => text.includes(pattern))) {
      return { url: `/app-icons/${rule.icon}.svg`, label: rule.label };
    }
  }
  return { url: "/app-icons/container.svg", label: "container app" };
}

function appInitials(value) {
  const words = String(value || "?").trim().split(/[^A-Za-z0-9]+/).filter(Boolean);
  if (!words.length) return "?";
  return words.slice(0, 2).map((word) => word[0]).join("").toUpperCase();
}

async function setAppIcon(app) {
  const current = app.icon_source === "custom" ? app.icon_url : "";
  const value = window.prompt(`Image URL for ${app.display_name || app.app_id}`, current);
  if (value === null) return;
  try {
    await api(`/api/admin/apps/${encodeURIComponent(app.app_id)}/icon`, {
      method: "POST",
      body: JSON.stringify({ icon_url: value.trim() }),
    });
    showNotice(value.trim() ? "App image saved." : "App image override cleared.");
    await refresh();
  } catch (error) {
    showNotice(error.message, "error");
  }
}

async function savePreference(appID) {
  try {
    const saved = await api("/api/user/notification-preferences", {
      method: "POST",
      body: JSON.stringify({ app_id: appID, notify_on_down: true, notify_on_recovery: true }),
    });
    state.notificationPreferences.set(String(appID || ""), saved);
    state.notificationPreferencesLoaded = true;
    showNotice("Notification preference saved.");
    await refresh();
  } catch (error) {
    showNotice(error.message, "error");
  }
}

async function diagnose() {
  await runDiagnosis(
    $("diagnostic-question").value || "What is wrong right now?",
    $("diagnostic-output"),
  );
}

async function runUserChat() {
  await runDiagnosis(
    $("user-chat-input").value || "What is wrong right now?",
    $("user-chat-output"),
  );
}

function focusUserChat() {
  const panel = document.querySelector(".panel.user-chat");
  if (!panel || panel.hidden) return;
  panel.scrollIntoView({ block: "nearest", inline: "nearest", behavior: "smooth" });
  $("user-chat-input").focus({ preventScroll: true });
}

async function runAssistantChat() {
  await runDiagnosis(
    $("assistant-input").value || "What is wrong right now?",
    $("assistant-output"),
  );
  $("assistant-panel").hidden = false;
}

async function runDiagnosis(question, output) {
  const adminSurface = hasAdminSurface();
  const path = adminSurface ? "/api/admin/diagnose" : "/api/user/diagnose";
  try {
    const result = await api(path, {
      method: "POST",
      body: JSON.stringify({ question }),
    });
    output.classList.remove("chat-empty", "muted");
    output.classList.add("chat-result");
    output.replaceChildren(
      node("strong", { text: `${result.severity} confidence ${Math.round((result.confidence || 0) * 100)}%` }),
      node("p", { text: result.diagnosis || result.general_user_summary || "No diagnosis returned." }),
      result.evidence?.length ? node("p", { class: "muted", text: `Evidence: ${result.evidence.join("; ")}` }) : null,
      adminSurface ? node("p", { class: "muted", text: result.admin_message || "" }) : null,
    );
    showNotice("Diagnosis completed.");
  } catch (error) {
    showNotice(error.message, "error");
  }
}

async function notifyAdmin(message) {
  try {
    await api("/api/user/notify-admin", {
      method: "POST",
      body: JSON.stringify({ message: message || $("diagnostic-question").value || "A standard user reported a problem." }),
    });
    showNotice("Admin notification queued.");
  } catch (error) {
    showNotice(error.message, "error");
  }
}

async function loadAudit() {
  if (!hasAdminSurface()) return;
  try {
    const data = await api("/api/admin/audit");
    state.auditEntries = Array.isArray(data) ? [...data].sort((a, b) => String(b.time || "").localeCompare(String(a.time || ""))) : [];
    renderAuditTable();
  } catch (error) {
    state.auditEntries = [];
    $("audit-row-count").textContent = "0 events";
    $("audit-output").replaceChildren(node("div", { class: "empty", text: error.message }));
  }
}

function renderAuditTable() {
  const filter = String($("audit-filter")?.value || "").trim().toLowerCase();
  const entries = state.auditEntries.filter((entry) => auditEntryText(entry).includes(filter));
  $("audit-row-count").textContent = `${entries.length} event${entries.length === 1 ? "" : "s"}`;
  if (!entries.length) {
    $("audit-output").replaceChildren(node("div", { class: "empty", text: filter ? "No audit events match the filter." : "No audit events recorded." }));
    return;
  }
  $("audit-output").replaceChildren(node("table", { class: "audit-table" },
    node("thead", {},
      node("tr", {},
        node("th", { text: "Time" }),
        node("th", { text: "Actor" }),
        node("th", { text: "Action" }),
        node("th", { text: "Details" }),
        node("th", { text: "Redacted" }),
      ),
    ),
    node("tbody", {}, entries.map((entry) => node("tr", {},
      node("td", { text: formatTime(entry.time) }),
      node("td", { text: entry.actor || "unknown" }),
      node("td", { text: entry.action || "unknown" }),
      node("td", {}, auditDetails(entry.details)),
      node("td", {}, node("span", { class: `severity ${entry.redacted ? "medium" : "none"}`, text: entry.redacted ? "Yes" : "No" })),
    ))),
  ));
}

function auditEntryText(entry) {
  return [entry.time, entry.actor, entry.action, JSON.stringify(entry.details || {})].join(" ").toLowerCase();
}

function auditDetails(details) {
  const entries = Object.entries(details || {});
  if (!entries.length) return node("span", { class: "muted", text: "No details" });
  return node("div", { class: "audit-detail-chips" }, entries.map(([key, value]) => node("span", { class: "audit-chip" },
    node("strong", { text: key }),
    node("span", { text: formatAuditValue(value) }),
  )));
}

function formatAuditValue(value) {
  if (value === null || value === undefined) return "empty";
  if (typeof value === "object") return JSON.stringify(value);
  return String(value);
}

async function loadSettings() {
  if (!hasAdminSurface()) return;
  await loadRoleSettings();
  const cards = [];
  for (const item of SETTINGS_ENDPOINTS) {
    try {
      const data = await api(item.path);
      cards.push(renderSettingsCard(item, data));
    } catch (error) {
      cards.push(node("article", { class: "settings-card settings-section", "data-settings-section": item.section }, node("h3", { text: item.title }), node("p", { class: "muted", text: error.message })));
    }
  }
  $("settings-grid").replaceChildren(...cards);
  setSettingsSection(state.settingsSection);
}

function setSettingsSection(section) {
  const validSections = new Set(["roles", ...SETTINGS_ENDPOINTS.map((item) => item.section)]);
  state.settingsSection = validSections.has(section) ? section : "roles";
  document.querySelectorAll("#settings-menu [data-settings-section]").forEach((button) => {
    button.classList.toggle("active", button.dataset.settingsSection === state.settingsSection);
  });
  document.querySelectorAll("#tab-settings .settings-section").forEach((panel) => {
    panel.hidden = panel.dataset.settingsSection !== state.settingsSection;
  });
}

async function loadRoleSettings() {
  try {
    const data = await api("/api/admin/settings/roles");
    state.roleVisibility = clone(data.visibility || {});
    state.roleApps = data.apps || [];
    state.roleUsers = data.users || [];
    state.roleUsersOriginal = clone(data.users || []);
    renderRoleSettings();
  } catch (error) {
    $("role-settings").replaceChildren(node("div", { class: "empty", text: error.message }));
  }
}

function renderRoleSettings() {
  const visibility = state.roleVisibility || {};
  const roles = visibility.roles || [];
  if (!roles.length) {
    $("role-settings").replaceChildren(node("div", { class: "empty", text: "No roles are configured." }));
    return;
  }
  if (!roles.some((role) => role.role === state.selectedRole)) {
    state.selectedRole = visibility.default_role || roles[0].role;
  }
  const selectedRole = roles.find((role) => role.role === state.selectedRole) || roles[0];
  const roleControls = node("section", { class: "role-editor" },
    node("div", { class: "role-editor-head" },
      node("div", {},
        node("h3", { text: "Access Roles" }),
        node("p", { class: "muted", text: `${roles.length} role${roles.length === 1 ? "" : "s"} configured` }),
      ),
      node("label", {},
        "Default role",
        node("select", {
          onchange: (event) => {
            visibility.default_role = event.target.value;
          },
        }, roles.map((role) => node("option", {
          value: role.role,
          selected: role.role === visibility.default_role,
          text: role.display_name || role.role,
        }))),
      ),
    ),
    node("div", { class: "role-workspace" },
      node("nav", { class: "role-sidebar", "aria-label": "Roles" }, roles.map((role) => roleNavItem(role, visibility.default_role))),
      renderRoleDetail(selectedRole, visibility.default_role, roles),
    ),
  );
  const userControls = renderUserManagement(roles);
  $("role-settings").replaceChildren(roleControls, userControls);
}

function roleNavItem(role, defaultRole) {
  const visibleCount = visibleRoleAppCount(role);
  const appCount = state.roleApps.length;
  return node("button", {
    type: "button",
    class: `role-nav-item${role.role === state.selectedRole ? " active" : ""}`,
    "aria-pressed": String(role.role === state.selectedRole),
    onclick: () => {
      state.selectedRole = role.role;
      renderRoleSettings();
    },
  },
    node("span", { class: "role-nav-main" },
      node("strong", { text: role.display_name || role.role }),
      node("small", { text: role.role }),
    ),
    node("span", { class: "role-nav-meta" },
      role.role === defaultRole ? node("span", { class: "role-badge", text: "Default" }) : null,
      node("small", { text: `${visibleCount}/${appCount} apps` }),
    ),
  );
}

function renderRoleDetail(role, defaultRole, roles) {
  const visibleCount = visibleRoleAppCount(role);
  const members = usersForRole(role.role);
  const hiddenCount = Math.max(0, state.roleApps.length - visibleCount);
  return node("article", { class: "role-detail" },
    node("header", { class: "role-detail-head" },
      node("div", {},
        node("h3", { text: role.display_name || role.role }),
        node("p", { class: "muted", text: role.role }),
      ),
      role.role === defaultRole ? node("span", { class: "role-badge", text: "Default" }) : null,
    ),
    node("label", { class: "role-name-field" },
      "Display name",
      node("input", {
        value: role.display_name || "",
        placeholder: role.role,
        oninput: (event) => {
          role.display_name = event.target.value;
          const nav = document.querySelector(`.role-nav-item[aria-pressed="true"] strong`);
          if (nav) nav.textContent = role.display_name || role.role;
        },
      }),
    ),
    node("div", { class: "role-stat-strip" },
      roleStat("Users", members.length),
      roleStat("Visible apps", visibleCount),
      roleStat("Hidden apps", hiddenCount),
    ),
    node("section", { class: "role-permission-section" },
      node("div", { class: "role-section-title" },
        node("h4", { text: "Capabilities" }),
        node("p", { class: "muted", text: "Controls what this role can see or ask for." }),
      ),
      node("div", { class: "role-options" },
        roleFlag(role, "can_use_llm", "Status chat"),
        roleFlag(role, "show_nas_status_to_users", "Server health"),
        roleFlag(role, "show_wan_status_to_users", "Router status"),
        roleFlag(role, "show_incident_ids_to_users", "Incident IDs"),
      ),
    ),
    roleAssignmentSection(role, roles),
    node("div", { class: "role-app-section" },
      node("div", { class: "role-app-head" },
        node("div", { class: "role-section-title" },
          node("h4", { text: "App access" }),
          node("p", { class: "muted", text: `${visibleCount} of ${state.roleApps.length} visible to this role.` }),
        ),
        node("div", { class: "role-app-bulk" },
          node("button", {
            type: "button",
            class: "command",
            "data-glyph": "+",
            onclick: () => {
              role.hidden_app_ids = [];
              renderRoleSettings();
            },
            text: "Show all",
          }),
          node("button", {
            type: "button",
            class: "command",
            "data-glyph": "x",
            onclick: () => {
              role.hidden_app_ids = state.roleApps.map((app) => String(app.app_id || app.container_name || app.display_name).toLowerCase()).filter(Boolean).sort();
              renderRoleSettings();
            },
            text: "Hide all",
          }),
        ),
      ),
      node("div", { class: "role-app-list" }, state.roleApps.map((app) => roleAppToggle(role, app))),
    ),
  );
}

function roleStat(label, value) {
  return node("span", { class: "role-stat" },
    node("span", { text: label }),
    node("strong", { text: String(value) }),
  );
}

function usersForRole(roleName) {
  return assignableRoleUsers().filter((user) => String(user.role || "") === String(roleName || ""));
}

function assignableRoleUsers() {
  return state.roleUsers.filter((user) => String(user.role || "") !== "admin");
}

function accountRoles(roles) {
  return [{ role: "admin", display_name: "Admin" }, ...roles];
}

function roleAssignmentSection(role, roles) {
  const members = usersForRole(role.role);
  const changed = changedRoleAssignmentCount();
  return node("section", { class: "role-member-section" },
    node("div", { class: "role-assignment-head" },
      node("div", { class: "role-section-title" },
        node("h4", { text: "User assignments" }),
        node("p", { class: "muted", text: members.length ? `${members.length} user${members.length === 1 ? "" : "s"} currently use this role.` : "No users currently use this role." }),
      ),
      node("button", {
        type: "button",
        class: "primary command",
        "data-glyph": "v",
        disabled: changed === 0,
        onclick: saveRoleAssignments,
        text: changed ? `Save ${changed}` : "Saved",
      }),
    ),
    node("div", { class: "role-assignment-list" },
      assignableRoleUsers().length ? assignableRoleUsers().map((user) => roleAssignmentRow(user, roles, role.role)) : node("span", { class: "empty inline-empty", text: "No non-admin users" }),
    ),
  );
}

function roleAssignmentRow(user, roles, selectedRole) {
  const assigned = String(user.role || "") === String(selectedRole || "");
  return node("div", { class: `role-assignment-row${assigned ? " assigned" : ""}${user.disabled ? " disabled" : ""}` },
    node("span", { class: "role-assignment-user" },
      node("strong", { text: user.display_name || user.username }),
      node("small", { text: user.disabled ? `${user.username} - disabled` : user.username }),
    ),
    node("select", {
      "aria-label": `Role for ${user.username}`,
      onchange: (event) => {
        user.role = event.target.value;
        renderRoleSettings();
      },
    }, roles.map((role) => node("option", {
      value: role.role,
      selected: String(role.role) === String(user.role),
      text: role.display_name || role.role,
    }))),
  );
}

function changedRoleAssignmentCount() {
  const original = new Map((state.roleUsersOriginal || []).map((user) => [user.username, String(user.role || "")]));
  return assignableRoleUsers().filter((user) => String(user.role || "") !== (original.get(user.username) || "")).length;
}

function visibleRoleAppCount(role) {
  const hidden = new Set((role.hidden_app_ids || []).map((id) => String(id).toLowerCase()));
  return state.roleApps.filter((app) => {
    const appID = app.app_id || app.container_name || app.display_name;
    return !hidden.has(String(appID).toLowerCase());
  }).length;
}

function roleFlag(role, key, label) {
  return node("label", { class: "toggle-line permission-toggle" },
    node("input", {
      type: "checkbox",
      checked: role[key],
      onchange: (event) => {
        role[key] = event.target.checked;
      },
    }),
    label,
  );
}

function roleAppToggle(role, app) {
  const hidden = new Set((role.hidden_app_ids || []).map((id) => id.toLowerCase()));
  const appID = app.app_id || app.container_name || app.display_name;
  const visible = !hidden.has(String(appID).toLowerCase());
  const status = normalizeStatus(app.current_status);
  return node("label", { class: `role-app-toggle ${visible ? "visible" : "hidden"}` },
    node("input", {
      type: "checkbox",
      checked: visible,
      onchange: (event) => {
        updateRoleApp(role, appID, event.target.checked);
        renderRoleSettings();
      },
    }),
    renderAppLogo(app),
    node("span", { class: "role-app-name" },
      node("strong", { text: app.display_name || appID }),
      node("small", {},
        statusIndicator(app.current_status || "unknown", status, "status-dot-only"),
        app.current_status || "unknown",
      ),
    ),
    node("small", { class: `role-app-state ${visible ? "online" : "hidden"}`, text: visible ? "Visible" : "Hidden" }),
  );
}

function updateRoleApp(role, appID, visible) {
  const hidden = new Set((role.hidden_app_ids || []).map((id) => String(id).toLowerCase()));
  if (visible) hidden.delete(String(appID).toLowerCase());
  else hidden.add(String(appID).toLowerCase());
  role.hidden_app_ids = [...hidden].sort();
}

function renderUserManagement(roles) {
  const userRoles = accountRoles(roles);
  const username = node("input", { placeholder: "Username", autocomplete: "off" });
  const displayName = node("input", { placeholder: "Display name", autocomplete: "off" });
  const password = node("input", { placeholder: "Password for new/reset", type: "password", autocomplete: "new-password" });
  const roleSelect = node("select", {}, userRoles.map((role) => node("option", { value: role.role, text: role.display_name || role.role })));
  const disabled = node("input", { type: "checkbox" });
  return node("section", { class: "user-editor" },
    node("div", { class: "section-head" },
      node("div", {},
        node("h3", { text: "User Accounts" }),
        node("p", { class: "muted", text: "Create users, reset passwords, or disable accounts." }),
      ),
      node("span", { class: "muted", text: `${state.roleUsers.length} user${state.roleUsers.length === 1 ? "" : "s"}` }),
    ),
    node("div", { class: "user-management-grid" },
      node("div", { class: "user-list" }, state.roleUsers.map((user) => node("button", {
        type: "button",
        class: "user-row",
        onclick: () => {
          username.value = user.username;
          displayName.value = user.display_name || "";
          roleSelect.value = user.role;
          disabled.checked = !!user.disabled;
          password.value = "";
        },
      },
        node("span", { class: "user-row-main" },
          node("strong", { text: user.display_name || user.username }),
          node("small", { text: user.username }),
        ),
        node("span", { class: `user-role ${user.disabled ? "disabled" : ""}`, text: user.disabled ? "Disabled" : roleDisplayName(userRoles, user.role) }),
      ))),
      node("div", { class: "user-form-panel" },
        node("div", { class: "user-form-head" },
          node("h4", { text: "Create or edit user" }),
          node("p", { class: "muted", text: "Saving an existing username updates that account." }),
        ),
        node("div", { class: "user-form" },
          node("label", {}, "Username", username),
          node("label", {}, "Display name", displayName),
          node("label", {}, "Role", roleSelect),
          node("label", {}, "Password", password),
          node("label", { class: "toggle-line" }, disabled, "Disabled"),
          node("button", {
            type: "button",
            class: "command",
            "data-glyph": "v",
            onclick: () => saveUser({ username, displayName, roleSelect, password, disabled }),
            text: "Save user",
          }),
        ),
      ),
    ),
  );
}

function roleDisplayName(roles, roleName) {
  const role = roles.find((item) => item.role === roleName);
  return role?.display_name || String(roleName || "").replaceAll("_", " ");
}

async function saveRoles() {
  try {
    const saved = await api("/api/admin/settings/roles", {
      method: "POST",
      body: JSON.stringify(state.roleVisibility),
    });
    state.roleVisibility = clone(saved);
    renderRoleSettings();
    showNotice("Role access saved.");
    await refresh();
  } catch (error) {
    showNotice(error.message, "error");
  }
}

async function saveUser(fields) {
  try {
    const saved = await api("/api/admin/users", {
      method: "POST",
      body: JSON.stringify({
        username: fields.username.value.trim(),
        display_name: fields.displayName.value.trim(),
        role: fields.roleSelect.value,
        password: fields.password.value,
        disabled: fields.disabled.checked,
      }),
    });
    fields.password.value = "";
    showNotice(`${saved.username} saved.`);
    await loadRoleSettings();
  } catch (error) {
    showNotice(error.message, "error");
  }
}

async function saveRoleAssignments() {
  const original = new Map((state.roleUsersOriginal || []).map((user) => [user.username, String(user.role || "")]));
  const changed = assignableRoleUsers().filter((user) => String(user.role || "") !== (original.get(user.username) || ""));
  if (!changed.length) return;
  try {
    for (const user of changed) {
      await api("/api/admin/users", {
        method: "POST",
        body: JSON.stringify({
          username: user.username,
          display_name: user.display_name || user.username,
          role: user.role,
          disabled: !!user.disabled,
          password: "",
        }),
      });
    }
    showNotice(`${changed.length} assignment${changed.length === 1 ? "" : "s"} saved.`);
    await loadRoleSettings();
    await refresh();
  } catch (error) {
    showNotice(error.message, "error");
  }
}

function addRole() {
  if (!state.roleVisibility) return;
  const name = window.prompt("Role name");
  if (name === null) return;
  const role = roleSlug(name);
  if (!role) {
    showNotice("Role name is required.", "error");
    return;
  }
  state.roleVisibility.roles ||= [];
  if (state.roleVisibility.roles.some((item) => item.role === role)) {
    showNotice("Role already exists.", "error");
    return;
  }
  state.roleVisibility.roles.push({
    role,
    display_name: name.trim(),
    can_use_llm: true,
    show_nas_status_to_users: true,
    show_wan_status_to_users: true,
    show_incident_ids_to_users: false,
    hidden_app_ids: [],
    hidden_container_names: [],
  });
  state.selectedRole = role;
  renderRoleSettings();
}

function roleSlug(value) {
  return String(value || "").trim().toLowerCase().replace(/[^a-z0-9_-]+/g, "_").replace(/^_+|_+$/g, "");
}

function clone(value) {
  return JSON.parse(JSON.stringify(value));
}

function renderSettingsCard(item, data) {
  switch (item.section) {
    case "visibility":
      return renderVisibilitySettings(item, data);
    case "blacklist":
      return renderBlacklistSettings(item, data);
    case "apps":
      return renderAppImageSettings(item, data);
    case "llm":
      return renderLLMSettings(item, data);
    case "integrations":
      return renderIntegrationSettings(item, data);
    case "notifications":
      return renderNotificationSettings(item, data);
    default:
      return settingsCard(item, node("div", { class: "empty", text: "Unknown settings section." }));
  }
}

function settingsCard(item, body, save) {
  const status = node("span", { class: "settings-save-state muted", "aria-live": "polite" });
  const saveButton = save ? node("button", {
    type: "button",
    class: "primary command",
    "data-glyph": "v",
    onclick: () => save(status),
    text: "Save",
  }) : null;
  return node("article", { class: "settings-card settings-section", "data-settings-section": item.section },
    node("header", {},
      node("h3", { text: item.title }),
      node("div", { class: "settings-actions" }, saveButton, status),
    ),
    body,
  );
}

function renderVisibilitySettings(item, data) {
  const settings = clone(data || {});
  settings.roles ||= [];
  const roleOptions = settings.roles.map((role) => ({ value: role.role, label: role.display_name || role.role }));
  const defaultRole = settingSelectField("Default role", settings.default_role || "general_user", roleOptions);
  const chat = settingToggle("Status chat", settings.general_user_can_use_llm !== false);
  const nas = settingToggle("Server health", settings.show_nas_status_to_users !== false);
  const wan = settingToggle("Router status", settings.show_wan_status_to_users !== false);
  const incidentIDs = settingToggle("Incident IDs", !!settings.show_incident_ids_to_users);
  const hiddenApps = listEditor("Hidden app IDs", settings.hidden_app_ids || [], "app id");
  const hiddenContainers = listEditor("Hidden container names", settings.hidden_container_names || [], "container name");
  const body = node("div", { class: "settings-form" },
    node("div", { class: "settings-field-grid" }, defaultRole.element),
    node("div", { class: "settings-toggle-grid" }, chat.element, nas.element, wan.element, incidentIDs.element),
    node("div", { class: "settings-two-col" }, hiddenApps.element, hiddenContainers.element),
  );
  return settingsCard(item, body, (status) => saveSettingsPayload(item.title, item.path, {
    ...settings,
    default_role: defaultRole.input.value,
    general_user_can_use_llm: chat.input.checked,
    show_nas_status_to_users: nas.input.checked,
    show_wan_status_to_users: wan.input.checked,
    show_incident_ids_to_users: incidentIDs.input.checked,
    hidden_app_ids: hiddenApps.values(),
    hidden_container_names: hiddenContainers.values(),
  }, status));
}

function renderBlacklistSettings(item, data) {
  const settings = clone(data || {});
  const groups = [
    ["Apps", [
      ["blacklist_app_ids", "App IDs", "app id"],
      ["blacklist_container_names", "Container names", "container name"],
      ["blacklist_display_names", "Display names", "display name"],
    ]],
    ["Storage", [
      ["blacklist_folder_paths", "Folder paths", "/mnt/user/private"],
      ["blacklist_share_names", "Share names", "share"],
      ["blacklist_file_paths", "File paths", "path"],
      ["blacklist_filename_globs", "Filename globs", "*.key"],
    ]],
    ["Network and logs", [
      ["blacklist_log_patterns", "Log patterns", "pattern"],
      ["blacklist_env_names", "Environment names", "*_KEY"],
      ["blacklist_url_patterns", "URL patterns", "url pattern"],
      ["blacklist_hostnames", "Hostnames", "host"],
      ["blacklist_ips", "IP addresses", "ip"],
      ["blacklist_usernames", "Usernames", "username"],
    ]],
  ];
  const editors = new Map();
  const sections = groups.map(([title, fields]) => node("section", { class: "settings-subsection" },
    node("h4", { text: title }),
    node("div", { class: "settings-two-col" }, fields.map(([key, label, placeholder]) => {
      const editor = listEditor(label, settings[key] || [], placeholder);
      editors.set(key, editor);
      return editor.element;
    })),
  ));
  const redactIPs = settingToggle("Redact IPs", !!settings.redact_ips);
  const redactHosts = settingToggle("Redact hostnames", !!settings.redact_hostnames);
  const redactEmails = settingToggle("Redact emails", settings.redact_emails !== false);
  const body = node("div", { class: "settings-form" },
    sections,
    node("section", { class: "settings-subsection" },
      node("h4", { text: "Redaction" }),
      node("div", { class: "settings-toggle-grid" }, redactIPs.element, redactHosts.element, redactEmails.element),
    ),
  );
  return settingsCard(item, body, (status) => {
    const payload = { ...settings };
    for (const [key, editor] of editors.entries()) payload[key] = editor.values();
    payload.redact_ips = redactIPs.input.checked;
    payload.redact_hostnames = redactHosts.input.checked;
    payload.redact_emails = redactEmails.input.checked;
    return saveSettingsPayload(item.title, item.path, payload, status);
  });
}

function renderAppImageSettings(item, data) {
  const overrides = { ...(data?.icon_overrides || {}) };
  const apps = state.snapshot?.apps || [];
  const appRows = [];
  const appKeys = new Set();
  const appList = apps.map((app) => {
    const key = String(app.app_id || app.container_name || app.display_name || "").trim();
    if (key) appKeys.add(key);
    const input = node("input", {
      value: key ? (overrides[key] || "") : "",
      placeholder: app.icon_url && app.icon_source !== "built-in" ? app.icon_url : "https://example.local/icon.png",
      inputmode: "url",
    });
    appRows.push({ key, input });
    return node("div", { class: "settings-app-image-row" },
      renderAppLogo(app),
      node("span", { class: "settings-row-main" },
        node("strong", { text: app.display_name || key || "App" }),
        node("small", { text: app.icon_source ? `Current: ${app.icon_source}` : "No image source" }),
      ),
      input,
    );
  });
  const extraRows = [];
  const extras = Object.entries(overrides).filter(([key]) => !appKeys.has(key));
  const extraList = node("div", { class: "settings-extra-list" });
  const addExtra = (key = "", url = "") => {
    const keyInput = node("input", { value: key, placeholder: "App key" });
    const urlInput = node("input", { value: url, placeholder: "Image URL or /app-icons/name.svg", inputmode: "url" });
    const row = { keyInput, urlInput, element: null };
    row.element = node("div", { class: "settings-kv-row" },
      keyInput,
      urlInput,
      node("button", {
        type: "button",
        class: "command ghost",
        "data-glyph": "x",
        "aria-label": "Remove image override",
        onclick: () => {
          const index = extraRows.indexOf(row);
          if (index >= 0) extraRows.splice(index, 1);
          row.element.remove();
        },
      }),
    );
    extraRows.push(row);
    extraList.append(row.element);
  };
  extras.forEach(([key, url]) => addExtra(key, url));
  const body = node("div", { class: "settings-form" },
    node("section", { class: "settings-subsection" },
      node("h4", { text: "Known apps" }),
      node("div", { class: "settings-row-list" }, appList.length ? appList : node("div", { class: "empty", text: "No apps loaded." })),
    ),
    node("section", { class: "settings-subsection" },
      node("div", { class: "settings-section-title-row" },
        node("h4", { text: "Other images" }),
        node("button", { type: "button", class: "command", "data-glyph": "+", onclick: () => addExtra(), text: "Add" }),
      ),
      extraList,
    ),
  );
  return settingsCard(item, body, (status) => {
    const icon_overrides = {};
    for (const row of appRows) {
      const url = row.input.value.trim();
      if (row.key && url) icon_overrides[row.key] = url;
    }
    for (const row of extraRows) {
      const key = row.keyInput.value.trim();
      const url = row.urlInput.value.trim();
      if (key && url) icon_overrides[key] = url;
    }
    return saveSettingsPayload(item.title, item.path, { icon_overrides }, status);
  });
}

function renderLLMSettings(item, data) {
  const settings = clone(data || {});
  const enabled = settingToggle("Enabled", settings.enabled !== false);
  const provider = settingSelectField("Provider", settings.provider || "disabled", [
    { value: "disabled", label: "Disabled" },
    { value: "openai", label: "OpenAI" },
    { value: "anthropic", label: "Anthropic" },
  ]);
  const openAIModel = settingTextField("OpenAI model", settings.openai_model || "gpt-5");
  const anthropicModel = settingTextField("Anthropic model", settings.anthropic_model || "claude-sonnet-4-5");
  const timeout = durationSecondsField("Timeout", settings.timeout || 45000000000);
  const openAIKey = settingTextField("OpenAI API key", "", {
    type: "password",
    autocomplete: "new-password",
    placeholder: settings.openai_api_key_set ? "Saved key unchanged" : "Paste API key",
  });
  const anthropicKey = settingTextField("Anthropic API key", "", {
    type: "password",
    autocomplete: "new-password",
    placeholder: settings.anthropic_api_key_set ? "Saved key unchanged" : "Paste API key",
  });
  const clearOpenAI = settingToggle("Clear saved OpenAI key", false);
  const clearAnthropic = settingToggle("Clear saved Anthropic key", false);
  clearOpenAI.input.disabled = !settings.openai_api_key_set;
  clearAnthropic.input.disabled = !settings.anthropic_api_key_set;
  const policyEditors = Object.entries(settings.policies || {}).map(([name, policy]) => policyEditor(name, policy));
  const body = node("div", { class: "settings-form" },
    node("section", { class: "settings-subsection" },
      node("h4", { text: "Provider" }),
      node("div", { class: "settings-field-grid" }, provider.element, openAIModel.element, anthropicModel.element, timeout.element),
      node("div", { class: "settings-toggle-grid" }, enabled.element),
    ),
    node("section", { class: "settings-subsection" },
      node("h4", { text: "API keys" }),
      node("div", { class: "settings-field-grid" },
        keyFieldWithState(openAIKey, settings.openai_api_key_set),
        keyFieldWithState(anthropicKey, settings.anthropic_api_key_set),
      ),
      node("div", { class: "settings-toggle-grid" }, clearOpenAI.element, clearAnthropic.element),
    ),
    node("section", { class: "settings-subsection" },
      node("h4", { text: "Policies" }),
      node("div", { class: "settings-policy-list" }, policyEditors.map((editor) => editor.element)),
    ),
  );
  return settingsCard(item, body, (status) => {
    const payload = {
      enabled: enabled.input.checked,
      provider: provider.input.value,
      openai_model: openAIModel.input.value.trim(),
      anthropic_model: anthropicModel.input.value.trim(),
      timeout: secondsToDuration(timeout.input.value),
      clear_openai_api_key: clearOpenAI.input.checked,
      clear_anthropic_api_key: clearAnthropic.input.checked,
      policies: {},
    };
    if (openAIKey.input.value.trim()) payload.openai_api_key = openAIKey.input.value.trim();
    if (anthropicKey.input.value.trim()) payload.anthropic_api_key = anthropicKey.input.value.trim();
    for (const editor of policyEditors) payload.policies[editor.name] = editor.value();
    return saveSettingsPayload(item.title, item.path, payload, status);
  });
}

function renderIntegrationSettings(item, data) {
  const settings = clone(data || {});
  const mode = settingSelectField("Mode", settings.mode || "live", [
    { value: "live", label: "Live" },
    { value: "mixed", label: "Mixed" },
    { value: "fixture", label: "Fixture" },
  ]);
  const unraidURL = settingTextField("Unraid base URL", settings.unraid_base_url || "", { placeholder: "http://tower.local", inputmode: "url" });
  const unraidKey = settingTextField("Unraid API key", "", {
    type: "password",
    autocomplete: "new-password",
    placeholder: settings.unraid_api_key_set ? "Saved key unchanged" : "Paste API key",
  });
  const clearUnraidKey = settingToggle("Clear saved Unraid key", false);
  clearUnraidKey.input.disabled = !settings.unraid_api_key_set;
  const unraidKeyFile = settingTextField("Unraid key file", settings.unraid_api_key_file || "", { placeholder: "C:\\path\\to\\unraid.key" });
  const sshFallback = settingToggle("SSH fallback", !!settings.unraid_ssh_fallback);
  const sshHost = settingTextField("SSH host", settings.unraid_ssh_host || "");
  const sshPort = settingNumberField("SSH port", settings.unraid_ssh_port || 22, { min: 1, max: 65535 });
  const sshUser = settingTextField("SSH user", settings.unraid_ssh_user || "");
  const sshKeyFile = settingTextField("SSH key file", settings.unraid_ssh_key_file || "");
  const sshCommand = settingTextField("SSH command", settings.unraid_ssh_command || "ssh");
  const unifiURL = settingTextField("UniFi base URL", settings.unifi_base_url || "", { placeholder: "https://192.168.1.1", inputmode: "url" });
  const unifiKey = settingTextField("UniFi API key", "", {
    type: "password",
    autocomplete: "new-password",
    placeholder: settings.unifi_api_key_set ? "Saved key unchanged" : "Paste API key",
  });
  const clearUniFiKey = settingToggle("Clear saved UniFi key", false);
  clearUniFiKey.input.disabled = !settings.unifi_api_key_set;
  const unifiKeyFile = settingTextField("UniFi key file", settings.unifi_api_key_file || "", { placeholder: "C:\\path\\to\\unifi.key" });
  const unifiSite = settingTextField("UniFi site ID", settings.unifi_site_id || "default");
  const unifiTLS = settingToggle("Allow self-signed UniFi TLS", settings.unifi_insecure_tls !== false);
  const internetProbe = settingTextField("Internet probe URL", settings.internet_probe_url || "", { inputmode: "url" });
  const dnsProbe = settingTextField("DNS probe host", settings.dns_probe_host || "");
  const routerProbe = settingTextField("Router probe target", settings.router_probe_target || "", { inputmode: "url" });
  const nasProbe = settingTextField("NAS probe target", settings.nas_probe_target || "", { inputmode: "url" });
  const body = node("div", { class: "settings-form" },
    node("section", { class: "settings-subsection" },
      node("h4", { text: "Mode" }),
      node("div", { class: "settings-field-grid" }, mode.element),
    ),
    node("section", { class: "settings-subsection" },
      node("h4", { text: "Unraid" }),
      node("div", { class: "settings-field-grid" }, unraidURL.element, keyFieldWithState(unraidKey, settings.unraid_api_key_set), unraidKeyFile.element),
      node("div", { class: "settings-toggle-grid" }, clearUnraidKey.element, sshFallback.element),
      node("div", { class: "settings-field-grid" }, sshHost.element, sshPort.element, sshUser.element, sshKeyFile.element, sshCommand.element),
    ),
    node("section", { class: "settings-subsection" },
      node("h4", { text: "UniFi" }),
      node("div", { class: "settings-field-grid" }, unifiURL.element, keyFieldWithState(unifiKey, settings.unifi_api_key_set), unifiKeyFile.element, unifiSite.element),
      node("div", { class: "settings-toggle-grid" }, clearUniFiKey.element, unifiTLS.element),
    ),
    node("section", { class: "settings-subsection" },
      node("h4", { text: "Probes" }),
      node("div", { class: "settings-field-grid" }, internetProbe.element, dnsProbe.element, routerProbe.element, nasProbe.element),
    ),
  );
  return settingsCard(item, body, (status) => {
    const payload = {
      mode: mode.input.value,
      unraid_base_url: unraidURL.input.value.trim(),
      clear_unraid_api_key: clearUnraidKey.input.checked,
      unraid_api_key_file: unraidKeyFile.input.value.trim(),
      unraid_ssh_fallback: sshFallback.input.checked,
      unraid_ssh_host: sshHost.input.value.trim(),
      unraid_ssh_port: parseInt(sshPort.input.value, 10) || 22,
      unraid_ssh_user: sshUser.input.value.trim(),
      unraid_ssh_key_file: sshKeyFile.input.value.trim(),
      unraid_ssh_command: sshCommand.input.value.trim(),
      unifi_base_url: unifiURL.input.value.trim(),
      clear_unifi_api_key: clearUniFiKey.input.checked,
      unifi_api_key_file: unifiKeyFile.input.value.trim(),
      unifi_site_id: unifiSite.input.value.trim(),
      unifi_insecure_tls: unifiTLS.input.checked,
      internet_probe_url: internetProbe.input.value.trim(),
      dns_probe_host: dnsProbe.input.value.trim(),
      router_probe_target: routerProbe.input.value.trim(),
      nas_probe_target: nasProbe.input.value.trim(),
    };
    if (unraidKey.input.value.trim()) payload.unraid_api_key = unraidKey.input.value.trim();
    if (unifiKey.input.value.trim()) payload.unifi_api_key = unifiKey.input.value.trim();
    return saveSettingsPayload(item.title, item.path, payload, status);
  });
}

function renderNotificationSettings(item, data) {
  const settings = clone(data || {});
  const enabled = settingToggle("Enabled", settings.enabled !== false);
  const optIn = settingToggle("User opt-in", settings.global_opt_in_enabled !== false);
  const dedupe = settingToggle("Whole-outage deduping", settings.whole_outage_deduping !== false);
  const backendOptions = uniqueOptions(["mock", settings.backend].filter(Boolean).map((value) => ({ value, label: value })));
  const backend = settingSelectField("Backend", settings.backend || "mock", backendOptions);
  const rateLimit = durationSecondsField("Rate limit window", settings.rate_limit_window || 900000000000);
  const body = node("div", { class: "settings-form" },
    node("div", { class: "settings-toggle-grid" }, enabled.element, optIn.element, dedupe.element),
    node("div", { class: "settings-field-grid" }, backend.element, rateLimit.element),
  );
  return settingsCard(item, body, (status) => saveSettingsPayload(item.title, item.path, {
    enabled: enabled.input.checked,
    global_opt_in_enabled: optIn.input.checked,
    backend: backend.input.value,
    rate_limit_window: secondsToDuration(rateLimit.input.value),
    whole_outage_deduping: dedupe.input.checked,
  }, status));
}

function settingTextField(label, value, attrs = {}) {
  const input = node("input", { value: value || "", ...attrs });
  return { input, element: node("label", {}, label, input) };
}

function settingNumberField(label, value, attrs = {}) {
  const input = node("input", { type: "number", value: Number.isFinite(Number(value)) ? String(value) : "", ...attrs });
  return { input, element: node("label", {}, label, input) };
}

function settingSelectField(label, value, options) {
  const select = node("select", {}, options.map((option) => node("option", {
    value: option.value,
    selected: option.value === value,
    text: option.label,
  })));
  return { input: select, element: node("label", {}, label, select) };
}

function settingToggle(label, checked) {
  const input = node("input", { type: "checkbox", checked: !!checked });
  return { input, element: node("label", { class: "toggle-line setting-toggle" }, input, label) };
}

function durationSecondsField(label, duration) {
  return settingNumberField(label, durationToSeconds(duration), { min: 0, step: 1, inputmode: "numeric" });
}

function durationToSeconds(duration) {
  const value = Number(duration || 0);
  if (!Number.isFinite(value) || value <= 0) return 0;
  return Math.round(value / 1000000000);
}

function secondsToDuration(seconds) {
  const value = Number(seconds || 0);
  if (!Number.isFinite(value) || value <= 0) return 0;
  return Math.round(value * 1000000000);
}

function keyFieldWithState(field, isSet) {
  return node("div", { class: "settings-key-field" },
    field.element,
    node("span", { class: `settings-key-state ${isSet ? "set" : ""}`, text: isSet ? "Saved" : "Not set" }),
  );
}

function listEditor(label, values, placeholder) {
  let items = compactList(values);
  const list = node("div", { class: "settings-list-items" });
  const input = node("input", { placeholder });
  const render = () => {
    list.replaceChildren(...items.map((value, index) => node("div", { class: "settings-list-item" },
      node("span", { text: value }),
      node("button", {
        type: "button",
        class: "command ghost",
        "data-glyph": "x",
        "aria-label": `Remove ${value}`,
        onclick: () => {
          items.splice(index, 1);
          render();
        },
      }),
    )));
  };
  const add = () => {
    const value = input.value.trim();
    if (!value) return;
    const exists = items.some((item) => item.toLowerCase() === value.toLowerCase());
    if (!exists) items.push(value);
    input.value = "";
    render();
  };
  input.addEventListener("keydown", (event) => {
    if (event.key === "Enter") {
      event.preventDefault();
      add();
    }
  });
  render();
  return {
    element: node("div", { class: "settings-list-editor" },
      node("label", {}, label,
        node("div", { class: "settings-add-row" },
          input,
          node("button", { type: "button", class: "command", "data-glyph": "+", onclick: add, text: "Add" }),
        ),
      ),
      list,
    ),
    values: () => compactList(items),
  };
}

function compactList(values) {
  const seen = new Set();
  const out = [];
  for (const value of values || []) {
    const trimmed = String(value || "").trim();
    const key = trimmed.toLowerCase();
    if (!trimmed || seen.has(key)) continue;
    seen.add(key);
    out.push(trimmed);
  }
  return out;
}

function policyEditor(name, policy) {
  const current = clone(policy || {});
  const meta = policyMeta(name);
  const enabled = settingToggle("Enabled", current.enabled !== false);
  const includeLogs = settingToggle("Include logs", !!current.include_logs);
  const preferFacts = settingToggle("Prefer facts", current.prefer_incident_facts !== false);
  const allowHidden = settingToggle("Hidden app names", !!current.allow_hidden_app_names);
  const allowBlacklisted = settingToggle("Blacklisted names", !!current.allow_blacklisted_names);
  const failClosed = settingToggle("Fail closed", current.fail_closed_on_redaction !== false);
  const maxContext = settingNumberField("Max context bytes", current.max_context_bytes || 8000, { min: 1, step: 1000, inputmode: "numeric" });
  const maxLogs = settingNumberField("Max log lines", current.max_log_lines || 0, { min: 0, step: 1, inputmode: "numeric" });
  const logSources = listEditor("Allowed log sources", current.allowed_log_sources || [], "source");
  return {
    name,
    element: node("details", { class: "settings-policy", open: name === "admin_requested" },
      node("summary", {},
        node("span", {},
          node("strong", { text: meta.title }),
          node("small", { text: meta.description }),
        ),
        node("small", { text: `Recipient: ${current.recipient_role || meta.recipient}` }),
      ),
      node("p", { class: "muted", text: "Advanced context and redaction tuning." }),
      node("div", { class: "settings-toggle-grid" }, enabled.element, includeLogs.element, preferFacts.element, allowHidden.element, allowBlacklisted.element, failClosed.element),
      node("div", { class: "settings-field-grid" }, maxContext.element, maxLogs.element),
      logSources.element,
    ),
    value: () => ({
      ...current,
      name: current.name || name,
      enabled: enabled.input.checked,
      include_logs: includeLogs.input.checked,
      prefer_incident_facts: preferFacts.input.checked,
      allow_hidden_app_names: allowHidden.input.checked,
      allow_blacklisted_names: allowBlacklisted.input.checked,
      fail_closed_on_redaction: failClosed.input.checked,
      max_context_bytes: parseInt(maxContext.input.value, 10) || current.max_context_bytes || 1,
      max_log_lines: parseInt(maxLogs.input.value, 10) || 0,
      allowed_log_sources: logSources.values(),
      recipient_role: current.recipient_role || "admin",
    }),
  };
}

function policyMeta(name) {
  const values = {
    admin_requested: {
      title: "Admin-requested diagnosis",
      description: "Used when the technical owner asks from the Diagnostics tab.",
      recipient: "admin",
    },
    general_user_requested: {
      title: "General-user diagnosis",
      description: "Used for the compact app chat with role-scoped context.",
      recipient: "general user",
    },
    automatic_incident: {
      title: "Automatic incident summary",
      description: "Reserved for background incident explanation and notifications.",
      recipient: "admin",
    },
  };
  return values[name] || { title: policyDisplayName(name), description: "Custom diagnosis policy.", recipient: "admin" };
}

function policyDisplayName(name) {
  return String(name || "").replaceAll("_", " ");
}

function uniqueOptions(options) {
  const seen = new Set();
  return options.filter((option) => {
    if (seen.has(option.value)) return false;
    seen.add(option.value);
    return true;
  });
}

async function saveSettingsPayload(title, path, payload, status) {
  status.textContent = "Saving";
  status.dataset.tone = "info";
  try {
    const saved = await api(path, {
      method: "POST",
      body: JSON.stringify(payload),
    });
    status.textContent = "Saved";
    status.dataset.tone = "ok";
    showNotice(`${title} settings saved.`);
    await refresh();
    await loadSettings();
    return saved;
  } catch (error) {
    status.textContent = "Failed";
    status.dataset.tone = "error";
    showNotice(error.message, "error");
  }
}

async function logout() {
  try {
    await api("/api/auth/logout", { method: "POST", body: "{}" });
  } finally {
    location.reload();
  }
}

function isAdmin() {
  return state.user?.role === "admin";
}

function isCompactSite() {
  return SITE_MODE === "compact";
}

function hasAdminSurface() {
  return isAdmin() && !isCompactSite();
}

function canUseCompactChat(snapshot) {
  if (isAdmin()) return true;
  return snapshot.visibility?.general_user_can_use_llm !== false;
}

function serverRollupStatus(infra) {
  if (!infra.nas_reachable) return "offline";
  if (!infra.unraid_api_reachable || !infra.unraid_array_healthy || !infra.docker_service_available || linkStatus(infra) === "degraded") return "degraded";
  return "online";
}

function routerRollupStatus(infra) {
  const routerProbeKnown = hasProbeData(infra, "router");
  const internetProbeKnown = hasProbeData(infra, "internet");
  const dnsProbeKnown = hasProbeData(infra, "dns");
  const unifiKnown = hasCollectorData(infra, "unifi");
  if (routerProbeKnown && !infra.router_reachable) return "offline";
  if (!routerProbeKnown && !internetProbeKnown && !dnsProbeKnown && !unifiKnown) return "unknown";
  if (!routerProbeKnown && unifiKnown && !infra.unifi_gateway_reachable) return "offline";
  if ((internetProbeKnown && !infra.internet_reachable) || (dnsProbeKnown && !infra.dns_ok) || (unifiKnown && (!infra.unifi_wan_up || !infra.unifi_gateway_reachable))) return "degraded";
  return "online";
}

function appsRollupStatus(apps) {
  if (!apps.length) return "unknown";
  if (apps.some((app) => app.current_status === "offline")) return "offline";
  if (apps.some((app) => app.current_status === "degraded" || app.current_status === "unknown")) return "degraded";
  return "online";
}

function serverSummary(infra) {
  if (!infra.nas_reachable) return "The server is not reachable from the dashboard host.";
  if (!infra.unraid_api_reachable) return "The server is reachable, but the Unraid API is unavailable.";
  if (!infra.unraid_array_healthy) return "Unraid storage needs attention.";
  if (!infra.docker_service_available) return "Docker is unavailable on the server.";
  if (linkStatus(infra) === "degraded") return "The NAS link is below the expected negotiated speed.";
  return "Server, storage, and Docker are online.";
}

function routerSummary(infra) {
  const routerProbeKnown = hasProbeData(infra, "router");
  const internetProbeKnown = hasProbeData(infra, "internet");
  const dnsProbeKnown = hasProbeData(infra, "dns");
  const unifiKnown = hasCollectorData(infra, "unifi");
  if (!routerProbeKnown && !internetProbeKnown && !dnsProbeKnown && !unifiKnown) return "Router status has no live collector data.";
  if (routerProbeKnown && !infra.router_reachable) return "The router is not reachable from the dashboard host.";
  if (internetProbeKnown && !infra.internet_reachable) return "The router is reachable, but external internet checks are failing.";
  if (unifiKnown && !infra.unifi_gateway_reachable) return "UniFi integration is not reporting gateway status.";
  if (unifiKnown && !infra.unifi_wan_up) return "UniFi reports the WAN link is down.";
  if (dnsProbeKnown && !infra.dns_ok) return "Network DNS checks are failing.";
  return "Router, WAN, UniFi, and DNS checks are online.";
}

function appsSummary(apps, offlineApps, degradedApps) {
  if (!apps.length) return "No applications are visible in this snapshot.";
  if (offlineApps) return `${offlineApps} app${offlineApps === 1 ? "" : "s"} offline.`;
  if (degradedApps) return `${degradedApps} app${degradedApps === 1 ? "" : "s"} degraded.`;
  return "Visible applications are online.";
}

function sourceHealth(infra, key) {
  return infra.source_health?.[key] || "No collector detail reported";
}

function formatTime(value) {
  if (!value) return "No timestamp";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return String(value);
  return date.toLocaleString();
}

function formatBytes(value) {
  const bytes = Number(value || 0);
  if (!bytes) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB", "PB"];
  const exponent = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1);
  const amount = bytes / (1024 ** exponent);
  return `${amount.toFixed(amount >= 10 || exponent === 0 ? 0 : 1)} ${units[exponent]}`;
}

function formatPercent(value) {
  const number = Number(value || 0);
  return `${number.toFixed(1)}%`;
}

function boolStatus(value) {
  if (value === true) return "online";
  if (value === false) return "offline";
  return "unknown";
}

function probeStatus(infra, name, value) {
  return hasProbeData(infra, name) ? boolStatus(value) : "unknown";
}

function hasCollectorData(infra, key) {
  const health = String(infra.source_health?.[key] || "").toLowerCase();
  if (!health) return false;
  return !["not configured", "credentials are not configured", "collector is not configured", "no collector detail"].some((text) => health.includes(text));
}

function hasProbeData(infra, name) {
  const health = String(infra.source_health?.probes || "").toLowerCase();
  if (!hasCollectorData(infra, "probes")) return false;
  return !health.includes(`${String(name || "").toLowerCase()} skipped`);
}

function linkStatus(infra) {
  if (!infra.nas_link_speed_mbps) return "unknown";
  if (infra.expected_nas_link_mbps && infra.nas_link_speed_mbps < infra.expected_nas_link_mbps) return "degraded";
  return "online";
}

function linkNote(infra) {
  if (!infra.nas_link_speed_mbps) return "No link data";
  return `${infra.nas_link_speed_mbps} Mbps negotiated`;
}

function normalizeStatus(value) {
  const text = String(value || "unknown").toLowerCase();
  if (["online", "started", "healthy", "ok"].includes(text)) return "online";
  if (["degraded", "warning", "unknown"].includes(text)) return text;
  if (["offline", "stopped", "failed", "down"].includes(text)) return "offline";
  return "unknown";
}

if ("serviceWorker" in navigator) {
  navigator.serviceWorker.register("/service-worker.js").catch(() => {});
}

configureMobileRuntime();

$("login-form").addEventListener("submit", login);
$("refresh").addEventListener("click", refresh);
$("quick-diagnose").addEventListener("click", () => {
  setActiveTab("diagnostics");
});
$("diagnose").addEventListener("click", diagnose);
$("notify-admin").addEventListener("click", () => notifyAdmin());
$("user-chat-open").addEventListener("click", focusUserChat);
$("user-chat-send").addEventListener("click", runUserChat);
$("user-notify-admin").addEventListener("click", () => notifyAdmin("A standard user reported a problem."));
$("audit-refresh").addEventListener("click", loadAudit);
$("audit-filter").addEventListener("input", renderAuditTable);
$("settings-refresh").addEventListener("click", loadSettings);
$("settings-menu").addEventListener("click", (event) => {
  const button = event.target.closest("[data-settings-section]");
  if (button) setSettingsSection(button.dataset.settingsSection);
});
$("overview-rearrange").addEventListener("click", () => {
  setOverviewRearrange(!state.overviewRearrange);
});
$("restore-monitors").addEventListener("click", restoreMonitors);
$("nav-toggle").addEventListener("click", openNav);
$("nav-close").addEventListener("click", closeNav);
$("nav-backdrop").addEventListener("click", closeNav);
$("user-menu-toggle").addEventListener("click", openUserMenu);
$("user-drawer-close").addEventListener("click", () => closeUserMenu());
$("user-drawer-backdrop").addEventListener("click", () => closeUserMenu());
$("assistant-toggle").addEventListener("click", () => {
  $("assistant-panel").hidden = !$("assistant-panel").hidden;
});
$("assistant-send").addEventListener("click", runAssistantChat);
$("assistant-notify").addEventListener("click", () => notifyAdmin($("assistant-input").value || "A standard user reported a problem."));
$("add-role").addEventListener("click", addRole);
$("save-roles").addEventListener("click", saveRoles);
$("logout").addEventListener("click", logout);
$("tabs").addEventListener("click", (event) => {
  const tab = event.target.closest("[data-tab]");
  if (tab) setActiveTab(tab.dataset.tab);
});
document.addEventListener("click", (event) => {
  const jump = event.target?.closest?.("[data-tab-jump]");
  if (jump) setActiveTab(jump.dataset.tabJump);
});
document.addEventListener("keydown", (event) => {
  if (event.key !== "Enter" && event.key !== " ") return;
  if (event.target?.closest?.("button,input,textarea,select")) return;
  const jump = event.target.closest("[data-tab-jump]");
  if (!jump) return;
  event.preventDefault();
  setActiveTab(jump.dataset.tabJump);
});
document.addEventListener("keydown", handleUserDrawerKeydown);
$("app-search").addEventListener("input", (event) => {
  state.appSearch = event.target.value;
  renderApps(state.snapshot?.apps || []);
});
$("app-filters").addEventListener("click", (event) => {
  const button = event.target.closest("[data-filter]");
  if (!button) return;
  state.appFilter = button.dataset.filter;
  document.querySelectorAll("#app-filters button").forEach((item) => item.classList.toggle("active", item === button));
  renderApps(state.snapshot?.apps || []);
});

loadHiddenMonitors();
loadOverviewOrder();
restoreSession();
