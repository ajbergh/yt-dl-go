# Finding review and correction tracker

Branch: `fix/review-findings`

Reviewed 2026-11-09. Each source-confirmed issue has its own tracking file. Status distinguishes implemented corrections from open work; platform or behavioral reproductions still needed are stated explicitly.

| ID | Severity | Finding | Status |
| --- | --- | --- | --- |
| RF-001 | high | [Vault entry remains locked after deleting one file](RF-001-vault-delete-lock.md) | Fixed |
| RF-002 | high | [Unix publication fails on filesystems without hard links](RF-002-unix-hardlink-publication.md) | Fixed |
| RF-003 | medium | [Migration 20 saves jobs against a later schema](RF-003-migration-active-jobs.md) | Fixed |
| RF-004 | medium | [Interrupted playlist with missing output fails first reconciliation](RF-004-interrupted-startup-missing-file.md) | Fixed |
| RF-005 | medium | [Accessible empty download root removes Vault metadata](RF-005-empty-root-erases-vault.md) | Fixed |
| RF-006 | medium | [Vault search operates on encoded JSON rather than values](RF-006-vault-search-json.md) | Fixed |
| RF-007 | medium | [Windows Explorer launch acknowledgement](RF-007-windows-explorer-launch.md) | Fixed |
| RF-008 | medium | [Per-item deadline assumes access to the full shared bandwidth cap](RF-008-shared-bandwidth-deadline.md) | Fixed |
| RF-009 | medium | [Successful resumed item retains an earlier failure](RF-009-stale-item-failure.md) | Fixed |
| RF-010 | medium | [Audio startup timer waits for a whole range chunk](RF-010-audio-startup-chunk.md) | Fixed |
| RF-011 | medium | [Unauthenticated local settings API can select an executed browser path](RF-011-unauthenticated-browser-path.md) | Fixed |
| RF-012 | medium | [Vault section display resets while server filter persists](RF-012-vault-filter-tab-switch.md) | Fixed |
| RF-013 | medium | [Bulk queue actions include selected jobs hidden by filters](RF-013-queue-hidden-bulk-selection.md) | Fixed |
| RF-014 | medium | [Live reconciliation removes completed Queue rows](RF-014-completed-queue-disappears.md) | Fixed |
| RF-015 | medium | [Retry acknowledgement duplicates an SSE-inserted job](RF-015-retry-duplicate.md) | Fixed |
| RF-016 | medium | [Partly successful Add to queue duplicates accepted links](RF-016-partial-add-duplicates.md) | Fixed |
| RF-017 | medium | [Unix dev script omits backend dev build tag](RF-017-unix-dev-tag.md) | Fixed |
| RF-018 | medium | [Every progress update rerenders the Queue row list](RF-018-queue-progress-rendering.md) | Fixed |
| RF-019 | low | [Externally deleted file remains marked available](RF-019-stale-file-availability.md) | Fixed |
| RF-020 | low | [Imported organization can exceed the saved document size cap](RF-020-merged-organization-size.md) | Fixed |
| RF-021 | low | [Exports can exceed portable import limits](RF-021-export-import-limits.md) | Fixed |
| RF-022 | low | [Cancelling paused job fails when work directory is gone](RF-022-cancel-missing-workdir.md) | Fixed |
| RF-023 | low | [Browser-mode shutdown can wait for live event streams](RF-023-browser-shutdown-delay.md) | Fixed |
| RF-024 | low | [Native video partial can be discarded when browser capture is selected](RF-024-native-partial-browser-resume.md) | Fixed |
| RF-025 | low | [Chapter MP3 conversion rejects slightly shorter decoded audio](RF-025-chapter-duration-tolerance.md) | Fixed |
| RF-026 | low | [Speed and ETA retain old baseline after byte counter resets](RF-026-progress-reset-speed.md) | Fixed |
| RF-027 | low | [Build attestations are available outside release tags](RF-027-nonrelease-attestations.md) | Fixed |
| RF-028 | low | [Desktop build executes tools from a predictable shared temp directory](RF-028-shared-build-tools.md) | Fixed |
| RF-029 | low | [Playlist-wide controls only appear on the first item row](RF-029-playlist-first-row-controls.md) | Fixed |
| RF-030 | low | [Live reconnection overwrites unsaved settings edits](RF-030-reconnect-settings-draft.md) | Fixed |
| RF-031 | low | [Separate-window dev mode adds trailing whitespace to environment values](RF-031-windows-dev-address.md) | Fixed |

## Resolution notes

All 31 findings are resolved in code. RF-007 was addressed by separating process creation from Explorer exit status; the originally reported native UI symptom was not reproduced. Each tracker records implementation and validation limits.

- Missing files/volumes retain Vault metadata; explicit forget removes it.
- Schema migration 33 rebuilds normalized search indexes, with pre-migration backup.
- Unauthenticated clients configure the browser executable through startup CHROME_PATH; authenticated clients can still edit it.
- Exports beyond portable import limits return HTTP 422 instead of an unusable ZIP.

## Validation

- Full backend suite: `go test ./...` passes on Windows outside the sandbox (44.338s; filesystem migration tests require access to user-owned paths).
- `npm run check`: typecheck, production bundle validation, ESLint (19 existing warnings), and six frontend regressions pass.
- `node --test scripts/desktop-packaging.test.mjs`: six packaging tests pass.
- Linux regression binary executed in Debian WSL for Unix fallback/no-overwrite and the new issue regressions.
- Linux amd64 and macOS arm64 test binaries cross-compile. Physical exFAT/network mounts and native macOS/Explorer UI were not manually exercised.
- PowerShell dev-script parsing, quoted ADDR assignment, build-script syntax and `git diff --check` pass.

Regression sources: [backend](../../src/server/review_open_test.go), [initial batch](../../src/server/review_regression_test.go), [Unix publication](../../src/server/durable_unix_test.go), [frontend](../../scripts/review-findings.test.mjs).

## Pre-PR checks (2026-11-10)

- CI explicitly runs `npm run test:review` alongside the existing integration suite.
- Bun 1.4.2 integration tests: 111 passed. Browser-path guidance now uses product language.
- Browser E2E passes, including preservation of unavailable metadata and explicit forget. Its harness waits for scroll events before opening context menus and handles early event timeouts without losing diagnostics.
- Go lint (CI-pinned v2.14.0): zero issues after correcting two error strings. Vulnerability scan (v1.6.0): no vulnerabilities.
- Go vet (standard/E2E tags), development-origin policy check, and license checks pass.
- License drift detection now compares normalized Git contents and separately checks untracked generated files; no license contents or dependency versions changed.
- Native-range HTTP fixture writes now have a five-second deadline so cancelled-peer cleanup remains bounded; three race-enabled repetitions passed. The complete race run is recorded in the PR validation results.
