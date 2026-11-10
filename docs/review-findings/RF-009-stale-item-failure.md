# RF-009: Successful resumed item retains an earlier failure

Severity: medium

Status: Fixed

Reviewed: 2026-11-09

Source: src/server/worker.go: setItemFiles

Evidence: recordFailure appends failures; successful finalization previously retained them, and finish uses their presence to choose partial status.

Resolution: Remove prior failures for the finalized item when committing its files. Preserve failures for other items.

Validation: TestSuccessfulItemClearsPriorFailures passes, including duplicate old failures and an unrelated failure.
