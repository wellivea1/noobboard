# One command that runs every gate this repository enforces, in the order that
# fails fastest. The point is that "did I break anything?" has a single answer
# rather than four commands a contributor has to remember and CI has to be kept
# in sync with — .github/workflows/ci.yml runs the same steps.
#
#   .\scripts\check.ps1              build, vet, format, lint, test, markers
#   .\scripts\check.ps1 -Visual      also run the browser regression harness
#   .\scripts\check.ps1 -Fix         rewrite formatting instead of reporting it
#   .\scripts\check.ps1 -SkipLint    when golangci-lint is not installed
param(
  [switch]$Visual,
  [switch]$Fix,
  [switch]$SkipLint,
  [string]$GoExe = "C:\Program Files\Go\bin\go.exe"
)

$ErrorActionPreference = "Stop"
$Root = Split-Path -Parent $PSScriptRoot
Set-Location $Root

# Caches live inside the repo so a checkout is self-contained and a machine
# without a configured GOPATH still builds.
$env:GOCACHE = Join-Path $Root ".cache\go-build"
$env:GOMODCACHE = Join-Path $Root ".cache\go-mod"
$env:GOPATH = Join-Path $Root ".cache\gopath"
$GoBin = Join-Path $env:GOPATH "bin"
$GoFmt = Join-Path (Split-Path -Parent $GoExe) "gofmt.exe"

$failures = New-Object System.Collections.Generic.List[string]

function Start-Step {
  param([string]$Name)
  Write-Host ""
  Write-Host "== $Name" -ForegroundColor Cyan
}

function Add-Failure {
  param([string]$Name)
  $failures.Add($Name)
  Write-Host "   FAIL $Name" -ForegroundColor Red
}

Start-Step "build"
& $GoExe build ./...
if ($LASTEXITCODE -ne 0) { Add-Failure "build" }

Start-Step "vet"
& $GoExe vet ./...
if ($LASTEXITCODE -ne 0) { Add-Failure "vet" }

Start-Step "gofmt"
if ($Fix) {
  & $GoFmt -w cmd internal
  Write-Host "   rewrote formatting"
} else {
  $unformatted = & $GoFmt -l cmd internal
  if ($unformatted) {
    $unformatted | ForEach-Object { Write-Host "   $_" }
    Write-Host "   run: .\scripts\check.ps1 -Fix" -ForegroundColor Yellow
    Add-Failure "gofmt"
  }
}

Start-Step "lint"
if ($SkipLint) {
  Write-Host "   skipped (-SkipLint)"
} else {
  $linter = Join-Path $GoBin "golangci-lint.exe"
  if (-not (Test-Path $linter)) {
    # Not fatal locally: CI is the enforcing copy. Say how to get it rather than
    # blocking someone who only touched a doc.
    Write-Host "   golangci-lint not installed - skipping." -ForegroundColor Yellow
    Write-Host "   install: & '$GoExe' install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest"
  } else {
    & $linter run ./...
    if ($LASTEXITCODE -ne 0) { Add-Failure "lint" }
  }
}

Start-Step "test"
& $GoExe test ./...
if ($LASTEXITCODE -ne 0) { Add-Failure "test" }

# A merge that leaves conflict markers behind still compiles when the markers
# land in Markdown, and that has reached main here before. Scan the whole tree,
# not just the files git listed as conflicted.
Start-Step "conflict markers"
$markerHits = Get-ChildItem -Path cmd, internal, web, docs, scripts -Recurse -File -Include *.go, *.js, *.css, *.html, *.md, *.ps1 -ErrorAction SilentlyContinue |
  Select-String -Pattern '^(<<<<<<< |>>>>>>> |=======$)' -ErrorAction SilentlyContinue
if ($markerHits) {
  $markerHits | ForEach-Object { Write-Host "   $($_.Path):$($_.LineNumber)" }
  Add-Failure "conflict markers"
}

if ($Visual) {
  Start-Step "visual regression"
  & powershell.exe -NoProfile -ExecutionPolicy Bypass -File (Join-Path $PSScriptRoot "visual-check.ps1")
  if ($LASTEXITCODE -ne 0) { Add-Failure "visual regression" }
}

Write-Host ""
if ($failures.Count -gt 0) {
  Write-Host "FAILED: $($failures -join ', ')" -ForegroundColor Red
  exit 1
}
Write-Host "All checks passed." -ForegroundColor Green
if (-not $Visual) {
  Write-Host "UI change? Also run: .\scripts\check.ps1 -Visual" -ForegroundColor Yellow
}
exit 0
