# API And Config

Commands:

- `noobboard.exe serve`
- `noobboard.exe check-config`
- `noobboard.exe install-service`
- `noobboard.exe uninstall-service`
- `noobboard.exe start-service`
- `noobboard.exe stop-service`
- `noobboard.exe run-once`
- `noobboard.exe version`

Initial routes:

Admin routes are registered on the admin site/port. The compact site/port serves only login, health, summary, app-list, notification, and user diagnosis routes; `/api/admin/*` is not available there.

- `GET /healthz`
- `GET /api/status/summary`
- `POST /api/status/refresh`
- `GET /api/apps`
- `GET /api/apps/{id}`
- `GET /api/apps/{id}/history`
- `GET /api/infrastructure/history?subject=internet|dns|wan|nas|unraid_array`
- `GET /api/admin/status/full`
- `GET /api/admin/incidents`
- `GET /api/admin/audit`
- `GET /api/admin/users`
- `POST /api/admin/users`
- `POST /api/admin/apps/{id}/icon`
- `POST /api/admin/apps/{id}/action`
- `GET /api/admin/apps/{id}/logs`
- `POST /api/admin/diagnose`
- `POST /api/admin/agent/arm`
- `POST /api/admin/agent/approval`
- `GET /api/admin/repair-requests`
- `POST /api/admin/repair-requests/{id}/decision`
- `POST /api/user/diagnose`
- `POST /api/user/notify-admin`
- `GET /api/user/repair-requests`
- `POST /api/user/repair-request`
- `POST /api/user/apps/{id}/restart`
- `POST /api/user/apps/{id}/action`
- `GET /api/user/notification-preferences`
- `POST /api/user/notification-preferences`
- `GET /api/user/notifications`
- `GET /api/admin/settings/visibility`
- `POST /api/admin/settings/visibility`
- `GET /api/admin/settings/roles`
- `POST /api/admin/settings/roles`
- `GET /api/admin/settings/blacklist`
- `POST /api/admin/settings/blacklist`
- `GET /api/admin/settings/apps`
- `POST /api/admin/settings/apps`
- `GET /api/admin/settings/llm`
- `POST /api/admin/settings/llm`
- `POST /api/admin/settings/llm/openai/browser/start`
- `POST /api/admin/settings/llm/openai/browser/finish`
- `POST /api/admin/settings/llm/openai/headless/start`
- `POST /api/admin/settings/llm/openai/headless/poll`
- `GET /api/admin/settings/integrations`
- `POST /api/admin/settings/integrations`
- `GET /api/admin/settings/notifications`
- `POST /api/admin/settings/notifications`

Admin settings writes require the `X-CSRF-Token` returned by login. Successful writes are persisted in the JSON database under `runtime_settings` and applied immediately to the in-memory redactor, LLM client, notification manager, and visibility policy.

Docker app operations are admin-only. `POST /api/admin/apps/{id}/action` requires `X-CSRF-Token` and accepts `{"action":"start"}`, `{"action":"stop"}`, or `{"action":"restart"}`. `stop` and `restart` are disruptive and additionally require `{"confirmed":true,"confirm_app_id":"<resolved app_id>"}`; the server verifies that `confirm_app_id` matches the app resolved from the current snapshot before calling Docker. `GET /api/admin/apps/{id}/logs?limit=80` returns redacted Docker log lines and clamps `limit` to `1..200`. Both routes resolve `{id}` against the current admin snapshot and use a `container:` object ID or safe container name from that snapshot; clients do not submit raw Unraid object IDs. Restart prefers Unraid's native Docker restart mutation when available and falls back to stop/start only when that mutation is not present in the GraphQL schema. Log audit records store only metadata such as app ID, container name, line count, and whether redaction occurred. Live Unraid mode requires an API key with Docker permissions.

LLM repair is narrower than manual Docker control. `POST /api/admin/agent/approval` accepts a server-issued `approval_token` plus `choice:"deny"` or `choice:"allow_once"`. `allow_once` can execute only `ask_admin_to_restart_container`, only when the current admin session has temporary fix access enabled through the legacy route `POST /api/admin/agent/arm`, and only for a currently resolved app that has `app_catalog.agent_repair_allowed["<app id>"]=true`. Tokens are short-lived and single-use; non-restart recommendations, sessions without fix enablement, hidden/replaced targets, blacklisted apps, missing app opt-in, token replay, the per-app 10-minute cooldown, and the global 5/hour repair limit are refused before Docker is called. Successful approvals return the Docker `result` plus an `outcome` object with `before_status`, `after_status`, `recovered`, `verified`, and a plain-language message; verification polls NoobBoard status and does not auto-retry.

