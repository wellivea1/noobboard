# Agent Roadmap

A shared planning document for AI coding agents (and humans) working on NoobBoard.
It defines the next three workstreams, the design constraints each must respect, the
files most likely to change, and acceptance criteria. Update this file as work lands.

## How to use this document

- Each workstream below has a **Status** line. Keep it current using the legend.
- Before claiming a task, skim the **Constraints** for that workstream — they encode
  security invariants that are easy to break and expensive to walk back.
- When you finish a slice, tick its checklist item and note the PR/commit.
- If a change touches auth, LLM context, Docker control, or secrets, also update the
  matching policy doc (`docs/security.md`, `docs/llm-policy.md`, `docs/api-config.md`).

**Status legend:** `not-started` · `in-progress` · `blocked` · `done`

## Project conventions every agent must follow

These are non-negotiable for any change in this repo.

- **Build + test before pushing:**
  ```powershell
  & 'C:\Program Files\Go\bin\go.exe' test ./...
  & 'C:\Program Files\Go\bin\go.exe' build -o dist\noobboard.exe .\cmd\dashboard
  ```
- **Run the visual check after any UI change:** `scripts\visual-check.ps1` (see README).
- **Never commit secrets.** API keys, `.key` files, `auth*.txt`, `config.local.yaml`,
  `.env`, `data/`, `logs/`, and `dist/` are git-ignored. Secrets are read only from env
  vars, local config, or explicitly configured key files (`internal/config/config.go:
  applySecretFiles`). Do not widen this surface.
- **Two routers, two ports.** The admin router (`8787`) owns `/api/admin/*`; the compact
  router (`8788`) must never register an admin endpoint. See `internal/server/server.go`.
- **Mutations are gated.** Every mutating endpoint requires CSRF + same-origin checks and
  must be audited. Admin-only actions go through `requireAdmin`.
- **Redaction is mandatory.** Anything that reaches audit logs, displayed logs,
  notifications, or LLM context passes through `internal/privacy` first, fail-closed.
- **Source honesty.** Snapshots/app records carry `data_source` (`fixture`/`mixed`/
  `live`/`unraid-docker`). Do not let fixture data masquerade as live.
- **General-user UI must pass a literacy + layout bar.** Any change to the compact surface
  must keep technical vocabulary out of the default view (see the banned-term list under
  Workstream B), avoid horizontal scroll/dense tables, use ≥44px touch targets, and ship
  with mobile screenshots (390×844) in the PR for the key states.

---

## Multi-agent UX workflow

From a product review of the Codex-built app (see references): the backend is solid, but a
single implementer agent produced a developer-flavored UI. The mitigation is a process one
— **stop treating the implementer agent as the product designer.** Split the roles:

```text
UX agent (e.g. Claude): write UX spec + microcopy
        ↓
Implementer agent (e.g. Codex): implement the exact spec
        ↓
visual-check: screenshots + semantic/literacy UX audit
        ↓
UX agent: critique the screenshots
        ↓
Implementer agent: fix specific issues, one at a time
        ↓
repeat
```

Phased application for any UI workstream:

1. **Freeze backend scope.** Don't add backend features/adapters/security changes while
   fixing UX. The backend already covers most requirements; the risk now is feature
   accretion. Focus changes on compact UX, LLM presentation, and UX tests.
2. **Produce a UX spec.** A UX-focused agent designs the compact mobile UI *only*, against
   explicit constraints (tech-illiterate iPhone user; no admin controls/logs/hidden
   services; large touch targets; no dense tables; no horizontal scroll).
3. **Critique.** Return ranked UX problems, exact replacement microcopy, layout changes, and
   acceptance criteria the implementer can satisfy.
4. **Implement against a concrete ticket** with testable acceptance criteria — not a vague
   "make it nicer" request.
5. **Run the critic loop.** Feed one issue at a time; don't ask the implementer to solve 25
   UX problems in a single pass (it produces churn).

