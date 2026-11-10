# RF-006: Vault search operates on encoded JSON rather than values

Severity: medium

Status: Fixed

Reviewed: 2026-11-09

Source: src/server/library_pagination.go: libraryFilterClause

Evidence: Raw file_json appears in the substring and FTS search paths. Keys can match, and JSON escaping hides literal punctuation. SQLite lower does not provide general Unicode case folding. Simple CAFÉ searches passed the investigation, so the broad Unicode claim needs narrower tests.

Resolution: Schema migration 33 replaces raw-JSON FTS contents with decoded display values normalized with Unicode NFKC and case folding. Matching uses the same normalization. Database triggers cover imports, reconciliation and source/item updates. Migration backups and missing-table repair are preserved.

Validation: TestReviewSearchUsesNormalizedValues covers composed/decomposed Unicode, capitalized non-ASCII, case-fold expansion, quotes, ampersands, angle brackets, key-only misses and index updates. Historical migrations, backup/repair and scale tests pass.
