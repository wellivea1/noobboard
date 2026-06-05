# Plan: make the LLM reliably request tools + propose the auto-fix

## Symptom
When an app is down, the chat agent **does not reliably request read-only tool
access or propose the restart auto-fix**. It often returns
`recommended_action_id: none` / `ask_admin_to_check`, so no approval popup /
"Restart now" affordance appears, even when the app is repairable.

## Root causes (confirmed in code on main, build+tests green)

1. **Read-only status tools are OFF by default in every policy.**
   `internal/config/config.go` `defaultLLMPolicies()` sets `AgentToolsEnabled:
   false` for `admin_requested` (line ~277), `general_user_requested` (~297),
   and the others. With tools disabled, `buildContext` selects the *no-tools*
   instruction, which literally says **"Do not request tools"** (`context.go`
   ~81). So out of the box the model has nothing to request → "doesn't request
   tool access."

2. **The model is never told which apps are repairable.** The admin `apiReport`
   app entries and `generalAppReport` (`context.go` ~490–510, ~690–710) include
   `current_status`, `docker_state`, `endpoint_status`, but **not**
   `agent_repair_allowed`, `restart_allowed_general_user`, or any derived
   "restart candidate" flag. The model has no signal that a restart is an
   available, allowlisted action for a given app, so it falls back to advisory.

3. **No positive trigger for the restart action; contradictory framing.** The
   instructions (`context.go` ~81–83) and `Instructions()`/`AgentInstructions()`
   (~867–890) are purely prohibitive — "Never claim you can repair the system or
   execute actions", "do not execute actions" — and never say *when* to choose
   `ask_admin_to_restart_container`. This biases the model toward
   `ask_admin_to_check` / `none`.

4. **The action enum has no descriptions.** `schema.go` (~149) lists the
   `recommended_action_id` enum with no per-value `description`, so the model has
   no guidance on selection. With strict structured output, descriptions
   materially improve selection reliability.

5. **General-user diagnosis forbids recommending actions.** The general-user
   instruction (`context.go` ~301) says "…or recommend anything beyond telling
   the admin." So the general-user chat never proposes a fix, even though
   request-repair (#32) and direct general-user controls (#38) now exist.

6. **Coherence gap:** enabling `agent_control_enabled` (repair execution) does
   **not** enable `AgentToolsEnabled` (read-only status tools); they are
   independent flags, so an admin who turns on auto-repair still gets a model
   that can't refresh status or is told not to request tools.

## Plan for Codex (PR breakdown)

### AF1 — Enable + guide read-only tools and the restart proposal (admin)
- Default `admin_requested` policy to `AgentToolsEnabled: true` with
  `AgentMaxToolCalls: 2` (still admin-only, read-only, bounded, audited — the
  tools in `internal/llm/agent.go` are status-fetch only). Keep general-user
  tools off. Add a migration note so existing runtime settings pick up the new
  default (or surface it as a one-line settings default).
- Auto-enable (or strongly hint) read-only tools when `agent_control_enabled` is
  on, so the two flags don't diverge.
- Rewrite the admin instruction + `Instructions()`/`AgentInstructions()` to add a
  **positive trigger** and drop the contradictory "cannot repair/execute"
  language, while keeping the hard prohibitions. Target wording:
  > "You may call the read-only status tools to confirm live status before
  > answering. You cannot execute anything yourself, but NoobBoard can run **one
  > app restart** after admin approval. When a specific app is offline, exited,
  > or unhealthy and a restart is a reasonable first remediation, set
  > `recommended_action_id = ask_admin_to_restart_container` and
  > `recommended_action_target.kind = app` with that app's exact `app_id`. Prefer
  > this over `ask_admin_to_check` whenever a restart is plausibly corrective.
  > Never recommend logs/shell/storage/array/firewall/Docker-removal/UniFi
  > changes; those are not executable."

### AF2 — Surface repair-eligibility + an actionable signal in the report
- Add to the admin app report: `agent_repair_allowed` and a derived
  `restart_candidate` (true when status ∈ {offline, degraded} or `docker_state`
  ∈ {exited, unhealthy}). Keep `docker_state`/`endpoint_status`.
- Add to `generalAppReport`: `restart_allowed_general_user` and
  `repair_requestable` so the general-user path can propose request/direct fix.
- These are non-sensitive booleans; confirm they pass redaction + the
  general-user field stripping rules.

### AF3 — Schema descriptions for the action enum
- In `schema.go JSONSchema()`, add a `description` to `recommended_action_id`
  spelling out when each value applies — especially
  `ask_admin_to_restart_container` ("a specific app is down/unhealthy and a
  restart may fix it; set target to that app") and `ask_admin_to_check` ("manual
  investigation only; no executable fix"). Optionally add a `description` to
  `recommended_action_target`.

### AF4 — Let the general-user diagnosis propose a fix
- Update the general-user instruction (`context.go` ~301) to allow proposing the
  one allowlisted remediation in plain language when a visible app is down and is
  `repair_requestable` (or directly repairable): e.g. "If a visible app isn't
  working and can be fixed, you may suggest asking an admin to fix it — or
  restarting it if the household is allowed — in plain, non-technical language."
- Ensure the general-user `diagnose` response carries the matching affordance
  (today `server.go diagnose` attaches `agent_plan` only for admin). Align with
  the request-repair (#32) / direct-control (#38) flows so the compact chat
  surfaces "Ask an admin to fix this" / "Restart now" bound to the recommended
  app.

### AF5 — Server-side backstop (reliability safety net)
- If a diagnosis identifies exactly one repair-eligible app that is down but the
  model returned `none`/`ask_admin_to_check`, still surface the restart
  affordance for that app, clearly labeled "suggested". This prevents a single
  missed proposal from blocking the fix. **No execution-gate changes** — the
  admin still approves/arms; general users still request/opt-in.

### AF6 — Tests + eval
- Deterministic (no live LLM): assert the built diagnosis context for an
  exited + repair-allowed fixture app includes the repair-eligibility fields and
  the restart guidance text, and that tools are attached when the policy enables
  them.
- Optional offline eval: run the real configured model against a down-app fixture
  N times and report the `ask_admin_to_restart_container` proposal rate before/
  after, so we can measure the improvement rather than guess.
- Update `docs/llm-policy.md` (tools default-on for admin) and `docs/security.md`
  (read-only tools enabled by default for admin diagnosis; still bounded/audited).

## Notes / guardrails (unchanged)
- All execution gates stay exactly as-is: admin approval + arm + single-use token
  + restart-only allowlist + per-app opt-in + cooldown/rate-limit + optional
  auto-review + audit. This plan only makes the model **propose** the fix more
  reliably and lets it use the existing **read-only** tools; it does not widen
  what can execute or who can execute it.
- General-user tools stay **off**; the general-user side gains only plain-language
  *proposals* that route through the existing request/opt-in paths.
