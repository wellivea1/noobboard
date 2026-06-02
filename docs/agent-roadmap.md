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
   the compact port.
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
4. **Accounts step.** Force replacement of the bootstrap `admin` password, then create
   named admin/general users. Reuse `internal/users` (PBKDF2 hashing already there).
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

### Acceptance criteria
- [ ] Fresh install (empty data dir) lands on the wizard; completing it yields a working
      live dashboard with no manual env/config editing.
- [ ] Connection-test and LLM-key validation give clear pass/fail feedback.
- [ ] After completion the wizard is unreachable; re-running the binary goes straight to
      login.
- [ ] No secret is written to a git-tracked path or returned in any API response.
- [ ] `go test ./...`, build, and `visual-check.ps1` all pass.

### Open questions
- Where should key files live by default (repo `secrets/` for dev vs `%ProgramData%\
  NoobBoard\secrets` for the Windows service)? Pick per-deployment, document in
  `docs/deployment-windows.md`.
- Should phase-2 web login be limited to providers that actually support non-API-key
  subscription auth? Confirm current provider capabilities before building.

---

## Workstream B — Ease of UI customization on the noob end

**Status:** `not-started`
**Goal:** Let an admin (and optionally the general user) tailor the compact/"noob" view
through structured controls instead of JSON editors.

### Scope
- **Structured compact-view editor** replacing raw JSON for the common cases: choose
  visible apps, reorder them (drag-and-drop), set friendly display names, pick icons,
  toggle the NAS/WAN status tiles, and toggle chat/LLM availability for general users.
- **Icon picker** sourced from the bundled `web/public/app-icons/*.svg`, Docker-label
  icons, and admin icon-override URLs. Reuse the existing URL sanitization rules
  (unsupported schemes / embedded credentials rejected — see `docs/security.md`).
- **Live preview** of the compact surface while editing.
- **Per-device vs shared.** Today, overview monitor visibility/order are client-side
  localStorage prefs (per admin device). Decide explicitly which noob-view settings are
  shared runtime settings (persisted, affect all users) vs per-device prefs, and label
  them in the UI so the distinction is obvious.

### Constraints
- All persisted mutations stay admin-only, CSRF-protected, and audited; the compact router
  gains no admin endpoints.
- Build on existing models: `models.VisibilitySettings`, the app catalog / icon overrides
  (`config.AppCatalogConfig`), and per-role app visibility. Avoid inventing parallel
  settings state.
- Keep the JSON editors as an "advanced" fallback — the structured UI is additive, not a
  replacement that drops capability.

### Likely files
`web/public/app.js`, `web/public/styles.css`, `web/public/index.html`,
`internal/models/models.go`, settings handlers in `internal/server/server.go`,
`internal/db/store.go`.

### Acceptance criteria
- [ ] An admin can curate the noob view (visible apps, order, names, icons, tiles, chat)
      without touching JSON.
- [ ] Changes persist, survive restart, and reflect on the compact port.
- [ ] Shared vs per-device settings are clearly distinguished in the UI.
- [ ] Icon inputs are sanitized; bad URLs/schemes are rejected with feedback.
- [ ] `go test ./...`, build, and `visual-check.ps1` all pass.

---

## Workstream C — Customizable LLM access + optional full agent access

**Status:** `not-started`
**Goal:** (1) Make LLM access easy to customize per role/policy, and (2) add an **opt-in,
manually enabled** "agent mode" where the LLM can *act* to resolve problems (e.g., restart
a stuck container) rather than only producing an advisory report.

This is the highest-risk workstream. Treat OpenCode and Codex as **reference designs for
the permission/approval model**, but keep NoobBoard's tool surface deliberately narrower
than a general coding agent.

### Part 1 — Customizable LLM access (lower risk, do first)
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

### Part 2 — Full agent access (opt-in, fail-closed)
Today the LLM returns a read-only `Diagnosis` (`internal/llm/schema.go`) with a
`recommended_action_id` — it advises, it does not act. Agent mode lets the model call a
small, vetted set of **tools** that map onto operations the app already performs safely.

**Design, grounded in the references:**
- **Narrow tool allowlist.** Tools wrap existing audited operations only:
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

**Likely files:** new `internal/llm/agent.go` (runner + tool schema), `internal/llm/
schema.go` (tool-call validation), `internal/server/server.go` (arming + run endpoints,
CSRF), `internal/audit/audit.go`, `internal/config/config.go` (feature flag + allowlist),
`web/public/app.js`. **Must update `docs/llm-policy.md` and `docs/security.md` when this
lands** — those docs currently state the LLM has no repair tools or Docker control.

### Acceptance criteria
- [ ] Part 1: roles/limits/provider/model are configurable from a structured UI; redaction
      and role scoping are provably unchanged.
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

## Suggested sequencing

1. **A (setup wizard)** and **B (noob UI)** are largely independent and can proceed in
   parallel; both touch `web/public` and settings handlers, so coordinate frontend changes.
2. **C Part 1 (LLM customization)** can start anytime; it reuses existing policy structures.
3. **C Part 2 (agent access)** should land last and only after the audit/notification and
   approval-UI groundwork from Part 1 exists. It also benefits from the setup wizard (A)
   for provisioning the provider credentials it relies on.

## References

- OpenCode — providers, tools/permissions, agents, MCP: https://opencode.ai/docs/
- Codex — approval modes, sandbox, login/auth: https://github.com/openai/codex
- Existing internal notes: `docs/opencode-evaluation.md`, `docs/llm-policy.md`,
  `docs/security.md`, `docs/architecture.md`, `docs/api-config.md`.
