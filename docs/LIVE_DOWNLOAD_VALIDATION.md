# Live download validation — 2026-10-28

Branch: `fix/live-resolution-matrix`.

The opt-in `TestLiveResolutionMatrix` downloads complete videos through the real job API at every supported quality ceiling: 480, 720, 1080, 1440, 2160, and best. These are maximum actual pixel heights, so cropped films can have lower dimensions than their nominal YouTube labels. The service does not expose separate 144p, 240p, or 360p controls. The matrix does not claim to test every codec strategy or audio-only format.

| Sample | Duration | YouTube source |
| --- | --- | --- |
| Sintel trailer | 52 seconds | https://www.youtube.com/watch?v=HOfdboHvshg |
| Big Buck Bunny 4K / 60 fps | 10 minutes 35 seconds | https://www.youtube.com/watch?v=aqz-KE-bpKQ |
| Sintel | 14 minutes 48 seconds | https://www.youtube.com/watch?v=eRsGyueVLvQ |
| The Ownership Gap (user-supplied additional sample) | 5 minutes 22 seconds | https://www.youtube.com/watch?v=L5LmWDngnjU |

Validation uses Windows, Go 1.27.3, and temporary headless Microsoft Edge sessions. Each case checks finalized file size, actual container dimensions, audio/video presence, and both tracks through their expected duration with three seconds of endpoint tolerance. It does not decode every frame. Downloads use temporary managed-only storage and successful files are deleted after validation.

## Issues reproduced and corrected

- Adaptive MP4 reserved only source-track bytes. The trailer produced 2,244,976 bytes against a 2,236,693-byte reservation and incorrectly failed the 10 GiB job limit. MP4 reservations now include mux headroom.
- Live-test logging dereferenced an absent audio track for progressive selections. Logging now checks for a separate audio track.
- Browser quality requests used cropped pixel heights, producing invalid player quality names such as `hd816` and `hd480`. Requests now use validated representation names and nominal labels.
- Independently consumed browser event queues could process completion before response registration or the final data chunks. Completion is replayed for late registration, and Chrome's completed response body is preferred over a potentially truncated copy. Empty successful control responses and canceled obsolete requests do not abort healthy capture.
- Accelerated playback was assumed in capture deadlines. Capture now allows real-time playback and can advance within the overlap between the browser buffer and the contiguous media prefix already verified for every requested track.
- Multichannel AAC could be selected even though browser playback delivered stereo. MP4 acquisition now tries multichannel natively, then an available stereo AAC fallback, with a job-note disclosure.
- API request preflight could find a completed job that was evicted before handler dispatch, causing intermittent ticket 404s. Active API requests now keep that job resident until dispatch finishes.
- Native transfers sent both a URL `range` parameter and an HTTP `Range` header. A 1 KiB probe returned `Content-Range: bytes 0-1023/1024` instead of the full 224,463,491-byte source size and failed validation. Removing the URL range parameter returned the correct total. Transfers now use the HTTP header alone; the resume regression test rejects duplicate range parameters.
- A timed-out matrix case could leave its job active, making subsequent cases fail with capacity errors. Every case now cancels an unfinished job during cleanup. A host pause of approximately 35 minutes invalidated one run's timeouts; affected cases were scheduled for fresh validation.
- Chrome's default inspector cache evicted larger 1440p media responses. Network capture now requests a 128 MiB per-response cache and a 256 MiB session cache, and preserves those limits during playback preparation.
- Two persistence/queue fixtures still used the production thumbnail fetcher. On a slow network they could exceed their 10-second job deadline and leave cleanup errors. They now disable remote thumbnail capture like the generic offline fixture server.
- A large browser media response could receive bytes for more than a minute before its completed fragments reached the assembler. The idle check now accounts for bytes arriving on recognized media responses; unrelated page traffic does not extend it. The overall capture deadline still applies. A regression test covers incomplete media responses and unrelated traffic.
- Native fallback metadata refresh now has the same two-minute bound as initial metadata lookup. Opt-in tracing identifies browser preparation stages and native range response status to help diagnose upstream failures.

