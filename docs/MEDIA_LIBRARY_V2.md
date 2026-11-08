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

**Delete files** invokes the existing job-level endpoint and deletes **all files associated with the selected download job**. It is not a single-file delete. When a job contains multiple media files, an explicit warning appears.

**Forget item** uses the preexisting single-file library-index removal endpoint. It leaves the downloaded file and job history on disk.

Favorites and collections are now stored in the **SQLite state database (migration v32)**, using a versioned organization document. A legacy `localStorage` compatibility cache remains, and old local preferences are migrated when the database organization document is empty and at revision zero. They are not yet included in Library ZIP exports/imports.

Search and server filters work against the API's existing 50-job pagination. Sort and favorites filter apply to the currently loaded pages; to load more, use **Load more**. The sidebar count for media types reflects loaded records, while the overall library stats use server aggregates.

## Follow-up implementation (2026-11-08)

- Fixed the browser E2E expectation to wait for the new **Media Library** heading instead of the removed "Download library" heading.
- Added source-playlist browsing and channel facet navigation. Playlist browsing operates on loaded pages, and channel filtering still delegates to the existing server API.
- Implemented local user collections with create, rename, remove, and add/remove media membership. Deleting a collection does **not** delete any downloaded files. The collection model validates persisted data, deduplicates membership, and rejects case-insensitive duplicate names.
- Added narrow-window Library navigation and a collection-creation control.
- Added targeted frontend regression tests for collection behaviors, playlist browsing, and collection model validation.
- Canonical scope: [complete master prompt](YOUTUBE_VAULT_MEDIA_LIBRARY_V2_MASTER_PROMPT.md). Delivery and verification status: [tracking document](MEDIA_LIBRARY_V2_TRACKER.md).

**Persistence improvement:** favorites and collections use an authenticated `GET/PUT /api/library/organization` endpoint backed by SQLite. Updates use a revision compare-and-swap to avoid silently overwriting another window. Stale updates return 409 with a reload option in the UI. Existing per-browser values migrate on first use if no server state exists. Library ZIP import/export portability remains outstanding.

## SQLite organization persistence (2026-11-08)

- `src/server/library_organization.go`: versioned, strictly validated database document for favorites and collections.
- Migration v32 installs the organization table without modifying downloaded files or job history.
- `GET /api/library/organization` and `PUT /api/library/organization` use existing bearer authorization; PUT has a 512 KiB request-body limit and returns HTTP 409 on a stale revision.
- `src/hooks/use-library-organization.ts`: on opening the Library, loads canonical state from SQLite, migrates legacy local preferences when safe, and serializes edits to avoid same-window races.
- Tests cover backend restart persistence and stale/invalid writes, plus frontend hydration across remounts.
- Browser E2E assertions were updated for redesigned heading and empty-Library text. Confirm latest CI outcomes separately before merging.

**Remaining limitations:** organization metadata is not in Library ZIP export/import, and UI membership/count filters remain limited to loaded pages. Revision conflicts require a manual reload; automatic multi-window change broadcasting is not implemented.

## Safe single-file deletion (2026-11-08)

- Added a separate authenticated `DELETE /api/jobs/{jobID}/library-files/{fileID}` endpoint in `src/server/library_file_delete.go`.
- This action verifies the selected published file (and tracked caption sidecar, if present) under a rooted filesystem handle, rejects changed files and symbolic links, and refuses operations while the job has active readers.
- The endpoint removes the **selected file only**, then excludes that item from the Library index while leaving siblings and job history intact. Existing private thumbnails and working copies for that file are cleaned up using current helpers.
- The Library inspector now offers **Delete selected file** and **Delete entire download** as distinct actions. The selected-file action has its own irreversible-deletion confirmation showing the media title.
- Added Go regression tests for two-file playlists, restart/history consistency, modified/symlinked files, unknown file IDs and unauthorized access. Added frontend confirmation/routing test and updated the browser E2E selector for the newly labeled job-wide action.

**Follow-up:** Review partial filesystem failures, UI feedback, and native-window tests. Automated verification for this head is pending. Export/import of favorites and collections remains a separate roadmap milestone.

## Next development passes

- [ ] Real virtualized scrolling and scalable thumbnail-fetch scheduling for 10,000+ media items
- [ ] Server-supported global file-based sorting, favorites filtering, and page cursors
- [x] Durable SQLite-backed favorites and user-defined collections (migration v32; export/import pending)
- [ ] Grouped channel and source-playlist browsing
- [x] True per-file permanent deletion with dedicated Go API and explicit confirmation (current CI pending)
- [ ] Multi-select and safe, purpose-specific bulk actions
- [ ] Optional richer playback experience and metadata indexing
- [ ] Manual native-window visual acceptance on Windows, macOS, and Linux
- [ ] Confirm browser, Go and desktop CI checks on this branch

## Regression focus

The original UI tests cover the existing queue and settings workflows, thumbnails, previews, file operations, and library filters. Check the new per-file rendering regression in `src/ui.test.mjs`.

This phase adds SQLite migration v32 without introducing any new runtime dependencies or storage quotas. The migration participates in the existing pre-migration backup strategy.
