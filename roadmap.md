# yt-dl-go Product Roadmap

> Durable roadmap for the `yt-dl-go` product. This file is the source of truth for roadmap scope, sequencing, implementation status, acceptance criteria, and follow-up work.
>
> **Branch:** `roadmap/cross-platform-packaging-v1`
>
> **Last updated:** 2026-09-21

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

**Status:** [x] Complete

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
- Validation: CI run `35551454898` passed frontend type-check/build, Bun integration tests, Go tests, Go vet, and Windows production build.

### P1.7 Queue reordering and priority

**Status:** [x] Complete

Implemented explicit, durable queue ordering using the repository's existing `@dnd-kit` dependencies.

#### Implemented

- Added persisted per-job `queuePosition` with SQLite migration v10.
- Converted the queue channel into a scheduler wake signal; the scheduler now starts the lowest-position queued job.
- Added atomic `PUT /api/queue/order` exact-set reordering for queued jobs.
- Added `POST /api/jobs/{id}/next` for an explicit “Download next” priority action.
- Added `PUT /api/jobs/{id}/items` for exact-set playlist item reordering using durable original playlist indexes.
- Reordered playlist items keep contiguous runtime queue indexes while original playlist indexes continue to drive filenames and MP3 track metadata.
- Queue order and playlist item order persist across restart.
- Resumed paused jobs join the end of the queued-job order.
- Reorder APIs reject active/non-queued work, so already-active downloads are never moved unexpectedly.
- Added nested `@dnd-kit` sortable surfaces for queued jobs and queued playlist items, with keyboard-accessible drag handles.
- Added backend regression coverage for scheduler priority, “Download next,” queue persistence, and playlist item-order persistence.
- Added frontend coverage for persisted queue rendering, drag handles, and “Download next.”
- Validation: CI run `35551920569` passed frontend type-check/build, Bun integration tests, Go tests, Go vet, and Windows production build.

### P1.8 Retry one playlist item

**Status:** [x] Complete

A single failed or cancelled playlist entry can now be retried without re-running the successful playlist entries.

#### Implemented

- Added durable `POST /api/jobs/{id}/retry-item` with a one-based queue `index`.
- Endpoint is limited to stopped playlist jobs and failed/cancelled queue rows.
- Retry intent is persisted on the queue item through existing `queue_items` JSON.
- Worker detects retry mode and executes only the marked playlist item.
- Valid finalized sibling files remain intact, including their existing file IDs.
- Successful sibling items are skipped rather than re-downloaded.
- Non-target failed/cancelled sibling state is preserved during a targeted retry.
- Original `playlistIndex` still drives output naming and MP3 track metadata.
- Targeted retry state survives restart and resumes as a targeted retry.
- Retry intent is cleared after the job reaches a terminal state.
- Queue UI exposes a per-row **Retry item** action only where the backend contract allows it.
- Backend regression coverage verifies isolated retry execution, sibling-file preservation, invalid completed-item rejection, and restart persistence.
- Frontend regression coverage verifies the row-level action and request payload.
- Validation: CI run `35552251892` passed frontend type-check/build, Bun integration tests, Go tests, Go vet, and Windows production build.

### P1.9 Bandwidth limiting

**Status:** [x] Complete

Optional application-level bandwidth control is now available for long-running batch jobs.

#### Implemented

