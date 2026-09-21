# yt-dl-go Product Roadmap

> Durable roadmap for the `yt-dl-go` product. This file is the source of truth for roadmap scope, sequencing, implementation status, acceptance criteria, and follow-up work.
>
> **Branch:** `roadmap/media-library-batch-v1`
>
> **Last updated:** 2026-09-20

## Product direction

`yt-dl-go` is evolving from a local YouTube downloader into a private local media acquisition, batch-processing, organization, and library application.

The target workflow is:

```text
YouTube URL / Playlist
        ↓
Inspect metadata and formats
        ↓
Choose video/audio, quality, category, and batch options
        ↓
Persistent queue and resumable processing
        ↓
Publish to user-selected output structure
        ↓
Searchable local media library
```

The roadmap deliberately preserves the existing strengths of the project: a single Go executable, an embedded React UI, a pure-Go media path where practical, SQLite-backed durability, loopback-first security, restart recovery, adaptive-download support, and no mandatory FFmpeg dependency.

## Status legend

- [ ] Planned
- [~] In progress
- [x] Implemented
- [!] Blocked or needs a design decision
- [T] Implemented but runtime/test validation still required

A roadmap item is marked **Implemented** only when the product code, persistence/API changes, tests, and this roadmap have been updated together. If source changes are complete but runtime or test execution has not yet been verified, use **[T]**.

## Engineering guardrails

1. Keep the application local-first and single-user by default.
2. Preserve the single-executable distribution model.
3. Prefer pure Go and avoid CGO where practical.
4. Do not regress restart recovery, partial-download cleanup, or final-file validation.
5. Treat files published into the user's chosen output directory as user-owned media, not disposable cache.
6. Keep the SQLite schema forward-migratable and backward-compatible with existing databases.
7. Avoid exposing signed YouTube media URLs to the frontend.
8. Keep destructive operations explicit and narrowly scoped.
9. Prefer small, reviewable commits. Update this roadmap in the same implementation sequence.
10. Add tests for behavioral changes before marking work complete.

---

# Milestone 1 — Media Library & Batch Workflow v1

Goal: make the frontend and output-management experience catch up with the existing downloader engine.

## P0 — Product consistency and safe file management

### P0.1 Per-download category selection

**Status:** [x] Complete

Users can already create categories and use `{category}` in naming patterns and category-based subfolders, but new jobs currently capture only the global default category.

#### Scope

- Add a category selector to every inspected download card.
- Capture the selected category when a job is queued.
- Validate the category against the persisted user category list.
- Persist the selected category with the job.
- Use the selected category in filename expansion and category subfolder routing.
- Preserve the category across restart/recovery.
- Add frontend and backend tests.

#### Acceptance criteria

- A user can inspect two URLs and assign different categories before queuing them.
- The selected category is visible in the created job/file metadata.
- Category-based output routing uses the selected category rather than the global default.
- Invalid or unknown categories are rejected by the server.
- Older clients that omit `category` continue to use the configured default category.

### P0.2 Batch “apply to all” controls

**Status:** [x] Complete

For multi-URL inspection and playlist workflows, add batch controls that can apply common settings without editing every card individually.

#### Scope

- Apply media type to all inspected items.
- Apply video quality to all applicable inspected items.
- Apply MP3 bitrate to all applicable inspected items.
- Apply category to all inspected items.
- Preserve per-item overrides after a batch value is applied.

#### Acceptance criteria

- Batch changes update all eligible cards immediately.
- Unsupported audio/video combinations remain disabled.
- Individual cards can still be changed afterward.

### P0.3 Separate Library removal from disk deletion

**Status:** [x] Complete

Current job deletion can remove both managed app files and files published to the user's chosen output directory. That is too destructive for a media-library workflow.

#### Target actions

- **Remove from Library** — remove app history/reference while preserving user-published output.
- **Delete managed copy** — remove the private app-managed copy only.
- **Delete published media** — explicitly remove the output copy.
- **Delete everything** — remove history plus all tracked copies, with a strong confirmation.

#### Acceptance criteria

- Removing an item from Library never silently deletes media in the configured output directory.
- Destructive actions state exactly which copies will be removed.
- Output path validation remains in place for all delete operations.

### P0.4 Storage policy and duplicate-copy control

