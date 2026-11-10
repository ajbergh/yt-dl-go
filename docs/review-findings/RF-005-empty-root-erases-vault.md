# RF-005: Accessible empty download root removes Vault metadata

Severity: medium

Status: Fixed

Reviewed: 2026-11-09

Source: src/server/library_disk.go: openLibraryPath, reconcileLibraryJob

Evidence: Accessible roots with absent tracked files cause durable index deletion. TestStartupDropsEmptyLibraryAndKeepsImports explicitly expects this behavior; an empty mount is indistinguishable from deletion.

Resolution: Missing published files retain their Vault rows and source metadata with unavailable flags. Interrupted-history normalization and later saves preserve absent groups. Explicit forget remains available, and finalized replacement groups can supersede old entries.

Validation: Updated startup/empty-root policy tests; TestReviewMissingInterruptedMetadataSurvivesSaveAndCanBeForgotten; TestReviewAvailabilityRecoversWithoutLosingMetadata.
