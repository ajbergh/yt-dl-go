# RF-026: Speed and ETA retain old baseline after byte counter resets

Severity: low

Status: Fixed

Reviewed: 2026-11-09

Source: src/server/worker.go: updateProgress

Evidence: The speed baseline updates only when downloaded >= progressBytes. A reset below the prior baseline leaves the prior speed/time until bytes catch up.

Resolution: A decreasing byte counter resets its speed baseline, timestamp and speed before Queue speed/ETA are refreshed. Subsequent samples use the new baseline.

Validation: TestReviewProgressResetClearsSpeedBaseline passes.
