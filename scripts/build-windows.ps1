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
    $viteCLI = Join-Path $repoRoot 'node_modules\vite\bin\vite.js'
    if (-not (Test-Path -LiteralPath $viteCLI -PathType Leaf)) {
        throw "Frontend dependencies are missing. Run 'npm ci' after closing any running app or Vite dev server, then rerun this build."
    }

    if ([string]::IsNullOrWhiteSpace($OutputPath)) {
        $OutputPath = Join-Path $repoRoot 'dist\youtube-downloader.exe'
    } elseif (-not [IO.Path]::IsPathRooted($OutputPath)) {
        $OutputPath = Join-Path $repoRoot $OutputPath
    }
    $OutputPath = [IO.Path]::GetFullPath($OutputPath)
    $outputDirectory = Split-Path -Parent $OutputPath

    Set-Location $repoRoot
    $serverDist = Join-Path $repoRoot 'src\server\dist'
    if (Test-Path -LiteralPath $serverDist) {
        $serverDistItem = Get-Item -LiteralPath $serverDist
        if ($serverDistItem.LinkType) {
            throw "Refusing to write through symlink: $serverDist"
        }
    } else {
        New-Item -ItemType Directory -Path $serverDist -Force | Out-Null
    }

    Write-Host 'Building frontend...' -ForegroundColor Cyan
    Invoke-Native 'npm' @('run', 'build', '--', '--outDir', $serverDist)
    if (-not (Test-Path -LiteralPath (Join-Path $serverDist 'index.html') -PathType Leaf)) {
        throw "Frontend build did not produce $(Join-Path $serverDist 'index.html')"
    }

    Push-Location (Join-Path $repoRoot 'src\server')
    try {
        $env:CGO_ENABLED = '0'
        if ([string]::IsNullOrWhiteSpace($env:GOCACHE)) {
            # Keep the default build cache in a user-writable temp location.
            $env:GOCACHE = Join-Path ([IO.Path]::GetTempPath()) 'yt-dl-go-go-build-cache'
        }
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