Admin diagnosis can run the same repair autonomously only when `agent_auto_repair_enabled=true`, `agent_control_enabled=true`, `action_auto_review_enabled=true`, the current admin session has temporary fix access enabled, the target app is opted in, and the target app is currently not online. The model still supplies only the schema `recommended_action_id` and target hint; the server resolves and executes the restart. Reviewer denial/failure, missing opt-in, an online target, cooldown, or rate limit prevents Docker control. Successful autonomous repair returns an `agent_plan` with `auto_executed:true`, no approval token, and the same `outcome` shape.

General-user repair requests are request-only. `POST /api/user/repair-request` accepts a visible `app_id`, `action_id:"ask_admin_to_restart_container"`, and optional `diagnosis_summary`; it stores a pending request and notifies admins through the configured notification backend. It does not start, stop, restart, or enable an admin session. Admins review requests with `GET /api/admin/repair-requests` and `POST /api/admin/repair-requests/{id}/decision` using `choice:"approve"` or `choice:"deny"`. Approval still requires the same admin-session fix enablement, per-app `agent_repair_allowed` opt-in, blacklist, optional safety-reviewer approval, cooldown, and global rate-limit checks before Docker is called. Request outcomes are stored on the request and can be read by the requester through `GET /api/user/repair-requests`.

Direct general-user app controls are separate from admin-session LLM/agent action. `POST /api/user/apps/{id}/action` accepts `{"action":"start"|"stop"|"restart","confirmed":true,"confirm_app_id":"<resolved app_id>"}`; `POST /api/user/apps/{id}/restart` is kept as a compatibility alias for restart. These routes are available only for apps visible to that user, only when `app_catalog.general_user_restarts_enabled=true`, only when that app also has `app_catalog.restart_allowed_general_user["<app id>"]=true`, and only for Docker targets. The setting names are retained for compatibility, but the current UI labels them as standard-user app controls. This is a permanent saved opt-in, not the session enablement gate used for admin approval or autonomous admin repair. The server refuses start for an already-running app, stop for an already-stopped app, restart for a stopped app that should use Start, and restart for an app that is currently online. Privacy blacklists, hidden apps, the optional safety-reviewer gate, the shared per-app cooldown, and the shared global repair rate limit are enforced before Docker is called. The routes return the same verification `outcome` shape as admin repair.

General-user diagnosis can also run a permanent-setting auto-fix when `app_catalog.general_user_auto_repair_enabled=true` and the same standard-user app-control global/per-app opt-in would allow the target. The model still supplies only the restart repair recommendation and target hint; the server re-resolves the visible app, chooses `start` when the app is stopped (`docker_state=exited`) and `restart` otherwise, then runs the same general-user app-action safety path. Stop is never selected automatically; it remains a manual button. Auto-fix outcomes are returned inline in `agent_plan.outcome` with `auto_executed:true`.

Runtime settings endpoints accept and return JSON:

