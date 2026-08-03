# NoobBoard UI standards

This is the design system. Read it before adding, moving, or restyling anything
in `web/public/`. It exists because the previous UI was not badly designed so
much as *undesigned*: it accreted one component at a time, each locally
reasonable, with no system tying them together, and every visual decision was
made twice — once per surface — and drifted.

The rules below are deliberately narrow. A system that permits three ways to
show a state will be used in three ways.

---

## 0. Where the rules live

| Layer | File | What it decides |
|---|---|---|
| Tokens | `web/public/styles.css` (`:root`) | Every colour, size, radius, duration |
| Primitives | `web/public/styles.css` | Container roles, status label, controls |
| Composition | `web/public/index.html` | Page skeletons, landmarks |
| Behaviour | `web/public/app.js` | Rendering, filters, disclosure |
| Enforcement | `cmd/visualcheck/main.go` | Automated audits of the rules below |

`scripts/visual-check.ps1` is not optional. It renders both surfaces at desktop
and 390×844, and fails the build on overflow, undersized touch targets, banned
vocabulary on the compact surface, and the structural rules in §9.

---

## 1. References this system is built on

These are the sources the rules are drawn from. Where they disagree, the order
below is the tiebreak order.

1. **WCAG 2.2** — the only hard floor. 1.4.3 Contrast (Minimum, AA) 4.5:1 for
   body text and 3:1 for large text; 1.4.11 Non-text Contrast (AA) 3:1 for the
   parts of a control that identify it; 2.4.7 Focus Visible; 2.5.8 Target Size
   (Minimum, AA) 24×24 CSS px, with 2.5.5 (AAA) 44×44 as our own floor on
   anything a finger touches; 1.4.1 Use of Colour — colour is never the only
   carrier of meaning.
2. **Apple Human Interface Guidelines** — 44×44 pt minimum hit target; the
   compact surface's audience is on iPhones and this is the number they are
   calibrated to. Material Design 3's 48×48 dp is compatible; we use 44.
3. **Nielsen's 10 usability heuristics** — in particular *visibility of system
   status* (the verdict line), *match between system and the real world* (plain
   English on the compact surface), *recognition rather than recall* (settings
   search), and *aesthetic and minimalist design* (empty states that step aside).
4. **Gestalt grouping** — proximity and common region do the work that borders
   used to. Whitespace groups; a box is a last resort.
5. **Tufte, data-ink ratio** — every pixel that is not data has to justify
   itself. Applied literally to the admin surface: a value in a table needs no
   capsule around it.
6. **Fitts's law** — the most frequent target is the largest and the closest.
   On the compact surface the whole tile is the target, not a chevron inside it.
7. **Refactoring UI** (Wathan & Schoger) — hierarchy comes from de-emphasising
   the secondary, not from shouting the primary; limit the palette; use a
   restricted, non-linear size scale.
8. **GOV.UK Design System** — one thing per page, progressive disclosure, and
   plain-language error and status copy.

---

## 2. Principles

**P1. Colour means exactly one thing: state.**
Interaction (hover, selection, the primary button) is expressed with the neutral
ramp — fill, border, weight. Nothing that is not a status may use a status hue.
The previous UI used one pale blue-grey simultaneously for active nav, primary
button, link, "success", and "feature available", which left real status confined
to 6px dots.

**P2. Structure is carried by containers, not by text size.**
Three container roles exist (§5). If everything is a rounded rect with a 1px
border and a near-black fill, all hierarchy has to come from type, and no type
scale is wide enough to carry it alone.

**P3. A page answers one question.**
Overview answers "is it okay right now". The answer is the first thing on the
page, at the only display size in the product. Everything else on the page is
support for that answer.

**P4. The healthy state should be nearly empty.**
Nothing is wrong ~95% of the time. Optimise for that: the verdict collapses to a
sentence, and panels with nothing to say hide themselves rather than rendering
"No disk data / No capacity data / No parity check data" as if those were values.

**P5. A signal used everywhere is not a signal.**
Uppercase has exactly one job (§4). There is one status label (§6). There are no
decorative capsules.