- Added persisted `bandwidthLimitBytesPerSec` application setting with SQLite migration v11.
- `0` remains the backward-compatible and UI-default unlimited mode.
- Added a 1 GiB/s validation ceiling and rejects negative limits.
- Added one shared FIFO token bucket for all active Go-managed media transfers.
- Each transfer receives a bounded grant and re-enters at the back of the queue, providing round-robin fairness under contention.
- Progressive video, original M4A, MP3 source downloads, adaptive range downloads, and adaptive audio-source downloads all use the shared limiter.
- Local transcoding/muxing and local published-file copies are not throttled because the setting represents inbound download bandwidth.
- Browser-assisted adaptive capture is bypassed while limited because Chrome network traffic cannot be accurately accounted by the Go limiter; the existing Go range path is used instead.
- Settings changes update the live limiter immediately, including active transfers.
- Added Settings UI in MiB/s with `0 = Unlimited`.
- Added deterministic limiter tests for FIFO fairness, cancellation, and switching to unlimited without timing-dependent throughput assertions.
- Added API validation, SQLite restart-persistence, and frontend preference coverage.
- Validation: CI run `35553564877` passed frontend type-check/build, Bun integration tests, Go tests, Go vet, and Windows production build.

---

# Milestone 4 — Real-time UX and desktop polish

## P1.10 Server-Sent Events for job updates

**Status:** [x] Complete

The UI now uses authenticated Server-Sent Events for near-real-time job/settings updates instead of polling the full job list approximately every 1.8 seconds.

#### Implemented

- Added a bounded in-process event broker with monotonically increasing event IDs.
- Added authenticated `GET /api/events` using `text/event-stream`.
- Every connection receives an initial `snapshot` with current jobs and persisted settings.
- Emits `job-created`, throttled `job-progress`, `job-status`, `job-file-finalized`, `job-error`, `job-deleted`, and `settings-changed`.
- Progress events are throttled per active item to avoid turning transfer callbacks into an event flood.
- Added 20-second heartbeat comments for idle connections.
- Slow clients use a bounded buffer; an overloaded client drops older state rather than blocking download workers.
- Frontend consumes SSE through authenticated `fetch`, preserving bearer-header support that native `EventSource` cannot provide.
- The previous 1.8-second polling loop is removed; a 30-second full-job reconciliation remains as repair/fallback for dropped events.
- Stream closure/protocol failure triggers automatic reconnect with visible degraded-state messaging.
- SSE streams are exempt from the normal 30-second response write deadline; a regression test protects long-lived connections.
- Added backend coverage for authentication, framing, initial snapshot, job-created delivery, and streaming lifetime.
- Added frontend coverage proving live progress is applied without repeated full-list polling.
- Validation: CI run `35555097968` passed frontend type-check/build, Bun integration tests, Go tests, Go vet, and Windows production build.

### P2.1 System notifications

**Status:** [x] Implemented

The browser UI already contained the full notification path; this roadmap item was stale documentation/status rather than missing product behavior.

Implemented behavior:

- persisted `notificationsEnabled` preference in SQLite-backed application settings
- explicit opt-in toggle in Settings
- notification permission is requested only when the user enables the feature
- permission denied/unavailable states are surfaced in the UI without breaking download behavior
- live SSE job transitions are compared against the prior known job status so initial hydration/snapshots do not emit duplicate alerts
- completed single downloads emit **Download completed**
- completed playlists/batches emit **Batch completed**
- failed jobs emit **Download failed**
- partially completed playlists emit **Playlist partially completed** with completed/total counts
- storage/output/disk/filesystem failures receive the higher-priority **Download storage error** notification
- notification tags include job ID + terminal status to keep OS/browser deduplication scoped to the transition

Scope note: these are browser/OS Notification API alerts and therefore require the UI/browser session to remain open. Native background notification delivery is intentionally not part of P2.1; that concern belongs with P2.2 tray/background mode.

Validation coverage includes permission gating/persistence, suppression of initial snapshot notifications, live terminal-transition delivery, and direct assertions for all four roadmap notification classes.

### P2.2 Background mode / native tray investigation

**Status:** [x] Investigation complete; background mode implemented, native tray deferred

The existing service already supported headless startup through the internal `NO_BROWSER=1` environment switch. This work promotes that capability into a documented end-user runtime mode without changing the single-executable architecture.

#### Implemented