**Status:** [x] Complete

The application currently retains a managed copy under `DATA_DIR` and publishes another copy to the configured output location. Large files may therefore consume approximately twice their final size.

#### Proposed policy modes

- **Managed + Published** — current behavior.
- **Published only** — finalize to the user output path and retain metadata/history only.
- **Managed only** — keep media private to the application library.

#### Acceptance criteria

- Storage mode is a persisted setting.
- Existing jobs retain the storage policy captured when queued.
- The UI explains the durability tradeoffs for each mode.
- Ticket/download behavior remains safe for files that still exist.

### P0.5 Native folder selection and filesystem actions

**Status:** [x] Complete

Typing an absolute path manually is not sufficient desktop UX.

#### Scope

- Native folder-picker integration for output location.
- Reveal output file in Finder/Explorer/file manager.
- Open containing folder.
- Copy absolute path.
- Graceful fallback to the existing text field when native integration is unavailable.

#### Design note

The current browser-hosted SPA cannot directly expose arbitrary local filesystem actions. Evaluate a minimal localhost API capability with strict path validation, or a desktop shell only if necessary. Do not weaken server path controls.

---

# Milestone 2 — Library quality and metadata

## P1.1 Preserve thumbnails locally

**Status:** [x] Complete

Remote thumbnail URLs are not durable library metadata.

#### Scope

- Download and validate the selected YouTube thumbnail.
- Store a managed thumbnail with the media record.
- Optionally publish sidecar artwork next to output media.
- Serve local thumbnails to the UI when available.
- Keep remote URLs as fallback metadata only.

### P1.2 MP3 ID3 metadata and artwork

**Status:** [x] Complete

The pure-Go MP3 path currently emits audio without ID3 tags.

#### Proposed fields

- Title
- Artist/channel
- Album/playlist title when available
- Track number for playlist position
- Publish date
- Source URL
- Cover artwork

#### Acceptance criteria

- Common players display title and artist without filename parsing.
- Playlist audio carries track numbering where known.
- Metadata writing remains pure Go where practical.

### P1.3 M4A “original audio” mode

**Status:** [x] Complete

When YouTube exposes AAC-in-MP4 audio, users should be able to preserve it without transcoding.

#### Proposed options

- M4A — source AAC, no MP3 transcode
- MP3 — 128 / 192 / 256 / 320 kbps

Benefits: lower CPU use, faster completion, and no additional lossy generation.

### P1.4 Library folder/category navigation

**Status:** [x] Complete

Promote the Library from a job-results list to a true local media browser.

#### Scope

- Category/channel sidebar or filter panel.
- File count and storage-size summaries.
- Grid/list views with equivalent capabilities.
- Search title, creator, category, filename, and playlist.
- Open/reveal/copy-path actions.
- Persistent user layout preference.

### P1.5 Local media preview

**Status:** [x] Complete

Provide lightweight playback from finalized local media.

#### Scope

- Video playback for supported browser codecs.
- Audio playback.
- Seek/range support through scoped local URLs.
- No auto-play.
- Do not expose arbitrary filesystem paths.

---

# Milestone 3 — Playlist and queue controls

## P1.6 Playlist item selection before queueing

**Status:** [T] Implemented; CI validation pending

Playlist inspection now allows users to choose which exposed items are queued.

#### Implemented

- Expandable playlist inspection with all valid exposed entries selected by default.
- Select all / clear all controls and per-item checkboxes.
- Selected-item counts and disabled queue submission when a playlist selection is empty.
- Approximate aggregate MP3 output size when selected durations and bitrate make an estimate meaningful.
- Selected entries are submitted with their original one-based playlist index and video ID.
- The worker re-fetches playlist metadata and rejects stale/tampered selections before media transfer.
- Scheduler queue indexes remain contiguous while `playlistIndex` preserves original position.
- Original playlist position is retained in managed filenames and MP3 track numbers; MP3 track totals retain the full exposed playlist count.
- Selection state persists through SQLite/restart using the existing queue-item JSON and is preserved by whole-job Retry.
- Queue labels distinguish selection order from original playlist position.
- Backend coverage includes input validation, subset processing, original-position naming, stale-selection rejection, retry preservation, and SQLite persistence.
- Frontend coverage includes Select all/Clear all, per-item selection, request shaping, selected count, and MP3 aggregate estimates.

