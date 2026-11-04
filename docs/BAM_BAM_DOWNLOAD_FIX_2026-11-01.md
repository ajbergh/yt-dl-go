# Playback preparation navigation recovery, 2026-11-01

Sample: https://www.youtube.com/watch?v=-8VfKZCOo_I — Camila Cabello, Bam Bam, 256 seconds.

## Reproduction

The running executable reported unknown build metadata and had failed jobs for Best, 1080p Best, and 720p Compatibility. Their history was retained. An isolated baseline 1080p run completed, but an isolated 720p Compatibility run reproduced the read failure: video completed, the first audio attempt exhausted the 30-second page navigation timeout, and the second audio attempt lost its metadata polling context during document navigation. Native AAC fallback requests returned HTTP 403.

The selected H.264 representation is nominally hd1080 but cropped to 1920×720. It is eligible under the app's actual-height 720p ceiling. Validation uses the selected representation's real dimensions; this is not a 360p recovery downgrade.

## Fix

Playback preparation now starts navigation through `Page.navigate` and waits for player readiness instead of the full document load event. A page can have usable media while unrelated requests keep its load event pending.

When metadata polling, playback setup, or playback polling loses its document execution context, preparation restarts against the current document. It reapplies quality and playback setup without another explicit page reload and without extending the original preparation deadline. Permanent errors and cancellation still stop the attempt. Existing fresh-tab recovery remains available for other capture failures.

This change preserves requested source identity, media size/completeness checks, and publication gates.

## Verification

- The real Edge regression fixture navigated during metadata and playback readiness polling and kept the final document's image request pending. Setup recovered three document interruptions and completed. The final fixture additionally asserted that the image request was outstanding and `document.readyState` was not complete; it passed in 6.48 seconds.
- Unit checks passed for navigation recovery, permanent-error propagation, and deadline enforcement.
- Full offline Go suite: passed in 33.954 seconds. `go vet -tags=e2e ./...` passed.
- Final-source isolated 720p Compatibility run: passed in 90.20 seconds, 62,585,643 output bytes, selected 1920×720 MP4, both tracks/endpoints verified through the expected source duration. Its audio setup recovered two navigation interruptions within one capture attempt.
- All three packaged production UI cases passed: 1080p Best, 720p Compatibility, and Best. Each produced the selected 1920×720 MP4 at 62,585,643 bytes, appeared in Library, and passed independent published-file verification. Each track's setup recovered one document interruption without a capture retry; none of these three submissions failed.
- The earlier `41ZY18JqI2A` sample also passed on final source at actual 1080p Compatibility, 76,548,552 bytes, in 82.99 seconds for the case.

Checks establish container structure, dimensions and track endpoints; they do not decode every frame.

The installed candidate reports `6d65025-navigation-fix`, build date `2026-11-01T01:27:00Z`, executable SHA-256 `63111d7a1f24eda859cbc477ff0422d6f397511831d99c025146f683666e21bd`. The original executable backup and ignored diagnostic artifacts are under `.vite-cache/8vf-readiness/`; isolated logs are `src/server/.cache/8vf-*.log`. Do not commit upstream diagnostic payloads.

The patched app was left running normally on port 8080 with tracing disabled. The existing data directory, settings, and three originally failed jobs were preserved. These results validate the reproduced setup failure and the tested samples; they do not establish universal YouTube download reliability.

Run the local browser fixture with `YTDL_TEST_BROWSER` pointing to a Chrome/Edge executable and `go test -run '^TestLiveBrowserPreparationSurvivesNavigationAndPendingLoad$' -count=1 -v`. It uses a local HTTP server and temporary browser profile.