**P6. One component set, two surfaces.**
The admin panel and the compact app run on two ports from two bundles and that
security boundary does not move. But they share tokens and primitives, because
most of the drift between them was never a design decision — it was two people
solving the same problem twice.

**P7. Navigation is not content.**
A page title that repeats the nav item you just clicked is overhead. It renders
only where the navigation is not on screen. The same rule applies to nesting:
one page never carries two levels of permanent navigation plus a third inside
the pane. Settings broke this and cost 750px of a 1440px screen before any
content began.

**P8. Density follows the input device, not the brand.**
A phone gets 44px targets and 15px type because a finger is imprecise and the
screen is close. A desktop console gets 28px controls and 13px type because a
mouse is precise and the operator wants a screenful of state. These are the
same design at two densities, expressed entirely as token overrides — see §3.

---

## 3. Tokens

Never write a raw colour, size, radius, or duration in a rule. If no token fits,
the design is drifting — change the design, not the token list. As of this
document there are **zero** raw hex values in `styles.css` outside `:root`.

### Surfaces

| Token | Use |
|---|---|
| `--surface-canvas` | The page itself |
| `--surface-sunken` | Wells: inputs, code blocks, logo plates |
| `--surface-raised` | Panels, sidebar, cards, tiles |
| `--surface-overlay` | Menus, dialogs, hovered rows |

A container may sit **one** step above its parent. Two steps means the hierarchy
is wrong, not that a third surface is needed. There are no shadows: elevation is
a surface step plus a hairline.

### Lines

`--line` (default), `--line-soft` (peers inside one container), `--line-strong`
(a boundary you are meant to notice), `--line-control`.

`--line-control` is the only hairline WCAG 1.4.11 applies to, because it is the
sole indicator of an input's boundary. It clears 3:1 against every surface. The
others are decorative structure and are deliberately quieter.

### Ink

Three levels, and there is no fourth. If text needs to be quieter than `--ink-3`
it should not be on the screen.

| Token | Contrast on canvas / raised / overlay | Use |
|---|---|---|
| `--ink-1` | 17.0 / 15.9 / 14.4 | Content and answers |
| `--ink-2` | 9.1 / 8.5 / 7.7 | Supporting copy, labels |
| `--ink-3` | 6.0 / 5.6 / 5.1 | Timestamps, placeholders, keys |

### Status

Exactly four states. Everything in the product maps onto one of them, and
nothing that is not a status may use these hues.

| State | Meaning |
|---|---|
| `ok` | working / healthy / allowed |
| `warn` | degraded / needs attention |
| `bad` | not working / failed / denied |
| `neutral` | unknown / not measured / not applicable |

Each has three steps: the bare hue (indicators, ≥3:1 — **not** legible enough
for text), `-ink` (any status rendered as a word, ≥10:1), and `-wash` (tile
fills on the compact surface only).

### Type scale

Six steps. Each has one job. Do not add a seventh, and **do not set a
`font-size` outside the scale** — the only exceptions in the stylesheet are
`::before`/`::after` glyphs, which are icon metrics sized to their box.

| Token | Touch | Desktop admin | Job |
|---|---|---|---|
| `--text-display` | 1.75rem | 1.25rem | The one answer a page exists to give. **Max one per screen.** |
| `--text-title` | 1.0625rem | 0.9375rem | Section heading (`h2`) |
| `--text-subtitle` | 0.9375rem | 0.875rem | Sub-section heading (`h3`), card heading |
| `--text-body` | 0.9375rem | 0.8125rem | Running text, control labels |
| `--text-meta` | 0.8125rem | 0.75rem | Secondary lines, table cells, metadata |
| `--text-micro` | 0.75rem | 0.6875rem | Group headings and column labels only. Never a sentence. |

The old scale had one 2.55rem `h1` and clustered everything else between
0.78–0.9rem, which is why the containers were carrying all the hierarchy.

