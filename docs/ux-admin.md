# Admin panel UX review

A review of the **admin surface** (port `8787`) for an implementer agent (Codex). Unlike the
compact surface (`docs/ux-compact.md`), the admin panel is for the technical owner and is
*allowed* to be dense and use technical vocabulary. So this is a **targeted-improvement
spec, not a ground-up redesign** — the global frame is good; fix the specific issues below.

## How to use this spec (implementer)

- The admin panel is largely solid. Work the findings in priority order (P1 → P3), one at a
  time, each with before/after admin screenshots (desktop 1440-wide and mobile 390-wide).
- This is UI/presentation only. **Freeze backend scope** (AGENTS.md) — the data already
  exists on the snapshot/audit APIs; the job is presentation.
- Respect the guardrails in `AGENTS.md` (two ports/routers, admin-only mutations stay gated,
  redaction). Nothing here relaxes them.

## How this was reviewed

Captured every admin tab at 1440×1000 (fixture + live): Overview, Server, Router, Apps,
Incidents, Diagnostics, Admin, Settings (Role Access + LLM), plus mobile (390×844) via
`cmd/visualcheck` and the Chrome extension against a fixture instance.

## What's working (preserve)

- Consistent global frame: eyebrow (`NOOBBOARD`) + large page title + status pills +
  top-right actions (Refresh / Diagnose / Sign out) + left `SECTIONS` nav.
- **Source honesty**: the `Fixture Data` pill, the Server "Collectors" card, and per-app
  `SOURCE` lines make fixture-vs-live obvious.
- **Structured settings** (Role Access, Visibility, Blacklist, App Images, LLM, Integrations,
  Notifications) — no raw JSON editors; API keys are **write-only** with "Clear saved key".
- Nav active-state correctly tracks the current section; consistent status-dot + label rows
  and severity badges.

## Findings

### P1

1. **Floating "Ask me for help!" chat overlaps interactive content.** It's docked bottom-right
   on every page and, on **Settings → Role Access**, covers the "App access" controls
   (Show all / Hide all + the app rows). Fix: dock it so it never overlaps interactive
   controls (reserve bottom padding / safe area), or make it a collapsible launcher that
   expands over empty space. It also renders when diagnostics are unavailable — hide or
   disable it when there's no LLM provider.
2. **Three overlapping LLM/diagnosis entry points.** (a) top-right **Diagnose** button,
   (b) the floating **Ask me for help!** chat on every page, and (c) the **Diagnostics** tab
   ("Ask For A Diagnosis" / "Run diagnosis"). Plus **Notify admin** appears in several places.
   Consolidate to one primary diagnosis surface (the Diagnostics tab) + at most the persistent
   chat launcher; make the top-right "Diagnose" simply jump to Diagnostics rather than being a
   third path.
3. **Admin workspace is raw JSON.** The Admin tab is two unformatted JSON dumps — **Audit Log**
   and **Raw Admin Snapshot**. The audit log is the important one and is currently unreadable.
   Render it as a **structured, filterable table** (Time, Actor, Action, Details, Redacted)
   with newest-first ordering and a text filter. Keep "Raw Admin Snapshot" but relabel it a
   **Developer / debug** disclosure, collapsed by default.

### P2

4. **The overall-status line is repeated as a subtitle on every tab.** Pages like Settings,
   Admin, and Diagnostics show "Emby is offline." / "Emby cannot be checked because Docker is
   unavailable." under their titles, where it's irrelevant noise. Make the page subtitle
   contextual to the page (or omit it), and keep overall status to the Overview + the existing
   top-right status pill.
5. **Mobile top-bar crowding/truncation.** At 390px the header truncates the title ("Runtime"…)
   and the `Fixture Data` pill ("…icture Data"), and a transient toast competes for the same
   row. Tighten the mobile header: collapse pills into the menu or a compact indicator, allow
   the title to wrap or shrink, and don't let toasts overlap the header.
6. **Settings has two stacked vertical navs.** The main `SECTIONS` sidebar plus a second
   vertical settings submenu (Role Access / Visibility / …) styled similarly read as nested
   sidebars. Differentiate them — e.g., render the settings submenu as horizontal segmented
   tabs or add a clear "Settings ›" breadcrumb/heading — so the hierarchy is obvious.
