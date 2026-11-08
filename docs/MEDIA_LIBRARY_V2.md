# Media Library 2.0 — Implementation notes

Branch: `feature/media-library-v2`
Design target: media-first, dark, Plex-inspired desktop library.

## Delivered in first iteration

- The Library now displays **individual downloaded media files**, not download jobs with nested file lists.
- Responsive 16:9 grid and compact list view share the same loaded media data.
- Browse by All, Videos, Audio, Favorites, or files added within the last 30 days.
- Existing server-side search, type, category, and channel filters remain wired to the Go API.
- Sort loaded files by date, title, size, or duration; layout and sort preference persist.
- A context-sensitive inspector provides native preview, reveal, copy path, open folder, per-file **forget from index**, and confirmed **delete entire job files** actions.
- The inspector uses actual file metadata without fabricated descriptions, subscribers, or disk quotas.
- Audio entries have distinct icons and graceful artwork fallback.
- Local authenticated thumbnails retain the existing API and now support larger presentation with browser image lazy-loading.
- Existing Queue, Settings, download logic, Wails integration, and server schema were not changed.

## Important semantics

**Delete entire download** invokes the existing job-level endpoint and deletes all files associated with that download job. **Delete selected file** uses a separate validated endpoint to remove only the selected file and its associated caption sidecar. Both actions require explicit confirmation.

**Forget item** uses the preexisting single-file library-index removal endpoint. It leaves the downloaded file and job history on disk.

Favorites and collections are now stored in the **SQLite state database (migration v32)**, using a versioned organization document. A legacy `localStorage` compatibility cache remains, and old local preferences are migrated when the database organization document is empty and at revision zero. They are included in portable Library ZIP v2 metadata exports/imports.

The new Library uses a separate `/api/library/files` endpoint with **50 individual media files per page**, global SQLite sorting, and server-wide type/category/channel/search/favorites/collection filters. The legacy `/api/library` job-grouped endpoint remains available for older clients. Click **Load more** to append the next 50 files. Category/channel facets and total storage statistics remain server aggregates; the sidebar media-type counts and playlist discovery buttons indicate the loaded set.

## Follow-up implementation (2026-11-08)

- Fixed the browser E2E expectation to wait for the new **Media Library** heading instead of the removed "Download library" heading.
- Added source-playlist browsing and channel facet navigation. Playlist browsing operates on loaded pages, and channel filtering still delegates to the existing server API.
- Implemented local user collections with create, rename, remove, and add/remove media membership. Deleting a collection does **not** delete any downloaded files. The collection model validates persisted data, deduplicates membership, and rejects case-insensitive duplicate names.
- Added narrow-window Library navigation and a collection-creation control.
- Added targeted frontend regression tests for collection behaviors, playlist browsing, and collection model validation.
- Canonical scope: [complete master prompt](YOUTUBE_VAULT_MEDIA_LIBRARY_V2_MASTER_PROMPT.md). Delivery and verification status: [tracking document](MEDIA_LIBRARY_V2_TRACKER.md).

**Persistence improvement:** favorites and collections use an authenticated `GET/PUT /api/library/organization` endpoint backed by SQLite. Updates use a revision compare-and-swap to avoid silently overwriting another window. Stale updates return 409 with a reload option in the UI. Existing per-browser values migrate on first use if no server state exists. Library ZIP v2 now exports and restores portable organization metadata; older v1 ZIP archives are still accepted.

## SQLite organization persistence (2026-11-08)

- `src/server/library_organization.go`: versioned, strictly validated database document for favorites and collections.
- Migration v32 installs the organization table without modifying downloaded files or job history.
- `GET /api/library/organization` and `PUT /api/library/organization` use existing bearer authorization; PUT has a 512 KiB request-body limit and returns HTTP 409 on a stale revision.
- `src/hooks/use-library-organization.ts`: on opening the Library, loads canonical state from SQLite, migrates legacy local preferences when safe, and serializes edits to avoid same-window races.
- Tests cover backend restart persistence and stale/invalid writes, plus frontend hydration across remounts.
- Browser E2E assertions were updated for redesigned heading and empty-Library text. Confirm latest CI outcomes separately before merging.

**Remaining limitations:** UI membership/count filters remain limited to loaded pages. Revision conflicts require manual reload; automatic multi-window change broadcasting is not implemented.

## Safe single-file deletion (2026-11-08)

- Added a separate authenticated `DELETE /api/jobs/{jobID}/library-files/{fileID}` endpoint in `src/server/library_file_delete.go`.
- This action verifies the selected published file (and tracked caption sidecar, if present) under a rooted filesystem handle, rejects changed files and symbolic links, and refuses operations while the job has active readers.
- The endpoint removes the **selected file only**, then excludes that item from the Library index while leaving siblings and job history intact. Existing private thumbnails and working copies for that file are cleaned up using current helpers.
- The Library inspector now offers **Delete selected file** and **Delete entire download** as distinct actions. The selected-file action has its own irreversible-deletion confirmation showing the media title.
- Added Go regression tests for two-file playlists, restart/history consistency, modified/symlinked files, unknown file IDs and unauthorized access. Added frontend confirmation/routing test and updated the browser E2E selector for the newly labeled job-wide action.

**Follow-up:** Review partial filesystem failures, UI feedback, and native-window tests. The selected-file deletion phase passed source/desktop CI on head `e795208`.

## Portable Library ZIP v2 organization metadata (2026-11-08)

