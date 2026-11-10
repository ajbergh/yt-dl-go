# RF-011: Unauthenticated local settings API can select an executed browser path

Severity: medium

Status: Fixed

Reviewed: 2026-11-09

Source: src/server/server.go: ServeHTTP, handleSettings; SECURITY.md

Evidence: Origin checking is conditional on a supplied Origin header and bearer auth is conditional on a configured token. The settings patch accepts ChromePath. SECURITY.md documents the single-user unauthenticated loopback assumption.

Resolution: The unauthenticated API rejects changes to ChromePath, while ordinary preferences remain editable. Authenticated clients and the desktop bridge retain path editing; CHROME_PATH remains available at startup. The browser UI disables this field and explains startup configuration. SECURITY.md documents the boundary.

Validation: TestReviewUnauthenticatedSettingsCannotSelectExecutable; authenticated runtime settings persistence test; frontend checks.
