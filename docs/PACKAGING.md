# Cross-platform packaging

The downloader is a CGO-free Go executable with the React UI embedded at build time. The same source now packages for Windows, Linux, and macOS without bundling a browser or media helper binary.

## CI artifact matrix

The CI workflow builds and verifies these package targets:

| Platform | Architecture | Package |
| --- | --- | --- |
| Windows | amd64 on the current Windows runner | `youtube-downloader-windows-<arch>.zip` |
| Linux | amd64 | `youtube-downloader-linux-amd64.tar.gz` |
| Linux | arm64 | `youtube-downloader-linux-arm64.tar.gz` |
| macOS | amd64 | `youtube-downloader-darwin-amd64.tar.gz` |
| macOS | arm64 | `youtube-downloader-darwin-arm64.tar.gz` |

Every archive has a sibling `.sha256` file. CI artifacts are validation packages, not signed public releases.

The Windows artifact also retains the raw `youtube-downloader.exe` for compatibility with the existing workflow.

## Local builds

Install Go 1.26+, Node.js/npm, and run `npm ci` first.

### Windows

```powershell
.\scripts\build-windows.ps1 -OutputPath dist\youtube-downloader.exe
.\scripts\package-windows.ps1 -ExecutablePath dist\youtube-downloader.exe
```

The first script rebuilds the embedded UI, runs Go tests, and produces the executable. The second creates a ZIP containing the executable, README, and third-party notices, plus a SHA-256 checksum file.

### Linux

```bash
TARGET_OS=linux TARGET_ARCH=amd64 bash ./scripts/build-unix.sh
```

Use `TARGET_ARCH=arm64` for an ARM64 package. The script rebuilds the embedded UI, runs native-host Go tests and vet, then performs the requested CGO-free target build and creates the archive/checksum.

### macOS

```bash
TARGET_OS=darwin TARGET_ARCH=arm64 bash ./scripts/build-unix.sh
```

Use `TARGET_ARCH=amd64` for Intel Macs. The package is a command-line executable that starts the local service and opens the browser; it is not currently wrapped in a `.app` bundle.

## Platform integration behavior

### Browser opening and browser-assisted HD

The UI URL opens through the operating system default URL handler:

- Windows: `rundll32.exe url.dll,FileProtocolHandler`
- macOS: `open`
- Linux: `xdg-open`

Adaptive-HD authorization uses chromedp's platform browser discovery when `CHROME_PATH` is unset. `CHROME_PATH` remains the explicit override on every platform and is the most deterministic option for packaged deployments.

The application does not bundle Chrome/Chromium. Progressive downloads that do not require the browser-assisted adaptive path remain handled by the native Go engine.

### Background/no-browser operation

All platform packages support `--background` and `--no-browser`. These suppress the automatic URL-handler launch but do **not** detach the process: the local service, queue, and workers continue in the foreground until the executable receives its normal shutdown signal. The process logs the UI URL for later manual access.

`NO_BROWSER=1` remains available for CI/headless automation. No current package installs a login item, service manager unit, or native tray icon.

A native tray dependency was investigated for P2.2. The leading zero-CGO candidate, `github.com/gogpu/systray`, satisfies the single-binary/cross-platform build model but is still young and, as of September 2026, has unresolved menu-dispatch and macOS interaction issues upstream. The repository therefore does not make that dependency part of production packages yet.

### Folder selection

- Windows: PowerShell + `System.Windows.Forms.FolderBrowserDialog`
- macOS: `osascript` folder chooser
- Linux: `zenity --file-selection --directory`

On Linux, `zenity` is required only for the **Browse** button. A user can still type an absolute output path in Settings when `zenity` is unavailable.

### Reveal/open output

- Windows: Explorer, with `/select,` for reveal.
- macOS: `open -R` for reveal and `open <folder>` for folder opening.
- Linux: `xdg-open <folder>`. There is no portable freedesktop file-selection command, so **Reveal** intentionally opens the containing folder rather than attempting to execute or select the media file.

The command-selection semantics are covered by platform-independent Go tests.

## Signing and notarization

The repository does **not** currently sign CI artifacts. Signing is deliberately kept out of ordinary branch CI because it requires protected credentials and should be performed only in a controlled release workflow.

A signed public-release pipeline would require:

- **Windows:** an Authenticode code-signing certificate, preferably backed by an approved secret/key service, followed by signature verification before publishing.
- **macOS:** Apple Developer ID Application signing, hardened runtime where appropriate, notarization with Apple, ticket stapling where applicable, and verification with `codesign`/`spctl`. An eventual `.app`/DMG distribution would also need bundle metadata and signing of the full bundle.
- **Linux:** there is no universal OS code-signing requirement. Published archives should at minimum retain SHA-256 checksums; a future release process may add GPG or Sigstore/cosign signatures.

No signing key, Apple credential, or certificate should be committed to the repository.

## Release packaging recommendation

When promoting CI-validated packages to a GitHub Release:

1. build from an immutable release tag;
2. run the full frontend, Go, browser-E2E, and platform packaging gates;
3. sign/notarize applicable platform artifacts using protected release secrets;
4. verify signatures/checksums after signing;
5. publish archives and checksum/signature files together;
6. record the source tag/commit and supported architectures in the release notes.

