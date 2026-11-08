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

Favorites are stored in the local frontend preferences (`localStorage`) using a composite job/file key, without a new SQLite migration. They are not included in exports or synchronized across separate browsers/devices.

Search and server filters work against the API's existing 50-job pagination. Sort and favorites filter apply to the currently loaded pages; to load more, use **Load more**. The sidebar count for media types reflects loaded records, while the overall library stats use server aggregates.

## Follow-up implementation (2026-11-08)

- Fixed the browser E2E expectation to wait for the new **Media Library** heading instead of the removed "Download library" heading.
- Added source-playlist browsing and channel facet navigation. Playlist browsing operates on loaded pages, and channel filtering still delegates to the existing server API.
- Implemented local user collections with create, rename, remove, and add/remove media membership. Deleting a collection does **not** delete any downloaded files. The collection model validates persisted data, deduplicates membership, and rejects case-insensitive duplicate names.
- Added narrow-window Library navigation and a collection-creation control.
- Added targeted frontend regression tests for collection behaviors, playlist browsing, and collection model validation.
- Canonical scope: [complete master prompt](YOUTUBE_VAULT_MEDIA_LIBRARY_V2_MASTER_PROMPT.md). Delivery and verification status: [tracking document](MEDIA_LIBRARY_V2_TRACKER.md).

**Persistence limitation:** collections and favorites currently use per-browser localStorage; they are not part of SQLite, Library ZIP exports, or shared between Wails and a separate browser profile. Server-backed persistence remains outstanding.

## Next development passes

- [ ] Real virtualized scrolling and scalable thumbnail-fetch scheduling for 10,000+ media items
- [ ] Server-supported global file-based sorting, favorites filtering, and page cursors
- [ ] Durable SQLite-backed favorites and user-defined collections
- [ ] Grouped channel and source-playlist browsing
- [ ] True per-file permanent deletion with dedicated Go API and explicit confirmation
- [ ] Multi-select and safe, purpose-specific bulk actions
- [ ] Optional richer playback experience and metadata indexing
- [ ] Manual native-window visual acceptance on Windows, macOS, and Linux
- [ ] Confirm browser, Go and desktop CI checks on this branch

## Regression focus

The original UI tests cover the existing queue and settings workflows, thumbnails, previews, file operations, and library filters. Check the new per-file rendering regression in `src/ui.test.mjs`.

No new app storage quota, native dependencies, or schema migration are introduced.
