# Feature plan: App detail page + status history

## Goal

In the **end-user (compact) view**, let a user tap/click an app to open a more
detailed page for that app. The page shows the app's current state plus a
**history** of when it changed (went offline, came back online, degraded, …).
The **internet/infrastructure status** gets the same history treatment.

History must accrue **continuously** — not only while a browser happens to be
open — so the backend has to poll on a timer and persist status transitions.

Scope: this targets the compact/general-user surface. The history *backend* is
role-agnostic, so the admin surface can reuse it later, but the new UI work here
is the compact app-detail and infra-detail pages.

---

## Implementation status & review (reviewed 2026-06-04)

Codex landed PR #25 (`codex/api-coverage-safety-hardening`), which implements the
bulk of this plan **and** a safety-first scaffold for chat-driven automatic
repair. Verified on this machine: `go build ./...` ✓, `go test ./...` ✓,
`cmd/visualcheck` ✓ (`ok:true`, 0 sub-44px targets, 0 banned terms, no overflow).

**Landed:**
- **Phase 1 — poller + cache:** `App.RunPoller(ctx, cfg.Polling.Interval)` is
  started from `runServers`; read handlers serve a cloned cached snapshot
  (`latestSnapshot`) with a cold-start fallback; only the poller processes
  notifications; runtime-settings writes invalidate the cache;
  `POST /api/status/refresh` forces an immediate collect.
- **Phase 2 — history store + recorder:** `db.FileHistoryStore` (append-only
  `history.jsonl` + in-memory per-subject ring) and `history.Recorder`
  (transition-only diff; seeds baseline with no event; emits one `unknown` on app
  disappearance; covers infra subjects internet/dns/wan/nas/unraid_array). Hourly
  `Prune` by age + per-subject cap. Wired into the poller.
- **Phase 3 — history API:** `GET /api/apps/{id}/history` and
  `GET /api/infrastructure/history?subject=…`, role-gated, redacted notes,
  uptime 24h/7d, `no-store`.
- **Phases 4–5 — UI:** app cards are activatable (`role=button`, keydown), open an
  `app-detail`/`infra-detail` compact view with current status, last-working,
  uptime, and a plain-language timeline; focus-managed back; "Check again" uses
  the shared refresh endpoint. Privacy filter strips the new Unraid/Docker infra
  fields from general users.
- **Approval-gated repair v1:** the diagnosis schema returns a closed-set
  `recommended_action_id` + `recommended_action_target`; the server builds an
  `llmAgentPlanView` with an HMAC-signed, 5-min, actor/action/target/nonce-bound
  approval token; chat renders a normal approval popup; admin can enable fixes for the current
  session (`AgentControlEnabled` gate, `AgentArmDuration` ≤1h). `allow_once`
  now executes only `ask_admin_to_restart_container`, only for a currently
  resolved, opted-in, non-blacklisted app, and only once per token. Non-restart
  recommendations remain non-executing.

**Follow-up implementation notes:**
- The general-user infra-history API now restricts raw technical subjects
  (`dns`, `wan`, `unraid_array`) to admins. General users can query `internet`
  and, when server visibility is enabled, `nas`; non-admin infra history
  responses rewrite display names and notes into plain language.
- `cmd/visualcheck` now opens app-detail and infra-detail from the compact view,
  captures desktop + mobile screenshots, asserts render/history/empty-state,
  checks banned terms/overflow/touch targets, and verifies Back restores focus.
- `docs/security.md` records that `history.jsonl` is local operational state and
  must remain out of git.
- The approval-gated repair path now includes per-app `agent_repair_allowed`
  settings, restart-only server-side execution, single-use approval tokens,
  cooldown/rate-limit enforcement, post-restart verification, inline chat
  outcome reporting, history notes, and lifecycle audit coverage. Session-enabled
  autonomous restart repair is now implemented behind explicit admin enablement,
  session fix enablement, per-app opt-in, auto-review, and the same repair limits.
- The visual harness now includes the LLM settings limit copy plus a rendered
  approval dialog/outcome state, so regressions in the session-enabled/approved chat UI are
  caught without live LLM credentials.
- **Still optional:** the 24-hour status bar remains a nice-to-have.

---

## Current architecture (what exists vs. what's missing)

Grounded in the code as it stands today:

