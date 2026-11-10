# RF-001: Vault entry remains locked after deleting one file

Severity: high

Status: Fixed

Reviewed: 2026-11-09

Source: src/server/server.go; src/server/library_file_delete.go

Evidence: Both library-only branches left deleting=true; eviction explicitly refuses deleting jobs.

Resolution: Clear deleting before eviction in both handlers.

Validation: TestLibraryOnlyDeletionAllowsNextFile passes for forget and physical-delete routes.
