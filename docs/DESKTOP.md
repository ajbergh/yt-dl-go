# Wails v2 desktop builds

The desktop edition uses Wails **v2.17.0** to display the existing React interface
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

- **Windows 10/11:** WebView2 Runtime is required. Wails uses its runtime-download
  bootstrap strategy if it is missing. The output is a native `youtube-vault.exe`.
- **macOS:** install Xcode command-line tools (`xcode-select --install`). The
  output is a `.app` bundle, packaged in a `.tar.gz` archive.
- **Linux:** install GCC, pkg-config, GTK3, WebKitGTK 4.1 and development headers.
  Ubuntu 24.04 build dependencies: `sudo apt-get install build-essential pkg-config
  libgtk-3-dev libwebkit2gtk-4.1-dev`. Packages require GTK3/WebKitGTK 4.1 at runtime.
  The build automatically uses the `webkit2_41` tag. No zenity dependency is needed
  for the desktop folder picker.

The executable/bundle is under `src/server/build/bin/`. Distributable archives and
SHA-256 sidecars are under `dist/desktop/<os>-<arch>/`. Each package contains the
project license, existing notices/LGPL source, and an additional platform-specific
desktop dependency license report and license texts. Binaries are unsigned;
macOS bundles are not notarized. Wails does not supply OS signing identities.

## Build automation

The **Desktop builds** workflow produces Windows, Linux and macOS AMD64/ARM64
archives. It is available through workflow dispatch and runs for desktop branches
and pull requests. It uploads validation artifacts; it does not create a tag or
publish a GitHub Release. Native desktop packages are separate from the existing
CGO-free browser/server release artifacts.

After building, `npm run test:desktop` compiles an isolated `desktop,e2e` smoke
binary. Its test-only readiness binding verifies that the native WebView mounts
React, authenticates to the private service, and shuts down cleanly. It uses
disposable data and is never included in the distributable archives. CI runs
the Linux window test under Xvfb; on a headless Linux host use
`dbus-run-session -- xvfb-run -a npm run test:desktop`.

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

Before release, test the native window, queue events, a permitted media download,
preview seeking, save/cancel, ZIP export, folder picker and close/restart on each
OS family. Build and HTTP transport checks do not replace native UI acceptance.
