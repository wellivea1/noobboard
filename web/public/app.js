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
  roleDirty: false,
  selectedRole: "",
  selectedUser: "",
  auditEntries: [],
  activityFilter: "all",
  activitySearch: "",
  settingsSearch: "",
  repairRequests: [],
  userRepairRequests: [],
  notificationPreferences: new Map(),
  notificationPreferencesLoaded: false,
  notificationPreferencesLoading: false,
  compactNotificationDialog: null,
  notificationPollTimer: null,
  notificationLastSeenTime: "",
  notificationRecordsLoaded: false,
  userDrawerActiveSection: "settings",
  userDrawerLastFocus: null,
  userView: "status",
  userDetailID: "",
  userDetailSubject: "",
  userDetailReturnFocus: null,
  openAIAuthDialog: null,
  agentApprovalDialog: null,
  agentApprovalCooldownTimer: null,
  chatBusy: {
    diagnostic: false,
    user: false,
    assistant: false,
  },
};

const $ = (id) => document.getElementById(id);
const MONITOR_STORAGE_KEY = "noobboard.hiddenMonitors.v1";
const OVERVIEW_ORDER_STORAGE_KEY = "noobboard.overviewOrder.v1";
const NOTIFICATION_SIGNUP_STORAGE_KEY = "noobboard.notificationSignup.v1";
const NOTIFICATION_LAST_SEEN_STORAGE_KEY = "noobboard.notificationLastSeen.v1";
const NOTIFICATION_POLL_INTERVAL_MS = 30000;
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
/* Model pickers.
   Checked against the providers' own catalogues on 2026-08-01:
   developers.openai.com/api/docs/models + /deprecations, and the Anthropic
   models overview. Two rules for this list:
     1. Never carry a retired ID — it 404s, and the admin has no way to know
        the model is gone rather than the key being wrong.
     2. Keep it short. This is an operator picking a model for one structured
        JSON call, not a catalogue. Superseded generations stay only while the
        provider still serves them, so a saved value keeps working. */
const OPENAI_MODEL_OPTIONS = [
  { value: "gpt-5.6-terra", label: "GPT-5.6 Terra (recommended)" },
  { value: "gpt-5.6-sol", label: "GPT-5.6 Sol" },
  { value: "gpt-5.6-luna", label: "GPT-5.6 Luna" },
  { value: "gpt-5.5", label: "GPT-5.5" },
  { value: "gpt-5.5-pro", label: "GPT-5.5 pro" },
  { value: "gpt-5.4", label: "GPT-5.4" },
  { value: "gpt-5.4-mini", label: "GPT-5.4 mini" },
];
const CHATGPT_MODEL_OPTIONS = OPENAI_MODEL_OPTIONS.filter((option) => option.value.startsWith("gpt-"));
const ANTHROPIC_MODEL_OPTIONS = [
  { value: "claude-opus-5", label: "Claude Opus 5 (recommended)" },
  { value: "claude-sonnet-5", label: "Claude Sonnet 5" },
  { value: "claude-opus-4-8", label: "Claude Opus 4.8" },
  { value: "claude-sonnet-4-6", label: "Claude Sonnet 4.6" },
  { value: "claude-haiku-4-5", label: "Claude Haiku 4.5" },
  { value: "claude-sonnet-4-5", label: "Claude Sonnet 4.5" },
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
    const top = Math.max(0, Math.round(viewport?.offsetTop || 0));
    const layoutHeight = Math.round(window.innerHeight || height || 0);
    const keyboardInset = Math.max(0, layoutHeight - height - top);
    const keyboardOpen = keyboardInset > 80 || (layoutHeight > 0 && height > 0 && height < layoutHeight * 0.78);
    if (height > 0) root.style.setProperty("--visual-viewport-height", `${height}px`);
    root.style.setProperty("--visual-viewport-top", `${top}px`);
    root.style.setProperty("--keyboard-inset-bottom", `${keyboardInset}px`);
    root.classList.toggle("is-keyboard-open", keyboardOpen);
    scheduleCompactChatIntoView();
  };
  setVisualViewportHeight();
  window.addEventListener("resize", setVisualViewportHeight, { passive: true });
  window.visualViewport?.addEventListener("resize", setVisualViewportHeight, { passive: true });
  window.visualViewport?.addEventListener("scroll", setVisualViewportHeight, { passive: true });

  if (window.matchMedia?.("(pointer: coarse)").matches) {
    document.addEventListener("focusin", (event) => {
      const target = event.target;
      if (!(target instanceof HTMLInputElement || target instanceof HTMLTextAreaElement)) return;
      if (target.closest(".panel.user-chat")) {
        scheduleCompactChatIntoView();
        return;
      }
      setTimeout(() => {
        target.scrollIntoView({ block: "center", inline: "nearest", behavior: "smooth" });
      }, 250);
    });
  }
}

function scheduleCompactChatIntoView() {
  if (!document.body.classList.contains("compact-view") || document.body.dataset.compactView !== "chat") return;
  clearTimeout(scheduleCompactChatIntoView.timer);
  scheduleCompactChatIntoView.timer = setTimeout(() => {
    const panel = document.querySelector(".panel.user-chat");
    const input = $("user-chat-input");
    if (!panel || panel.hidden || !input) return;
    const active = document.activeElement;
    if (active !== input && !panel.contains(active)) return;
    const row = panel.querySelector(".button-row") || input;
    row.scrollIntoView({ block: "end", inline: "nearest", behavior: "smooth" });
  }, 90);
}

