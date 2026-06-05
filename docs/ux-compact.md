# Compact (shared-home) UX redesign spec

A UX spec for the **compact / general-user surface** (port `8788`, the `#user-home` view),
written for an implementer agent (Codex). It is the Phase-2/3 output of the Multi-agent UX
workflow in `docs/agent-roadmap.md` and the concrete spec behind Workstream B1.

## How to use this spec (implementer)

- Implement the **exact** target below; don't redesign the admin panel (port `8787`) except
  to share safe components.
- This is UI/presentation + microcopy only. **Freeze backend scope** — no new adapters,
  integrations, or security changes (see AGENTS.md). Status data already exists on the
  snapshot; the job is to *translate and present* it.
- Work one numbered finding at a time. Each PR must include mobile screenshots (390×844) of
  the five states in §4 and pass the audit in §7.
- Respect every guardrail in `AGENTS.md` (two ports, no `/api/admin/*` on compact, redaction,
  mobile-first). This spec never relaxes them.

## 1. What this surface is for

The compact app is a **shared home status remote**, not a mini admin dashboard. Its users are
non-technical members of the household. From the first screen, with no scrolling, a user must
be able to answer:

1. Is the thing I want working?
2. Is this an internet problem or a server problem?
3. Do I need to tell the admin?
4. Should I avoid touching anything?

If a screen doesn't help answer those, it's wrong for this surface.

## 2. Current state (review findings)

Reviewed the live build in fixture mode at 390×844 across the working, app-down, and
internet-down scenarios (`cmd/visualcheck`, general-user/`viewer` home). Ranked problems:

1. **No hero status.** The screen opens with a 3-row admin-style status list; there is no
   single large answer to "is everything OK / is it the internet or the server / what do I
   do." (P1)
2. **Technical vocabulary leaks into the default view** — direct guardrail violation. Observed
   strings on the general-user home:
   - "Array started; **Docker** running."
   - "Internet, **DNS**, and **UniFi** responding."
   - Internet-outage state: "**Router** is reachable but external **HTTPS** checks failed." and
     "Local **gateway** reachable; internet check failed." A non-technical user cannot tell
     from this that *the internet is down but the server is fine* — the single most important
     translation the app must make. (P1)
3. **Icon-only / ambiguous primary actions.** A lone `!` button (notify admin?), a `↻` (check
     again), and a `✕` that is actually *Sign out* but reads as "close." No labels. (P1)
4. **No recommended next step.** "Emby is offline." appears with no "tell the admin?" / "wait"
     / "don't touch anything" guidance. (P2)
5. **Page title is "Server health"** — technical framing, and identical to the admin Server
     tab title. (P2)
6. **Touch targets below 44px.** Top-bar controls render ~40–42px tall on mobile (`refresh`,
     `logout`, `notify`, chat send). (P2)
7. **Status conveyed largely by color dots**; weak text/shape redundancy for color-blind
     users, and large dead space below the fold is unused for reassurance/guidance. (P3)

The admin desktop surface (sidebar + Live Status + Incidents + Diagnostic Facts) is dense but
acceptable for the owner — **leave it as-is** apart from shared components.

## 3. Target layout (top to bottom, single column)

1. **Hero status card** — the answer. Large status word/icon, one-line plain-English
   explanation, and one recommended next step. Color + icon + text (not color alone). Fills
   the first screen's attention; see §4 for per-state content.
2. **Primary actions** — at most two above the fold, both **text-labeled** buttons, ≥44×44:
   - `Notify admin` (always)
   - `Ask what's wrong` (only when status chat is available; otherwise omit entirely — do not
     show a disabled mystery box)
   - `Check again` may live in the header but must be labeled or have an `aria-label` and be
     ≥44×44.
3. **Your apps** — list of visible apps: icon, name, plain status (`Working` / `Not working` /
   `Problem` / `Unknown`), optional one-line plain summary. No Start/Stop/Restart on the
   list cards themselves.
   Exception: the app detail page may show compact Start, Restart, and Stop controls when
   an admin has enabled standard-user app controls and opted that app in.
   Stopped apps should present Start as the usable fix; Restart should be disabled for an
   exited/stopped container because the server expects Start for that state.
   The "Ask what's wrong" diagnosis may also auto-start or auto-restart that same opted-in
   app when the separate standard-user automatic-fixes setting is enabled; it must render the
   outcome plainly and still offer admin escalation on refusal/failure.
4. **Technical details** — a `<details>` disclosure, **closed by default**, that may contain
   the existing technical rows for a curious user. This is the only place banned terms may
   appear on the compact surface.

Remove from the general-user view: the bare `!` button, the unlabeled `✕`/sign-out ambiguity
(label it or move it into a menu), and the "Overall / Server / Router" technical row labels.

## 4. Per-state hero content (exact microcopy)

Derive the state from snapshot fields (`overall_status`, `infrastructure.*`, `apps[].current_status`),
**first match wins**. Never use banned terms in any string below.

