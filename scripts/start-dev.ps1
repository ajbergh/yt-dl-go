<#
.SYNOPSIS
    Starts development instances of both the frontend (Vite) and backend (Go API)
    and displays local, network, and system IP addresses and endpoints.

.DESCRIPTION
    This script discovers local and network IP addresses, verifies port availability,
    initializes frontend and backend dev services, displays an IP summary banner,
    streams color-coded logs, and ensures clean termination of all child processes
    when stopped.

.PARAMETER OpenBrowser
    Automatically opens the default web browser to the frontend dev URL when ready.

.PARAMETER Expose
    Binds the Vite frontend dev server to all network interfaces (--host 0.0.0.0).

.PARAMETER SeparateWindows
    Launches frontend and backend in separate terminal windows instead of streaming in one.

.PARAMETER FrontendPort
    Port for the Vite frontend dev server (default: 5173).

.PARAMETER BackendPort
    Port for the Go backend API server (default: 8080).

.PARAMETER KillExistingPorts
    Automatically terminates any existing processes listening on the dev ports before starting.

.EXAMPLE
    .\scripts\start-dev.ps1

.EXAMPLE
    .\scripts\start-dev.ps1 -OpenBrowser -Expose
#>

[CmdletBinding()]
param(
    [switch]$OpenBrowser,
    [switch]$Expose,
    [switch]$SeparateWindows,
    [switch]$KillExistingPorts,
    [int]$FrontendPort = 5173,
    [int]$BackendPort = 8080
)

$ErrorActionPreference = 'Stop'

# Locate repository paths
$scriptDir = if ($PSScriptRoot) {
    $PSScriptRoot
} elseif ($MyInvocation.MyCommand.Path) {
    Split-Path -Parent $MyInvocation.MyCommand.Path
} else {
    Join-Path (Get-Location).Path 'scripts'
}
$repoRoot = (Resolve-Path (Join-Path $scriptDir '..')).Path
$serverDir = Join-Path $repoRoot 'src\server'
$frontendDir = $repoRoot
$dataDir = Join-Path $repoRoot 'downloads'

# Helper: Test for required commands
function Test-RequiredCommand {
    param([Parameter(Mandatory)][string]$Name)
    if ($null -eq (Get-Command $Name -ErrorAction SilentlyContinue)) {
        throw "Required command '$Name' was not found on PATH."
    }
}

# Helper: Retrieve network IP addresses across platforms / interfaces
function Get-HostIPAddresses {
    $results = @()
    try {
        $interfaces = [System.Net.NetworkInformation.NetworkInterface]::GetAllNetworkInterfaces() |
            Where-Object { $_.OperationalStatus -eq 'Up' -and $_.NetworkInterfaceType -ne 'Loopback' }

        foreach ($iface in $interfaces) {
            $props = $iface.GetIPProperties()
            foreach ($addr in $props.UnicastAddresses) {
                if ($addr.Address.AddressFamily -eq 'InterNetwork') {
                    $ipStr = $addr.Address.ToString()
                    if (-not $ipStr.StartsWith('169.254.') -and -not $ipStr.StartsWith('127.')) {
                        $results += [PSCustomObject]@{
                            Interface = $iface.Name
                            IPAddress = $ipStr
                        }
                    }
                }
            }
        }
    } catch {
        if (Get-Command Get-NetIPAddress -ErrorAction SilentlyContinue) {
            $results = Get-NetIPAddress -AddressFamily IPv4 -ErrorAction SilentlyContinue |
                Where-Object { $_.IPAddress -notmatch '^127\.' -and $_.IPAddress -notmatch '^169\.254\.' } |
                ForEach-Object {
                    [PSCustomObject]@{
                        Interface = $_.InterfaceAlias
                        IPAddress = $_.IPAddress
                    }
                }
        }
    }
    return $results
}

# Helper: Check port listeners
function Get-PortListener {
    param([int]$Port)
    if (Get-Command Get-NetTCPConnection -ErrorAction SilentlyContinue) {
        return Get-NetTCPConnection -LocalPort $Port -State Listen -ErrorAction SilentlyContinue
    }
    return $null
}

# Helper: Kill process listening on port
function Stop-PortListener {
    param([int]$Port)
    $conns = Get-PortListener -Port $Port
    if ($conns) {
        foreach ($c in $conns) {
            if ($c.OwningProcess -and $c.OwningProcess -ne $PID) {
                try {
                    Write-Host "Terminating process tree for PID $($c.OwningProcess) on port $Port..." -ForegroundColor Yellow
                    & taskkill /PID $c.OwningProcess /T /F | Out-Null
                } catch {
                    # Process might have already terminated
                }
            }
        }
    }
}