### P1.7 Queue reordering and priority

**Status:** [ ] Planned

The repository already includes `@dnd-kit`; use it for explicit user queue ordering.

#### Scope

- Drag-to-reorder queued jobs/items.
- “Download next” action.
- Persist queue priority/order.
- Preserve ordering across restart.
- Do not reorder already-active work unexpectedly.

### P1.8 Retry one playlist item

**Status:** [ ] Planned

A single failed playlist entry should not require retrying an entire playlist.

#### Scope

- Per-item retry endpoint/state.
- Preserve successful playlist files.
- Retry only failed/cancelled entry when possible.
- Keep original playlist ordering.

### P1.9 Bandwidth limiting

**Status:** [ ] Planned

Add optional application-level bandwidth control for long-running batch jobs.

#### Scope

- Global limit.
- Unlimited default.
- Persist setting.
- Fair sharing across active transfers.

---

# Milestone 4 — Real-time UX and desktop polish

## P1.10 Server-Sent Events for job updates

**Status:** [ ] Planned

The UI currently polls the full job list approximately every 1.8 seconds.

#### Proposed events

- job-created
- job-progress
- job-status
- job-file-finalized
- job-error
- job-deleted
- settings-changed

Use SSE rather than WebSockets unless bidirectional real-time messaging becomes necessary.

### P2.1 System notifications

**Status:** [ ] Planned

Optional notifications for:

- batch completed
- job failed
- playlist partially completed
- disk/output error

### P2.2 Tray/background mode

**Status:** [ ] Planned

Investigate whether a lightweight tray workflow improves long-running batch downloads without compromising the simple executable model.

### P2.3 Automatic updater and signed releases

**Status:** [ ] Planned

- version metadata
- signed release artifacts
- update notification
- optional automatic update flow
- rollback-safe behavior

---

# Milestone 5 — Format expansion

## P1.11 Subtitle/caption extraction

**Status:** [ ] Planned

Add opt-in subtitle/caption download for owned/authorized content.

#### Scope

- Enumerate available caption tracks.
- Select language.
- SRT/VTT output.
- Optional sidecar organization with media.
- Playlist support.

## P2.4 1440p and 2160p support

**Status:** [ ] Planned

This is an engineering project, not merely a quality-dropdown change.

YouTube commonly uses VP9/AV1 and Opus for higher resolutions. The implementation needs an explicit codec/container strategy.

#### Research and implementation areas

- VP9 and AV1 video handling.
- Opus audio handling.
- WebM output.
- Whether selected codecs can be muxed safely into MP4.
- Browser-assisted adaptive authorization parity with the current H.264 path.
- CPU/memory impact.
- Fallback semantics.
- Test fixtures and live verification.

### P2.5 Advanced codec/container selection

**Status:** [ ] Planned

Potential user-facing modes:

- Compatibility MP4
- Best quality
- AV1 preferred
- VP9 preferred
- Original audio

Do not expose codec complexity until format selection and compatibility checks are robust.

---

# Milestone 6 — Architecture and maintainability

## P1.12 Decompose `src/pages/home.tsx`

**Status:** [ ] Planned

The production page currently owns service initialization, polling, queue state, inspection, settings, library state, and most rendering.

#### Target structure

```text
src/
  pages/
    queue.tsx
    library.tsx
    settings.tsx
  components/downloader/
    add-download.tsx
    inspection-card.tsx
    batch-controls.tsx
    queue-toolbar.tsx
    queue-item.tsx
    library-card.tsx
    output-settings.tsx
  hooks/
    use-service.ts
    use-jobs.ts
    use-settings.ts
```

The exact boundaries may evolve, but new major features should not continue expanding a single page component indefinitely.

## P1.13 Frontend dependency cleanup

**Status:** [ ] Planned

Audit root dependencies and remove unused scaffold packages after the UI structure stabilizes.

## P1.14 Browser end-to-end testing

**Status:** [ ] Planned

Add a Playwright-style E2E layer covering:

1. service startup
2. URL inspection
3. queue creation
4. pause/resume
5. completion
6. Library appearance
7. settings persistence
8. restart persistence
9. destructive-action confirmations

