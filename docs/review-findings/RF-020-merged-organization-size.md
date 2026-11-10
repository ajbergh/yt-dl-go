# RF-020: Imported organization can exceed the saved document size cap

Severity: low

Status: Fixed

Reviewed: 2026-11-09

Source: src/server/library_organization_portable.go: mergePortableLibraryOrganization; src/server/library_organization.go

Evidence: Import bounds the incoming encoded document, but the merged result only validates collection/key counts and writes JSON without a byte-size check.

Resolution: Shared organization validation enforces the 512 KiB encoded envelope cap, including the next revision, for ordinary saves and the final merged import result. Validation occurs before transactional writes commit.

Validation: TestReviewOrganizationByteCapRollsBackImport verifies size-based rejection below count limits, no imported media committed, and unchanged organization revision.
