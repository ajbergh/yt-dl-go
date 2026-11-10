# RF-023: Browser-mode shutdown can wait for live event streams

Severity: low

Status: Fixed

Reviewed: 2026-11-09

Source: src/server/main.go: run shutdown sequence

Evidence: HTTP Shutdown waits up to 10 seconds before s.stop cancels service activity. Active SSE connections can keep shutdown waiting.

Resolution: Browser-mode graceful HTTP shutdown closes the event broker immediately through RegisterOnShutdown. SSE handlers exit before HTTP drain waits; worker/persistence cleanup retains its existing order.

Validation: TestReviewShutdownClosesLiveEventsBeforeDrain holds a live SSE request and verifies graceful shutdown completes within its deadline.
