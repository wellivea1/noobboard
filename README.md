# NoobBoard

NoobBoard is a local-first Go web app for monitoring an Unraid server, Docker apps, UniFi
network state, and basic connectivity. It serves two surfaces: a detailed **admin panel**
and a compact, phone-friendly **shared home status** view aimed at non-technical users.

The compact view is meant to help everyone in the home share awareness of service health
without needing to understand or manage the server itself.

It is designed for a LAN or private reverse-proxy deployment. Live collectors are used when
credentials are configured; fixture data is only used when explicitly selected for
development or visual QA.

> ## ⚠️ Disclaimer — personal use only
>
> This is a personal hobby project, shared as-is. It is intended for **private, personal,
> home-lab use on a trusted LAN** — not for production, commercial, or multi-tenant
> deployments. It comes with **no warranty and no guarantee of security, correctness, or
> support**; use it at your own risk. You are responsible for your own credentials, network
> exposure, and data. NoobBoard is not affiliated with or endorsed by Unraid/Lime
> Technology, Ubiquiti/UniFi, OpenAI, or Anthropic.

## Features

- Two separate web surfaces:
  - Admin panel on port `8787` with detailed status, incidents, settings, logs, and Docker controls.
  - Compact web app on port `8788` with simplified status, selected apps, and chat.
- Live Unraid status, array health, capacity, parity, Docker state, Docker logs, and Docker start/stop/restart actions.
- Live UniFi gateway, WAN, device, client, update, and warning telemetry.
- Direct probes for internet, DNS, router, and NAS reachability.
- OpenAI or Anthropic diagnosis support with strict JSON validation and redacted role-scoped context.
- Runtime settings for roles, visibility, app icons, privacy blacklist, LLM provider, and notifications.
- Local username/password auth with admin and standard-user roles.
- PWA metadata and safe-area handling for iOS Safari web apps and Android install prompts.

## Quick Start (recommended)

Prerequisites:

- Windows 10/11
- Windows PowerShell 5.1+ (or PowerShell 7)

The install script checks for and installs the Go toolchain (via `winget` if missing),
builds a self-contained `noobboard.exe`, and registers the NoobBoard Windows service.

```powershell
# Build only — no service. Good for trying it out or development:
.\install.ps1 -NoService
.\dist\noobboard.exe serve

# Full install as an auto-starting Windows service (run from an *elevated* prompt):
.\install.ps1 -Start
```

During a service install, the script also **prompts** whether to:

- add a Windows Firewall rule allowing LAN/WAN access on ports 8787-8788 (recommended), and
- set up the admin login now (enter a username and password).

The admin-login prompt only seeds the bootstrap credentials in the service config; it does
**not** complete setup, so the in-app setup wizard (when available) still runs and continues
from where the installer left off. See the install/wizard contract in
`docs/deployment-windows.md` and `docs/agent-roadmap.md`.

Useful flags: `-NoService` (build only), `-Start` (start the service after install),
`-RunTests` (run the test suite first), `-AllowLan` / `-NoFirewall` (firewall rule yes/no
without prompting), `-InstallDir <path>` (service binary location, default
`%ProgramFiles%\NoobBoard`). Run `Get-Help .\install.ps1 -Detailed` for the full list.

### Manual build (alternative)

Requires Go 1.25 or newer:

```powershell
& 'C:\Program Files\Go\bin\go.exe' test ./...
& 'C:\Program Files\Go\bin\go.exe' build -o dist\noobboard.exe .\cmd\dashboard
.\dist\noobboard.exe serve
```

Default URLs:

```text
Admin panel:     http://127.0.0.1:8787/
Compact web app: http://127.0.0.1:8788/
```

Development credentials:

- `admin` / `change-me-now`
- `viewer` / `change-me-now`

**Change these before any real LAN or reverse-proxy use.**

## Live Configuration

Live mode is the default. Keep credentials in environment variables, a local config file, or
local key files ignored by git.

```powershell
$env:NOOBBOARD_INTEGRATION_MODE = 'live'
$env:NOOBBOARD_PORT = '8787'
$env:NOOBBOARD_COMPACT_PORT = '8788'

$env:UNRAID_BASE_URL = 'http://tower.local'
$env:UNRAID_API_KEY_FILE = 'C:\path\to\unraid.key'

$env:UNIFI_BASE_URL = 'https://192.168.1.1'
$env:UNIFI_API_KEY_FILE = 'C:\path\to\unifi.key'
$env:UNIFI_SITE_ID = 'default'

$env:NOOBBOARD_LLM_PROVIDER = 'openai' # or 'anthropic'
$env:OPENAI_API_KEY = 'replace_me' # or ANTHROPIC_API_KEY

.\dist\noobboard.exe serve
```