Make screenshots a required PR artifact so the implementer is forced to *look at the thing*,
and back them with semantic DOM checks (screenshots catch aesthetics; DOM checks catch
leaked technical/admin terms). This workflow is the connective tissue for Workstream B and
the presentation half of Workstream C.

---

## Workstream A — First-run setup wizard

**Status:** `not-started`
**Goal:** A guided, browser-based first-run experience on the admin port that configures
credentials, the LLM provider, user accounts, and the compact/"noob" view — so a new
deployment is usable without hand-editing config or env vars.

### Scope
1. **First-run detection + lock.** Add a persisted `setup_completed` flag (in the file
   store, `internal/db/store.go`). Until set, the admin site redirects to the wizard;
   after completion the wizard routes return `404`/redirect. Never expose the wizard on
   the compact port. **First-run detection keys off `setup_completed` only** — never off
   the mere existence of an admin user or bootstrap credentials (see the installer contract
   below), so a pre-seeded admin does not bypass the wizard.
2. **Credentials step.** Collect Unraid base URL + API key and UniFi base URL + API key.
   Write keys to local key files using the existing secret-file format, and persist the
   non-secret config (base URLs, site id, mode=`live`) — mirror `config.local.yaml`.
   Validate live reachability before advancing (reuse the collectors; a "Test connection"
   button calls a bounded probe).
3. **LLM provider step.** Two paths:
   - **API key (phase 1):** choose `openai` or `anthropic`, paste key, store as a key
     file, set `NOOBBOARD_LLM_PROVIDER`. Verify with a tiny validation call.
   - **Provider web login (phase 2, optional):** OAuth/device-code sign-in for providers
     that support subscription auth, instead of a raw key. See OpenCode `auth login` and
     Codex `codex login` (ChatGPT auth) for the UX/token-exchange pattern. Tokens are
     secrets — store like key files, refresh server-side, never log. Ship phase 1 first;
     gate phase 2 behind a feature flag.
4. **Accounts step.** Ensure a real admin login exists, then optionally create named
   admin/general users. Reuse `internal/users` (PBKDF2 hashing already there). **If an admin
   was already pre-seeded by the installer** (see contract below), treat this step as
   satisfied: detect the existing admin and let the operator *continue* (optionally offering
   to change the password) rather than forcing re-entry or erroring. If only the built-in
   `change-me-now` default is present, require a replacement here.
5. **Compact/noob view step.** Pick what the noob surface shows: which apps are visible,
   whether NAS/WAN tiles appear, whether chat/LLM is available to general users. This
   writes `models.VisibilitySettings` + app visibility rather than introducing new state.

### Constraints
- Wizard routes live under the admin router only, are CSRF + same-origin protected, and
  are reachable pre-auth **only** while `setup_completed == false`; once an admin password
  is set in the accounts step, subsequent steps require that session.
- Secrets entered in the wizard go to key files / local config, never to git-tracked
  files, never echoed back in API responses, never logged.
- Reuse existing config plumbing (`internal/config/config.go`) — the wizard produces the
  same config a hand-written `config.local.yaml` would.

### Likely files
`internal/server/server.go` (routes + guard), new `internal/server/setup.go`,
`internal/db/store.go` (flag + persisted settings), `internal/users/users.go`,
`internal/config/config.go` (write-back helpers), `web/public/index.html`,
`web/public/app.js`, `web/public/styles.css`.

### Installer interop contract (do not break)
`install.ps1` can optionally pre-seed the admin login before the wizard ever runs. The
wizard MUST remain compatible with this. The contract:

- The installer writes `auth.bootstrap_admin_username` / `auth.bootstrap_admin_password`
  into the service config (`C:\ProgramData\NoobBoard\config.yaml`). It does **not** touch
  any `setup_completed` flag and does **not** create wizard state.
- `internal/users` bootstrap is **create-only**, keyed on the admin username: it seeds the
  admin once and is a no-op if that user already exists. Pre-seeding therefore just means
  "an admin user exists with a real password instead of `change-me-now`."