Existing Go and Bun tests remain the fast unit/integration layer.

## P2.6 Cross-platform packaging

**Status:** [ ] Planned

The Go architecture is suitable for expansion beyond Windows.

Investigate:

- macOS build and browser discovery
- Linux build and browser discovery
- filesystem reveal/open semantics
- release packaging
- code signing/notarization

---

# Explicitly deferred / non-goals

These are not current roadmap commitments:

- DRM bypass
- paywall bypass
- browser-cookie/account credential import
- private-video credential harvesting
- live HLS/DASH recording
- public multi-tenant hosting
- arbitrary non-YouTube site downloading

---

# Implementation journal

## 2026-09-20

### Roadmap creation

**Status:** [x] Complete

- Created `roadmap/media-library-batch-v1`.
- Added this durable roadmap.
- Chose **P0.1 Per-download category selection** as the first implementation item because it completes backend/output capabilities that already exist and has a small architectural footprint.

### P0.1 Per-download category selection

**Status:** [x] Complete

Implemented:

1. Extended frontend inspection draft state with `category`.
2. Added a category selector to each inspection result.
3. Added `category` to `POST /api/jobs`.
4. Added optional per-job category handling to the Go API.
5. Added canonical category validation against persisted `userCategories`.
6. Preserved backward compatibility by falling back to `defaultCategory` when the field is omitted.
7. Captured the selected category with the job so output filename expansion and category subfolders use the per-job value.
8. Exposed category in the job API response and frontend job type.
9. Preserved category when retrying failed/partial/cancelled jobs.
10. Updated backend and frontend tests plus the server API documentation.

Validation note: confirmed by CI run `35550906645` on the roadmap branch. Frontend type-check/build, Bun integration tests, Go tests, Go vet, and the Windows production executable build all passed.

### P0.2 Batch “apply to all” controls

**Status:** [x] Complete

Implemented:

1. Added an inspection-level “Apply to all inspected items” toolbar when more than one draft is present.
2. Added batch media-type selection; audio is applied only to eligible items and only when the backend reports MP3 support.
3. Added batch video-quality selection; individual videos keep their prior value when the requested ceiling is not advertised as supported.
4. Added batch MP3 bitrate selection for audio drafts.
5. Added batch category selection.
6. Kept all per-item controls active after batch application so users can override individual items.
7. Added UI coverage for batch controls and the per-item override behavior.
8. Made the Settings quality selector explicitly addressable and repaired a pre-existing brittle UI-test selector.

Validation note: confirmed by CI run `35550906645` on the roadmap branch. Frontend type-check/build, Bun integration tests, Go tests, Go vet, and the Windows production executable build all passed.


### CI foundation

**Status:** [x] Complete

- Added `.github/workflows/ci.yml`.
- CI runs frontend type-check/build, Bun integration tests, Go tests, and Go vet on Ubuntu.
- CI also performs the production Windows build through `scripts/build-windows.ps1` and uploads the executable as a short-lived workflow artifact.
- Push concurrency cancels obsolete branch runs so the newest commit is authoritative.

### P0.3 Separate Library removal from disk deletion

**Status:** [x] Complete

Implemented:

1. Added explicit managed-vs-published availability fields to finalized file metadata.
2. Added SQLite schema migration v6 to persist those availability states.
3. Changed ordinary `DELETE /api/jobs/{id}` to mean “Remove from Library”: private managed media and history are removed, while published output is preserved.
4. Added `DELETE /api/jobs/{id}/managed` for deleting only app-managed media.
5. Added `DELETE /api/jobs/{id}/published` for deleting only validated tracked output copies.
6. Added `DELETE /api/jobs/{id}/all` for explicit destructive deletion of managed media, published media, and history.
7. Added strict output-path validation before published media deletion.
8. Invalidated download tickets when managed media is removed and reject new/stale tickets for unavailable managed copies.
9. Changed retention pruning so it never deletes published user output.
10. Updated the Library UI with separate Remove, Managed copy, Published copy, and Delete everywhere actions, explicit confirmation text, availability badges, and disabled Save actions when managed copies are gone.
11. Added backend regression coverage for all deletion scopes and retention preservation, plus frontend tests for the scoped actions.
12. Updated the API documentation.

