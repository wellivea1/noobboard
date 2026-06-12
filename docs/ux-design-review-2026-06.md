# GUI design review — June 2026

Full-pass review of both surfaces using the `cmd/visualcheck` screenshot set
(26 captures, harness green). Findings are ordered by priority; each has a
file pointer and an acceptance check. The harness's automated gates all pass —
everything below is about visual quality, consistency, and affordance, the
things the gates don't measure.

Screens reviewed: desktop overview / apps / diagnostics+repair / queue /
settings(LLM), mobile admin overview / settings, compact home / chat(keyboard) /
app detail / infra detail / drawer, compact-on-desktop app detail.

---

## P1 — broken or visibly wrong

### 1. Compact app-detail action button wraps mid-word at desktop width
On `desktopUserAppDetail` (compact shell centered at desktop width), the
**Restart** button renders as "Resta / rt" — a mid-word wrap, same failure class
as the old "As / k" send-button bug. At mobile width the same row renders fine,
so the action-row sizing breaks somewhere between widths.

- Fix: `white-space: nowrap` + a sane `min-width` on the app-action row buttons
  (`.user-detail-body` action row in `web/public/styles.css`), and let the row
  wrap whole buttons to a second line if ever needed (`flex-wrap: wrap`).
- Accept: no mid-word wrap at any width 360–1440px; add a
  `buttonTextOverflow`-style harness assert on the detail action row at desktop
  width (the existing check apparently doesn't cover this view/width combo).

### 2. Toasts overlap the topbar title
Two captures show the floating notice covering header content:
- `desktopUserAppDetail`: a "Not Found" toast sits on top of the page title
  ("…mby" is occluded).
- `desktopAgentRepair`: "App action ran and the status updated." overlaps the
  NOOBBOARD eyebrow.

- Fix: give `#notice` a fixed, non-overlapping slot — below the topbar (push
  content) or anchored top-right with a margin clear of the title block. One
  rule for both surfaces.
- Accept: toast never intersects the title block at 390/768/1440px; harness
  bounds-overlap check extended to `#notice` vs `.title-block`.

### 3. Raw error text in user-facing toast
The compact surface showed a bare **"Not Found"** toast (API 404 surfaced
verbatim). General users should never see transport-level wording.

- Fix: map API errors to plain copy at the `notice()` call sites in
  `web/public/app.js` (e.g. "That app isn't available anymore."). Keep raw
  detail in console/audit only.
- Accept: no HTTP-status words (`Not Found`, `Forbidden`, `Conflict`, …) in any
  compact-surface toast; add to the banned-term list for the compact audit.

### 4. Two side-nav items can look active at once
On `desktopQueue`, **Diagnostics** still shows the active accent bar while
**Review Queue** is the open tab (also highlighted). Either the `active` class
isn't cleared on programmatic navigation or a stale `:focus-visible` style
mimics it.

- Fix: verify single-active in `setTab`/nav click handling in `app.js`; make
  focus style visually distinct from active (ring vs filled).
- Accept: exactly one `.tabs button.active` after any navigation path.

### 5. Stray empty row in the compact drawer
`mobileUserDrawer`: between the notification toggle and the ACCOUNT section
there's an empty gap bounded by two hairlines — an empty container still
rendering its borders.

- Fix: hide the container when it has no rows (`:empty` rule or conditional
  render in `app.js`).
- Accept: no double hairline / blank band in the drawer at any state.

### 6. Partial-width divider under "Auto-fix if safe"
`mobileUserChatKeyboard`: the hairline under the auto-fix checkbox stops
mid-panel instead of spanning like the other separators.

- Fix: make the divider span the composer width (or remove it — the checkbox
  row reads fine without it).

---

## P2 — consistency and affordance

### 7. Mobile admin topbar actions are bare, scattered glyphs
`mobileOverview` (admin): Refresh/Diagnostics/Sign-out collapse to borderless
`↺ ? ✕` glyphs with uneven gaps. They've lost button chrome entirely, the
spacing looks accidental, and **✕ reads as "close", not "sign out"** — a
destructive-feeling mystery button.

- Fix: keep the bordered `.command` chrome at mobile width with even `gap`;
  consider keeping short labels ("Refresh", "Sign out") since there's room, or
  at minimum swap sign-out to a distinct glyph and keep `aria-label`s.
- Accept: topbar actions visually consistent with desktop, evenly spaced, no
  bare ✕ for sign-out.

### 8. App-detail action row: three buttons, three different greys
`mobileUserAppDetail`: Start (light, enabled), Restart (mid-grey), Stop (dark)
— three distinct visual weights where only enabled/disabled exist. Mid-grey
Restart looks half-enabled.

- Fix: one shared disabled treatment (same bg + reduced opacity). Better:
  don't show non-applicable actions at all — a stopped app shows **Start** only,
  a running app shows **Restart / Stop** (matches the server rule that stopped
  apps must use Start).
- Accept: at most one visual style for disabled; or non-applicable actions
  hidden.

### 9. Desktop apps inventory: Docker controls are unlabeled bare icons
`desktopApps`: per-app controls render as tiny `▶ ↺ ■` glyphs with no button
boundary, no labels/tooltips, faint disabled distinction — inconsistent with
the adjacent bordered "Image" button, and small targets even for mouse use.

- Fix: same `.command` chrome as everywhere else (border + glyph + short label
  or `title` tooltip); one disabled treatment.

### 10. Settings: two different toggle paradigms on one page
`desktopSettings` (LLM): top half uses full-width checkbox rows ("Use LLM
diagnosis", "Allow admin-approved app fixes"); the WHO CAN ASK section uses
right-aligned "Enabled" checkbox chips. Same meaning, two looks.

- Fix: standardize on one pattern (the right-aligned chip reads better in
  scannable lists; the full-width row works for top-level switches — pick one
  and apply to both groups).

### 11. Status pills should state, not alarm
"Chat auto-fix — Off…" carries an orange **NEEDS REVIEW** pill; reads as an
alert when the real state is just "Off". Pills should describe state
(`On` / `Off` / `Ready`), with warn colors reserved for misconfiguration.

### 12. Contradictory LLM controls visible simultaneously
Provider = **Disabled** while "Use LLM diagnosis" is checked and the whole
fixes/reviewer stack below is fully interactive. Dim/disable dependent controls
when the provider is disabled, with one line of copy ("Select a provider to
enable diagnosis features").

### 13. Disclosures have no affordance
"Technical details" (compact home), "Advanced context and redaction",
"Advanced request timing" (settings) render as plain bold text/boxes — nothing
signals tappable. Add a chevron marker (rotate on open) to all
`details > summary` styles.

### 14. Live Status metric pairs are ragged
Overview rows: `Incidents 1   Facts 1`, `Total 1  Offline 1  Degraded 0`,
`● Array  started` — uneven gaps, and the lowercase `started` value reads like
a stray word. Render each pair as a small label:value chip (or right-aligned
tabular columns), and pill-style the array state (`Started`).

### 15. Status indicator shape varies without meaning
OVERALL/APPS use red squares; SERVER/ROUTER and all inline chips use dots. If
square ≠ dot carries no semantics, use dots everywhere; if it does (aggregate
vs check), apply it consistently (SERVER/ROUTER are aggregates too).

### 16. Post-fix "Open approval" ghost button
`desktopAgentRepair`: after a successful auto-fix, a disabled "Open approval"
button (with the `?` diagnose glyph) lingers above the Recovered card. Hide it
once the action has executed; give approval its own glyph (`!` or `✓`), not the
diagnostics `?`.

### 17. Duplicate titles in detail views
Compact detail pages repeat the title: topbar says "Emby" / "Internet details"
and the body header repeats it immediately below. Keep the body header (it has
the icon + status pill) and set the topbar to a generic "Details", or drop the
body duplicate.

---

## P3 — polish

- **18.** "Working 100.0% of the time." → trim trailing `.0`; consider
  friendlier zero-case ("Hasn't worked in the last 7 days") vs "Working 0% of
  the time."
- **19.** Outcome card uses ASCII `offline -> online`; use `→` like the button
  glyphs.
- **20.** Settings page is ~2,200px tall with a single small Save at the
  bottom — make the Save row sticky (with dirty-state indicator), or save per
  section.
- **21.** "notification message" policy card title is lowercase while siblings
  are Title Case — capitalize display names for custom policies.
- **22.** Incident line "2026-06-12-001 - Affected: emby - Emby container
  exited." is a dash-run-on; lay out ID / affected / detail as separate muted
  spans.
- **23.** Compact hero icon is static grey; tinting it with the status accent
  (orange/red/green) would tie the card together. Optional.

---

## Suggested PR slices

1. **P1 fixes** (#1–#6): CSS + small JS; add the two new harness asserts
   (detail-row overflow at desktop width, toast/title intersection).
2. **Button chrome pass** (#7–#9): topbar mobile actions, app-detail
   action row, apps-inventory controls — one consistent `.command` treatment +
   disabled rule.
3. **Settings coherence** (#10–#13, #16, #20–#21).
4. **Status display polish** (#14–#15, #17–#19, #22–#23).

Each slice keeps `go test ./...` + `cmd/visualcheck` green; slices 1–2 are the
visible-quality wins and should land first.
