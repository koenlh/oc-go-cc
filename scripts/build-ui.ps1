[CmdletBinding()]
param(
    [string]$Version,
    [string]$Output = "bin/oc-go-cc-ui.exe",
    [switch]$NoWindowGui
)

$ErrorActionPreference = "Stop"

$scriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$repoRoot = Split-Path -Parent $scriptDir

Set-Location $repoRoot

$goBin = Join-Path ${env:ProgramFiles} "Go\bin"
$goExe = Join-Path $goBin "go.exe"
if (Test-Path $goExe) {
    $env:Path = "$goBin;$env:Path"
}

if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
    throw "Go is not installed or not on PATH. Install Go 1.25+ first."
}

if (-not $Version) {
    $Version = git describe --tags --always --dirty 2>$null
    if (-not $Version) {
        $Version = "dev"
    }
}

New-Item -ItemType Directory -Force -Path (Split-Path -Parent $Output) | Out-Null

$ldflags = "-X main.version=$Version"
if (-not $NoWindowGui) {
    $ldflags = "-H windowsgui $ldflags"
}

go build -ldflags $ldflags -o $Output ./cmd/oc-go-cc-ui
if ($LASTEXITCODE -ne 0) {
    throw "UI build failed"
}

Write-Host "Built control panel: $Output"