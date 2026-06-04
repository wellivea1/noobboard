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
  selectedUser: "",
  auditEntries: [],
  repairRequests: [],
  userRepairRequests: [],
  notificationPreferences: new Map(),
  notificationPreferencesLoaded: false,
  notificationPreferencesLoading: false,
  userDrawerActiveSection: "settings",
  userDrawerLastFocus: null,
  userView: "status",
  userDetailID: "",
  userDetailSubject: "",
  userDetailReturnFocus: null,
  openAIAuthDialog: null,
  agentApprovalDialog: null,
  chatBusy: {
    diagnostic: false,
    user: false,
    assistant: false,
  },
};

const $ = (id) => document.getElementById(id);
const MONITOR_STORAGE_KEY = "noobboard.hiddenMonitors.v1";
const OVERVIEW_ORDER_STORAGE_KEY = "noobboard.overviewOrder.v1";
const OVERVIEW_MONITOR_IDS = ["overview.overall", "overview.server", "overview.router", "overview.apps"];
const SITE_MODE = String(window.__NOOBBOARD_SITE_MODE__ || window.__HSD_SITE_MODE__ || "admin").toLowerCase() === "compact" ? "compact" : "admin";
const NEW_USER_KEY = "__new_user__";
const SETTINGS_ENDPOINTS = [
  { title: "Visibility", section: "visibility", path: "/api/admin/settings/visibility" },
  { title: "Blacklist", section: "blacklist", path: "/api/admin/settings/blacklist" },
  { title: "Apps", section: "apps", path: "/api/admin/settings/apps" },
  { title: "LLM", section: "llm", path: "/api/admin/settings/llm" },
  { title: "Integrations", section: "integrations", path: "/api/admin/settings/integrations" },
  { title: "Notifications", section: "notifications", path: "/api/admin/settings/notifications" },
];
const OPENAI_MODEL_OPTIONS = [
  { value: "gpt-5.5", label: "GPT-5.5 (recommended)" },
  { value: "gpt-5.5-pro", label: "GPT-5.5 pro" },
  { value: "gpt-5.4", label: "GPT-5.4" },
  { value: "gpt-5.4-pro", label: "GPT-5.4 pro" },
  { value: "gpt-5.4-mini", label: "GPT-5.4 mini" },
  { value: "gpt-5.4-nano", label: "GPT-5.4 nano" },
  { value: "gpt-5.2", label: "GPT-5.2" },
  { value: "gpt-5.2-pro", label: "GPT-5.2 pro" },
  { value: "gpt-5.1", label: "GPT-5.1" },
  { value: "gpt-5", label: "GPT-5" },
  { value: "gpt-5-mini", label: "GPT-5 mini" },
  { value: "gpt-5-nano", label: "GPT-5 nano" },
  { value: "gpt-4.1", label: "GPT-4.1" },
  { value: "gpt-4.1-mini", label: "GPT-4.1 mini" },
  { value: "gpt-4o-mini", label: "GPT-4o mini" },
  { value: "o3-pro", label: "o3-pro" },
  { value: "o3", label: "o3" },
];
const CHATGPT_CODEX_MODEL_OPTIONS = [
  { value: "gpt-5.3-codex", label: "GPT-5.3 Codex" },
];
const ANTHROPIC_MODEL_OPTIONS = [
  { value: "claude-sonnet-4-5", label: "Claude Sonnet 4.5" },
  { value: "claude-opus-4-1-20250805", label: "Claude Opus 4.1" },
  { value: "claude-opus-4-20250514", label: "Claude Opus 4" },
  { value: "claude-sonnet-4-20250514", label: "Claude Sonnet 4" },
  { value: "claude-3-7-sonnet-20250219", label: "Claude Sonnet 3.7" },
  { value: "claude-3-5-haiku-20241022", label: "Claude Haiku 3.5" },
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

function openOpenAIAuthDialog() {
  closeOpenAIAuthDialog({ returnFocus: false });
  const previousFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null;
  const session = { cancelled: false };
  const content = node("div", { class: "openai-auth-content" },
    node("p", { class: "openai-auth-copy", text: "Preparing OpenAI connection..." }),
  );
  const status = node("p", { class: "openai-auth-status", "aria-live": "polite", text: "Starting..." });
  const closeButton = node("button", {
    type: "button",
    class: "command ghost",
    "data-glyph": "x",
    "aria-label": "Close OpenAI connection dialog",
    onclick: () => closeOpenAIAuthDialog(),
    text: "Close",
  });
  const dialog = node("section", {
    class: "openai-auth-dialog",
    role: "dialog",
    "aria-modal": "true",
    "aria-labelledby": "openai-auth-title",
  },
    node("header", { class: "openai-auth-head" },
      node("div", { class: "openai-auth-title-group" },
        node("span", { class: "openai-auth-mark", "aria-hidden": "true", text: "AI" }),
        node("div", {},
          node("p", { class: "eyebrow", text: "OpenAI" }),
          node("h2", { id: "openai-auth-title", text: "Connect OpenAI" }),
        ),
      ),
      closeButton,
    ),
    content,
    status,
  );
  const backdrop = node("div", { class: "openai-auth-backdrop" }, dialog);
  backdrop.addEventListener("click", (event) => {
    if (event.target === backdrop) closeOpenAIAuthDialog();
  });
  backdrop.addEventListener("keydown", handleOpenAIAuthDialogKeydown);
  document.body.append(backdrop);
  document.body.classList.add("openai-auth-open");
  state.openAIAuthDialog = { backdrop, dialog, content, status, closeButton, session, previousFocus };
  closeButton.focus({ preventScroll: true });
  return state.openAIAuthDialog;
}

function closeOpenAIAuthDialog(options = {}) {
  const current = state.openAIAuthDialog;
  if (!current) return;
  current.session.cancelled = true;
  current.backdrop.remove();
  document.body.classList.remove("openai-auth-open");
  state.openAIAuthDialog = null;
  if (options.returnFocus !== false && current.previousFocus?.isConnected) {
    current.previousFocus.focus({ preventScroll: true });
  }
}

function handleOpenAIAuthDialogKeydown(event) {
  const current = state.openAIAuthDialog;
  if (!current) return;
  if (event.key === "Escape") {
    event.preventDefault();
    closeOpenAIAuthDialog();
    return;
  }
  if (event.key !== "Tab") return;
  const focusable = openAIAuthDialogFocusableElements(current.dialog);
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

function openAIAuthDialogFocusableElements(dialog) {
  return [...dialog.querySelectorAll("a[href],button:not([disabled]),input:not([disabled]),select:not([disabled]),textarea:not([disabled]),summary,[tabindex]:not([tabindex='-1'])")]
    .filter((element) => element.getClientRects().length && getComputedStyle(element).visibility !== "hidden");
}

function setOpenAIAuthDialogStatus(dialog, text, tone = "info") {
  if (!dialog || dialog.session.cancelled) return;
  dialog.status.textContent = text;
  dialog.status.dataset.tone = tone;
}

function renderOpenAIAuthWorking(dialog, text) {
  if (!dialog || dialog.session.cancelled) return;
  dialog.content.replaceChildren(node("p", { class: "openai-auth-copy", text }));
  setOpenAIAuthDialogStatus(dialog, "Waiting for OpenAI...", "info");
}

function renderOpenAIAuthCode(dialog, data, note = "") {
  if (!dialog || dialog.session.cancelled) return;
  const code = String(data.user_code || "").trim();
  const verificationURL = String(data.verification_url || "").trim();
  const copyButton = node("button", {
    type: "button",
    class: "command",
    "data-glyph": "\u2398",
    onclick: () => copyOpenAICode(code, copyButton),
    text: "Copy",
  });
  const openLink = node("a", {
    class: "button-link primary command",
    href: verificationURL,
    target: "_blank",
    rel: "noreferrer",
  }, "Open OpenAI login");
  const content = [
    node("div", { class: "openai-auth-code-block" },
      node("span", { class: "openai-auth-label", text: "Device code" }),
      node("div", { class: "openai-auth-code", text: code || "Waiting..." }),
    ),
    node("div", { class: "openai-auth-actions" },
      copyButton,
      openLink,
    ),
    node("p", { class: "openai-auth-copy", text: "Keep this dialog open until OpenAI accepts the code." }),
  ];
  if (note) content.unshift(node("p", { class: "openai-auth-copy", text: note }));
  dialog.content.replaceChildren(...content);
  setOpenAIAuthDialogStatus(dialog, "Waiting for OpenAI to confirm the code...", "info");
}

async function copyOpenAICode(code, button) {
  if (!code) return;
  try {
    await navigator.clipboard.writeText(code);
    const original = button.textContent;
    button.textContent = "Copied";
    setTimeout(() => {
      if (button.isConnected) button.textContent = original;
    }, 1400);
  } catch {
    showNotice("Could not copy the OpenAI code.", "error");
  }
}

function renderOpenAIAuthSuccess(dialog) {
  if (!dialog || dialog.session.cancelled) return;
  dialog.content.replaceChildren(
    node("p", { class: "openai-auth-copy", text: "OpenAI is connected." }),
    node("div", { class: "openai-auth-actions" },
      node("button", { type: "button", class: "primary command", "data-glyph": "v", onclick: () => closeOpenAIAuthDialog(), text: "Done" }),
    ),
  );
  setOpenAIAuthDialogStatus(dialog, "Connected.", "ok");
}

function renderOpenAIAuthError(dialog, message) {
  if (!dialog || dialog.session.cancelled) return;
  dialog.content.replaceChildren(
    node("p", { class: "openai-auth-copy", text: message }),
    node("div", { class: "openai-auth-actions" },
      node("button", { type: "button", class: "command", "data-glyph": "x", onclick: () => closeOpenAIAuthDialog(), text: "Close" }),
    ),
  );
  setOpenAIAuthDialogStatus(dialog, "Connection failed.", "bad");
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
  if (!response.ok) {
    const error = new Error(data.error || response.statusText);
    error.status = response.status;
    error.data = data;
    throw error;
  }
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
    setCompactView(state.userView || "status");
  }
  renderMonitorRestore();
}

function setActiveTab(tabName) {
  if (!hasAdminSurface()) {
    state.activeTab = "user-home";
    setCompactView(state.userView || "status");
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
  if (tabName === "admin") {
    loadAudit();
    loadRepairRequests();
  }
  if (tabName === "settings") loadSettings();
}

function setCompactView(view) {
  if (hasAdminSurface()) return;
  const nextView = ["chat", "app-detail", "infra-detail"].includes(view) ? view : "status";
  state.userView = nextView;
  document.body.dataset.compactView = state.userView;
  const titles = {
    status: "Home status",
    chat: "Ask what's wrong",
    "app-detail": "App details",
    "infra-detail": "Internet details",
  };
  $("page-title").textContent = titles[state.userView] || titles.status;
  const statusButton = $("user-status-open");
  const chatButton = $("user-chat-open");
  const tabSelectedView = state.userView === "chat" ? "chat" : "status";
  if (statusButton) {
    statusButton.classList.toggle("active", tabSelectedView === "status");
    statusButton.setAttribute("aria-selected", String(tabSelectedView === "status"));
  }
  if (chatButton) {
    chatButton.classList.toggle("active", tabSelectedView === "chat");
    chatButton.setAttribute("aria-selected", String(tabSelectedView === "chat"));
  }
  const chatPanel = document.querySelector(".panel.user-chat");
  if (chatPanel) chatPanel.hidden = state.userView !== "chat";
  const appDetail = $("user-app-detail");
  const infraDetail = $("user-infra-detail");
  if (appDetail) appDetail.hidden = state.userView !== "app-detail";
  if (infraDetail) infraDetail.hidden = state.userView !== "infra-detail";
  document.querySelectorAll("[data-user-status-view]").forEach((element) => {
    element.hidden = state.userView !== "status";
  });
  if (state.userView === "chat") {
    $("user-chat-input")?.focus({ preventScroll: true });
  }
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
    const snapshot = await api("/api/status/refresh", { method: "POST" });
    state.snapshot = snapshot;
    if (!hasAdminSurface()) await loadUserRepairRequests({ render: false, quiet: true });
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
  $("assistant-input").disabled = !canChat || state.chatBusy.assistant;
  $("assistant-send").disabled = !canChat || state.chatBusy.assistant;
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
  if (userChatPanel) {
    userChatPanel.dataset.chatAvailable = canChat ? "true" : "false";
  }
  if ($("user-chat-open")) {
    $("user-chat-open").disabled = !canChat;
    $("user-chat-open").setAttribute("aria-disabled", String(!canChat));
  }
  $("user-chat-input").placeholder = "Ask what's wrong or whether an app is working.";
  $("user-chat-input").disabled = !canChat || state.chatBusy.user;
  $("user-chat-send").disabled = !canChat || state.chatBusy.user;
  if (!canChat) {
    if (!$("user-chat-input").value.trim()) $("user-chat-input").value = "What is wrong right now?";
    setChatNotice($("user-chat-output"), "Status chat is not available.");
  } else if ($("user-chat-output").classList.contains("chat-unavailable")) {
    if (!$("user-chat-input").value.trim()) $("user-chat-input").value = "What is wrong right now?";
    resetChatPlaceholder($("user-chat-output"), "Ask what's wrong or whether an app is working.");
  }
  if (state.userView === "chat" && !canChat) state.userView = "status";
  setCompactView(state.userView || "status");
  const statusCards = [
    userStatusCard("Overall", snapshot.overall_status || "unknown", compactOverallSummary(snapshot)),
  ];
  if (snapshot.visibility?.show_nas_status_to_users !== false) {
    statusCards.push(userStatusCard("Server", serverRollupStatus(infra), compactServerSummary(infra), { subject: "nas", detailLabel: "Open server details" }));
  }
  if (snapshot.visibility?.show_wan_status_to_users !== false) {
    statusCards.push(userStatusCard("Router", routerRollupStatus(infra), compactRouterSummary(infra), { subject: "internet", detailLabel: "Open internet details" }));
  }
  $("user-status-grid").replaceChildren(...statusCards);
  $("user-app-count").textContent = `${apps.length} app${apps.length === 1 ? "" : "s"}`;
  if (!apps.length) {
    $("user-apps").replaceChildren(node("div", { class: "empty", text: "No selected apps are visible right now." }));
    if (isUserMenuOpen()) renderUserDrawer();
    return;
  }
  $("user-apps").replaceChildren(...apps.map(renderUserAppCard));
  if (state.userView === "app-detail" && state.userDetailID) {
    loadAppDetail(state.userDetailID, { focus: false });
  } else if (state.userView === "infra-detail" && state.userDetailSubject) {
    loadInfraDetail(state.userDetailSubject, { focus: false });
  }
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
  if (!hasAdminSurface()) return "Status chat is not set up yet.";
  const provider = snapshot.diagnostics_provider || "disabled";
  if (provider === "openai") return "Status chat requires OPENAI_API_KEY.";
  if (provider === "anthropic") return "Status chat requires ANTHROPIC_API_KEY.";
  return "Status chat requires OpenAI or Anthropic setup.";
}

function setChatNotice(output, message) {
  output.classList.remove("chat-empty", "chat-result", "chat-pending", "chat-error");
  output.classList.add("chat-unavailable", "muted");
  output.textContent = message;
}

function resetChatPlaceholder(output, message) {
  output.classList.remove("chat-unavailable", "chat-result", "chat-pending", "chat-error");
  output.classList.add("chat-empty", "muted");
  output.textContent = message;
}

function setChatPending(output, message) {
  output.classList.remove("chat-empty", "chat-unavailable", "chat-result", "chat-error");
  output.classList.add("chat-pending", "muted");
  output.textContent = message;
}

function setChatError(output, message) {
  output.classList.remove("chat-empty", "chat-unavailable", "chat-result", "chat-pending", "muted");
  output.classList.add("chat-error");
  output.textContent = message;
}

function setChatControlsBusy(input, button, busy) {
  for (const control of [input, button]) {
    if (!control) continue;
    control.disabled = !!busy;
    control.setAttribute("aria-busy", String(!!busy));
  }
}

function compactChatErrorMessage(error) {
  const message = String(error?.message || "");
  if (error?.data?.code === "openai_usage_limit") {
    return "OpenAI usage limit reached. Tell the admin if you need help right now.";
  }
  if (error?.status === 403) return "Status chat is not available right now.";
  if (/context|codex|responses api|api key|token|json|model/i.test(message)) {
    return "I could not check that right now. Tell the admin if this keeps happening.";
  }
  return "I could not check that right now. Tell the admin if this keeps happening.";
}

function submitOnEnter(event, handler) {
  if (event.key !== "Enter" || event.shiftKey || event.isComposing) return;
  event.preventDefault();
  handler();
}

function userStatusCard(label, value, summary, options = {}) {
  const status = normalizeStatus(value);
  const clickable = !!options.subject;
  const attrs = {
    class: `user-status-card status-row ${status}${clickable ? " has-detail" : ""}`,
    "data-status-kind": statusIconClass(label),
    "aria-label": `${label}: ${value}. ${summary || ""}`,
  };
  if (clickable) {
    attrs.role = "button";
    attrs.tabindex = "0";
    attrs["aria-label"] = options.detailLabel || attrs["aria-label"];
    attrs.onclick = (event) => openInfraDetail(options.subject, event.currentTarget);
    attrs.onkeydown = (event) => activateCardOnKey(event, () => openInfraDetail(options.subject, event.currentTarget));
  }
  return node("article", attrs,
    statusArtwork(label),
    node("div", { class: "status-copy" },
      node("span", { class: "status-title-line" },
        statusIndicator(value, status, "status-dot-only"),
        node("span", { class: "overview-label", text: label }),
      ),
      node("p", { class: "status-note", text: summary || "" }),
    ),
    clickable ? node("span", { class: "detail-chevron", "aria-hidden": "true", text: ">" }) : null,
  );
}

function renderUserAppCard(app) {
  const status = normalizeStatus(app.current_status);
  const statusText = plainAppStatus(app);
  return node("article", {
    class: `user-app-card app-row ${status}`,
    role: "button",
    tabindex: "0",
    "aria-label": `${appDisplayName(app)}: ${statusText}. Open details.`,
    onclick: (event) => openAppDetail(app.app_id, event.currentTarget),
    onkeydown: (event) => activateCardOnKey(event, () => openAppDetail(app.app_id, event.currentTarget)),
  },
    renderAppLogo(app),
    node("div", { class: "user-app-main" },
      node("div", { class: "app-title-line" },
        node("h3", { "data-app-name": "", text: appDisplayName(app) }),
        statusIndicator(statusText, status, "compact-app-status"),
      ),
      node("p", { class: "muted", text: plainAppSummary(app) }),
    ),
    node("span", { class: "detail-chevron", "aria-hidden": "true", text: ">" }),
  );
}

function activateCardOnKey(event, action) {
  if (event.key !== "Enter" && event.key !== " ") return;
  event.preventDefault();
  action();
}

function openAppDetail(appID, returnFocus = null) {
  const id = String(appID || "").trim();
  if (!id) return;
  state.userDetailID = id;
  state.userDetailSubject = "";
  state.userDetailReturnFocus = returnFocus instanceof HTMLElement ? returnFocus : document.activeElement;
  setCompactView("app-detail");
  loadAppDetail(id, { focus: true });
}

async function loadAppDetail(appID, options = {}) {
  const body = $("user-app-detail-body");
  if (!body) return;
  body.replaceChildren(node("div", { class: "detail-empty", text: "Loading details..." }));
  try {
    const encoded = encodeURIComponent(appID);
    const [app, history] = await Promise.all([
      api(`/api/apps/${encoded}`),
      api(`/api/apps/${encoded}/history?window=7d&limit=50`),
    ]);
    body.replaceChildren(renderAppDetail(app, history));
    $("page-title").textContent = appDisplayName(app);
    if (options.focus) $("user-app-detail-back")?.focus({ preventScroll: true });
  } catch (error) {
    body.replaceChildren(
      node("div", { class: "detail-empty" },
        node("strong", { text: "Could not load details." }),
        node("p", { text: "Go back and try again." }),
      ),
    );
    showNotice(error.message, "error");
  }
}

function openInfraDetail(subject, returnFocus = null) {
  const id = String(subject || "").trim().toLowerCase();
  if (!id) return;
  state.userDetailSubject = id;
  state.userDetailID = "";
  state.userDetailReturnFocus = returnFocus instanceof HTMLElement ? returnFocus : document.activeElement;
  setCompactView("infra-detail");
  loadInfraDetail(id, { focus: true });
}

async function loadInfraDetail(subject, options = {}) {
  const body = $("user-infra-detail-body");
  if (!body) return;
  body.replaceChildren(node("div", { class: "detail-empty", text: "Loading details..." }));
  try {
    const history = await api(`/api/infrastructure/history?subject=${encodeURIComponent(subject)}&window=7d&limit=50`);
    body.replaceChildren(renderInfraDetail(history));
    $("page-title").textContent = plainInfraSubjectName(subject);
    if (options.focus) $("user-infra-detail-back")?.focus({ preventScroll: true });
  } catch (error) {
    body.replaceChildren(
      node("div", { class: "detail-empty" },
        node("strong", { text: "Could not load details." }),
        node("p", { text: "Go back and try again." }),
      ),
    );
    showNotice(error.message, "error");
  }
}

function closeCompactDetail() {
  const returnFocus = state.userDetailReturnFocus;
  state.userDetailID = "";
  state.userDetailSubject = "";
  state.userDetailReturnFocus = null;
  setCompactView("status");
  if (returnFocus?.isConnected) returnFocus.focus({ preventScroll: true });
}

function renderAppDetail(app, history) {
  const status = normalizeStatus(app.current_status || history?.current);
  return node("div", { class: "detail-stack" },
    node("header", { class: "detail-header" },
      renderAppLogo(app),
      node("div", { class: "detail-title" },
        node("h2", { text: appDisplayName(app) }),
        statusIndicator(plainAppStatus(app), status, "compact-app-status"),
      ),
    ),
    node("section", { class: "detail-summary" },
      detailMetric("Right now", plainAppSummary(app)),
      detailMetric("Last working", relativeTime(history?.last_seen_online)),
      detailMetric("Last 7 days", uptimeText(history?.uptime_pct_7d)),
    ),
    userRepairDetailActions(app),
    node("section", { class: "detail-history" },
      node("h3", { text: "Recent changes" }),
      renderHistoryTimeline(history, "app"),
    ),
  );
}

function userRepairDetailActions(app) {
  if (hasAdminSurface() || !isUserRepairCandidate(app)) return null;
  const canRestart = !!app.restart_allowed_general_user;
  const request = latestUserRepairRequestForApp(app.app_id);
  const pending = request?.status === "pending";
  return node("section", { class: "detail-actions user-repair-actions" },
    canRestart ? node("button", {
      type: "button",
      class: "primary command",
      "data-glyph": "r",
      onclick: (event) => runUserAppRestart(app, event.currentTarget),
      text: "Restart now",
    }) : node("button", {
      type: "button",
      class: "primary command",
      "data-glyph": "!",
      disabled: pending,
      onclick: (event) => requestAdminRepairForApp(app, event.currentTarget),
      text: pending ? "Asked admin" : "Ask admin",
    }),
    request ? node("span", {
      class: `settings-state-pill user-repair-state ${userRepairRequestTone(request)}`,
      text: userRepairRequestStatusText(request),
    }) : null,
  );
}

function isUserRepairCandidate(app) {
  const status = normalizeStatus(app?.current_status);
  return status !== "online" && (app?.data_source === "unraid-docker" || app?.docker_state || app?.container_id || app?.container_name);
}

function requestAdminRepairForApp(app, button = null) {
  return requestAdminRepair({
    recommended_action_id: "ask_admin_to_restart_container",
    target: { id: app.app_id, label: appDisplayName(app) },
  }, {
    general_user_summary: plainAppSummary(app),
  }, button);
}

async function runUserAppRestart(app, button = null) {
  const appID = app?.app_id || "";
  const label = appDisplayName(app);
  if (!appID) {
    showNotice("App id is required.", "error");
    return;
  }
  if (!confirm(`Restart ${label}?`)) return;
  const original = button?.textContent || "";
  if (button) {
    button.disabled = true;
    button.textContent = "Restarting";
  }
  try {
    const result = await api(`/api/user/apps/${encodeURIComponent(appID)}/restart`, {
      method: "POST",
      body: JSON.stringify({ confirmed: true, confirm_app_id: appID }),
    });
    if (result.outcome) {
      showNotice(agentRepairOutcomeNotice(result.outcome), result.outcome.recovered ? "info" : "error");
    } else {
      showNotice(`Restart requested for ${label}.`);
    }
    await refresh();
  } catch (error) {
    showNotice(error.message, "error");
  } finally {
    if (button?.isConnected) {
      button.disabled = false;
      button.textContent = original;
    }
  }
}

function renderInfraDetail(history) {
  const status = normalizeStatus(history?.current);
  const subject = String(history?.subject_id || "").toLowerCase();
  const label = plainInfraSubjectName(subject);
  return node("div", { class: "detail-stack" },
    node("header", { class: "detail-header" },
      statusArtwork(subject === "nas" ? "Server" : "Router"),
      node("div", { class: "detail-title" },
        node("h2", { text: label }),
        statusIndicator(status === "online" ? "Working" : status === "offline" ? "Not working" : status === "degraded" ? "Problem" : "Unknown", status, "compact-app-status"),
      ),
    ),
    node("section", { class: "detail-summary" },
      detailMetric("Right now", infraStatusSummary(status)),
      detailMetric("Last 24 hours", uptimeText(history?.uptime_pct_24h)),
      detailMetric("Last 7 days", uptimeText(history?.uptime_pct_7d)),
    ),
    node("section", { class: "detail-history" },
      node("h3", { text: "Recent changes" }),
      renderHistoryTimeline(history, subject || "internet"),
    ),
  );
}

function detailMetric(label, value) {
  return node("div", { class: "detail-metric" },
    node("span", { text: label }),
    node("strong", { text: value || "Not enough history yet." }),
  );
}

function renderHistoryTimeline(history, kind) {
  const events = Array.isArray(history?.events) ? history.events : [];
  if (!events.length) return node("div", { class: "detail-empty", text: "No changes recorded yet." });
  return node("ol", { class: "history-list" },
    events.map((event) => {
      const status = normalizeStatus(event.to);
      return node("li", { class: `history-event ${status}` },
        node("span", { class: "history-dot", "aria-hidden": "true" }),
        node("span", { class: "history-copy" },
          node("strong", { text: historyEventText(event, kind) }),
          node("small", { text: relativeTime(event.at) }),
        ),
      );
    }),
  );
}

function historyEventText(event, kind) {
  const status = normalizeStatus(event?.to);
  if (kind === "nas") {
    if (status === "online") return "Server came back";
    if (status === "offline") return "Server stopped responding";
    if (status === "degraded") return "Server had a problem";
    return "Server status changed";
  }
  if (kind === "internet") {
    if (status === "online") return "Internet came back";
    if (status === "offline") return "Internet stopped working";
    if (status === "degraded") return "Internet had a problem";
    return "Internet status changed";
  }
  if (status === "online") return "Came back";
  if (status === "offline") return "Stopped working";
  if (status === "degraded") return "Had a problem";
  return "Status changed";
}

function plainInfraSubjectName(subject) {
  if (subject === "internet") return "Internet details";
  if (subject === "nas") return "Server details";
  return "Connection details";
}

function infraStatusSummary(status) {
  if (status === "online") return "Working normally.";
  if (status === "offline") return "Not responding.";
  if (status === "degraded") return "Having a problem.";
  return "Status unknown.";
}

function uptimeText(value) {
  const number = Number(value);
  if (!Number.isFinite(number)) return "Not enough history yet.";
  return `Working ${Math.max(0, Math.min(100, number)).toFixed(number >= 99 ? 1 : 0)}% of the time.`;
}

function relativeTime(value) {
  if (!value) return "Not recorded yet.";
  const date = new Date(value);
  const then = date.getTime();
  if (Number.isNaN(then)) return "Not recorded yet.";
  const diffMs = Date.now() - then;
  const absMs = Math.abs(diffMs);
  const minute = 60 * 1000;
  const hour = 60 * minute;
  const day = 24 * hour;
  if (absMs < minute) return "Just now";
  if (absMs < hour) {
    const minutes = Math.max(1, Math.round(absMs / minute));
    return `${minutes} minute${minutes === 1 ? "" : "s"} ago`;
  }
  if (absMs < day) {
    const hours = Math.round(absMs / hour);
    return `${hours} hour${hours === 1 ? "" : "s"} ago`;
  }
  if (absMs < 7 * day) {
    const days = Math.round(absMs / day);
    return `${days} day${days === 1 ? "" : "s"} ago`;
  }
  return date.toLocaleDateString();
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
  if (!infra.unraid_api_reachable) return "Reachable, but status checks are unavailable.";
  if (infra.unraid_array_state && infra.unraid_array_healthy === false) return "Needs attention.";
  if (infra.docker_service_available === false) return "Apps are unavailable.";
  return "Server checks look normal.";
}

function compactRouterSummary(infra) {
  const routerProbeKnown = hasProbeData(infra, "router");
  const internetProbeKnown = hasProbeData(infra, "internet");
  const dnsProbeKnown = hasProbeData(infra, "dns");
  const unifiKnown = hasCollectorData(infra, "unifi");
  if (!routerProbeKnown && !internetProbeKnown && !dnsProbeKnown && !unifiKnown) return "No live connection data.";
  if (routerProbeKnown && !infra.router_reachable) return "Home connection device is not reachable.";
  if (internetProbeKnown && !infra.internet_reachable) return "Home connection is reachable; internet check failed.";
  if (dnsProbeKnown && !infra.dns_ok) return "Name lookup check failed.";
  if (unifiKnown && !infra.unifi_gateway_reachable) return "Network status is unavailable.";
  if (unifiKnown && infra.unifi_wan_up === false) return "Internet link is down.";
  return "Internet checks look normal.";
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
      const requiresConfirmation = ["restart", "stop"].includes(action);
      if (requiresConfirmation && !confirm(`${label} ${app.display_name || app.app_id}?`)) return;
      runAppAction(app, action, event.currentTarget, requiresConfirmation);
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

async function runAppAction(app, action, button, confirmed = false) {
  const appID = app.app_id || app.container_name || app.display_name;
  if (!appID) {
    showNotice("App id is required.", "error");
    return;
  }
  const previous = button?.disabled;
  if (button) button.disabled = true;
  try {
    const payload = { action };
    if (confirmed) {
      payload.confirmed = true;
      payload.confirm_app_id = app.app_id || appID;
    }
    await api(`/api/admin/apps/${encodeURIComponent(appID)}/action`, {
      method: "POST",
      body: JSON.stringify(payload),
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
    { input: $("diagnostic-question"), button: $("diagnose"), busyKey: "diagnostic" },
  );
}

async function runUserChat() {
  await runDiagnosis(
    $("user-chat-input").value || "What is wrong right now?",
    $("user-chat-output"),
    { input: $("user-chat-input"), button: $("user-chat-send"), busyKey: "user" },
  );
}

function focusUserChat() {
  setCompactView("chat");
  const panel = document.querySelector(".panel.user-chat");
  if (!panel || panel.hidden) return;
  panel.scrollIntoView({ block: "nearest", inline: "nearest", behavior: "smooth" });
  $("user-chat-input").focus({ preventScroll: true });
}

async function runAssistantChat() {
  await runDiagnosis(
    $("assistant-input").value || "What is wrong right now?",
    $("assistant-output"),
    { input: $("assistant-input"), button: $("assistant-send"), busyKey: "assistant" },
  );
  $("assistant-panel").hidden = false;
}

async function runDiagnosis(question, output, options = {}) {
  const adminSurface = hasAdminSurface();
  const path = adminSurface ? "/api/admin/diagnose" : "/api/user/diagnose";
  const questionText = String(question || "What is wrong right now?").trim() || "What is wrong right now?";
  if (options.busyKey && state.chatBusy[options.busyKey]) return;
  if (options.busyKey) state.chatBusy[options.busyKey] = true;
  setChatPending(output, "Checking status...");
  setChatControlsBusy(options.input, options.button, true);
  try {
    const result = await api(path, {
      method: "POST",
      body: JSON.stringify({ question: questionText }),
    });
    output.classList.remove("chat-empty", "muted", "chat-pending", "chat-error");
    output.classList.add("chat-result");
    if (adminSurface) {
      output.replaceChildren(
        node("strong", { text: `${result.severity} confidence ${Math.round((result.confidence || 0) * 100)}%` }),
        node("p", { text: result.diagnosis || result.general_user_summary || "No diagnosis returned." }),
        result.evidence?.length ? node("p", { class: "muted", text: `Evidence: ${result.evidence.join("; ")}` }) : null,
        result.admin_message ? node("p", { class: "muted", text: result.admin_message }) : null,
        result.agent_plan ? renderAgentPlanPrompt(result.agent_plan) : null,
      );
      maybeOpenAgentApprovalDialog(result.agent_plan);
    } else {
      output.replaceChildren(
        node("strong", { text: "Answer" }),
        node("p", { text: result.general_user_summary || result.diagnosis || "I could not find a clear answer." }),
        result.agent_plan?.can_request_repair ? renderUserRepairRequestPrompt(result.agent_plan, result) : null,
        result.should_notify_admin ? node("p", { class: "muted", text: "Tell the admin if you need this fixed." }) : null,
      );
    }
    showNotice("Diagnosis completed.");
  } catch (error) {
    const message = adminSurface ? error.message : compactChatErrorMessage(error);
    setChatError(output, message);
    if (adminSurface) showNotice(error.message, "error");
  } finally {
    if (options.busyKey) state.chatBusy[options.busyKey] = false;
    setChatControlsBusy(options.input, options.button, false);
  }
}

function renderAgentPlanPrompt(plan) {
  const requiresApproval = !!plan.requires_admin_approval;
  const statusText = agentPlanStatusText(plan);
  const statusTone = agentPlanStatusTone(plan);
  const targetText = agentPlanTargetText(plan);
  return node("section", { class: "agent-plan-prompt" },
    node("div", { class: "agent-plan-head" },
      node("span", {},
        node("strong", { text: plan.title || "Agent fix plan" }),
        node("small", { text: plan.summary || "Review the model recommendation before allowing any action." }),
        targetText ? node("small", { text: targetText }) : null,
      ),
      node("span", { class: `settings-state-pill ${statusTone}`, text: statusText }),
    ),
    plan.auto_repair_message ? node("p", { class: "muted", text: plan.auto_repair_message }) : null,
    plan.outcome ? renderAgentRepairOutcome(plan.outcome) : null,
    requiresApproval ? node("div", { class: "agent-plan-actions" },
      node("button", {
        type: "button",
        class: "command",
        "data-glyph": "?",
        onclick: () => openAgentApprovalDialog(plan),
        text: "Open approval",
      }),
    ) : null,
  );
}

function agentPlanStatusText(plan) {
  if (plan?.auto_executed && plan?.outcome?.recovered) return "Fixed";
  if (plan?.auto_executed) return "Restart sent";
  switch (plan?.status) {
    case "approval_ready":
      return "Ready";
    case "approval_needs_arm":
      return "Arm first";
    case "approval_rate_limited":
      return "Limited";
    case "auto_review_refused":
      return "Review blocked";
    case "auto_execute_failed":
      return "Fix failed";
    case "target_unresolved":
      return "No target";
    default:
      return plan?.can_execute ? "Ready" : "Fix locked";
  }
}

function agentPlanStatusTone(plan) {
  if (plan?.auto_executed && plan?.outcome?.recovered) return "state-ok";
  if (plan?.auto_executed || plan?.can_execute || plan?.status === "approval_needs_arm") return "state-warn";
  if (plan?.status === "auto_review_refused" || plan?.status === "auto_execute_failed") return "state-bad";
  return "state-muted";
}

function renderUserRepairRequestPrompt(plan, diagnosis = {}) {
  const target = plan?.target || {};
  if (!plan?.can_request_repair || !target.id) return null;
  const label = target.label || target.id;
  const directRestart = !!plan.can_execute;
  return node("section", { class: "agent-plan-prompt user-repair-prompt" },
    node("div", { class: "agent-plan-head" },
      node("span", {},
        node("strong", { text: directRestart ? "Restart this app" : "Ask admin to fix this" }),
        node("small", { text: directRestart ? `${label} can be restarted from here.` : `${label} can be sent to an admin for review.` }),
      ),
      node("span", { class: `settings-state-pill ${directRestart ? "state-ok" : "state-warn"}`, text: directRestart ? "Ready" : "Admin review" }),
    ),
    node("div", { class: "agent-plan-actions" },
      directRestart ? node("button", {
        type: "button",
        class: "primary command",
        "data-glyph": "r",
        onclick: (event) => runUserAppRestart({ app_id: target.id, display_name: label }, event.currentTarget),
        text: "Restart now",
      }) : null,
      node("button", {
        type: "button",
        class: directRestart ? "command" : "primary command",
        "data-glyph": "!",
        onclick: (event) => requestAdminRepair(plan, diagnosis, event.currentTarget),
        text: "Ask admin",
      }),
    ),
  );
}

async function requestAdminRepair(plan, diagnosis = {}, button = null) {
  if (!plan?.target?.id) return;
  const original = button?.textContent || "";
  if (button) {
    button.disabled = true;
    button.textContent = "Sending";
  }
  try {
    const summary = diagnosis.general_user_summary || diagnosis.diagnosis || plan.summary || "A user asked for help with this app.";
    const result = await api("/api/user/repair-request", {
      method: "POST",
      body: JSON.stringify({
        app_id: plan.target.id,
        action_id: plan.recommended_action_id || "ask_admin_to_restart_container",
        diagnosis_summary: summary,
      }),
    });
    if (result.request) upsertUserRepairRequest(result.request);
    showNotice("Request sent to admin.");
    if (button) button.textContent = "Sent";
    refreshCompactRepairStatus();
  } catch (error) {
    showNotice(error.message, "error");
    if (button) {
      button.disabled = false;
      button.textContent = original;
    }
  }
}

function upsertUserRepairRequest(request) {
  if (!request?.id) return;
  const existing = state.userRepairRequests.findIndex((item) => item.id === request.id);
  if (existing >= 0) state.userRepairRequests[existing] = request;
  else state.userRepairRequests.unshift(request);
  state.userRepairRequests.sort((a, b) => String(b.created_at || "").localeCompare(String(a.created_at || "")));
}

function latestUserRepairRequestForApp(appID) {
  const id = String(appID || "").trim();
  if (!id) return null;
  return (state.userRepairRequests || []).find((request) => request.app_id === id) || null;
}

function userRepairRequestTone(request) {
  if (request?.status === "pending") return "state-warn";
  if (request?.status === "executed" && request?.outcome?.recovered) return "state-ok";
  if (request?.status === "failed") return "state-bad";
  return "state-muted";
}

function userRepairRequestStatusText(request) {
  if (request?.status === "pending") return "Waiting for admin";
  if (request?.status === "denied") return "Admin declined";
  if (request?.status === "failed") return "Fix failed";
  if (request?.status === "executed") {
    if (request?.outcome?.recovered) return "Fixed";
    return "Admin ran fix";
  }
  return "Request updated";
}

function refreshCompactRepairStatus() {
  if (hasAdminSurface()) return;
  if (state.userView === "app-detail" && state.userDetailID) {
    loadAppDetail(state.userDetailID, { focus: false });
  } else if (state.snapshot) {
    renderUserHome(state.snapshot);
  }
}

function agentPlanTargetText(plan) {
  const target = plan?.target || {};
  if (target.resolved && target.label) return `Target: ${target.label}`;
  if (target.resolved && target.id) return `Target: ${target.id}`;
  if (target.reason && target.kind === "app") return `Target not resolved: ${target.reason}`;
  return "";
}

function normalizeAgentApprovalOptions(plan) {
  const rawOptions = Array.isArray(plan?.options) ? plan.options : [];
  const options = [];
  const seen = new Set();
  for (const raw of rawOptions) {
    const id = String(raw?.id || "").trim();
    if (!id || seen.has(id)) continue;
    seen.add(id);
    options.push({
      id,
      label: String(raw?.label || id).trim(),
      description: String(raw?.description || "").trim(),
      enabled: raw?.enabled !== false,
      selected: !!raw?.selected,
      reason: String(raw?.reason || "").trim(),
    });
  }
  if (options.length) return options;
  return [
    {
      id: "deny",
      label: "Do not allow",
      description: "Keep the diagnosis and do not permit an automatic fix.",
      enabled: true,
      selected: true,
      reason: "",
    },
    {
      id: "allow_once",
      label: "Allow fix",
      description: "Permit this single fix attempt.",
      enabled: !!plan?.can_execute,
      selected: false,
      reason: plan?.can_execute ? "" : "Arm this session and enable automatic repair for the target app first.",
    },
  ];
}

function initialAgentApprovalChoice(options) {
  return (options.find((option) => option.enabled && option.selected) || options.find((option) => option.enabled) || options[0] || {}).id || "deny";
}

function maybeOpenAgentApprovalDialog(plan) {
  if (!plan || !plan.requires_admin_approval) return;
  window.setTimeout(() => {
    if (state.agentApprovalDialog) return;
    openAgentApprovalDialog(plan);
  }, 0);
}

function openAgentApprovalDialog(plan) {
  closeAgentApprovalDialog({ returnFocus: false });
  const previousFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null;
  const approvalOptions = normalizeAgentApprovalOptions(plan);
  let selectedChoice = initialAgentApprovalChoice(approvalOptions);
  const optionRows = [];
  const closeButton = node("button", {
    type: "button",
    class: "command ghost",
    "data-glyph": "x",
    "aria-label": "Close fix approval dialog",
    onclick: () => closeAgentApprovalDialog(),
    text: "Close",
  });
  const submitButton = node("button", {
    type: "button",
    class: "primary command",
    "data-glyph": "v",
    onclick: () => submitAgentApproval(plan, selectedChoice, submitButton),
    text: "Submit choice",
  });
  const statusNode = node("p", {
    class: "openai-auth-status",
    "aria-live": "polite",
    "data-tone": plan.can_execute ? "info" : "bad",
    text: plan.can_execute ? "Approval will run one restart for the selected app." : "Automatic fixes require per-app opt-in and an armed admin session.",
  });
  const updateSelection = () => {
    const selectedOption = approvalOptions.find((option) => option.id === selectedChoice);
    const enabled = !!selectedOption?.enabled;
    submitButton.disabled = !enabled;
    submitButton.textContent = selectedChoice === "deny" ? "Do not allow" : selectedOption?.label || "Submit choice";
    for (const item of optionRows) {
      item.row.classList.toggle("selected", item.option.id === selectedChoice);
    }
    if (selectedOption?.reason) {
      statusNode.dataset.tone = "bad";
      statusNode.textContent = selectedOption.reason;
    } else if (selectedChoice === "deny") {
      statusNode.dataset.tone = "info";
      statusNode.textContent = "No automatic fix will run.";
    } else {
      statusNode.dataset.tone = "info";
      statusNode.textContent = "NoobBoard will run one restart for this app.";
    }
  };
  const optionNodes = approvalOptions.map((option) => {
    const input = node("input", {
      type: "radio",
      name: "agent-approval-choice",
      value: option.id,
      checked: option.id === selectedChoice,
      disabled: !option.enabled,
      onchange: () => {
        selectedChoice = option.id;
        updateSelection();
      },
    });
    const row = node("label", { class: `agent-approval-option${option.enabled ? "" : " disabled"}${option.id === selectedChoice ? " selected" : ""}` },
      input,
      node("span", { class: "agent-approval-option-text" },
        node("strong", { text: option.label || option.id }),
        option.description ? node("small", { text: option.description }) : null,
        option.reason ? node("small", { class: "agent-approval-option-reason", text: option.reason }) : null,
      ),
    );
    optionRows.push({ option, row, input });
    return row;
  });
  const dialog = node("section", {
    class: "openai-auth-dialog agent-approval-dialog",
    role: "dialog",
    "aria-modal": "true",
    "aria-labelledby": "agent-approval-title",
  },
    node("header", { class: "openai-auth-head" },
      node("div", { class: "openai-auth-title-group" },
        node("span", { class: "openai-auth-mark", "aria-hidden": "true", text: "AI" }),
        node("div", {},
          node("p", { class: "eyebrow", text: "Agent approval" }),
          node("h2", { id: "agent-approval-title", text: "Allow automatic fix?" }),
        ),
      ),
      closeButton,
    ),
    node("div", { class: "openai-auth-content agent-approval-content" },
      node("p", { class: "openai-auth-copy", text: plan.summary || "The model suggested a fix. Approve or deny before allowing any automatic action." }),
      node("div", { class: "agent-approval-request" },
        node("span", { class: "openai-auth-label", text: "Requested fix" }),
        node("strong", { text: plan.title || plan.recommended_action_id || "Unknown action" }),
        agentPlanTargetText(plan) ? node("small", { text: agentPlanTargetText(plan) }) : null,
      ),
      node("fieldset", { class: "agent-approval-options" },
        node("legend", { class: "openai-auth-label", text: "Approval choice" }),
        optionNodes,
      ),
      node("div", { class: "openai-auth-actions" },
        submitButton,
        node("button", { type: "button", class: "command", "data-glyph": "x", onclick: () => closeAgentApprovalDialog(), text: "Close" }),
      ),
    ),
    statusNode,
  );
  const backdrop = node("div", { class: "openai-auth-backdrop agent-approval-backdrop" }, dialog);
  backdrop.addEventListener("click", (event) => {
    if (event.target === backdrop) closeAgentApprovalDialog();
  });
  backdrop.addEventListener("keydown", handleAgentApprovalDialogKeydown);
  document.body.append(backdrop);
  document.body.classList.add("agent-approval-open");
  state.agentApprovalDialog = { backdrop, dialog, previousFocus };
  updateSelection();
  const selectedInput = optionRows.find((item) => item.option.id === selectedChoice && !item.input.disabled)?.input;
  (selectedInput || closeButton).focus({ preventScroll: true });
}

async function submitAgentApproval(plan, choice, button) {
  const selectedChoice = String(choice || "deny").trim();
  const originalText = button?.textContent || "";
  if (button) {
    button.disabled = true;
    button.textContent = "Recording";
  }
  try {
    const response = await api("/api/admin/agent/approval", {
      method: "POST",
      body: JSON.stringify({
        approval_token: plan.approval_token || "",
        choice: selectedChoice,
      }),
    });
    if (selectedChoice === "allow_once" && response.outcome) {
      appendAgentRepairOutcome(response.outcome);
      showNotice(agentRepairOutcomeNotice(response.outcome), response.outcome.recovered ? "info" : "error");
    } else {
      showNotice("Automatic fix was not allowed.");
    }
    closeAgentApprovalDialog();
  } catch (error) {
    showNotice(error.message, "error");
  } finally {
    if (button?.isConnected) {
      button.disabled = false;
      button.textContent = originalText;
    }
  }
}

function appendAgentRepairOutcome(outcome) {
  const prompt = document.querySelector(".agent-plan-prompt");
  if (!prompt) return;
  prompt.querySelector(".agent-repair-outcome")?.remove();
  prompt.append(renderAgentRepairOutcome(outcome));
}

function renderAgentRepairOutcome(outcome) {
  const recovered = !!outcome?.recovered;
  const verified = !!outcome?.verified;
  const before = repairStatusText(outcome?.before_status);
  const after = repairStatusText(outcome?.after_status);
  const message = String(outcome?.message || "").trim() || (verified ? "Repair verification completed." : "Repair was sent, but verification did not complete.");
  return node("div", { class: `agent-repair-outcome ${recovered ? "recovered" : verified ? "unresolved" : "unverified"}` },
    node("strong", { text: recovered ? "Recovered" : verified ? "Still needs attention" : "Verification incomplete" }),
    node("span", { text: `${before} -> ${after}` }),
    node("small", { text: message }),
  );
}

function agentRepairOutcomeNotice(outcome) {
  if (outcome?.recovered) return "Approved fix ran and the app recovered.";
  if (outcome?.verified) return "Approved fix ran, but the app still is not responding.";
  return "Approved fix ran, but verification did not complete.";
}

function repairStatusText(status) {
  const value = String(status || "unknown").trim();
  if (!value) return "unknown";
  return value.replace(/_/g, " ");
}

function closeAgentApprovalDialog(options = {}) {
  const current = state.agentApprovalDialog;
  if (!current) return;
  current.backdrop.remove();
  document.body.classList.remove("agent-approval-open");
  state.agentApprovalDialog = null;
  if (options.returnFocus !== false && current.previousFocus?.isConnected) {
    current.previousFocus.focus({ preventScroll: true });
  }
}

function handleAgentApprovalDialogKeydown(event) {
  const current = state.agentApprovalDialog;
  if (!current) return;
  if (event.key === "Escape") {
    event.preventDefault();
    closeAgentApprovalDialog();
    return;
  }
  if (event.key !== "Tab") return;
  const focusable = openAIAuthDialogFocusableElements(current.dialog);
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

async function loadRepairRequests() {
  if (!hasAdminSurface()) return;
  const output = $("repair-requests-output");
  if (!output) return;
  output.replaceChildren(node("div", { class: "empty", text: "Loading requests..." }));
  try {
    const data = await api("/api/admin/repair-requests");
    state.repairRequests = Array.isArray(data) ? data : [];
    renderRepairRequests();
  } catch (error) {
    state.repairRequests = [];
    output.replaceChildren(node("div", { class: "empty", text: error.message }));
  }
}

async function loadUserRepairRequests(options = {}) {
  if (hasAdminSurface()) return;
  try {
    const data = await api("/api/user/repair-requests");
    state.userRepairRequests = Array.isArray(data) ? data : [];
    if (options.render !== false) refreshCompactRepairStatus();
  } catch (error) {
    state.userRepairRequests = [];
    if (!options.quiet) showNotice(error.message, "error");
  }
}

function renderRepairRequests() {
  const output = $("repair-requests-output");
  if (!output) return;
  const requests = state.repairRequests || [];
  if (!requests.length) {
    output.replaceChildren(node("div", { class: "empty", text: "No repair requests." }));
    return;
  }
  output.replaceChildren(...requests.map(renderRepairRequestRow));
}

function renderRepairRequestRow(request) {
  const pending = request.status === "pending";
  const outcome = request.outcome || {};
  const note = outcome.message || request.resolution_note || request.diagnosis_summary || "No details provided.";
  return node("article", { class: `repair-request-row ${request.status || "pending"}` },
    node("div", { class: "repair-request-main" },
      node("strong", { text: request.app_label || request.app_id || "App" }),
      node("span", { class: "muted", text: `${request.requester_name || "User"} - ${formatTime(request.created_at)}` }),
      node("p", { text: note }),
    ),
    node("div", { class: "repair-request-actions" },
      node("span", { class: `settings-state-pill ${pending ? "state-warn" : outcome.recovered ? "state-ok" : "state-muted"}`, text: request.status || "pending" }),
      pending ? node("button", {
        type: "button",
        class: "primary command",
        "data-glyph": "v",
        onclick: (event) => decideRepairRequest(request.id, "approve", event.currentTarget),
        text: "Approve",
      }) : null,
      pending ? node("button", {
        type: "button",
        class: "command",
        "data-glyph": "x",
        onclick: (event) => decideRepairRequest(request.id, "deny", event.currentTarget),
        text: "Deny",
      }) : null,
    ),
  );
}

async function decideRepairRequest(id, choice, button = null) {
  const original = button?.textContent || "";
  if (button) {
    button.disabled = true;
    button.textContent = choice === "approve" ? "Running" : "Saving";
  }
  try {
    const result = await api(`/api/admin/repair-requests/${encodeURIComponent(id)}/decision`, {
      method: "POST",
      body: JSON.stringify({ choice }),
    });
    if (result.outcome) {
      showNotice(agentRepairOutcomeNotice(result.outcome), result.outcome.recovered ? "info" : "error");
    } else {
      showNotice(choice === "approve" ? "Repair request approved." : "Repair request denied.");
    }
    await loadRepairRequests();
  } catch (error) {
    showNotice(error.message, "error");
    if (button) {
      button.disabled = false;
      button.textContent = original;
    }
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
    state.roleApps = hydrateRoleApps(data.apps || []);
    state.roleUsers = data.users || [];
    state.roleUsersOriginal = clone(data.users || []);
    renderRoleSettings();
  } catch (error) {
    $("role-settings").replaceChildren(node("div", { class: "empty", text: error.message }));
  }
}

function hydrateRoleApps(apps) {
  const snapshotApps = new Map((state.snapshot?.apps || []).flatMap((app) => {
    const keys = [
      app.app_id,
      app.container_name,
      app.display_name,
    ].map((value) => String(value || "").trim().toLowerCase()).filter(Boolean);
    return keys.map((key) => [key, app]);
  }));
  return apps.map((app) => {
    const keys = [
      app.app_id,
      app.container_name,
      app.display_name,
    ].map((value) => String(value || "").trim().toLowerCase()).filter(Boolean);
    const live = keys.map((key) => snapshotApps.get(key)).find(Boolean);
    if (!live) return app;
    return {
      ...live,
      ...app,
      icon_url: app.icon_url || live.icon_url,
      icon_source: app.icon_source || live.icon_source,
      web_url: app.web_url || live.web_url,
      image_ref: app.image_ref || live.image_ref,
      template_path: app.template_path || live.template_path,
    };
  });
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
    node("div", { class: "role-workspace" },
      renderRoleSidebar(roles, visibility.default_role),
      renderRoleDetail(selectedRole, visibility.default_role, roles, visibility),
    ),
  );
  const userControls = renderUserManagement(roles);
  $("role-settings").replaceChildren(roleControls, userControls);
}

function renderRoleSidebar(roles, defaultRole) {
  return node("aside", { class: "role-sidebar management-sidebar" },
    node("div", { class: "management-sidebar-head" },
      node("div", {},
        node("h3", { text: "Roles" }),
        node("p", { class: "muted", text: `${roles.length} configured` }),
      ),
      node("button", {
        type: "button",
        class: "command",
        "data-glyph": "+",
        onclick: addRole,
        text: "Create role",
      }),
    ),
    node("nav", { class: "management-list", "aria-label": "Roles" }, roles.map((role) => roleNavItem(role, defaultRole))),
  );
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

function renderRoleDetail(role, defaultRole, roles, visibility) {
  const visibleCount = visibleRoleAppCount(role);
  const members = usersForRole(role.role);
  const hiddenCount = Math.max(0, state.roleApps.length - visibleCount);
  return node("article", { class: "role-detail" },
    node("header", { class: "role-detail-head" },
      node("div", {},
        node("h3", { text: role.display_name || role.role }),
        node("p", { class: "muted", text: role.role }),
      ),
      node("div", { class: "role-detail-actions" },
        role.role === defaultRole ? node("span", { class: "role-badge", text: "Default" }) : null,
        node("label", { class: "role-default-field" },
          node("span", { text: "Default role" }),
          node("select", {
            onchange: (event) => {
              visibility.default_role = event.target.value;
              renderRoleSettings();
            },
          }, roles.map((item) => node("option", {
            value: item.role,
            selected: item.role === defaultRole,
            text: item.display_name || item.role,
          }))),
        ),
        node("button", {
          type: "button",
          class: "primary command",
          "data-glyph": "v",
          onclick: saveRoleAccess,
          text: "Save role access",
        }),
      ),
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
      node("span", { class: `settings-state-pill ${changed ? "state-warn" : "state-ok"}`, text: changed ? `${changed} pending` : "Saved" }),
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
  ensureSelectedUser();
  const selected = state.roleUsers.find((user) => user.username === state.selectedUser);
  const creating = state.selectedUser === NEW_USER_KEY || !selected;
  return node("section", { class: "user-editor" },
    node("div", { class: "user-management-grid" },
      renderUserSidebar(userRoles),
      renderUserDetail(creating ? null : selected, userRoles),
    ),
  );
}

function ensureSelectedUser() {
  if (state.selectedUser === NEW_USER_KEY) return;
  if (state.roleUsers.some((user) => user.username === state.selectedUser)) return;
  state.selectedUser = state.roleUsers[0]?.username || NEW_USER_KEY;
}

function renderUserSidebar(userRoles) {
  return node("aside", { class: "user-list management-sidebar" },
    node("div", { class: "management-sidebar-head" },
      node("div", {},
        node("h3", { text: "User Accounts" }),
        node("p", { class: "muted", text: `${state.roleUsers.length} user${state.roleUsers.length === 1 ? "" : "s"}` }),
      ),
      node("button", {
        type: "button",
        class: `command${state.selectedUser === NEW_USER_KEY ? " active" : ""}`,
        "data-glyph": "+",
        onclick: () => {
          state.selectedUser = NEW_USER_KEY;
          renderRoleSettings();
        },
        text: "Create user",
      }),
    ),
    node("nav", { class: "management-list", "aria-label": "User accounts" },
      state.roleUsers.length ? state.roleUsers.map((user) => userNavItem(user, userRoles)) : node("div", { class: "empty inline-empty", text: "No users yet." }),
    ),
  );
}

function userNavItem(user, userRoles) {
  const active = user.username === state.selectedUser;
  return node("button", {
    type: "button",
    class: `user-row${active ? " active" : ""}${user.disabled ? " disabled" : ""}`,
    "aria-pressed": String(active),
    onclick: () => {
      state.selectedUser = user.username;
      renderRoleSettings();
    },
  },
    node("span", { class: "user-row-main" },
      node("strong", { text: user.display_name || user.username }),
      node("small", { text: user.username }),
    ),
    node("span", { class: `user-role ${user.disabled ? "disabled" : ""}`, text: user.disabled ? "Disabled" : roleDisplayName(userRoles, user.role) }),
  );
}

function renderUserDetail(user, userRoles) {
  const creating = !user;
  const defaultRole = state.roleVisibility?.default_role === "admin" ? "general_user" : (state.roleVisibility?.default_role || "general_user");
  const username = node("input", {
    value: user?.username || "",
    placeholder: "username",
    autocomplete: "off",
    disabled: !creating,
  });
  const displayName = node("input", {
    value: user?.display_name || "",
    placeholder: "Display name",
    autocomplete: "off",
  });
  const roleSelect = node("select", {}, userRoles.map((role) => node("option", {
    value: role.role,
    selected: role.role === (user?.role || defaultRole),
    text: role.display_name || role.role,
  })));
  const password = node("input", {
    placeholder: creating ? "Required for new user" : "Leave blank to keep password",
    type: "password",
    autocomplete: "new-password",
  });
  const disabled = node("input", { type: "checkbox", checked: !!user?.disabled });
  return node("article", { class: "user-detail" },
    node("header", { class: "user-detail-head" },
      node("div", {},
        node("h3", { text: creating ? "Create User" : (user.display_name || user.username) }),
        node("p", { class: "muted", text: creating ? "Add a named account for the admin panel or compact app." : user.username }),
      ),
      creating ? null : node("span", { class: `user-role ${user.disabled ? "disabled" : ""}`, text: user.disabled ? "Disabled" : roleDisplayName(userRoles, user.role) }),
    ),
    node("div", { class: "user-form" },
      node("label", {}, "Username", username),
      node("label", {}, "Display name", displayName),
      node("label", {}, "Role", roleSelect),
      node("label", {}, creating ? "Password" : "New password", password),
      node("label", { class: "toggle-line" }, disabled, "Disabled"),
      node("div", { class: "user-form-actions" },
        node("button", {
          type: "button",
          class: "primary command",
          "data-glyph": "v",
          onclick: () => saveUser({ username, displayName, roleSelect, password, disabled, creating }),
          text: creating ? "Create user" : "Save changes",
        }),
      ),
    ),
  );
}

function roleDisplayName(roles, roleName) {
  const role = roles.find((item) => item.role === roleName);
  return role?.display_name || String(roleName || "").replaceAll("_", " ");
}

async function saveRoleAccess() {
  const original = new Map((state.roleUsersOriginal || []).map((user) => [user.username, String(user.role || "")]));
  const changedAssignments = assignableRoleUsers().filter((user) => String(user.role || "") !== (original.get(user.username) || ""));
  try {
    const saved = await api("/api/admin/settings/roles", {
      method: "POST",
      body: JSON.stringify(state.roleVisibility),
    });
    state.roleVisibility = clone(saved);
    for (const user of changedAssignments) {
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
    await refresh();
    await loadRoleSettings();
    showNotice(changedAssignments.length
      ? `Role access and ${changedAssignments.length} assignment${changedAssignments.length === 1 ? "" : "s"} saved.`
      : "Role access saved.");
  } catch (error) {
    showNotice(error.message, "error");
  }
}

async function saveUser(fields) {
  const username = fields.username.value.trim();
  if (!username) {
    showNotice("Username is required.", "error");
    fields.username.focus();
    return;
  }
  if (fields.creating && !fields.password.value) {
    showNotice("Password is required for a new user.", "error");
    fields.password.focus();
    return;
  }
  try {
    const saved = await api("/api/admin/users", {
      method: "POST",
      body: JSON.stringify({
        username,
        display_name: fields.displayName.value.trim(),
        role: fields.roleSelect.value,
        password: fields.password.value,
        disabled: fields.disabled.checked,
      }),
    });
    fields.password.value = "";
    state.selectedUser = saved.username || username;
    showNotice(`${saved.username} saved.`);
    await loadRoleSettings();
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
    ["Apps", "Hide application identifiers before they reach user-facing views or LLM context.", [
      ["blacklist_app_ids", "App IDs", "app id"],
      ["blacklist_container_names", "Container names", "container name"],
      ["blacklist_display_names", "Display names", "display name"],
    ]],
    ["Storage", "Redact private paths, shares, and filename patterns from status details.", [
      ["blacklist_folder_paths", "Folder paths", "/mnt/user/private"],
      ["blacklist_share_names", "Share names", "share"],
      ["blacklist_file_paths", "File paths", "path"],
      ["blacklist_filename_globs", "Filename globs", "*.key"],
    ]],
    ["Network and logs", "Remove sensitive hosts, addresses, accounts, URLs, and log patterns.", [
      ["blacklist_log_patterns", "Log patterns", "pattern"],
      ["blacklist_env_names", "Environment names", "*_KEY"],
      ["blacklist_url_patterns", "URL patterns", "url pattern"],
      ["blacklist_hostnames", "Hostnames", "host"],
      ["blacklist_ips", "IP addresses", "ip"],
      ["blacklist_usernames", "Usernames", "username"],
    ]],
  ];
  const editors = new Map();
  const sections = groups.map(([title, description, fields]) => node("section", { class: "settings-subsection settings-blacklist-section" },
    node("h4", { text: title }),
    node("p", { class: "muted", text: description }),
    node("div", { class: "settings-blacklist-grid" }, fields.map(([key, label, placeholder]) => {
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
      node("p", { class: "muted", text: "Global redaction switches applied after blacklist matching." }),
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
  const repairAllowed = { ...(data?.agent_repair_allowed || {}) };
  const userRestartEnabled = settingToggle("Allow standard-user restart buttons", !!data?.general_user_restarts_enabled);
  const userRestartAllowed = { ...(data?.restart_allowed_general_user || {}) };
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
    const repairToggle = settingToggle("Allow automatic repair", key ? !!repairAllowed[key] : false);
    const userRestartToggle = settingToggle("Allow user restart", key ? !!userRestartAllowed[key] : false);
    appRows.push({ key, input, repairInput: repairToggle.input, userRestartInput: userRestartToggle.input });
    return node("div", { class: "settings-app-image-row" },
      renderAppLogo(app),
      node("span", { class: "settings-row-main" },
        node("strong", { text: app.display_name || key || "App" }),
        node("small", { text: app.icon_source ? `Image: ${app.icon_source}` : "No image source" }),
      ),
      node("div", { class: "settings-app-controls" },
        input,
        repairToggle.element,
        userRestartToggle.element,
      ),
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
      node("h4", { text: "Standard-user restarts" }),
      userRestartEnabled.element,
      node("p", { class: "muted", text: "When enabled, only apps with the per-app user restart toggle can show a compact-view restart button." }),
    ),
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
    const agent_repair_allowed = {};
    const restart_allowed_general_user = {};
    for (const row of appRows) {
      const url = row.input.value.trim();
      if (row.key && url) icon_overrides[row.key] = url;
      if (row.key && row.repairInput.checked) agent_repair_allowed[row.key] = true;
      if (row.key && row.userRestartInput.checked) restart_allowed_general_user[row.key] = true;
    }
    for (const row of extraRows) {
      const key = row.keyInput.value.trim();
      const url = row.urlInput.value.trim();
      if (key && url) icon_overrides[key] = url;
    }
    for (const [key, allowed] of Object.entries(repairAllowed)) {
      if (allowed && !appKeys.has(key)) agent_repair_allowed[key] = true;
    }
    for (const [key, allowed] of Object.entries(userRestartAllowed)) {
      if (allowed && !appKeys.has(key)) restart_allowed_general_user[key] = true;
    }
    return saveSettingsPayload(item.title, item.path, {
      icon_overrides,
      agent_repair_allowed,
      general_user_restarts_enabled: userRestartEnabled.input.checked,
      restart_allowed_general_user,
    }, status);
  });
}

function renderLLMSettings(item, data) {
  const settings = clone(data || {});
  const enabled = settingToggle("Use LLM diagnosis", settings.enabled !== false);
  const provider = settingSelectField("Provider", settings.provider || "disabled", [
    { value: "disabled", label: "Disabled" },
    { value: "openai", label: "OpenAI" },
    { value: "anthropic", label: "Anthropic" },
  ]);
  const authMethodName = `openai-auth-${Date.now()}`;
  const selectedAuthMethod = settings.openai_auth_method || "api_key";
  const browserStatus = node("span", { class: `settings-key-state ${settings.chatgpt_connected ? "set" : ""}`, text: settings.chatgpt_connected ? "Connected" : "Not connected" });
  const browserMessage = node("p", { class: "muted settings-connection-detail", "aria-live": "polite" });
  const headlessMessage = node("div", { class: "muted settings-connection-detail", "aria-live": "polite", text: "Use this from phones, LAN devices, or when the browser login is not available." });
  const headlessConnect = node("button", {
    type: "button",
    class: "command",
    "data-glyph": ">",
    onclick: () => connectOpenAIChatGPTHeadless(headlessMessage, headlessConnect),
    text: "Get code",
  });
  const browserConnect = node("button", {
    type: "button",
    class: "command",
    "data-glyph": ">",
    onclick: () => connectOpenAIChatGPTBrowser(browserMessage, browserConnect, headlessMessage, headlessConnect),
    text: settings.chatgpt_connected ? "Reconnect" : "Connect",
  });
  const authChoices = [
    settingChoice(authMethodName, "chatgpt_browser", "ChatGPT Pro/Plus (browser)", "Requires opening this admin page as localhost on the NoobBoard host.", selectedAuthMethod === "chatgpt_browser", node("div", { class: "settings-choice-action" }, browserStatus, browserConnect)),
    settingChoice(authMethodName, "chatgpt_headless", "ChatGPT Pro/Plus (code)", "Shows a login code for another browser or device.", selectedAuthMethod === "chatgpt_headless", node("div", { class: "settings-choice-action" }, headlessConnect)),
    settingChoice(authMethodName, "api_key", "API key", "Uses a saved OpenAI API key.", selectedAuthMethod === "api_key"),
  ];
  const openAIModel = settingSelectField("OpenAI API model", knownModelValue(OPENAI_MODEL_OPTIONS, settings.openai_model, "gpt-5.5"), OPENAI_MODEL_OPTIONS);
  const chatGPTModel = settingSelectField("ChatGPT Codex model", knownModelValue(CHATGPT_CODEX_MODEL_OPTIONS, settings.openai_model, "gpt-5.3-codex"), CHATGPT_CODEX_MODEL_OPTIONS);
  const anthropicModel = settingSelectField("Anthropic model", knownModelValue(ANTHROPIC_MODEL_OPTIONS, settings.anthropic_model, "claude-sonnet-4-5"), ANTHROPIC_MODEL_OPTIONS);
  const timeout = durationSecondsField("Timeout", settings.timeout || 45000000000);
  const agentControlEnabled = settingToggle("Enable action approval gate", !!settings.agent_control_enabled);
  const agentAutoRepairEnabled = settingToggle("Enable autonomous app restart", !!settings.agent_auto_repair_enabled);
  const agentArmDuration = durationSecondsField("Arm window", settings.agent_arm_duration || settings.agent_readiness?.agent_arm_duration || 600000000000);
  const actionAutoReviewEnabled = settingToggle("Require auto-review before fixes", !!settings.action_auto_review_enabled);
  const actionAutoReviewModel = settingSelectField("Auto-review model", settings.action_auto_review_model || "same", actionReviewModelOptions(settings));
  const actionAutoReviewReasoning = settingSelectField("Auto-review reasoning", settings.action_auto_review_reasoning || "", [
    { value: "", label: "Provider default" },
    { value: "low", label: "Low" },
    { value: "medium", label: "Medium" },
    { value: "high", label: "High" },
    { value: "xhigh", label: "Extra high" },
  ]);
  const actionAutoReviewReferences = listEditor(
    "Reference files",
    settings.action_auto_review_reference_paths || [],
    "docs/security.md",
  );
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
  const clearChatGPT = settingToggle("Forget ChatGPT login", false);
  const clearAnthropic = settingToggle("Clear saved Anthropic key", false);
  clearOpenAI.input.disabled = !settings.openai_api_key_set;
  clearChatGPT.input.disabled = !settings.chatgpt_connected;
  clearAnthropic.input.disabled = !settings.anthropic_api_key_set;
  const policyEditors = Object.entries(settings.policies || {}).map(([name, policy]) => policyEditor(name, policy));
  const apiKeyBlock = node("div", { class: "settings-field-stack" },
    keyFieldWithState(openAIKey, settings.openai_api_key_set),
    clearOpenAI.element,
  );
  const openAISection = node("section", { class: "settings-subsection" },
    node("h4", { text: "OpenAI login" }),
    node("div", { class: "settings-choice-list" }, authChoices.map((choice) => choice.element)),
    browserMessage,
    headlessMessage,
    node("p", { class: "muted", text: "API-key mode uses OpenAI API models. ChatGPT login uses Codex models only; unsupported saved values fall back to the default Codex model." }),
    node("div", { class: "settings-field-grid" }, openAIModel.element, chatGPTModel.element),
    apiKeyBlock,
    clearChatGPT.element,
  );
  const anthropicSection = node("section", { class: "settings-subsection" },
    node("h4", { text: "Anthropic" }),
    node("div", { class: "settings-field-grid" },
      anthropicModel.element,
      keyFieldWithState(anthropicKey, settings.anthropic_api_key_set),
    ),
    clearAnthropic.element,
  );
  const syncLLMVisibility = () => {
    const selectedProvider = provider.input.value;
    const method = authChoices.find((choice) => choice.input.checked)?.input.value || "api_key";
    openAISection.hidden = selectedProvider !== "openai";
    anthropicSection.hidden = selectedProvider !== "anthropic";
    apiKeyBlock.hidden = method !== "api_key";
    openAIModel.element.hidden = method !== "api_key";
    chatGPTModel.element.hidden = method === "api_key";
    browserMessage.hidden = selectedProvider !== "openai" || method !== "chatgpt_browser" || !browserMessage.textContent;
    headlessMessage.hidden = selectedProvider !== "openai" || method !== "chatgpt_headless";
    for (const choice of authChoices) choice.element.classList.toggle("selected", choice.input.checked);
  };
  provider.input.addEventListener("change", syncLLMVisibility);
  for (const choice of authChoices) choice.input.addEventListener("change", syncLLMVisibility);
  const body = node("div", { class: "settings-form" },
    node("section", { class: "settings-subsection" },
      node("h4", { text: "Diagnosis provider" }),
      node("div", { class: "settings-field-grid" }, provider.element),
      node("div", { class: "settings-toggle-grid" }, enabled.element),
    ),
    openAISection,
    anthropicSection,
    node("section", { class: "settings-subsection" },
      node("h4", { text: "Action auto-review" }),
      node("div", { class: "settings-toggle-grid" }, actionAutoReviewEnabled.element),
      node("div", { class: "settings-field-grid" }, actionAutoReviewModel.element, actionAutoReviewReasoning.element),
      actionAutoReviewReferences.element,
      node("p", { class: "muted", text: "When enabled, NoobBoard asks the selected reviewer model to check the proposed fix against these local reference docs before any approved restart runs." }),
    ),
    renderLLMAgentReadiness(settings.agent_readiness || {}, {
      controls: [agentControlEnabled.element, agentAutoRepairEnabled.element, agentArmDuration.element],
    }),
    node("section", { class: "settings-subsection" },
      node("h4", { text: "Who can ask" }),
      node("div", { class: "settings-policy-list" }, policyEditors.map((editor) => editor.element)),
    ),
    node("details", { class: "settings-advanced" },
      node("summary", { text: "Advanced request timing" }),
      node("div", { class: "settings-field-grid" }, timeout.element),
    ),
  );
  syncLLMVisibility();
  return settingsCard(item, body, (status) => {
    const payload = {
      enabled: enabled.input.checked,
      provider: provider.input.value,
      openai_auth_method: authChoices.find((choice) => choice.input.checked)?.input.value || "api_key",
      openai_model: (authChoices.find((choice) => choice.input.checked)?.input.value || "api_key") === "api_key" ? openAIModel.input.value : chatGPTModel.input.value,
      anthropic_model: anthropicModel.input.value.trim(),
      timeout: secondsToDuration(timeout.input.value),
      agent_control_enabled: agentControlEnabled.input.checked,
      agent_auto_repair_enabled: agentAutoRepairEnabled.input.checked,
      agent_arm_duration: secondsToDuration(agentArmDuration.input.value),
      action_auto_review_enabled: actionAutoReviewEnabled.input.checked,
      action_auto_review_model: actionAutoReviewModel.input.value,
      action_auto_review_reasoning: actionAutoReviewReasoning.input.value,
      action_auto_review_reference_paths: actionAutoReviewReferences.values(),
      clear_openai_api_key: clearOpenAI.input.checked,
      clear_chatgpt_auth: clearChatGPT.input.checked,
      clear_anthropic_api_key: clearAnthropic.input.checked,
      policies: {},
    };
    if (openAIKey.input.value.trim()) payload.openai_api_key = openAIKey.input.value.trim();
    if (anthropicKey.input.value.trim()) payload.anthropic_api_key = anthropicKey.input.value.trim();
    for (const editor of policyEditors) payload.policies[editor.name] = editor.value();
    return saveSettingsPayload(item.title, item.path, payload, status);
  });
}

function actionReviewModelOptions(settings) {
  const openAIModel = knownModelValue(OPENAI_MODEL_OPTIONS, settings.openai_model, "gpt-5.5");
  const chatGPTModel = knownModelValue(CHATGPT_CODEX_MODEL_OPTIONS, settings.openai_model, "gpt-5.3-codex");
  const anthropicModel = knownModelValue(ANTHROPIC_MODEL_OPTIONS, settings.anthropic_model, "claude-sonnet-4-5");
  const options = [
    { value: "same", label: "Same as diagnosis" },
    { value: `openai/${openAIModel}`, label: `OpenAI: ${openAIModel}` },
    { value: `chatgpt/${chatGPTModel}`, label: `ChatGPT connector: ${chatGPTModel}` },
    { value: `anthropic/${anthropicModel}`, label: `Anthropic: ${anthropicModel}` },
  ];
  const current = String(settings.action_auto_review_model || "same").trim() || "same";
  if (isKnownActionReviewModel(current) && !options.some((option) => option.value === current)) options.push({ value: current, label: current });
  return options;
}

function knownModelValue(options, current, fallback) {
  const value = String(current || "").trim();
  return options.some((option) => option.value === value) ? value : fallback;
}

function isKnownActionReviewModel(value) {
  if (value === "same") return true;
  const [provider, model] = String(value || "").split("/", 2);
  if (!provider || !model) return false;
  const options = provider === "openai"
    ? OPENAI_MODEL_OPTIONS
    : provider === "chatgpt"
      ? CHATGPT_CODEX_MODEL_OPTIONS
      : provider === "anthropic"
        ? ANTHROPIC_MODEL_OPTIONS
        : [];
  return options.some((option) => option.value === model);
}

function renderLLMAgentReadiness(readiness, options = {}) {
  const modes = readiness.review_modes || [];
  const tools = readiness.read_only_tools || [];
  const activeText = readiness.admin_tools_enabled
    ? `On, up to ${readiness.admin_tool_call_limit || 0} calls`
    : "Off";
  const readOnlyNames = tools.map((tool) => tool.label).filter(Boolean).join(", ");
  const autoReview = modes.find((mode) => mode.id === "auto_review") || {};
  const autoAction = modes.find((mode) => mode.id === "auto_action") || {};
  const reference = readiness.opencode_auto_review || {};
  const controlEnabled = !!readiness.agent_control_enabled;
  const armed = !!readiness.agent_armed;
  const armSeconds = durationToSeconds(readiness.agent_arm_duration) || 600;
  const armAction = node("button", {
    type: "button",
    class: `command ${armed ? "ghost" : "primary"}`,
    "data-glyph": armed ? "x" : "v",
    disabled: !controlEnabled,
    onclick: () => setAgentArm(!armed, armSeconds, armAction),
    text: armed ? "Disarm" : "Arm",
  });
  return node("section", { class: "settings-subsection agent-readiness" },
    node("div", { class: "settings-section-title-row" },
      node("h4", { text: "Agent approval" }),
      settingsStatePill(readiness.mutating_tools_available ? "available" : "locked", readiness.mutating_tools_available ? "Actions available" : "Actions locked"),
    ),
    options.controls?.length ? node("div", { class: "settings-field-grid" }, options.controls) : null,
    node("div", { class: "settings-status-list" },
      settingsStatusRow("Read-only live tools", activeText, readiness.admin_tools_enabled ? "available" : "locked", readOnlyNames || "No read-only tools are registered."),
      settingsStatusRow("Action arm", agentArmStatusText(readiness), armed ? "armed" : controlEnabled ? "planned" : "locked", agentArmDetailText(readiness), armAction),
      settingsStatusRow("Automatic fixes", readiness.mutating_tools_available ? "Restart approval available" : "Locked", readiness.mutating_tools_available ? "available" : "locked", readiness.mutating_tools_available ? agentRepairLimitDetail(readiness) : "Chat cannot start, stop, restart, or change infrastructure yet."),
      settingsStatusRow("Auto-review", autoReview.enabled ? "Available" : agentModeStatusText(autoReview.status), autoReview.status || "locked", autoReviewDetail(reference)),
      settingsStatusRow("Auto action", agentModeStatusText(autoAction.status), autoAction.status || "locked", autoActionDetail(autoAction, readiness)),
    ),
    node("p", { class: "muted agent-reference-note", text: reference.design_finding || "Future repair actions require schema validation, audit policy, and explicit approval." }),
  );
}

function autoActionDetail(autoAction, readiness) {
  if (!autoAction?.enabled) return "Off. Diagnosis will propose a fix and use the approval popup.";
  if (!readiness?.agent_control_enabled) return "Enable the action approval gate first.";
  const status = String(autoAction.status || "").toLowerCase();
  if (status === "review_required") return "Requires action auto-review so a separate model can veto the restart.";
  if (status === "armed") return "Armed for this session. Only non-online opted-in apps can be restarted automatically.";
  return "Arm this admin session before diagnosis can run an autonomous restart.";
}

function autoReviewDetail(reference) {
  if (!reference?.enabled) return "Optional reviewer gate is off; admin approval still uses the normal popup.";
  const model = reference.model || "same";
  const count = Number(reference.reference_count || 0);
  const refText = count === 1 ? "1 reference" : `${count} references`;
  const reasoning = reference.reasoning ? `, ${reference.reasoning} reasoning` : "";
  return `Reviewer: ${model}${reasoning}; ${refText} configured. Fails closed before any approved restart runs.`;
}

function agentRepairLimitDetail(readiness) {
  const cooldownSeconds = durationToSeconds(readiness.repair_cooldown) || 600;
  const windowSeconds = durationToSeconds(readiness.repair_rate_limit_window) || 3600;
  const max = Number(readiness.repair_rate_limit_max || 5);
  return `Only opted-in apps can be restarted. Limit: 1 per app every ${formatSeconds(cooldownSeconds)}, ${max} total per ${formatSeconds(windowSeconds)}.`;
}

function agentArmStatusText(readiness) {
  if (readiness.agent_armed) return "Armed";
  return readiness.agent_control_enabled ? "Disarmed" : "Off";
}

function agentArmDetailText(readiness) {
  if (!readiness.agent_control_enabled) return "Enable the action approval gate and save before this session can be armed.";
  if (readiness.agent_armed && readiness.agent_armed_until) return `This admin session is armed until ${timeOnly(readiness.agent_armed_until)}.`;
  return "Arm only when you are ready to approve a specific automatic fix.";
}

function timeOnly(value) {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "the configured expiry";
  return date.toLocaleTimeString([], { hour: "numeric", minute: "2-digit" });
}

function formatSeconds(seconds) {
  const value = Number(seconds || 0);
  if (!Number.isFinite(value) || value <= 0) return "configured window";
  if (value < 60) return `${Math.round(value)}s`;
  const minutes = value / 60;
  if (minutes < 60) return `${Math.round(minutes)}m`;
  const hours = minutes / 60;
  return `${Math.round(hours)}h`;
}

async function setAgentArm(armed, durationSeconds, button) {
  const originalText = button?.textContent || "";
  if (button) {
    button.disabled = true;
    button.textContent = armed ? "Arming" : "Disarming";
  }
  try {
    await api("/api/admin/agent/arm", {
      method: "POST",
      body: JSON.stringify({ armed: !!armed, duration_seconds: durationSeconds || 0 }),
    });
    showNotice(armed ? "Agent approval armed for this admin session." : "Agent approval disarmed.");
    await loadSettings();
  } catch (error) {
    showNotice(error.message, "error");
  } finally {
    if (button?.isConnected) {
      button.disabled = false;
      button.textContent = originalText;
    }
  }
}

function settingsStatusRow(label, value, status, detail, action = null) {
  return node("div", { class: "settings-status-row" },
    node("span", { class: "settings-status-main" },
      node("strong", { text: label }),
      detail ? node("small", { text: detail }) : null,
    ),
    action ? node("span", { class: "settings-status-side" }, settingsStatePill(status, value), action) : settingsStatePill(status, value),
  );
}

function settingsStatePill(status, text) {
  return node("span", { class: `settings-state-pill ${settingsStateClass(status)}`, text });
}

function settingsStateClass(status) {
  switch (String(status || "").toLowerCase()) {
    case "available":
      return "state-ok";
    case "planned":
    case "armed":
    case "review_required":
      return "state-warn";
    case "blocked":
    case "locked":
      return "state-muted";
    default:
      return "state-muted";
  }
}

function agentModeStatusText(status) {
  switch (String(status || "").toLowerCase()) {
    case "available":
      return "Available";
    case "planned":
      return "Planned";
    case "armed":
      return "Armed";
    case "review_required":
      return "Needs review";
    case "blocked":
      return "Locked";
    default:
      return "Off";
  }
}

function settingChoice(name, value, title, description, checked, action = null) {
  const input = node("input", { type: "radio", name, value, checked: !!checked });
  const element = node("label", { class: `settings-choice${checked ? " selected" : ""}` },
    input,
    node("span", { class: "settings-choice-copy" },
      node("strong", { text: title }),
      node("small", { text: description }),
    ),
    action,
  );
  return { input, element };
}

async function connectOpenAIChatGPTBrowser(message, button, headlessMessage = null, headlessButton = null) {
  button.disabled = true;
  message.hidden = false;
  message.textContent = "Opening OpenAI login...";
  const dialog = openOpenAIAuthDialog();
  renderOpenAIAuthWorking(dialog, "Opening OpenAI browser login...");
  try {
    const data = await api("/api/admin/settings/llm/openai/browser/start", { method: "POST", body: "{}" });
    const popup = window.open(data.auth_url, "_blank");
    if (!popup) {
      const fallbackMessage = "OpenAI login was blocked by the browser. Use this code login instead.";
      message.textContent = fallbackMessage;
      if (headlessMessage && headlessButton) {
        headlessMessage.hidden = false;
        await connectOpenAIChatGPTHeadless(headlessMessage, headlessButton, { dialog, note: fallbackMessage });
        return;
      }
      renderOpenAIAuthError(dialog, fallbackMessage);
      return;
    }
    popup.opener = null;
    message.textContent = "Finish OpenAI login in the new window. This page will update when it connects.";
    renderOpenAIAuthWorking(dialog, "Finish OpenAI login in the new browser window.");
    await waitForChatGPTConnection(data.poll_id, message, dialog);
  } catch (error) {
    if (error.status === 409 && error.data?.fallback === "chatgpt_headless" && headlessMessage && headlessButton) {
      const fallbackMessage = "Browser login only works from localhost on the NoobBoard host. Use this code login from a LAN device.";
      message.textContent = fallbackMessage;
      headlessMessage.hidden = false;
      await connectOpenAIChatGPTHeadless(headlessMessage, headlessButton, { dialog, note: fallbackMessage });
      return;
    }
    message.textContent = error.message;
    renderOpenAIAuthError(dialog, error.message);
    showNotice(error.message, "error");
  } finally {
    button.disabled = false;
  }
}

async function connectOpenAIChatGPTHeadless(message, button, options = {}) {
  const dialog = options.dialog || openOpenAIAuthDialog();
  if (button) button.disabled = true;
  message.hidden = false;
  message.textContent = "Starting OpenAI code login...";
  renderOpenAIAuthWorking(dialog, "Requesting an OpenAI device code...");
  try {
    const data = await api("/api/admin/settings/llm/openai/headless/start", { method: "POST", body: "{}" });
    renderOpenAIAuthCode(dialog, data, options.note || "");
    message.replaceChildren(
      node("span", { text: "Use code " }),
      node("strong", { text: data.user_code }),
      node("span", { text: " at " }),
      node("a", { href: data.verification_url, target: "_blank", rel: "noreferrer", text: data.verification_url }),
    );
    const popup = window.open(data.verification_url, "_blank");
    if (popup) popup.opener = null;
    await pollOpenAIChatGPTHeadless(data.poll_id, data.interval_seconds || 5, message, dialog);
  } catch (error) {
    message.textContent = error.message;
    renderOpenAIAuthError(dialog, error.message);
    showNotice(error.message, "error");
  } finally {
    if (button) button.disabled = false;
  }
}

async function waitForChatGPTConnection(pollID, message, dialog = null) {
  for (let attempt = 0; attempt < 90; attempt++) {
    if (dialog?.session.cancelled) return;
    await delay(2000);
    if (dialog?.session.cancelled) return;
    const data = await api("/api/admin/settings/llm/openai/browser/finish", {
      method: "POST",
      body: JSON.stringify({ poll_id: pollID }),
    });
    if (data.status === "connected") {
      message.textContent = "OpenAI is connected.";
      renderOpenAIAuthSuccess(dialog);
      showNotice("OpenAI connected.");
      await loadSettings();
      return;
    }
  }
  message.textContent = "Still waiting for OpenAI login. Save is not required after the login window finishes.";
  setOpenAIAuthDialogStatus(dialog, "Still waiting for OpenAI login.", "info");
}

async function pollOpenAIChatGPTHeadless(pollID, intervalSeconds, message, dialog = null) {
  for (let attempt = 0; attempt < 120; attempt++) {
    if (dialog?.session.cancelled) return;
    await delay(Math.max(1, Number(intervalSeconds) || 5) * 1000);
    if (dialog?.session.cancelled) return;
    const data = await api("/api/admin/settings/llm/openai/headless/poll", {
      method: "POST",
      body: JSON.stringify({ poll_id: pollID }),
    });
    if (data.status === "connected") {
      message.textContent = "OpenAI is connected.";
      renderOpenAIAuthSuccess(dialog);
      showNotice("OpenAI connected.");
      await loadSettings();
      return;
    }
    if (data.interval_seconds) intervalSeconds = data.interval_seconds;
  }
  message.textContent = "The OpenAI login code timed out. Start again to get a new code.";
  setOpenAIAuthDialogStatus(dialog, "The OpenAI code timed out.", "bad");
}

function delay(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
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
  const nasClientHint = settingTextField("NAS client hint", settings.unifi_nas_client_hint || "", { placeholder: "IP, hostname, MAC, or UniFi client name" });
  const expectedNASLink = settingNumberField("Expected NAS link Mbps", settings.expected_nas_link_mbps || 0, { min: 0, max: 100000, step: 1, inputmode: "numeric" });
  const internetProbe = settingTextField("Internet probe URL", settings.internet_probe_url || "", { inputmode: "url" });
  const dnsProbe = settingTextField("DNS probe host", settings.dns_probe_host || "");
  const routerProbe = settingTextField("Router probe target", settings.router_probe_target || "", { inputmode: "url" });
  const nasProbe = settingTextField("NAS probe target", settings.nas_probe_target || "", { inputmode: "url" });
  const body = node("div", { class: "settings-form" },
    node("section", { class: "settings-subsection" },
      node("h4", { text: "Mode" }),
      node("p", { class: "muted", text: "Choose whether status comes from live integrations, fixtures, or a mixed setup." }),
      node("div", { class: "settings-field-grid" }, mode.element),
    ),
    node("section", { class: "settings-subsection" },
      node("h4", { text: "Unraid API" }),
      node("p", { class: "muted", text: "Primary NAS connection used for app, storage, and server status." }),
      node("div", { class: "settings-field-grid" }, unraidURL.element, keyFieldWithState(unraidKey, settings.unraid_api_key_set), unraidKeyFile.element),
      node("div", { class: "settings-toggle-grid" }, clearUnraidKey.element),
    ),
    node("section", { class: "settings-subsection" },
      node("h4", { text: "Unraid SSH fallback" }),
      node("p", { class: "muted", text: "Optional fallback when the Unraid API is reachable but cannot provide enough detail." }),
      node("div", { class: "settings-toggle-grid" }, sshFallback.element),
      node("div", { class: "settings-field-grid" }, sshHost.element, sshPort.element, sshUser.element, sshKeyFile.element, sshCommand.element),
    ),
    node("section", { class: "settings-subsection" },
      node("h4", { text: "UniFi API" }),
      node("p", { class: "muted", text: "Router and network status source." }),
      node("div", { class: "settings-field-grid" }, unifiURL.element, keyFieldWithState(unifiKey, settings.unifi_api_key_set), unifiKeyFile.element, unifiSite.element),
      node("div", { class: "settings-toggle-grid" }, clearUniFiKey.element, unifiTLS.element),
    ),
    node("section", { class: "settings-subsection" },
      node("h4", { text: "NAS network matching" }),
      node("p", { class: "muted", text: "Hints used to match the NAS in UniFi client data and validate link speed." }),
      node("div", { class: "settings-field-grid" }, nasClientHint.element, expectedNASLink.element),
    ),
    node("section", { class: "settings-subsection" },
      node("h4", { text: "Health probes" }),
      node("p", { class: "muted", text: "Lightweight checks for internet, DNS, router, and NAS reachability." }),
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
      unifi_nas_client_hint: nasClientHint.input.value.trim(),
      expected_nas_link_mbps: parseInt(expectedNASLink.input.value, 10) || 0,
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
  const agentTools = settingToggle("Read-only live tools", !!current.agent_tools_enabled);
  const agentMaxCalls = settingNumberField("Max tool calls", current.agent_max_tool_calls || 0, { min: 0, step: 1, inputmode: "numeric" });
  const canUseTools = (current.recipient_role || meta.recipient) === "admin";
  agentTools.input.disabled = !canUseTools;
  agentMaxCalls.input.disabled = !canUseTools;
  const logSources = listEditor("Allowed log sources", current.allowed_log_sources || [], "source");
  return {
    name,
    element: node("article", { class: "settings-policy-card" },
      node("div", { class: "settings-policy-main" },
        node("span", { class: "settings-policy-copy" },
          node("strong", { text: meta.title }),
          node("small", { text: meta.description }),
        ),
        enabled.element,
      ),
      node("details", { class: "settings-policy-advanced" },
        node("summary", { text: "Advanced context and redaction" }),
        node("p", { class: "muted", text: `Recipient: ${current.recipient_role || meta.recipient}` }),
        node("div", { class: "settings-toggle-grid" }, includeLogs.element, preferFacts.element, allowHidden.element, allowBlacklisted.element, failClosed.element),
        node("div", { class: "settings-field-grid" }, maxContext.element, maxLogs.element),
        logSources.element,
        node("section", { class: "settings-subsection" },
          node("h4", { text: "Read-only live tools" }),
          node("p", { class: "muted", text: "Admin diagnosis can refresh sanitized live status. These tools cannot start, stop, restart, repair, or change settings." }),
          node("div", { class: "settings-toggle-grid" }, agentTools.element),
          node("div", { class: "settings-field-grid" }, agentMaxCalls.element),
        ),
      ),
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
      agent_tools_enabled: canUseTools && agentTools.input.checked,
      agent_max_tool_calls: parseInt(agentMaxCalls.input.value, 10) || current.agent_max_tool_calls || 0,
      agent_tool_rules: current.agent_tool_rules || [],
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
$("diagnostic-question").addEventListener("keydown", (event) => submitOnEnter(event, diagnose));
$("notify-admin").addEventListener("click", () => notifyAdmin());
$("user-status-open").addEventListener("click", () => setCompactView("status"));
$("user-chat-open").addEventListener("click", focusUserChat);
$("user-chat-send").addEventListener("click", runUserChat);
$("user-chat-input").addEventListener("keydown", (event) => submitOnEnter(event, runUserChat));
$("user-notify-admin").addEventListener("click", () => notifyAdmin("A standard user reported a problem."));
$("user-app-detail-back").addEventListener("click", closeCompactDetail);
$("user-infra-detail-back").addEventListener("click", closeCompactDetail);
$("audit-refresh").addEventListener("click", loadAudit);
$("repair-requests-refresh").addEventListener("click", loadRepairRequests);
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
$("assistant-input").addEventListener("keydown", (event) => submitOnEnter(event, runAssistantChat));
$("assistant-notify").addEventListener("click", () => notifyAdmin($("assistant-input").value || "A standard user reported a problem."));
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