**The scale needs a base.** `body` sets `font-size: var(--text-body)`. Without
it, any element whose own rule did not set a size falls through to the browser
default of 16px — which is off the scale entirely, and is how a detail-row value
ended up rendering at 16px beside a 12px label. Setting it on `body` is also
what makes the density block below reach everything that has not opted out.

### Weight

Three: `--weight-normal` 400, `--weight-medium` 500, `--weight-strong` 600. The
old sheet ran to 620–900 on ordinary labels and values, which is why everything
read as emphasised and nothing read as important. `<strong>` inside a data row
is structural (it pairs with a label), so it takes the medium weight rather than
the browser's 700.

### Density

One media block in `styles.css` overrides tokens for `body.admin-view` at
≥861px. Nothing else in the stylesheet knows which density it is in, and a
component that needs a density exception is a component that is wrong.

| | Touch (compact surface; admin <861px) | Desktop admin (≥861px) |
|---|---|---|
| `--control-height` | 44px | 28px |
| control padding | 0.62rem / 0.72rem | 0.25rem / 0.45rem |
| `--pad-panel` | 16px | 12px |
| `--pad-row` | 8px | 5px |
| `--nav-width` | 220px | 184px |
| Settings form row | label above control | label left in a `--label-col` column, control right |
| Settings form width | full width | rows capped at `--form-max` |
| Monitor / status row | name over summary | name and summary on one baseline |
| Diagnostics | ask above answer | ask and answer side by side |
| App row | stacked card | one table row: identity · state · metadata · actions |

The 44px floor is a **touch** requirement (Apple HIG; WCAG 2.5.5) and the visual
harness audits it exactly where a finger is the input device. With a mouse the
floor is WCAG 2.5.8's 24px.

### Space, shape, motion

4px base (`--space-1` … `--space-10`). Gaps between peers use 2–4; between
sections 5–6; `--space-10` is page-level only.

`--radius-sm` 3px controls and chips · `--radius-md` 5px panels and cards ·
`--radius-lg` 12px compact tiles and dialogs · `--radius-pill` indicators only.
Radii stay small deliberately: a large corner radius reads as consumer
software, and this is an instrument panel.

`--motion-fast` (120ms) for state changes, `--motion-base` (180ms) for
movement, both with `--ease`. Everything animated must be disabled under
`prefers-reduced-motion`.

---

## 4. Capitals

**Uppercase is used by exactly one rule in the stylesheet**, and that rule names
a *group* of things — a settings group, a table column, a drawer section.

It is never used for:

- a field label (`NAS`, `UNRAID API`)
- a metadata key (`DOCKER`, `IMAGE`, `SOURCE`)
- a value or state word (`ONLINE`, `STARTED`, `READY`, `OFF`)

To add a capitalised heading, join the shared selector list in `styles.css`. Do
not restate the treatment locally — and *do* join it when you add a heading that
names a group, including a `<summary>`. Every disclosure heading in Settings
belongs to this list; the ones that were missing from it read as ordinary bold
text and flattened the hierarchy they were supposed to create.

Everything else — headings, buttons, nav items, labels — is **sentence case**.
"Server health", not "Server Health".

---

## 5. Container roles

There are three. There is no fourth.

**Panel** — a titled region of a page. One hairline, one surface step,
`--radius-md`. **Panels do not nest.** A panel inside a panel is a structure
error, not a styling problem; use a sub-section heading. The old Settings page
was four levels deep (`Runtime settings` → `Runtime Settings` → `SETTINGS
SECTIONS` → `LLM`) and none of the levels added information.

**Row** — one member of a list inside a panel. No border and no fill of its own;
peers are separated by a single hairline; the last row has none. Rows carry
status, not chrome. Admin lists are rows.

**Tile** — a large tappable summary object, **compact surface only**. Status is
the tile's *fill*, so a tile needs no dot and no pill: the broken thing is the
only saturated object on screen. Touch targets are large by construction,
because the smallest tappable object is a whole tile.

---

## 6. The status label

One object, three class names, identical treatment: a coloured indicator plus a
sentence-case word, on no background and inside no box.

- `.status` — a live state (online / degraded / offline / unknown)
- `.severity` — an incident or fact severity
- `.pill` — a standing fact about the session (role, data source)

