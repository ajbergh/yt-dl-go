# Changelog

This file summarizes notable user-facing changes. Version **v0.1.0** was publicly released on **2026-10-10**.

## [Unreleased]

## [0.3.0] — 2026-11-06

### Added

- Native File → Exit and Help → About menus, with version and build details.
- Native desktop confirmation dialogs titled YouTube Vault, with No selected by default; browser builds retain browser confirmations.

### Changed

- The selected download folder is the Library's single media location. Playback and previews read directly from disk; storage-policy settings and automatic duplicate media copies are removed.
- Library cards now offer Reveal, Copy path, Preview, and one Delete files action. Redundant Folder, Save/ZIP, and Remove controls are removed.
- The generic format explanation is no longer displayed on queued or active downloads, including saved jobs from older versions.

### Fixed

- Startup reconciles deleted media without restoring it from obsolete copies; unavailable Library folders preserve their metadata.
- Legacy app-only files migrate into their recorded download folder. Missing captions do not block startup, and a failed migration save rolls back newly created outputs.
- Restarted jobs reuse finalized Library files instead of downloading them again.
- Windows Reveal passes the selection switch and file path separately. Linux tries file selection through FileManager1 and falls back to the containing folder when unavailable.
- Browser audio capture receives a share of the reserved headroom. Browser reservation overruns fall back to verified native ranges without relaxing the job limit or changing the selected quality.

## [0.2.0] — 2026-11-05

### Added
- Wails v2 desktop windows for Windows, macOS, and Linux, with native dialogs, single-instance handling, private per-launch authentication, and shutdown.
- AMD64/ARM64 portable desktop packages and Windows setup EXE, macOS PKG, and Linux DEB installers, with installation/removal and data-preservation CI.
- Shared crimson cloud-download icons across the app, executable/setup, macOS bundle, Linux launcher, and browser assets.
- Protected macOS app and PKG signing/notarization support; credentialed verification remains pending.
- Bulk queue pause, resume, cancel, and delete, with per-job failure reporting.
- Retry all failed playlist items while retaining successful files.
- Edit queued or paused download settings and add new downloads as paused.

### Changed
- Desktop-first README with Windows Wails screenshots, updated core guides, and consolidated planning/investigation documentation.
- Normal desktop copy uses application terminology instead of storage/service implementation details.

### Fixed
- Preserve failed selections after partial deletion, protect active file transfers, and clear obsolete resumable output when changing media settings.
- Coordinate dependency and CodeQL updates, refresh license notices, and keep generated frontend bundles and build caches out of Git.

### Distribution notes
- Windows packages are not Authenticode-signed. macOS packages do not have Developer ID distribution signatures or notarization; desktop apps use ad-hoc signing for execution.
- Automated native build, installer, persistence, and smoke checks cover all six desktop targets. Final manual macOS/Linux acceptance and credentialed signing verification remain pending.
- Verify package SHA-256 checksums and GitHub build provenance before installation. Automatic self-update remains disabled.

## [0.1.0] — 2026-10-10

### Added
- One local-first, CGO-free Go executable that serves an embedded React interface on Windows, macOS, and Linux.
- Single-video and playlist inspection with configurable resolution ceilings, codec preferences (compatibility MP4, VP9, AV1), selectable categories, and batch defaults.
- Pure-Go MP3 output, source AAC/M4A extraction when available, container/ID3 metadata, local thumbnails, optional subtitle sidecars, chapter metadata, and opt-in MP3 chapter splitting.
- Persistent, restart-recoverable download queues with pause/resume, failed-item retry, controlled concurrency, bandwidth limits, and clear fallback notes when a selected format cannot be obtained.
- A durable SQLite-backed Library with search, filters, pagination, local previews, selective file operations, and Library export/import.
- Multiple storage policies for app-managed and published media, user-configurable output naming, native folder selection, and reveal/open actions.
- Advisory update discovery from published GitHub Releases and embedded `--version` build identity.
- Cross-platform CI packages, pinned dependency checks, CodeQL, vulnerability/race checks, release checksums, and GitHub build provenance attestations.

### Changed
- Finished Library items outlive transient jobs; queue capacity and retention no longer silently delete the only user-owned media copy.
- Per-user application data directories replace legacy working-directory storage by default; explicit migration remains available through `--migrate-legacy-data`.
- Desktop-relevant runtime limits are configurable from Settings, with environment-variable override precedence and effective-source visibility.
- Download completion and ticket issuance retain terminal job state while its persistence write remains pending.

### Security
- Local loopback API binding by default, with bearer tokens and exact allowed origins/hosts required for non-loopback deployments.
- Content Security Policy, framing protection, scoped short-lived file tickets, private app data, validated output paths, and controlled media URL handling.
- License notices, LGPL decoder source, and relinking instructions in build and release artifacts.

### Current limitations
- Only download content you own or are permitted to save. YouTube delivery changes can affect format availability; no requested resolution is guaranteed.
- Browser-assisted adaptive capture requires a compatible locally installed Chromium-based browser and does not import normal browser cookies or private account credentials.
- The bundled UI does not send `API_TOKEN`, so token-protected API deployments need a separate authenticated client.
- Initial public Windows and macOS packages are unsigned and not notarized. Check their SHA-256 hashes; independent GitHub attestation API verification was unavailable at publication.
- Automatic self-update remains disabled pending native signing, final-artifact verification, atomic replacement, and rollback support.

The v0.1.0 release contains six OS/architecture archives, a source archive, and checksum files. See the public GitHub Release for download links and disclosures.