7. **LLM settings: three repeated policy blocks with weak grouping.** The LLM section repeats
   ~9 identically-labeled controls (Enabled, Include logs, Prefer facts, Hidden app names,
   Blacklisted names, Fail closed, Max context bytes, Max log lines, Allowed log sources) three
   times (one per policy) without prominent per-policy headings. Add a clear name + one-line
   purpose per policy (admin-requested / general-user-requested / automatic-incident) and
   collapse advanced policy tuning behind progressive disclosure. (Aligns with Workstream C
   Part 1.)

### P3

8. **Status-row value alignment is inconsistent.** On Server health some rows show a
   right-aligned value (ARRAY → STARTED) while NAS / UNRAID API / DOCKER show none. Standardize:
   always show a state value, or none, and right-align consistently.
9. **App-control buttons are icon-only.** The Apps tab ▶ / ↻ / ■ controls rely on tooltips; add
   `aria-label`s, and add a confirm step for the destructive Stop/Restart (admin-only, audited
   actions). The metadata row (DOCKER / IMAGE / SOURCE) is fine for admin.
10. **Incident evidence is raw key=value.** "container=emby docker=unknown health=unknown
    endpoint=unknown" is acceptable for admin but would scan better as labeled chips.
11. **Vertical-space balance.** Server / Apps / Incidents / Diagnostics leave ~half the viewport
    empty under a small top block. Constrain content width consistently and/or use the space
    (recent activity, quick actions) rather than leaving large gaps.

## Acceptance criteria (testable)

- [ ] The chat launcher never overlaps interactive controls at 1440 or 390 width (bounds
      check); it is hidden/disabled when no LLM provider is configured.
- [ ] Exactly one primary diagnosis surface; the top-right Diagnose navigates to it rather than
      duplicating it.
- [ ] The Admin tab audit log renders as a table (rows, sortable/filterable), not a `<pre>` JSON
      blob; the raw snapshot is a collapsed "debug" disclosure.
- [ ] Page subtitles are contextual; the overall-status string does not appear on Settings /
      Admin / Diagnostics.
- [ ] Mobile header (390px) shows no truncated title/pill and no toast/header overlap; no
      horizontal overflow.
- [ ] App-control buttons have accessible labels; Stop/Restart confirm before acting.
- [ ] Admin panel remains functional for all tabs; `go test ./...`, build, and
      `visual-check.ps1` pass; PR includes desktop + mobile screenshots of changed tabs.

## Visual-check (`cmd/visualcheck`) additions

The harness covers admin overview/server/router/apps/settings + mobile. Extend it to also
capture and assert the gaps:

- Capture **Incidents**, **Diagnostics**, and **Admin** tabs (desktop + mobile).
- Assert the audit log is a table (e.g. `#audit-log table tbody tr` count > 0) rather than a
  `<pre>`/`<code>` JSON node.
- Assert the chat launcher's bounding box does not intersect any visible interactive control
  (reuse the existing `componentBoundsOverflow` machinery).
- Assert page subtitle != overall-status string on settings/admin.
- Keep the existing small-touch-target / overflow assertions (now coarse-pointer aware).

## Implementation pointers

- `web/public/index.html` — admin tab containers, the Admin workspace audit/snapshot panels,
  the top-bar (title + pills + actions), the floating assistant/chat panel.
- `web/public/app.js` — per-tab render functions (overview/server/router/apps/incidents/
  diagnostics/admin/settings), the `$("summary")` page-subtitle assignment, the audit-log
  renderer (turn the JSON dump into a table), the settings submenu, the assistant/chat panel
  wiring, and the LLM settings section (policy grouping/labels).
- `web/public/styles.css` — chat launcher docking/z-index, mobile header layout, settings
  submenu styling, status-row value alignment.
- No server changes expected; audit data already comes from `/api/admin/audit` and the snapshot
  from `/api/admin/status/full`.

## Out of scope

Backend features/adapters, security policy, LLM provider behavior, and the compact surface.
If a finding seems to need backend work, stop and raise it rather than expanding scope.
