# Capability review — diagnose, act, verify

What NoobBoard can actually detect, explain, and repair for each thing it talks
to; where the upstream APIs offer more than it uses; and what to build next.

Reviewed 2026-08-01 against `main`. Every claim below is from the code, with
file references, not from the docs describing it.

---

## 1. The shape of the problem

The app's purpose is system management with automated issue diagnosis and
handling. That is a loop:

> **detect** a bad state → **diagnose** the cause → **act** to fix it → **verify** the fix held

Today exactly **one** subsystem completes that loop: Docker containers. Every
other subsystem stops at *diagnose*, and two stop at *detect*. That is the
single most useful framing for what to build next — not "add features" but
"close loops that already start".

### Capability matrix

| Subsystem | Detect | Diagnose | Act | Verify |
|---|---|---|---|---|
| Docker containers | ✅ state, health, exit code, logs | ✅ rules + LLM tools, restart-loop aware | ✅ start / stop / restart | ✅ re-checks after |
| Unraid array | ✅ state, capacity, disks, parity | ✅ 4 rules | ⚠️ start array only | ✅ re-checks after |
| Unraid host (CPU/RAM/VM/shares) | ✅ collected | ⚠️ memory + capacity (E2) | ❌ | — |
| UniFi network | ✅ WAN, devices, clients, link speed | ✅ 4 rules | ❌ **read-only adapter** | — |
| Internet / DNS / router | ⚠️ binary up/down only | ✅ 3 rules | — (correctly upstream) | — |
| NoobBoard's own host | ❌ **nothing** | ❌ | ❌ | — |

Detail per row follows. `⚠️` marks something that works but is narrower than the
data or API already available.

---

## 2. What the LLM can actually see

This is where the largest quality gap is, and it is cheap to close.

The agent advertises **four tools**, all read-only status projections
(`internal/llm/agent.go:15-18`):

- `noobboard_current_status`
- `noobboard_server_status`
- `noobboard_network_status`
- `noobboard_app_status`

**The model has no access to container logs.** `docker.Logs` exists
(`internal/adapters/docker/docker.go:57`), an admin endpoint serves it
(`/api/admin/apps/{id}/logs`), and the compact surface never needs it — but it
is not a tool. So when the LLM is asked "why did Emby stop", it answers from
`state=exited` and a one-line status string. A human would open the log first.
This is the difference between "Emby is not running; try restarting it" and
"Emby exited because its config volume is missing".

**The model has no access to history.** The poller writes `history.jsonl`
(`internal/server/server.go:235` → `History.Append`), so the data to answer "has
this happened before?" exists. No tool reads it. The model therefore cannot
distinguish a first-time blip from a container that has crashed five times
today — which is the difference between "restart it" and "restarting will not
help, look at the disk".

**Exit codes are fetched and discarded.** The Docker query returns `status`
(`internal/adapters/docker/live.go:41-46`), which carries strings like
`Exited (137) 2 minutes ago`. `dockerHealth` (`live.go:602`) greps that string
for `healthy`/`unhealthy` and nothing else. Exit **137** is an OOM kill, **0** is
a clean intentional stop, **1** is an application error — three different
diagnoses and three different correct actions, all currently flattened to
"offline".

**Five of seven recommended actions are dead ends.** The plan schema
(`internal/llm/schema.go:161`) offers seven `recommended_action_id` values. Only
two execute: `ask_admin_to_restart_container` and `ask_admin_to_start_array`.
The other five (`ask_admin_to_check`, `ask_admin_to_check_unifi`,
`ask_admin_to_check_storage`, `none`, `unknown`) all resolve to "tell a human".
That is honest, but it means the automated-handling half of the product's
purpose currently covers two failure modes.

---

## 3. Per-subsystem findings

### Docker containers — the one complete loop

Detect, diagnose, act and verify all present. `verifyRepairOutcome`
(`internal/server/server.go:2621`) re-reads the container after an action and
records whether it actually recovered, which is the part most implementations
skip. Actions are approval-gated, safety-reviewed, rate-limited and audited.

