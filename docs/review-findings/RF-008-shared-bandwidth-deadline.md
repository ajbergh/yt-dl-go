# RF-008: Per-item deadline assumes access to the full shared bandwidth cap

Severity: medium

Status: Fixed

Reviewed: 2026-11-09

Source: src/server/worker.go: processItem, itemTransferTimeout

Evidence: processItem passes the shared limiter rate directly to a per-item timeout calculation. Concurrent jobs/items share that rate.

Resolution: Capped transfers inherit their parent context without a full-rate per-item deadline. Network idle deadlines, cancellation and the optional overall job deadline remain effective. Uncapped transfers retain individual deadlines.

Validation: TestReviewCappedTransferUsesParentDeadline verifies capped/uncapped behavior and parent cancellation.