Shape carries the state as well as colour, satisfying WCAG 1.4.1: **circle** =
ok, **triangle** = degraded, **square** = offline, **ring** = unknown. This is
why `.status-dot-only` is safe to use in dense rows.

**Do not add a filled or bordered variant.** The nine capsules this replaced
(`NOT CONNECTED`, `FIX PATH AVAILABLE`, `ON, UP TO 2 CALLS`, `READY`, `OFF`,
`SAVED`, `Medium`, `Admin`, `Fixture Data`) were the same concept in four shapes,
which is how rows ended up triple-encoding one state.

State a state **once per row**. A red square *and* an uppercase `OFFLINE` *and* a
coloured capsule is three encodings of one fact.

---

## 7. Page patterns

**Utility bar.** Global actions, right-aligned, no border. The page name renders
only below 861px on the admin surface, where the sidebar has become a drawer.
On the compact surface it always renders — that surface has no sidebar. The
page description survives as `#summary`, visually hidden: a restatement of the
title is useful to a screen reader and to nobody else.

**Verdict.** The single display-size answer, plus one supporting line and at most
two actions. Only Overview has one.

**Two-column body.** Primary content left, "whatever is currently true" right.
The right column and its panels hide when empty, and the layout collapses to one
column — which is what fixed the 40–60% empty viewport, not more data.

**Stream.** Aligned columns: time (tabular numerals, short form for today),
kind indicator, what happened, actor. Expandable rows use `<details>`; the
expanded body must not repeat what the summary row already shows.

**Settings.** Section tabs and search share one toolbar row **across the top of
the pane**, not down its side: the app already has a permanent sidebar, and the
role editor carries its own list+detail, so a vertical settings rail made three
columns of navigation. One focused section at a time, full pane width. Long
sections collapse into disclosure groups whose headings are the summaries; the
first group is open. Search opens matching groups, and never moves the pane out
from under an unsaved edit. The section title and its Save control share the
card's header row.

**Tab strip vs. segmented control.** They look similar and are not the same
component. A `.segmented` control divides one row between a fixed set of equal
options and may stretch them (the Apps and Activity filters). A tab strip is a
list of destinations that must keep their labels and scroll when there is no
room (`.settings-menu`). Reusing `.segmented` for the section tabs made them
inherit `flex: 1 0 70px` and clip their own labels on a phone.

**Form rows.** On a desktop admin pane a field is label-left in a fixed
`--label-col` column, control right, hairline between rows. Stacking a label
over a full-width control is the touch layout, and it is what made a settings
section 2,000px tall.

**Anchor the form, not the control.** The row is capped at `--form-max`, and
the control fills what is left of it. Capping each control instead left
dropdowns stranded mid-pane — starting after the label column, ending nowhere
in particular, with no shared right edge to read down. Every control in a
section now lands on the same two edges. It follows that every field container
in Settings is **one column**: a multi-column grid of label-left rows squeezes
the label against the control.

**One boolean per row.** A grid of `label ▸ [x]` cells puts every checkbox
immediately to the *left of the next label*, so "Status chat [x] Server health
[x]" reads as the box belonging to the following label. Proximity beats
alignment: one row each, hairline between, control on the same right edge as
every other control. And a checkbox is always on the **right** — Settings had
two boolean row types that read in opposite directions.

**Section headers are a band, not a line of text.** A card title that is the
same size and weight as the group headings under it, with nothing between them,
is a floating title: the pane opens with three lines of near-identical text and
no way to tell which one names it. The title is one step up in size, in
full-contrast ink, over a rule that spans the card and that the Save control
shares. Group headings below it take the uppercase micro-label (§4), which is
what makes the two levels legible as levels.

**Empty states.** If a panel has nothing to say, hide the panel. Do not render
"No X" styled exactly like a value — and do not put it in a box. A dashed border
with a fill around one sentence is a container pretending there is something in
it; the sentence alone says the same thing in a third of the room. The same
applies to output wells: a chat or diagnosis panel reserves its height once
there is something to show, not before.