function node(tag, attrs = {}, ...children) {
  const element = document.createElement(tag);
  for (const [key, value] of Object.entries(attrs)) {
    if (key === "class") element.className = value;
    else if (key === "text") element.textContent = value === null || value === undefined ? "" : value;
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

function compactSurfaceErrorMessage(error, fallback = "That could not be completed right now.") {
  console.warn("Compact surface request failed", error);
  const status = Number(error?.status || 0);
  if (status === 404) return "That item is not available anymore. Check again to refresh the page.";
  if (status === 403) return "You do not have access to do that.";
  if (status === 409) return "The app status changed. Check again and try once more.";
  if (status === 429) return "Too many requests were made. Wait a moment and try again.";
  if (/deadline|timeout|timed out/i.test(String(error?.message || ""))) {
    return "The server took too long to respond. Check its status and try again.";
  }
  return fallback;
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
        remember_me: form.get("remember_me") === "on",
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
  if (state.activeTab === "settings" && tabName !== "settings" && settingsHasUnsavedChanges() && !confirm("Discard unsaved settings changes?")) {
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
    overview: "Overview",
    activity: "Activity",
    server: "Server health",
    router: "Router and UniFi",
    apps: "Apps",
    diagnostics: "Diagnostics",
    queue: "Review queue",
    settings: "Settings",
  };
  $("page-title").textContent = titles[tabName] || "Overview";
  renderPageSubtitle();
  closeNav();
  if (tabName === "queue") {
    loadRepairRequests();
  }
  if (tabName === "activity") {
    loadAudit();
    loadRepairRequests();
  }
  // Re-entering the tab must not silently rebuild the forms over unsaved edits.
  if (tabName === "settings" && !settingsHasUnsavedChanges()) loadSettings();
}

function setCompactView(view) {
  if (hasAdminSurface()) return;
  const nextView = ["chat", "app-detail", "infra-detail"].includes(view) ? view : "status";
  state.userView = nextView;
  document.body.dataset.compactView = state.userView;
  const titles = {
    status: "Home status",
    chat: "Ask what's wrong",
    "app-detail": "Details",
    "infra-detail": "Details",
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
    scheduleCompactChatIntoView();
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
    if (!hasAdminSurface() && state.notificationRecordsLoaded) {
      await loadUserNotificationRecords({ notify: true });
    }
  } catch (error) {
    showNotice(hasAdminSurface() ? error.message : compactSurfaceErrorMessage(error, "NoobBoard could not refresh right now. Try again shortly."), "error");
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
  // Not rendered on screen: this is the accessible description of the page, which
  // is the only place a restatement of the page title belongs.
  const copy = {
    overview: snapshot.server_summary || "Current status, incidents, and diagnostic facts.",
    activity: "Incidents, repair decisions, and configuration changes in one stream.",
    server: "Storage, service, and collector health for the server.",
    router: "Internet, DNS, router, and UniFi health.",
    apps: "Search visible apps, review metadata, and use admin-only controls.",
    diagnostics: "Ask the configured diagnosis provider for a structured explanation.",
    queue: "Review standard-user repair requests that need an admin decision.",
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
  syncAutoRepairControls(snapshot);
  syncAssistant(snapshot);
  if (!hasAdminSurface()) {
    renderUserHome(snapshot);
    return;
  }
  renderVerdict(snapshot);
  renderOverviewCards(snapshot);
  renderServerHealth(snapshot);
  renderRouterStatus(snapshot);
  renderFacts(snapshot.facts || []);
  renderIncidentStrip(snapshot.incidents || []);
  renderActivity();
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
  syncAutoRepairControls(snapshot);
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
  if ($("user-ask-primary")) {
    $("user-ask-primary").hidden = !canChat;
    $("user-ask-primary").disabled = !canChat;
    $("user-ask-primary").setAttribute("aria-disabled", String(!canChat));
  }
  $("user-chat-input").placeholder = "Ask what's wrong or whether an app is working.";
  $("user-chat-input").disabled = !canChat || state.chatBusy.user;
  $("user-chat-send").disabled = !canChat || state.chatBusy.user;
  syncAutoRepairControls(snapshot);
  if (!canChat) {
    if (!$("user-chat-input").value.trim()) $("user-chat-input").value = "What is wrong right now?";
    setChatNotice($("user-chat-output"), "Status chat is not available.");
  } else if ($("user-chat-output").classList.contains("chat-unavailable")) {
    if (!$("user-chat-input").value.trim()) $("user-chat-input").value = "What is wrong right now?";
    resetChatPlaceholder($("user-chat-output"), "Ask what's wrong or whether an app is working.");
  }
  if (state.userView === "chat" && !canChat) state.userView = "status";
  setCompactView(state.userView || "status");
  startCompactNotificationPolling();
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

function syncAutoRepairControls(snapshot = state.snapshot) {
  const info = snapshot?.repair_automation || {};
  const available = diagnosticsAvailable(snapshot || {});
  const reason = info.reason || "Auto-fix is disabled in admin settings.";
  const adminReady = available && !!info.admin_auto_repair_available;
  const userReady = available && canUseCompactChat(snapshot || {}) && !!info.user_auto_repair_available;
  syncAutoRepairControl("diagnostic-auto-repair", adminReady, reason, "diagnostic");
  syncAutoRepairControl("assistant-auto-repair", hasAdminSurface() ? adminReady : userReady, reason, "assistant");
  syncAutoRepairControl("user-chat-auto-repair", userReady, reason, "user");
}

function syncAutoRepairControl(id, available, reason, busyKey) {
  const input = $(id);
  if (!input) return;
  const wrap = $(`${id}-wrap`) || input.closest(".chat-auto-repair");
  const busy = !!(busyKey && state.chatBusy[busyKey]);
  const disabled = !available || busy;
  input.disabled = disabled;
  input.setAttribute("aria-disabled", String(disabled));
  if (!available) input.checked = false;
  const title = available
    ? "Allow NoobBoard to run one eligible, reviewed app fix for this diagnosis."
    : reason;
  input.title = title;
  if (wrap) {
    wrap.classList.toggle("is-disabled", disabled);
    wrap.title = title;
  }
}

function autoRepairRequested(input) {
  return !!(input && !input.disabled && input.checked);
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
  input.addEventListener("change", () => handleCompactNotificationPreferenceChange(app, input, row, stateText));
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
    showNotice(compactSurfaceErrorMessage(error, "Notification settings could not be loaded. Try again."), "error");
  } finally {
    state.notificationPreferencesLoading = false;
    if (isUserMenuOpen()) renderUserDrawer();
  }
}

function handleCompactNotificationPreferenceChange(app, input, row, stateText) {
  const enabled = input.checked;
  if (enabled && !compactNotificationSignupComplete()) {
    input.checked = false;
    setCompactSwitchState(input, stateText, false);
    openCompactNotificationSignup({ app, input, row, stateText });
    return;
  }
  persistCompactNotificationPreference(app, input, row, stateText, enabled);
}

async function persistCompactNotificationPreference(app, input, row, stateText, enabled) {
  const appID = String(app.app_id || "").trim();
  if (!appID) return;
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
    return true;
  } catch {
    input.checked = previous;
    setCompactSwitchState(input, stateText, previous);
    if (error) error.textContent = "Couldn't save that \u2014 try again.";
    return false;
  } finally {
    input.disabled = false;
    delete row.dataset.saving;
  }
}

function setCompactSwitchState(input, stateText, enabled) {
  input.checked = enabled;
  if (stateText) stateText.textContent = enabled ? "On" : "Off";
}

function compactNotificationStorageSuffix() {
  const raw = state.user?.id || state.user?.username || "user";
  return encodeURIComponent(String(raw));
}

function compactNotificationSignupStorageKey() {
  return `${NOTIFICATION_SIGNUP_STORAGE_KEY}.${compactNotificationStorageSuffix()}`;
}

function compactNotificationLastSeenStorageKey() {
  return `${NOTIFICATION_LAST_SEEN_STORAGE_KEY}.${compactNotificationStorageSuffix()}`;
}

function compactNotificationSignupMode() {
  try {
    return localStorage.getItem(compactNotificationSignupStorageKey()) || "";
  } catch {
    return "";
  }
}

function setCompactNotificationSignupMode(mode) {
  try {
    localStorage.setItem(compactNotificationSignupStorageKey(), mode);
  } catch {
    // Local storage can be blocked; the permission still applies for the current browser session.
  }
}

function compactNotificationSignupComplete() {
  if (compactNotificationSignupMode()) return true;
  if (systemNotificationPermission() === "granted") {
    setCompactNotificationSignupMode("system");
    return true;
  }
  return false;
}

function systemNotificationPermission() {
  if (!("Notification" in window)) return "unsupported";
  return Notification.permission || "default";
}

function compactNotificationSignupBody() {
  const permission = systemNotificationPermission();
  if (permission === "denied") {
    return "Device alerts are blocked for this page. NoobBoard can still show alerts here while it is open.";
  }
  if (permission === "unsupported" || window.isSecureContext === false) {
    return "NoobBoard can show alerts here while this page is open. Device alerts need a supported page address.";
  }
  return "NoobBoard can show alerts on this device when a selected app stops working or starts working again.";
}

function openCompactNotificationSignup(pending) {
  closeCompactNotificationSignup({ returnFocus: false });
  const previousFocus = document.activeElement instanceof HTMLElement ? document.activeElement : pending.input;
  const titleID = `compact-notification-title-${Date.now()}`;
  const status = node("p", { class: "compact-notification-signup-status", role: "status", text: "You can change this later in Settings." });
  const primary = node("button", { type: "button", class: "command primary", text: "Turn on alerts" });
  const cancel = node("button", { type: "button", class: "command", text: "Not now" });
  const dialog = node("section", {
    class: "compact-notification-signup",
    role: "dialog",
    "aria-modal": "true",
    "aria-labelledby": titleID,
  },
    node("div", { class: "compact-notification-signup-head" },
      node("h2", { id: titleID, text: "Turn on alerts?" }),
      node("p", { text: compactNotificationSignupBody() }),
    ),
    status,
    node("div", { class: "compact-notification-signup-actions" }, cancel, primary),
  );
  const backdrop = node("div", { class: "compact-notification-signup-backdrop" }, dialog);
  backdrop.addEventListener("click", (event) => {
    if (event.target === backdrop) closeCompactNotificationSignup();
  });
  backdrop.addEventListener("keydown", handleCompactNotificationSignupKeydown);
  cancel.addEventListener("click", () => closeCompactNotificationSignup());
  primary.addEventListener("click", () => confirmCompactNotificationSignup(pending, primary, status));
  document.body.append(backdrop);
  document.body.classList.add("compact-notification-signup-open");
  state.compactNotificationDialog = { backdrop, dialog, previousFocus };
  primary.focus({ preventScroll: true });
}

function closeCompactNotificationSignup(options = {}) {
  const { returnFocus = true } = options;
  const current = state.compactNotificationDialog;
  if (!current) return;
  current.backdrop.remove();
  document.body.classList.remove("compact-notification-signup-open");
  state.compactNotificationDialog = null;
  if (returnFocus) current.previousFocus?.focus?.({ preventScroll: true });
}

function handleCompactNotificationSignupKeydown(event) {
  if (!state.compactNotificationDialog) return;
  if (event.key === "Escape") {
    event.preventDefault();
    event.stopPropagation();
    closeCompactNotificationSignup();
    return;
  }
  if (event.key !== "Tab") return;
  event.stopPropagation();
  const focusable = openAIAuthDialogFocusableElements(state.compactNotificationDialog.dialog);
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

async function requestSystemNotificationPermission() {
  if (!("Notification" in window)) return "unsupported";
  if (Notification.permission === "granted" || Notification.permission === "denied") {
    return Notification.permission;
  }
  if (window.isSecureContext === false) return "unavailable";
  try {
    return await Notification.requestPermission();
  } catch {
    return "unavailable";
  }
}

async function confirmCompactNotificationSignup(pending, primary, status) {
  primary.disabled = true;
  status.textContent = "Turning on alerts...";
  const permission = await requestSystemNotificationPermission();
  const mode = permission === "granted" ? "system" : "in-app";
  setCompactNotificationSignupMode(mode);
  closeCompactNotificationSignup({ returnFocus: false });
  const { app, input, row, stateText } = pending;
  let saved = false;
  if (typeof pending.afterSignup === "function") {
    saved = await pending.afterSignup(mode);
  } else if (input?.isConnected && row?.isConnected) {
    saved = await persistCompactNotificationPreference(app, input, row, stateText, true);
  }
  startCompactNotificationPolling();
  if (saved) showNotice(mode === "system" ? "Alerts turned on." : "Alerts saved. Keep NoobBoard open here to see them.");
}

function startCompactNotificationPolling() {
  if (hasAdminSurface() || !compactNotificationSignupComplete()) return;
  if (!state.notificationLastSeenTime) {
    try {
      state.notificationLastSeenTime = localStorage.getItem(compactNotificationLastSeenStorageKey()) || "";
    } catch {
      state.notificationLastSeenTime = "";
    }
  }
  if (!state.notificationRecordsLoaded) {
    loadUserNotificationRecords({ notify: false });
  }
  if (state.notificationPollTimer) return;
  state.notificationPollTimer = window.setInterval(() => {
    loadUserNotificationRecords({ notify: true });
  }, NOTIFICATION_POLL_INTERVAL_MS);
}

async function loadUserNotificationRecords(options = {}) {
  if (hasAdminSurface()) return;
  try {
    const records = await api("/api/user/notifications?limit=20");
    state.notificationRecordsLoaded = true;
    handleUserNotificationRecords(Array.isArray(records) ? records : [], options);
  } catch {
    // Polling is best-effort; the status view should not become noisy if the session expires.
  }
}

function handleUserNotificationRecords(records, options = {}) {
  const sorted = records
    .map((record) => ({ record, time: Date.parse(record.time || "") }))
    .filter((item) => Number.isFinite(item.time))
    .sort((a, b) => a.time - b.time);
  if (!sorted.length) return;
  const previous = Number(state.notificationLastSeenTime || 0);
  const newest = sorted[sorted.length - 1].time;
  if (!previous) {
    saveCompactNotificationLastSeen(newest);
    return;
  }
  const unseen = sorted.filter((item) => item.time > previous);
  if (options.notify) {
    unseen.forEach((item) => showUserNotificationRecord(item.record));
  }
  if (newest > previous) saveCompactNotificationLastSeen(newest);
}

function saveCompactNotificationLastSeen(value) {
  const normalized = String(value || "");
  state.notificationLastSeenTime = normalized;
  try {
    localStorage.setItem(compactNotificationLastSeenStorageKey(), normalized);
  } catch {
    // Local storage can be unavailable; current-session de-dupe still works.
  }
}

async function showUserNotificationRecord(record) {
  const message = String(record.message || "NoobBoard status changed.");
  if (systemNotificationPermission() === "granted") {
    const tag = String(record.dedupe || record.id || "noobboard-status");
    const options = { body: message, tag, data: { url: "/" } };
    try {
      const registration = await navigator.serviceWorker?.ready;
      if (registration?.showNotification) {
        await registration.showNotification("NoobBoard", options);
        return;
      }
    } catch {
      // Fall back to the page-level Notification constructor.
    }
    try {
      new Notification("NoobBoard", options);
      return;
    } catch {
      // Fall through to the in-app notice.
    }
  }
  showNotice(message);
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
  output.replaceChildren(renderThinkingLoader(message || "Checking status..."));
}

function setChatError(output, message) {
  output.classList.remove("chat-empty", "chat-unavailable", "chat-result", "chat-pending", "muted");
  output.classList.add("chat-error");
  output.textContent = message;
}

function renderThinkingLoader(message) {
  return node("div", { class: "thinking-loader thinking-bubble skeleton-spark-concept", role: "status", "aria-live": "polite" },
    node("div", { class: "skeleton-spark-container", "aria-hidden": "true" },
      node("div", { class: "skeleton-row r1" }),
      node("div", { class: "skeleton-row r2" }),
      node("div", { class: "spark-cursor" }),
    ),
    node("span", { class: "thinking-label", text: message }),
  );
}

function cleanChatText(value) {
  if (value === null || value === undefined) return "";
  const text = String(value).trim();
  if (!text || text.toLowerCase() === "null") return "";
  return text;
}

function cleanCompactChatText(value) {
  let text = cleanChatText(value);
  if (!text) return "";
  const replacements = [
    [/\bWAN\b/gi, "internet"],
    [/\bDocker\b/gi, "app service"],
    [/\bcontainer(s)?\b/gi, "app$1"],
    [/\barray\b/gi, "server storage"],
    [/\bparity\b/gi, "storage protection"],
    [/\bUnraid\b/gi, "the server"],
  ];
  for (const [pattern, replacement] of replacements) text = text.replace(pattern, replacement);
  return text.replace(/\s+/g, " ").trim();
}

function firstChatText(...values) {
  for (const value of values) {
    const text = cleanChatText(value);
    if (text) return text;
  }
  return "";
}

function cleanChatList(values) {
  if (!Array.isArray(values)) return [];
  return values.map(cleanChatText).filter(Boolean);
}

function firstCompactChatText(...values) {
  for (const value of values) {
    const text = cleanCompactChatText(value);
    if (text) return text;
  }
  return "";
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
    if (options.focus) $("user-app-detail-back")?.focus({ preventScroll: true });
  } catch (error) {
    body.replaceChildren(
      node("div", { class: "detail-empty" },
        node("strong", { text: "Could not load details." }),
        node("p", { text: "Go back and try again." }),
      ),
    );
    showNotice(compactSurfaceErrorMessage(error, "Those app details could not be loaded. Check again and retry."), "error");
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
    if (options.focus) $("user-infra-detail-back")?.focus({ preventScroll: true });
  } catch (error) {
    body.replaceChildren(
      node("div", { class: "detail-empty" },
        node("strong", { text: "Could not load details." }),
        node("p", { text: "Go back and try again." }),
      ),
    );
    showNotice(compactSurfaceErrorMessage(error, "Those connection details could not be loaded. Check again and retry."), "error");
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
  if (hasAdminSurface()) return null;
  const canControl = !!app.restart_allowed_general_user && isUserAppControlTarget(app);
  if (!canControl && !isUserRepairCandidate(app)) return null;
  const request = latestUserRepairRequestForApp(app.app_id);
  const pending = request?.status === "pending";
  const showRequestStatus = !!request && (!canControl || request.status !== "pending");
  return node("section", { class: "detail-actions user-repair-actions" },
    canControl ? node("div", { class: "user-app-control-row" }, userAppControlActions(app).map((item) => {
      const disabledReason = userAppControlDisabledReason(item.action, app);
      return node("button", {
        type: "button",
        class: item.primary ? "primary command" : "command",
        "data-glyph": item.glyph,
        disabled: !!disabledReason,
        title: disabledReason || item.title,
        "aria-label": `${item.label} ${appDisplayName(app)}`,
        onclick: (event) => runUserAppAction(app, item.action, event.currentTarget),
        text: item.label,
      });
    })) : node("button", {
      type: "button",
      class: "primary command",
      "data-glyph": "!",
      disabled: pending,
      onclick: (event) => requestAdminRepairForApp(app, event.currentTarget),
      text: pending ? "Asked admin" : "Ask admin",
    }),
    showRequestStatus ? node("span", {
      class: `settings-state-pill user-repair-state ${userRepairRequestTone(request)}`,
      text: userRepairRequestStatusText(request),
    }) : null,
  );
}

function isUserAppControlTarget(app) {
  return !!(app?.data_source === "unraid-docker" || app?.docker_state || app?.container_id || app?.container_name);
}

function isUserRepairCandidate(app) {
  const status = normalizeStatus(app?.current_status);
  return status !== "online" && isUserAppControlTarget(app);
}

function userAppControlActions() {
  return [
    { action: "start", label: "Start", glyph: ">", title: "Start this app", primary: true },
    { action: "restart", label: "Restart", glyph: "r", title: "Restart this app", primary: true },
    { action: "stop", label: "Stop", glyph: "x", title: "Stop this app", primary: false },
  ];
}

function userAppControlDisabledReason(action, app) {
  const status = normalizeStatus(app?.current_status);
  const dockerState = String(app?.docker_state || "").toLowerCase();
  if (action === "start" && (dockerState === "running" || status === "online" || status === "degraded")) return "Already running";
  if (action === "stop" && (dockerState === "exited" || (status === "offline" && dockerState !== "running"))) return "Already stopped";
  return "";
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
  return runUserAppAction(app, "restart", button);
}

async function runUserAppAction(app, action, button = null, options = {}) {
  const appID = app?.app_id || "";
  const label = appDisplayName(app);
  const actionName = String(action || "").trim().toLowerCase();
  const actionLabel = userAppActionLabel(actionName);
  let keepButtonState = false;
  let inlineSuccessOutcome = null;
  const returnToChat = !!options.inlineSuccess && state.userView === "chat";
  if (!appID) {
    showNotice("App id is required.", "error");
    return;
  }
  if (!["start", "stop", "restart"].includes(actionName)) {
    showNotice("App action is not supported.", "error");
    return;
  }
  if (!confirm(`${actionLabel} ${label}?`)) return;
  const original = button?.textContent || "";
  if (button) {
    button.disabled = true;
    button.textContent = userAppActionProgressText(actionName);
  }
  try {
    const result = await api(`/api/user/apps/${encodeURIComponent(appID)}/action`, {
      method: "POST",
      body: JSON.stringify({ action: actionName, confirmed: true, confirm_app_id: appID }),
    });
    if (result.outcome) {
      if (state.userView === "app-detail" && result.outcome.target_id) {
        state.userDetailID = result.outcome.target_id;
      }
      if (options.inlineSuccess && result.outcome.recovered) {
        inlineSuccessOutcome = result.outcome;
        keepButtonState = true;
      } else {
        showNotice(agentRepairOutcomeNotice(result.outcome), agentRepairOutcomeTone(result.outcome));
      }
    } else {
      showNotice(`${actionLabel} requested for ${label}.`);
    }
    await refresh();
    if (inlineSuccessOutcome) {
      if (returnToChat) setCompactView("chat");
      keepButtonState = renderUserAppActionInlineSuccess(button, app, actionName, inlineSuccessOutcome);
      if (!keepButtonState) {
        showNotice(agentRepairOutcomeNotice(inlineSuccessOutcome), "info");
      }
    }
  } catch (error) {
    showNotice(compactSurfaceErrorMessage(error, `${actionLabel} could not be completed. Check the app status and try again.`), "error");
  } finally {
    if (button?.isConnected && keepButtonState) {
      button.disabled = false;
    } else if (button?.isConnected) {
      button.disabled = false;
      button.textContent = original;
    }
  }
}

function renderUserAppActionInlineSuccess(button, app, action, outcome = {}) {
  const prompt = button?.closest?.(".user-repair-prompt");
  if (!prompt) return false;
  const label = cleanChatText(outcome.target_label) || appDisplayName(app);
  const actionText = directActionPastTense(action);
  const actions = prompt.querySelector(".agent-plan-actions");
  if (!actions) return false;
  prompt.querySelector(".user-action-result")?.remove();
  const result = node("div", { class: "agent-repair-outcome recovered user-action-result", role: "status", "aria-live": "polite" },
    node("strong", { text: `${label} ${actionText} successfully.` }),
    node("small", { text: "Send another message or try again if you continue to have issues." }),
  );
  actions.insertAdjacentElement("afterend", result);
  button.disabled = false;
  button.textContent = "Try again";
  button.setAttribute("data-glyph", "r");
  button.title = `Try ${userAppActionLabel(action).toLowerCase()} again`;
  const pill = prompt.querySelector(".agent-plan-head .settings-state-pill");
  if (pill) {
    pill.classList.remove("state-warn", "state-muted", "state-bad");
    pill.classList.add("state-ok");
    pill.textContent = sentenceCase(directActionPastTense(action));
  }
  return true;
}

function sentenceCase(value) {
  const text = String(value || "").trim();
  if (!text) return "";
  return text.charAt(0).toUpperCase() + text.slice(1);
}

function userAppActionLabel(action) {
  switch (String(action || "").trim().toLowerCase()) {
    case "start_array":
      return "Start array";
    case "start":
      return "Start";
    case "stop":
      return "Stop";
    case "restart":
      return "Restart";
    default:
      return "Control";
  }
}

function directActionPastTense(action) {
  switch (String(action || "").trim().toLowerCase()) {
    case "start_array":
      return "started";
    case "start":
      return "started";
    case "stop":
      return "stopped";
    case "restart":
      return "restarted";
    default:
      return "changed";
  }
}

function userAppActionProgressText(action) {
  switch (action) {
    case "start":
      return "Starting";
    case "stop":
      return "Stopping";
    case "restart":
      return "Restarting";
    default:
      return "Working";
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
  const bounded = Math.max(0, Math.min(100, number));
  if (bounded === 0) return "Has not worked during this period.";
  const rounded = bounded >= 99.95 ? "100" : bounded.toFixed(1).replace(/\.0$/, "");
  return `Working ${rounded}% of the time.`;
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

/* ---------------------------------------------------------------------------
   Overview verdict.

   The Overview page exists to answer one question, so it opens by answering it
   in a single display-size sentence. When nothing is wrong that sentence plus
   the monitor rail is the whole page; when something is wrong the sentence
   names it and carries the two actions worth taking.

   The compact surface answers the same question through compactHero(). The two
   share the shape deliberately, but not the words: this one is allowed to name
   the array, the container service and the WAN, and the compact one is not.
   --------------------------------------------------------------------------- */
function renderVerdict(snapshot) {
  const element = $("verdict");
  if (!element) return;
  const verdict = adminVerdict(snapshot);
  element.className = `verdict ${verdict.tone}`;
  element.replaceChildren(
    node("p", { class: "verdict-state" }, statusIndicator(verdict.state, verdict.tone)),
    node("h1", { class: "verdict-headline", text: verdict.headline }),
    node("p", { class: "verdict-detail", text: verdict.detail }),
    verdict.actions.length
      ? node("div", { class: "verdict-actions" }, ...verdict.actions.map((action) => node("button", {
        type: "button",
        class: action.primary ? "primary command" : "command",
        "data-glyph": action.glyph,
        onclick: action.run,
        text: action.label,
      })))
      : null,
  );
}

function adminVerdict(snapshot) {
  const infra = snapshot.infrastructure || {};
  const apps = snapshot.apps || [];
  const incidents = snapshot.incidents || [];
  const offline = apps.filter((app) => normalizeStatus(app.current_status) === "offline");
  const degraded = apps.filter((app) => normalizeStatus(app.current_status) === "degraded");
  const checked = infra.last_checked_at ? ` · checked ${relativeTime(infra.last_checked_at)}` : "";
  const askAction = { label: "Ask what happened", glyph: "?", run: () => setActiveTab("diagnostics") };
  const activityAction = { label: "View activity", glyph: ">", run: () => setActiveTab("activity") };

  if (infra.nas_reachable === false) {
    return {
      tone: "offline",
      state: "Not working",
      headline: "The NAS is unreachable.",
      detail: `Nothing downstream of the NAS can be verified while it is offline${checked}.`,
      actions: [{ ...askAction, primary: true }, activityAction],
    };
  }
  if (hasHomeServerProblem(infra)) {
    return {
      tone: "offline",
      state: "Not working",
      headline: "The server needs attention.",
      detail: `${serverSummary(infra)}${checked}`,
      actions: [{ label: "Open server health", glyph: ">", primary: true, run: () => setActiveTab("server") }, askAction],
    };
  }
  if (infra.internet_reachable === false) {
    return {
      tone: "degraded",
      state: "Degraded",
      headline: "The internet connection is down.",
      detail: `The server itself is responding. ${routerSummary(infra)}${checked}`,
      actions: [{ label: "Open router", glyph: ">", primary: true, run: () => setActiveTab("router") }, askAction],
    };
  }
  if (offline.length === 1 && !degraded.length) {
    const name = appDisplayName(offline[0]);
    return {
      tone: "degraded",
      state: "Degraded",
      headline: `${name} is not running.`,
      detail: `Every other monitored service is healthy${checked}.`,
      actions: [{ label: `Open ${name}`, glyph: ">", primary: true, run: () => setActiveTab("apps") }, askAction],
    };
  }
  if (offline.length || degraded.length) {
    const parts = [];
    if (offline.length) parts.push(`${offline.length} offline`);
    if (degraded.length) parts.push(`${degraded.length} degraded`);
    return {
      tone: "degraded",
      state: "Degraded",
      headline: `${offline.length + degraded.length} apps need attention.`,
      detail: `${parts.join(", ")} of ${apps.length} monitored${checked}.`,
      actions: [{ label: "Open apps", glyph: ">", primary: true, run: () => setActiveTab("apps") }, askAction],
    };
  }
  if (incidents.length) {
    return {
      tone: "degraded",
      state: "Degraded",
      headline: `${incidents.length} open incident${incidents.length === 1 ? "" : "s"}.`,
      detail: `${incidents[0].summary || incidents[0].type || "An incident is open"}${checked}.`,
      actions: [{ ...activityAction, primary: true }, askAction],
    };
  }
  if (!snapshot.overall_status && !apps.length && !infra.last_checked_at) {
    return {
      tone: "unknown",
      state: "Checking",
      headline: "Collecting status…",
      detail: "No snapshot has been returned yet.",
      actions: [],
    };
  }
  return {
    tone: "online",
    state: "Working",
    headline: "Everything is working.",
    detail: `${apps.length} app${apps.length === 1 ? "" : "s"}, storage and internet all healthy${checked}.`,
    actions: [],
  };
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
  $("server-detail-grid").replaceChildren(...[
    detailSection("server.collectors", "Collectors", [
      detailRow("Unraid", sourceHealth(infra, "unraid")),
      detailRow("Docker", sourceHealth(infra, "docker")),
      detailRow("Checked", infra.last_checked_at ? formatTime(infra.last_checked_at) : null),
    ]),
    detailSection("server.storage", "Storage", [
      detailRow("Version", infra.unraid_version || null),
      detailRow("Disks", infra.array_disk_count ? `${infra.array_disk_count} disk${infra.array_disk_count === 1 ? "" : "s"} (${infra.array_disk_warning_count || 0} warning${infra.array_disk_warning_count === 1 ? "" : "s"})` : null),
      detailRow("Capacity", infra.array_capacity_total_bytes ? `${formatBytes(infra.array_capacity_used_bytes)} used of ${formatBytes(infra.array_capacity_total_bytes)} (${formatPercent(infra.array_capacity_used_pct)})` : null),
      detailRow("Parity", infra.parity_check_state || null),
      detailRow("Warnings", infra.storage_warnings?.length ? infra.storage_warnings.join("; ") : null),
    ]),
  ].filter(Boolean));
}

/* Response times, with each link measured against its own recent median rather
   than a fixed number — 200ms is normal on some connections and terrible on
   others. "Usual" is omitted until there are enough samples to mean anything. */
function probeLatencyRows(infra) {
  const readings = infra.probe_latencies || [];
  return readings.map((probe) => {
    const parts = [`${probe.latency_ms} ms`];
    if (probe.baseline_ms) parts.push(`usual ${probe.baseline_ms} ms`);
    if (probe.failure_rate >= 0.05) parts.push(`${Math.round(probe.failure_rate * 100)}% of recent checks failed`);
    if (!probe.ok) parts.push("failing now");
    return detailRow(probeLatencyLabel(probe.subject), parts.join(" · "));
  });
}

function probeLatencyLabel(subject) {
  const labels = { internet: "Internet", dns: "DNS", router: "Router", nas: "Server" };
  return labels[subject] || subject;
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
      detailRow("Checked", infra.last_checked_at ? formatTime(infra.last_checked_at) : null),
    ]),
    detailSection("router.unifi", "UniFi", [
      detailRow("Site", infra.unifi_site_name || infra.unifi_site_id || null),
      detailRow("Devices", infra.unifi_device_count ? `${infra.unifi_device_count} device${infra.unifi_device_count === 1 ? "" : "s"} (${infra.unifi_offline_device_count || 0} offline)` : null),
      detailRow("Clients", Number.isFinite(Number(infra.unifi_client_count)) ? `${infra.unifi_client_count}` : null),
      detailRow("Updates", Number.isFinite(Number(infra.unifi_firmware_updates)) ? `${infra.unifi_firmware_updates}` : null),
      detailRow("WANs", Number.isFinite(Number(infra.unifi_wan_count)) ? `${infra.unifi_wan_count}` : null),
      detailRow("Warnings", infra.unifi_warnings?.length ? infra.unifi_warnings.join("; ") : null),
    ]),
    detailSection("router.timing", "Response times", probeLatencyRows(infra)),
    detailSection("router.nas-link", "NAS Link", [
      detailRow("Expected", infra.expected_nas_link_mbps ? `${infra.expected_nas_link_mbps} Mbps` : null),
      detailRow("Negotiated", infra.nas_link_speed_mbps ? `${infra.nas_link_speed_mbps} Mbps` : null),
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
  const displayValue = statusish ? displayLabel(value) : value;
  return node("span", { class: `overview-metric${statusish ? ` overview-metric-status ${status}` : ""}` },
    statusish ? statusIndicator(value, status, "status-dot-only") : null,
    node("span", { text: label }),
    showValue ? node("strong", { text: displayValue }) : null,
  );
}

function displayLabel(value) {
  const text = String(value || "").trim().replaceAll("_", " ");
  return text.replace(/\b\w/g, (letter) => letter.toUpperCase());
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

// A missing value is not a value. Pass null (not "No disk data") and the row
// disappears; a section whose rows all disappear says so once instead of listing
// five absences styled exactly like real readings.
function detailRow(label, value) {
  if (value === null || value === undefined || value === "") return null;
  return node("div", { class: "detail-row" },
    node("span", { class: "meta-label", text: label }),
    node("strong", { text: value }),
  );
}

function detailSection(id, title, children) {
  const childNodes = children.filter(Boolean);
  return visibleMonitor(id, node("section", { class: "detail-section monitor-shell" },
    node("h3", { text: title }),
    childNodes.length
      ? node("div", { class: "detail-list" }, childNodes)
      : node("p", { class: "detail-empty", text: "Nothing reported by this collector." }),
  ));
}

function renderFacts(facts) {
  $("fact-count").textContent = `${facts.length} fact${facts.length === 1 ? "" : "s"}`;
  // An empty state rendered like data is still data on screen. When there is
  // nothing to say, the whole panel steps aside instead of listing "no X".
  const panel = $("overview-facts");
  if (panel) panel.hidden = !facts.length;
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
  const panel = $("overview-incidents");
  if (panel) panel.hidden = !incidents.length;
  if (!incidents.length) {
    $("incident-strip").replaceChildren(node("div", { class: "empty", text: "No current incidents." }));
    return;
  }
  $("incident-strip").replaceChildren(...incidents.slice(0, 3).map(renderIncidentSummaryRow));
}

// Rendered as the expanded body of an activity row, so it must not repeat the
// summary, time or severity the row already shows.
function renderIncidentCard(incident) {
  return node("article", { class: "incident-card" },
    node("p", { class: "muted", text: `${incident.id || incident.type}${incident.affected_services?.length ? ` · affects ${incident.affected_services.join(", ")}` : ""}` }),
    incident.evidence?.length ? evidenceChips(incident.evidence) : null,
  );
}

// Evidence arrives either as key=value tokens or as a plain sentence. Only the
// former is a labelled pair; splitting a sentence on whitespace and labelling
// every word "Evidence" produced rows of noise.
function evidenceChips(evidence) {
  const chips = [];
  for (const item of evidence || []) {
    const text = String(item || "").trim();
    if (!text) continue;
    const tokens = text.split(/\s+/);
    if (tokens.length > 1 && tokens.every((token) => token.indexOf("=") > 0)) {
      for (const token of tokens) {
        const index = token.indexOf("=");
        chips.push([token.slice(0, index), token.slice(index + 1)]);
      }
      continue;
    }
    const index = tokens.length === 1 ? text.indexOf("=") : -1;
    if (index > 0) chips.push([text.slice(0, index), text.slice(index + 1)]);
    else chips.push(["", text]);
  }
  return node("div", { class: "evidence-chips" }, chips.map(([label, value]) => node("span", { class: "evidence-chip" },
    label ? node("strong", { text: label }) : null,
    node("span", { text: value || "unknown" }),
  )));
}

function renderIncidentSummaryRow(incident) {
  const details = [
    incident.id || incident.type,
    incident.affected_services?.length ? `Affected: ${incident.affected_services.join(", ")}` : "",
    incident.evidence?.length ? incident.evidence[0] : "",
  ].filter(Boolean);
  return node("article", { class: "incident-card incident-row" },
    node("header", {},
      node("h3", { text: incident.summary || incident.type }),
      node("span", { class: `severity ${incident.severity || "none"}`, text: incident.severity || "none" }),
    ),
    details.length ? node("p", { class: "muted incident-row-meta" }, details.map((detail) => node("span", { text: detail }))) : null,
  );
}

function renderApps(apps) {
  const search = state.appSearch.trim().toLowerCase();
  const visible = apps.filter((app) => {
    const statusMatch = state.appFilter === "all" || app.current_status === state.appFilter;
    const text = [app.display_name, app.app_id, app.current_status, app.image_ref].join(" ").toLowerCase();
    return statusMatch && (!search || text.includes(search));
  });
  const count = $("app-count");
  if (count) count.textContent = `${visible.length} app${visible.length === 1 ? "" : "s"}${visible.length === apps.length ? "" : ` of ${apps.length}`}`;
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
    ),
    // Controls are a sibling of the identity block, not a child of it, so the
    // desktop layout can park them in their own column on the right edge.
    node("div", { class: "app-row-actions" },
      ...actions,
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
  if (!compactNotificationSignupComplete()) {
    const app = (state.snapshot?.apps || []).find((item) => String(item.app_id || "") === String(appID || ""));
    const previousFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    openCompactNotificationSignup({
      app,
      input: previousFocus,
      afterSignup: () => savePreferenceDirect(appID, { showSuccess: false }),
    });
    return;
  }
  await savePreferenceDirect(appID);
}

async function savePreferenceDirect(appID, options = {}) {
  try {
    const saved = await api("/api/user/notification-preferences", {
      method: "POST",
      body: JSON.stringify({ app_id: appID, notify_on_down: true, notify_on_recovery: true }),
    });
    state.notificationPreferences.set(String(appID || ""), saved);
    state.notificationPreferencesLoaded = true;
    if (options.showSuccess !== false) showNotice("Notification preference saved.");
    await refresh();
    return true;
  } catch (error) {
    showNotice(compactSurfaceErrorMessage(error, "That notification preference could not be saved. Try again."), "error");
    return false;
  }
}

async function diagnose() {
  await runDiagnosis(
    $("diagnostic-question").value || "What is wrong right now?",
    $("diagnostic-output"),
    { input: $("diagnostic-question"), button: $("diagnose"), autoRepairInput: $("diagnostic-auto-repair"), busyKey: "diagnostic" },
  );
}

async function runUserChat() {
  await runDiagnosis(
    $("user-chat-input").value || "What is wrong right now?",
    $("user-chat-output"),
    { input: $("user-chat-input"), button: $("user-chat-send"), autoRepairInput: $("user-chat-auto-repair"), busyKey: "user" },
  );
}

function focusUserChat() {
  setCompactView("chat");
  const panel = document.querySelector(".panel.user-chat");
  if (!panel || panel.hidden) return;
  panel.scrollIntoView({ block: "nearest", inline: "nearest", behavior: "smooth" });
  $("user-chat-input").focus({ preventScroll: true });
  scheduleCompactChatIntoView();
}

async function runAssistantChat() {
  await runDiagnosis(
    $("assistant-input").value || "What is wrong right now?",
    $("assistant-output"),
    { input: $("assistant-input"), button: $("assistant-send"), autoRepairInput: $("assistant-auto-repair"), busyKey: "assistant" },
  );
  $("assistant-panel").hidden = false;
}

async function runDiagnosis(question, output, options = {}) {
  const adminSurface = hasAdminSurface();
  const path = adminSurface ? "/api/admin/diagnose" : "/api/user/diagnose";
  const questionText = String(question || "What is wrong right now?").trim() || "What is wrong right now?";
  const wantsAutoRepair = autoRepairRequested(options.autoRepairInput);
  if (options.busyKey && state.chatBusy[options.busyKey]) return;
  if (options.busyKey) state.chatBusy[options.busyKey] = true;
  setChatPending(output, "Checking status...");
  setChatControlsBusy(options.input, options.button, true);
  syncAutoRepairControls(state.snapshot);
  try {
    const result = await api(path, {
      method: "POST",
      body: JSON.stringify({ question: questionText, auto_repair: wantsAutoRepair }),
    });
    output.classList.remove("chat-empty", "muted", "chat-pending", "chat-error");
    output.classList.add("chat-result");
    if (adminSurface) {
      const evidence = cleanChatList(result.evidence);
      const diagnosisText = firstChatText(result.diagnosis, result.general_user_summary, "No diagnosis returned.");
      const adminMessage = cleanChatText(result.admin_message);
      output.replaceChildren(
        node("strong", { text: `${result.severity} confidence ${Math.round((result.confidence || 0) * 100)}%` }),
        node("p", { text: diagnosisText }),
        evidence.length ? node("p", { class: "muted", text: `Evidence: ${evidence.join("; ")}` }) : null,
        adminMessage ? node("p", { class: "muted", text: adminMessage }) : null,
        result.agent_plan ? renderAgentPlanPrompt(result.agent_plan) : null,
      );
      maybeOpenAgentApprovalDialog(result.agent_plan);
    } else {
      const userPlanNodes = [];
      if (isArrayStartPlan(result.agent_plan)) {
        userPlanNodes.push(renderArrayStartPrompt(result.agent_plan));
      } else if (result.agent_plan?.auto_repair_attempted) {
        userPlanNodes.push(renderAgentPlanPrompt(result.agent_plan));
        if (!result.agent_plan.auto_executed && result.agent_plan.can_request_repair) userPlanNodes.push(renderUserRepairRequestPrompt(result.agent_plan, result));
      } else if (result.agent_plan?.can_request_repair) {
        userPlanNodes.push(renderUserRepairRequestPrompt(result.agent_plan, result));
      }
      const autoFixed = !!(result.agent_plan?.auto_executed && result.agent_plan?.outcome?.recovered);
      const answerText = firstCompactChatText(result.general_user_summary, result.diagnosis, "I could not find a clear answer.");
      output.replaceChildren(
        node("strong", { text: "Answer" }),
        node("p", { text: answerText }),
        ...userPlanNodes,
        result.should_notify_admin && !autoFixed ? node("p", { class: "muted", text: "Tell the admin if you need this fixed." }) : null,
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
    syncAutoRepairControls(state.snapshot);
  }
}

function renderAgentPlanPrompt(plan) {
  const requiresApproval = !!plan.requires_admin_approval && !plan.outcome && !plan.auto_executed;
  const statusText = agentPlanStatusText(plan);
  const statusTone = agentPlanStatusTone(plan);
  const targetText = agentPlanTargetText(plan);
  const title = firstChatText(plan.title, "Agent fix plan");
  const summary = firstChatText(plan.summary, "Review the model recommendation before allowing any action.");
  const autoRepairMessage = cleanChatText(plan.auto_repair_message);
  return node("section", { class: "agent-plan-prompt", "data-plan-id": plan.id || "" },
    node("div", { class: "agent-plan-head" },
      node("span", {},
        node("strong", { text: title }),
        node("small", { text: summary }),
        targetText ? node("small", { text: targetText }) : null,
      ),
      node("span", { class: `settings-state-pill ${statusTone}`, text: statusText }),
    ),
    autoRepairMessage ? node("p", { class: "muted", text: autoRepairMessage }) : null,
    plan.outcome ? renderAgentRepairOutcome(plan.outcome) : null,
    requiresApproval ? node("div", { class: "agent-plan-actions" },
      node("button", {
        type: "button",
        class: "command",
        "data-glyph": "v",
        onclick: () => openAgentApprovalDialog(plan),
        text: "Open approval",
      }),
    ) : null,
  );
}

function agentPlanStatusText(plan) {
  if (plan?.auto_executed && plan?.outcome?.recovered) return "Fixed";
  if (plan?.auto_executed) return `${userAppActionLabel(plan?.outcome?.action || plan?.direct_action || "restart")} sent`;
  switch (plan?.status) {
    case "direct_array_start_available":
      return "Ready";
    case "approval_ready":
      return "Ready";
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
  if (plan?.auto_executed || plan?.can_execute) return "state-warn";
  if (plan?.status === "auto_review_refused" || plan?.status === "auto_execute_failed") return "state-bad";
  return "state-muted";
}

function renderUserRepairRequestPrompt(plan, diagnosis = {}) {
  const target = plan?.target || {};
  if (!plan?.can_request_repair || !target.id) return null;
  const label = target.label || target.id;
  const directAction = String(plan.direct_action || "restart").trim().toLowerCase();
  const directActionLabel = userAppActionLabel(directAction);
  const directActionAvailable = !!plan.can_execute;
  return node("section", { class: "agent-plan-prompt user-repair-prompt" },
    node("div", { class: "agent-plan-head" },
      node("span", {},
        node("strong", { text: directActionAvailable ? `${directActionLabel} this app` : "Ask admin to fix this" }),
        node("small", { text: directActionAvailable ? `${label} can be ${directActionPastTense(directAction)} from here.` : `${label} can be sent to an admin for review.` }),
      ),
      node("span", { class: `settings-state-pill ${directActionAvailable ? "state-ok" : "state-warn"}`, text: directActionAvailable ? "Ready" : "Admin review" }),
    ),
    node("div", { class: "agent-plan-actions" },
      directActionAvailable ? node("button", {
        type: "button",
        class: "primary command",
        "data-glyph": directAction === "start" ? ">" : "r",
        onclick: (event) => runUserAppAction({ app_id: target.id, display_name: label }, directAction, event.currentTarget, { inlineSuccess: true }),
        text: `${directActionLabel} now`,
      }) : null,
      node("button", {
        type: "button",
        class: directActionAvailable ? "command" : "primary command",
        "data-glyph": "!",
        onclick: (event) => requestAdminRepair(plan, diagnosis, event.currentTarget),
        text: "Ask admin",
      }),
    ),
  );
}

function isArrayStartPlan(plan) {
  return String(plan?.recommended_action_id || "").trim() === "ask_admin_to_start_array";
}

function renderArrayStartPrompt(plan) {
  const canStart = !!(plan?.can_execute && plan?.execution_token);
  const statusTone = canStart ? "state-warn" : agentPlanStatusTone(plan);
  const statusText = canStart ? "Ready" : agentPlanStatusText(plan);
  const summary = firstChatText(
    plan?.summary,
    "Contact the admin first to confirm the array was not intentionally stopped. If the admin is unavailable or asleep and service needs to be restored, starting the array is okay.",
  );
  return node("section", { class: "agent-plan-prompt array-start-prompt", "data-plan-id": plan?.id || "" },
    node("div", { class: "agent-plan-head" },
      node("span", {},
        node("strong", { text: firstChatText(plan?.title, "Start array") }),
        node("small", { text: summary }),
      ),
      node("span", { class: `settings-state-pill ${statusTone}`, text: statusText }),
    ),
    node("div", { class: "agent-plan-actions" },
      node("button", {
        type: "button",
        class: "primary command",
        "data-glyph": ">",
        disabled: !canStart,
        onclick: (event) => runArrayStartAction(plan, event.currentTarget),
        text: "Start storage",
      }),
    ),
  );
}

async function runArrayStartAction(plan, button = null) {
  if (!plan?.execution_token) {
    showNotice("This LLM action is no longer available. Send another message to check again.", "error");
    return;
  }
  if (!confirm("Start the Unraid array? Contact the admin first if possible, unless they are unavailable or asleep and service needs to be restored.")) return;
  const original = button?.textContent || "";
  if (button) {
    button.disabled = true;
    button.textContent = "Starting";
  }
  markAgentRepairProgress(plan, "pending", "Starting the Unraid array and checking status...");
  try {
    const response = await api("/api/user/agent/action", {
      method: "POST",
      body: JSON.stringify({ execution_token: plan.execution_token, choice: "start_array" }),
    });
    if (response.outcome) {
      appendAgentRepairOutcome(response.outcome, plan);
      showNotice(agentRepairOutcomeNotice(response.outcome), agentRepairOutcomeTone(response.outcome));
    } else {
      showNotice("Start array request was sent.");
    }
    await refresh();
  } catch (error) {
    const message = compactSurfaceErrorMessage(error, "NoobBoard could not start storage. Check the server status and try again.");
    markAgentRepairProgress(plan, "failed", message);
    showNotice(message, "error");
    if (button?.isConnected) {
      button.disabled = false;
      button.setAttribute("aria-disabled", "false");
      button.textContent = original;
    }
  }
}

async function requestAdminRepair(plan, diagnosis = {}, button = null) {
  if (!plan?.target?.id) return;
  const original = button?.textContent || "";
  if (button) {
    button.disabled = true;
    button.textContent = "Sending";
  }
  try {
    const summary = firstChatText(diagnosis.general_user_summary, diagnosis.diagnosis, plan.summary, "A user asked for help with this app.");
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
    showNotice(compactSurfaceErrorMessage(error, "The request could not be sent to the admin. Try again."), "error");
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
      reason: plan?.can_execute ? "" : "Turn on admin-approved app fixes and per-app admin/AI app fix first.",
    },
  ];
}

function initialAgentApprovalChoice(options) {
  return (options.find((option) => option.enabled && option.selected) || options.find((option) => option.enabled) || options[0] || {}).id || "deny";
}

function agentPlanDirectAction(plan) {
  const action = String(plan?.direct_action || plan?.outcome?.action || "restart").trim().toLowerCase();
  if (["start", "stop", "restart"].includes(action)) return action;
  return "restart";
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
  const approvalSummary = firstChatText(plan.summary, "The model suggested a fix. Approve or deny before allowing any automatic action.");
  const requestedFix = firstChatText(plan.title, plan.recommended_action_id, "Unknown action");
  const directAction = agentPlanDirectAction(plan);
  const actionLabel = userAppActionLabel(directAction);
  const actionVerb = actionLabel.toLowerCase();
  let selectedChoice = initialAgentApprovalChoice(approvalOptions);
  const optionRows = [];
  const cooldownNode = renderAgentApprovalCooldown(plan);
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
    text: plan.can_execute ? `Approval will ${actionVerb} the selected app once.` : "Fixes require admin app fixes and per-app opt-in.",
  });
  const updateSelection = () => {
    const selectedOption = approvalOptions.find((option) => option.id === selectedChoice);
    const enabled = !!selectedOption?.enabled;
    submitButton.disabled = !enabled;
    submitButton.textContent = selectedChoice === "deny" ? "Do not allow" : selectedOption?.label || "Submit choice";
    for (const item of optionRows) {
      item.row.classList.toggle("selected", item.option.id === selectedChoice);
      item.row.classList.toggle("disabled", !item.option.enabled);
      item.input.disabled = !item.option.enabled;
      if (item.reasonNode) {
        item.reasonNode.textContent = item.option.reason || "";
        item.reasonNode.hidden = !item.option.reason;
      }
    }
    if (selectedOption?.reason) {
      statusNode.dataset.tone = "bad";
      statusNode.textContent = selectedOption.reason;
    } else if (selectedChoice === "deny") {
      statusNode.dataset.tone = "info";
      statusNode.textContent = "No automatic fix will run.";
    } else {
      statusNode.dataset.tone = "info";
      statusNode.textContent = `NoobBoard will ${actionVerb} this app once, then verify whether it recovered.`;
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
    const reasonNode = node("small", { class: "agent-approval-option-reason", text: option.reason || "", hidden: !option.reason });
    const row = node("label", { class: `agent-approval-option${option.enabled ? "" : " disabled"}${option.id === selectedChoice ? " selected" : ""}` },
      input,
      node("span", { class: "agent-approval-option-text" },
        node("strong", { text: option.label || option.id }),
        option.description ? node("small", { text: option.description }) : null,
        reasonNode,
      ),
    );
    optionRows.push({ option, row, input, reasonNode });
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
          node("p", { class: "eyebrow", text: "Fix approval" }),
          node("h2", { id: "agent-approval-title", text: "Allow automatic fix?" }),
        ),
      ),
      closeButton,
    ),
    node("div", { class: "openai-auth-content agent-approval-content" },
      node("p", { class: "openai-auth-copy", text: approvalSummary }),
      node("div", { class: "agent-approval-request" },
        node("span", { class: "openai-auth-label", text: "Requested fix" }),
        node("strong", { text: requestedFix }),
        node("span", { class: "agent-approval-direct-action", text: `${actionLabel} once` }),
        agentPlanTargetText(plan) ? node("small", { text: agentPlanTargetText(plan) }) : null,
      ),
      cooldownNode,
      node("ol", { class: "agent-approval-steps", "aria-label": "Approval progress" },
        node("li", { class: "current" }, "Review"),
        node("li", {}, "Approve"),
        node("li", {}, `${actionLabel} and verify`),
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
  state.agentApprovalDialog = { backdrop, dialog, previousFocus, statusNode, submitButton };
  updateSelection();
  startAgentApprovalCooldownCountdown(plan, cooldownNode, approvalOptions, optionRows, updateSelection);
  const selectedInput = optionRows.find((item) => item.option.id === selectedChoice && !item.input.disabled)?.input;
  (selectedInput || closeButton).focus({ preventScroll: true });
}

function renderAgentApprovalCooldown(plan) {
  const cooldownSeconds = agentPlanCooldownSeconds(plan);
  const retrySeconds = agentPlanRetrySeconds(plan);
  const text = retrySeconds > 0
    ? `Cooldown active. Try again in ${formatSeconds(retrySeconds)}.`
    : `After this fix, this app has a ${formatSeconds(cooldownSeconds)} cooldown before another automatic fix.`;
  return node("div", { class: `agent-approval-cooldown${retrySeconds > 0 ? " is-waiting" : ""}`, "aria-live": "polite" },
    node("strong", { text: retrySeconds > 0 ? "Cooldown" : "Cooldown after approval" }),
    node("small", { text }),
  );
}

function startAgentApprovalCooldownCountdown(plan, cooldownNode, approvalOptions, optionRows, updateSelection) {
  if (state.agentApprovalCooldownTimer) {
    window.clearInterval(state.agentApprovalCooldownTimer);
    state.agentApprovalCooldownTimer = null;
  }
  const deadline = agentPlanRetryDeadline(plan);
  if (!deadline || !cooldownNode) return;
  const allowOption = approvalOptions.find((option) => option.id === "allow_once");
  const label = cooldownNode.querySelector("strong");
  const detail = cooldownNode.querySelector("small");
  const tick = () => {
    const remaining = Math.max(0, Math.ceil((deadline - Date.now()) / 1000));
    if (remaining > 0) {
      cooldownNode.classList.add("is-waiting");
      if (label) label.textContent = "Cooldown";
      if (detail) detail.textContent = `Try again in ${formatSeconds(remaining)}. This updates automatically.`;
      if (allowOption) {
        allowOption.enabled = false;
        allowOption.reason = `Cooldown active. Try again in ${formatSeconds(remaining)}.`;
      }
    } else {
      cooldownNode.classList.remove("is-waiting");
      cooldownNode.classList.add("is-ready");
      if (label) label.textContent = "Cooldown clear";
      if (detail) detail.textContent = `This action is available. After it runs, this app has a ${formatSeconds(agentPlanCooldownSeconds(plan))} cooldown.`;
      if (allowOption) {
        allowOption.enabled = true;
        allowOption.reason = "";
      }
      if (state.agentApprovalCooldownTimer) {
        window.clearInterval(state.agentApprovalCooldownTimer);
        state.agentApprovalCooldownTimer = null;
      }
    }
    for (const item of optionRows) {
      if (item.option.id === "allow_once") {
        item.input.disabled = !item.option.enabled;
      }
    }
    updateSelection();
  };
  tick();
  if (agentPlanRetrySeconds(plan) > 0) {
    state.agentApprovalCooldownTimer = window.setInterval(tick, 1000);
  }
}

function agentPlanCooldownSeconds(plan) {
  const value = Number(plan?.repair_cooldown_seconds || 0);
  return Number.isFinite(value) && value > 0 ? value : 60;
}

function agentPlanRetrySeconds(plan) {
  const value = Number(plan?.retry_after_seconds || 0);
  return Number.isFinite(value) && value > 0 ? Math.ceil(value) : 0;
}

function agentPlanRetryDeadline(plan) {
  const retryAt = Date.parse(plan?.retry_at || "");
  if (Number.isFinite(retryAt) && retryAt > Date.now()) return retryAt;
  const seconds = agentPlanRetrySeconds(plan);
  return seconds > 0 ? Date.now() + seconds * 1000 : 0;
}

async function submitAgentApproval(plan, choice, button) {
  const selectedChoice = String(choice || "deny").trim();
  const allowed = selectedChoice === "allow_once";
  const directAction = agentPlanDirectAction(plan);
  const actionLabel = userAppActionLabel(directAction);
  const actionProgress = userAppActionProgressText(directAction);
  const originalText = button?.textContent || "";
  if (button) {
    button.disabled = true;
    button.textContent = allowed ? actionProgress : "Recording";
  }
  const dialog = state.agentApprovalDialog;
  if (allowed) dialog?.dialog?.classList.add("is-running");
  if (dialog?.statusNode) {
    dialog.statusNode.dataset.tone = "info";
    dialog.statusNode.textContent = allowed ? `Approval recorded. ${actionProgress} the app and checking status...` : "Recording denial...";
  }
  if (allowed) {
    markAgentRepairProgress(plan, "pending", `Approval recorded. ${actionProgress} the app and checking whether it recovers...`);
    closeAgentApprovalDialog({ returnFocus: false });
  }
  try {
    const response = await api("/api/admin/agent/approval", {
      method: "POST",
      body: JSON.stringify({
        approval_token: plan.approval_token || "",
        choice: selectedChoice,
      }),
    });
    if (allowed && response.outcome) {
      appendAgentRepairOutcome(response.outcome, plan);
      if (dialog?.statusNode?.isConnected) {
        dialog.statusNode.dataset.tone = agentRepairOutcomeTone(response.outcome) === "error" ? "bad" : "info";
        dialog.statusNode.textContent = agentRepairOutcomeNotice(response.outcome);
      }
      showNotice(agentRepairOutcomeNotice(response.outcome), agentRepairOutcomeTone(response.outcome));
      closeAgentApprovalDialog({ returnFocus: false });
    } else if (allowed) {
      markAgentRepairProgress(plan, "failed", `The ${actionLabel.toLowerCase()} request completed, but NoobBoard did not return a verification outcome.`);
      if (dialog?.statusNode?.isConnected) {
        dialog.statusNode.dataset.tone = "bad";
        dialog.statusNode.textContent = "The request completed, but NoobBoard did not return verification.";
      }
      showNotice("Fix request completed without a verification outcome.", "error");
    } else {
      showNotice("Automatic fix was not allowed.");
      closeAgentApprovalDialog();
    }
  } catch (error) {
    const message = hasAdminSurface() ? error.message : compactSurfaceErrorMessage(error, "NoobBoard could not run the approved fix. Check again and retry.");
    if (allowed) markAgentRepairProgress(plan, "failed", message);
    if (dialog?.statusNode?.isConnected) {
      dialog.dialog?.classList.remove("is-running");
      dialog.statusNode.dataset.tone = "bad";
      dialog.statusNode.textContent = message;
    }
    showNotice(message, "error");
  } finally {
    if (button?.isConnected) {
      button.disabled = false;
      button.textContent = originalText;
    }
  }
}

function cssEscapeValue(value) {
  const text = String(value || "");
  if (window.CSS?.escape) return window.CSS.escape(text);
  return text.replace(/["\\]/g, "\\$&");
}

function agentPlanPromptFor(plan) {
  const planID = String(plan?.id || "").trim();
  if (planID) {
    const matched = document.querySelector(`.agent-plan-prompt[data-plan-id="${cssEscapeValue(planID)}"]`);
    if (matched) return matched;
  }
  return document.querySelector(".agent-plan-prompt");
}

function setAgentPlanPromptStatus(prompt, text, tone = "state-muted") {
  const pill = prompt?.querySelector(".agent-plan-head .settings-state-pill");
  if (!pill) return;
  pill.className = `settings-state-pill ${tone}`;
  pill.textContent = text;
}

function setAgentPlanActionsDisabled(prompt, disabled) {
  for (const button of prompt?.querySelectorAll(".agent-plan-actions button") || []) {
    button.disabled = !!disabled;
    button.setAttribute("aria-disabled", String(!!disabled));
  }
}

function markAgentRepairProgress(plan, stateName, message) {
  const prompt = agentPlanPromptFor(plan);
  if (!prompt) return null;
  const state = String(stateName || "pending").trim() || "pending";
  const text = cleanChatText(message) || (state === "failed" ? "NoobBoard could not run the approved fix." : "NoobBoard is running the approved fix.");
  prompt.classList.add("agent-plan-active");
  prompt.querySelector(".agent-repair-outcome")?.remove();
  prompt.querySelector(".agent-repair-progress")?.remove();
  const progress = node("div", { class: `agent-repair-progress ${state}`, "aria-live": "polite" },
    node("strong", { text: state === "failed" ? "Fix did not run" : "Fix running" }),
    node("small", { text }),
  );
  const actions = prompt.querySelector(".agent-plan-actions");
  if (actions) prompt.insertBefore(progress, actions);
  else prompt.append(progress);
  setAgentPlanActionsDisabled(prompt, true);
  setAgentPlanPromptStatus(prompt, state === "failed" ? "Fix failed" : "Running", state === "failed" ? "state-bad" : "state-warn");
  return prompt;
}

function appendAgentRepairOutcome(outcome, plan = null) {
  const prompt = agentPlanPromptFor(plan);
  if (!prompt) return;
  prompt.classList.add("agent-plan-active");
  prompt.querySelector(".agent-repair-progress")?.remove();
  prompt.querySelector(".agent-repair-outcome")?.remove();
  prompt.querySelector(".agent-plan-actions")?.remove();
  setAgentPlanPromptStatus(prompt, outcome?.recovered ? "Fixed" : outcome?.verified ? "Still down" : "Sent", outcome?.recovered ? "state-ok" : "state-warn");
  prompt.append(renderAgentRepairOutcome(outcome));
  focusAgentPlanPrompt(prompt);
}

function focusAgentPlanPrompt(prompt) {
  if (!(prompt instanceof HTMLElement)) return;
  prompt.setAttribute("tabindex", "-1");
  prompt.scrollIntoView({ block: "nearest", inline: "nearest", behavior: "smooth" });
  prompt.focus({ preventScroll: true });
}

function renderAgentRepairOutcome(outcome) {
  const recovered = !!outcome?.recovered;
  const verified = !!outcome?.verified;
  const before = repairStatusText(outcome?.before_status);
  const after = repairStatusText(outcome?.after_status);
  const message = cleanChatText(outcome?.message) || (verified ? "Repair verification completed." : "Repair was sent, but verification did not complete.");
  return node("div", { class: `agent-repair-outcome ${recovered ? "recovered" : verified ? "unresolved" : "unverified"}` },
    node("strong", { text: recovered ? "Recovered" : verified ? "Still needs attention" : "Verification incomplete" }),
    node("span", { text: `${before} \u2192 ${after}` }),
    node("small", { text: message }),
  );
}

function agentRepairOutcomeNotice(outcome) {
  if (String(outcome?.action || "").trim() === "start_array") {
    if (outcome?.recovered) return "Server storage started successfully.";
    if (outcome?.verified) return "Start storage ran, but server storage still is not started.";
    return "Start storage ran, but verification did not complete.";
  }
  if (outcome?.recovered) return "App action ran and the status updated.";
  if (outcome?.verified) return "App action ran, but the app still is not responding.";
  return "App action was sent, but NoobBoard could not verify the final status yet.";
}

function agentRepairOutcomeTone(outcome) {
  if (outcome?.recovered) return "info";
  if (!outcome?.verified) return "info";
  return "error";
}

function repairStatusText(status) {
  const value = String(status || "unknown").trim();
  if (!value) return "unknown";
  return value.replace(/_/g, " ");
}

function closeAgentApprovalDialog(options = {}) {
  const current = state.agentApprovalDialog;
  if (!current) return;
  if (state.agentApprovalCooldownTimer) {
    window.clearInterval(state.agentApprovalCooldownTimer);
    state.agentApprovalCooldownTimer = null;
  }
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
    showNotice(compactSurfaceErrorMessage(error, "The admin could not be notified. Try again."), "error");
  }
}

async function loadAudit() {
  if (!hasAdminSurface()) return;
  try {
    const data = await api("/api/admin/audit");
    state.auditEntries = Array.isArray(data) ? [...data].sort((a, b) => String(b.time || "").localeCompare(String(a.time || ""))) : [];
    renderActivity();
  } catch (error) {
    state.auditEntries = [];
    renderActivity(error.message);
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
    if (!options.quiet) showNotice(compactSurfaceErrorMessage(error, "Repair requests could not be loaded. Try again."), "error");
  }
}

function renderRepairRequests() {
  const output = $("repair-requests-output");
  if (!output) return;
  const requests = state.repairRequests || [];
  if (!requests.length) {
    output.replaceChildren(node("div", { class: "empty", text: "No repair requests." }));
  } else {
    output.replaceChildren(...requests.map(renderRepairRequestRow));
  }
  renderActivity();
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
      showNotice(agentRepairOutcomeNotice(result.outcome), agentRepairOutcomeTone(result.outcome));
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

/* ---------------------------------------------------------------------------
   Activity — one typed stream instead of five destinations.

   Incidents, repair requests and audit entries were three screens over the same
   underlying history, each with its own layout, and the audit table was the only
   one that was actually chronological. They are one stream here, classified into
   three kinds so a filter replaces a page:

     problem  something broke, or an action was refused or failed
     repair   something was attempted, approved, denied or executed
     change   configuration and account changes

   Rows are aligned columns, not cards: time, kind, what happened. That keeps the
   scanning density of the old audit table while making the entries readable.
   --------------------------------------------------------------------------- */
const ACTIVITY_SUBJECTS = [
  ["settings.llm.chatgpt", "ChatGPT connection", "change"],
  ["settings.roles", "Role access settings", "change"],
  ["settings.visibility", "Visibility settings", "change"],
  ["settings.blacklist", "Blacklist settings", "change"],
  ["settings.apps", "App catalogue settings", "change"],
  ["settings.llm", "LLM settings", "change"],
  ["settings.integrations", "Integration settings", "change"],
  ["settings.notifications", "Notification settings", "change"],
  ["notification.preference", "Notification preference", "change"],
  ["app.container.logs", "Container logs", "change"],
  ["app.container", "Container control", "repair"],
  ["app.icon", "App icon", "change"],
  ["infra.unraid_array", "Storage array", "repair"],
  ["llm.agent_auto_repair", "Automatic repair", "repair"],
  ["llm.agent_plan", "Repair plan", "repair"],
  ["llm.array_start", "Array start", "repair"],
  ["llm.agent_tool", "Assistant tool call", "repair"],
  ["llm.diagnosis", "Diagnosis", "change"],
  ["llm.failed", "Diagnosis", "problem"],
  ["repair_request", "Repair request", "repair"],
  ["user.repair_request", "Repair request", "repair"],
  ["user.notify_admin", "Admin notification", "change"],
  ["user.saved", "Account", "change"],
  ["auth", "Sign-in", "change"],
];

const ACTIVITY_VERBS = {
  saved: "saved",
  login: "succeeded",
  failed: "failed",
  throttled: "was throttled",
  proposed: "proposed",
  suggested: "suggested",
  approved: "approved",
  denied: "denied",
  refused: "refused",
  executed: "executed",
  verified: "verified",
  cleared: "cleared",
  connected: "connected",
  start: "started",
  action: "ran",
  logs: "read",
  action_failed: "failed to run",
  logs_failed: "failed to read",
  execute_failed: "failed to execute",
  verify_failed: "failed verification",
  rate_limited: "was rate limited",
  replay_blocked: "was blocked as a replay",
  non_executable: "was not executable",
  control_disabled: "was blocked: control disabled",
  auto_review_refused: "was refused by the safety reviewer",
  auto_reviewed: "passed the safety reviewer",
  notify_user_failed: "could not notify the user",
  notify_failed: "could not notify",
  callback_error: "failed during callback",
  array_start_suggested: "suggested starting the array",
  created: "created",
};

// A failure or a refusal is a problem regardless of which namespace it came
// from, so the suffix wins over the subject's default kind.
const ACTIVITY_PROBLEM_SUFFIXES = /(failed|refused|rate_limited|replay_blocked|non_executable|control_disabled|throttled|error)$/;

function activityKindForAction(action) {
  const name = String(action || "");
  if (ACTIVITY_PROBLEM_SUFFIXES.test(name)) return "problem";
  const match = ACTIVITY_SUBJECTS.find(([prefix]) => name === prefix || name.startsWith(`${prefix}.`));
  return match ? match[2] : "change";
}

function activityTitleForAction(action) {
  const name = String(action || "unknown");
  const match = ACTIVITY_SUBJECTS.find(([prefix]) => name === prefix || name.startsWith(`${prefix}.`));
  if (!match) return sentenceCase(name.replace(/[._]/g, " "));
  const subject = match[1];
  const suffix = name === match[0] ? "" : name.slice(match[0].length + 1);
  if (!suffix) return subject;
  const verb = ACTIVITY_VERBS[suffix] || suffix.replace(/_/g, " ");
  return `${subject} ${verb}`;
}

function activityEvents() {
  const events = [];
  for (const incident of state.snapshot?.incidents || []) {
    events.push({
      kind: "problem",
      time: incident.detected_at || incident.started_at || state.snapshot?.infrastructure?.last_checked_at || "",
      title: incident.summary || incident.type || "Incident",
      actor: "monitor",
      tone: incident.severity === "high" || incident.severity === "critical" ? "offline" : "degraded",
      meta: incident.severity || "none",
      body: () => renderIncidentCard(incident),
      search: [incident.id, incident.type, incident.summary, (incident.affected_services || []).join(" ")].join(" "),
    });
  }
  for (const request of state.repairRequests || []) {
    const outcome = request.outcome || {};
    events.push({
      kind: "repair",
      time: request.decided_at || request.created_at || "",
      title: `Repair request — ${request.app_label || request.app_id || "app"}`,
      actor: request.requester_name || "user",
      tone: request.status === "pending" ? "degraded" : outcome.recovered ? "online" : "hidden",
      meta: request.status || "pending",
      detail: outcome.message || request.resolution_note || request.diagnosis_summary || "",
      search: [request.app_id, request.app_label, request.requester_name, request.status, request.diagnosis_summary].join(" "),
    });
  }
  for (const entry of state.auditEntries || []) {
    const kind = activityKindForAction(entry.action);
    events.push({
      kind,
      time: entry.time || "",
      title: activityTitleForAction(entry.action),
      actor: entry.actor || "unknown",
      // "hidden" is the neutral/informational tone. An audit entry that simply
      // records something happening is not an unknown state.
      tone: kind === "problem" ? "offline" : "hidden",
      meta: entry.redacted ? "redacted" : "",
      details: entry.details,
      search: auditEntryText(entry),
    });
  }
  return events.sort((a, b) => String(b.time || "").localeCompare(String(a.time || "")));
}

function renderActivity(errorMessage = "") {
  const stream = $("activity-stream");
  if (!stream) return;
  const search = state.activitySearch.trim().toLowerCase();
  const events = activityEvents().filter((event) => {
    if (state.activityFilter !== "all" && event.kind !== state.activityFilter) return false;
    if (!search) return true;
    return `${event.title} ${event.actor} ${event.detail || ""} ${event.search || ""}`.toLowerCase().includes(search);
  });
  $("activity-count").textContent = `${events.length} event${events.length === 1 ? "" : "s"}`;
  if (errorMessage) {
    stream.replaceChildren(node("div", { class: "empty", text: errorMessage }));
    return;
  }
  if (!events.length) {
    stream.replaceChildren(node("div", { class: "empty", text: search || state.activityFilter !== "all" ? "No activity matches that filter." : "No activity recorded yet." }));
    return;
  }
  stream.replaceChildren(...events.map(activityRow));
}

function activityRow(event) {
  const expandable = Boolean(event.body || (event.details && Object.keys(event.details).length));
  const row = node("article", { class: `activity-row kind-${event.kind}` },
    node("time", { class: "activity-time", datetime: event.time || "", title: formatTime(event.time), text: formatStreamTime(event.time) }),
    node("span", { class: `activity-kind ${event.tone || "unknown"}` }, statusIndicator(event.kind, event.tone || "unknown", "status-dot-only")),
    node("div", { class: "activity-main" },
      node("p", { class: "activity-title", text: event.title }),
      event.detail ? node("p", { class: "activity-detail", text: event.detail }) : null,
    ),
    node("span", { class: "activity-actor", text: event.actor }),
    event.meta ? node("span", { class: "activity-meta-tag", text: event.meta }) : null,
  );
  if (!expandable) return row;
  const body = node("div", { class: "activity-body" }, event.body ? event.body() : auditDetails(event.details));
  return node("details", { class: `activity-entry kind-${event.kind}` },
    node("summary", {}, row),
    body,
  );
}

function auditEntryText(entry) {
  return [entry.time, entry.actor, entry.action, JSON.stringify(entry.details || {})].join(" ").toLowerCase();
}

function auditDetails(details) {
  const entries = Object.entries(details || {});
  if (!entries.length) return node("span", { class: "muted", text: "No details" });
  return node("div", { class: "audit-detail-chips" }, entries.map(([key, value]) => node("span", { class: "audit-chip" },
    node("strong", { text: `${key}:` }),
    node("span", { text: ` ${formatAuditValue(value)}` }),
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
    cards.push(await buildSettingsCard(item));
  }
  $("settings-grid").replaceChildren(...cards);
  setSettingsSection(state.settingsSection);
  applySettingsSearch();
}

async function buildSettingsCard(item) {
  try {
    const data = await api(item.path);
    return renderSettingsCard(item, data);
  } catch (error) {
    return node("article", { class: "settings-card settings-section", "data-settings-section": item.section }, node("h3", { text: item.title }), node("p", { class: "muted", text: error.message }));
  }
}

// Rebuild only one settings card so a save (or connector login) does not
// destroy unsaved edits sitting in the other sections.
async function reloadSettingsSection(section) {
  const item = SETTINGS_ENDPOINTS.find((entry) => entry.section === section);
  const existing = document.querySelector(`#settings-grid .settings-section[data-settings-section="${section}"]`);
  if (!item || !existing) {
    await loadSettings();
    return;
  }
  existing.replaceWith(await buildSettingsCard(item));
  setSettingsSection(state.settingsSection);
}

function settingsHasUnsavedChanges() {
  if (state.roleDirty) return true;
  return !!document.querySelector("#settings-grid .settings-footer.is-dirty");
}

function setSettingsSection(section) {
  const validSections = new Set(["roles", "advanced", ...SETTINGS_ENDPOINTS.map((item) => item.section)]);
  state.settingsSection = validSections.has(section) ? section : "roles";
  document.querySelectorAll("#settings-menu [data-settings-section]").forEach((button) => {
    button.classList.toggle("active", button.dataset.settingsSection === state.settingsSection);
  });
  document.querySelectorAll("#tab-settings .settings-section").forEach((panel) => {
    panel.hidden = panel.dataset.settingsSection !== state.settingsSection;
  });
}

/* Settings search. Settings grew ~10x past what a single scrolling form can
   carry, and the section list only helps once you already know which section a
   setting lives in. The search matches the rendered label text of every section,
   hides the sections that cannot match, and moves to the first that can. */
function settingsSectionSearchText(section) {
  const panel = document.querySelector(`#tab-settings .settings-section[data-settings-section="${section}"]`);
  const button = document.querySelector(`#settings-menu [data-settings-section="${section}"]`);
  return `${button?.textContent || ""} ${panel?.textContent || ""}`.toLowerCase();
}

function applySettingsSearch() {
  const query = state.settingsSearch.trim().toLowerCase();
  let firstMatch = "";
  document.querySelectorAll("#settings-menu [data-settings-section]").forEach((button) => {
    const section = button.dataset.settingsSection;
    const match = !query || settingsSectionSearchText(section).includes(query);
    button.hidden = !match;
    if (match && !firstMatch) firstMatch = section;
  });
  const empty = $("settings-search-empty");
  if (empty) empty.hidden = Boolean(firstMatch);
  // Never move the pane out from under an unsaved edit.
  if (query && firstMatch && firstMatch !== state.settingsSection && !settingsHasUnsavedChanges()) {
    setSettingsSection(firstMatch);
  }
  // A match hidden inside a collapsed group is not a match the reader can see.
  document.querySelectorAll("#tab-settings .settings-section").forEach((section) => {
    section.querySelectorAll(".settings-group").forEach((group, index) => {
      group.open = query ? (group.textContent || "").toLowerCase().includes(query) : index === 0;
    });
  });
}

async function loadRoleSettings() {
  try {
    const data = await api("/api/admin/settings/roles");
    state.roleVisibility = clone(data.visibility || {});
    state.roleApps = hydrateRoleApps(data.apps || []);
    state.roleUsers = data.users || [];
    state.roleUsersOriginal = clone(data.users || []);
    state.roleDirty = false;
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
        node("button", {
          type: "button",
          class: "primary command",
          "data-glyph": "v",
          onclick: saveRoleAccess,
          text: "Save role access",
        }),
      ),
    ),
    node("section", { class: "role-general-config" },
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
              state.roleDirty = true;
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
              state.roleDirty = true;
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
  const footer = node("footer", { class: "settings-footer" });
  const saveButton = save ? node("button", {
    type: "button",
    class: "primary command",
    "data-glyph": "v",
    onclick: async () => {
      const saved = await save(status);
      if (saved !== undefined) footer.classList.remove("is-dirty");
    },
    text: "Save",
  }) : null;
  if (save) {
    footer.append(status, saveButton);
    const markDirty = () => {
      footer.classList.add("is-dirty");
      status.textContent = "Unsaved changes";
      status.dataset.tone = "info";
    };
    body.addEventListener("input", markDirty);
    body.addEventListener("change", markDirty);
  }
  collapseSettingsSubsections(body);
  return node("article", { class: "settings-card settings-section", "data-settings-section": item.section },
    node("header", {},
      node("h3", { text: item.title }),
    ),
    save ? footer : null,
    body,
  );
}

/* A section like LLM still runs to a couple of thousand pixels on its own, so
   each labelled group inside it becomes a disclosure with its heading as the
   summary. You see the shape of the section immediately and open only the group
   you came for; the first group stays open so the pane never reads as empty.
   Applied here rather than in each renderer so every section behaves the same
   way and new ones get it for free. */
function collapseSettingsSubsections(body) {
  let converted = 0;
  for (const group of [...body.querySelectorAll(".settings-subsection")]) {
    // A group's heading is either a bare h4 or an h4 in a title row that also
    // carries a state or an action; either way the whole thing becomes the
    // summary, so the state stays readable while the group is closed.
    const titleRow = group.querySelector(":scope > .settings-section-title-row");
    const heading = titleRow?.querySelector(":scope > h4") || group.querySelector(":scope > h4");
    // Skip groups with no heading (nothing to put in the summary) and groups
    // already inside a disclosure, so nesting never doubles up.
    if (!heading || group.closest("details")) continue;
    // A control inside a <summary> would toggle the disclosure as well as doing
    // its own job, so a title row that carries one stays in the group body.
    const titleRowIsInert = titleRow && !titleRow.querySelector("button,input,select,textarea,a");
    const details = node("details", { class: "settings-group", open: converted === 0 });
    const summary = node("summary", {});
    group.replaceWith(details);
    if (titleRowIsInert) {
      summary.append(titleRow);
    } else {
      summary.append(node("h4", { text: heading.textContent }));
      // The heading is now in the summary, so the original has to go or the
      // group renders its own name twice.
      heading.remove();
    }
    details.append(summary, group);
    converted += 1;
  }
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
  const userRestartEnabled = settingToggle("Allow standard-user app controls", !!data?.general_user_restarts_enabled);
  const userAutoRepairEnabled = settingToggle("Allow standard-user automatic fixes", !!data?.general_user_auto_repair_enabled);
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
    const repairToggle = settingToggle("Allow admin/AI app fix", key ? !!repairAllowed[key] : false);
    const userRestartToggle = settingToggle("Allow user controls", key ? !!userRestartAllowed[key] : false);
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
      node("h4", { text: "Standard-user app controls" }),
      node("div", { class: "settings-toggle-grid" }, userRestartEnabled.element, userAutoRepairEnabled.element),
      node("p", { class: "muted", text: "When app controls are enabled, only apps with the per-app user-control toggle can show compact-view Start, Restart, and Stop buttons. Automatic fixes let standard-user diagnosis start or restart an opted-in app without an admin session." }),
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
      general_user_auto_repair_enabled: userAutoRepairEnabled.input.checked,
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
  const openAIModel = settingSelectField("OpenAI API model", knownModelValue(OPENAI_MODEL_OPTIONS, settings.openai_model, "gpt-5.6-terra"), OPENAI_MODEL_OPTIONS);
  const chatGPTModel = settingSelectField("ChatGPT account model", knownModelValue(CHATGPT_MODEL_OPTIONS, settings.openai_model, "gpt-5.6-terra"), CHATGPT_MODEL_OPTIONS);
  const anthropicModel = settingSelectField("Anthropic model", knownModelValue(ANTHROPIC_MODEL_OPTIONS, settings.anthropic_model, "claude-opus-5"), ANTHROPIC_MODEL_OPTIONS);
  const timeout = durationSecondsField("Timeout", settings.timeout || 45000000000);
  const agentControlEnabled = settingToggle("Allow admin-approved app fixes", !!settings.agent_control_enabled);
  const actionAutoReviewEnabled = settingToggle("Require reviewer before fixes", !!settings.action_auto_review_enabled);
  const actionAutoReviewModel = settingSelectField("Reviewer model", settings.action_auto_review_model || "same", actionReviewModelOptions(settings));
  const actionAutoReviewReasoning = settingSelectField("Reviewer reasoning", settings.action_auto_review_reasoning || "", [
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
    node("p", { class: "muted", text: "API-key mode uses OpenAI API model access. ChatGPT login uses the ChatGPT account connector, which has different account-scoped model support; Codex-only model IDs are not offered here and unsupported saved values fall back before a request is sent." }),
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
  const providerInactive = node("p", { class: "settings-provider-inactive", role: "status", text: "Diagnosis is inactive until a provider is selected. You can configure the options below before enabling it." });
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
    providerInactive.hidden = selectedProvider !== "disabled";
    for (const choice of authChoices) choice.element.classList.toggle("selected", choice.input.checked);
  };
  provider.input.addEventListener("change", syncLLMVisibility);
  for (const choice of authChoices) choice.input.addEventListener("change", syncLLMVisibility);
  const body = node("div", { class: "settings-form" },
    node("section", { class: "settings-subsection" },
      node("h4", { text: "Diagnosis provider" }),
      node("div", { class: "settings-field-grid" }, provider.element),
      node("div", { class: "settings-toggle-grid" }, enabled.element),
      providerInactive,
    ),
    openAISection,
    anthropicSection,
    renderLLMAgentReadiness(settings.agent_readiness || {}, {
      controls: [agentControlEnabled.element],
    }),
    node("section", { class: "settings-subsection" },
      node("h4", { text: "Safety reviewer" }),
      node("div", { class: "settings-toggle-grid" }, actionAutoReviewEnabled.element),
      node("div", { class: "settings-field-grid" }, actionAutoReviewModel.element, actionAutoReviewReasoning.element),
      actionAutoReviewReferences.element,
      node("p", { class: "muted", text: "When enabled, NoobBoard asks the selected reviewer model to check the proposed fix against these local reference docs before any approved app action runs." }),
    ),
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
      agent_auto_repair_enabled: actionAutoReviewEnabled.input.checked,
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
  const openAIModel = knownModelValue(OPENAI_MODEL_OPTIONS, settings.openai_model, "gpt-5.6-terra");
  const chatGPTModel = knownModelValue(CHATGPT_MODEL_OPTIONS, settings.openai_model, "gpt-5.6-terra");
  const anthropicModel = knownModelValue(ANTHROPIC_MODEL_OPTIONS, settings.anthropic_model, "claude-opus-5");
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
      ? CHATGPT_MODEL_OPTIONS
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
  return node("section", { class: "settings-subsection agent-readiness" },
    node("div", { class: "settings-section-title-row" },
      node("h4", { text: "Admin app fixes" }),
      settingsStatePill(readiness.mutating_tools_available ? "available" : "locked", readiness.mutating_tools_available ? "Fix path available" : "Fix path locked"),
    ),
    options.controls?.length ? node("div", { class: "settings-field-grid" }, options.controls) : null,
    node("div", { class: "settings-status-list" },
      settingsStatusRow("Read-only live tools", activeText, readiness.admin_tools_enabled ? "available" : "locked", readOnlyNames || "No read-only tools are registered."),
      settingsStatusRow("Approval popup", readiness.agent_control_enabled ? "Ready" : "Off", readiness.agent_control_enabled ? "available" : "locked", readiness.agent_control_enabled ? agentRepairLimitDetail(readiness) : "Turn on admin-approved app fixes before chat can ask to change an app."),
      settingsStatusRow("Safety reviewer", autoReview.enabled ? "Available" : "Off", autoReview.enabled ? (autoReview.status || "available") : "locked", autoReviewDetail(reference)),
      settingsStatusRow("Chat auto-fix", autoAction.enabled ? agentModeStatusText(autoAction.status) : "Off", autoAction.enabled ? (autoAction.status || "available") : "locked", autoActionDetail(autoAction, readiness)),
    ),
    node("p", { class: "muted agent-reference-note", text: reference.design_finding || "Future repair actions require schema validation, audit policy, and explicit approval." }),
  );
}

function autoActionDetail(autoAction, readiness) {
  if (!autoAction?.enabled) return "Off. Diagnosis will propose a fix and use the approval popup.";
  if (!readiness?.agent_control_enabled) return "Turn on admin-approved fixes first.";
  const status = String(autoAction.status || "").toLowerCase();
  if (status === "review_required") return "Requires the safety reviewer so a separate model can veto the restart.";
  if (status === "available") return "Available only when a chat auto-fix toggle is turned on for that question. Only non-online opted-in apps can be restarted automatically.";
  return "Turn on the safety reviewer before diagnosis can run an automatic restart.";
}

function autoReviewDetail(reference) {
  if (!reference?.enabled) return "Optional reviewer gate is off; admin approval still uses the normal popup.";
  const model = reference.model || "same";
  const count = Number(reference.reference_count || 0);
  const refText = count === 1 ? "1 reference" : `${count} references`;
  const reasoning = reference.reasoning ? `, ${reference.reasoning} reasoning` : "";
  return `Reviewer: ${model}${reasoning}; ${refText} configured. Fails closed before any approved app action runs.`;
}

function agentRepairLimitDetail(readiness) {
  const cooldownSeconds = durationToSeconds(readiness.repair_cooldown) || 60;
  const windowSeconds = durationToSeconds(readiness.repair_rate_limit_window) || 3600;
  const max = Number(readiness.repair_rate_limit_max || 5);
  return `Only opted-in apps can be changed. Limit: 1 per app every ${formatSeconds(cooldownSeconds)}, ${max} total per ${formatSeconds(windowSeconds)}.`;
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
      return "Not enabled";
    case "armed":
      return "Enabled";
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
      await reloadSettingsSection("llm");
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
      await reloadSettingsSection("llm");
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

// The value is seconds and the field gave no unit, so "900" was unreadable
// without knowing the wire format. The unit belongs in the label.
function durationSecondsField(label, duration) {
  return settingNumberField(`${label} (seconds)`, durationToSeconds(duration), { min: 0, step: 1, inputmode: "numeric" });
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
  return String(name || "").replaceAll("_", " ").replace(/\b\w/g, (letter) => letter.toUpperCase());
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
    const section = SETTINGS_ENDPOINTS.find((entry) => entry.path === path)?.section;
    await reloadSettingsSection(section);
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

// Timestamps in a scanning column: drop the date for today's entries so the
// column stays narrow and the numerals line up.
function formatStreamTime(value) {
  if (!value) return "—";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return String(value);
  const time = date.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", hour12: false });
  const today = new Date();
  const sameDay = date.getFullYear() === today.getFullYear() && date.getMonth() === today.getMonth() && date.getDate() === today.getDate();
  if (sameDay) return time;
  return `${date.toLocaleDateString([], { day: "2-digit", month: "short" })} ${time}`;
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
$("user-ask-primary").addEventListener("click", focusUserChat);
$("user-chat-send").addEventListener("click", runUserChat);
$("user-chat-input").addEventListener("keydown", (event) => submitOnEnter(event, runUserChat));
$("user-notify-admin").addEventListener("click", () => notifyAdmin("A standard user reported a problem."));
$("user-app-detail-back").addEventListener("click", closeCompactDetail);
$("user-infra-detail-back").addEventListener("click", closeCompactDetail);
$("activity-refresh").addEventListener("click", () => {
  loadAudit();
  loadRepairRequests();
});
$("repair-requests-refresh").addEventListener("click", loadRepairRequests);
$("activity-search").addEventListener("input", (event) => {
  state.activitySearch = event.target.value || "";
  renderActivity();
});
$("activity-filters").addEventListener("click", (event) => {
  const button = event.target.closest("[data-activity-filter]");
  if (!button) return;
  state.activityFilter = button.dataset.activityFilter;
  document.querySelectorAll("#activity-filters button").forEach((entry) => {
    entry.classList.toggle("active", entry === button);
  });
  renderActivity();
});
$("settings-search").addEventListener("input", (event) => {
  state.settingsSearch = event.target.value || "";
  applySettingsSearch();
});
$("settings-refresh").addEventListener("click", () => {
  if (settingsHasUnsavedChanges() && !confirm("Discard unsaved settings changes?")) return;
  state.roleDirty = false;
  loadSettings();
});
$("settings-menu").addEventListener("click", (event) => {
  const button = event.target.closest("[data-settings-section]");
  if (button) setSettingsSection(button.dataset.settingsSection);
});
// Role Access edits live in state (not a settings card), so track dirtiness here
// for the reload guards; loadRoleSettings/save clear it on a fresh fetch.
$("role-settings").addEventListener("input", () => { state.roleDirty = true; });
$("role-settings").addEventListener("change", () => { state.roleDirty = true; });
window.addEventListener("beforeunload", (event) => {
  if (!hasAdminSurface() || !settingsHasUnsavedChanges()) return;
  event.preventDefault();
  event.returnValue = "";
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
