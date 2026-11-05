# Wails v2 desktop builds

The desktop edition uses Wails **v2.17.1** to display the existing React interface
in a native window. Closing the window stops the private Go service and workers.
The existing browser/server edition remains available through the original build
scripts; both editions use the same per-user data. Close one before starting the
other. Back up existing data before trying a development build.

## Build on the target operating system

Install the repository's Node/npm and Go versions, then `npm ci` in the repository
root. The build command installs pinned Wails and license-scanner tools in the
user's temporary directory; it does not require a global Wails installation.

```sh
npm run build:desktop
```

An explicit target can be supplied with `npm run build:desktop -- windows/arm64`,
`darwin/arm64`, or `linux/amd64`. Windows supports both architectures from a Windows
host. Linux and macOS builds require matching native OS/architecture runners.

Every desktop target produces both a portable package and an installer, for
AMD64 and ARM64:

| Platform | Portable | Installer |
| --- | --- | --- |
| Windows | Standalone `.exe` and notices `.zip` | `-setup.exe` (NSIS) |
| macOS | `.app` bundle in `.tar.gz` | `.pkg` |
| Linux | `.tar.gz` | `.deb` (Debian/Ubuntu with compatible runtime libraries) |

Windows packaging requires NSIS 3.11 (`choco install nsis --version=3.11`).
Set `NSIS_PATH` to a custom `makensis.exe` location if needed. The setup installs
per user, creates Start menu and desktop shortcuts, registers an uninstaller,
and installs WebView2 when missing (internet access required for that runtime).
Uninstall through Windows Settings; saved data and downloads are retained.
The macOS PKG installs into `/Applications`; remove the app through Finder.
The portable macOS archive can be extracted and launched without the installer.

On Windows, build standalone desktop executables for both architectures with
the PowerShell helper:

```powershell
./scripts/build-desktop-windows.ps1
# Or build just one architecture:
./scripts/build-desktop-windows.ps1 -Architecture amd64
./scripts/build-desktop-windows.ps1 -Architecture arm64
```

The helper can be invoked from any working directory. Run `npm ci` in the
repository root first. It uses the same pinned tools and packaging as
`npm run build:desktop` and stops if a build fails. The applications are:

- `dist/desktop/windows-amd64/youtube-vault-windows-amd64.exe`
- `dist/desktop/windows-arm64/youtube-vault-windows-arm64.exe`

Each EXE embeds the interface and Go backend and runs directly in a native
desktop window without an adjacent assets folder or application DLLs. Each has
a SHA-256 sidecar. A companion ZIP retains the notices, license texts and LGPL
source/relinking materials for distribution; these files are not needed to run
the EXE.

- **Windows 10/11:** WebView2 Runtime is required. Wails uses its runtime-download
  bootstrap strategy if it is missing. The output is a native `youtube-vault.exe`.
- **macOS:** install Xcode command-line tools (`xcode-select --install`). The
  output is a `.app` bundle, packaged in a `.tar.gz` archive.
- **Linux:** install GCC, pkg-config, GTK3, WebKitGTK 4.1 and development headers.
  Ubuntu 24.04 build dependencies: `sudo apt-get install build-essential pkg-config
  libgtk-3-dev libwebkit2gtk-4.1-dev`. Packages require GTK3/WebKitGTK 4.1 at runtime.
  The build automatically uses the `webkit2_41` tag. No zenity dependency is needed
  for the desktop folder picker.

Linux builds also produce `youtube-vault-linux-<arch>.deb`. On Ubuntu 24.04,
install it with `sudo apt install ./youtube-vault-linux-amd64.deb` (or the ARM64
package). This installs `youtube-vault`, an application-menu launcher, an icon,
and notices/source under `/usr/share/doc/youtube-vault`. Runtime dependencies
are generated from the actual binary with `dpkg-shlibdeps`. Remove it with
`sudo apt remove youtube-vault`; per-user application data is retained. The
tarball remains available for other distributions with compatible system
libraries; it does not bundle GTK/WebKit or guarantee older glibc compatibility.
Building the Debian package additionally requires `dpkg-dev`.

The executable/bundle is under `src/server/build/bin/`. Distributable archives and
SHA-256 sidecars are under `dist/desktop/<os>-<arch>/`. Each package contains the
project license, existing notices/LGPL source, and an additional platform-specific
desktop dependency license report and license texts. Ordinary CI builds are
unsigned and macOS bundles are not notarized. Wails does not supply OS signing
identities. Set `DESKTOP_OUTPUT_ROOT` to use another output directory, for
example `dist/desktop-validation` when the normal Windows EXE is running.

## Build automation

The application icon is defined once in `public/app-icon.svg`. After changing
it, run `npm run icons:generate` and commit the regenerated PNG/ICO files.
Builds run `icons:check` to reject stale assets. The 1024px PNG supplies the
macOS ICNS bundle icon and the Linux embedded/window icon. A generated 256px
PNG supplies the installed Linux launcher icon. The Windows ICO contains
16–256px images and is embedded into both
architectures' EXEs. The same design is used by the application header and
browser/favicon assets. Package smoke tests check the actual EXE icon images,
macOS ICNS pixels, and Debian-installed PNG.

