# RF-007: Windows Explorer launch acknowledgement

Severity: medium

Status: Fixed

Source: src/server/filesystem.go: openTrackedOutput, launchTrackedCommand

Evidence: Explorer execution used Run, so any post-launch nonzero exit became HTTP 500. A successful native Explorer UI operation with a nonzero exit was not observed; the source-level launch/exit-status coupling has been removed.

Resolution: Windows reports process creation failure only. Successfully started commands are reaped asynchronously, and the HTTP context cannot terminate the user shell. macOS/Linux retain synchronous command/fallback handling.

Validation: TestReviewExplorerLaunchIgnoresChildExit verifies a child that exits nonzero is accepted after launch and missing executables still return an error. Native Explorer UI behavior was not manually exercised.