You can also keep this configuration in a local config file and run
`.\dist\noobboard.exe serve --config config.local.yaml`. When running as a Windows service,
the default config path is `C:\ProgramData\NoobBoard\config.yaml`.

Legacy `HSD_*` environment variables are still accepted as fallback aliases, but new
configuration should use `NOOBBOARD_*`.

Bare local IPs are accepted for `UNRAID_BASE_URL` and `UNIFI_BASE_URL`. Unraid values
normalize to `http://...`; UniFi values normalize to `https://...`.

## Optional SSH Docker Fallback

If the Unraid GraphQL Docker API omits containers, enable SSH fallback. It runs Docker CLI
commands directly on Unraid using key/agent-based SSH and argument-array process execution.

```powershell
$env:UNRAID_SSH_FALLBACK_ENABLED = 'true'
$env:UNRAID_SSH_HOST = 'tower.local'
$env:UNRAID_SSH_USER = 'root'
$env:UNRAID_SSH_KEY_FILE = 'C:\path\to\unraid-ssh-key'
```

The app prefers SSH only when SSH sees more containers than GraphQL, or when GraphQL cannot
serve logs/control for a selected SSH-sourced app.

## Fixture Mode

Fixture mode is for deterministic demos and tests. It must be selected explicitly.

```powershell
$env:NOOBBOARD_INTEGRATION_MODE = 'fixture'
$env:NOOBBOARD_FIXTURE_SCENARIO = 'single_container_exited'
.\dist\noobboard.exe run-once
```

Fixtures live under `fixtures/incidents`.

## Visual QA

Run the visual check before and after UI changes:

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\scripts\visual-check.ps1
```

The check starts an isolated fixture-backed dashboard, signs in with the development admin
and viewer accounts, captures desktop and mobile screenshots under `.cache`, and checks for
common regressions such as blank dashboards, hidden login screens, missing app logos,
missing compact content, button overflow, and mobile body overflow.

## Windows Service

The recommended path is `.\install.ps1 -Start` (see Quick Start), which builds the binary,
copies it to `%ProgramFiles%\NoobBoard`, and registers the service. To manage it manually:

```powershell
.\dist\noobboard.exe install-service
.\dist\noobboard.exe start-service
.\dist\noobboard.exe stop-service
.\dist\noobboard.exe uninstall-service
```

Default Windows paths:

- Config: `C:\ProgramData\NoobBoard\config.yaml`
- Database: `C:\ProgramData\NoobBoard\data\dashboard.db.json`
- Logs: `C:\ProgramData\NoobBoard\logs\`

Allow LAN access on a private network only (the installer can do this with `-AllowLan`):

```powershell
New-NetFirewallRule -DisplayName "NoobBoard" -Direction Inbound -Action Allow -Protocol TCP -LocalPort 8787,8788 -Profile Private
```

## Security Notes

- Do not commit `.env`, `config.local.yaml`, API keys, SSH keys, database files, logs, or `.cache`.
- Admin APIs are only registered on the admin port.
- Compact web app requests to `/api/admin/*` return `404`.
- Mutating requests require CSRF tokens and same-origin checks.
- App logs, audit entries, notification text, and LLM context are redacted before use.
- Docker controls are admin-only and audited.
- OpenAI and Anthropic are the only supported LLM providers.

See `docs/security.md` for the full list of controls.

## Useful Commands

```powershell
.\dist\noobboard.exe check-config
.\dist\noobboard.exe run-once
.\dist\noobboard.exe version
```

## Project Layout

```text
install.ps1         Dependency check + build + service install
cmd/dashboard       CLI entrypoint and server startup
cmd/visualcheck     Local screenshot/DOM regression harness
internal/adapters   Unraid, Docker, UniFi, probes, and fixtures
internal/server     HTTP routing, auth, settings, and static app serving
internal/diagnostics Incident and status rules
web/public          Embedded PWA frontend
docs                Architecture, API config, deployment, and security notes
AGENTS.md           Orientation + don't-regress guardrails for contributors/agents
docs/agent-roadmap.md  Planned workstreams and design notes
```

## Contributing / Roadmap

Read `AGENTS.md` first for the app's purpose and the invariants that keep the two surfaces
safe. Planned work and design decisions live in `docs/agent-roadmap.md`.