The **Desktop builds** workflow produces Windows AMD64/ARM64 executables and
Windows, Linux and macOS AMD64/ARM64 archives. It is available through workflow dispatch and runs for desktop branches
and pull requests. It uploads validation artifacts; it does not create a tag or
publish a GitHub Release. The tagged release workflow reuses this same native
matrix and includes the desktop archives, standalone Windows EXEs, Windows setup
EXEs, macOS PKGs, Linux Debian
packages, checksums and provenance alongside the browser/server artifacts in a
draft release. Release dry runs exercise the same matrix without publishing.

After building, `npm run test:desktop` compiles an isolated `desktop,e2e` smoke
binary. Its test-only readiness binding verifies that the native WebView mounts
React, authenticates to the private service, and shuts down cleanly. It uses
disposable data and is never included in the distributable archives. CI runs
the Linux window test under Xvfb; on a headless Linux host use
`dbus-run-session -- xvfb-run -a npm run test:desktop`.

CI also runs `node scripts/desktop-package-smoke.mjs` against the extracted
production archive. It checks checksums and notices, verifies the packaged
backend's health, embedded interface, identity and database restart, then
launches the unchanged production native window and checks DOM/service-binding
startup. This startup check terminates the window afterwards; the separate
e2e test checks clean UI-driven shutdown. Linux CI additionally installs,
launches and removes the `.deb` and validates its desktop entry. Windows and
macOS CI also install, reinstall, launch and remove the final installer outputs.
Release assembly requires both portable and installer coverage for all six
targets, plus checksums and provenance for the final files. The window
tests require no other copy of YouTube Vault to be running. For local backend
checks while another copy holds the single-instance lock, use
`node scripts/desktop-package-smoke.mjs --backend-only`.

### Protected macOS signing

To enable Developer ID signing and notarization for stable tag releases, create
a protected GitHub environment named `macos-signing` with these secrets:

- `APPLE_CERTIFICATE_P12_BASE64`: base64-encoded Developer ID Application P12.
- `APPLE_CERTIFICATE_PASSWORD`: P12 import password.
- `APPLE_SIGNING_IDENTITY`: Developer ID Application certificate identity.
- `APPLE_INSTALLER_CERTIFICATE_P12_BASE64`: Developer ID Installer P12, base64.
- `APPLE_INSTALLER_CERTIFICATE_PASSWORD`: installer P12 import password.
- `APPLE_INSTALLER_SIGNING_IDENTITY`: Developer ID Installer certificate identity.
- `APPLE_ID`, `APPLE_TEAM_ID`, `APPLE_APP_PASSWORD`: notarization credentials.

Set repository variable `MACOS_SIGNING_ENABLED=true` after configuring the
environment's approval and tag restrictions. PRs and dry runs stay unsigned.
Enabled tag builds fail if credentials are missing. The signing script imports
the identity into a temporary keychain, signs with hardened runtime and a
timestamp, submits to Apple's notary service, staples and validates the ticket,
and checks Gatekeeper acceptance. Packaging, checksums and provenance happen
after signing/stapling both the app and the PKG. The keychain and temporary certificate are removed.
See [Apple's notarization workflow](https://developer.apple.com/documentation/security/customizing-the-notarization-workflow).
Credentialed signing and clean-Mac acceptance must be verified before claiming
that public packages are signed/notarized. Windows Authenticode remains a
separate release gate.

## Runtime and development

The desktop window serves embedded assets through Wails. A Wails-only bootstrap
binding supplies an in-memory per-launch bearer and an ephemeral IPv4 loopback
address. The service accepts only its exact loopback host and desktop origins;
it cannot bind to a LAN address. `ADDR`, `API_TOKEN` and HTTP origin overrides do
not expose the desktop service. Data/output preferences and other engine settings
continue to apply.

SSE and ranged previews use real authenticated HTTP rather than Wails' buffering
asset handler. File/ZIP saves and Library exports use native save dialogs and
stream to a temporary file beside the selected destination; a failed transfer
does not overwrite an existing file. Tokens are not stored in local storage or
URLs. Folder selection uses the Wails native picker.

For development, rebuild with `npm run build:desktop`. This deliberately uses
static production assets rather than Wails' dynamic Vite integration. Existing
`npm run dev:all` remains the browser development workflow.

`youtube-vault --version`, `--no-browser`, and `--migrate-legacy-data` preserve the
existing diagnostic/server modes. A Chrome-compatible browser is still needed
for browser-assisted adaptive media extraction; Wails' embedded webview does not
replace that engine dependency.

For startup diagnostics when a GUI build has no console, set `DESKTOP_LOG_FILE`
to a writable file before launching. This records native startup logs; it does
not log the per-launch bearer. The file is truncated on each launch.

Before release, test the native window, queue events, a permitted media download,
preview seeking, save/cancel, ZIP export, folder picker and close/restart on each
OS family. Build and HTTP transport checks do not replace native UI acceptance.