- `visibility`: snake_case fields from the public visibility policy.
- `roles`: returns the visibility policy plus current apps and users for the role editor. Role writes accept the same visibility shape, including `default_role` and a `roles` array.
- `blacklist`: snake_case privacy and redaction fields.
- `apps`: `icon_overrides`, a map from app id/container/display name to an image URL; `agent_repair_allowed`, a map from app id/container/display name to an explicit admin/agent restart-repair opt-in; `general_user_restarts_enabled`, the global switch for standard-user app controls; `general_user_auto_repair_enabled`, the global switch for standard-user diagnosis auto-fixes; and `restart_allowed_general_user`, the per-app standard-user app-control opt-in map. Omitted or false opt-in values mean direct start/stop/restart controls and diagnosis auto-fixes are disabled for that app. The legacy key names are preserved to avoid breaking existing runtime settings. Icon URLs must be `http`, `https`, or a local app-served path beginning with `/`; embedded URL credentials are rejected. Docker/Unraid icon labels are filtered with the same URL rules before being exposed to clients. When Docker/Unraid does not provide an accepted icon label and no override is configured, the frontend chooses a generic built-in category icon from the app name/image reference. These built-ins are not official product logos.
- `llm`: snake_case provider/model/policy fields plus write-only `openai_api_key` and `anthropic_api_key` inputs. The default provider is `disabled`; use `openai` with `openai_auth_method=api_key` and an OpenAI key, `openai_auth_method=chatgpt_browser|chatgpt_headless` with the admin-only ChatGPT connector, or `anthropic` with an Anthropic key for live chat/diagnosis. Browser login uses OpenAI's registered loopback callback and only works when the admin page is opened as `localhost` on the NoobBoard host; LAN devices use the code login shown in the OpenAI connection dialog while NoobBoard polls for completion. Duration values are encoded as Go duration nanoseconds. Reads return `openai_api_key_set`, `anthropic_api_key_set`, `chatgpt_connected`, `chatgpt_access_token_set`, and `chatgpt_account_id_set` booleans instead of key/token values. `clear_chatgpt_auth=true` forgets saved ChatGPT connector tokens. `agent_control_enabled` enables admin-approved app fixes, `agent_auto_repair_enabled` enables autonomous restart repair when the safety-reviewer gate also allows it, and `agent_arm_duration` controls how long the legacy route `POST /api/admin/agent/arm` grants temporary fix access to the current admin session. `action_auto_review_enabled`, `action_auto_review_model`, `action_auto_review_reasoning`, and `action_auto_review_reference_paths` configure the safety reviewer. `agent_readiness` also reports the repair cooldown/rate-limit values used by the server. Session enablement state is not persisted in settings. `openai_model` is selected from known OpenAI API models in API-key mode and from current GPT model names in ChatGPT connector mode; unsupported saved ChatGPT values fall back to `gpt-5.5`, and ChatGPT-account 400 model-support responses retry with the next configured GPT fallback.
- `integrations`: snake_case Unraid, UniFi, SSH fallback, UniFi NAS-link telemetry, and probe fields plus write-only `unraid_api_key` and `unifi_api_key` inputs. Reads return `unraid_api_key_set` / `unifi_api_key_set` booleans instead of key values. Bare local hosts/IPs are normalized to `http://...` for Unraid and `https://...` for UniFi.
- `notifications`: snake_case notification fields. Duration values are encoded as Go duration nanoseconds.

Simple config file example:

```yaml
server:
  bind_address: "127.0.0.1"
  port: 8787
  compact_port: 8788
database:
  path: "C:\\ProgramData\\NoobBoard\\data\\dashboard.db.json"
fixtures:
  dir: "fixtures"
  scenario: "all_systems_online"
notifications:
  enabled: true
  global_opt_in_enabled: true
  backend: "mock"
llm:
  provider: "openai"
  openai_auth_method: "api_key"
  openai_model: "gpt-5.5"
  agent_control_enabled: true
  agent_auto_repair_enabled: false
  action_auto_review_enabled: true
  action_auto_review_model: "same"
  # Prefer the admin settings UI for secrets. Local config may also provide:
  # openai_api_key, chatgpt_refresh_token, chatgpt_access_token, chatgpt_account_id,
  # anthropic_api_key, anthropic_model.
integrations:
  mode: "live"
  unraid_base_url: "http://tower.local"
  unraid_api_key_file: "C:\\path\\to\\unraid.key"
  unraid_ssh_fallback: false
  unraid_ssh_host: "tower.local"
  unraid_ssh_port: 22
  unraid_ssh_user: "root"
  unraid_ssh_key_file: "C:\\path\\to\\unraid-ssh-key"
  unifi_base_url: "https://192.168.1.1"
  unifi_api_key_file: "C:\\path\\to\\unifi.key"
  unifi_site_id: "default"
  unifi_nas_client_hint: "tower.local"
  expected_nas_link_mbps: 1000
  internet_probe_url: "https://www.gstatic.com/generate_204"
  dns_probe_host: "cloudflare.com"
  router_probe_target: "https://192.168.1.1"
  nas_probe_target: "http://tower.local"
retention:
  max_status_event_age: "90d"
  max_status_events_per_subject: 500
```