| Priority | Condition (plain) | Status | Headline | Explanation | Next step |
|---|---|---|---|---|---|
| 1 | Home server not reachable (`infrastructure.nas_reachable == false`) | problem (red) | **Can't reach the home server** | The home server isn't responding right now. | Wait a few minutes; if it doesn't come back, tell the admin. |
| 2 | Internet down but server fine (`internet_reachable == false` && `nas_reachable == true`) | problem (amber) | **The internet looks down** | Your home server is fine — the internet connection isn't working. | This is usually your internet provider. Check the modem/router or wait. You don't need to touch the server. |
| 3 | Server/storage problem (array not started/healthy, or app platform down) | problem (red) | **The home server has a problem** | Storage or apps aren't running correctly right now. | Tell the admin. Avoid changing settings. |
| 4 | One app down (others fine) | warning (amber) | **{App} isn't working** | Everything else looks fine. | Tell the admin if you need {App}. |
| 4b | Multiple apps down (platform fine) | warning (amber) | **Some apps aren't working** | {n} apps are down; the rest are fine. | Tell the admin if you need them. |
| 5 | All good | ok (green) | **Everything's working** | All your apps and the home server are running. | Nothing to do. |
| 6 | Loading / unknown | neutral | **Checking…** | Getting the latest status. | — |

Notes:
- "home server" replaces NAS/Unraid/array/storage. "apps" replaces containers/Docker. "the
  internet" replaces WAN/DNS/gateway/HTTPS.
- {App} uses the app's friendly display name.
- The hero must be computed from structured fields, **not** by reusing the admin
  `source_health`/`server_summary` strings (those are the source of the leaks in §2).

## 5. Plain-English mapping (apply everywhere on the compact surface)

`container` → app · `endpoint failed` / `HTTPS check failed` → not responding · `WAN down` /
`DNS` / `gateway` → internet · `array stopped` → server storage isn't ready · `NAS unreachable`
→ can't reach the home server · `health check failed` → not responding · `degraded` → problem ·
`online` → working · `offline` → not working.

**Banned in the default compact view** (allowed only inside the §3.4 Technical details
disclosure): container, docker, unraid, array, parity, endpoint, graphql, probe, WAN, LAN,
API, SSH, telemetry, SMART, syslog, filesystem, cache pool, gateway, HTTPS, DNS, UniFi.

## 6. Acceptance criteria (testable)

- [ ] On `#user-home` at 390×844, the hero card answers the four questions from §1 on the
      first screen, no scrolling, for all five states in §4.
- [ ] The internet-down state explicitly tells the user it is **the internet, not the server**.
- [ ] No banned term (§5) appears anywhere in the default compact DOM; banned terms appear only
      inside the closed-by-default Technical details disclosure.
- [ ] No icon-only primary action: `Notify admin` (and `Ask what's wrong` when available) are
      text-labeled; `Check again` and any sign-out control have visible text or `aria-label`.
- [ ] When chat is unavailable, no disabled chat box is shown (the action is omitted).
- [ ] All interactive controls on the compact surface are ≥44×44 px at 390×844.
- [ ] No horizontal scroll; no element-bounds overflow at 390×844.
- [ ] Page title is plain (e.g. "Home status"), not "Server health".
- [ ] Status uses icon + text, not color alone.
- [ ] Admin panel (8787) is unchanged except for intentionally shared components.
- [ ] `go test ./...`, build, and `visual-check.ps1` (incl. new audit in §7) pass; PR includes
      390×844 screenshots of all five states.

## 7. Semantic / literacy audit additions for `cmd/visualcheck`

Extend `generalUserExpression` flags + `assertVisualFlags` (general-user/mobile-user-home path)
to make the guardrails enforceable, not just visual:

- `userHeroVisible` — a `#user-hero` element is present and non-empty (fail if absent).
- `bannedTermCount` — count of banned terms (§5) found in the visible text of `#user-home`
  excluding any `details[data-technical]:not([open])`; assert `== 0`.
- `iconOnlyPrimaryActionCount` — primary action buttons with no accessible label; assert `== 0`.
- Keep the existing `smallTouchTargetCount == 0` assertion (currently failing at ~40–42px) and
  `bodyHorizontalOverflow == false`.

## 8. Implementation pointers

- `web/public/app.js`
  - `renderUserHome(snapshot)` (~L433): add the hero card; relabel/remove the "Overall/Server/
    Router" technical rows; gate the technical rows behind the §3.4 disclosure.
  - `compactOverallSummary` / `compactServerSummary` / `compactRouterSummary`: these emit the
    leaking strings in §2 — replace with the §4/§5 plain-English logic (state-driven hero +
    plain app summaries). `compactAppSummary` (~L524) already strips the app-name prefix; keep
    that, ensure output is plain.
  - Page title hard-coded "Server health" (~L339/347/359): change to a plain title.
  - Primary actions / chat panel (`#user-notify-admin`, `#user-chat-*`): label actions; omit the
    chat box when unavailable.
- `web/public/styles.css`: hero card styles; enforce ≥44×44 on compact controls.
- `web/public/index.html`: `#user-home` markup (hero container, `details[data-technical]`).
- `internal/diagnostics/rules.go`: source of the admin-oriented summary strings. Prefer
  deriving general-user copy on the client from structured fields; only touch rules.go if a new
  structured field is genuinely needed (keep admin text intact).

## 9. Out of scope

Backend features/adapters, security policy, LLM provider behavior (beyond presenting an
existing chat answer), and the admin panel layout. If something here seems to require those,
stop and raise it rather than expanding scope.

## 10. UX-critique loop

After implementation, capture the §4 screenshots and run a critique pass (see the prompt in
`docs/agent-roadmap.md` → Multi-agent UX workflow). Feed fixes back one finding at a time.
