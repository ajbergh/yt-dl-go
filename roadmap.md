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

**Status:** [~] In progress

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

**Status:** [ ] Planned

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

**Status:** [ ] Planned

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

**Status:** [ ] Planned

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

**Status:** [ ] Planned

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

**Status:** [ ] Planned

Remote thumbnail URLs are not durable library metadata.

#### Scope

- Download and validate the selected YouTube thumbnail.
- Store a managed thumbnail with the media record.
- Optionally publish sidecar artwork next to output media.
- Serve local thumbnails to the UI when available.
- Keep remote URLs as fallback metadata only.

### P1.2 MP3 ID3 metadata and artwork

**Status:** [ ] Planned

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

**Status:** [ ] Planned

When YouTube exposes AAC-in-MP4 audio, users should be able to preserve it without transcoding.

#### Proposed options

- M4A — source AAC, no MP3 transcode
- MP3 — 128 / 192 / 256 / 320 kbps

Benefits: lower CPU use, faster completion, and no additional lossy generation.

### P1.4 Library folder/category navigation

**Status:** [ ] Planned

Promote the Library from a job-results list to a true local media browser.

#### Scope

- Category/channel sidebar or filter panel.
- File count and storage-size summaries.
- Grid/list views with equivalent capabilities.
- Search title, creator, category, filename, and playlist.
- Open/reveal/copy-path actions.
- Persistent user layout preference.

### P1.5 Local media preview

**Status:** [ ] Planned

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

**Status:** [ ] Planned

Playlist inspection should allow users to choose which exposed items are queued.

#### Scope

- Expandable playlist inspection.
- Select all / clear all.
- Per-item checkboxes.
- Selected item count.
- Approximate aggregate size when estimates are available.
- Preserve original playlist index for output naming and metadata.

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

**Status:** [~] In progress

Planned implementation sequence:

1. Extend frontend inspection draft state with `category`.
2. Add a category selector to each inspection result.
3. Send `category` in `POST /api/jobs`.
4. Extend the create-job request contract with optional `category`.
5. Validate category against persisted configured categories.
6. Use default category when omitted for backward compatibility.
7. Capture category on the job before output publication.
8. Update backend/API/frontend tests.
9. Update docs and this roadmap.