Implementation update: Phase 1 has landed. `cmd/dashboard` starts
`App.RunPoller(ctx, cfg.Polling.Interval)`, read handlers serve a cloned cached
snapshot with a cold-start fallback, and normal HTTP reads no longer process
notifications. The original poller/notification bullets below are retained as
historical rationale for the phase.

- **No background poller.** `config.PollingConfig.Interval` (default `30s`,
  `internal/config/config.go`) is validated but **never consumed**. Snapshots
  are built **on demand** in `App.collectSnapshot` (`internal/server/server.go`),
  called per request from `statusSummary`, `apps`, `appByID`, `adminStatus`, etc.
  → Nothing runs when no browser is connected.
- **Notifications are also on-demand.** `notifications.Manager.ProcessSnapshot`
  is invoked inside `collectSnapshot` (server.go ~265), i.e. only when a request
  triggers a snapshot. Moving to a poller fixes this too.
- **Storage is a single-file JSON store.** `db.FileStore` serializes the entire
  `State` to disk on every mutation (`persistLocked` → `MarshalIndent` whole
  state → temp file → rename). `Audit` is capped at 2000 entries. Appending a
  status sample every 30s into `State` would rewrite an ever-growing file each
  tick — not acceptable. History needs its own append-friendly storage.
- **Per-app data already models status + last-seen.** `models.AppStatus` has
  `CurrentStatus`, `LastSeenOnline`, `LastSeenOffline`, `CurrentProbeResult`.
  `models.InfrastructureStatus` has `InternetReachable`, `DNSOK`,
  `RouterReachable`, `UniFiWANUp`, `NASReachable`, `LastCheckedAt`. These are
  point-in-time only; there is no transition log.
- **A per-app endpoint already exists.** `GET /api/apps/{id}` → `appByID`
  returns the role-filtered `AppStatus`. We extend, not invent.
- **Role filtering is centralized.** `privacy.FilterSnapshotForRole` +
  `internal/privacy/visibility.go` decide which apps a general user sees
  (`VisibleToGeneralUsers`, hidden lists, blacklist) and gate infra
  (`ShowWANStatusToUsers`, `ShowNASStatusToUsers`). The history API must reuse
  exactly these rules.
- **Shared routes** are registered in `registerSharedRoutes` (both admin and
  compact routers, behind `requireAuth`). New user-facing history routes go here.
- **Compact UI uses view states.** `setCompactView(view)` in `web/public/app.js`
  sets `document.body.dataset.compactView` to `"status"` / `"chat"` and toggles
  panels. App cards render via `renderUserAppCard` as a **static `<article>`**
  with no click handler.
- **Retention config exists.** `config.RetentionConfig` already has
  `MaxAuditEntries`, `MaxIncidentAge`, etc. — extend it for history.
- **Visual-check harness** (`cmd/visualcheck`) gates the compact view on:
  touch targets ≥44px, no banned technical terms leaking to general users
  (`bannedTermCount`), no horizontal/bounds overflow, accessible primary actions.
  Any new compact view must satisfy all of these.

---

## Key design decisions

1. **Record transitions, not samples.** On each poll, diff the new snapshot's
   per-subject status against the previous one; emit an event only when it
   changes. Compact, sufficient for "went offline / came online", and enough to
   compute uptime over a window from event durations.
2. **A background poller is the enabler** (Phase 1). It is the single place that
   builds the full snapshot, updates a cache, records history, and processes
   notifications. Per-request handlers then serve the **cached** snapshot
   (role-filtered), which also reduces load on the Unraid/UniFi/Docker APIs and
   makes "Check again" instant.
3. **Separate append-only history store** (`history.jsonl`) rather than stuffing
   events into `db.State`. Avoids rewriting the whole state file every 30s and
   keeps growth linear-append. In-memory ring buffer for fast reads; periodic
   compaction/prune by age + per-subject cap.
4. **Detail surface = a real page**, implemented as a new compact view state
   `data-compact-view="app-detail"` (and `"infra-detail"`), with a back control —
   consistent with the existing `status`/`chat` view-state pattern, not a modal.
5. **Reuse role filtering.** The history API resolves visibility through the same
   helpers as `FilterSnapshotForRole`; a general user can never query history for
   an app/subject they cannot see.

---

## Data model additions

`internal/models/models.go`:

