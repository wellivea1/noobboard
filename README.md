# Server Status

Server Status is a local-first Go web app for monitoring an Unraid server, Docker apps, UniFi network state, and basic connectivity from a compact phone-friendly dashboard or a detailed admin panel.

It is designed for a LAN or private reverse-proxy deployment. Live collectors are used when credentials are configured; fixture data is only used when explicitly selected for development or visual QA.

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

## Quick Start

Prerequisites:

- Go 1.25 or newer
- Windows PowerShell for the examples below

```powershell
& 'C:\Program Files\Go\bin\go.exe' test ./...
& 'C:\Program Files\Go\bin\go.exe' build -o dist\server-status.exe .\cmd\dashboard
.\dist\server-status.exe serve
```

Default URLs:

```text
Admin panel:     http://127.0.0.1:8787/
Compact web app: http://127.0.0.1:8788/
```

Development credentials:

- `admin` / `change-me-now`
- `viewer` / `change-me-now`

Change these before any real LAN or reverse-proxy use.

## Live Configuration

Live mode is the default. Keep credentials in environment variables, a local config file, or local key files ignored by git.

```powershell
$env:HSD_INTEGRATION_MODE = 'live'
$env:HSD_PORT = '8787'
$env:HSD_COMPACT_PORT = '8788'

$env:UNRAID_BASE_URL = 'http://tower.local'
$env:UNRAID_API_KEY_FILE = 'C:\path\to\unraid.key'

$env:UNIFI_BASE_URL = 'https://192.168.1.1'
$env:UNIFI_API_KEY_FILE = 'C:\path\to\unifi.key'
$env:UNIFI_SITE_ID = 'default'

$env:HSD_LLM_PROVIDER = 'openai' # or 'anthropic'
$env:OPENAI_API_KEY = 'replace_me' # or ANTHROPIC_API_KEY

.\dist\server-status.exe serve
```

Bare local IPs are accepted for `UNRAID_BASE_URL` and `UNIFI_BASE_URL`. Unraid values normalize to `http://...`; UniFi values normalize to `https://...`.

## Optional SSH Docker Fallback

If the Unraid GraphQL Docker API omits containers, enable SSH fallback. It runs Docker CLI commands directly on Unraid using key/agent-based SSH and argument-array process execution.

```powershell
$env:UNRAID_SSH_FALLBACK_ENABLED = 'true'
$env:UNRAID_SSH_HOST = 'tower.local'
$env:UNRAID_SSH_USER = 'root'
$env:UNRAID_SSH_KEY_FILE = 'C:\path\to\unraid-ssh-key'
```

The app prefers SSH only when SSH sees more containers than GraphQL, or when GraphQL cannot serve logs/control for a selected SSH-sourced app.

## Fixture Mode

Fixture mode is for deterministic demos and tests. It must be selected explicitly.

```powershell
$env:HSD_INTEGRATION_MODE = 'fixture'
$env:HSD_FIXTURE_SCENARIO = 'single_container_exited'
.\dist\server-status.exe run-once
```

Fixtures live under `fixtures/incidents`.

## Visual QA

Run the visual check before and after UI changes:

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\scripts\visual-check.ps1
```

The check starts an isolated fixture-backed dashboard, signs in with the development admin and viewer accounts, captures desktop and mobile screenshots under `.cache`, and checks for common regressions such as blank dashboards, hidden login screens, missing app logos, missing compact content, button overflow, and mobile body overflow.

## Windows Service

Build first, then install from an elevated PowerShell prompt:

```powershell
.\dist\server-status.exe install-service
.\dist\server-status.exe start-service
```

Default Windows paths:

- Config: `C:\ProgramData\ServerStatus\config.yaml`
- Database: `C:\ProgramData\ServerStatus\data\dashboard.db.json`
- Logs: `C:\ProgramData\ServerStatus\logs\`

Allow LAN access on a private network only:

```powershell
New-NetFirewallRule -DisplayName "Server Status" -Direction Inbound -Action Allow -Protocol TCP -LocalPort 8787,8788 -Profile Private
```

## Security Notes

- Do not commit `.env`, `config.local.yaml`, API keys, SSH keys, database files, logs, or `.cache`.
- Admin APIs are only registered on the admin port.
- Compact web app requests to `/api/admin/*` return `404`.
- Mutating requests require CSRF tokens and same-origin checks.
- App logs, audit entries, notification text, and LLM context are redacted before use.
- Docker controls are admin-only and audited.
- OpenAI and Anthropic are the only supported LLM providers.

## Useful Commands

```powershell
.\dist\server-status.exe check-config
.\dist\server-status.exe run-once
.\dist\server-status.exe version
```

## Project Layout

```text
cmd/dashboard       CLI entrypoint and server startup
cmd/visualcheck     Local screenshot/DOM regression harness
internal/adapters   Unraid, Docker, UniFi, probes, and fixtures
internal/server     HTTP routing, auth, settings, and static app serving
internal/diagnostics Incident and status rules
web/public          Embedded PWA frontend
docs                Architecture, API config, deployment, and security notes
```
