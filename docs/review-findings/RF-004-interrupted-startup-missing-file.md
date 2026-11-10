# RF-004: Interrupted playlist with missing output fails first reconciliation

Severity: medium

Status: Fixed

Reviewed: 2026-11-09

Source: src/server/library_disk.go: reconcileLibraryJob

Evidence: History normalization may delete the library row before reconciliation deletes it again; deleteLibraryItem then returns sql.ErrNoRows.

Resolution: Make reconciliation deletion idempotent by accepting sql.ErrNoRows.

Validation: TestInterruptedPlaylistMissingFileReconcilesOnFirstAttempt and startup reconciliation tests pass.
