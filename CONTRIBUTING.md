# Contributing

NoobBoard runs unattended on someone's home server and can start and stop their
services. A wrong answer on the dashboard is worse than no answer, and a wrong
action is worse still. The practices below exist to keep that true as the
codebase grows.

Read `AGENTS.md` first for what the product is and which invariants must not
regress. `docs/architecture.md` is the map of the code.

## Setup

Go 1.25 or newer. Nothing else is required to build or test.

```powershell
& 'C:\Program Files\Go\bin\go.exe' build ./...
```

Caches are kept inside the checkout (`.cache/`) so the repo is self-contained:

```powershell
$env:GOCACHE = "$PWD\.cache\go-build"
$env:GOMODCACHE = "$PWD\.cache\go-mod"
$env:GOPATH = "$PWD\.cache\gopath"
```

The linter is a separate install, pinned to the version CI runs:

```powershell
& 'C:\Program Files\Go\bin\go.exe' install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2
```

## Before you push

One command runs every gate, in the order that fails fastest:

```powershell
.\scripts\check.ps1
```

That is build, `go vet`, `gofmt -l`, `golangci-lint`, `go test ./...`, and a
conflict-marker scan. `.github/workflows/ci.yml` runs the same steps plus
`go test -race` on Linux and a Windows build — **if you change one, change the
other.**

After any change under `web/public`, also run the browser regression harness:

```powershell
.\scripts\check.ps1 -Visual
```

Other flags: `-Fix` rewrites formatting instead of reporting it, `-SkipLint`
when the linter is not installed.

## Standards

**Formatting is not a review topic.** `gofmt` decides. `.gitattributes` pins the
working tree to LF so formatting means the same thing on Windows and in CI;
`.editorconfig` covers what an editor writes back.

**The linter is a real gate, not a suggestion.** `.golangci.yml` documents why
each linter is enabled — and, for the two that were tried and removed, how many
findings they produced and why none were defects. If a rule starts producing
noise, argue it out in that file with numbers rather than sprinkling `//nolint`.
An inline `//nolint` needs a reason on the same line, and the reason has to say
what makes the code correct, not that the linter is annoying.

**Errors travel wrapped.** `%w`, not `%v`, so `errors.Is` works through the
chain. A collector that cannot reach its service returns a *status* with
`SourceHealth` set, not an error — a failed collector must degrade one panel,
not blank the dashboard.

**Close write handles explicitly.** A deferred `Close` on a file you wrote to
runs after the return value is fixed, so a failure is reported as success. Read
handles may use `defer func() { _ = f.Close() }()`; write paths check the error
before declaring success.

## Comments

Comment the **why**, never the what. `// increment i` is noise; the reason a
threshold is 30 minutes, or why a field is a pointer rather than a bool, is not
recoverable from the code and belongs in the file.

The most valuable comment is the one that records a wrong turn. Several in this
codebase name the exact bug they prevent — a reviewer prompt that named no
operation, a metric store that reported success after a failed close, a label
that made 864,000 seconds render as "100%". Those stop the next person
reintroducing them.

## Tests

- **Test the behaviour, not the shape.** Assert against the registry or the
  chain head, not a hardcoded count that a future entry breaks.
- **A bug fix comes with the test that would have caught it.** Name what it
  pins, with the failure in the comment.
- **The live services are not a test dependency.** `fixtures/` provides
  deterministic telemetry; `internal/adapters/fixture` implements every
  collector interface.
- **UI changes need `cmd/visualcheck`.** It asserts structure, overflow, touch
  targets, and banned vocabulary on both surfaces at desktop and mobile sizes.
  If you add a UI affordance that matters, add the assertion that holds the
  line — the admin app-detail disclosure and the data-clear controls are both
  checked there for exactly that reason.

## Commits and pull requests

- One concern per commit. A whitespace normalisation and a behaviour change do
  not belong in the same diff.
- The message says **why**, and states what was actually verified. "Tests pass"
  when they were not run is worse than saying nothing.
- Work on a branch and open a PR. Do not commit to `main`.
- Never commit secrets. `*.key`, `auth*.txt`, `.env`, `config.local.yaml`,
  `data/`, `logs/`, and `dist/` are ignored — do not widen that list.

## Changing an invariant

The guardrails in `AGENTS.md` and the invariants in `docs/architecture.md` are
there because breaking them is costly and quiet. If a change needs one to move,
that is fine — but move it deliberately: update the document in the same commit,
say what replaces the guarantee, and add the test or harness assertion that
holds the new line.
