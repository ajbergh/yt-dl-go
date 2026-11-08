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
| M06 | Sorting and favorites | Partial | Favorites now use SQLite-backed organization API with legacy migration; sorting remains client-side and loaded-page scoped |
| M07 | Channel and source playlist browsing | Partial | Sidebar channel facet shortcuts plus loaded source-playlist view; true server-wide playlist grouping remains |
| M08 | User collections | Partial | SQLite-backed create/rename/delete/membership and portable ZIP v2 export/import implemented; global collection browse, visual QA and CI remain |
| M09 | Dedicated per-file permanent delete | Complete | Selected-file API, path validation and preservation tests; previous branch head `e795208` passed CI and desktop builds |
| M10 | Multi-select and bulk file actions | Not started | Needs safe selection and backend semantics |
| M11 | Keyboard and accessibility polish | Partial | Ctrl/Cmd+F, focusable cards, Enter preview; audit still needed |
| M12 | High-scale performance | Partial | Existing server page cursors and thumbnail loading; virtualization and server-wide file sorting remain |
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
- [ ] Grid virtualization / intersection-based local thumbnail fetching.
- [ ] Full-resolution artwork fallback performance for remote and offline content.

### Organization and persistence
- [x] Existing server-side search/type/category/channel query and pagination integration.
- [x] Grid/list and sort preferences in local browser storage.
- [x] Favorites and collection membership stored in SQLite v32; synchronized across windows when reloaded (revision conflicts prevent silent overwrites).
- [x] Portable ZIP v2 metadata export/import for favorites and collections with deterministic media-identity remapping (latest CI pending).
- [ ] Global server-supported file sorting/pagination and favorites filtering.
- [x] Collection export/import: additive merges by case-insensitive collection name, preserves existing data, and records empty collections (latest CI pending).
- [ ] Source playlist dedicated server filtering when libraries span many API pages.
- [ ] Orphan cleanup for deleted/moved media references.

### Safety and accessibility
- [x] Existing native preview, reveal, copy-path and open-folder API integration.
- [x] Explicit job-wide Delete label; warning for multi-file jobs.
- [x] Existing confirmation for job-wide Delete.
- [x] Dedicated single-file permanent deletion endpoint and UI, guarded by tracked-path validation and native confirmation (latest CI to verify).
- [ ] Standard multiselect keyboard behavior and safe batch confirmations.
- [ ] Screen-reader and high-contrast desktop audit.
- [ ] Complete reduced-motion/mobile/narrow visual audit.

## CI history / validation truthfulness

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