**Closed in E1/E2.** Exit codes are parsed and reach both the rules and LLM
context. Restart loops are caught by counting history events per app before the
rules run (`annotateRestartLoops`), and report as their own incident type with
evidence that warns against restarting again — a crash loop needs the opposite
response to a steady failure.

Remaining: a container flapping faster than the poll interval can still be
sampled during an `Up 5 seconds` window and read as online for that cycle. The
history count catches it across cycles, which is the case that matters.

### Unraid array and storage

Four rules fire: `array_stopped`, `array_degraded`, `unraid_notifications`,
`storage_warning_*` (`internal/diagnostics/rules.go:146-166`). `StartArray` is
the only mutation (`internal/adapters/unraid/live.go:149`) and it is verified
(`server.go:2698`).

**Correction (2026-08-02, during E2).** The original review claimed per-disk
data was unused. That was wrong, and the mistake is worth recording: the
per-disk checks run in the *adapter*, not the rule engine.
`unraid/live.go:82-90` walks `disks { name status temp size }`, flags any disk
over 55°C or with a non-OK status, and emits them as `StorageWarnings`, which
`rules.go` turns into `storage_warning_*` incidents. Reading only `rules.go`
made it look absent. Anyone auditing coverage has to read the adapters too.

Remaining gap: parity check state is read but never actionable (no start/cancel).

### Unraid host telemetry — resolved in E2

**Was:** zero rules referenced any of it and none was displayed, so every poll
paid four extra GraphQL round-trips for data nothing consumed. **Now:** memory
drives `memory_pressure`, array capacity drives `array_capacity_high`, and the
rest is shown on the Server page under Host and Workloads. Nothing is
fetched-and-hidden any more.

Still not diagnosed, deliberately:

- **VM stopped.** There is no baseline for which VMs are *supposed* to be
  running, so "1 of 2 VMs stopped" is a fact, not a problem. A rule without a
  baseline is a false-alarm generator. Displayed, not alerted on.
- **Per-share capacity.** The share query returns counts and names only, not
  free space, so a "share nearly full" rule is not possible from what is
  currently fetched. It needs a wider query first.
- **CPU brand/cores/threads.** Inventory, not health.

### UniFi — detects problems it cannot touch

Four rules fire, including `unifi_devices_offline` and
`nas_link_speed_degraded` (`rules.go:126-162`). The adapter is entirely
read-only: `/info`, `/sites`, `/devices`, `/clients`, `/wans`
(`internal/adapters/unifi/live.go:227-240`).

The UniFi Network Integration API v1 supports device actions —
`POST /v1/sites/{siteId}/devices/{deviceId}/actions` with `RESTART`, and PoE
port power-cycle. A PoE power-cycle is *precisely* the standard remedy for the
degraded-link and offline-device conditions the app already detects. This is the
clearest case in the codebase of detecting something the API would let us fix.

### Probes — binary, so "slow" is undetectable

`httpReachable`, `dnsReachable`, `tcpReachable`
(`internal/adapters/probes/live.go:82-127`) return booleans. There is no
latency, packet loss or jitter measurement and no baseline.

The most common real complaint on a home server — "the internet is slow", "Plex
keeps buffering" — is therefore outside what the app can detect at all. It will
report everything green while the WAN is at 400ms and 5% loss.

### NoobBoard's own host — unmonitored

Nothing checks the machine NoobBoard runs on: free disk (where `history.jsonl`
grows unbounded), memory, whether the Windows service is degraded, or whether
the poller is actually still polling. A monitoring tool that cannot report its
own health will fail silently, and the failure looks identical to "everything is
fine".

---

## 4. API coverage

| Upstream | Used | Available and unused |
|---|---|---|
| Unraid GraphQL | array state/capacity/disks, parity, notifications, CPU, memory, version, VMs, shares, docker networks, `StartArray` | per-disk SMART detail, parity check start/cancel, share free-space thresholds, plugin/update state |
| Unraid Docker GraphQL | container list (`id/names/state/status/autoStart/image/labels/webUiUrl/templatePath`), start/stop/restart, logs | exit-code parsing from the `status` string already fetched; per-container stats |
| UniFi Integration v1 | `/info`, `/sites`, `/devices`, `/clients`, `/wans` | **device `RESTART` action**, **PoE port power-cycle**, statistics endpoints |
| Probes | HTTP GET, DNS lookup, TCP dial | latency, loss, jitter, traceroute-style hop isolation |
| NoobBoard host | — | nothing collected at all |