**One toolbar row per page.** Search, filters and the page's reload live on a
single line above the content they act on; the count lives in the panel's own
head beside its title. Activity had a title block, a toolbar and a count row —
three bands of chrome, ~100px, before the first event.

**Actions do not shrink.** In a flex header or toolbar the copy takes
`flex: 1 1 auto; min-width: 0` and the action takes `flex: 0 0 auto`. Left to
the default the button shrinks instead and wraps its own label mid-word
("Reloa / d"), because `button.command` wraps by design for narrow phones.

**A scanning column is fixed-width.** Sized to its content, the Activity
timestamp column was 34px on a row from today and 76px on an older one, which
shifted every indicator and title by a different amount and destroyed the
column the stream exists to be read down. If the eye is meant to run down it,
give it a width.

---

## 8. Content and voice

- Sentence case everywhere.
- Name the thing. "Emby is not running", not "An error occurred".
- Say what to do next, or say that there is nothing to do.
- **Admin** may use precise technical vocabulary — array, container, WAN, parity.
- **Compact must not.** Docker, container, Unraid, array, parity, endpoint,
  GraphQL, WAN, API, SSH, probe and telemetry are banned from the default
  compact view and belong to a collapsed "Technical details" disclosure at most.
  `cmd/visualcheck` fails the build on these; app display names are exempted by
  wrapping them in `[data-app-name]`.
- Never present fixture data as live. The source label is not decoration.

---

## 9. What the harness enforces

Changing these means changing `cmd/visualcheck/main.go` deliberately, not
working around a failure.

- No horizontal overflow on `body` at any tested width; no element escaping its
  container.
- No visible tappable control under 44×44 on the compact surface.
- No banned technical vocabulary in the compact view or its drawer.
- The assistant launcher never overlaps an interactive control.
- The admin page title renders below 861px and does not render above it.
- Exactly one active nav item; exactly one visible settings section.
- Activity merges at least two sources; its filter and its search both narrow
  the stream and restore it.
- Settings search narrows the section list and restores it; the debug snapshot
  exists in Settings → Advanced and is collapsed.
- Buttons do not clip their own text — and the failure names the offending
  button, so it is an address rather than a boolean.

---

## 9a. Charts

Charts follow the same rules as everything else, plus these. The full method is
the `dataviz` skill; what is binding here is how it lands in this product.

- **Colour still means state.** A data line is neutral ink (`--ink-2`), not a
  hue. The only hue on a plot is a status band — on the latency charts, failed
  checks. A series is never coloured for identity when neutral will do.
- **Small multiples over shared axes.** Latency for four links is one measure at
  four scales; on one linear axis the 2ms LAN hop is a flat smudge. One chart
  per subject, each with its own y-scale. Never a dual-axis plot.
- **One series per chart needs no legend** — the caption names it.
- **Direct-label the endpoint only.** A number on every point goes unread.
- **Grid and axes are solid hairlines.** Dashed gridlines read as a threshold
  when they are only a grid.
- **Every chart has a table twin.** No value may be reachable only by hovering.
- **Size the box to include the axis band**, or the card grows a nested
  scrollbar that crops the labels.
- **`tabular-nums` on axis ticks and table cells**, where numbers align
  vertically — not on a standalone figure.
- **Charts are inline SVG built with `svgNode`.** No chart library: the CSP
  forbids external scripts, and the tokens are already the design system.

---

## 9b. Decisions and generated answers

Two patterns that keep going wrong, written down so they stop.

**A decision dialog is buttons, not a form.** If the user is choosing between a
small fixed set of actions, each action is its own button and pressing it *is*
the choice. Radio group plus a Submit button makes the user act twice for one
decision, and a submit label that mutates with the selection means the button
they are about to press says something different from what they read a moment
ago.

- **The button carries the operation and its target** — "Start EmbyServer", not
  "Allow fix". A button label that needs the surrounding paragraph to be
  actionable is not a label.
- **The dialog title is the decision** — "Start EmbyServer?", not "Allow
  automatic fix?". The user should not read three blocks to find out what is
  being proposed and on what.
