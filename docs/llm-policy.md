# LLM Policy

LLM support is provider-based. The default provider is `disabled`, so the app does not fabricate diagnostic answers when no real provider is configured. Real providers are selected with `NOOBBOARD_LLM_PROVIDER=openai` or `NOOBBOARD_LLM_PROVIDER=anthropic`.

OpenAI uses the Responses API with strict structured outputs. Anthropic uses the Messages API with a forced client tool whose `input_schema` matches the diagnosis schema. Both providers are called through Go `net/http`; no SDK or runtime dependency is required.

Environment variables:

- `OPENAI_API_KEY`
- `OPENAI_MODEL`
- `ANTHROPIC_API_KEY`
- `ANTHROPIC_MODEL`

When `NOOBBOARD_LLM_PROVIDER=openai`, `OPENAI_API_KEY` must be present. When `NOOBBOARD_LLM_PROVIDER=anthropic`, `ANTHROPIC_API_KEY` must be present. If the provider or matching key is missing, the API reports diagnostics as unavailable and chat controls are disabled.

The LLM never receives raw credentials, unrestricted logs, arbitrary files, shell access, Docker control, Unraid mutations, UniFi configuration access, or repair tools.

The app follows this flow:

Raw telemetry -> deterministic collectors -> diagnostic rule engine -> incident facts -> blacklist/redaction layer -> role-specific LLM context builder -> strict JSON diagnosis -> audit/notification pipeline.

OpenCode design note: OpenCode separates discovery (`websearch`) from retrieval (`webfetch`) and permission-gates both. If this dashboard ever gets web-backed research, it should be a separate admin-only feature with explicit source domains, audit entries, rate limits, and no access to raw private telemetry. Web access is intentionally not exposed in the current diagnostics path.
