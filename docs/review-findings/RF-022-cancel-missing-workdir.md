# RF-022: Cancelling paused job fails when work directory is gone

Severity: low

Status: Fixed

Reviewed: 2026-11-09

Source: src/server/server.go: discardPausedParts, discardResumableOutputLocked; src/server/bulk_actions.go

Evidence: discardPausedParts returns the ReadDir not-exist error; cancellation without a running worker calls it before changing status.

Resolution: A missing working directory counts as already cleaned. Cancellation still clears durable resumable state and records cancellation.

Validation: TestReviewCancellationWithMissingWorkDirectory passes.