Important bug fixed: before this milestone, automatic retention cleanup could delete files already copied into the user's configured download directory.

### P0.4 Storage policy and duplicate-copy control

**Status:** [x] Complete

Implemented:

1. Added persisted `storageMode` with three supported policies: `managed-published`, `published-only`, and `managed-only`.
2. Added SQLite migration v7 for both application settings and per-job captured storage mode.
3. Captured the selected storage policy when a job is queued and preserved it when retrying jobs.
4. `managed-published` retains the existing two-copy behavior.
5. `published-only` publishes the finalized file and then removes the private managed media copy.
6. `managed-only` retains the private Library copy and skips publication to the configured output directory.
7. Updated restart/resume validation so completed playlist items can be considered valid from the copy set their policy actually retained.
8. Added a Settings UI explaining the disk/durability tradeoffs and that policy changes affect only newly queued jobs.
9. Added backend tests for all three modes and invalid values plus frontend persistence coverage.
10. Updated API documentation.

### P0.5 Native folder selection and filesystem actions

**Status:** [x] Complete

Implemented:

1. Added a protected localhost filesystem API that accepts only a job ID, persisted file ID, and one of three fixed actions: `reveal`, `open-folder`, or `copy-path`.
2. The server resolves paths exclusively from tracked published-file metadata; clients cannot submit arbitrary executable filesystem paths.
3. Re-validates output containment, filename consistency, file type, existence, and expected size before every filesystem action.
4. Added Windows Explorer reveal/open support, macOS Finder support, and Linux folder opening through `xdg-open`.
5. Added a native folder-selection endpoint. Windows uses the local Windows Forms folder chooser through PowerShell STA, macOS uses the native `choose folder` dialog, and Linux supports `zenity` when installed.
6. Folder selection returns only an absolute validated path; cancellation is a normal `204` response.
7. Connected Settings “Browse” to the native picker while retaining the editable absolute-path field as fallback.
8. Added Library Reveal, Open Folder, and Copy Path actions for published files.
9. Filesystem/open operations participate in the job reader guard so published deletion cannot race an active reveal/open operation.
10. Added backend and frontend tests using injected selectors/openers so CI never launches real OS dialogs.
11. Updated API documentation.

### P1.1 Preserve thumbnails locally

**Status:** [x] Complete

Implemented:

1. Added best-effort thumbnail capture during media finalization without making artwork failure fatal to the download.
2. Restricted thumbnail sources and redirects to approved YouTube image hosts and routed downloads through the existing guarded public-HTTPS client.
3. Added a 5 MiB limit and restricted stored image types to JPEG, PNG, or WebP.
4. Stored local artwork inside private per-job managed storage; absolute artwork paths are never exposed by the API.
5. Added SQLite migration v8 for local-thumbnail availability and MIME metadata.
6. Added an authenticated job-scoped thumbnail endpoint keyed by persisted file ID.
7. Added reader guards and regular-file validation so thumbnail serving cannot race managed-storage deletion.
8. Kept the approved remote YouTube thumbnail URL as fallback metadata.
9. Updated the Library to fetch local artwork with authenticated blob requests and fall back to the remote thumbnail if local artwork is unavailable.
10. Ensured managed-copy deletion clears local artwork availability, while published-only storage may retain the lightweight local artwork/metadata until Library retention or removal.
11. Added backend persistence/serving tests and frontend local-thumbnail preference coverage.
12. Updated API documentation.

Published sidecar artwork remains intentionally deferred; P1.1's durable-Library goal is satisfied by private local artwork without creating additional user-visible files.


### P1.2 MP3 ID3 metadata and artwork

**Status:** [x] Complete

Implemented:

1. Added a dependency-free ID3v2.3 writer in pure Go.
2. Writes Unicode-safe title, artist/channel, playlist album, track number/total, publish year plus full publish-date metadata, and canonical YouTube source URL.
3. Embeds the locally captured thumbnail as front-cover APIC artwork when it is JPEG/PNG/WebP and no larger than 1 MiB.
4. Integrates tag writing directly into the existing AAC-to-MP3 conversion stream before MP3 frames, preserving atomic finalization and hard output-budget accounting.
5. Captures audio metadata and artwork before conversion so cover art can be embedded without rewriting the finalized MP3.
6. Keeps metadata best-effort: tag construction problems cannot fail an otherwise valid audio conversion.
7. Preserves the existing untagged converter entry point for compatibility with lower-level tests/callers.
8. Added unit coverage for ID3v2.3 framing, syncsafe tag sizing, Unicode fields, playlist track metadata, source URL, publish date, supported artwork, and oversized/unsupported artwork exclusion.
9. Updated the server documentation.

