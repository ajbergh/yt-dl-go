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

## Tagged release workflow and provenance

Pushing a stable semantic-version tag such as `v1.2.3` triggers `.github/workflows/release.yml`.

The workflow:

1. validates the stable tag format and requires it to equal `v<package.json version>`;
2. reruns frontend type-check/build, Bun tests, Go tests/vet, and browser E2E;
3. builds the Windows package plus Linux/macOS amd64 and arm64 packages;
4. injects `VERSION`, `COMMIT`, and `BUILD_DATE` into the Go executable via linker variables;
5. verifies every per-package SHA-256 checksum;
6. produces a canonical `SHA256SUMS.txt`;
7. creates GitHub build-provenance attestations for each release archive and the checksum manifest using OIDC;
8. creates a **draft** GitHub Release containing the archives and checksum files.

The draft is the release-publication safety gate: it is not returned by GitHub's latest-release endpoint, so the app cannot advertise the release until a human publishes it after the required review/signing steps in [RELEASES.md](RELEASES.md).

For local release-like builds, the package scripts accept these optional environment variables:

- `VERSION` — semantic version such as `v1.2.3`
- `COMMIT` — source commit identifier
- `BUILD_DATE` — metadata-safe RFC3339-style build timestamp

When absent, binaries report `dev`, `unknown`, and `unknown`.

### What the provenance attestation does — and does not — mean

GitHub provenance attestations cryptographically bind release archives to the GitHub Actions workflow and source repository. They are useful supply-chain evidence, but they are **not** equivalent to operating-system-native code signing.

The repository still does not possess or embed signing credentials:

- **Windows:** public self-updating distribution should use an Authenticode certificate and verify the signature before replacement.
- **macOS:** public self-updating distribution should use Apple Developer ID signing and notarization; a future `.app`/DMG path would also require bundle-level signing/stapling.
- **Linux:** published archives retain SHA-256 checksums plus GitHub provenance; optional GPG/Sigstore signatures can be added later if distribution requirements demand them.

No signing key, Apple credential, or certificate should be committed to the repository.

## Update-discovery policy

Tagged release builds expose their embedded build metadata through `/api/health`. The UI can ask `/api/update` for the latest stable project release. That server-side check is bounded, redirect-free, and validates both semantic-version metadata and the GitHub release URL.

The app deliberately does **not** replace its own executable. A safe automatic updater must not be enabled until all of the following are available:

1. OS-native signing/notarization for applicable platforms;
2. downloaded-artifact checksum **and** signature/provenance verification;
3. platform-specific atomic replacement semantics;
4. preservation of the previously working binary;
5. restart verification and automatic rollback when the new process does not become healthy;
6. clear opt-in/update policy and failure reporting.

