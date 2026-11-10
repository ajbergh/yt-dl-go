# RF-013: Bulk queue actions include selected jobs hidden by filters

Severity: medium

Status: Fixed

Reviewed: 2026-11-09

Source: src/pages/queue.tsx

Evidence: Selection persists while filteredQueue changes; bulk buttons previously passed the entire selection.

Resolution: Intersect selected job IDs with the currently visible queue rows for counts and every bulk action.

Validation: npm run test:review renders selected rows, hides one through filtering, and verifies bulk delete sends only the visible ID. npm run check passes.