## Results

Testing was paused on 2026-10-29 because of the user's slow network, then resumed at the user's explicit request with the additional sample above. The user subsequently lifted the restriction, and a fresh 4K run verified Big Buck Bunny's `2160` case. Twenty-three cases now have verified complete files, including all six settings for the user-supplied video. Big Buck Bunny's `best` case failed and remains unresolved. The user requested another pause on 4K testing after this run; no retry is running. Partial transfers are not counted as passes.

The 4K changes maintain the requested player quality without resetting unchanged settings, use normal playback speed while advancing only through verified buffered media, and initialize each retry from a blank page with HTTP caching disabled. These changes have now completed a full-file `2160` validation, but `best` remains unresolved.

Results below combine successful post-fix runs and targeted reruns. Early runs exposed the failures listed above; this is not a claim that every upstream request succeeded on its first attempt. Cells show the verified actual height for the requested ceiling.

| Sample | 480 | 720 | 1080 | 1440 | 2160 | best |
| --- | --- | --- | --- | --- | --- | --- |
| Sintel trailer | Pass: 272p | Pass: 546p | Pass: 818p | Pass: 818p | Pass: 818p | Pass: 818p |
| Big Buck Bunny | Pass: 480p | Pass: 720p | Pass: 1080p | Pass: 1440p | Pass: 2160p | Failed; retry paused |
| Sintel | Pass: 364p | Pass: 546p | Pass: 872p | Pass: 872p | Pass: 872p | Pass: 872p |
| The Ownership Gap | Pass: 360p | Pass: 720p | Pass: 1080p | Pass: 1080p | Pass: 1080p | Pass: 1080p |

Big Buck Bunny's MP4 cases disclosed the verified stereo AAC fallback. Higher ceilings on the two Sintel samples selected their highest eligible source rather than upscaling it.

After the network restriction was lifted, Big Buck Bunny's `2160` case passed in 810.06 seconds, producing a 1,372,838,976-byte WebM with verified 3840x2160 video, audio, and both tracks through the expected duration. The `best` case selected the same source but failed in 248.82 seconds: browser capture stopped receiving media, its early retry failed during playback preparation, and native range fallback received repeated HTTP 403 responses at byte offset 142,606,336. The process exited with one passed and one failed case before the pause was handled. No additional retries were launched. The raw run is recorded in the ignored `.cache/unrestricted-4k-matrix.log`.

The user-supplied video completed all six settings in 898.51 seconds. Its 1080p case recovered from a transient browser-capture failure through the existing retry path. It exposes no 480p representation and has a 1080p maximum, so the ceiling behavior shown above is expected. Final sizes were 13,033,381 bytes (360p), 40,122,800 bytes (720p), and 75,277,725 bytes (1080p).

A targeted live rerun against the latest idle-timer, metadata-timeout, and diagnostic changes also passed the user-supplied video's `480` case: 360p, 13,033,381 bytes, with both tracks through their expected duration. The case took 111.74 seconds (143.24 seconds including sample setup and cleanup). The first browser capture returned an authorization error; the existing retry path completed successfully. This rerun does not add a distinct matrix case or validate the pending 4K cases.

Offline verification on 2026-10-29: `go test ./... -skip '^TestLive' -count=1 -timeout 10m` passed against the resumed changes (42.686 seconds), including the incomplete-response idle regression. The earlier full offline run after fixing the persistence fixtures also passed (77.234 seconds). `git diff --check` passed. Live results above were obtained separately; the latest idle-timer and metadata-timeout changes have offline coverage and the targeted live rerun, but have not completed a fresh full live matrix.

The reproduction command and test behavior are documented in [the server README](../src/server/README.md#live-resolution-regression-test). Raw diagnostic logs remain in the ignored `src/server/.cache/` directory and can contain short-lived upstream media URLs; this report intentionally records source watch URLs and aggregate results only.
