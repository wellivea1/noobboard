#Requires -Version 5.1
<#
.SYNOPSIS
    Updates an existing NoobBoard Windows service from GitHub.

.DESCRIPTION
    Syncs this checkout from GitHub, rebuilds NoobBoard, replaces the installed service
    binary, and restarts the app. This is the preferred path after NoobBoard has already
    been installed with install.ps1.

    This script is intentionally update-only: it refuses to run if the NoobBoard Windows
    service is not already installed. For a first install, run:

        .\install.ps1 -Start

    Existing firewall and auth settings are preserved by install.ps1 during the rebuild.

.PARAMETER Remote
    Git remote to fetch/pull from. Default: origin.

.PARAMETER Branch
    Git branch to update from. Default: main.

.PARAMETER RunTests
    Run "go test ./..." before rebuilding and restarting.

.PARAMETER InstallDir
    Service binary location. Must match the existing install if a non-default location was used.
    Default: "%ProgramFiles%\NoobBoard".

.EXAMPLE
    # From an elevated PowerShell prompt:
    .\update.ps1

.EXAMPLE
    # Update from a specific branch and run tests first:
    .\update.ps1 -Branch main -RunTests
#>
[CmdletBinding()]
param(
    [string]$Remote = 'origin',
    [string]$Branch = 'main',
    [switch]$RunTests,
    [string]$InstallDir = (Join-Path $env:ProgramFiles 'NoobBoard')
)

$ErrorActionPreference = 'Stop'
$RepoRoot = Split-Path -Parent $MyInvocation.MyCommand.Path
Set-Location $RepoRoot

$ServiceName = 'NoobBoard'

function Write-Step($msg)  { Write-Host "`n==> $msg" -ForegroundColor Cyan }
function Write-Ok($msg)    { Write-Host "    $msg" -ForegroundColor Green }
function Write-Warn2($msg) { Write-Host "    $msg" -ForegroundColor Yellow }

function Test-Admin {
    $id = [Security.Principal.WindowsIdentity]::GetCurrent()
    $p  = New-Object Security.Principal.WindowsPrincipal($id)
    return $p.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}

function Invoke-Native([string]$FilePath, [string[]]$Arguments, [string]$FailureMessage) {
    & $FilePath @Arguments
    if ($LASTEXITCODE -ne 0) {
        throw "$FailureMessage (exit $LASTEXITCODE)."
    }
}

Write-Step 'Checking update prerequisites'
if (-not (Test-Admin)) {
    throw 'NoobBoard updates require Administrator privileges because the Windows service is replaced and restarted. Re-run from an elevated PowerShell prompt.'
}

if (-not (Get-Command git -ErrorAction SilentlyContinue)) {
    throw 'git was not found on PATH. Install Git for Windows or run from a shell where git is available.'
}

$installScript = Join-Path $RepoRoot 'install.ps1'
if (-not (Test-Path -LiteralPath $installScript)) {
    throw "install.ps1 was not found at $installScript."
}

$service = Get-Service -Name $ServiceName -ErrorAction SilentlyContinue
if (-not $service) {
    throw "The NoobBoard Windows service is not installed. Use '.\install.ps1 -Start' for the first install, then use '.\update.ps1' for later updates."
}
Write-Ok "Found installed service '$ServiceName' ($($service.Status))."

Invoke-Native 'git' @('rev-parse', '--is-inside-work-tree') 'This directory is not a git checkout'

$trackedStatus = & git status --porcelain --untracked-files=no
if ($LASTEXITCODE -ne 0) {
    throw "Could not inspect git status (exit $LASTEXITCODE)."
}
if ($trackedStatus) {
    Write-Warn2 'Tracked local changes are present:'
    $trackedStatus | ForEach-Object { Write-Host "    $_" -ForegroundColor Yellow }
    throw 'Refusing to update over tracked local changes. Commit, stash, or discard them before running update.ps1.'
}
Write-Ok 'Tracked working tree is clean.'

Write-Step "Syncing source from GitHub ($Remote/$Branch)"
Invoke-Native 'git' @('fetch', $Remote, $Branch) "git fetch $Remote $Branch failed"

$currentBranch = (& git branch --show-current).Trim()
if ($LASTEXITCODE -ne 0) {
    throw "Could not determine the current git branch (exit $LASTEXITCODE)."
}
if ($currentBranch -ne $Branch) {
    Write-Ok "Switching from '$currentBranch' to '$Branch'."
    Invoke-Native 'git' @('checkout', $Branch) "git checkout $Branch failed"
}

Invoke-Native 'git' @('pull', '--ff-only', $Remote, $Branch) "git pull --ff-only $Remote $Branch failed"
$head = (& git rev-parse --short HEAD).Trim()
if ($LASTEXITCODE -ne 0) {
    throw "Could not read the updated git revision (exit $LASTEXITCODE)."
}
Write-Ok "Source synced at $head."

Write-Step 'Rebuilding and restarting NoobBoard'
$installArgs = @(
    '-NoProfile',
    '-ExecutionPolicy', 'Bypass',
    '-File', $installScript,
    '-Start',
    '-InstallDir', $InstallDir
)
if ($RunTests) {
    $installArgs += '-RunTests'
}

& powershell.exe @installArgs
if ($LASTEXITCODE -ne 0) {
    throw "install.ps1 failed during update (exit $LASTEXITCODE)."
}

Write-Step 'Done'
Write-Ok 'NoobBoard source was synced, rebuilt, and the service was restarted.'