- Added supported `--background` and `--no-browser` runtime arguments.
- Both modes suppress automatic browser launch while leaving the local HTTP service, scheduler, and download workers running normally.
- The process remains foreground-owned rather than silently daemonizing; Ctrl+C / normal process signals retain the existing graceful shutdown and resumable-job behavior.
- The service logs the browser UI URL so the user can open it manually at any time.
- `NO_BROWSER=1` remains supported for CI and existing automation.
- Unknown command-line arguments are rejected instead of being silently ignored.
- Added platform-independent regression coverage for default browser launch behavior, both runtime aliases, legacy environment compatibility, and unknown-argument rejection.
- Updated root/backend/packaging documentation.

#### Native tray decision

A native tray was investigated rather than adopted blindly. The strongest current zero-CGO candidate is `github.com/gogpu/systray` v0.3.0 (released August 30, 2026), which supports Windows, macOS, and Linux and builds with `CGO_ENABLED=0`. That makes it architecturally compatible with the project's single-binary packaging model.

It is **not** being added to production yet. As of September 21, 2026, the upstream project still has open correctness work around menu dispatch while a menu is being rebuilt and macOS double-click dispatch, with fixes pending review. A tray integration would also need real desktop-session validation beyond ordinary headless package builds.

Revisit native tray UI when:

1. the relevant upstream fixes are released and stable;
2. Windows/macOS/Linux tray creation + menu actions can be exercised on real desktop runners or hardware;
3. adding the dependency does not regress P2.6 CGO-free packaging;
4. background notifications can be designed coherently with P2.1 rather than duplicating browser notifications.

Until then, the supported background/no-browser mode provides long-running batch operation without introducing a fragile desktop shell dependency.

### P2.3 Release metadata, update notification, and automatic updater

**Status:** [~] Safe release foundation implemented; automatic self-update deferred

#### Implemented

- immutable build metadata (`version`, source `commit`, `buildDate`) exposed through `/api/health`
- release metadata injected by both Windows and Unix build scripts; local/branch builds remain `dev`
- bounded authenticated `GET /api/update` latest-release discovery
- development builds skip external update checks entirely
- stable semantic-version comparison and draft/prerelease rejection
- strict release-link allowlist for this repository's GitHub Releases
- Settings UI showing current build metadata, latest stable version, and update-available link
- update discovery is non-blocking and never affects downloader/service readiness
- tag-driven `vMAJOR.MINOR.PATCH` release workflow
- full source/test/browser validation before release packaging
- Windows, Linux amd64/arm64, and macOS amd64/arm64 release packages
- individual SHA-256 files plus canonical `SHA256SUMS.txt`
- GitHub OIDC build-provenance attestations for release archives and checksum manifest
- unit coverage for semantic versions, development-build network suppression, response-size limits, stable-release validation, unsafe URL rejection, and development API metadata
- frontend integration coverage for build metadata and update-available UI

#### Intentionally deferred

Automatic download/replacement is **not** implemented. GitHub provenance attestations are signed supply-chain evidence but are not substitutes for Windows Authenticode or Apple Developer ID/notarization.

Before self-update can be enabled, require:

1. OS-native signing/notarization credentials and verification;
2. checksum plus signature/provenance verification of the downloaded platform artifact;
3. atomic replacement semantics for Windows/Linux/macOS;
4. preservation of the known-good previous executable;
5. restart health verification and automatic rollback;
6. explicit user update policy/opt-in and actionable recovery errors.

Until those conditions are satisfied, an available update opens the validated GitHub Release for manual installation rather than modifying the running executable.

#### Validation

CI run `35659497292` passed frontend type-check/build, Bun integration tests, Go tests, Go vet, real-browser E2E, Windows production build/package/upload, Linux amd64 + arm64 packages, and macOS amd64 + arm64 packages. This run includes the release-metadata linker injection path on every platform build script.

The tag-only `.github/workflows/release.yml` is intentionally not executed by branch CI; it reuses the same validated build scripts and adds stable-tag validation, package checksum verification, GitHub OIDC provenance attestations, canonical `SHA256SUMS.txt`, and GitHub Release publication.

