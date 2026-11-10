# RF-014: Live reconciliation removes completed Queue rows

Severity: medium

Status: Fixed

Reviewed: 2026-11-09

Source: src/hooks/use-service.ts

Evidence: SSE snapshots and the 30-second reconciliation replaced client rows with the server active/retryable snapshot, dropping completed jobs.

Resolution: Retain terminal jobs observed in this session when merging snapshots; explicit job-deleted events still remove them. Clear completed remains available.

Validation: The frontend reconciliation regression retains completed rows and deduplicates incoming IDs. npm run check passes. Completed history is not loaded after a full application reload.
