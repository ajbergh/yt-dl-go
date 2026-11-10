# Architecture and developer guide

This guide covers the implementation, source builds, alternate browser/server
edition, and saved-data recovery. For downloads and everyday use, see the
[main README](../README.md).

## Desktop architecture

The desktop edition uses Wails v2 to display the React interface in its own
native window, with native save, folder, and confirmation dialogs. A private Go
service handles downloads, SQLite persistence, media streaming, and live queue
updates. Desktop bootstrapping supplies a private per-launch API origin and
bearer token; the desktop service does not expose a configurable network listener.

Closing the desktop window stops the app and its workers. The queue, Vault,
preferences, and output locations persist for the next launch. No login item,
background service, or tray process is installed.

The [desktop build and runtime guide](DESKTOP.md) documents the pinned Wails
version, native prerequisites, icons, packaging, tests, and protected signing.

## Build from source

Install the Node version in [`.nvmrc`](../.nvmrc), npm, and the Go version in
[`src/server/go.mod`](../src/server/go.mod), then run from the repository root:

```sh
npm ci
npm run build:desktop
```

Build on the target OS. Linux and macOS also require matching native architecture;
Windows can build AMD64 and ARM64 from one host. Install NSIS 3.11 for Windows,
Xcode command-line tools for macOS, or the GTK/WebKit development libraries and
Debian packaging tools for Linux. The script installs pinned Wails tooling.

On Windows, build both architectures with:

```powershell
./scripts/build-desktop-windows.ps1
# Or only one:
./scripts/build-desktop-windows.ps1 -Architecture amd64
```

Outputs are under `dist/desktop/<os>-<arch>/`. See the
[desktop build guide](DESKTOP.md) for explicit targets, prerequisites, checksums,
native tests, and signing. Never store application data under the repository's
generated `dist` directory.

## Browser/server edition

The alternate `youtube-downloader` executable serves the same interface in your
browser. It defaults to `http://127.0.0.1:8080`; keep its process running while
using it. `--no-browser` and `--background` suppress browser opening but do not
detach the process. Stop it with Ctrl+C.

The [packaging guide](PACKAGING.md) covers its scripts and platform integration.
Network/API configuration belongs to the [engine guide](../src/server/README.md)
and [security policy](../SECURITY.md). Both editions share the same per-user data;
run one edition at a time.

## Media formats and browser-assisted capture

Quality choices are maximum heights, not upscaling requests or guarantees.
Compatibility MP4 selects H.264/AAC; Best quality, Prefer VP9, and Prefer AV1 may
produce WebM when appropriate. Completed files report their actual quality and
format, and fallback notes explain changes. M4A preserves a compatible source
AAC track; MP3 conversion is built in. Captions are optional VTT/SRT sidecars and
may fail independently of the media download.

For browser-assisted adaptive HD/4K delivery, install Chrome, Chromium, or
Microsoft Edge. Discovery is automatic; advanced settings or `CHROME_PATH` can
select an executable. The embedded desktop WebView does not replace that
dependency. Temporary capture sessions do not import your normal browser's
cookies or credentials. YouTube delivery changes can affect availability and
completion.

## Saved data, backups, and recovery

The selected download folder is the Vault's media location. The default is
`~/Downloads/YouTube_Vault`, organized by channel. Playback reads the saved file
directly. At startup, missing files are removed from the Vault; an unavailable
download folder, such as a disconnected drive, preserves its entries. Legacy
app-only media migrates into its recorded download folder when upgrading.

Preferences and history live separately from the executable:

| Platform | Default application-data directory |
| --- | --- |
| Windows | `%LOCALAPPDATA%\yt-dl-go` |
| macOS | `~/Library/Application Support/yt-dl-go` |
| Linux | `$XDG_DATA_HOME/yt-dl-go`, or `~/.local/share/yt-dl-go` |

Set `DATA_DIR` before launch to choose another private location. Back up both this
directory and the download folder with the app closed before migration or rollback.
Installers retain saved data and downloads when removed.

For startup problems, set `DESKTOP_LOG_FILE` to a writable path before launching.
If an error reports an incompatible data schema, preserve the data and restore a
compatible backup; do not delete the database or add columns manually. Legacy
`./downloads` data can be copied with `youtube-vault --migrate-legacy-data` after
closing the app. See the [engine guide](../src/server/README.md) for recovery details.

## Development and verification

See [Contributing](../CONTRIBUTING.md) for setup and checks, the
[frontend guide](../src/README.md) for UI development, and the
[engine/API guide](../src/server/README.md) for configuration and media behavior.
Native CI covers all six OS/architecture targets with build, portable-package,
installer, launch, removal, and saved-data preservation checks as applicable.

Historical manual acceptance and signing evidence lives in the
[release checklist](RELEASE_CHECKLIST.md). It applies to the recorded builds,
not automatically to later releases. The [release policy](RELEASES.md) distinguishes
checksums/provenance from native signing; each release's notes disclose its
signing status and any remaining platform acceptance limitations.
