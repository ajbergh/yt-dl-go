# RF-003: Migration 20 saves jobs against a later schema

Severity: medium

Status: Fixed

Reviewed: 2026-11-09

Source: src/server/store.go: migrateV20, loadJobsFiltered

Evidence: The migration loader normalized interrupted jobs via saveJob, which queries library_item_exclusions introduced in v27. The review reproduced no-such-table on v19.

Resolution: Use migration loading without interrupted-job normalization. Normal startup performs normalization after migrations.

Validation: TestHistoricalMigrationWithInterruptedJobs passes for queued/downloading/processing/paused v14 upgrades; historical fixture checks pass.
