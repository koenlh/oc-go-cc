[CmdletBinding()]
param(
    [string]$Version,
    [string]$GoProxy = "direct",
    [string]$OutputDir = "dist",
    [switch]$SkipChecksums,
    [switch]$SkipClean
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

$binary = "oc-go-cc"
$package = "./cmd/oc-go-cc"
$platforms = @(
    "darwin-amd64",
    "darwin-arm64",
    "linux-amd64",
    "linux-arm64",
    "windows-amd64",
    "windows-arm64"
)

$env:GOPROXY = $GoProxy
$env:CGO_ENABLED = "0"
$ldflags = "-X main.version=$Version -s -w"

if (-not $SkipClean) {
    Remove-Item -Recurse -Force $OutputDir -ErrorAction SilentlyContinue
}
New-Item -ItemType Directory -Force -Path $OutputDir | Out-Null

Write-Host "Building release binaries (version: $Version)..."

foreach ($platform in $platforms) {
    $parts = $platform.Split("-")
    $goos = $parts[0]
    $goarch = $parts[1]
    $ext = if ($goos -eq "windows") { ".exe" } else { "" }
    $outputPath = Join-Path $OutputDir ("{0}_{1}{2}" -f $binary, $platform, $ext)

    Write-Host ("  -> {0}/{1}" -f $goos, $goarch)

    $env:GOOS = $goos
    $env:GOARCH = $goarch

    go build -ldflags $ldflags -o $outputPath $package
    if ($LASTEXITCODE -ne 0) {
        throw "Build failed for $platform"
    }
}

Remove-Item Env:GOOS, Env:GOARCH -ErrorAction SilentlyContinue

if (-not $SkipChecksums) {
    $checksumPath = Join-Path $OutputDir "checksums.txt"
    Get-ChildItem $OutputDir -File -Filter "$binary_*" |
        Sort-Object Name |
        Get-FileHash -Algorithm SHA256 |
        ForEach-Object {
            "{0}  {1}" -f $_.Hash.ToLower(), (Split-Path $_.Path -Leaf)
        } |
        Set-Content $checksumPath
}

Write-Host ""
Write-Host "Built binaries:"
Get-ChildItem $OutputDir | Sort-Object Name | Format-Table Name, Length, LastWriteTime -AutoSize