---

# Milestone 5 — Format expansion

## P1.11 Subtitle/caption extraction

**Status:** [x] Implemented

Add opt-in subtitle/caption download for owned/authorized content.

#### Implemented scope

- Video inspection enumerates validated YouTube caption tracks and exposes language labels plus auto-generated/manual status to the UI.
- Download drafts can opt into one caption language and choose WebVTT or SubRip (SRT); no caption is downloaded unless explicitly selected.
- Manual captions are preferred over auto-generated ASR when YouTube exposes both for the same language.
- WebVTT is fetched through the guarded native HTTP path with a strict HTTPS YouTube timed-text allowlist, redirect rejection, a 5 MiB response ceiling, and no signed caption URL persistence.
- SRT conversion is implemented in-process in Go; no FFmpeg or other caption binary is required.
- Caption sidecars are stored beside the managed media name and, when output publishing is enabled, beside the published media using the same resolved output basename.
- Managed job ZIP downloads include finalized caption sidecars.
- Playlist inspection samples an accessible item for caption language choices; the worker resolves the selected language independently for each item. Missing or failed captions are recorded as per-file warnings and do not discard otherwise valid media.
- Caption configuration and finalized sidecar metadata persist through SQLite schema migration v13 and are retained by retry/restart flows.
- Managed/published deletion semantics, storage accounting, restart byte-budget accounting, Library search/stats/status badges, and health capability reporting include caption sidecars.
- Backend tests cover language/URL validation, manual-vs-ASR selection, WebVTT-to-SRT conversion, publishing, ZIP inclusion, and unsafe language rejection. Frontend integration coverage verifies caption selection and job payloads.

#### Validation

- CI run `35559277729` passed frontend type-check/build, Bun integration tests, Go tests, Go vet, the Windows production build, and Windows artifact upload.

## P2.4 1440p and 2160p support

**Status:** [x] Implemented

This is an engineering project, not merely a quality-dropdown change. YouTube commonly uses VP9/AV1 video and Opus audio for higher resolutions, so the implementation adds an explicit codec/container strategy instead of pretending every quality can remain H.264/AAC MP4.

#### Implemented

1. Added `1440` and `2160` quality ceilings across the API, persisted settings, inspection metadata, TypeScript quality model, and UI labels. Quality remains a ceiling rather than an upscale guarantee.
2. Preserved the existing compatibility-oriented H.264 + AAC adaptive MP4 path through 1080p.
3. Added adaptive WebM selection for VP9 and AV1 video with Opus audio. At equal height/frame rate VP9 is preferred for broader native decoder compatibility; AV1 remains available when it is the better or only high-resolution representation.
4. Added a streaming, pure-Go WebM remuxer using `github.com/at-wat/ebml-go v0.19.3`. Separate YouTube video/audio WebMs are parsed incrementally and timestamp-interleaved into seekable WebM without decoding, transcoding, CGO, FFmpeg, or loading the complete media into memory.
5. Preserved Opus codec-private metadata, codec delay, seek pre-roll, video dimensions, keyframe flags, and timestamps in finalized WebM output.
6. Extended browser-assisted adaptive authorization so playback targeting is codec-aware for H.264, VP9, AV1, and Opus rather than globally forcing H.264.
7. High-resolution video and audio tracks use the existing resumable bounded-range downloader and browser fallback. Both tracks are independently completion-verified before remuxing.
8. Added safe fallback semantics: if adaptive high-resolution acquisition or WebM validation/remuxing fails and a verified progressive MP4 exists, the job falls back to that complete file and records the downgrade in the job note instead of returning corrupt or partial media.
9. Updated storage budgeting for separate WebM video + Opus audio sources.
10. Added unit coverage for 1440p VP9 selection, 2160p AV1 selection, VP9-over-AV1 tie-breaking, codec-aware browser policy, WebM storage estimates, VP9/Opus remuxing, track metadata preservation, frame counts, and rejection of unsupported WebM codecs.
11. Added the `ebml-go` Apache-2.0 license and third-party notice. Windows/Linux/macOS packaging now includes the complete `licenses/` directory so packaged notice links remain valid.
12. Updated root/server documentation to describe 1440p/4K behavior, the MP4/WebM strategy, browser requirements, fallback semantics, and the expanded quality values.

