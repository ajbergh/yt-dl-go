# RF-027: Build attestations are available outside release tags

Severity: low

Status: Fixed

Reviewed: 2026-11-09

Source: .github/workflows/release.yml; .github/workflows/desktop.yml

Evidence: Release supports manual and release-validation branch execution; provenance steps lack a release-tag restriction. Desktop gating permits a caller-provided release-desktop prefix outside PR events.

Resolution: All release provenance steps require a push from a v-prefixed tag. Reusable desktop attestation additionally retains the release artifact-prefix requirement. Manual/branch dry runs still produce artifacts without release attestations.

Validation: Workflow conditions reviewed; release/desktop packaging tests pass. No release workflow was dispatched.
