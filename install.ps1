#Requires -Version 5.1
<#
.SYNOPSIS
    Checks for and installs NoobBoard's dependencies, builds the app, and installs it.

.DESCRIPTION
    1. Ensures a new-enough Go toolchain is present (installs it via winget if missing).
    2. Downloads Go module dependencies and builds a self-contained noobboard.exe.
    3. Installs the app: by default, copies the binary to a stable location and registers
       the NoobBoard Windows service (requires an elevated/Administrator prompt).
    4. Optionally prompts to add a LAN/WAN firewall rule and to set up the admin login now.

    The compiled binary embeds the web frontend, so a single .exe is all that gets installed.

    The admin-login prompt only seeds the *bootstrap* credentials in the service config; it
    does not mark setup as complete, so the future in-app setup wizard still runs and simply
    continues from the accounts step. See docs/agent-roadmap.md and docs/deployment-windows.md.

.PARAMETER NoService
    Build only. Skip copying to InstallDir and registering the Windows service.
    Useful for local development; run the app with: .\dist\noobboard.exe serve

.PARAMETER Start
    Start the NoobBoard service immediately after installing it.

.PARAMETER RunTests
    Run "go test ./..." before building.

.PARAMETER AllowLan
    Add the firewall rule (Private + Public profiles) without prompting (non-interactive
    "yes"). When neither -AllowLan nor -NoFirewall is given and the session is interactive,
    the installer asks.

.PARAMETER NoFirewall
    Never add the firewall rule and do not prompt (non-interactive "no").

.PARAMETER InstallDir
    Where the service binary is installed. Default: "%ProgramFiles%\NoobBoard".

.PARAMETER GoMinVersion
    Minimum acceptable Go version. Default: 1.25.0 (matches go.mod).

.EXAMPLE
    # From an elevated PowerShell prompt, install and start the service:
    .\install.ps1 -Start

.EXAMPLE
    # Just install dependencies and build, no service:
    .\install.ps1 -NoService
#>
[CmdletBinding()]
param(
    [switch]$NoService,
    [switch]$Start,
    [switch]$RunTests,
    [switch]$AllowLan,
    [switch]$NoFirewall,
    [string]$InstallDir = (Join-Path $env:ProgramFiles 'NoobBoard'),
    [string]$GoMinVersion = '1.25.0'
)

$ErrorActionPreference = 'Stop'
$RepoRoot = Split-Path -Parent $MyInvocation.MyCommand.Path
Set-Location $RepoRoot

function Write-Step($msg)  { Write-Host "`n==> $msg" -ForegroundColor Cyan }
function Write-Ok($msg)    { Write-Host "    $msg" -ForegroundColor Green }
function Write-Warn2($msg) { Write-Host "    $msg" -ForegroundColor Yellow }

function Test-Admin {
    $id = [Security.Principal.WindowsIdentity]::GetCurrent()
    $p  = New-Object Security.Principal.WindowsPrincipal($id)
    return $p.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}

function Confirm-Firewall {
    # Decide whether to add the inbound firewall rule.
    # -AllowLan forces yes, -NoFirewall forces no, otherwise prompt (recommended = yes).
    if ($NoFirewall) { return $false }
    if ($AllowLan)   { return $true }
    if (-not [Environment]::UserInteractive) {
        Write-Warn2 'Non-interactive session; skipping the firewall rule. Re-run with -AllowLan to add it.'
        return $false
    }
    $title   = 'Allow network access through Windows Firewall?'
    $message = 'Add an inbound firewall rule for NoobBoard (TCP 8787-8788, Private + Public ' +
               'profiles) so other devices on your network can reach it. Without it, NoobBoard ' +
               'is reachable only from this machine.'
    $yes = New-Object System.Management.Automation.Host.ChoiceDescription(
        '&Yes (recommended)', 'Add the firewall rule to allow LAN/WAN access.')
    $no  = New-Object System.Management.Automation.Host.ChoiceDescription(
        '&No', 'Leave the firewall unchanged; access stays local to this machine.')
    $choices = [System.Management.Automation.Host.ChoiceDescription[]]@($yes, $no)
    $answer  = $Host.UI.PromptForChoice($title, $message, $choices, 0)  # default = Yes
    return ($answer -eq 0)
}