```go
type StatusSubjectType string

const (
    SubjectApp   StatusSubjectType = "app"
    SubjectInfra StatusSubjectType = "infra"
)

// StatusEvent is one recorded transition for a subject.
type StatusEvent struct {
    ID          string            `json:"id"`
    SubjectType StatusSubjectType `json:"subject_type"`           // "app" | "infra"
    SubjectID   string            `json:"subject_id"`             // app_id, or "internet"/"dns"/"wan"/"nas"/"unraid_array"
    DisplayName string            `json:"display_name"`
    From        CurrentStatus     `json:"from"`                   // previous status ("" if first observation)
    To          CurrentStatus     `json:"to"`
    At          time.Time         `json:"at"`
    Note        string            `json:"note,omitempty"`         // optional plain-language reason (redacted)
}

// StatusHistory is the API response for one subject.
type StatusHistory struct {
    SubjectType     StatusSubjectType `json:"subject_type"`
    SubjectID       string            `json:"subject_id"`
    DisplayName     string            `json:"display_name"`
    Current         CurrentStatus     `json:"current"`
    LastSeenOnline  *time.Time        `json:"last_seen_online,omitempty"`
    LastSeenOffline *time.Time        `json:"last_seen_offline,omitempty"`
    UptimePct24h    *float64          `json:"uptime_pct_24h,omitempty"`
    UptimePct7d     *float64          `json:"uptime_pct_7d,omitempty"`
    Events          []StatusEvent     `json:"events"`             // newest-first, windowed + capped
}
```

Infra subjects map booleans → `CurrentStatus` via a small normalizer:
`internet`/`dns`/`wan`/`nas` reachable→`online` else `offline`;
`unraid_array` healthy→`online`, present-but-unhealthy→`degraded`, else `offline`.

---

## Backend work (phased)

### Phase 1 — Background poller + snapshot cache
**Status:** implemented in `codex/api-coverage-safety-hardening`
*(foundational; independently valuable: notifications fire continuously, fewer upstream API calls)*

- Add `App.RunPoller(ctx, interval)` in `internal/server`. On each tick (and once
  immediately): call the existing `collectSnapshot(ctx, true)`, store the result
  in a new `App.cachedSnapshot` (guarded by a `sync.RWMutex`, with `GeneratedAt`).
- Start it from `cmd/dashboard/main.go:runServers` as a goroutine bound to `ctx`
  (so SCM/SIGTERM stop cancels it). Use `cfg.Polling.Interval`.
- Change request handlers to serve from cache: add `App.latestSnapshot(role)`
  that clones the cached full snapshot and applies `FilterSnapshotForRole`. Point
  `statusSummary`/`apps`/`appByID`/`adminStatus`/etc. at it. Keep `Snapshot()` and
  `collectSnapshot()` for `run-once` and tests.
- **Notifications de-dup:** only the poller calls `ProcessSnapshot`. Per-request
  snapshot building uses `processNotifications=false` (handlers now read cache
  anyway). Prevents double notifications.
- Shared manual refresh: `POST /api/status/refresh` lets the compact "Check
  again" button request an immediate collector refresh instead of waiting for
  the next tick. The route is authenticated, CSRF-protected, records history
  transitions, and returns the role-filtered snapshot.
- **Cold start:** before the first tick completes, serve a freshly collected
  snapshot (or a "warming up" state) so the first page load isn't empty.
- Tests: poller updates cache; handlers read cache; notifications processed once
  per tick.

### Phase 2 — History store + recorder
**Status:** implemented in `codex/api-coverage-safety-hardening`
Completed implementation notes: `RunPoller` performs the immediate/timed
collections and stores a cloned full snapshot; `latestSnapshot` clones again
before role filtering so general-user filtering cannot mutate cached admin data;
runtime settings writes invalidate the cache. The shared manual refresh endpoint
is implemented as `POST /api/status/refresh`.

Phase 2 implementation update: status transition models, append-only
`history.jsonl`, the in-memory recorder, retention config, poller wiring, and
tests have landed. The store writes JSONL next to the configured database path
and keeps an in-memory per-subject ring for reads; `Prune` compacts by age and
per-subject cap.

