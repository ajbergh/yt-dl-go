# RF-024: Native video partial can be discarded when browser capture is selected

Severity: low

Status: Fixed

Reviewed: 2026-11-09

Source: src/server/worker.go: adaptive video paths; src/server/browser_pair.go; src/server/sabr_checkpoint.go

Evidence: Video transfer acquires a browser and attempts paired capture before native-range resumption. Existing parts are accepted for checkpoint-capable providers without first checking native method. jobSABRCheckpoints.Load explicitly discards a part whose Method is not browser-sabr.

Resolution: Paired browser capture inspects persisted video/audio parts before selecting transport. A native-range checkpoint with verified bytes bypasses paired capture and resumes through native range validation.

Validation: TestReviewNativePartialSkipsBrowserPair verifies native bytes and checkpoint method remain unchanged; browser checkpoint/pause tests pass.
