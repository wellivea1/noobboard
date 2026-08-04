# Architecture

NoobBoard is a single Go binary that runs on a Windows mini PC on a home LAN and
serves two web surfaces: a dense admin panel on `8787` and a compact status app
for non-technical household members on `8788`. It monitors an Unraid NAS, its
Docker apps, UniFi network state, and basic connectivity, and can diagnose and
repair a narrow, allow-listed set of problems with an LLM's help.

This document is the map. Policy detail lives in `docs/security.md`,
`docs/llm-policy.md`, and `docs/ui-standards.md`; the roadmap lives in
`docs/agent-roadmap.md`.

## Dependency direction

Packages form a layered graph with no cycles. Everything points inward toward
`models`, and only `server` knows about the outside world.

```text
models          (no internal deps)  wire and domain types
  ├── config    settings, defaults, secret-file loading
  ├── diagnostics  telemetry -> incidents, pure functions over models
  ├── history   status-transition recorder
  ├── privacy   redaction and role filtering        (config, models)
  ├── db        JSON store, status history, metrics (config, models)
  ├── users     accounts and roles                  (config, db, models)
  ├── audit     append-only actor log               (db, models, privacy)
  ├── llm       providers, context, strict schema   (config, models, privacy)
  ├── notifications                                 (audit, config, db, models)
  └── adapters/{unraid,docker,unifi,probes,fixture} (models, config)

server          depends on all of the above and on web
service         no internal deps; Windows SCM integration behind build tags
```

Two rules keep that graph honest:

- **`models` never imports anything internal.** It is the shared vocabulary; a
  dependency there would make every other package cyclic.
- **`diagnostics` is pure.** It takes a snapshot and returns incidents. No I/O,
  no clock reads beyond what it is handed. That is what makes the rules testable
  without a live NAS.

`server` is the only package that composes: it owns the routers, the snapshot
cache, and the lifecycle. Nothing imports it.

## Inside `internal/server`

One package, split by responsibility. Each file opens with what belongs there
and why.

| File | Owns |
|---|---|
| `server.go` | Construction, both routers, snapshot cache, background poller |
| `auth.go` | Sessions, login throttle, cookies, `requireAuth` / `requireAdmin` |
| `http.go` | JSON responses, body limits, security headers, origin checks |
| `status.go` | Read-only status and history endpoints |
| `apps.go` | App identity resolution, Docker control, container logs |
| `data.go` | Recorded-data summary and clearing |
| `agent_plan.go` | Diagnosis, the action registry, plan derivation, backstops |
| `agent_repair.go` | Approval tokens, execution, outcome verification, rate limits |
| `repair_requests.go` | The general-user repair path |
| `settings.go` | Settings endpoints and their wire types |
| `runtime.go` | Applying settings to a running process, collector rebuild |
| `probes.go` | Probe sample windows, baselines, latency buckets |
| `openai_auth.go` | The ChatGPT connector's browser and headless login flows |

### Shared state

`App` holds what must outlive a request. Every lock is documented on the struct
with exactly what it guards, and two clusters own their own:

- **`probeTracker`** — the rolling per-subject sample window and the five-minute
  latency bucket being filled. One lock, because they advance together on every
  poll.
- **`agentRepairLimiter`** — the per-app cooldown and the global hourly repair
  cap. The window arithmetic and the expiry sweep are not reachable from
  outside the type.

## Request and data flow

```text
poller ──> collectors ──> rule engine ──> snapshot ──┬──> history recorder
   (polling.interval)                                 └──> snapshot cache
                                                             │
                            role filter + redaction <────────┘
                                      │
                    ┌─────────────────┴─────────────────┐
              admin routes (8787)              compact routes (8788)
```

- The poller collects immediately at startup and then on `polling.interval`,
  processes notifications, and stores a **cloned** full snapshot in memory.
  Read routes serve clones with role filtering applied; a request arriving
  before the first poll triggers a cold-start collection.
- Status transitions are appended to `history.jsonl` beside the database. The
  recorder seeds a baseline on first observation, writes only on change, emits
  one `unknown` transition when a known app disappears, and prunes by age and
  per-subject cap.
- Probe latency is downsampled into five-minute buckets in `latency.jsonl`. Raw
  per-poll samples are never persisted.

## Invariants

These are load-bearing. Breaking one is a behaviour change, not a refactor.

- **Two surfaces, two ports.** The compact router never registers
  `/api/admin/*`. Admin data and controls do not reach the general-user surface.