- `internal/db/history.go`: `HistoryStore` interface + `FileHistoryStore`
  (append-only `history.jsonl` next to `Database.Path`), with an in-memory ring
  buffer (cap per subject, e.g. 500) for reads and a `Prune(retention)` that
  compacts the file by age (`Retention.MaxStatusEventAge`, new) + per-subject cap.

  ```go
  type HistoryFilter struct {
      SubjectType models.StatusSubjectType
      SubjectID   string
      Since       time.Time
      Limit       int
  }
  type HistoryStore interface {
      Append(events []models.StatusEvent) error
      Query(filter HistoryFilter) ([]models.StatusEvent, error)
      Prune(retention config.RetentionConfig) error
  }
  ```
- `internal/history/recorder.go`: `Recorder` holds the previous status per
  subject key (`subjectType|subjectID`) in memory. `Record(snapshot) []StatusEvent`
  diffs apps + infra subjects and appends transitions.
  - **First observation seeds the baseline and emits no event** (avoids a burst
    of "changed" on startup). Optionally emit an `"initial"` marker — default no.
  - Maintain `LastSeenOnline/Offline` from observed transitions if collectors
    don't already populate them.
  - **Disappearing apps:** if a tracked app is absent from a snapshot, emit one
    transition to `unknown`, then stop tracking until it returns.
- Wire `Recorder.Record` + `HistoryStore.Append` into the poller (Phase 1), after
  the snapshot is built. Run `Prune` on a slower cadence (e.g. hourly).
- Add `Retention.MaxStatusEventAge` (default ~90 days) and a per-subject cap to
  `config` + defaults.
- Tests: transitions detected; no event on first sight; flap online→offline→online
  produces two events; prune drops old/over-cap events; JSONL survives restart.

### Phase 3 — History API
**Status:** implemented in `codex/api-coverage-safety-hardening`

- New shared routes (in `registerSharedRoutes`, behind `requireAuth`):
  - `GET /api/apps/{id}/history?window=7d&limit=100`
  - `GET /api/infrastructure/history?subject=internet&window=7d`