#### Codec/container policy

| Requested path | Video | Audio | Final container |
| --- | --- | --- | --- |
| Progressive compatibility | Source combined stream | Source combined stream | MP4 or WebM |
| Adaptive compatibility | H.264 | AAC | MP4 |
| Adaptive high resolution | VP9 preferred at equal resolution; AV1 supported | Opus | WebM |

P2.5 remains the place for user-selectable codec/container preferences such as explicit AV1 preference or compatibility-only output. P2.4 keeps those choices automatic.

#### Validation

- PR CI run `35617803735` passed frontend type-check/build, Bun integration tests, Go tests, Go vet, real-browser E2E, Windows production build/package/upload, Linux amd64 + arm64 production package/upload, and macOS amd64 + arm64 production package/upload.
- Earlier CI iterations exposed and fixed: the missing module checksum, a generated browser codec-script compile error, an interleaved WebM-reader test deadlock, YouTube JSON `contentLength` fixture encoding, the legacy test that treated 2160p as unsupported, and a pre-existing Windows timing limit in the 151-item playlist fixture. The Windows fix changes only test timeout/wait behavior, not production scheduler semantics.
- The opt-in live-download harness already accepts `YTDL_LIVE_MIN_HEIGHT=1440` or `2160` for authorized real-media verification. It remains intentionally outside normal CI because it depends on current YouTube delivery behavior and downloads real media.

### P2.5 Advanced codec/container selection

**Status:** [x] Implemented

Added an explicit video output-strategy model on top of the validated P2.4 high-resolution codec/container pipeline.

#### User-facing strategies

- **Best quality · automatic** — preserves the P2.4 automatic selection policy and chooses the highest supported representation under the selected quality ceiling.
- **Compatibility MP4 · H.264/AAC** — strict MP4 mode. It selects only progressive H.264/AAC MP4 or adaptive H.264 video + AAC audio and never silently emits WebM.
- **Prefer VP9 · WebM** — selects VP9 + Opus when available under the quality ceiling; otherwise falls back to the automatic best-supported representation and records that fallback in the job note.
- **Prefer AV1 · WebM** — selects AV1 + Opus when available under the quality ceiling; otherwise falls back to the automatic best-supported representation and records that fallback in the job note.
- **Original audio** remains the existing **Audio only → M4A · original AAC** workflow rather than being duplicated as a video-format option.

#### Persistence and API behavior

1. Added durable `videoStrategy` job state and `defaultVideoStrategy` application settings.
2. Added SQLite schema migration **V14** for `jobs.video_strategy` and `app_settings.default_video_strategy`, both defaulting to `best` for existing databases.
3. New video jobs capture the explicit strategy or inherit the current default; audio-only jobs reject video strategies.
4. Whole-job retries preserve the original strategy.
5. Existing stored jobs/settings hydrate safely to `best` when no strategy was previously recorded.
6. Strategy is independent from the quality ceiling: for example, `2160 + compatibility` can legitimately produce 1080p MP4 when no higher H.264/AAC representation exists.
7. The automatic `best` policy retains the P2.4 safety invariant that adaptive H.264/AAC MP4 participates automatically only when the verified progressive MP4 fallback required by that path exists. Explicit Compatibility MP4 may attempt the strict adaptive MP4 pair because the user explicitly selected that policy.

#### UI

- Added **Video format** to each inspected video item.
- Added a batch **Apply video format to all** control.
- Added **Default video format** to Settings.
- Queue rows display both the quality ceiling and selected strategy.
- Context text explains strict MP4 behavior and VP9/AV1 preference fallback semantics without exposing raw itags, signed URLs, or codec implementation details.

