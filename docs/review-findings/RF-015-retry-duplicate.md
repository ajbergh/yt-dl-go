# RF-015: Retry acknowledgement duplicates an SSE-inserted job

Severity: medium

Status: Fixed

Reviewed: 2026-11-09

Source: src/hooks/use-jobs.ts

Evidence: Retry unconditionally prepended its result even if SSE had already inserted the same ID.

Resolution: Remove an existing row with the retry result ID before prepending it.

Validation: npm run check passes.
