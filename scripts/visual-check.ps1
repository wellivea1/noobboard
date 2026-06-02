param(
  [int]$Port = 8791,
  [int]$DebugPort = 9232,
  [string]$Scenario = "single_container_exited",
  [string]$GoExe = "C:\Program Files\Go\bin\go.exe",
  [string]$EdgePath = "",
  [switch]$KeepServer
)

$ErrorActionPreference = "Stop"
$Root = Split-Path -Parent $PSScriptRoot
Set-Location $Root

$env:GOCACHE = Join-Path $Root ".cache\go-build"
$env:GOMODCACHE = Join-Path $Root ".cache\go-mod"
$env:GOPATH = Join-Path $Root ".cache\gopath"

$argsList = @(
  "run",
  ".\cmd\visualcheck",
  "-port", $Port,
  "-debug-port", $DebugPort,
  "-scenario", $Scenario,
  "-go", $GoExe
)

if ($EdgePath) {
  $argsList += @("-edge", $EdgePath)
}
if ($KeepServer) {
  $argsList += "-keep-server"
}

& $GoExe @argsList
exit $LASTEXITCODE
