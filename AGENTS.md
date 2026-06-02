# AGENTS.md

Orientation for agents (and humans) working in this repo. Keep this file short: it
exists to convey **what NoobBoard is for** and to **prevent regressions**. Planning,
roadmaps, and detailed specs live in `docs/` (start with `docs/agent-roadmap.md`).

## What NoobBoard is

A local, Windows-first **household** server-status and diagnostics dashboard. It runs on a
headless Windows 11 mini PC on the home LAN and serves a web/PWA dashboard. It monitors an
Unraid NAS (array, capacity, parity), Docker apps, UniFi network/WAN state, and basic
internet/DNS/router/NAS connectivity, with optional OpenAI/Anthropic diagnosis.

It has **two distinct surfaces**, and conflating them is the most common mistake:

- **Admin panel** (port `8787`) — a dense, detailed dashboard for the technical owner:
  full status, incidents, logs, settings, and Docker controls.
- **Compact app** (port `8788`) — a **household status remote**, not a mini admin panel.
  Its users are *extremely non-technical iPhone users*. It exists to answer four questions:
  1. Is the thing I want working?
  2. Is this an internet problem or a server problem?
  3. Do I need to tell the admin?
  4. Should I avoid touching anything?

The LLM's job on the compact side is to **translate system state into plain English and a
clear next step** — making the status understandable is more important than giving the
model more authority.

## Don't-regress guardrails

These invariants are easy to break and costly to walk back. Preserve them in any change.

- **Two surfaces, two ports.** The compact router (`8788`) must never register
  `/api/admin/*`. Admin data/controls never leak into the general-user surface.
- **No technical vocabulary in the general-user UI.** Words like Docker, container, Unraid,
  array, parity, endpoint, GraphQL, WAN, API, SSH, probe, telemetry belong to admin or a
  hidden, off-by-default "technical details" disclosure — never the default compact view.
  Use plain-English status ("working" / "not working" / "problem" / "unknown").
- **General users never see** admin controls, raw logs, hidden/blacklisted services, or
  source/role/debug pills.
- **Redaction is mandatory and fail-closed.** Everything reaching display, logs,
  notifications, audit entries, or LLM context passes through `internal/privacy` first.
- **Mutations are gated.** Every mutating endpoint requires CSRF + same-origin checks and is
  audited; Docker controls and settings are admin-only (`requireAdmin`).
- **Source honesty.** Snapshots/app records carry `data_source` (`fixture`/`mixed`/`live`/
  `unraid-docker`). Fixture/demo data must never be presentable as live state.
- **Secrets stay out of git.** API keys, `*.key`, `auth*.txt`, `.env`, `config.local.yaml`,
  `data/`, `logs/`, and `dist/` are git-ignored. Secrets are read only from env vars, local
  config, or explicitly configured key files (`internal/config/config.go`). Don't widen this.
- **Mobile-first compact UI.** No horizontal scrolling, no dense tables, large touch targets
  (≥44px), readable on a 390×844 iPhone viewport.

## Before you push

```powershell
& 'C:\Program Files\Go\bin\go.exe' test ./...
& 'C:\Program Files\Go\bin\go.exe' build -o dist\noobboard.exe .\cmd\dashboard
# After any UI change, also run the visual regression check:
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\scripts\visual-check.ps1
```

## Where things live

```text
cmd/dashboard        CLI entrypoint and server startup
cmd/visualcheck      Screenshot / DOM regression harness
internal/adapters    Unraid, Docker, UniFi, probes, fixtures
internal/server      HTTP routing, auth, settings, static serving (admin + compact)
internal/diagnostics Incident and status rules
internal/llm         OpenAI/Anthropic clients, context builder, strict-JSON schema
internal/privacy     Redaction and visibility filtering
internal/{users,audit,notifications,config,db,models,service}
web/public           Embedded PWA frontend (index.html, app.js, styles.css)
docs/                Architecture, policies, and the agent roadmap
fixtures/            Deterministic demo/test telemetry
```

For roadmap, design specs, and policy detail, see `docs/agent-roadmap.md`,
`docs/architecture.md`, `docs/security.md`, and `docs/llm-policy.md`.
