# OpenCode Evaluation Notes

Checked against current OpenCode docs on 2026-06-01.

Relevant ideas worth copying:

- Provider abstraction: OpenCode supports many providers through provider configuration. This dashboard should stay narrower: direct OpenAI and Anthropic API calls only; deterministic development should use fixture telemetry, not a mock LLM answer generator.
- Tool permissions: OpenCode exposes tools such as `webfetch` and `websearch` behind configurable permissions. If this dashboard later adds web access for ChatGPT-style incident research, it should be a separate tool with explicit admin enablement, audit logging, redaction before outbound requests, URL allow/deny controls, and no access to local secret files.
- MCP extensibility: OpenCode supports MCP servers for external tools, but its own docs warn that MCP tools add context. For this app, MCP is a future extension point, not a default path for Unraid/UniFi diagnostics.
- Web access split: copy the distinction between discovery and retrieval. A future implementation should separate search from fetching a specific URL, and the LLM policy should be able to enable one without the other.

Security decision:

- Do not give automatic incident diagnosis open web access by default. The app handles LAN infrastructure state, local API keys, logs, and redacted service names. Web access should be opt-in per policy and fail closed when redaction finds sensitive content.

References:

- https://opencode.ai/docs/providers/
- https://opencode.ai/docs/tools/
- https://opencode.ai/docs/mcp-servers/
