# LLM Policy

LLM support is provider-based. The default provider is `disabled`, so the app does not fabricate diagnostic answers when no real provider is configured. Real providers are selected with `NOOBBOARD_LLM_PROVIDER=openai` or `NOOBBOARD_LLM_PROVIDER=anthropic`.

OpenAI API-key mode uses the Responses API with strict structured outputs. OpenAI ChatGPT connector mode follows the Codex/OpenCode-style OAuth pattern: an admin starts browser or device-code login, NoobBoard stores the returned refresh/access tokens as write-only local runtime settings, refreshes access tokens server-side, and sends diagnosis requests to the ChatGPT Codex responses endpoint with the saved account id. Browser login must use OpenAI's registered loopback callback (`http://localhost:1455/auth/callback`), so it only works when the admin page is opened as `localhost` on the NoobBoard host. LAN devices use the code login, which displays the OpenAI device code in the connection dialog and does not depend on a local redirect. Anthropic uses the Messages API with a forced client tool whose `input_schema` matches the diagnosis schema. All providers are called through Go `net/http`; no SDK or runtime dependency is required.

Environment variables:

- `OPENAI_API_KEY`
- `OPENAI_MODEL`
- `NOOBBOARD_OPENAI_AUTH_METHOD=api_key|chatgpt_browser|chatgpt_headless`
- `NOOBBOARD_CHATGPT_REFRESH_TOKEN`
- `NOOBBOARD_CHATGPT_ACCESS_TOKEN`
- `NOOBBOARD_CHATGPT_ACCOUNT_ID`
- `ANTHROPIC_API_KEY`
- `ANTHROPIC_MODEL`

When `NOOBBOARD_LLM_PROVIDER=openai`, either `openai_auth_method=api_key` with `OPENAI_API_KEY` or `openai_auth_method=chatgpt_browser|chatgpt_headless` with saved ChatGPT connector tokens must be present. When `NOOBBOARD_LLM_PROVIDER=anthropic`, `ANTHROPIC_API_KEY` must be present. If the provider or matching credential is missing, the API reports diagnostics as unavailable and chat controls are disabled.

For OpenAI, the provider remains `openai`; `openai_auth_method` selects API key, ChatGPT browser login, or ChatGPT headless login. Settings reads return only booleans such as `openai_api_key_set` and `chatgpt_connected`; raw API keys, refresh tokens, access tokens, and account ids are never returned.

The LLM never receives raw credentials, unrestricted logs, arbitrary files, shell access, Docker control, Unraid mutations, UniFi configuration mutation, or repair tools.

ChatGPT connector tokens are credentials. They are admin-only, audited on connect/clear, and must not be displayed, logged, sent to notifications, or included in LLM context.

Every diagnosis request collects a fresh NoobBoard snapshot from the configured collectors before building LLM context. In `live` or `mixed` integration mode, that means the LLM receives current read-only Unraid, UniFi, Docker, and probe status fields that pass role visibility and redaction. The compact/general-user context stays bounded by sending a concise API report with source health and selected visible apps instead of the full raw snapshot.

Admin-requested diagnosis can optionally enable read-only agent tools for live status lookups. The browser/headless ChatGPT connector does not grant those tools by itself; it only supplies credentials. Tool access is controlled by `LLMPolicy.agent_tools_enabled`, `agent_max_tool_calls`, and `agent_tool_rules`, is denied for non-admin recipients, and is limited to these read-only tools:

- `noobboard_current_status`
- `noobboard_server_status`
- `noobboard_network_status`
- `noobboard_app_status`

Tool calls refresh sanitized NoobBoard snapshots through the normal collectors, then run role filtering and redaction before results are returned to the model. The tool loop has a hard call budget and fails closed on unknown tools, invalid arguments, collector errors, schema failures, or oversized/redaction-failed context. Mutating repair tools remain intentionally unimplemented.

The app follows this flow:

Raw telemetry -> deterministic collectors -> diagnostic rule engine -> incident facts -> blacklist/redaction layer -> role-specific LLM context builder -> strict JSON diagnosis -> audit/notification pipeline.

OpenCode design note: OpenCode separates credential transport from tool authorization. Its OpenAI/Codex plugin handles OAuth/device-code tokens, while its permission and tool layers decide which tools are advertised and enforce checks again before side effects. NoobBoard follows that model with a narrower tool set. OpenCode also separates discovery (`websearch`) from retrieval (`webfetch`) and permission-gates both. If this dashboard ever gets web-backed research, it should be a separate admin-only feature with explicit source domains, audit entries, rate limits, and no access to raw private telemetry. Web access is intentionally not exposed in the current diagnostics path.
