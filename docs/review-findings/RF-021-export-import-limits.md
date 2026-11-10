# RF-021: Exports can exceed portable import limits

Severity: low

Status: Fixed

Reviewed: 2026-11-09

Source: src/server/library_export.go; src/server/library_import.go

Evidence: Export gathers all records and streams archive entries without enforcing import limits of 10000 records and bounded archive/JSON sizes.

Resolution: Exports reject more than 10000 records early, then validate the completed ZIP against the actual import contract before serving it. Compressed/expanded sizes, entry limits, JSON, organization and portable records share import validation; failures clean up and return an actionable HTTP 422.

Validation: TestReviewExportRejectsUnimportableArchive and existing ZIP round-trip/export tests pass.