- A v2 `library.json` document now includes `organization.favorites` and `organization.collections` in addition to portable media records; the private `state.db` backup remains unchanged. Exports omit dangling favorites/memberships not represented by the exported media records, but preserve empty collections.
- Imports still accept **v1** archives without organization metadata. V2 import validates names, IDs, and limits and remaps saved source `jobId:fileId` references to the deterministic imported media IDs, so favorites and collections point to the new Library records instead of the original device IDs.
- Existing favorites are kept; collections with equal case-insensitive names merge memberships; collection ID collisions receive deterministic new IDs. Import does not move, overwrite, or delete media files, settings, or download history.
- Library metadata and organization changes run in **one SQLite transaction**. Conflicts or invalid merged organization abort the entire import. Repeated imports are idempotent, including organization revision tracking.
- Settings now reports when favorites/collections were restored. Go coverage includes export filtering, round-trip ZIP restore, merge/collision semantics, v1 compatibility, duplicate/invalid metadata rejection, and rollback on collection-limit errors.

**Known limitations:** ZIP import adds metadata-only media entries without copying files. The restored media remains unavailable until downloaded again. Organization data is portable, but there is no direct reconciliation of imported media with existing local media whose IDs differ. Global media filtering is supported by `/api/library/files`; full grid virtualization remains future work.

## Safe loaded-page multi-selection and thumbnail scheduling (2026-11-08)

- Card selection now supports plain click, Ctrl/Cmd-click toggling, Shift-click ranges, Ctrl/Cmd+A (visible loaded items only), arrow navigation, and Escape to clear a selection. The toolbar exposes an explicit **Select all loaded** action; it does not silently select unseen paginated media.
- Selected items can be favorited/unfavorited or added/removed from a collection in one revision-checked organization update. Favorite and collection membership limits remain enforced.
- **Forget selected (keep files)** requests one native confirmation, removes at most 100 individual Library index entries through existing authenticated per-file endpoints, and refreshes the Library once. It reports successes and failures separately and leaves failed entries selected for retry. Actual media files and job history remain untouched.
- **No bulk permanent delete** action is offered in this phase. Permanent deletion remains an individually confirmed, guarded single-file operation; job-wide deletion remains separately labeled and confirmed.
- Local authenticated thumbnail requests begin only when cards approach the viewport (400px margin). Remote images retain native lazy loading; platforms without IntersectionObserver keep immediate loading. This does **not** yet virtualize the grid.
- Unit and integration tests cover additive/shift range selection, selection clearing and search focus safety, bounded organization edits, metadata-only bulk forget with partial failures, and deferred local thumbnail fetch behavior.

**Validation:** The previous head `c58030a` passed CI, CodeQL and desktop builds. Latest bulk/performance CI must be checked independently. Native desktop screen-reader/visual acceptance remains incomplete.

## File-grain SQLite pagination and global ordering (2026-11-08)

- Added a distinct authenticated **`GET /api/library/files`** endpoint, preserving the existing job-grouped `/api/library` API. It returns at most 100 files per page (the UI requests 50), each with its source job metadata, a source count, global matching-media count, facets, storage stats, and an opaque cursor.
- Supports global file-level sorts **recent, oldest, title A–Z, largest first and longest first**, with stable file-ID tie-breaking. Opaque cursors encode sort and filter fingerprint and reject use with a changed filter or sort. Invalid limits, filters and methods are rejected.
- SQLite filters now operate on individual files for media type, category, channel and search, plus global favorites and collection memberships from the persistent organization document. `playlists=1` and `source=<jobID>` constrain playlist browsing even when a playlist spans many pages. Recent-only filtering is also server-side.
- The store loader now accepts a list of selected file IDs to avoid materializing entire 10,000-file playlists when only 50 entries are requested. The React loader appends records by composite media identity rather than job ID and lets SQLite govern cross-page ordering. Only visible loaded records may be bulk-selected.
- **Deletion safety:** The whole-download delete confirmation and inspector warning explicitly include all files in the source job, **even files not visible on loaded pages**. The separate confirmed individual-file deletion remains unchanged.
- Added Go regression cases for global sorting, cursor validity, per-file organization facets and 10,100-file pagination bounds, plus UI regression coverage for 102 files in one playlist and favorites located beyond the first page.

**Remaining:** Grid virtualization (rendering only visible cards), backend query-plan/index tuning on very large collections, complete source-playlist discovery outside loaded pages, native desktop visual review and final CI verification.

## Next development passes

- [ ] Virtualized scrolling for 10,000+ media items (local thumbnails now defer fetching until near viewport)
- [x] Global file-level keyset pagination, sort and favorites/collection filters via `/api/library/files` (latest CI pending)
- [x] Durable SQLite-backed favorites and user-defined collections, including portable ZIP v2 export/import (latest ZIP CI pending)
- [ ] Grouped channel and source-playlist browsing
- [x] True per-file permanent deletion with dedicated Go API and explicit confirmation (previous completed CI green)
- [x] Loaded-page multi-select and safe, purpose-specific bulk actions (full-library bulk and physical bulk deletion not implemented)
- [ ] Optional richer playback experience and metadata indexing
- [ ] Manual native-window visual acceptance on Windows, macOS, and Linux
- [ ] Confirm browser, Go and desktop CI checks on this branch

## Regression focus

The original UI tests cover the existing queue and settings workflows, thumbnails, previews, file operations, and library filters. Check the new per-file rendering regression in `src/ui.test.mjs`.

This phase adds SQLite migration v32 without introducing any new runtime dependencies or storage quotas. The migration participates in the existing pre-migration backup strategy.
