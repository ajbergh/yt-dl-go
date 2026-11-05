<#
.SYNOPSIS
Build standalone Wails desktop executables for Windows.
.EXAMPLE
./scripts/build-desktop-windows.ps1
.EXAMPLE
./scripts/build-desktop-windows.ps1 -Architecture amd64
#>
[CmdletBinding()]
param(
    [ValidateSet('amd64', 'arm64', 'all')]
    [string]$Architecture = 'all'
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

if ($env:OS -ne 'Windows_NT') {
    throw 'build-desktop-windows.ps1 must be run on Windows.'
}

$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
$originalLocation = (Get-Location).Path

foreach ($command in @('go', 'node', 'git', 'powershell.exe')) {
    if ($null -eq (Get-Command $command -ErrorAction SilentlyContinue)) {
        throw "Required command '$command' was not found on PATH."
    }
}
if (-not (Test-Path -LiteralPath (Join-Path $repoRoot 'node_modules/vite/bin/vite.js') -PathType Leaf)) {
    throw "Frontend dependencies are missing. Run 'npm ci' in the repository root, then rerun this build."
}

$architectures = if ($Architecture -eq 'all') { @('amd64', 'arm64') } else { @($Architecture) }
try {
    Set-Location $repoRoot
    foreach ($targetArch in $architectures) {
        Write-Host "Building Wails desktop application for windows/$targetArch..." -ForegroundColor Cyan
        & node (Join-Path $PSScriptRoot 'build-desktop.mjs') "windows/$targetArch"
        if ($LASTEXITCODE -ne 0) {
            throw "Wails desktop build for windows/$targetArch failed with exit code $LASTEXITCODE."
        }
    }
    $outputRoot = if ($env:DESKTOP_OUTPUT_ROOT) { $env:DESKTOP_OUTPUT_ROOT } else { 'dist/desktop' }
    Write-Host "Desktop executables and SHA-256 checksums: $outputRoot" -ForegroundColor Green
} finally {
    Set-Location $originalLocation
}