- **Redaction is mandatory and fail-closed.** Anything reaching display, logs,
  notifications, audit entries, or LLM context passes through `internal/privacy`
  first. A redaction failure blocks the response; it does not pass the original
  through.
- **Source honesty.** Snapshots and app records carry `data_source`
  (`fixture` / `mixed` / `live` / `unraid-docker`). Fixture data can never be
  presented as live state, and fixture collectors refuse control actions rather
  than simulating success.
- **Mutations are gated.** Every mutating endpoint requires CSRF and a
  same-origin check, and is audited. Docker control and settings are
  admin-only.
- **The model chooses from a list; the server decides what runs.** The LLM
  returns an allow-listed action id and a target hint. Resolving the target,
  deriving the concrete Docker operation, checking opt-in and rate limits, and
  executing are all server-side. A separate reviewer model gates execution.
- **Secrets never enter git or a settings response.** Keys are read from env
  vars, local config, or configured key files. Settings reads return
  `*_set` booleans, never key material.

## Collectors

All adapters implement the interfaces in `internal/adapters/*` and are
interchangeable with the fixture implementations, so the server, rule engine,
privacy, notification, and LLM layers never change when an adapter does.

**Unraid** — read-only GraphQL for array health, disk warnings, capacity,
uptime, and best-effort parity state, with `x-api-key`. Optional diagnostics
(CPU, memory, notification counts, Docker networks, VM domains, shares) are
queried independently so a permission-limited or older schema degrades one field
instead of the whole status path. Parity uses `vars.mdResync*` and deliberately
avoids `mdResyncSize`, which overflows GraphQL `Int` on some servers. If GraphQL
fails, the adapter probes the web GUI: a responding GUI reports the NAS
reachable and the API unavailable, rather than a full outage. The one non-Docker
mutation is `StartArray`, reachable only through the signed-plan array-start
path.

**Docker** — live Unraid GraphQL using `dockerContainers { … }` with a legacy
`docker { containers { … } }` fallback. An optional SSH fallback runs `docker ps`
directly, and the composed collector uses whichever source sees more containers.
SSH control and log fallback is limited to transport and schema failures — never
to permission, validation, or not-found errors. Mutations and log reads target
the strict Docker object ID from GraphQL, or a safe `container:<name>` fallback
derived from the server's own snapshot; a user-submitted `PrefixedID` is never
accepted. Stop and restart require confirmation tied to the server-resolved app
id. Restart prefers the native `docker.restart` mutation and falls back to
stop/start only when it is unsupported. Logs come from
`DockerContainerLogs.lines { timestamp message }` with `tail`, are redacted
before the response, and are audited by metadata only — log text is never
stored.

**UniFi** — read-only local integration API. Verifies `/info`, resolves the
configured site, and paginates sites, devices, clients, and WAN definitions with
bounded page counts. When the WAN endpoint exposes only definitions, WAN status
falls back to gateway-device state; explicit up/down/link fields drive
`unifi_wan_up` when present. Client data optionally supplies NAS link speed when
`unifi_nas_client_hint` identifies the NAS.

**Probes** — direct HTTP, DNS, and TCP checks for internet, DNS, router, and NAS
reachability. Router and NAS targets are inferred from the configured UniFi and
Unraid base URLs unless overridden. A skipped target is carried in source health
as unknown, never as failed.

The rule engine emits endpoint-probe evidence only when an endpoint probe
actually failed. A Docker-state failure produces Docker evidence
(`container exited`), not generic HTTP/TCP text.

## Frontend

`web/public/{index.html,app.js,styles.css}` is one embedded bundle
(`web/embed.go`, `//go:embed public/*`) served by both surfaces, which branch on
a body class. **Assets are embedded, so a CSS or JS edit requires a rebuild to
take effect.**

`docs/ui-standards.md` is binding for anything under `web/public`. Visual
regressions are caught by `cmd/visualcheck`, a CDP harness that drives Edge
headless and asserts structure, overflow, touch targets, and banned vocabulary
on both surfaces at desktop and mobile viewports.

## Windows service

Service installation lives behind build-tagged code in `internal/service`. Under
the Service Control Manager the binary runs as a real service via
`golang.org/x/sys/windows/svc`: `serve` detects SCM launch, performs the
start/stop handshake, and cancels the server context on Stop or Shutdown. Run
from a console it falls back to signal-based foreground mode.
