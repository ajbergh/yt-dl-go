# Media Library 2.0 — Durable Implementation Tracker

> Canonical requirements: [YOUTUBE_VAULT_MEDIA_LIBRARY_V2_MASTER_PROMPT.md](YOUTUBE_VAULT_MEDIA_LIBRARY_V2_MASTER_PROMPT.md) (full 22-section, 905-line source).  
> Feature branch: [feature/media-library-v2](https://github.com/ajbergh/yt-dl-go/tree/feature/media-library-v2)  
> Review PR: [#149](https://github.com/ajbergh/yt-dl-go/pull/149) (draft; **do not merge** without validation).  
> Last reviewed: **2026-11-08**.

## Principles

- **No fabricated features:** mark complete only when real data, UI handlers, persistence, and validation are present.
- **Protect downloaded files:** job-wide deletion is explicitly labeled and confirmed. Removing a library record is not deletion.
- **Preserve engine:** no downloader rewrite, no Wails bridge rewrite, and no unreviewed schema migrations.
- **All work on one branch:** \`feature/media-library-v2\`; do not merge until review.
- Keep this file updated when code or CI status changes. Link evidence for each milestone.

## Milestone tracker

| ID | Work item | Status | Implementation / acceptance evidence |
| --- | --- | --- | --- |
| M01 | Existing architecture assessment | Complete | React/TS/Tailwind frontend; Go API/Wails; existing file metadata, thumbnails, filesystem and preview APIs |
| M02 | Media-first per-file card presentation | Complete | \`src/pages/library.tsx\` derives one card from each \`job.files[]\` |
| M03 | Responsive grid and list | Complete | Same data with view toggle; local preference |
| M04 | Selected-item detail inspector | Complete | File metadata and preview/reveal/copy/open-folder actions |
| M05 | Search, category, channel and media type filters | Complete | Existing \`/api/library\` filtering reused; search input debounced in \`src/pages/home.tsx\` |
| M06 | Sorting and favorites | In verification | `/api/library/files` now sorts across all media records, with SQLite global favorites filtering; new CI and desktop acceptance pending |
| M07 | Channel and source playlist browsing | Partial | Sidebar channel facet shortcuts plus loaded source-playlist view; true server-wide playlist grouping remains |
| M08 | User collections | In verification | SQLite-backed CRUD, ZIP v2 portability, and server-wide collection membership filtering; UI/CI validation still required |
| M09 | Dedicated per-file permanent delete | Complete | Selected-file API, path validation and preservation tests; previous branch head `e795208` passed CI and desktop builds |
| M10 | Multi-select and bulk file actions | Partial | Loaded-page Ctrl/Cmd, Shift and select-all; bulk favorites, collections, and confirmed metadata-only forget (100 max); permanent bulk file deletion intentionally unsupported |
| M11 | Keyboard and accessibility polish | Partial | Ctrl/Cmd+F/A, Escape, arrow focus/selection, Shift ranges, and Enter preview; screen-reader and contrast audit still needed |
| M12 | High-scale performance | Partial | File-level 50/100-record database paging and viewport-gated thumbnails implemented; grid virtualization and indexed sorting performance remain to be optimized |
| M13 | Desktop responsive visual acceptance | Not started | Manually compare mockup with native windows on Win/macOS/Linux |
| M14 | Automated browser tests | In progress | Repaired stale Library heading and empty-library browser selectors; latest CI E2E rerun required |
| M15 | Cross-platform desktop CI | In progress | Reverify latest branch head after changes |
| M16 | Delivery documentation and PR | In progress | This tracker + complete prompt + feature PR, keep draft |

## Immediate execution queue

- [x] Import complete source master prompt into \`docs/YOUTUBE_VAULT_MEDIA_LIBRARY_V2_MASTER_PROMPT.md\`.
- [x] Diagnose initial CI failure: \`scripts/browser-e2e.mjs\` waited for old Library heading.
- [x] Change browser E2E assertion to expect "Media Library".
- [x] Add this durable tracking document.
- [x] Add playlist and channel browsing with truthful loaded/filtered counts.
- [x] Implement basic create/rename/delete collection and add/remove media UX without touching underlying downloads.
- [x] Add UI regression tests for new organization flows.
- [ ] Verify final branch CI; fix failing jobs or document reproducible blockers.
- [x] Implement loaded-page multi-select (plain, Ctrl/Cmd, Shift, Ctrl/Cmd+A, arrows, Escape) and safe batch favorites/collection operations.
- [x] Add single-confirmation, metadata-only bulk forget (up to 100 loaded entries) with partial failure reporting and one Library refresh.
- [x] Defer authenticated local thumbnail blob requests until cards enter a 400px viewport margin, with fallback when IntersectionObserver is unavailable.
- [x] Add unit/integration coverage for selection, bulk changes, partial failures, keyboard shortcuts and local thumbnail scheduling.
- [x] Separate `Delete selected file` from `Delete entire download`; reject modified/symlinked paths and preserve sibling files and history.
- [x] Add backend and frontend regression tests for single-file deletion and refresh browser E2E button text.
- [x] Implement SQLite v32 organization persistence, authenticated GET/PUT endpoints, revision-based conflict rejection, and one-time legacy preference migration.
- [x] Add Go persistence/authorization/conflict tests and frontend remount integration coverage.
- [ ] Update PR #149 with checked features, verification, and limitations.
- [x] Include portable favorites and collections in ZIP v2, retain acceptance of v1 archives, and merge organization atomically with imported media metadata.
- [x] Cover source-to-imported key mapping, duplicate-name/ID merges, orphan reference omission, idempotence and rollback with backend tests.

## Detailed acceptance / unfinished scope

### Browse and metadata
- [x] Separate files from download jobs in the visible Library grid.
- [x] Media-specific artwork fallbacks and duration/quality/format metadata.
- [x] Selected media inspector with current preview and filesystem actions.
- [ ] True list table with headers and column sorting (current list is compact card rows).
- [ ] Grid virtualization for 10k+ media (intersection-based local thumbnail loading already implemented).
- [ ] Full-resolution artwork fallback performance for remote and offline content.

### Organization and persistence
- [x] Existing server-side search/type/category/channel query and pagination integration.
- [x] Grid/list and sort preferences in local browser storage.
- [x] Favorites and collection membership stored in SQLite v32; synchronized across windows when reloaded (revision conflicts prevent silent overwrites).
- [x] Portable ZIP v2 metadata export/import for favorites and collections with deterministic media-identity remapping (latest CI pending).
- [x] Global file-level keyset pagination, sort (recent/oldest/title/size/duration), and SQLite favorites/collection filters (new CI pending).
- [x] Collection export/import: additive merges by case-insensitive collection name, preserves existing data, and records empty collections (latest CI pending).
- [x] Server-side `playlists=1` and `source=<jobID>` file filters; UI source-discovery list still reflects loaded pages.
- [ ] Orphan cleanup for deleted/moved media references.

### Safety and accessibility
- [x] Existing native preview, reveal, copy-path and open-folder API integration.
- [x] Explicit job-wide Delete label; warning for multi-file jobs.
- [x] Existing confirmation for job-wide Delete.
- [x] Dedicated single-file permanent deletion endpoint and UI, guarded by tracked-path validation and native confirmation (latest CI to verify).
- [x] Loaded-page multiselect keyboard behavior and single-confirmation metadata-only bulk forget; permanent bulk deletion intentionally not offered.
- [ ] Screen-reader and high-contrast desktop audit.
- [ ] Complete reduced-motion/mobile/narrow visual audit.

## CI history / validation truthfulness

- Review of `983086bf` confirmed that all checks except browser E2E passed, including race tests, security analysis, production packages, and all six desktop builds. The E2E failure was the stale `/Delete these library files/i` assertion; the application now warns about deleting ALL source files, including unloaded pages. The repaired test verifies that safety wording and always dismisses an unexpected dialog before diagnostics. The embedded build and repaired browser E2E passed locally; verify the new-head CI before claiming this branch green. PR 150 at `8890e237` is fully green. See the [merge plan](PR149_PR150_MERGE_PLAN.md) for the simulated conflict, integration requirements, and native acceptance gates. This PR remains draft and unmerged.

- File-level global paging implementation adds `/api/library/files` independently of the legacy job-grouped `/api/library`. Page queries use SQLite keyset cursors with a sort+filter fingerprint to reject stale/mismatched cursors, deterministic per-file tie-breaking, and limit 1–100. Unlike job-group paging, file-page reads materialize **only selected file IDs**, even when a single playlist contains 10,100 files. End-to-end UI now pages and deduplicates by `jobID:fileID`, preserves server ordering, and supports global sorting, favorite filtering and collection membership. New Go tests exercise 5 sorts, malformed/changed cursors, global filters, and a 10,100-file playlist; UI tests exercise 102-file multi-page accumulation and favorites beyond the first page. **CI for new commits is pending.**
- Safety correction: job-wide delete UI and confirmation now always warn that *every* source download file may be deleted, including media on unloaded pages. No changes to the validated single-file deletion or legacy `/api/library` contract.
- The preceding head `0ff1768` passed source CI (including browser E2E), CodeQL and Desktop builds. Current global paging head must be checked independently.

- M10 / M11 work: `src/lib/library-selection.ts` provides deterministic range/additive selection and bounded batch favorite/collection helpers. `src/pages/library.tsx` renders the selection toolbar and keyboard controls. `src/hooks/use-jobs.ts` bulk-forgets **only indexed Library entries**, with one confirmation, per-item error reporting, and a 100-item cap; no downloaded media or job history is deleted. CI on the latest branch head is pending.
- M12 incremental performance: `LibraryThumbnail` now gates local authenticated thumbnail blob fetches behind an `IntersectionObserver` with a 400px prefetch margin, falls back to immediate loading in unsupported WebViews, and retains native `loading=lazy` for remote art. Visual/window inspection and full-list virtualization are still outstanding.
- Previous PR head `c58030a` passed the **CI, CodeQL and desktop builds** workflows on 2026-11-08. New M10-M12 commits require fresh checks.

- Portable ZIP v2 retains `state.db` for private backups and adds only validated, tracked favorites and collections to `library.json`. v1 ZIP imports remain supported. Source `jobId:fileId` references map to deterministic imported IDs, while absent/stale entries are ignored. Merge never deletes existing favorites, merges same-name collections, and remaps colliding collection IDs. Organization and media import share one SQLite transaction; a failure rolls back both. Added round-trip, idempotency, legacy-v1, validation and 200-collection-limit rollback tests. **Latest-head CI pending**.

- Completed pre-ZIP PR head `e795208` passed **CI, CodeQL, and desktop builds**. ZIP organization portability changes are subsequent commits and require independent CI validation before marking the new head green.
- Selected-file deletion only operates on tracked, regular-sized published output under the configured library root. It does not delete a sibling media entry, media associated with another job, or whole-job history. Refuses modified or symlinked output, and active readers. The operation is not an undoable trash move; documented confirmation is mandatory. Partial caption/media filesystem deletion errors are reported and the job state is persisted for recovery.

- Follow-up persistence implementation adds `src/server/library_organization.go` and v32 database migration, preserving existing jobs and files. The `/api/library/organization` GET/PUT endpoint rejects invalid entries and stale revisions (409). `src/hooks/use-library-organization.ts` loads from SQLite, migrates existing local-only preferences *only when remote revision is zero and empty*, and serializes client updates. Legacy localStorage remains a compatibility cache, not the source of truth.
- Added Go API tests for restart persistence, unknown fields/validation, stale writes and bearer authentication; added frontend remount hydration test. These tests and latest-head platform build/E2E results need verification before marking complete.
- The previous full CI failure on `fb968af` was at the browser E2E deletion assertion, which expected old string "Your library is empty" instead of new "Your media library is empty"; corrected in `e31f7b6`.

- Added tests covering invalid collections, duplicate names/membership, collection creation/rename/delete, membership updates, and source-playlist browse filtering. These tests must pass in the newest CI run before marking M14 complete.

- Initial PR head \`b7b0c2e\`: frontend typecheck, lint, build, Bun UI tests, Go tests, race tests and CodeQL passed; **browser E2E failed** waiting for literal "Download library" after heading changed to "Media Library".
- Browser assertion changed in commit \`2331e8e\`. This is a test-selector correction, not proof that the browser suite passed. Verify the newest GitHub Actions run and update this section before claiming green CI.
- Native desktop screenshot/interaction testing has not been performed manually in this session.

## How to continue safely

1. Read the master prompt, this tracker, \`docs/MEDIA_LIBRARY_V2.md\`, and the latest PR checks.
2. Make code changes only on \`feature/media-library-v2\`; keep atomic commits.
3. Add targeted tests before marking a milestone complete.
4. Re-run source tests, native CI, and browser E2E through GitHub Actions.
5. Update this tracker with real outcomes and known limitations; leave PR in draft until acceptance.

