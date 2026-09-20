[CmdletBinding()]
param(
    [string]$OutputPath
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

if ($env:OS -ne 'Windows_NT') {
    throw 'build-windows.ps1 must be run on Windows.'
}

$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
$originalLocation = (Get-Location).Path

function Test-RequiredCommand {
    param([Parameter(Mandatory)][string]$Name)

    if ($null -eq (Get-Command $Name -ErrorAction SilentlyContinue)) {
        throw "Required command '$Name' was not found on PATH."
    }
}

function Invoke-Native {
    param(
        [Parameter(Mandatory)][string]$FilePath,
        [Parameter()][string[]]$ArgumentList = @()
    )

    & $FilePath @ArgumentList
    if ($LASTEXITCODE -ne 0) {
        $arguments = $ArgumentList -join ' '
        throw "Command failed with exit code $LASTEXITCODE`: $FilePath $arguments"
    }
}

try {
    foreach ($command in @('go', 'node', 'npm')) {
        Test-RequiredCommand $command
    }

    foreach ($path in @(
        (Join-Path $repoRoot 'package.json'),
        (Join-Path $repoRoot 'src\server\go.mod')
    )) {
        if (-not (Test-Path -LiteralPath $path -PathType Leaf)) {
            throw "Required project file is missing: $path"
        }
    }

    if ([string]::IsNullOrWhiteSpace($OutputPath)) {
        $OutputPath = Join-Path $repoRoot 'dist\youtube-downloader.exe'
    } elseif (-not [IO.Path]::IsPathRooted($OutputPath)) {
        $OutputPath = Join-Path $repoRoot $OutputPath
    }
    $OutputPath = [IO.Path]::GetFullPath($OutputPath)
    $outputDirectory = Split-Path -Parent $OutputPath

    Set-Location $repoRoot
    Write-Host 'Installing frontend dependencies...' -ForegroundColor Cyan
    Invoke-Native 'npm' @('ci')

    Write-Host 'Building frontend...' -ForegroundColor Cyan
    Invoke-Native 'npm' @('run', 'build')

    $webDist = Join-Path $repoRoot 'dist'
    $webEntry = Join-Path $webDist 'index.html'
    if (-not (Test-Path -LiteralPath $webEntry -PathType Leaf)) {
        throw "Frontend build did not produce $webEntry"
    }

    $serverDist = Join-Path $repoRoot 'src\server\dist'
    if (Test-Path -LiteralPath $serverDist) {
        $serverDistItem = Get-Item -LiteralPath $serverDist
        if ($serverDistItem.LinkType) {
            throw "Refusing to write through symlink: $serverDist"
        }
        Get-ChildItem -LiteralPath $serverDist -Force | Remove-Item -Recurse -Force
    } else {
        New-Item -ItemType Directory -Path $serverDist -Force | Out-Null
    }
    Copy-Item -Path (Join-Path $webDist '*') -Destination $serverDist -Recurse -Force
    if (-not (Test-Path -LiteralPath (Join-Path $serverDist 'index.html') -PathType Leaf)) {
        throw "Failed to copy frontend assets into $serverDist"
    }

    Push-Location (Join-Path $repoRoot 'src\server')
    try {
        $env:CGO_ENABLED = '0'
        Write-Host 'Downloading Go dependencies...' -ForegroundColor Cyan
        Invoke-Native 'go' @('mod', 'download')

        Write-Host 'Running Go tests...' -ForegroundColor Cyan
        Invoke-Native 'go' @('test', './...')

        New-Item -ItemType Directory -Path $outputDirectory -Force | Out-Null
        Write-Host "Building $OutputPath..." -ForegroundColor Cyan
        Invoke-Native 'go' @(
            'build',
            '-buildvcs=false',
            '-trimpath',
            '-ldflags=-s -w',
            '-o',
            $OutputPath,
            '.'
        )
    } finally {
        Pop-Location
    }

    if (-not (Test-Path -LiteralPath $OutputPath -PathType Leaf)) {
        throw "Go build completed without producing $OutputPath"
    }
    $outputInfo = Get-Item -LiteralPath $OutputPath
    if ($outputInfo.Length -le 0) {
        throw "Go build produced an empty executable: $OutputPath"
    }

    Write-Host "Build succeeded: $OutputPath ($($outputInfo.Length) bytes)" -ForegroundColor Green
} finally {
    Set-Location $originalLocation
}