The pattern: **the read side is well covered and the write side is nearly
empty.** Two of the three integrations expose actions the app does not call.

---

## 5. Plan

Ordered by leverage-per-effort, not by size. Each stage is independently
shippable and each closes a specific loop above.

### Stage 1 — Let the model see what a human would look at

*Closes: the diagnosis-quality gap. Largest quality win available, no new
integration, no new write surface.*

1. **`noobboard_app_logs` tool** — last N lines for one visible app, through
   `internal/privacy` like everything else, admin policies only, hard line cap.
   The plumbing exists; this is an agent tool wrapping it.
2. **`noobboard_app_history` tool** — recent state transitions for one app from
   `history.jsonl`. Enables "this is the fourth crash today".
3. **Parse the exit code** from the Docker `status` string into
   `models.AppStatus`, and surface OOM (137) distinctly. Feed it to the rules and
   the report.

Risk is low and bounded: all three are read-only, and redaction already sits on
the path. The logs tool is the one to review carefully — container logs are the
most likely place for a credential to appear, so it must fail closed.

### Stage 2 — Diagnose what is already being collected

*Closes: the collected-but-unused gap. No new API calls at all.*

4. **Rules for host telemetry**: memory pressure, share nearing full, VM stopped
   that is normally running.
5. **Per-disk rules**: disk temperature threshold, per-disk non-OK status.
6. **Restart-loop detection** from history: N transitions within M minutes is a
   distinct incident type from "offline", with a distinct recommended action
   (do *not* restart again).
7. Show the telemetry on the Server page, or delete the collection. Either is
   defensible; collecting and hiding it is not.

### Stage 3 — Close the UniFi loop

*Closes: detecting what we could fix. First new write surface, so it inherits
the full existing gate.*

8. **UniFi device restart** and **PoE port power-cycle** as executable actions,
   behind the same machinery Docker actions already use: admin approval or
   explicit opt-in, safety reviewer, replay protection, rate limits, audit, and
   post-action verification.
9. New `recommended_action_id` values that map to them, shrinking the dead-end
   list from five to three.

Constraint worth stating up front: power-cycling a PoE port can drop the
switch's own uplink or the admin's connection. The action allowlist must exclude
the port the NoobBoard host and the NAS are attached to unless the admin has
explicitly named it, and verification must handle "the network went away and
came back".

### Stage 4 — Make "slow" detectable

*Closes: the probe blind spot.*

10. Latency and loss on the existing probe targets, stored as history.
11. Baseline-relative rules ("WAN latency is 6× its 7-day median"), not fixed
    thresholds — a 200ms baseline is normal on some links.

### Stage 5 — Monitor the monitor

*Closes: silent failure.*

12. Self-check: free disk on the history path, `history.jsonl` size with
    rotation, last successful poll age, service state.
13. Surface it as a first-class monitor so a stalled poller is visible as a
    problem rather than as stale-but-green data.

### Deliberately not planned

- **Arbitrary shell / SSH as an LLM tool.** The SSH fallback
  (`internal/adapters/docker/ssh.go`) exists for collection. Exposing it as a
  tool would make every other guardrail decorative.
- **Unraid share/user mutations, UniFi firewall or DNS changes.** Detection is
  useful; automated modification of network security config on a home LAN is
  not a risk this product should take.
- **Auto-acting without the approval gate for any new action class.** Stage 3
  adds actions, not a new trust level.

---

## 6. Summary

The read path is mature and the diagnosis rules are reasonable. Two things hold
the product back from its stated purpose:

1. **The model diagnoses blind.** It cannot read logs or history — the two
   artefacts a human uses first — so its explanations are limited to restating
   status fields. Stage 1 fixes this with no new integration.
2. **The write path covers two failure modes.** Container control and array
   start are the only things it can actually do, while it already detects
   network faults whose APIs expose remedies. Stage 3 fixes this.

Stage 2 is nearly free and removes waste already being paid for on every poll.
