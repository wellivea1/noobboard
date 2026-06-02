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
- `GET /api/apps`
- `GET /api/apps/{id}`
- `GET /api/admin/status/full`
- `GET /api/admin/incidents`
- `GET /api/admin/audit`
- `GET /api/admin/users`
- `POST /api/admin/users`
- `POST /api/admin/apps/{id}/icon`
- `POST /api/admin/apps/{id}/action`
- `GET /api/admin/apps/{id}/logs`
- `POST /api/admin/diagnose`
- `POST /api/user/diagnose`
- `POST /api/user/notify-admin`
- `GET /api/user/notification-preferences`
- `POST /api/user/notification-preferences`
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
- `GET /api/admin/settings/integrations`
- `POST /api/admin/settings/integrations`
- `GET /api/admin/settings/notifications`
- `POST /api/admin/settings/notifications`

Admin settings writes require the `X-CSRF-Token` returned by login. Successful writes are persisted in the JSON database under `runtime_settings` and applied immediately to the in-memory redactor, LLM client, notification manager, and visibility policy.

Docker app operations are admin-only. `POST /api/admin/apps/{id}/action` requires `X-CSRF-Token` and accepts `{"action":"start"}`, `{"action":"stop"}`, or `{"action":"restart"}`. `GET /api/admin/apps/{id}/logs?limit=80` returns redacted Docker log lines and clamps `limit` to `1..200`. Both routes resolve `{id}` against the current admin snapshot and use the configured Docker collector target; clients do not submit raw Unraid object IDs. Log audit records store only metadata such as app ID, container name, line count, and whether redaction occurred. Live Unraid mode requires an API key with Docker permissions.

Runtime settings endpoints accept and return JSON:

- `visibility`: snake_case fields from the public visibility policy.
- `roles`: returns the visibility policy plus current apps and users for the role editor. Role writes accept the same visibility shape, including `default_role` and a `roles` array.
- `blacklist`: snake_case privacy and redaction fields.
- `apps`: `icon_overrides`, a map from app id/container/display name to an image URL. URLs must be `http`, `https`, or a local app-served path beginning with `/`; embedded URL credentials are rejected. Docker/Unraid icon labels are filtered with the same URL rules before being exposed to clients. When Docker/Unraid does not provide an accepted icon label and no override is configured, the frontend chooses a generic built-in category icon from the app name/image reference. These built-ins are not official product logos.
- `llm`: snake_case provider/model/policy fields plus write-only `openai_api_key` and `anthropic_api_key` inputs. The default provider is `disabled`; use `openai` with an OpenAI key or `anthropic` with an Anthropic key for live chat/diagnosis. Duration values are encoded as Go duration nanoseconds. Reads return `openai_api_key_set` / `anthropic_api_key_set` booleans instead of key values.
- `integrations`: snake_case Unraid, UniFi, SSH fallback, and probe fields plus write-only `unraid_api_key` and `unifi_api_key` inputs. Reads return `unraid_api_key_set` / `unifi_api_key_set` booleans instead of key values. Bare local hosts/IPs are normalized to `http://...` for Unraid and `https://...` for UniFi.
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
  internet_probe_url: "https://www.gstatic.com/generate_204"
  dns_probe_host: "cloudflare.com"
  router_probe_target: "https://192.168.1.1"
  nas_probe_target: "http://tower.local"
```

`live` is the default and reports missing live collectors as collector errors instead of replacing them with fixture Docker data. Live network probes use `NOOBBOARD_INTERNET_PROBE_URL`, `NOOBBOARD_DNS_PROBE_HOST`, `NOOBBOARD_ROUTER_PROBE_TARGET`, and `NOOBBOARD_NAS_PROBE_TARGET`; router and NAS targets fall back to the configured UniFi and Unraid base URLs. Skipped probe targets are reported as unknown rather than failed. The UniFi collector counts WAN definitions and uses explicit WAN status/link fields when present; definition-only WAN responses fall back to gateway-device state. Use `NOOBBOARD_INTEGRATION_MODE=fixture` only for deterministic demos/tests, or `NOOBBOARD_INTEGRATION_MODE=mixed` when configured live Unraid, Unraid-backed Docker, and UniFi clients should be combined with fixture network-probe clients. API snapshots include `integration_mode`, optional `fixture_scenario`, and per-app `data_source` so clients can distinguish fixture data from live Docker telemetry.

If the Unraid GraphQL Docker API omits containers, enable `UNRAID_SSH_FALLBACK_ENABLED` / `integrations.unraid_ssh_fallback` and provide `UNRAID_SSH_HOST`, `UNRAID_SSH_USER`, and optionally `UNRAID_SSH_KEY_FILE`. The SSH collector runs `docker ps -a --no-trunc --format '{{json .}}'` without a shell, parses Docker CLI JSON, and is preferred only when it sees more containers than GraphQL or when GraphQL cannot serve logs/control for a selected SSH-sourced app.

`UNRAID_BASE_URL` and `integrations.unraid_base_url` may be a full HTTP(S) URL or a bare local host/IP; bare values are normalized to `http://...`. `UNIFI_BASE_URL` and `integrations.unifi_base_url` similarly accept a bare local host/IP and normalize it to `https://...`. Embedded credentials and non-HTTP schemes are rejected.

API keys can be supplied directly with `UNRAID_API_KEY` / `UNIFI_API_KEY` or through `UNRAID_API_KEY_FILE` / `UNIFI_API_KEY_FILE` and the matching simple-config keys. Secret files may contain one bare token line or one `KEY=value` / `KEY: value` line; comments and blank lines are ignored. When a key file is configured, it overrides the direct key value.
