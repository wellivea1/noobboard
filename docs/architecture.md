# NoobBoard Architecture

NoobBoard is a native Go service that runs on a separate Windows 11 mini PC and serves a LAN web/PWA dashboard.

The service is built around these boundaries:

- Collectors gather deterministic telemetry from Unraid, Docker, UniFi, and network probes.
- The rule engine converts telemetry into normalized app state and incident facts.
- Snapshots and app records carry source metadata (`fixture`, `mixed`, `live`, or `unraid-docker`) so fixture/demo data is visible and cannot be confused with current Docker state.
- Privacy filtering and redaction run before display, notifications, or LLM calls.
- The LLM layer receives only role-specific sanitized context and returns strict JSON.
- Notification delivery is interface-based and starts with a mock backend.
- Windows service installation lives behind build-tagged service-management code.
- The backend exposes two HTTP routers on separate ports: the admin site has the detailed panel and `/api/admin/*`; the compact site serves the lightweight compact web app and shared user APIs only.
- Overview monitor visibility and order are client-side preferences. They use local storage so each admin device can hide and arrange live status rows without mutating shared runtime settings.
- Runtime settings are grouped behind a frontend settings submenu. The role editor remains a structured UI, while lower-level visibility, blacklist, app image, LLM, and notification settings stay in focused JSON editor sections.

Initial implementation uses fixture collectors. Real adapters should implement the same interfaces in `internal/adapters/*` without changing the server, rule engine, privacy, notification, or LLM policy layers.

The current live adapter status:

- Unraid: read-only GraphQL status, array health, disk warning, capacity, and best-effort parity check state using `x-api-key`. Parity state uses safe `vars.mdResync*` fields and intentionally avoids `mdResyncSize` because some servers expose it as an overflowing GraphQL `Int`.
- UniFi: read-only local integration client using `/proxy/network/integration/v1/info`, `/sites`, `/devices`, `/clients`, and `/wans` with `X-API-KEY`. WAN definitions are counted, and explicit WAN state/link fields such as status, state, up, connected, and online are interpreted when the API provides them.
- Docker: live Unraid GraphQL client using the documented `dockerContainers { id names state status autoStart image labels webUiUrl templatePath }` query with a legacy `docker { containers { ... } }` fallback. If enabled, the SSH Docker fallback runs `docker ps` directly on Unraid and the composed collector uses whichever source sees the larger container list. The adapter reads accepted icon URL labels such as `net.unraid.docker.icon` for app logos and ignores unsupported schemes or embedded credentials; admin-provided icon overrides are stored in runtime settings. If neither source is available, the frontend falls back to generic built-in category icons based on the app name/image reference. Admin-only container start/stop actions use Unraid `docker.start` and `docker.stop` mutations with GraphQL variables, or SSH Docker commands for SSH-sourced apps; restart is a stop-then-start sequence for GraphQL and a Docker restart command for SSH. Admin-only container logs are exposed through `GET /api/admin/apps/{id}/logs?limit=80`; the server resolves `{id}` against the current app snapshot, asks the Docker adapter for bounded logs, redacts them before response, and audits metadata without storing log text.
- UniFi: live integration API client verifies `/info`, resolves the configured site, and paginates sites, devices, clients, and WAN definitions with bounded page counts before summarizing gateway, WAN, device, client, update, and warning telemetry. If the WAN endpoint exposes only definitions, WAN status falls back to gateway-device state; if it exposes explicit down/up/link fields, those fields drive `unifi_wan_up` and warnings.
- Network probes: live mode performs direct HTTP/DNS/TCP checks for internet, DNS, router, and NAS reachability. Router and NAS targets are inferred from configured UniFi and Unraid base URLs unless explicit probe targets are configured. Skipped probe targets are carried in source health and treated as unknown, not failed.
- Unraid API fallback: when GraphQL fails, the Unraid adapter probes the base web GUI. If the web GUI responds, the snapshot marks the NAS reachable and the Unraid API unavailable instead of reporting a full NAS outage.

The rule engine only emits endpoint-probe evidence when an endpoint probe actually failed. Docker-state-only failures should produce Docker evidence, such as `container exited`, instead of generic HTTP/TCP probe text.
