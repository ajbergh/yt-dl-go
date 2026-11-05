# Packaging

YouTube Vault has two editions built from the same interface and engine. Native
**desktop** packages use Wails, open their own window, and provide native save
and folder dialogs. The alternate **browser/server** packages open the interface
in an installed browser. They share per-user application data; close one before
starting the other.

## Native desktop artifacts

Both AMD64 and ARM64 are built on native CI runners for all three OS families.
`<arch>` below is `amd64` or `arm64`; macOS filenames use `darwin`.

| Platform | Portable | Installer |
| --- | --- | --- |
| Windows | `youtube-vault-windows-<arch>.exe` and `.zip` | `youtube-vault-windows-<arch>-setup.exe` |
| macOS | `youtube-vault-darwin-<arch>.tar.gz` | `youtube-vault-darwin-<arch>.pkg` |
| Linux | `youtube-vault-linux-<arch>.tar.gz` | `youtube-vault-linux-<arch>.deb` |

Run `npm ci` and `npm run build:desktop` on the target OS. Outputs go under
`dist/desktop/<os>-<arch>/`. Windows supports cross-building the two Windows
architectures; macOS and Linux require matching native architecture. NSIS,
Xcode tools, or Linux GTK/WebKit and Debian build dependencies are required.
See [DESKTOP.md](DESKTOP.md) for complete commands, prerequisites, icons, and signing.

The Windows EXE embeds the interface and runs without an adjacent assets folder.
Its companion ZIP carries mandatory distribution notices. Setup installs per
user with shortcuts and uninstall registration; missing WebView2 is provisioned
through the bundled bootstrapper and requires internet access. macOS PKGs install
`YouTube Vault.app` into `/Applications`, with notices inside the bundle. Debian
packages install the executable, application launcher, icon, and notices and
express dependencies scanned from the native binary. The Linux tarball still
needs compatible system GTK/WebKit and C libraries.

## Browser/server artifacts and builds

These packages use `youtube-downloader-*` names and have no Wails window or desktop
installer. The Go executable is built with `CGO_ENABLED=0`; an external browser
shows the embedded interface.

| Platform | Architectures | Archive |
| --- | --- | --- |
| Windows | amd64 / arm64 | `youtube-downloader-windows-<arch>.zip` |
| Linux | amd64 / arm64 | `youtube-downloader-linux-<arch>.tar.gz` |
| macOS | amd64 / arm64 | `youtube-downloader-darwin-<arch>.tar.gz` |

Install the pinned Node/npm and Go toolchains and run `npm ci`, then use:

```powershell
# Windows browser/server edition
./scripts/build-windows.ps1 -OutputPath dist/youtube-downloader.exe
./scripts/package-windows.ps1 -ExecutablePath dist/youtube-downloader.exe
```

```sh
# Unix browser/server edition; choose the target and architecture
TARGET_OS=linux TARGET_ARCH=amd64 bash ./scripts/build-unix.sh
TARGET_OS=darwin TARGET_ARCH=arm64 bash ./scripts/build-unix.sh
```

The scripts refresh the embedded frontend, validate the build, and package notices
and checksums. Browser/server macOS output is a command-line executable, whereas
the desktop edition above has an `.app` bundle. Browser/server builds can
cross-compile because they do not link the native WebView libraries.

Browser opening uses Windows' URL handler, macOS `open`, or Linux `xdg-open`.
The browser edition's folder picker uses `osascript` on macOS or `zenity` on Linux;
the Wails edition uses native dialogs and does not require zenity. All editions
need Chrome/Chromium/Edge for browser-assisted adaptive capture when that path is used.

`--background`, `--no-browser`, and `NO_BROWSER=1` select foreground HTTP operation
without opening a browser. These modes do not install a service, detach the
process, or provide a tray icon. With no arguments, the desktop binary opens its
native window and the browser/server binary opens the browser.

## Verification and releases

Each distributable has a `.sha256` sidecar. Packages include the project license,
dependency notices and license texts, and LGPL source/relinking materials. Desktop
packages additionally include a native dependency report and license directory.
The raw Windows EXE must be distributed with its companion notice materials.

The release workflow builds both six-target editions, adds the corresponding
source archive, verifies all expected artifacts and hashes, and creates
`SHA256SUMS.txt`. Current coverage is 14 desktop artifacts, six browser/server
archives, and one source archive. Release assembly independently rejects missing
portable or installer coverage rather than trusting uploaded artifact counts alone.

Native CI extracts the final portable packages, verifies icons/notices/build
identity, checks persistence and native startup, and exercises install/reinstall,
launch, removal, and saved-data preservation. These automated checks supplement
manual native UI acceptance; see the [release checklist](RELEASE_CHECKLIST.md).

A workflow dispatch is nonpublishing. A matching stable tag creates a **draft**
release after validation. Final hashes and provenance are generated after any
configured signing/notarization. Ordinary validation packages have no Windows
Authenticode or macOS Developer ID distribution signature. Ad-hoc macOS signing
supports execution but is not Developer ID signing. See [RELEASES.md](RELEASES.md)
for protected signing, publication, update discovery, and rollback policy.