- Consequently, when the wizard ships it must: (a) still run whenever `setup_completed`
  is false, regardless of whether an admin already exists; and (b) at the accounts step,
  detect a pre-seeded admin and **continue** from there (don't force re-entry, don't error).
- The wizard owns `config.yaml` going forward; when it writes config it must **preserve**
  any existing `auth:` keys (merge, don't clobber) — mirroring how the installer merges.

Net effect: installer (optional credential seeding) and wizard (full guided setup) are
complementary, and either can run first without breaking the other.

### Acceptance criteria
- [ ] Fresh install (empty data dir) lands on the wizard; completing it yields a working
      live dashboard with no manual env/config editing.
- [ ] Connection-test and LLM-key validation give clear pass/fail feedback.
- [ ] After completion the wizard is unreachable; re-running the binary goes straight to
      login.
- [ ] No secret is written to a git-tracked path or returned in any API response.
- [ ] With an installer-pre-seeded admin, the wizard still runs and continues from the
      accounts step (no re-entry forced, no error); existing `auth:` config is preserved.
- [ ] `go test ./...`, build, and `visual-check.ps1` all pass.

### Open questions
- Where should key files live by default (repo `secrets/` for dev vs `%ProgramData%\
  NoobBoard\secrets` for the Windows service)? Pick per-deployment, document in
  `docs/deployment-windows.md`.
- Should phase-2 web login be limited to providers that actually support non-API-key
  subscription auth? Confirm current provider capabilities before building.

---

## Workstream B — Compact "noob" UX (redesign + easy customization)

**Status:** `in-progress` (B1 spec written → `docs/ux-compact.md`; implementation not started)
**Goal:** Make the compact surface genuinely usable by an extremely non-technical iPhone
user (the redesign), and make it easy for an admin to tailor what that user sees (the
customization editor). The redesign is the foundation; customization sits on top of it.

> Product-review context (see references): today the compact surface inherits too much
> "dashboard thinking" — it shows source/role pills, rearrange/restore-monitor controls,
> icon-only topbar actions, horizontally scrolling action strips, status grids, and
> compressed/truncated app cards. It can pass mechanical mobile-overflow checks while still
> being dense and unparseable. The compact app is a **shared home status remote, not an admin
> dashboard.** Follow the Multi-agent UX workflow above to drive this work.

### B1 — Compact UX foundation (redesign)
The compact general-user view should let a user answer: *Is my thing working? Internet or
server problem? Tell the admin? Avoid touching anything?*

- **Remove from the general-user view:** rearrange/restore-monitor controls, source pill,
  role pill, topbar horizontal action strip, admin-style tabs, app-image controls, incident
  IDs (unless explicitly enabled), status grids, and any raw technical terms.
- **Compact home layout:**
  1. Hero status card — large headline, status color, one-sentence explanation, one
     recommended next step.
  2. Primary actions — *Notify admin*, *Check again*, and *Ask what's wrong* (only if LLM
     available). No more than ~2 primary actions above the fold; no icon-only primary actions.
  3. Visible apps list — icon/name + Working / Not working / Problem / Unknown + optional
     one-sentence summary.
  4. Per-app notification opt-in toggles.
  5. Optional **technical details** — hidden by default.
- **Plain-English mapping** (apply consistently in copy): container→app, endpoint failed→
  not responding, WAN down→internet appears down, array stopped→server storage is not ready,
  NAS unreachable→server cannot be reached, health check failed→app is not responding,
  degraded→problem, online→working, offline→not working.
- **Banned technical terms** in the default general-user view (allowed only in admin or a
  hidden technical-details disclosure): container, docker, unraid, array, parity, endpoint,
  graphql, probe, WAN, LAN, API, SSH, telemetry, SMART, syslog, filesystem, cache pool.
- **Semantic UX audit in `cmd/visualcheck`:** extend the harness beyond pixel/overflow checks
  with DOM-level assertions — e.g. compact home/chat visible, ≤5 status items above the fold,
  no banned terms present, no admin tabs, no icon-only primary actions. Screenshots catch
  aesthetics; these catch leaked admin/technical vocabulary.