`live` is the default and reports missing live collectors as collector errors instead of replacing them with fixture Docker data. Live network probes use `NOOBBOARD_INTERNET_PROBE_URL`, `NOOBBOARD_DNS_PROBE_HOST`, `NOOBBOARD_ROUTER_PROBE_TARGET`, and `NOOBBOARD_NAS_PROBE_TARGET`; router and NAS targets fall back to the configured UniFi and Unraid base URLs. Skipped probe targets are reported as unknown rather than failed. The Unraid collector always gathers core array/capacity/disk/parity/uptime status and, when the API/schema permits, also records CPU brand/core/thread counts, memory use, unread notification/alert/warning counts, Docker network names/counts, VM domain counts/names, and share counts/names for admin diagnostics. Optional Unraid diagnostics are independent best-effort reads, so one unsupported field group does not suppress other supported facts. The UniFi collector counts WAN definitions and uses explicit WAN status/link fields when present; definition-only WAN responses fall back to gateway-device state. If `unifi_nas_client_hint` is configured, or if `nas_probe_target` / `unraid_base_url` identifies the NAS client, the UniFi client list is also used to populate `nas_link_speed_mbps`; `expected_nas_link_mbps` enables degraded-link diagnostics. This is read-only client telemetry and does not change UniFi configuration. Use `NOOBBOARD_INTEGRATION_MODE=fixture` only for deterministic demos/tests, or `NOOBBOARD_INTEGRATION_MODE=mixed` when configured live Unraid, Unraid-backed Docker, and UniFi clients should be combined with fixture network-probe clients. API snapshots include `integration_mode`, optional `fixture_scenario`, and per-app `data_source` so clients can distinguish fixture data from live Docker telemetry.

While serving, NoobBoard polls collectors on `polling.interval` and caches the latest full snapshot. Status/app/admin read APIs serve cloned cached snapshots, with a cold-start collection if the cache is empty. Runtime settings writes invalidate the cache; the next read or poll refreshes it. `run-once` still collects directly.

The poller also records status transitions to `history.jsonl` next to `database.path`. The first observation seeds a baseline and does not emit events. Subsequent app and infrastructure status changes append `models.StatusEvent` records; pruning is controlled by `retention.max_status_event_age` and `retention.max_status_events_per_subject`. `NOOBBOARD_MAX_STATUS_EVENT_AGE` accepts Go-style durations, whole-day values such as `90d`, or raw nanoseconds.

`POST /api/status/refresh` is the shared "Check again" route. It requires auth plus `X-CSRF-Token`, immediately refreshes the cached snapshot through configured collectors, records status-history transitions, runs notification de-dupe, and returns the requesting role's filtered snapshot. It refreshes NoobBoard's view of state only; it does not start, stop, restart, repair, or change Unraid/UniFi configuration.

Status history is exposed through shared authenticated routes. `GET /api/apps/{id}/history?window=7d&limit=100` returns the current role-visible app state plus newest-first transition events. `GET /api/infrastructure/history?subject=internet&window=7d` supports `internet`, `dns`, `wan`, `nas`, and `unraid_array`; admins can query all subjects. General users can query `internet` and, when server visibility is enabled, `nas`; those responses use plain-language display names and notes. Raw `dns`, `wan`, and `unraid_array` infrastructure subjects are admin-only to avoid leaking technical vocabulary. Hidden apps and disallowed infrastructure subjects return `404` rather than leaking existence.

If the Unraid GraphQL Docker API omits containers, enable `UNRAID_SSH_FALLBACK_ENABLED` / `integrations.unraid_ssh_fallback` and provide `UNRAID_SSH_HOST`, `UNRAID_SSH_USER`, and optionally `UNRAID_SSH_KEY_FILE`. The SSH collector runs `docker ps -a --no-trunc --format '{{json .}}'` without a shell, parses Docker CLI JSON, and is preferred only when it sees more containers than GraphQL or when GraphQL cannot serve logs/control because the endpoint is unavailable or the schema lacks the requested Docker field. Permission, validation, or not-found GraphQL errors do not fall through to SSH.

`UNRAID_BASE_URL` and `integrations.unraid_base_url` may be a full HTTP(S) URL or a bare local host/IP; bare values are normalized to `http://...`. `UNIFI_BASE_URL` and `integrations.unifi_base_url` similarly accept a bare local host/IP and normalize it to `https://...`. Embedded credentials and non-HTTP schemes are rejected. UniFi NAS-link settings can be supplied with `NOOBBOARD_UNIFI_NAS_CLIENT_HINT` / `integrations.unifi_nas_client_hint` and `NOOBBOARD_EXPECTED_NAS_LINK_MBPS` / `integrations.expected_nas_link_mbps`.

API keys can be supplied directly with `UNRAID_API_KEY` / `UNIFI_API_KEY` or through `UNRAID_API_KEY_FILE` / `UNIFI_API_KEY_FILE` and the matching simple-config keys. Secret files may contain one bare token line or one `KEY=value` / `KEY: value` line; comments and blank lines are ignored. When a key file is configured, it overrides the direct key value.