function Confirm-AuthSetup {
    # Ask whether to set up the admin login now. Skips silently in non-interactive sessions.
    if (-not [Environment]::UserInteractive) {
        Write-Warn2 'Non-interactive session; skipping admin login setup.'
        return $false
    }
    $title   = 'Set up the NoobBoard admin login now?'
    $message = 'Choose the admin username and password used to sign in. If you skip this, the ' +
               'default development login (admin / change-me-now) applies until you change it, ' +
               'and the in-app setup wizard will ask for it when that feature ships.'
    $yes = New-Object System.Management.Automation.Host.ChoiceDescription(
        '&Yes (recommended)', 'Enter an admin username and password now.')
    $no  = New-Object System.Management.Automation.Host.ChoiceDescription(
        '&No', 'Skip; use the default login or set it later in the app/wizard.')
    $choices = [System.Management.Automation.Host.ChoiceDescription[]]@($yes, $no)
    return ($Host.UI.PromptForChoice($title, $message, $choices, 0) -eq 0)
}

function ConvertFrom-SecureToPlain([System.Security.SecureString]$Secure) {
    if (-not $Secure) { return '' }
    $bstr = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($Secure)
    try { return [Runtime.InteropServices.Marshal]::PtrToStringBSTR($bstr) }
    finally { [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($bstr) }
}

function Read-AdminCredentials {
    # Prompt for username (default 'admin') and a confirmed non-empty password.
    $user = Read-Host 'Admin username [admin]'
    if ([string]::IsNullOrWhiteSpace($user)) { $user = 'admin' }
    if ($user -match '\s') {
        Write-Warn2 'Username cannot contain whitespace; using "admin".'
        $user = 'admin'
    }
    for ($attempt = 1; $attempt -le 3; $attempt++) {
        $p1 = ConvertFrom-SecureToPlain (Read-Host 'Admin password' -AsSecureString)
        $p2 = ConvertFrom-SecureToPlain (Read-Host 'Confirm password' -AsSecureString)
        if ([string]::IsNullOrEmpty($p1)) { Write-Warn2 'Password cannot be empty.'; continue }
        if ($p1 -ne $p2)                  { Write-Warn2 'Passwords did not match; try again.'; continue }
        return [pscustomobject]@{ Username = $user; Password = $p1 }
    }
    Write-Warn2 'Too many attempts; skipping admin login setup.'
    return $null
}

function Set-NoobConfigAuth([string]$Path, [string]$Username, [string]$Password) {
    # Merge bootstrap admin credentials into the simple key/value config file without
    # clobbering other settings. Inserts under an existing 'auth:' section if present
    # (idempotent re-runs don't accumulate headers). Written without a BOM (the Go config
    # parser is BOM-sensitive).
    $lines = @()
    if (Test-Path -LiteralPath $Path) {
        $lines = @(Get-Content -LiteralPath $Path)
        if ($lines.Count -gt 0) { $lines[0] = $lines[0].TrimStart([char]0xFEFF) }
    }
    # Drop any bootstrap admin lines we may have written on a previous run.
    $lines = @($lines | Where-Object { $_ -notmatch '^\s*bootstrap_admin_(username|password)\s*:' })
    $userLine = "  bootstrap_admin_username: $Username"
    $passLine = "  bootstrap_admin_password: $Password"

    $authIdx = -1
    for ($i = 0; $i -lt $lines.Count; $i++) {
        if ($lines[$i].Trim() -eq 'auth:') { $authIdx = $i; break }
    }

    $out = @()
    if ($authIdx -ge 0) {
        $out += $lines[0..$authIdx]                 # everything up to and including 'auth:'
        $out += $userLine
        $out += $passLine
        if ($authIdx -lt $lines.Count - 1) {
            $out += $lines[($authIdx + 1)..($lines.Count - 1)]  # the rest
        }
    } else {
        if ($lines.Count -gt 0) {
            $out += $lines
            if ($out[-1].Trim() -ne '') { $out += '' }
        }
        $out += @('auth:', $userLine, $passLine)
    }
    [System.IO.File]::WriteAllLines($Path, [string[]]$out, (New-Object System.Text.UTF8Encoding($false)))
}

function Protect-ConfigFile([string]$Path) {
    # Restrict the config file (which holds the bootstrap password until first run) to
    # Administrators and SYSTEM, which is the account the auto-start service runs under.
    try {
        & icacls $Path /inheritance:r /grant:r 'SYSTEM:F' 'Administrators:F' | Out-Null
    } catch {
        Write-Warn2 "Could not restrict permissions on $Path ($($_.Exception.Message))."
    }
}

function Get-GoExe {
    # Prefer one on PATH, then the standard install location.
    $cmd = Get-Command go -ErrorAction SilentlyContinue
    if ($cmd) { return $cmd.Source }
    $standard = Join-Path $env:ProgramFiles 'Go\bin\go.exe'
    if (Test-Path $standard) { return $standard }
    return $null
}

function Get-GoVersion($goExe) {
    # "go version go1.26.3 windows/amd64" -> [version]1.26.3
    $out = & $goExe version
    if ($out -match 'go(\d+\.\d+(?:\.\d+)?)') {
        $v = $Matches[1]
        if (($v -split '\.').Count -eq 2) { $v = "$v.0" }
        return [version]$v
    }
    return $null
}

# ---------------------------------------------------------------------------
# 1. Dependency: Go toolchain
# ---------------------------------------------------------------------------
Write-Step "Checking for the Go toolchain (>= $GoMinVersion)"
$goExe = Get-GoExe
$needInstall = $true
if ($goExe) {
    $ver = Get-GoVersion $goExe
    if ($ver -and $ver -ge [version]$GoMinVersion) {
        Write-Ok "Found Go $ver at $goExe"
        $needInstall = $false
    } else {
        Write-Warn2 "Found Go $ver at $goExe, but >= $GoMinVersion is required."
    }
} else {
    Write-Warn2 'Go was not found.'
}

if ($needInstall) {
    Write-Step "Installing Go via winget"
    if (-not (Get-Command winget -ErrorAction SilentlyContinue)) {
        throw "winget is not available. Install Go $GoMinVersion or newer manually from https://go.dev/dl/ and re-run this script."
    }
    winget install --id GoLang.Go --exact --silent --accept-package-agreements --accept-source-agreements
    if ($LASTEXITCODE -ne 0) {
        throw "winget failed to install Go (exit $LASTEXITCODE). Install manually from https://go.dev/dl/ and re-run."
    }
    # Make Go available in this session without requiring a new shell.
    $goBin = Join-Path $env:ProgramFiles 'Go\bin'
    if (Test-Path $goBin) { $env:Path = "$goBin;$env:Path" }
    $goExe = Get-GoExe
    if (-not $goExe) { throw 'Go was installed but go.exe could not be located. Open a new terminal and re-run this script.' }
    $ver = Get-GoVersion $goExe
    if (-not $ver -or $ver -lt [version]$GoMinVersion) { throw "Installed Go version $ver is still below $GoMinVersion." }
    Write-Ok "Installed Go $ver at $goExe"
}

# ---------------------------------------------------------------------------
# 2. Build
# ---------------------------------------------------------------------------
Write-Step 'Downloading module dependencies'
& $goExe mod download
Write-Ok 'Dependencies are present.'

if ($RunTests) {
    Write-Step 'Running tests (go test ./...)'
    & $goExe test ./...
    if ($LASTEXITCODE -ne 0) { throw "Tests failed (exit $LASTEXITCODE)." }
    Write-Ok 'All tests passed.'
}

Write-Step 'Building noobboard.exe'
$distExe = Join-Path $RepoRoot 'dist\noobboard.exe'
& $goExe build -o $distExe .\cmd\dashboard
if ($LASTEXITCODE -ne 0) { throw "Build failed (exit $LASTEXITCODE)." }
Write-Ok "Built $distExe"

if ($NoService) {
    Write-Step 'Build complete (-NoService)'
    Write-Host '    Run the app in the foreground with:' -ForegroundColor Gray
    Write-Host "        $distExe serve" -ForegroundColor Gray
    Write-Host '    Admin panel: http://127.0.0.1:8787/   Compact app: http://127.0.0.1:8788/' -ForegroundColor Gray
    return
}

# ---------------------------------------------------------------------------
# 3. Install (Windows service)
# ---------------------------------------------------------------------------
if (-not (Test-Admin)) {
    Write-Warn2 'Service installation requires Administrator privileges.'
    Write-Host  '    The binary is built. Re-run this script from an elevated PowerShell prompt' -ForegroundColor Gray
    Write-Host  '    to install the service, or pass -NoService to build only.' -ForegroundColor Gray
    throw 'Not running as Administrator; cannot install the NoobBoard service.'
}

Write-Step "Installing the NoobBoard service binary to $InstallDir"
New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
$installedExe = Join-Path $InstallDir 'noobboard.exe'

# Stop and remove any existing service first so the binary is not locked. Use sc.exe by
# service name (not the installed binary) so this works even if the old binary is missing
# or -InstallDir changed since the last install.
$existing = Get-Service -Name 'NoobBoard' -ErrorAction SilentlyContinue
if ($existing) {
    Write-Warn2 'Existing NoobBoard service found; stopping and removing it before reinstall.'
    & sc.exe stop NoobBoard | Out-Null
    Start-Sleep -Seconds 1
    & sc.exe delete NoobBoard | Out-Null
    Start-Sleep -Seconds 1
}

Copy-Item -Path $distExe -Destination $installedExe -Force
Write-Ok "Copied binary to $installedExe"

Write-Step 'Registering the Windows service'
& $installedExe install-service
if ($LASTEXITCODE -ne 0) { throw "install-service failed (exit $LASTEXITCODE)." }
Write-Ok 'Service "NoobBoard" registered (auto-start).'

if (Confirm-Firewall) {
    Write-Step 'Adding firewall rule for TCP 8787-8788 (Private + Public profiles)'
    if (-not (Get-NetFirewallRule -DisplayName 'NoobBoard' -ErrorAction SilentlyContinue)) {
        New-NetFirewallRule -DisplayName 'NoobBoard' -Direction Inbound -Action Allow `
            -Protocol TCP -LocalPort 8787,8788 -Profile Private,Public | Out-Null
        Write-Ok 'Firewall rule added (Private and Public profiles).'
        Write-Warn2 'This allows inbound access on Private AND Public networks. That is normally fine on a'
        Write-Warn2 'home LAN (Windows often mislabels it "Public"), but it does NOT isolate this host if it'
        Write-Warn2 'is genuinely internet-facing. Change the default admin password now, and for real WAN'
        Write-Warn2 'access prefer an HTTPS reverse proxy over exposing these ports directly.'
    } else {
        Write-Ok 'Firewall rule already exists.'
    }
} else {
    Write-Ok 'Skipped firewall rule; NoobBoard is reachable only from this machine.'
}

# ---------------------------------------------------------------------------
# 4. Optional: set up the admin login now
# ---------------------------------------------------------------------------
# This only seeds the *bootstrap* admin credentials in the service config. It does NOT mark
# setup as complete: the future in-app setup wizard still runs and simply continues from the
# accounts step (an admin already exists). See docs/agent-roadmap.md (Workstream A) and
# docs/deployment-windows.md for the installer/wizard contract.
$cfgDir  = Join-Path $env:ProgramData 'NoobBoard'
$cfgFile = Join-Path $cfgDir 'config.yaml'
$dbFile  = Join-Path $cfgDir 'data\dashboard.db.json'

if (Confirm-AuthSetup) {
    if (Test-Path -LiteralPath $dbFile) {
        Write-Warn2 'A NoobBoard database already exists; bootstrap credentials only apply on first run.'
        Write-Warn2 'To change an existing login, use the app settings (or the setup wizard) instead.'
    }
    $cred = Read-AdminCredentials
    if ($cred) {
        New-Item -ItemType Directory -Force -Path $cfgDir | Out-Null
        Set-NoobConfigAuth -Path $cfgFile -Username $cred.Username -Password $cred.Password
        Protect-ConfigFile -Path $cfgFile
        Write-Ok "Saved admin login for user '$($cred.Username)' to $cfgFile"
        Write-Host '    On first run the admin is created and the password is hashed into the database.' -ForegroundColor Gray
        Write-Host "    The password ALSO remains in plain text in $cfgFile" -ForegroundColor Gray
        Write-Host '    (restricted to Administrators/SYSTEM). Treat that file as a secret; you may delete the' -ForegroundColor Gray
        Write-Host '    bootstrap_admin_password line after the first successful sign-in.' -ForegroundColor Gray
    }
} else {
    Write-Ok 'Skipped admin login setup.'
    Write-Host '    Default login (admin / change-me-now) applies until changed, or the setup wizard will ask.' -ForegroundColor Gray
}

if ($Start) {
    Write-Step 'Starting the NoobBoard service'
    & $installedExe start-service
    if ($LASTEXITCODE -ne 0) { throw "start-service failed (exit $LASTEXITCODE)." }
    Write-Ok 'Service started.'
}

Write-Step 'Done'
Write-Host '    Admin panel:     http://127.0.0.1:8787/' -ForegroundColor Gray
Write-Host '    Compact web app: http://127.0.0.1:8788/' -ForegroundColor Gray
Write-Host ''
Write-Host '    Configure live credentials at C:\ProgramData\NoobBoard\config.yaml' -ForegroundColor Gray
Write-Host '    (see README "Live Configuration"). Manage the service with:' -ForegroundColor Gray
Write-Host "        $installedExe start-service | stop-service | uninstall-service" -ForegroundColor Gray
Write-Host '    Change the default admin password before any real use.' -ForegroundColor Yellow