### B2 — Customization editor
- **Structured compact-view editor** replacing raw JSON for the common cases: choose visible
  apps, reorder (drag-and-drop), set friendly display names, pick icons, toggle the NAS/WAN
  status tiles, and toggle chat/LLM availability for general users.
- **Icon picker** sourced from the bundled `web/public/app-icons/*.svg`, Docker-label icons,
  and admin icon-override URLs. Reuse the existing URL sanitization rules (unsupported
  schemes / embedded credentials rejected — see `docs/security.md`).
- **Live preview** of the compact surface while editing.
- **Per-device vs shared.** Today, overview monitor visibility/order are client-side
  localStorage prefs (per admin device). Decide explicitly which noob-view settings are
  shared runtime settings (persisted, affect all users) vs per-device prefs, and label them
  in the UI so the distinction is obvious.

### Constraints
- All persisted mutations stay admin-only, CSRF-protected, and audited; the compact router
  gains no admin endpoints.
- Build on existing models: `models.VisibilitySettings`, the app catalog / icon overrides
  (`config.AppCatalogConfig`), and per-role app visibility. Avoid inventing parallel
  settings state.
- Structured settings controls are the default surface for runtime configuration. Avoid
  falling back to raw JSON editors unless an explicitly advanced escape hatch is added.
- Integration settings remain admin-only and must keep API keys write-only in responses.

### Likely files
`web/public/app.js`, `web/public/styles.css`, `web/public/index.html`,
`cmd/visualcheck` (semantic UX audit), `internal/models/models.go`, settings handlers in
`internal/server/server.go`, `internal/db/store.go`. The B1 redesign spec (compact surface)
is written: **`docs/ux-compact.md`** — implement B1 against it before starting B2. A related
follow-on, **B3 — compact settings drawer** (hamburger → extensible side panel with a minimal
Settings destination: notifications + account), is specified in **`docs/ux-compact-settings.md`**.

### Acceptance criteria
- [ ] On the compact general-user view, a non-technical user can answer the four questions
      from the first screen.
- [ ] No banned technical terms appear in the default general-user UI (enforced by the
      semantic audit); admin-only terms stay on the admin surface or behind technical details.
- [ ] No horizontal overflow; ≥44px touch targets; readable on a 390×844 viewport.
- [ ] An admin can curate the noob view (visible apps, order, names, icons, tiles, chat)
      without touching JSON.
- [ ] Changes persist, survive restart, and reflect on the compact port.
- [ ] Shared vs per-device settings are clearly distinguished in the UI.
- [ ] Icon inputs are sanitized; bad URLs/schemes are rejected with feedback.
- [ ] PR includes mobile screenshots (390×844) for: compact home, app-down, internet-down,
      server-down, and a general-user LLM response.
- [ ] `go test ./...`, build, `visual-check.ps1`, and the new semantic UX audit all pass.

---

## Workstream C — Customizable LLM access + optional full agent access

**Status:** `in-progress` (read-only live API tools implemented; mutating repair tools not started)
**Goal:** (1) Make LLM access easy to customize per role/policy, and (2) add an **opt-in,
manually enabled** "agent mode" where the LLM can *act* to resolve problems (e.g., restart
a stuck container) rather than only producing an advisory report.

This is the highest-risk workstream. Treat OpenCode and Codex as **reference designs for
the permission/approval model**, but keep NoobBoard's tool surface deliberately narrower
than a general coding agent.

> **Sequencing principle (from the product review): make the LLM more *useful* before more
> *powerful*.** The current safety policy is directionally correct; the gap is presentation
> and workflow, not model authority. Ship Part 1 (presentation + customization) and prove it
> is genuinely helpful to a non-technical user *before* granting any action-taking authority
> in Part 2.