# Verify environment prerequisites
foreach ($cmd in @('node', 'npm', 'go')) {
    Test-RequiredCommand $cmd
}

# Verify or install frontend dependencies
if (-not (Test-Path (Join-Path $repoRoot 'node_modules'))) {
    Write-Host "node_modules not found. Installing frontend dependencies..." -ForegroundColor Cyan
    Push-Location $repoRoot
    try {
        & npm install
    } finally {
        Pop-Location
    }
}

# Check for existing port conflicts
foreach ($p in @($FrontendPort, $BackendPort)) {
    $existing = Get-PortListener -Port $p
    if ($existing) {
        $pids = ($existing | ForEach-Object { $_.OwningProcess }) -join ', '
        if ($KillExistingPorts) {
            Stop-PortListener -Port $p
        } else {
            Write-Warning "Port $p is currently in use by process PID(s): $pids."
            Write-Warning "Pass -KillExistingPorts to terminate them automatically or free the port manually."
        }
    }
}

# Retrieve IP information
$detectedIPs = Get-HostIPAddresses

# Build allowed origins for CORS
$allowedOrigins = @(
    "http://localhost:$FrontendPort",
    "http://127.0.0.1:$FrontendPort",
    "http://localhost:$BackendPort",
    "http://127.0.0.1:$BackendPort"
)
foreach ($item in $detectedIPs) {
    $allowedOrigins += "http://$($item.IPAddress):$FrontendPort"
    $allowedOrigins += "http://$($item.IPAddress):$BackendPort"
}
$allowedOriginsStr = ($allowedOrigins | Select-Object -Unique) -join ','

# Display dev instance IPs banner
Write-Host ""
Write-Host "==========================================================================" -ForegroundColor Cyan
Write-Host "                   YouTube Downloader - Dev Environment                   " -ForegroundColor White
Write-Host "==========================================================================" -ForegroundColor Cyan
Write-Host ""
Write-Host "  [Frontend - Vite Development Server]" -ForegroundColor Green
Write-Host "    -> Local URL:   http://localhost:$FrontendPort/  (or http://127.0.0.1:$FrontendPort/)" -ForegroundColor White
if ($detectedIPs.Count -gt 0) {
    foreach ($item in $detectedIPs) {
        Write-Host "    -> Network URL: http://$($item.IPAddress):$FrontendPort/ ($($item.Interface))" -ForegroundColor White
    }
}
Write-Host ""
Write-Host "  [Backend - Go API Service]" -ForegroundColor Magenta
Write-Host "    -> Local API:   http://localhost:$BackendPort/  (or http://127.0.0.1:$BackendPort/)" -ForegroundColor White
Write-Host "    -> API Health:  http://127.0.0.1:$BackendPort/api/health" -ForegroundColor White
if ($detectedIPs.Count -gt 0) {
    foreach ($item in $detectedIPs) {
        Write-Host "    -> Network API: http://$($item.IPAddress):$BackendPort/ ($($item.Interface))" -ForegroundColor White
    }
}
Write-Host ""
Write-Host "  [Host IP Addresses]" -ForegroundColor Yellow
Write-Host "    -> Loopback:    127.0.0.1 (localhost)" -ForegroundColor White
if ($detectedIPs.Count -gt 0) {
    foreach ($item in $detectedIPs) {
        Write-Host "    -> $($item.Interface.PadRight(12)): $($item.IPAddress)" -ForegroundColor White
    }
} else {
    Write-Host "    -> (No active external network adapters detected)" -ForegroundColor Gray
}
Write-Host ""
Write-Host "==========================================================================" -ForegroundColor Cyan
Write-Host "  Press [Ctrl+C] to stop all development services." -ForegroundColor DarkGray
Write-Host "==========================================================================" -ForegroundColor Cyan
Write-Host ""

