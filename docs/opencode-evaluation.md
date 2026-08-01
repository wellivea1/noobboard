# OpenCode Evaluation Notes

Checked against current OpenCode docs on 2026-06-01, the `sst/opencode`
source on 2026-06-03, and the `opencode-auto-review` package source on
2026-06-04. Re-checked against the OpenCode provider docs on 2026-08-01
during the model refresh — see "Model catalogue" below.

## Model catalogue (re-check, 2026-08-01)

OpenCode does not hardcode model ids. It pulls provider and model metadata from
**models.dev**, discovers some providers over OAuth, and gives users blacklist
and whitelist filters over the resulting picker — so model lifecycle is the
catalogue's problem, not OpenCode's.

NoobBoard hardcodes model ids in three places that must agree: the picker in
`web/public/app.js`, the ChatGPT-Codex allowlist in `internal/llm/chatgpt.go`,
and the fallback chain beside it. That is why this list went stale enough to
carry two retired Anthropic ids (`claude-3-7-sonnet-*`, `claude-3-5-haiku-*`)
that now 404, presenting to the admin as an auth failure rather than a removed
model.

**Not adopting the catalogue itself.** Fetching models.dev at startup adds an
outbound dependency on a third party for a LAN-only monitoring tool, on a path
that currently talks to nothing but the configured provider. The failure mode —
no picker when the catalogue is unreachable — is worse than a list that needs
editing twice a year, and it would need its own allowlist and redaction review.

**Worth adopting: one source of truth in-repo.** The three lists should be one.
The server already serves settings, so the natural shape is the Go list as the
source and the picker reading it from an admin endpoint, which also lets the
server reject a saved id it no longer serves. Follow-up, not part of this change.

Relevant ideas worth copying:

- Provider abstraction: OpenCode supports many providers through provider configuration. This dashboard stays narrower: OpenAI and Anthropic remain the only LLM providers, with OpenAI offering API-key auth plus an admin-only ChatGPT browser/headless connector patterned after OpenCode's Codex auth flow. Deterministic development should use fixture telemetry, not a mock LLM answer generator.
- Tool permissions: OpenCode exposes tools such as `webfetch` and `websearch` behind configurable permissions. If this dashboard later adds web access for ChatGPT-style incident research, it should be a separate tool with explicit admin enablement, audit logging, redaction before outbound requests, URL allow/deny controls, and no access to local secret files.
- MCP extensibility: OpenCode supports MCP servers for external tools, but its own docs warn that MCP tools add context. For this app, MCP is a future extension point, not a default path for Unraid/UniFi diagnostics.
- Web access split: copy the distinction between discovery and retrieval. A future implementation should separate search from fetching a specific URL, and the LLM policy should be able to enable one without the other.
- Action guard from source: OpenCode's browser/headless OpenAI connector only handles credentials. Tool safety lives elsewhere: agent/session permission rules decide which tools are advertised, and tool implementations call the permission service again before side effects. NoobBoard copies that split by keeping ChatGPT browser auth as credential transport, putting live API tool access behind LLM policy rules, and showing a separate approval popup before any future automatic fix can run.
- Codex request shape: OpenCode routes ChatGPT-account requests to `https://chatgpt.com/backend-api/codex/responses`, sends the ChatGPT account header plus `originator` and `session-id`, omits max output token overrides, sets `store: false`, streams Responses output, includes `reasoning.encrypted_content` for stateless Responses continuation, and strips stored response item ids unless storage is explicitly enabled.
- Context overflow: OpenCode does not split one oversized request into model-call chunks. It detects overflow against the model context window, compacts/summarizes older session history, prunes old tool output when configured, and retries with a single smaller request. NoobBoard follows the same principle for compact diagnostics by shrinking the structured general-user status report before sending it.
- Current NoobBoard implementation: admin diagnosis can opt into read-only live status tools (`noobboard_current_status`, `noobboard_server_status`, `noobboard_network_status`, `noobboard_app_status`). These tools refresh sanitized NoobBoard snapshots and do not expose shell, filesystem, raw API clients, credentials, Docker control, arbitrary Unraid mutations, or UniFi configuration mutation. General-user policies never receive tools. The compact LLM-only array-start path is a separate signed server action, not an advertised LLM tool.
- Auto-review package: `dzianisv/opencode-plugins/packages/auto-review` is sufficient as a workflow reference for NoobBoard's reviewer-model gate. It listens for completed non-trivial turns, skips aborted/child/review-loop sessions, deduplicates reviewed messages, creates a child `AUTO-REVIEW` session, and asks a different model family to return PASS/FAIL/UNKNOWN evidence. NoobBoard copies only the configurable separate-review idea. Its implementation reviews one concrete server-side app action against redacted live status and allowlisted local reference docs; the server still enforces action allowlists, admin approval or explicit user-control opt-in, replay protection where applicable, auditing, and rate limits.
- Auto-review model note: the package examples use provider-specific high-reasoning settings, and its auto-selection code prefers a different configured model family while ranking stronger model names. This is not evidence that Codex auto-review uses a particular unreleased model family.

Security decision:

- Do not give automatic incident diagnosis open web access by default. The app handles LAN infrastructure state, local API keys, logs, and redacted service names. Web access should be opt-in per policy and fail closed when redaction finds sensitive content.
- Do not treat ChatGPT browser login as an authorization boundary for actions. It proves only that a credential exists; NoobBoard still enforces admin role, CSRF/same-origin on settings, policy allowlists, redaction, and hard tool-call limits.
- Auto-review may be exposed as active only as a fail-closed server-side approval gate. It is not enough by itself to run an action: reviewer denials or provider errors block Docker calls, and all normal admin approval, explicit chat auto-fix request, or explicit user-control opt-in and rate-limit checks still apply.

References:

- https://opencode.ai/docs/providers/
- https://opencode.ai/docs/tools/
- https://opencode.ai/docs/mcp-servers/
- `sst/opencode`: `packages/opencode/src/plugin/openai/codex.ts`
- `sst/opencode`: `packages/opencode/src/provider/transform.ts`
- `sst/opencode`: `packages/opencode/src/session/overflow.ts`
- `sst/opencode`: `packages/opencode/src/session/compaction.ts`
- `sst/opencode`: `packages/opencode/src/permission/index.ts`
- `sst/opencode`: `packages/opencode/src/tool/registry.ts`
- `sst/opencode`: `packages/opencode/src/tool/shell.ts`
- `sst/opencode`: `packages/opencode/src/tool/edit.ts`
- `dzianisv/opencode-plugins`: `packages/auto-review/auto-review.ts`
