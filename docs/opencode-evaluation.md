# OpenCode Evaluation Notes

Checked against current OpenCode docs on 2026-06-01 and the `sst/opencode`
source on 2026-06-03.

Relevant ideas worth copying:

- Provider abstraction: OpenCode supports many providers through provider configuration. This dashboard stays narrower: OpenAI and Anthropic remain the only LLM providers, with OpenAI offering API-key auth plus an admin-only ChatGPT browser/headless connector patterned after OpenCode's Codex auth flow. Deterministic development should use fixture telemetry, not a mock LLM answer generator.
- Tool permissions: OpenCode exposes tools such as `webfetch` and `websearch` behind configurable permissions. If this dashboard later adds web access for ChatGPT-style incident research, it should be a separate tool with explicit admin enablement, audit logging, redaction before outbound requests, URL allow/deny controls, and no access to local secret files.
- MCP extensibility: OpenCode supports MCP servers for external tools, but its own docs warn that MCP tools add context. For this app, MCP is a future extension point, not a default path for Unraid/UniFi diagnostics.
- Web access split: copy the distinction between discovery and retrieval. A future implementation should separate search from fetching a specific URL, and the LLM policy should be able to enable one without the other.
- Action guard from source: OpenCode's browser/headless OpenAI connector only handles credentials. Tool safety lives elsewhere: agent/session permission rules decide which tools are advertised, and tool implementations call the permission service again before side effects. NoobBoard copies that split by keeping ChatGPT browser auth as credential transport and putting live API tool access behind LLM policy rules.
- Codex request shape: OpenCode routes ChatGPT-account requests to `https://chatgpt.com/backend-api/codex/responses`, sends the ChatGPT account header plus `originator` and `session-id`, omits max output token overrides, sets `store: false`, includes `reasoning.encrypted_content` for stateless Responses continuation, and strips stored response item ids unless storage is explicitly enabled.
- Context overflow: OpenCode does not split one oversized request into model-call chunks. It detects overflow against the model context window, compacts/summarizes older session history, prunes old tool output when configured, and retries with a single smaller request. NoobBoard follows the same principle for compact diagnostics by shrinking the structured general-user status report before sending it.
- Current NoobBoard implementation: admin diagnosis can opt into read-only live status tools (`noobboard_current_status`, `noobboard_server_status`, `noobboard_network_status`, `noobboard_app_status`). These tools refresh sanitized NoobBoard snapshots and do not expose shell, filesystem, raw API clients, credentials, Docker control, Unraid mutations, or UniFi configuration mutation. General-user policies never receive tools.

Security decision:

- Do not give automatic incident diagnosis open web access by default. The app handles LAN infrastructure state, local API keys, logs, and redacted service names. Web access should be opt-in per policy and fail closed when redaction finds sensitive content.
- Do not treat ChatGPT browser login as an authorization boundary for actions. It proves only that a credential exists; NoobBoard still enforces admin role, CSRF/same-origin on settings, policy allowlists, redaction, and hard tool-call limits.

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