# Mode 1: Separate Windows
if ($SeparateWindows) {
    Write-Host "Launching Frontend and Backend in separate windows..." -ForegroundColor Cyan

    $feHostArg = if ($Expose) { "--host" } else { "" }
    $feCmd = "title YouTube Downloader - Frontend (Vite) && cd /d `"$repoRoot`" && node .\node_modules\vite\bin\vite.js --port $FrontendPort $feHostArg"
    $frontendProc = Start-Process -FilePath "cmd.exe" -ArgumentList "/k $feCmd" -PassThru

    $beCmd = "title YouTube Downloader - Backend (Go API) && cd /d `"$serverDir`" && set CGO_ENABLED=0 && set ADDR=127.0.0.1:$BackendPort && set ALLOWED_ORIGINS=$allowedOriginsStr && set DATA_DIR=$dataDir && go run -tags=dev ."
    $backendProc = Start-Process -FilePath "cmd.exe" -ArgumentList "/k $beCmd" -PassThru

    if ($OpenBrowser) {
        Start-Sleep -Seconds 2
        Start-Process "http://localhost:$FrontendPort/"
    }

    Write-Host "Frontend PID: $($frontendProc.Id), Backend PID: $($backendProc.Id)" -ForegroundColor DarkGray
    Write-Host "Keep this window open to monitor or press [Ctrl+C] to terminate both." -ForegroundColor Yellow

    try {
        while (-not $frontendProc.HasExited -or -not $backendProc.HasExited) {
            Start-Sleep -Milliseconds 500
        }
    } finally {
        Write-Host "`nStopping dev instances..." -ForegroundColor Yellow
        if (-not $frontendProc.HasExited) { & taskkill /PID $frontendProc.Id /T /F 2>$null | Out-Null }
        if (-not $backendProc.HasExited) { & taskkill /PID $backendProc.Id /T /F 2>$null | Out-Null }
        Stop-PortListener -Port $FrontendPort
        Stop-PortListener -Port $BackendPort
        Write-Host "Dev instances stopped." -ForegroundColor Green
    }
    return
}

# Mode 2: Unified Terminal Output (Default)
Write-Host "Starting services in background..." -ForegroundColor Cyan

$backendJob = Start-Job -ScriptBlock {
    param($dir, $origins, $addr, $data)
    Set-Location $dir
    $env:CGO_ENABLED = "0"
    $env:ADDR = $addr
    $env:ALLOWED_ORIGINS = $origins
    $env:DATA_DIR = $data
    $env:NO_BROWSER = "1"
    & go run -tags=dev . 2>&1
} -ArgumentList $serverDir, $allowedOriginsStr, "127.0.0.1:$BackendPort", $dataDir

$frontendJob = Start-Job -ScriptBlock {
    param($dir, $exposeFlag, $port)
    Set-Location $dir
    $argsList = @(".\node_modules\vite\bin\vite.js", "--port", "$port")
    if ($exposeFlag) {
        $argsList += "--host"
    }
    & node @argsList 2>&1
} -ArgumentList $frontendDir, [bool]$Expose, $FrontendPort

$browserOpened = $false

try {
    while ($true) {
        # Check if both jobs have stopped
        if ($backendJob.State -ne 'Running' -and $frontendJob.State -ne 'Running') {
            Write-Host "`nBoth services have exited." -ForegroundColor Yellow
            break
        }

        # Check frontend job failure early
        if ($frontendJob.State -eq 'Failed') {
            Write-Host "`nFrontend job failed to start." -ForegroundColor Red
        }

        # Check backend job failure early
        if ($backendJob.State -eq 'Failed') {
            Write-Host "`nBackend job failed to start." -ForegroundColor Red
        }

        # Stream backend lines
        $bOut = Receive-Job -Job $backendJob -ErrorAction SilentlyContinue
        if ($bOut) {
            $bOut | ForEach-Object {
                $line = "$_".TrimEnd()
                if ($line) {
                    Write-Host "[backend]  $line" -ForegroundColor Magenta
                }
            }
        }

        # Stream frontend lines
        $fOut = Receive-Job -Job $frontendJob -ErrorAction SilentlyContinue
        if ($fOut) {
            $fOut | ForEach-Object {
                $line = "$_".TrimEnd()
                if ($line) {
                    Write-Host "[frontend] $line" -ForegroundColor Cyan
                }
            }
        }

        # Optionally open browser once services are up
        if ($OpenBrowser -and -not $browserOpened) {
            $healthCheck = $null
            try {
                $healthCheck = Invoke-RestMethod -Uri "http://127.0.0.1:$BackendPort/api/health" -TimeoutSec 1 -ErrorAction SilentlyContinue
            } catch {}

            if ($healthCheck -and $healthCheck.ready) {
                Write-Host "Services ready. Opening browser to http://localhost:$FrontendPort/..." -ForegroundColor Green
                Start-Process "http://localhost:$FrontendPort/"
                $browserOpened = $true
            }
        }

        Start-Sleep -Milliseconds 250
    }
} finally {
    Write-Host "`nStopping dev instances..." -ForegroundColor Yellow

    # Stop background jobs
    Stop-Job -Job $backendJob, $frontendJob -ErrorAction SilentlyContinue | Out-Null
    Remove-Job -Job $backendJob, $frontendJob -Force -ErrorAction SilentlyContinue | Out-Null

    # Ensure no orphaned processes hold the ports
    Stop-PortListener -Port $FrontendPort
    Stop-PortListener -Port $BackendPort

    Write-Host "Dev instances stopped." -ForegroundColor Green
}
