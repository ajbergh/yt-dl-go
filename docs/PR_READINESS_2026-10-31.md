# PR readiness work — 2026-10-31

Branch: `fix/live-resolution-matrix`, working changes after `9fc0c8d`.

This work addresses the review findings. It does not establish universal YouTube compatibility or erase the failures recorded in [the earlier production UI report](PRODUCTION_UI_1080_2026-10-30.md).

## Changes

- Browser completion no longer discards late data events. If Chrome cannot return its completed response body, assembly waits for a byte-count-verified streamed copy. It checks Content-Length or waits for the complete encoded event count and compares the copy with the decoded byte count. HTTP chunk framing is not counted as media. Unknown or mismatched bodies fail safely; waiting is bounded and cancellation remains effective. The assembled bytes are copied before parsing so late events cannot mutate a parser's input.
- Dual-track WebM capture receives one fresh-tab retry for late failures as well as startup failures. Both attempts stay within the item's overall deadline. Failed-attempt parts are removed, per-track checkpoints are restored for the next attempt, replacement providers are closed, and storage failures stop recovery.
- Capture deadlines permit long-form playback instead of imposing a fixed 45-minute cap. The existing item and job deadlines still apply.
- Missing adaptive source lengths, item reservation overruns, browser response-buffer limits, and the actual job quota have distinct errors. Reservation and quota diagnostics record byte counts without signed source URLs. Storage and reservation failures are preserved instead of being flattened into browser authorization failures.
- Production builds use Node's native config loader. Vite's runner loader failed while evaluating Tailwind's CommonJS dependency chain. The Windows build script invokes Vite directly so npm's PowerShell argument forwarding cannot consume `--outDir` and cause a rebuild of stale generated output.
- The server README now states that progressive 360p recovery requires explicit opt-in.

## Offline validation

- Complete offline Go suite with CGO disabled: passed, 38.232 seconds after the final framing and early-event replay changes (earlier run: 45.104 seconds). Final E2E-tagged `go vet` also passed.
- Frontend integration tests: 69 passed, 337 assertions. Existing React `act` warnings remain.
- `npm run check`: passed, including type checking, production bundle validation, and ESLint. ESLint reports 19 existing Fast Refresh warnings and no errors.
- Browser E2E: passed, covering inspection, queue configuration, pause/resume, completion, Library, preferences, and restart persistence.
- The actual Windows build script passed its Go tests and built a 24,479,744-byte executable under `.vite-cache/readiness/`.
- That executable passed startup/restart smoke checks for health, build identity, embedded UI, and SQLite using disposable private application data.
- The final executable also passed both startup/restart smoke sessions against the database produced by its real UI download. All temporary validation services and their browser children were stopped afterward.
- New regression coverage includes completion before final data, evicted completed-body retrieval, chunked response byte accounting, rejection of unverified copies, fresh dual-track retry through actual WebM remuxing, failed-attempt cleanup, provider closure, cancellation, storage failures, long-video deadlines, and source-length/reservation error classification.
- A real local HTTP/Edge fixture confirmed that six chunked payload bytes produce 21 encoded data bytes; the final accounting regression includes that framing overhead.

Windows ACL tests require execution outside the restricted agent sandbox; the initial sandbox-only run was not a passing result.

## Live validation

The first targeted API run downloaded both previously failing samples at the 1080p ceiling, with 360p recovery disabled:

| Sample | Actual output | Final bytes | Result |
| --- | --- | --- | --- |
| Caminandes 2 | 1080p MP4 | 55,190,068 | Passed; first browser video attempt failed, fresh-tab retry completed |
| Big Buck Bunny 60 fps | 1080p MP4 | 268,108,106 | Passed; native multichannel AAC returned 403, compatible stereo AAC fallback completed and was disclosed |

Both files passed container checks for dimensions, audio/video presence, and track endpoints through the expected source duration. This does not decode every frame. The cases took 54.91 and 214.63 seconds respectively, excluding sample setup. These runs preceded the final chunked-response accounting adjustment, so they are supporting evidence rather than a claim that every final code path has been exercised live.

The final-code Caminandes 2 API rerun also passed: 1080p, 55,190,068 bytes, verified tracks and endpoints, 180.05 seconds for the case. Its first audio capture stalled after receiving media and the fresh-tab retry completed. This recovery is retained as part of the result.

The first packaged production UI validation used executable SHA-256 `6c3a6bad6bdf3b3fd60b546c64e8a56abadd8578fae5c61a2ace737c390c8285`, before the final HTTP-framing adjustment, on a separate local port with disposable private data. Managed and published output both stayed under `.vite-cache/readiness/`; the user's existing app instance and Library were not involved. The same application process handled both samples so browser-pool reuse was exercised. Watch URLs and 1080p ceilings were submitted through UI controls; API polling observed results. Published files received independent `TestLivePublishedFile` checks.

Both packaged UI cases passed: Caminandes 2 at 1080p, 55,190,068 bytes, and Big Buck Bunny at 1080p, 268,108,106 bytes. Both were visible in Library and passed independent published-file verification. Big Buck Bunny disclosed its compatible AAC fallback. No submitted job failed in this two-case UI run.

The final accounting implementation additionally replays bounded byte-count records from data events that precede response registration. A separate final-binary Caminandes 2 UI rerun also passed at actual 1080p, 55,190,068 bytes, with Library visibility and independently verified published video/audio through the expected duration. Its executable SHA-256 is `5b07a6975ac24a09373be71e01a5443cde34dafe08d7241dd9acf4f970546bfa`; artifacts are under `.vite-cache/readiness-final/production-ui/`. Big Buck Bunny's packaged UI pass above predates only the final framing/replay changes. No new 4K downloads were started.

Ignored diagnostic artifacts: `src/server/.cache/readiness-1080.log`, `src/server/.cache/readiness-final-caminandes.log`, and `.vite-cache/readiness/`. Diagnostic logs can contain temporary upstream details and should not be committed.

## Remaining readiness gates

Later sample investigation: [preroll failures and fixes](VEVO_DOWNLOAD_FIX_2026-10-31.md), including [uBlock-style player-data filtering and final-build results](UBLOCK_PLAYER_FILTERING.md). Earlier passes below do not establish readiness for that additional sample.

- Revalidate the previously unresolved Big Buck Bunny `best`/4K result when 4K testing resumes.
- Run a broader final-build matrix across codec strategies, audio-only output, different source characteristics, and quality ceilings. Count failed submissions and internal recoveries instead of replacing failures with eventual passes.
- Exercise real network interruption, bandwidth caps, multiple concurrent jobs, browser crashes, and restart/resume against the packaged app.
- Run Linux race tests and the remaining cross-platform CI checks; verify graceful Windows console shutdown separately.
- Add full media decoding checks where a decoder is available. Existing container checks establish structure and endpoints, not complete frame integrity.
- The exact original false-quota production failures have not been causally reproduced. Passing reruns and clearer errors improve the evidence but do not prove the original cause has been eliminated.