No new runtime dependency, CGO dependency, or FFmpeg requirement was introduced.


### P1.3 M4A “original audio” mode

**Status:** [x] Complete

Implemented:

1. Added optional per-job `audioFormat` with `mp3` as the backward-compatible default and `m4a` as the zero-transcode option.
2. Added SQLite migration v9 and retry persistence for the selected audio format.
3. Added direct AAC-in-MP4 transfer to finalized `.m4a` output without decode/re-encode.
4. Preserved the source stream bytes exactly in the M4A path while retaining existing size limits, progress reporting, atomic `.part` finalization, storage policy, output publishing, and restart semantics.
5. Added `.m4a` to managed-file validation and published-output validation.
6. Added per-item and batch audio-format controls to inspection UX.
7. MP3 bitrate controls now appear only for MP3 jobs; M4A requests do not send meaningless bitrate values.
8. Queue and Library labels distinguish tagged MP3 from M4A original AAC.
9. Added backend coverage proving M4A output bytes match the source bytes and API validation rejects unsupported audio formats.
10. Added frontend coverage proving the M4A request sends `audioFormat:"m4a"` without `audioBitrate`.
11. Updated API documentation.

This path remains limited to compatible AAC-in-MP4 audio streams already selected by the native YouTube format selector; it does not introduce Opus/WebM conversion or FFmpeg.


### P1.4 Library folder/category navigation

**Status:** [x] Complete

Implemented:

1. Added category and channel facets derived from persisted finalized-file metadata.
2. Added file counts per category/channel in filter options.
3. Expanded Library search across job title, source URL, category, audio format, file title, creator/channel, filename, published filename, and relative output path.
4. Added logical media, managed-copy, and published-copy storage summaries so users can see the effect of storage policy and scoped deletion.
5. Added visible-job counts and a one-click filter reset.
6. Preserved existing video/audio filtering and filesystem actions alongside the new facets.
7. Persisted the user's Grid/List Library preference in local browser storage.
8. Added UI regression coverage for category/channel filtering, storage summaries, and layout persistence.

The facet implementation remains frontend-derived from SQLite-backed job/file metadata; no duplicate category or channel index is introduced in the database.


### P1.5 Local media preview

**Status:** [x] Complete

Implemented:

1. Extended the existing random file-capability ticket model with an `inline` preview scope rather than exposing filesystem paths or bearer tokens in media URLs.
2. Inline tickets require a specific managed `fileId`, cannot represent ZIP archives, and expire after 30 minutes; ordinary download tickets remain five-minute attachment tickets.
3. Reused the existing validated managed-file access path and HTTP single-range support so browser media controls can seek efficiently without loading the full file into JavaScript memory.
4. Inline responses use the finalized media MIME type and `Content-Disposition: inline`.
5. Added a Library Preview action for managed media and a modal supporting browser-compatible video and audio.
6. Preview uses `preload="metadata"`, standard controls, and never autoplays.
7. Managed-copy removal disables preview automatically; no new arbitrary-path API was introduced.
8. Added backend tests for inline ticket validation, MIME/disposition, and `206` byte-range behavior.
9. Added frontend coverage for inline ticket creation, modal rendering, media URL scoping, no-autoplay behavior, and close behavior.
10. Updated API documentation.

Published-only jobs intentionally do not stream their user-owned output back through the app preview API; those files remain available through native Open/Reveal actions, while in-app preview requires a managed copy.


### P1.6 Playlist item selection before queueing

**Status:** [T] Implemented; CI validation pending

Implemented the playlist subset contract end to end: inspection selection UX, original-index persistence, fresh-metadata revalidation, contiguous scheduler indexes, original-position output naming/MP3 track metadata, retry/restart durability, and regression coverage. No new schema migration was required because queue items are already stored as JSON.
