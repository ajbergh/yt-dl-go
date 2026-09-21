[CmdletBinding()]
param(
    [string]$ExecutablePath,
    [string]$ArchivePath,
    [string]$Architecture
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path

if ([string]::IsNullOrWhiteSpace($ExecutablePath)) {
    $ExecutablePath = Join-Path $repoRoot 'dist\youtube-downloader.exe'
} elseif (-not [IO.Path]::IsPathRooted($ExecutablePath)) {
    $ExecutablePath = Join-Path $repoRoot $ExecutablePath
}
$ExecutablePath = [IO.Path]::GetFullPath($ExecutablePath)

if (-not (Test-Path -LiteralPath $ExecutablePath -PathType Leaf)) {
    throw "Windows executable was not found: $ExecutablePath"
}
$exeInfo = Get-Item -LiteralPath $ExecutablePath
if ($exeInfo.Length -le 0) {
    throw "Windows executable is empty: $ExecutablePath"
}

if ([string]::IsNullOrWhiteSpace($Architecture)) {
    $Architecture = switch ($env:PROCESSOR_ARCHITECTURE) {
        'AMD64' { 'amd64' }
        'ARM64' { 'arm64' }
        'x86' { '386' }
        default {
            $goArch = (& go env GOARCH 2>$null)
            if ($LASTEXITCODE -eq 0 -and -not [string]::IsNullOrWhiteSpace($goArch)) {
                $goArch.Trim()
            } else {
                'unknown'
            }
        }
    }
}
if ($Architecture -notmatch '^[A-Za-z0-9._-]+$') {
    throw "Architecture contains unsupported characters: $Architecture"
}

$archiveBase = "youtube-downloader-windows-$Architecture"
if ([string]::IsNullOrWhiteSpace($ArchivePath)) {
    $ArchivePath = Join-Path (Split-Path -Parent $ExecutablePath) "$archiveBase.zip"
} elseif (-not [IO.Path]::IsPathRooted($ArchivePath)) {
    $ArchivePath = Join-Path $repoRoot $ArchivePath
}
$ArchivePath = [IO.Path]::GetFullPath($ArchivePath)
$archiveDirectory = Split-Path -Parent $ArchivePath
New-Item -ItemType Directory -Path $archiveDirectory -Force | Out-Null

$readme = Join-Path $repoRoot 'README.md'
$notices = Join-Path $repoRoot 'src\server\THIRD_PARTY_NOTICES.md'
foreach ($required in @($readme, $notices)) {
    if (-not (Test-Path -LiteralPath $required -PathType Leaf)) {
        throw "Required package file is missing: $required"
    }
}

$stageRoot = Join-Path ([IO.Path]::GetTempPath()) ("yt-dl-go-package-" + [Guid]::NewGuid().ToString('N'))
$stage = Join-Path $stageRoot $archiveBase
try {
    New-Item -ItemType Directory -Path $stage -Force | Out-Null
    Copy-Item -LiteralPath $ExecutablePath -Destination (Join-Path $stage 'youtube-downloader.exe')
    Copy-Item -LiteralPath $readme -Destination (Join-Path $stage 'README.md')
    Copy-Item -LiteralPath $notices -Destination (Join-Path $stage 'THIRD_PARTY_NOTICES.md')

    if (Test-Path -LiteralPath $ArchivePath) {
        Remove-Item -LiteralPath $ArchivePath -Force
    }
    Compress-Archive -LiteralPath $stage -DestinationPath $ArchivePath -CompressionLevel Optimal
} finally {
    if (Test-Path -LiteralPath $stageRoot) {
        Remove-Item -LiteralPath $stageRoot -Recurse -Force
    }
}

if (-not (Test-Path -LiteralPath $ArchivePath -PathType Leaf) -or (Get-Item -LiteralPath $ArchivePath).Length -le 0) {
    throw "Packaging did not produce a non-empty archive: $ArchivePath"
}

$checksumPath = "$ArchivePath.sha256"
$hash = (Get-FileHash -LiteralPath $ArchivePath -Algorithm SHA256).Hash.ToLowerInvariant()
"$hash  $(Split-Path -Leaf $ArchivePath)" | Set-Content -LiteralPath $checksumPath -Encoding ascii -NoNewline

Write-Host "Package succeeded: $ArchivePath" -ForegroundColor Green
Write-Host "Checksum: $checksumPath" -ForegroundColor Green
