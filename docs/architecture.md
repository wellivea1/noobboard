# NoobBoard Architecture

NoobBoard is a native Go service that runs on a separate Windows 11 mini PC and serves a LAN web/PWA dashboard.

The service is built around these boundaries:

- Collectors gather deterministic telemetry from Unraid, Docker, UniFi, and network probes.
- The rule engine converts telemetry into normalized app state and incident facts.
- Snapshots and app records carry source metadata (`fixture`, `mixed`, `live`, or `unraid-docker`) so fixture/demo data is visible and cannot be confused with current Docker state.
- Privacy filtering and redaction run before display, notifications, or LLM calls.
- The LLM layer receives only role-specific sanitized context and returns strict JSON.
- Notification delivery is interface-based and starts with a mock backend. Per-user app alerts
  are also stored locally so the compact app can show device alerts while the page or installed
  app is open; this is not a full background push transport.
- Windows service installation lives behind build-tagged service-management code. Under the
  Service Control Manager the binary runs as a real service (`golang.org/x/sys/windows/svc`):
  `serve` detects SCM launch, performs the start/stop handshake, and cancels the server
  context on Stop/Shutdown. Run from a console it falls back to signal-based foreground mode.
- The backend exposes two HTTP routers on separate ports: the admin site has the detailed panel and `/api/admin/*`; the compact site serves the lightweight compact web app and shared user APIs only.
- A background poller runs while the service is active. It collects a full snapshot immediately and then on `polling.interval`, processes notifications, and stores a cloned full snapshot in memory. Status/app/admin read routes serve cloned cached snapshots with role filtering; first request before the poller completes performs a cold-start collection.
- Status transitions are recorded by the poller into an append-only `history.jsonl` beside the configured database file. The recorder seeds a baseline on first observation, records app and infrastructure transitions only when status changes, emits one `unknown` transition when a previously seen app disappears, and prunes history by age and per-subject cap.
- The compact app exposes role-filtered app history through tappable app rows plus plain-language server and internet detail pages. These views use the shared history API and keep default copy in plain language; hidden apps or disallowed infrastructure subjects remain `404` from the API.
- Overview monitor visibility and order are client-side preferences. They use local storage so each admin device can hide and arrange live status rows without mutating shared runtime settings.
- Runtime settings are grouped behind a frontend settings submenu. Role access, visibility,
  blacklist, app image, LLM, integration, and notification settings use structured admin
  controls rather than raw JSON editors. API key fields are write-only in the UI: settings
  responses report whether a key is configured, but never echo key material.

Initial implementation uses fixture collectors. Real adapters should implement the same interfaces in `internal/adapters/*` without changing the server, rule engine, privacy, notification, or LLM policy layers.

The current live adapter status:

- Unraid: read-only GraphQL status, array health, disk warning, capacity, uptime, best-effort parity check state, and optional diagnostics (CPU brand/core/thread counts, memory use, unread notification/alert/warning counts, Docker network names/counts, VM domain counts/names, and share counts/names) using `x-api-key`. Optional diagnostics are queried independently so older, permission-limited, or partially incompatible schemas do not break the core status path or mask other supported fields. Parity state uses safe `vars.mdResync*` fields and intentionally avoids `mdResyncSize` because some servers expose it as an overflowing GraphQL `Int`.
- UniFi: read-only local integration client using `/proxy/network/integration/v1/info`, `/sites`, `/devices`, `/clients`, and `/wans` with `X-API-KEY`. WAN definitions are counted, and explicit WAN state/link fields such as status, state, up, connected, and online are interpreted when the API provides them.
- Docker: live Unraid GraphQL client using the documented `dockerContainers { id names state status autoStart image labels webUiUrl templatePath }` query with a legacy `docker { containers { ... } }` fallback. If enabled, the SSH Docker fallback runs `docker ps` directly on Unraid and the composed collector uses whichever source sees the larger container list. The adapter reads accepted icon URL labels such as `net.unraid.docker.icon` for app logos and ignores unsupported schemes or embedded credentials; admin-provided icon overrides are stored in runtime settings. If neither source is available, the frontend falls back to generic built-in category icons based on the app name/image reference. Admin-only container start/stop actions use Unraid `docker.start` and `docker.stop` mutations with GraphQL variables, or SSH Docker commands for SSH-sourced apps. Stop/restart requests require backend-visible confirmation tied to the server-resolved app ID before the Docker adapter is called. Restart prefers a native Unraid `docker.restart` mutation and falls back to stop/start only when that mutation is not supported. Docker mutation/log targets require a `container:` object ID or safe container name, never arbitrary Unraid `PrefixedID` values. SSH control/log fallback is limited to API transport/schema-unavailable failures, not GraphQL permission, validation, or not-found errors. Admin-only container logs are exposed through `GET /api/admin/apps/{id}/logs?limit=80`; the server resolves `{id}` against the current app snapshot, asks the Docker adapter for bounded logs, redacts them before response, and audits metadata without storing log text.
- UniFi: live integration API client verifies `/info`, resolves the configured site, and paginates sites, devices, clients, and WAN definitions with bounded page counts before summarizing gateway, WAN, device, client, update, and warning telemetry. If the WAN endpoint exposes only definitions, WAN status falls back to gateway-device state; if it exposes explicit down/up/link fields, those fields drive `unifi_wan_up` and warnings. The client list also provides optional NAS link-speed telemetry when `unifi_nas_client_hint` or the NAS/Unraid target identifies the NAS client; this is read-only and only feeds `nas_link_speed_mbps` plus degraded-link diagnostics when `expected_nas_link_mbps` is configured.
- Network probes: live mode performs direct HTTP/DNS/TCP checks for internet, DNS, router, and NAS reachability. Router and NAS targets are inferred from configured UniFi and Unraid base URLs unless explicit probe targets are configured. Skipped probe targets are carried in source health and treated as unknown, not failed.
- Unraid API fallback: when GraphQL fails, the Unraid adapter probes the base web GUI. If the web GUI responds, the snapshot marks the NAS reachable and the Unraid API unavailable instead of reporting a full NAS outage.

The rule engine only emits endpoint-probe evidence when an endpoint probe actually failed. Docker-state-only failures should produce Docker evidence, such as `container exited`, instead of generic HTTP/TCP probe text.
