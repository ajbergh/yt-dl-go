# Merge plan for PRs 149 and 150

Reviewed 2026-11-08. This is a proposed sequence; neither PR is merged by this plan.

## Verified baseline

- PR 150, `8890e237`, has all 15 reported checks green, including source/race tests,
  CodeQL, production packages, and all six Wails targets.
  [CI run](https://github.com/ajbergh/yt-dl-go/actions/runs/42531096345).
- PR 149, `983086bf`, passed every reported check except browser E2E. Its failing
  assertion expected the retired deletion prompt. The application correctly warned
  that deleting an entire download includes media on unloaded Library pages.
  [Failing validation job](https://github.com/ajbergh/yt-dl-go/actions/runs/42515469639/job/128837402206).
- The repair checks the confirmation type, whole-download scope, unloaded-page
  warning, and irreversibility. An assertion failure dismisses the dialog before
  diagnostics, preventing the secondary CDP timeout. The repaired browser suite and
  embedded frontend build passed locally; new-head GitHub checks must pass too.
- A non-checkout `git merge-tree --write-tree` simulation of the two reviewed heads
  found one textual conflict, in `src/pages/library.tsx`. Clean textual merges do
  not establish behavioral compatibility.

## Recommended order

1. Verify the latest PR 150 head still matches its green CI run, then squash-merge
   PR 150 into `main`. Its engine work can land independently while PR 149 remains
   draft for the required native UI acceptance.
2. Merge the resulting `main` into `feature/media-library-v2`. Preserve its existing
   review history; do not replay or force-push its long implementation history.
3. Resolve the Library UI conflict in favor of PR 149's media-first design, then
   address the semantic integration work below. Commit this on PR 149.
4. Run the complete CI and desktop matrix on that combined head. Complete native
   desktop visual/interaction acceptance and review the remaining tracker scope
   before taking PR 149 out of draft. Green CI alone is insufficient.
5. Once those gates pass and the accepted scope is documented, squash-merge PR 149.
   If `main` changes again, refresh the branch and revalidate before merging.

## Required integration work

- **Active files in the new Library API:** PR 150 publishes finalized tracks before
  their source job finishes. Remove the terminal-status restriction in PR 149's
  `loadLibraryFilePage` base query. Preserve file-level keyset cursors, organization
  filters, bounded selected-file loading, and the 10,100-file scale coverage.
- **Automatic merges to retain and verify:** the simulation retained
  `selectedFileIDs` in `loadLibraryDiskJobs`, active-row persistence/backfill,
  PR 149's organization/file/delete routes, and PR 150's `job-file-finalized`
  refresh in the hook that now fetches `/api/library/files`.
- **Inspector and bulk actions:** keep Preview, Reveal, Copy path, and Open folder
  working for finalized active files. Disable stopped-job-only selected-file
  deletion, whole-download deletion, and metadata forget actions for active jobs,
  including mixed active/stopped bulk selections. Retain server-side rejection.
- **Shared audio ownership:** PR 150 intentionally gives repeated audio jobs
  distinct Library records sharing one physical file. PR 149 rejects shared paths
  for individual deletion, but whole-job deletion still unlinks them. Add an
  ownership preflight that rejects whole-job deletion before any filesystem change
  if an outside Library item owns a target media/caption path. Align the UI error
  and documentation with this policy so another job's media cannot disappear.
- **Portable metadata:** retain migration v32, archive v1 compatibility, archive v2
  organization mapping, idempotent import, and rollback behavior. Shared files must
  retain distinct logical identities for favorites/collections and export/import.

## Combined-head regression gates

- A partially completed playlist appears through `/api/library/files`, including
  pagination, sorting, favorites/collections, and restart/backfill.
- `job-file-finalized` refreshes the new Library without duplicate cards or lost
  cursor/filter state. An active finalized file supports preview ranges and native
  filesystem actions; forbidden active mutations stay disabled and return 409.
- Repeated audio produces two records and one file; deleting either job or one
  record cannot unlink the other record's media. Ownership rejection must happen
  before deleting any unrelated sibling or caption.
- Archive v2 round trips preserve organization on distinct reused-file identities;
  old archives and database upgrades remain supported.
- All source, frontend, race, security/license, browser E2E, and desktop/package
  gates pass on the actual combined head. Native UI acceptance remains a separate
  requirement in [the tracker](MEDIA_LIBRARY_V2_TRACKER.md).
