# Preroll handling and download readiness, 2026-10-31

Sample: https://www.youtube.com/watch?v=41ZY18JqI2A (216 seconds).

Subsequent uBlock Origin investigation and implementation: [targeted player-data filtering](UBLOCK_PLAYER_FILTERING.md).

Current result: the player-data-filter candidate passed three 1080p Compatibility production UI submissions plus final-source 480p and Best/2160p cases. Its full offline suite and vet passed. Internal recoveries and the earlier failed candidates are documented below and in the linked filtering report; broader PR readiness checks remain outstanding.

## Findings and changes

- Preroll and requested-content streams can share an itag. SABR capture now checks initialization video identity before pinning a format, and rejects explicitly foreign media headers. An incomplete preroll must not prevent the real track from initializing.
- Requested-content duration and ad state now gate playback acceleration and verified-prefix seeks. Prerolls use normal playback speed and player-level resume controls. An enabled, visible YouTube Skip control can be clicked; an ad is never sought to its end.
- An unknown-length progressive AAC fallback is no longer attempted after a preferred-track read failure. The original read error is preserved instead of reporting a misleading size/quota failure.
- The live resolution matrix accepts `YTDL_LIVE_MATRIX_STRATEGY` to reproduce Compatibility separately from Best.

## Evidence and limits

The initial and intermediate runs reproduced incomplete reads, including Best failing at native byte offset 125,829,120. Identity filtering alone did not resolve paused prerolls. Player-level resume alone also failed. These failures remain part of the evidence.

After enabled Skip handling, the 480p run passed (18,394,340 output bytes). The final source Best run passed at actual 2160p (458,764,159 output bytes, 73.08 seconds for the case). Container checks verified dimensions, both tracks, and track endpoints; they did not decode every frame.

The full offline Go suite passed in 39.933 seconds with live tests excluded, and `go vet -tags=e2e ./...` passed. After adding the visible-replacement control regression, the full suite passed again in 46.613 seconds and vet passed again. Regression coverage includes incomplete same-itag prerolls for video and both audio codecs, foreign media identities, disabled/hidden Skip controls, paused/ended prerolls, and unknown-length fallback error preservation.

The first candidate reports commit `9fc0c8d-vevo-fix`; executable SHA-256: `b0ae15a7fece15c3ee4ea980a61a8899a0fe7814cc31d051daa2184d465c6362`. The candidate with visible-replacement control selection installed on port 8080 reports `9fc0c8d-vevo-fix2`; SHA-256: `d20f81cdd79d2c28a66b04fb02b4029a6090d6e91238929575e874f735e570c4`. The original executable was backed up and the existing database and failed-job history were retained.

**Still a PR readiness concern:** the first production UI 1080p Compatibility submission failed after reporting 73,008,786 transferred bytes. A diagnostic UI rerun completed at actual 1080p (76,548,552 bytes), appeared in Library, and passed independent published-file verification. Its first AAC attempt stalled in a preroll; the fresh-tab retry recovered. Both the failed submission and internal recovery count against reliability. A passing Best run or eventual retry does not establish universal download reliability. Ignored artifacts are under `.vite-cache/vevo-patched/` and `src/server/.cache/vevo-*.log`; do not commit upstream diagnostic payloads.

## Lessons from open-source ad skippers

- [iSponsorBlockTV's player integration](https://github.com/dmunozv04/iSponsorBlockTV/blob/main/src/iSponsorBlockTV/ytlounge.py) distinguishes ad events, content identity, and skip availability. Its TV transport is not a direct replacement for browser control. Apply the state model: wait for availability, request Skip, then verify requested-content progress.
- [YT AutoSkip's content script](https://github.com/epsilon003/skippery-slope/blob/main/src/content.js) combines DOM observation with polling and searches for visible controls across selector variants. These are useful browser resilience patterns. Its optional seek-to-end behavior should not be copied into a downloader that must preserve the complete requested media.
- [SponsorBlock](https://github.com/ajayyy/SponsorBlock) uses crowdsourced timestamps for segments inside the requested video. That solves a separate problem from YouTube-inserted prerolls and would alter downloaded content.

The branch now searches all matching Skip controls and tests that a hidden legacy button cannot mask a visible replacement. That candidate's UI submission still failed: one AAC attempt showed no Skip controls and slowly advancing preroll playback, then hit the 60-second requested-media idle timeout; its fresh retry failed playback preparation. Selector changes alone do not resolve that path.

A further change recognizes advancing preroll playback as liveness while waiting for requested media. Stalled prerolls still expire, main-video playback cannot substitute for captured-byte progress, and the overall capture deadline and verified-output requirements remain in force. Its full offline suite passed in 41.048 seconds, and vet passed. That candidate's UI run still failed: consecutive ads outlasted the bounded capture, then its fresh retry failed playback preparation. Its build tag was `9fc0c8d-vevo-fix3`, SHA-256 `ceb668d88b6964c42139da9a517b440bda1a77727a21d20b43e35b3fbc1bf077`. Increasing liveness tolerance alone is insufficient.

Further hardening should cover player replacement/navigation, multiple ads, countdown-to-enabled transitions, and no-progress after a Skip click. Use event observation plus bounded polling, avoid duplicate clicks, and keep independent source-identity/completeness checks as the authority for publication. Clicking Skip alone is not evidence of a successful download.

The longer diagnostic run exposed a second transition issue: visible Skip clicks could leave the next preroll paused, while the control loop returned before applying its resume policy. The subsequent change continues through normal player resume after a click, with a regression test for that exact state. This is separate from source verification and does not relax publication checks.
