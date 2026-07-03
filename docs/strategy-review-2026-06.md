# Strategy review — goals adherence, management UX, LLM expansion (June 2026)

A top-to-bottom review of the app against its stated goals (`AGENTS.md`,
`docs/agent-roadmap.md`), with a prioritized plan for optimizations and
direction changes. Grounded in the current `main` (post-#61), the June GUI
review, the `docs/ux-admin.md` punch list, and code inspection of the LLM
plumbing.

---

## Part 1 — Adherence to goals

### What's holding (verified)

The core identity — *shared home status remote + dense admin panel, two ports,
plain language, redaction-first, gated mutations* — is intact and actively
enforced:

- Two-surface separation, banned-vocabulary audit, ≥44px targets, and overflow
  checks are enforced by `cmd/visualcheck` on every UI change, not just by
  convention.
- The compact surface now genuinely answers the four questions (hero card,
  plain-language app rows, Notify admin, per-app notification opt-ins,
  technical-details disclosure, app/infra detail pages with history).
- The repair stack is a faithful implementation of the roadmap's safety
  posture: closed-set recommendations, server-side target resolution,
  approval/reviewer/opt-in/cooldown gates, full audit. No guardrail
  violations found.

### Deviations found

**D1 — Sequencing inversion (the big one).** The roadmap's headline guidance
was *"make the LLM useful before making it powerful"* — C Part 1
(presentation/customization) before C Part 2 (agent authority), and Part 2
"lands last" after A (wizard) and B (compact UX). In practice, **Part 2 is
near-complete** (approval-gated repair, request-scoped autonomous repair,
safety reviewer, array-start path, general-user direct controls) while:
- **Workstream A (setup wizard) has not started** — a new deployment still
  requires installer prompts + env/config editing; the "usable without
  hand-editing config" goal is unmet.
- **B2 (customization editor) has not started** — no friendly rename,
  reorder, icon picker, or live preview; visibility is checkbox-only.
- C Part 1's structured-settings checkbox is still open.

**Direction change:** declare the agent-authority surface **feature-frozen**
(it is complete for v1: propose + auto + reviewer + limits; no new mutation
types) and swing effort to A → B2 → C Part 1 presentation. Rationale: every
remaining high-value item is about *adoption and daily usefulness*, not
capability; more authority now adds risk with no user-visible payoff.

**D2 — Roadmap statuses have drifted from reality.** The doc understates
progress in some places and overstates in others, which misleads agents
planning work:
- Workstream B says "implementation not started" — B1 and B3 are demonstrably
  shipped (hero layout, drawer, semantic audits, plain-language mapping).
- Workstream D says "implementation not started" — several `ux-admin.md`
  items shipped via the June GUI pass (audit table, settings tabs, mobile
  header, toast placement, control chrome).
- Workstream C's last acceptance box ("once Edge-based visual verification is
  available") is stale — the Edge harness runs and passes today.
*(Fixed in this PR: status lines updated to match reality.)*

**D3 — A dead admin control.** The `automatic_incident` LLM policy exists in
config and is presented in settings ("Automatic incident summary — reserved
for background incident explanation and notifications"), but **no code path
ever invokes it** (`ModeAutomaticIncident` has zero call sites). An admin can
enable and tune a policy that does nothing. Either wire it (see E2 — the
recommended path) or remove the toggle until it works; a dead switch breaks
the "structured settings are honest" principle.

**D4 — LLM usefulness is reactive-only.** The stated compact-LLM job is to
"translate system state into plain English and a clear next step." Today that
only happens when a user *asks*. The poller detects incidents around the
clock, but nothing explains them proactively — notifications carry no
plain-English "what happened / what to do." This is the largest gap between
the app's stated purpose and its behavior (and D3 is the mechanism meant to
close it).

---

## Part 2 — Management interface usability plan

The admin panel is allowed to be dense; these are targeted fixes. Ordered by
value.

**M1 — Consolidate diagnosis entry points** *(ux-admin P1.2, still open)*.
Three parallel surfaces remain: top-right Diagnostics button, the floating
"Ask me for help!" dock, and the Diagnostics tab. Make the top-right button a
jump-to-tab, keep the dock as the single persistent launcher, and hide the
dock when no provider is configured *(ux-admin P1.1 second half)*.

**M2 — Fix the floating chat overlap** *(ux-admin P1.1, still open)*. The
dock still overlaps Role Access controls on Settings. Reserve bottom padding
on scrollable admin content (safe-area style) so the dock never covers
interactive controls.

**M3 — Sticky save + dirty state on Settings.** The LLM section alone is
~2,200px with one small Save at the bottom. Make the save row sticky with an
"unsaved changes" indicator; warn on tab-switch with dirty state. (June
review #20, deferred then.)

**M4 — Review Queue count badge.** Pending standard-user repair requests are
invisible until the tab is opened. Show a count badge on the Review Queue nav
item (data already in the admin snapshot path) so requests get noticed.

**M5 — Inactive-provider callout** *(June #13, Codex-validated approach)*.
When Provider = Disabled, show one clear callout ("Diagnosis is inactive
until a provider is selected") above the LLM section instead of a fully
interactive form that silently does nothing. Keep the form editable.

**M6 — Small leftovers from the June review:** drawer double-border band
(#5), focus-vs-active nav distinction (#4), page-contextual subtitles
(ux-admin P2.4), incident evidence as labeled chips (P3.10).

**M7 — Settings text filter (new, cheap).** A single filter box above the
settings sections that hides non-matching rows. The settings surface has
grown ~10× since the tabs were designed; finding "cooldown" or "reviewer"
now takes scrolling. One input + a `hidden` toggle per labeled row.

---

## Part 3 — LLM connector expansion plan

Ordered by value ÷ risk. E1–E3 are the recommended near-term slice; all keep
the existing trust boundary (strict JSON, redaction, fail-closed, no new
mutation authority).

**E1 — OpenAI-compatible base URL → local models (Ollama / LM Studio /
llama.cpp).** Both provider endpoints are hardcoded
(`api.openai.com`, `api.anthropic.com` in `internal/llm/{openai,anthropic}.go`).
Add `llm.openai_base_url` (validated `http(s)`, loopback/LAN allowed, default
unchanged) so the existing OpenAI client can point at any OpenAI-compatible
server. For a **local-first** app this is the single most on-brand expansion:
household diagnosis with no cloud dependency, no API cost, and no telemetry
leaving the LAN. Strict-JSON validation already fails closed when a weaker
model returns malformed output — document that requirement. Small, contained
change (client constructor + config + settings field + docs).

**E2 — Wire `automatic_incident` into the poller (proactive explanations).**
When the poller's rule engine opens a *new* incident, run the (currently
dead) `automatic_incident` policy once to produce a plain-English
headline/explanation/next-step, attach it to the notification pipeline and
the compact hero, and cache it on the incident. Bounds: one call per incident
ID, global rate limit (e.g. 6/hour), skip when provider disabled, redaction
as usual, audited (`llm.auto_incident`). This converts the LLM from
ask-only to the "translate state into plain English" role the goals assign
it — and un-deads the D3 toggle.

**E3 — History-aware diagnosis.** The status-history store
(`history.jsonl`) is never used in LLM context (zero references in
`internal/llm`). Add (a) a bounded per-subject transition summary to the
context builder (last N events + uptime %, display names only), and (b) a
read-only `noobboard_app_history` tool alongside the existing four. Unlocks
the diagnosis class users actually want: *"Emby has dropped 4 times this
week, mostly overnight — likely a scheduled task or resource issue."* Data
exists; notes are already redacted.

**E4 — Read-only `noobboard_app_logs` tool.** Already named in the roadmap's
future list. Bounded, redacted, admin-policy-only, same per-tool allowlist
machinery as the existing tools. Biggest diagnosis-quality jump for admin
chat (models can see *why* a container is failing, not just that it exited).

**E5 — Multi-turn chat memory (bounded).** Every question is single-shot
today; follow-ups ("what about now?", "and the other one?") lose all
context. Keep the last N Q/A pairs per session server-side (in-memory,
redacted, per-policy byte budget, admin + compact). Medium effort; big
perceived-intelligence gain.

**E6 — Reviewer/fallback polish (small).** Generalize the ChatGPT-connector
model-fallback pattern (400 → next configured model) to API-key mode;
surface reviewer model/latency in the approval audit detail for
debuggability.

**E7 — Web research: keep deferred.** `docs/llm-policy.md` already sketches
the right shape (admin-only, domain-allowlisted, audited, separate from
diagnostics). Building it now contradicts the useful-before-powerful
posture; revisit after E1–E4 land.

**Explicit non-goals (direction):** no Anthropic OAuth (no supported
subscription-auth flow comparable to the ChatGPT connector — don't build on
an unsupported path), no MCP surface, no new mutation tools (stop, UniFi,
storage beyond the existing array-start), no shell — unchanged from the
roadmap.

---

## Suggested implementation order

| Slice | Contents | Why first |
|---|---|---|
| 1 | D2/D3 hygiene: roadmap statuses (this PR), remove-or-wire decision on `automatic_incident` (recommend wire = E2) | Honesty of docs + settings |
| 2 | E1 local-model base URL + M5 inactive callout | Highest value/effort ratio; pairs naturally in the same settings section |
| 3 | E2 proactive incident summaries | Closes the biggest purpose gap (D4) |
| 4 | M1–M3 admin usability (entry points, overlap, sticky save) | Daily-driver friction |
| 5 | E3 history-aware diagnosis + E4 logs tool | Diagnosis depth |
| 6 | Workstream A setup wizard (per existing roadmap spec) | Largest remaining roadmap gap; benefits from E1 (provider step gains a "local" option) |
| 7 | B2 customization editor, E5 chat memory, M4/M6/M7 | Next wave |

Each slice is independently shippable; none expands agent authority.
