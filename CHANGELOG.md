# Changelog

This file summarizes notable user-facing changes. Version **v0.1.0** was publicly released on **2026-10-10**.

## [Unreleased]

No changes recorded since v0.1.0.

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
