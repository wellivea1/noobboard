# AGENTS.md

Orientation for agents (and humans) working in this repo. Keep this file short: it
exists to convey **what NoobBoard is for** and to **prevent regressions**. Planning,
roadmaps, and detailed specs live in `docs/` (start with `docs/agent-roadmap.md`).

## What NoobBoard is

A local, Windows-first shared server monitoring and diagnostics dashboard. It runs on a
headless Windows 11 mini PC on the home LAN and serves a web/PWA dashboard. It monitors an
Unraid NAS (array, capacity, parity), Docker apps, UniFi network/WAN state, and basic
internet/DNS/router/NAS connectivity, with optional OpenAI/Anthropic diagnosis.

It has **two distinct surfaces**, and conflating them is the most common mistake:

- **Admin panel** (port `8787`) — a dense, detailed dashboard for the technical owner:
  full status, incidents, logs, settings, and Docker controls.
- **Compact app** (port `8788`) — a **shared home status remote**, not a mini admin panel.
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
- **One design system.** `docs/ui-standards.md` is binding for anything under `web/public`.
  Read it before adding UI. In short: tokens only (no raw colours or sizes), colour means
  state and nothing else, three container roles, one status label, one uppercase treatment,
  six type sizes. Adding a variant is a design change, not a styling detail.

## Before you push

One command runs every gate — build, vet, gofmt, lint, tests, conflict markers:

```powershell
.\scripts\check.ps1
```

After any change under `web/public`, add the browser regression harness:

```powershell
.\scripts\check.ps1 -Visual
```

CI (`.github/workflows/ci.yml`) runs the same steps plus `go test -race` and a
Windows build. `CONTRIBUTING.md` covers the standards these gates enforce and
what to do when a linter or an invariant needs to change.

## Where things live

```text
cmd/dashboard        CLI entrypoint and server startup
cmd/visualcheck      Screenshot / DOM regression harness
internal/adapters    Unraid, Docker, UniFi, probes, fixtures
internal/server      HTTP routing, auth, settings, static serving (admin + compact)
internal/diagnostics Incident and status rules
internal/llm         OpenAI/Anthropic clients, context builder, strict-JSON schema
internal/privacy     Redaction and visibility filtering
internal/{users,audit,notifications,config,db,models,history,service}
web/public           Embedded PWA frontend (index.html, app.js, styles.css)
docs/                Architecture, policies, and the agent roadmap
fixtures/            Deterministic demo/test telemetry
```

`internal/server` is one package split by responsibility — `auth.go`,
`status.go`, `apps.go`, `agent_plan.go`, `agent_repair.go`, `settings.go`,
`runtime.go`, `probes.go`, and others. Each file opens with what belongs in it.
`docs/architecture.md` has the full map and the dependency direction between
packages.

**`web/public` is embedded** (`//go:embed`), so a CSS or JS edit needs a rebuild
before it shows up.

For roadmap, design specs, and policy detail, see `docs/agent-roadmap.md`,
`docs/architecture.md`, `docs/security.md`, and `docs/llm-policy.md`. For anything
visual, start with `docs/ui-standards.md`. For how to work in the repo — gates,
comment and test standards, changing an invariant — see `CONTRIBUTING.md`.