- **The affirmative comes first**, matching every other action row.
- **One sentence of consequence**, in the live region, naming what will run and
  what will not. Not a progress stepper: a single click has no steps.
- **A disabled action states why**, where the eye already is, not only in a
  tooltip.

**A generated answer is structured, not a paragraph stack.** Model output lands
in fixed slots, and each slot gets the shape its content already has.

- **Verdict, cause, evidence, next step — in that order.** One status pill for
  the verdict (severity is a state), confidence as muted meta beside it.
- **A list is a list.** Evidence renders as `<ul>`; joining discrete facts with
  semicolons turns four observations into one run-on sentence nobody finishes.
- **Constrain length at the schema, not the CSS.** A model told "at most two
  sentences" writes two; a model truncated afterwards writes a wall of text that
  gets cut mid-word. Keep a server-side word-boundary trim as the backstop for
  providers that ignore the limit.
- **Label the action line.** "NEXT" in micro caps beats a fourth grey paragraph.

---

## 9c. The admin surface is a superset

The compact surface had a per-app detail view — recent changes, last seen
online, seven-day uptime — and the admin surface had none. An admin looking at
the same app saw strictly less than the person they administer for.

**Anything the compact surface shows about a subject, the admin surface shows
too, plus what only an admin may see.** Not the same view twice: the admin
version carries the raw form (status transition pairs, not "Came back"), the
figures the system's own rules read, and the operator-only data — container
logs, exit codes, the counts behind a diagnosis. The harness asserts this for
apps; hold the same line for anything added later.

Two corollaries:

- **An endpoint the admin API already exposes should be reachable from the admin
  UI.** Container logs existed as a route and an agent tool for months with no
  button anywhere — which is also how a broken query went unnoticed.
- **Anything the system records, an admin can inspect and clear.** Recorded
  history feeds diagnoses, so when it stops describing reality it has to be
  correctable from the dashboard, not by editing a file on disk. Show the count
  before the clear, name what reads it, and audit the deletion.

---

## 10. Checklist for new UI

1. Does this page answer one question, and is the answer first?
2. Am I adding a container role, a type size, a weight, a status shape, or a
   capital letter that does not already exist? If so, stop.
3. Every colour, font-size, weight, radius and duration from a token? (Glyph
   metrics in `::before`/`::after` are the only exception.)
4. Is any state encoded more than once in the same row?
5. Panel inside a panel?
6. Does the empty case hide, or does it render "No X" as data — or worse, as a
   box?
7. Does anything inherit 16px because its rule set no size?
8. Sentence case? Plain English if it can reach the compact surface?
9. ≥44px for anything touchable; visible focus ring; not colour-only.
10. Does it survive 390×844 and 1440×900 with no horizontal scroll?
11. At 1440×900, is the answer to "what is going on" above the fold without
    scrolling?
12. `go test ./...`, `go build`, **and** `scripts/visual-check.ps1`.

---

## 11. Known gaps

Honest list of what this system does not yet cover.

- **Light theme.** Tokens are semantic and a light theme is a token swap, but
  the values are dark-only today and `index.html` still advertises a light
  `theme-color`. Direction B in the design study was light-first; that was not
  adopted, and the app is deliberately dark-only until the swap is built.
- **Settings groups are collapsed, not separate sheets.** The LLM section fell
  from a ~2,270px wall to ~460px collapsed, which is a large improvement but
  not the "focused sheet per setting" the design study recommended.
- **Compact tiles are applied to the app list only.** Infrastructure rows on the
  compact surface are still list rows.
- **No automated contrast check.** The ratios in §3 were computed by hand
  against the four surfaces; nothing recomputes them when a token changes.
- **No automated token check.** "Every value from a token" is a rule the harness
  does not enforce; it is currently true (zero raw hex, zero raw weights, and
  raw `font-size`/`border-radius` only on glyph and shape exceptions) but
  nothing stops the next raw value from being added.
- **The role editor still nests a list+detail inside a settings section.** It
  now has the width for it, but a role picker plus one detail pane would be
  simpler than a permanent second list.
