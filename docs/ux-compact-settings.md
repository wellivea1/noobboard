# Compact settings drawer spec

An implementation spec for Codex: add a **menu (hamburger) → slide-out side panel** to the
compact / general-user surface (port `8788`, `body.compact-view`), housing a **minimal
settings panel**. The drawer is built as an **extensible container** so more destinations
(Help, About, Appearance, …) can be added later without re-architecting.

This is Workstream B (compact UX) follow-on; it sits on top of the redesigned compact home
(`docs/ux-compact.md`). Read that and `AGENTS.md` first.

## How to use this spec (implementer)

- Implement exactly what's below. UI/presentation only — **freeze backend scope**. Every API
  this needs already exists (`/api/user/notification-preferences`, `/api/auth/me`,
  `/api/auth/logout`); do not add or change endpoints.
- Respect `AGENTS.md` guardrails: the compact router never gets `/api/admin/*`; **no admin
  controls, no technical vocabulary, no source/role/debug pills** in the drawer; mobile-first;
  ≥44px touch targets; no horizontal scroll; works at 390×844.
- Ship 390×844 screenshots of the drawer (closed trigger, open, and the notifications list)
  and pass the harness additions in §8.

## 1. Why / what

The compact surface currently has no settings. Non-technical household members need a tiny,
obvious place to manage **what they get notified about** and to **sign out** — without ever
seeing admin machinery. A hamburger that opens a side panel is the familiar mobile pattern and
gives us a home for future general-user destinations.

Do **not** reuse the admin nav: the existing `#nav-toggle` + `#tabs` (`.side-nav`) are
`data-admin-detail` and open admin sections. Build a **separate** compact drawer so the two
surfaces stay independent (guardrail). Mirror the *mechanics* (`openNav`/`closeNav`,
`body.nav-open`, `#nav-backdrop`, the `.side-nav` slide CSS) — don't entangle them.

## 2. Trigger (hamburger)

- Add a dedicated button `#user-menu-toggle` (class `command nav-toggle`, glyph `☰`,
  `aria-label="Menu"`, `aria-haspopup="dialog"`, `aria-controls="user-drawer"`,
  `aria-expanded="false"`), visible **only on the compact surface**. It must not carry
  `data-admin-detail` (those are hidden for general users); instead show it via
  `body.compact-view #user-menu-toggle { display:flex }` and keep it hidden in admin view.