#### Tests

Coverage includes:

- strict Compatibility MP4 selection
- VP9 preference selection
- AV1 preference selection
- preferred-codec fallback marking
- invalid strategy rejection
- automatic P2.4 policy compatibility
- default-setting validation and persistence
- SQLite restart persistence for jobs/settings
- audio-only strategy rejection
- whole-job retry preservation
- per-item and batch UI controls
- non-default strategy submission from the UI
- real-browser default strategy save + service-restart persistence

#### Validation

CI run `35623299491` passed:

- frontend type-check/build
- Bun integration tests
- Go tests
- Go vet
- real-browser E2E
- Windows production build/package
- Linux amd64 + arm64 production packages
- macOS amd64 + arm64 production packages


---

# Milestone 6 — Architecture and maintainability

## P1.12 Decompose `src/pages/home.tsx`

**Status:** [x] Implemented

The production page previously owned service initialization, polling, queue state, inspection, settings, library state, job actions, and nearly all rendering in one ~1,479-line file.

#### Implemented structure

```text
src/
  pages/
    home.tsx
    queue.tsx
    library.tsx
    settings.tsx
  components/downloader/
    view-model.tsx
  hooks/
    use-service.ts
    use-jobs.ts
    use-settings.ts
```

- `queue.tsx` owns add/inspect controls, batch controls, queue filters, queue rendering, and drag/drop presentation.
- `library.tsx` owns Library filtering/layout, file cards, preview/save/filesystem controls, and scoped-delete presentation.
- `settings.tsx` owns service status and all preference/output configuration rendering.
- `use-service.ts` owns backend bootstrap, SQLite hydration, SSE updates, reconciliation, readiness/error state, and terminal notifications.
- `use-jobs.ts` owns queue/job mutations, retries, preview/save tickets, filesystem actions, queue/playlist reordering, batch actions, and cleared-completed persistence.
- `use-settings.ts` owns settings mutation/save behavior, folder selection, notification permission flow, and user-category management.
- `view-model.tsx` centralizes downloader-specific view types, labels, formatting/selection helpers, thumbnail loading, and the sortable queue row.
- `home.tsx` is reduced to roughly 450 lines and primarily coordinates derived queue/library state, inspection submission, top-level navigation/messages, and page composition.
- Existing DOM labels/test selectors were intentionally preserved so the refactor remains behavior-compatible.

#### Validation

- CI run `35565686299` passed frontend type-check/build, Bun integration tests, Go tests, Go vet, the Windows production build, and Windows artifact upload.

## P1.13 Frontend dependency cleanup

**Status:** [x] Implemented

Audited the production frontend graph after P1.12 and removed the generated scaffold that was no longer part of the downloader application.

Implemented:

1. Reduced direct runtime dependencies to React/ReactDOM, React Router, Tailwind, Geist, Lucide, and the three DnD packages used by the queue UI.
2. Removed unused QueryClient, MotionConfig, Sonner, Radix confirmation-provider, Zustand placeholder-store, and generated shadcn-style UI layers from the production shell.
3. Deleted 55 orphaned scaffold/helper files that were not reachable from the downloader entrypoint.
4. Removed unused TanStack/managed-app aliases and dependency pre-bundling from Vite.
5. Synchronized npm and Bun lockfile root dependency manifests.
6. Preserved router/error-boundary/preview-diagnostic behavior and the downloader's existing DOM/test contract.

Validation note: CI run `35566160345` passed frontend type-check/build, Bun integration tests, Go tests, Go vet, the Windows production build, and Windows artifact upload.

## P1.14 Browser end-to-end testing

**Status:** [x] Implemented

Added a real-browser E2E layer using headless Chrome plus the Chrome DevTools Protocol, avoiding another simulated DOM layer or a heavyweight browser-test runtime dependency.

Coverage:

