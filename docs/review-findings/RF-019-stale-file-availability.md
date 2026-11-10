# RF-019: Externally deleted file remains marked available

Severity: low

Status: Fixed

Reviewed: 2026-11-09

Source: src/server/store.go: loadLibraryDiskJobs; src/server/library_file_pagination.go

Evidence: Read models restore availability flags from persisted JSON without checking the file on each page request; external deletion after startup can leave stale badges until reconciliation.

Resolution: Requested Vault pages validate canonical paths, regular-file identity and recorded size before reporting availability. Metadata is retained and read-only export snapshots are not mutated. Explicit file/ticket operations revalidate retained files so restored files work immediately.

Validation: TestReviewAvailabilityRecoversWithoutLosingMetadata checks deletion, restoration and preview; startup unavailable-drive and export snapshot tests pass.