- Place it as the **first** item in the compact header row (leading, left-aligned), ≥44×44.
- Recommended header cleanup: move **Sign out** out of the header into the drawer (Account
  section, §5) so the compact header is just `[☰ Menu]  Home status … [Check again]`. Keep
  "Check again" in the header (it's a primary status action).

## 3. The drawer

- Add `#user-drawer` and `#user-drawer-backdrop` near the existing `#nav-backdrop` in the
  compact DOM. Keep them dedicated to the compact surface (don't reuse `#tabs`).
- **Element semantics:** `#user-drawer` is `role="dialog"`, `aria-modal="true"`, labelled by
  its `<h2>` (`aria-labelledby`). It is `hidden` (and not focusable) when closed.
- **Placement / size:** slides in from the **left** (matches the leading hamburger). Width
  `min(86vw, 360px)`, full height, with safe-area insets (`padding-top: var(--safe-top)` etc.).
  Content area scrolls vertically; the drawer itself never causes body horizontal scroll.
- **Scrim:** `#user-drawer-backdrop` is a full-viewport dimmed overlay behind the drawer;
  clicking it closes the drawer.
- **Animation:** transform `translateX(-100%)`→`0` over ~200ms ease; scrim fades opacity.
  Honor `@media (prefers-reduced-motion: reduce)` — no transform/opacity transition.
- **Open/close behavior** (new `openUserMenu()` / `closeUserMenu()`, mirroring openNav/closeNav):
  - Open: `body.classList.add("user-menu-open")`, set `aria-expanded="true"`, unhide drawer +
    scrim, **lock body scroll** while open, move focus to the drawer's close button.
  - Close on: the drawer's ✕ button, scrim click, **Escape** key, and after a destructive/
    navigating action (e.g. Sign out). On close: restore `aria-expanded="false"`, hide drawer +
    scrim, unlock scroll, and **return focus to `#user-menu-toggle`**.
  - **Focus trap:** while open, Tab/Shift+Tab cycle within the drawer; the rest of the page is
    `inert` (or `aria-hidden="true"`).

## 4. Drawer structure (extensible)

Build the drawer body from a JS array so adding a destination later is one entry — this is the
core of "more tabs can be added later":

```js
// each destination renders its own content into the provided container
const compactDrawerSections = [
  { id: "settings", label: "Settings", glyph: "⚙", render: renderCompactSettings },
  // future: { id: "help", label: "Help", glyph: "?", render: renderCompactHelp },
  // future: { id: "about", label: "About", glyph: "i", render: renderCompactAbout },
];
```

Drawer layout:

- **Header:** drawer title (`<h2 id="user-drawer-title">`) + a ✕ close button (≥44×44,
  `aria-label="Close menu"`).
- **Destination nav:** render a vertical list of buttons from `compactDrawerSections` (label +
  glyph), each ≥44px. **When the array has only one destination, render that destination's
  content directly and omit the nav chrome** (no empty tab bar). When there are ≥2, show the
  list and a content area; selecting an item swaps the content (master/detail within the
  drawer) and updates an `aria-current`/active state.
- **Content area:** the active destination's `render()` output.

Keep the destination contract small and pure (`render(container)` populates a node); the open
logic just calls the active section's render. Don't hardcode "settings" anywhere the array
isn't consulted.

## 5. Minimal initial content — the **Settings** destination (`renderCompactSettings`)

Two plain-language groups, each a `<section>` with an `<h3>`:

### 5a. Notifications
- Intro line: "Get a heads-up when something stops working."
- A **per-app toggle list**, one row per app currently visible to this user (use the same app
  list the compact home shows). Each row: app icon + name + a switch.
  - Label the switch in plain language: **"Tell me if {App} has a problem"**.
  - On load, fetch `GET /api/user/notification-preferences` and reflect current state.
  - On change, `POST /api/user/notification-preferences` with
    `{ app_id, notify_on_down: <on>, notify_on_recovery: <on> }` (one switch controls both, for
    minimalism). Optimistic update + revert with an inline error on failure.
  - Empty state (no visible apps): "No apps to notify about yet."
- Switches are ≥44px tall, have an accessible label tied to the app, and a visible on/off state
  (not color alone — include an on/off text or knob position).

### 5b. Account
- "Signed in as **{display name}**" (from `state.user` / `GET /api/auth/me`; never show role
  pills or technical identifiers).
- A full-width **Sign out** button (calls the existing `logout()`), labelled with text (not
  icon-only). This is the relocated header action.

That's the whole minimal panel. **No** theme/log/debug/admin/diagnostic options here.

### Future destinations (design later, not now)
List them as the rationale for the extensible array, but do not build:
- **Appearance** (text size / higher-contrast), **Help** ("What is this?" + how to read
  statuses), **About** (app name + version). Each would be one more entry in
  `compactDrawerSections`.

## 6. Microcopy (use verbatim; plain language, no jargon)

- Trigger: `Menu`
- Drawer title: `Menu`  (when a single destination, you may title it `Settings`)
- Close: `Close menu`
- Notifications heading: `Notifications`
- Notifications intro: `Get a heads-up when something stops working.`
- Per-app switch: `Tell me if {App} has a problem`
- Notifications empty: `No apps to notify about yet.`
- Account heading: `Account`
- Account identity: `Signed in as {display name}`
- Sign out button: `Sign out`
- Save error (inline): `Couldn't save that — try again.`

Never use: notify_on_down, container, push, webhook, ntfy, API, role, etc.

## 7. Visual design guidelines

- **Tokens:** reuse existing CSS variables — `--surface`, `--surface-alt`, `--border-soft`,
  `--text`, `--muted`, `--space-*`, the standard radius and focus-ring. The drawer should feel
  like the same app, not a new theme.
- **Surfaces:** drawer background `--surface`; a 1px `--border-soft` trailing edge + a soft
  shadow to lift it over the scrim. Scrim ~`rgba(0,0,0,0.5)`.
- **Header:** title left, ✕ right, both vertically centered, on a sticky drawer header so it
  stays visible while content scrolls.
- **Section headings (`h3`):** uppercase, `--muted`, the same treatment used elsewhere in the
  compact view; generous spacing between groups.
- **Switch/toggle:** large (≥44px row), label on the left, switch on the right; clear on/off;
  visible `:focus-visible` ring. (You may reuse the compact app's existing toggle styling if it
  meets the size/contrast bar; otherwise add a `.user-switch`.)
- **Sign out:** full-width secondary/destructive-styled button, ≥44px.
- **Spacing/scroll:** comfortable padding (`--space-4`); the content area scrolls; the header
  and (future) destination nav stay put. No horizontal scroll at any width.
- **Desktop:** the compact surface can also be opened on a desktop browser — the drawer caps at
  360px and overlays from the left with the scrim; it does not stretch full-width.

## 8. Accessibility checklist

- [ ] Trigger has `aria-label`, `aria-haspopup="dialog"`, `aria-controls`, and toggles
      `aria-expanded`.
- [ ] Drawer is `role="dialog"` `aria-modal="true"` with `aria-labelledby` its title.
- [ ] Focus moves into the drawer on open and is **trapped**; rest of page `inert`/aria-hidden.
- [ ] **Escape** closes; **scrim click** closes; focus **returns to the trigger** on close.
- [ ] Body scroll is locked while open.
- [ ] All controls ≥44×44; visible focus rings; state conveyed by more than color.
- [ ] `prefers-reduced-motion` disables the slide/fade.
- [ ] Switch labels name the app; the identity line and headings are readable by SR.

## 9. Acceptance criteria (testable)

- [ ] On the compact surface a hamburger appears in the header and is **absent** on the admin
      surface; the admin nav is unchanged.
- [ ] Activating it opens a left side panel over a scrim; Escape, scrim click, and ✕ all close
      it; focus is trapped while open and returns to the trigger on close.
- [ ] The drawer contains **only** Settings (Notifications + Account) — no admin controls, no
      technical terms, no source/role pills.
- [ ] Notification toggles load current state via GET and persist via POST; toggling reflects
      immediately and survives reopening the drawer.
- [ ] Sign out works from the drawer.
- [ ] Adding a second entry to `compactDrawerSections` renders a destination list with no other
      code changes (verify with a temporary throwaway entry, then remove it).
- [ ] No horizontal scroll; all controls ≥44px at 390×844; works on desktop (capped width).
- [ ] `go test ./...`, build, and `visual-check.ps1` (incl. §8/§9 harness additions) pass; PR
      includes the required screenshots.

## 10. Visual-check (`cmd/visualcheck`) additions

Extend the general-user/mobile path:

- `userMenuToggleVisible` — the compact hamburger is present/visible; assert it's **absent** in
  the admin runs.
- Drive open: click `#user-menu-toggle`, then assert `userDrawerOpen` (drawer visible,
  `aria-expanded=true`), `drawerBannedTermCount == 0` (reuse the §5 banned-term scan over the
  drawer), `drawerSmallTouchTargetCount == 0`, and no admin controls in the drawer
  (`#user-drawer [data-admin-only], #user-drawer [data-admin-detail]` count == 0).
- Assert the notifications list renders a row per visible app (or the empty state) and the
  Account section shows a Sign out control.
- Drive close (Escape) and assert focus returned to `#user-menu-toggle` and the drawer is
  hidden + body scroll unlocked.
- Capture screenshots: compact home with the hamburger, drawer open, notifications list.

## 11. Implementation pointers

- `web/public/index.html` — add `#user-menu-toggle` in the compact header; add `#user-drawer`
  (role=dialog) + `#user-drawer-backdrop`. Consider moving the header `Sign out` into the
  drawer.
- `web/public/app.js` — `openUserMenu()`/`closeUserMenu()` (mirror `openNav`/`closeNav` at
  ~L172), Escape + scrim handlers, focus trap + restore; the `compactDrawerSections` array and
  a `renderUserDrawer()`; `renderCompactSettings()` with the notifications list
  (reuse/extend `savePreference` at ~L1302 and add a GET-load) and the Account block (reuse
  `logout()` at ~L2468, `state.user`). Gate the hamburger via `body.compact-view` in
  `showDashboard()` (~L321) — do not give it `data-admin-detail`.
- `web/public/styles.css` — `.user-drawer`, `.user-drawer-backdrop`, the slide/scrim animation
  (mirror the existing `.side-nav` + `body.nav-open` rules), `.user-switch`, reduced-motion.

## 12. Out of scope

Backend/API changes, admin nav, theme system, anything that needs new endpoints. If a future
destination seems to need backend work, raise it rather than expanding this ticket.
