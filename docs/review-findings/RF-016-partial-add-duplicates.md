# RF-016: Partly successful Add to queue duplicates accepted links

Severity: medium

Status: Fixed

Reviewed: 2026-11-09

Source: src/pages/home.tsx: addDownloads

Evidence: Failure handling prepended accepted jobs without deduplication and retained all submitted drafts.

Resolution: Deduplicate accepted IDs and remove accepted drafts/URLs, leaving failed and unattempted links for retry.

Validation: npm run check passes. The partial-failure branch deduplicates accepted IDs and removes accepted drafts/URLs; a live partial API failure was not manually exercised.