1. starts the actual Go executable against a private temporary data directory
2. inspects a deterministic test-only YouTube fixture URL
3. creates a real queued download through the browser UI
4. pauses and resumes the live fixture stream through UI controls
5. waits for real worker completion
6. verifies the completed item appears in Library
7. changes and saves settings through the UI/API
8. restarts the Go service against the same SQLite/data directory and verifies settings + Library persistence after browser reload
9. verifies destructive “Delete everywhere” opens an explicit browser confirmation before mutation and removes history only after acceptance

Implementation details:

- `scripts/browser-e2e.mjs` launches a tagged E2E service binary and drives installed Chrome/Chromium over CDP.
- `src/server/e2e_fixture.go` is compiled only with the `e2e` build tag and supplies deterministic media metadata/bytes.
- `src/server/e2e_fixture_disabled.go` makes the production build incapable of enabling the fixture through environment variables.
- `NO_BROWSER=1` supports headless/CI startup without invoking the OS URL handler.
- CI runs `npm run e2e:browser` after the fast frontend/Go test layers.
- The first browser pass exposed and fixed a real custom-loopback-port bug: Vite's `crossorigin` asset requests carried the listener origin, but only the hard-coded 5173/8080 origins were accepted. Loopback listeners now automatically trust their own exact HTTP origin at the configured port while network-visible listeners still require explicit allowlists.
- Later full-matrix runs exposed a lifecycle ordering race: the asynchronous worker could publish `paused` over SSE before the Pause HTTP acknowledgement returned its older `downloading` snapshot, allowing the UI to regress to stale state. Job-action merging now preserves newer SSE lifecycle states for pause/cancel/resume, and Go coverage exercises pause during an active stream read.

Validation note: CI run `35601658829` passed frontend type-check/build, Bun integration tests (including stale-action state regression coverage), Go tests/vet (including active-stream pause/resume), and the full real-Chrome E2E scenario.

## P2.6 Cross-platform packaging

**Status:** [x] Implemented

The CGO-free Go architecture now has reproducible packaging paths for Windows, Linux, and macOS.

Implemented:

1. Added `scripts/build-unix.sh` to rebuild the embedded React UI, run native-host Go tests/vet, cross-build a requested Linux/macOS architecture with `CGO_ENABLED=0`, and create a tar.gz + SHA-256 checksum.
2. Added `scripts/package-windows.ps1` to package the checked Windows executable with README/third-party notices and produce a ZIP + SHA-256 checksum.
3. Expanded CI packaging to Windows plus Linux `amd64`/`arm64` and macOS `amd64`/`arm64` artifacts.
4. Kept Chrome/Chromium external: browser-assisted HD uses chromedp platform discovery or the existing explicit `CHROME_PATH` override.
5. Refactored browser-launch, native-folder-picker, and reveal/open command selection into testable platform helpers.
6. Added platform-independent tests covering Windows, macOS, and Linux command semantics.
7. Documented Linux's intentional reveal fallback: `xdg-open` opens the containing folder because freedesktop environments do not provide one portable file-selection command.
8. Documented Linux's `zenity` dependency for the native **Browse** button; manual absolute-path entry remains available without it.
9. Added `docs/PACKAGING.md` covering package contents, architectures, runtime integration, release promotion, and signing/notarization requirements.
10. Defined signing as a protected release-stage concern rather than ordinary branch CI: Windows Authenticode and macOS Developer ID/notarization require credentials that must not be stored in the repository.

Validation note: CI run `35601658829` passed the full source/test/browser gate plus Windows production build/package, Linux `amd64`/`arm64` packages, and macOS `amd64`/`arm64` packages. Every configured package artifact and checksum upload completed successfully.

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

**Status:** [x] Complete

Implemented the playlist subset contract end to end: inspection selection UX, original-index persistence, fresh-metadata revalidation, contiguous scheduler indexes, original-position output naming/MP3 track metadata, retry/restart durability, and regression coverage. No new schema migration was required because queue items are already stored as JSON.