### Part 1 — Customizable LLM access + presentation (lower risk, do first)
- **User-facing presentation.** The compact LLM result should render as a clear
  **headline + plain-English explanation + recommended next step + admin-message status**,
  not a raw diagnostic dump. Add presentation/guided-prompt fields to the response surface
  without changing provider behavior or relaxing redaction. (Ties directly into Workstream
  B's compact home layout.)
- Surface a friendly settings UI over the existing `models.LLMPolicy` map: provider +
  model selection, which roles may use the LLM, per-policy context byte/line limits,
  include-logs toggle, and the (future) web-research toggle described in
  `docs/llm-policy.md`.
- Keep the current flow intact: deterministic collectors → rule engine → incident facts →
  redaction → role-scoped context builder → **strict JSON** diagnosis. Customization tunes
  this path; it does not bypass redaction or role scoping.

**Likely files:** `internal/llm/context.go`, `internal/llm/provider.go`,
`internal/models/models.go`, settings handlers in `internal/server/server.go`,
`web/public/app.js`.

### Part 2 — Agent access (opt-in, fail-closed)
The baseline LLM returns a read-only `Diagnosis` (`internal/llm/schema.go`) with a
`recommended_action_id` — it advises, it does not mutate anything. Agent mode lets the
model call a small, vetted set of **tools** that map onto operations the app already
performs safely. Current baseline also requires a structured `recommended_action_target`;
the server resolves app targets against the admin snapshot before showing an approval
popup, and unresolved app targets stay non-actionable.
The action-approval arm gate is also in place: it is disabled by default, requires
`agent_control_enabled=true`, and arms only the current admin session for a bounded window.
Mutating repair execution remains locked until the explicit tool implementations exist.

**Design, grounded in the references:**
- **Read-only live API tools (implemented first).** Admin-requested diagnosis can opt into
  `noobboard_current_status`, `noobboard_server_status`, `noobboard_network_status`, and
  `noobboard_app_status`. These tools refresh sanitized NoobBoard snapshots through the
  normal collectors and never expose raw API clients, credentials, shell, filesystem,
  Docker control, Unraid mutations, or UniFi configuration mutation. General-user policies
  never receive tools.
- **Future narrow mutation allowlist.** Tools wrap existing audited operations only:
  `get_status`, `get_logs` (bounded + redacted), `docker_restart`, `docker_start`,
  `docker_stop` — all already admin-only, CSRF/audit-gated, and resolved from the
  server-side snapshot. **No shell, no filesystem, no arbitrary commands, no Unraid/UniFi
  config mutation.** This is the key divergence from OpenCode/Codex, which run shell in a
  sandbox; NoobBoard does not need that blast radius.
- **Approval modes** (mirroring Codex's suggest / auto-edit / full-auto and OpenCode's
  per-tool permissions):
  - `propose` (default): agent emits a plan + tool calls; an admin confirms each action.
  - `auto`: only for an admin-defined low-risk allowlist (e.g., restart a single flapping
    container), with per-action rate limits and a global kill switch.
- **Hard gating.** Agent mode is disabled by default and requires (a) an explicit config
  flag *and* (b) a separate per-session "arm" action in the admin UI. It is admin-port and
  admin-role only. Every tool call is audited with full transcript; every executed action
  notifies admins.
- **Bounded agent loop.** incident facts → LLM with tool schema → validate tool call
  against schema (same strictness as `ValidateDiagnosis`) → execute approved tool → feed
  result back → repeat under a hard turn/time budget (Codex-style turn limits). Fail closed
  on schema-validation or redaction failures.
- **Redaction still runs** on everything sent to the model and everything written to
  audit/notifications. The model never receives raw credentials or unrestricted logs.

**Reference notes (from `docs/opencode-evaluation.md` and the policy docs):**
- OpenCode: permission-gated tools, discovery (`websearch`) split from retrieval
  (`webfetch`), MCP as an extension point. NoobBoard keeps web access opt-in and out of
  the default diagnostics path.
- Codex: approval modes + sandbox + turn limits — adopt the approval-mode UX and turn
  budget; skip the general shell sandbox.

OpenCode auto-review package note: useful for a future reviewer-model workflow because it
deduplicates reviewed turns, skips child/review-loop sessions, and asks a different model
family for PASS/FAIL/UNKNOWN evidence. It is not a NoobBoard action-control
implementation. Its examples use `gpt-5.5` with `xhigh` reasoning; this is not proof that
Codex auto-review uses "5.4 Thinking".

**Likely files:** new `internal/llm/agent.go` (runner + tool schema), `internal/llm/
schema.go` (tool-call validation), `internal/server/server.go` (arming + run endpoints,
CSRF), `internal/audit/audit.go`, `internal/config/config.go` (feature flag + allowlist),
`web/public/app.js`. **Must update `docs/llm-policy.md` and `docs/security.md` when this
lands** — those docs currently state the LLM has no repair tools or Docker control.

### Acceptance criteria
- [ ] Part 1: roles/limits/provider/model are configurable from a structured UI; redaction
      and role scoping are provably unchanged.
- [ ] Part 1: LLM settings show an agent-readiness/approval section that distinguishes
      active read-only tools from the planned approval popup, auto-review, and auto-action modes.
- [ ] Part 2: agent mode is off by default; enabling it requires both a config flag and an
      explicit in-app arm step.
- [ ] Tool calls are schema-validated, restricted to the allowlist, audited with transcript,
      and notify admins on execution.
- [ ] `propose` mode requires per-action confirmation; `auto` mode honors the allowlist,
      rate limits, and kill switch.
- [ ] Redaction failures and invalid tool calls fail closed (no action taken).
- [ ] `docs/llm-policy.md` and `docs/security.md` updated to reflect the new capability.
- [ ] `go test ./...`, build, and `visual-check.ps1` all pass.

### Open questions
- Which actions are safe enough for `auto` mode out of the box? Start with single-container
  restart only; require admin opt-in for anything broader.
- Do we expose agent mode to non-admin roles ever? Default answer: no.

---

## Workstream D — Admin panel polish

**Status:** `in-progress` (review written → `docs/ux-admin.md`; implementation not started)
**Goal:** Targeted fixes to the admin surface (port `8787`). The admin panel is allowed to be
dense/technical, so this is a punch-list, not a redesign. Top items from the review: the
floating chat overlaps interactive controls; three overlapping diagnosis entry points; the
Admin tab is raw-JSON (turn the audit log into a filterable table); the overall-status string
is repeated as a subtitle on every tab; mobile header crowding/truncation. Full prioritized
findings, acceptance criteria, harness additions, and file pointers are in **`docs/ux-admin.md`**.

---

## Suggested sequencing

The product review's headline guidance: **freeze backend scope, fix the compact UX, and make
the LLM useful before making it powerful.**

1. **B (compact noob UX)** is the priority — start with B1 (the redesign) using the
   Multi-agent UX workflow, then B2 (customization). Do not add backend features here.
2. **A (setup wizard)** can proceed in parallel; it shares `web/public` and settings
   handlers with B, so coordinate frontend changes.
3. **C Part 1 (LLM presentation + customization)** pairs naturally with B (the compact LLM
   result is part of the home layout) and reuses existing policy structures.
4. **C Part 2 (agent access)** lands last — only after Part 1 proves the LLM is useful, and
   after the audit/notification and approval-UI groundwork exists. It also benefits from the
   setup wizard (A) for provisioning provider credentials.

## References

- **Product review (origin of the UX workflow and compact-UX guidance):** a multi-agent
  design review of the Codex-built app. Key conclusions integrated above — compact app is a
  shared home status remote, split UX-designer vs implementer agent roles, freeze backend
  scope, literacy/semantic audits, "useful before powerful." (Reproduce by giving a UX agent
  the live screenshots + the constraints in Workstream B.)
- OpenCode — providers, tools/permissions, agents, MCP: https://opencode.ai/docs/
- Codex — approval modes, sandbox, login/auth: https://github.com/openai/codex
- Existing internal notes: `docs/opencode-evaluation.md`, `docs/llm-policy.md`,
  `docs/security.md`, `docs/architecture.md`, `docs/api-config.md`.