- Handlers resolve the requesting user's role, then:
  - **App history:** confirm the app is visible to that role using the same
    predicate as `FilterSnapshotForRole` (visible-to-general, not hidden, not
    blacklisted). If not visible → `404` (don't leak existence).
  - **Infra history:** allow `internet`/`dns` generally; gate `wan` behind
    `ShowWANStatusToUsers` and `nas`/`unraid_array` behind `ShowNASStatusToUsers`
    for general users; admin sees all. Unauthorized subject → `404`.
  - Build `StatusHistory`: pull current status from the cached snapshot, events
    from `HistoryStore.Query`, compute `UptimePct*` from event durations within
    the window. Run event `Note`s through the redactor; for general users strip
    any admin-only fields (same spirit as the snapshot filter).
  - `Cache-Control: no-store` (consistent with other API responses).
- Tests: general user blocked from hidden app history (404); WAN gated; window
  parsing; uptime math; admin unrestricted.

---

## Frontend work
**Status:** first compact slice implemented in `codex/api-coverage-safety-hardening`.
Visible app cards open a compact detail page with current status, last-working
metadata, uptime, and a plain-language history timeline. The visible router
summary opens an internet detail page, and the visible server summary opens a
server detail page backed by the infrastructure history API. "Check again" now
uses the shared manual refresh endpoint. The optional 24-hour visual bar and
broader infra subjects remain open.

### Make app cards open a detail page
- In `renderUserAppCard` (`web/public/app.js`), render the card as an activatable
  control: `role="button"`, `tabindex="0"`, keep the existing descriptive
  `aria-label` (`"<App>: <status>"`), and add a small chevron affordance. Wire
  `click` + `Enter`/`Space` → `openAppDetail(app.app_id)`.
- `openAppDetail(appId)`:
  - `setCompactView("app-detail")` (extend `setCompactView` to handle the new
    state: set `data-compact-view`, page title to the app name, show the
    `#app-detail` panel, hide status/chat panels).
  - Fetch `GET /api/apps/{id}` and `GET /api/apps/{id}/history` in parallel;
    render into a new `#app-detail` section in `index.html`.
  - Provide a **back** control (reuse the hamburger area or a dedicated back
    button) returning to `setCompactView("status")` with focus restored to the
    originating card.

### App detail page content (plain language, no technical terms)
- Header: app logo + name + current status pill (reuse `statusIndicator`).
- "Right now": `plainAppSummary(app)` (e.g. "Working normally." / "Not
  responding.").
- "Last working": from `last_seen_online` ("Last working 2 hours ago").
- Optional uptime line: "Working 99% of the last 7 days" from `uptime_pct_7d`.
- **History timeline** (the core ask): newest-first list of events rendered in
  plain language, e.g.
  - ✓ "Came back online — Today 2:14 PM"
  - ✕ "Stopped working — Today 1:02 PM (after ~3 h working)"
  - empty state: "No changes recorded yet."
- Optional nice-to-have: a 24-hour horizontal bar split into colored segments
  (online/degraded/offline) for an at-a-glance day view.

### Internet / infrastructure detail
- When the status view shows an internet/WAN row to the user (subject to
  `ShowWANStatusToUsers`), make it tappable → `openInfraDetail("internet")`,
  reusing the same detail layout via `data-compact-view="infra-detail"` and
  `GET /api/infrastructure/history?subject=internet`.

### Banned-term safety
All compact strings stay plain-language. Do not surface `container`/`docker`/
`unraid`/`wan`/`dns`/etc. in general-user copy (the harness `bannedTermCount`
check fails the build otherwise). Use "internet" for the internet subject's
display name, not "WAN".

---

## Accessibility & visual-check harness

- Card-as-button: keyboard operable, visible focus, retains accessible name.
- New views must pass the existing gates: touch targets ≥44px, no banned terms,
  no horizontal/bounds overflow, accessible labels.
- Add harness coverage (`cmd/visualcheck/main.go`): open the app-detail view from
  a user-home card, assert it renders (title = app name, timeline present or
  empty-state), assert **back** returns to status with focus restored, run the
  banned-term + touch-target + overflow audits on the detail view, and screenshot
  it (desktop + mobile). For fixtures (no real transitions), either ship a small
  `history.jsonl` fixture or assert the "No changes recorded yet" empty state.

---

## Edge cases

- **Restart:** recorder's "previous" map is in-memory → first poll after restart
  reseeds baseline, emitting no spurious events. Persisted `history.jsonl` keeps
  prior events.
- **Fixture mode:** static scenario → no transitions; detail view shows empty
  state unless a fixture history file is provided.
- **Flapping:** every genuine change is one event; consider an optional minimum
  dwell-time later if a flaky probe spams events (not in v1).
- **Clock:** store UTC; format in the client's locale.
- **Concurrency:** poller writes cache + appends history under locks; readers use
  the cached clone.
- **Privacy:** an app hidden after history was recorded must not be queryable by a
  general user (404), even though events exist on disk.

---

## Testing summary

- Unit: recorder diff logic; infra bool→status normalizer; uptime math; history
  store append/query/prune + JSONL round-trip; visibility gating on the API.
- Integration: poller updates cache and records a transition when a fixture
  scenario flips; notifications fire exactly once per tick.
- Visual-check: app-detail + infra-detail views (render, back, a11y, no banned
  terms, no overflow), desktop + mobile screenshots.

---

## Suggested PR breakdown

1. **Poller + snapshot cache** (Phase 1) — backend only; notifications move to the
   poller. No UI change.
2. **History store + recorder** (Phase 2) — storage, model, recorder, retention,
   wired into the poller. No UI change.
3. **History API** (Phase 3) — the two endpoints + role gating + tests.
4. **App detail page** (frontend) — tappable cards, `app-detail` view, timeline.
5. **Internet/infra detail** (frontend) — `infra-detail` view + tappable internet
   row.
6. **Harness + docs** — visual-check coverage, `docs/security.md` (new endpoints,
   on-disk `history.jsonl` is sensitive and git-ignored), `docs/architecture.md`
   + `docs/agent-roadmap.md` updates.

Each PR is independently shippable and leaves the app working.

---

# Automatic server repair — path to deployable quality

The chat agent can already *diagnose* and *recommend* a fix, and the approval/arm
plumbing exists, but execution is hard-locked. This section plans the remaining
work to let an admin let the agent actually perform a repair (initially: restart
a crashed container) from chat, safely.

## Trust model (the non-negotiable principle)

**The LLM never executes anything.** It only returns a closed-set
`recommended_action_id` + a target *hint*. The **server** is the sole actuator:
it re-resolves the target against its own live snapshot, maps the action to a
hardcoded, allowlisted operation, and runs it only after every gate passes. No
free-form command, container name, or shell string from the model is ever
executed. This boundary already exists in the scaffold — we complete it without
weakening it.

## Defense-in-depth gates (all must hold to execute)

1. `LLM.AgentControlEnabled` is **on** (admin setting, default **off**).
2. The admin enabled fixes for this session (`AgentArmDuration` <= 1h, auto-expires).
3. A valid, **single-use**, unexpired approval token bound to actor + action +
   target.
4. The action is in the **executable allowlist** (v1: `restart` only).
5. The target app is **opted in** to auto-repair (per-app flag, default off) and
   not blacklisted.
6. Per-app **cooldown** + global **rate limit** not exceeded.
7. Requester is **admin** on the admin router (general users can never reach it).

## R1 — Server-side actuator (unlock execution)
- Add an `agentRepairExecutor` mapping `recommended_action_id` → a concrete op:
  - `ask_admin_to_restart_container` → `docker.ControlContainer(app, ActionRestart)`.
  - The `ask_admin_to_check_*` actions stay **non-executing** recommendations
    (they don't mutate) — they show in chat but never actuate.
- Reuse the existing `controlApp` discipline verbatim: re-resolve the target from
  the current full snapshot (`findAppByID`), apply the stop/restart confirmation
  rule, and audit `app.container.action` with `actor` = approving admin +
  `via:"agent_plan"`, `plan_id`, `recommended_action_id`.
- In `recordAgentApproval`, when `choice=allow_once` + token valid + session enabled +
  action executable + target resolved: call the executor and return the result,
  replacing today's `409 "locked"`. Compute `CanExecute` from
  (`AgentControlEnabled` && action∈allowlist && target resolved && app opted-in);
  enable the chat "Allow fix" button only when the session is enabled.

## R2 — Safety envelope
- **Per-app opt-in:** add `AgentRepairAllowed bool` to the app catalog entry
  (default false), surfaced in admin app settings. Only opted-in, non-blacklisted
  apps are eligible; reflected in `resolveAgentPlanTarget`.
- **Single-use tokens:** track consumed plan nonces (in-memory set with TTL = token
  expiry) so an approval executes at most once; reject replays.
- **Action allowlist:** hardcode `{restart}` for v1. Explicitly reject stop/start/
  delete/exec/anything else. Never expose shell/exec.
- **Cooldown + rate limit:** at most 1 agent restart per app per 10 min and 5
  agent actions per hour globally; over-limit is an audited refusal surfaced in
  chat.
- **Kill switch:** disable the session or set `AgentControlEnabled=off` to stop fixes instantly;
  session enablement auto-expires. One target per approval (no bulk).

## R3 — Outcome verification & reporting
- After executing, force a `refreshSnapshot` after a short delay, compare the
  target's before/after status, and write a history `StatusEvent` note
  ("Auto-repair: restarted - recovered" / "still not responding").
- Return the outcome to chat (recovered / still down, before→after). On failure,
  **do not auto-retry**; surface to the admin.
- Audit the full lifecycle: proposed → approved (by whom) → executed → verified.

## R4 — UI completion
- Enable "Allow fix" only when `can_execute` && session enabled; show the resolved app +
  action and a confirm affordance consistent with `controlApp`.
- Show the execution outcome inline in the chat thread.
- Admin settings: expose the `AgentControlEnabled` switch and per-app
  "Allow admin/AI restart" toggles alongside the session enable control; show
  cooldown/rate-limit + enabled-until state.

## R5 — Tests, security review, docs
- Unit: action→op mapping; rejects non-allowlisted actions; single-use token;
  cooldown/rate-limit; per-app opt-in; session-disabled/disabled/non-admin paths refuse
  **without** calling the Docker client.
- Integration: session enabled + approved + eligible restart calls a mock `ControlContainer`
  exactly once; replay blocked; cooldown blocks the second; verification re-poll
  records the outcome event.
- Run `/security-review` on the actuation path (trust boundary, no model→command
  injection, token replay, privilege checks, audit completeness).
- Docs: `docs/security.md` (new actuation capability + every gate + audit),
  `docs/llm-policy.md`, `docs/agent-roadmap.md`, and this file.

## Suggested repair PR breakdown
- **AR1:** per-app `AgentRepairAllowed` flag + admin settings toggle + plan
  eligibility wiring. **Landed:** stored as `app_catalog.agent_repair_allowed`
  and projected onto admin app snapshots.
- **AR2:** server-side actuator + single-use tokens + allowlist; unlock
  `recordAgentApproval` to execute restart; enable "Allow fix"; full audit.
  **Landed for restart-only approval-gated v1.**
- **AR3:** cooldown/rate-limit + outcome verification re-poll + chat outcome UI.
  **Landed for approval-gated restart v1.**
- **AR4:** security review + docs + harness coverage for the session-enabled/approved flow.
  **Landed for approval-gated restart v1.**
- **AR5:** session-enabled autonomous restart repair using the same server-side restart path,
  plus mandatory auto-review and no popup after execution/refusal.
  **Landed for restart-only autonomous repair.**

## Repair-specific open choices
- **Autonomy level (key decision).** v1 shipped as **approval-gated with current-session
  fix enablement**. Session-enabled autonomous restart repair is now available for one non-online
  opted-in app during the enabled session window, with mandatory auto-review and the same
  cooldown/rate-limit checks.
- **Executable action scope for v1:** restart-only (recommended) vs. also
  start/stop.
- **Cooldown/rate-limit defaults:** current v1 uses 1/app/10min, 5/hour global.
- **Per-app flag:** new `AgentRepairAllowed` (recommended) vs. reusing an existing
  restart-permission flag.

---

# General-user repair + admin-side fixes (follow-up, reviewed 2026-06-04)

The approval-gated **admin** restart repair shipped in PR #27 and is solid (build,
tests, harness green; every gate enforced; my earlier infra-history banned-term
finding was fixed). The gaps below are what remain incomplete — concentrated on
the **general-user side**, which today has no working repair path at all.

## Confirmed gaps

**General-user side (the main gap):**
- `diagnose` attaches `agent_plan` only when `mode == AdminRequested && role ==
  Admin` (`server.go` ~1351). A general user's diagnosis carries no plan, so the
  compact chat — which already renders `result.agent_plan` (`app.js` ~2170) —
  never shows a repair affordance. The model's `recommended_action_id` + target
  are computed for general users and then discarded.
- The only general-user action, `POST /api/user/notify-admin`, is a **stub**: it
  records an audit entry and returns `"queued"` but delivers nothing and carries
  no fix context (just free text). Nothing bridges a user report to the admin
  approval flow.
- `models.AppStatus.RestartAllowedGeneralUser` exists but is referenced **nowhere
  else** (no general-user control route; only `/api/user/{diagnose,notify-admin,
  notification-preferences}`). A latent capability the original design implied but
  never wired.
- No outcome feedback: even when an admin repairs, the reporting user is never
  told the app recovered.

**Admin-side quality:**
- **Verification window too short.** `agentRepairVerificationDelay = 2s` + a single
  re-poll (`verifyAgentRepairOutcome`) will frequently report "still not
  responding" on a *successful* restart, because containers rarely return to
  `online` within 2s. Fix: poll a few times over ~30–60s (stop early on
  recovery), or phrase the interim outcome as "restart sent, still coming up."
- Non-executable recommendations (`ask_admin_to_check_logs/unifi/storage`) are
  `ApprovalEligible` but not `Executable`, so they render an approval popup with a
  permanently disabled "Allow fix." Render them as informational instead.

## Decision: general-user repair = **request-by-default + per-app direct opt-in**

Two complementary paths, both reusing the existing restart safety envelope
(restart-only allowlist, server-side target re-resolution, not-blacklisted,
shared cooldown 1/app/10min + global 5/hr, single-use, full audit, post-restart
verification). The trust boundary is unchanged: the server only ever runs a fixed
restart against a server-resolved, opted-in app; no model or client input is
executed as a command.

### Path A — Request → admin approval (default for every repair-eligible app)
- Prereq: make `notify-admin` actually deliver. Persist a `RepairRequest`
  {id, requester, app_id, action, diagnosis_summary, status
  (pending/approved/denied/executed/failed), created_at, resolved_at, outcome}
  and surface it to admins (real notification + an in-app pending-requests queue).
- Compact UI: when a general user's diagnosis (or app-detail page) shows a
  fixable, eligible app, offer **"Ask an admin to fix this"** bound to the
  resolved app + recommended action. `POST /api/user/repair-request`
  (auth + CSRF; app must be visible to the role and repair-eligible).
- Admin UI: a pending-requests view (admin chat / settings / notifications) with
  approve/deny that runs through the **existing** approval+execution path
  (`recordAgentApproval` generalized to accept a request-originated approval, or a
  sibling endpoint). Approving still requires the admin gates (`AgentControlEnabled`,
  arm) so nothing changes the execution trust model.
- Feed the outcome back to the requester (notification + history note).

### Path B — Direct general-user restart (per-app opt-in)
- Wire up `RestartAllowedGeneralUser`: per-app admin toggle ("Allow household
  users to restart this app"), projected onto snapshots like `AgentRepairAllowed`,
  gated by a master switch (default **off**).
- New `POST /api/user/apps/{id}/restart` (auth + CSRF, **general-user reachable**):
  executes a restart **only** when the app is visible to the role, has
  `RestartAllowedGeneralUser`, is not blacklisted, and passes the shared
  cooldown/rate-limit; audited with `actor = general user`, `via:"user_direct"`;
  same verification + outcome. **No admin arm required** (the admin pre-authorized
  it via the per-app opt-in); the LLM is not in the loop (the user taps a button
  on a resolved app).
- Compact UI: a confirm-gated **"Restart now"** on directly-repairable apps
  (app-detail + chat), consistent with the admin `controlApp` confirm discipline.

### Safety / visibility notes
- `RestartAllowedGeneralUser` stays visible to general users (so the UI can show
  the button); `AgentRepairAllowed` remains admin-only/stripped.
- Cooldown/rate-limit state is **shared** across admin-agent, request-approved,
  and direct-user repairs so non-admins can't bypass the global cap.
- Both master switches default off; per-app opt-ins default off; blacklist always
  wins.

## Follow-up PR breakdown
- **GR0 — Quality fixes (small, do first):** lengthen/iterate the verification
  poll; make `notify-admin` deliver + carry context; stop rendering an approval
  popup for non-executable recommendations.
- **GR1 - Request path: IMPLEMENTED.** `RepairRequest` model + store, `POST /api/user/repair-request`,
  admin pending-requests queue + approve/deny wired to execution, requester
  outcome feedback. Compact "Ask an admin to fix this."
- **GR2 - Direct opt-in path: IMPLEMENTED.** `RestartAllowedGeneralUser` is
  wired through app catalog settings, snapshot projection, and the compact app
  detail "Restart now" affordance; `POST /api/user/apps/{id}/restart` uses the
  shared repair cooldown/rate-limit and verifies the result.
  **Update:** the same backward-compatible opt-in now gates compact Start,
  Restart, and Stop controls through `POST /api/user/apps/{id}/action`; the
  restart endpoint remains as a compatibility alias.
- **GR3 - Tests, `/security-review`, docs, harness:** backend tests now cover
  general-user repair requests, direct restart success, and refusal for
  non-opted-in/hidden/blacklisted/online apps; `security.md` and
  `api-config.md` describe the gates. Remaining harness work: add visual flags
  for the new compact affordances (plain language, >=44px, no overflow) and
  update `/security-review` output when that endpoint includes feature-specific
  assertions.

## Open choices for the general-user work
- **Direct path & the LLM:** Path B is a plain button on a resolved app (no model
  actuation) — recommended. (Letting a general-user *chat* trigger a direct fix
  would put the model in the loop and is not recommended for v1.)
- **Admin delivery for Path A:** in-app pending queue only (simplest) vs. also the
  notification backend (email/push) — depends on what notification transport you
  want wired.

---

## Open choices

- **History storage: DECIDED — dedicated `history.jsonl` append log** (avoids 30s
  whole-state rewrites; in-memory ring buffer for reads; periodic prune). The
  capped-in-`db.State` alternative was rejected because it rewrites the whole
  state file each poll.
- **Detail surface:** full page via a new compact view state (recommended, matches
  "page") vs. a slide-up drawer/modal.
- **24h status bar:** include the visual timeline bar in v1, or ship the textual
  timeline first and add the bar later.
- **Retention window:** default ~90 days + per-subject cap — adjust to taste.
