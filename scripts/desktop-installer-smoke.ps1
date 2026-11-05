# Run only on disposable GitHub runners: this exercises real per-user installation.
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
if ($env:GITHUB_ACTIONS -ne 'true') { throw 'Installer integration tests require a disposable GitHub runner.' }
$repo = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
$arch = if ($env:RUNNER_ARCH -eq 'ARM64') { 'arm64' } else { 'amd64' }
$output = Join-Path $repo "dist/desktop/windows-$arch"
$setup = Join-Path $output "youtube-vault-windows-$arch-setup.exe"
$install = Join-Path $env:LOCALAPPDATA 'Programs/YouTube Vault'
$registry = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\YouTube Vault'
$menu = Join-Path ([Environment]::GetFolderPath('Programs')) 'YouTube Vault.lnk'
$desktop = Join-Path ([Environment]::GetFolderPath('Desktop')) 'YouTube Vault.lnk'
if ((Test-Path -LiteralPath $install) -or (Test-Path -LiteralPath $registry)) { throw 'Refusing to overwrite an existing installation.' }
function Invoke-Installer([string]$File, [string[]]$Arguments) {
    $process = Start-Process -FilePath $File -ArgumentList $Arguments -Wait -PassThru -WindowStyle Hidden
    if ($process.ExitCode -ne 0) { throw "Installer exited with $($process.ExitCode)" }
}
Invoke-Installer $setup @('/S')
$executable = Join-Path $install 'app/youtube-vault.exe'
$portable = Join-Path $output "youtube-vault-windows-$arch.exe"
if ((Get-FileHash -LiteralPath $executable).Hash -ne (Get-FileHash -LiteralPath $portable).Hash) { throw 'Installed application differs from portable application.' }
foreach ($file in @($menu, $desktop, (Join-Path $install 'app/THIRD_PARTY_NOTICES.md'), (Join-Path $install 'app/DESKTOP_GO_LICENSES.csv'))) {
    if (-not (Test-Path -LiteralPath $file)) { throw "Missing installed file: $file" }
}
if (-not (Get-ItemProperty -LiteralPath $registry).QuietUninstallString) { throw 'Uninstall registration missing.' }
# An unrelated file at the chosen install root must survive upgrade and removal.
$sentinel = Join-Path $install 'keep-user-file.txt'
Set-Content -LiteralPath $sentinel -Value 'preserve me'
Invoke-Installer $setup @('/S')
if ((Get-Content -LiteralPath $sentinel) -ne 'preserve me') { throw 'Reinstallation removed user files.' }
& node (Join-Path $PSScriptRoot 'desktop-smoke.mjs') --executable $executable
if ($LASTEXITCODE -ne 0) { throw 'Installed application smoke test failed.' }
# _?= keeps NSIS in place so -Wait observes actual uninstall completion.
Invoke-Installer (Join-Path $install 'uninstall.exe') @('/S', "_?=$install")
foreach ($file in @($executable, $menu, $desktop, $registry)) {
    if (Test-Path -LiteralPath $file) { throw "Uninstall left installed integration: $file" }
}
if ((Get-Content -LiteralPath $sentinel) -ne 'preserve me') { throw 'Uninstall removed user files.' }
Remove-Item -LiteralPath $sentinel
# NSIS cannot delete its own executable in _?= mode; remove that known file.
Remove-Item -LiteralPath (Join-Path $install 'uninstall.exe') -ErrorAction SilentlyContinue
Remove-Item -LiteralPath $install
Write-Host 'Windows installer, reinstallation, launch, and removal verified.'
