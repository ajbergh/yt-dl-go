# Pinball Expo authorization failure

Video: `JRxQbQNpc9M`, “Which Pinball Machine Stole the Show? | Pinball Expo 2026”
(22 minutes 34 seconds). Both 1080 Compatibility and Best failed in the running
application with “media authorization was rejected; retry with a refreshed session”.

## What failed

The authorization message described the last fallback, rather than the first
failure. Browser capture failed first; subsequent direct media requests received
403 responses even after extraction refreshed their URLs.

Tracing reproduced three browser recovery problems:

- Playback initially negotiated a lower resolution. The requested 4K/HD track
  sometimes began at segment 3 rather than segment 1. Rejecting that incomplete
  beginning immediately aborted an otherwise usable session.
- A completed SABR response could contain a short fragment. One observed video
  segment declared 12,227,303 bytes but delivered 1,246,209. Treating that as a
  fatal capture error discarded the session instead of requesting a full copy.
- In another attempt, initialization metadata was missing. Later requested-itag
  media could not safely be selected. Seeking inside the player's existing
  buffer did not reliably produce new initialization or missing media bytes.

An isolated 1080 baseline completed, while an isolated Best run and concurrent
app downloads reproduced the failures. This was not diagnosed from HTTP 403
alone.

## Change

The assembler retains bounded, complete out-of-order segments while requesting
the missing beginning. It drops short fragments without advancing the verified
prefix or writing those fragment bytes to disk. Incomplete headers are cleared
at the response boundary so a later response can reuse their IDs safely.

Recovery seeks to the common verified prefix of both tracks, with overlap.
If initialization is missing, or the first seek does not refill the gap, it
reloads the same video through the player API at that position. It preserves
quality preference and limits attempts at each unchanged prefix. Prerolls,
different video IDs, and mismatched durations do not authorize a repair seek
or reload. Existing format/init digest checks and checkpoint hashes still apply.

Long captures can need more than one fresh browser session. Checkpoint-capable
pair captures now allow up to eight attempts while the verified common prefix
advances. Two consecutive attempts without verified progress stop recovery.
Providers without durable checkpoints retain the two-attempt limit. Cancellation,
storage failures, and the item deadline still stop the operation.

No partial prefix is published as a complete download, and no lower-resolution
fallback was enabled.

Protocol references: the maintained
[segment buffer](https://github.com/LuanRT/googlevideo/blob/main/src/utils/SabrSegmentBuffer.ts)
discards incomplete segments, and YouTube documents loading a specific video at
a requested start time through
[loadVideoById](https://developers.google.com/youtube/iframe_api_reference#loadVideoById).
These informed the recovery design; no implementation was copied.

## Validation

Regression tests cover a missing first segment, missing format/init metadata,
short fragments, response header reuse, repair completion, source/ad guards,
progress-dependent session retries, stalled retries, and the hard attempt cap.
The final full Go suite, vet, and lint passed. A published 1080 MP4 was independently
verified at 505,624,754 bytes, with both streams and the full 22:34 duration.
The final packaged app also completed Best at 3840x2160, publishing a
2,819,009,103-byte WebM. Independent container checks verified both tracks and
the full duration. The 4K run recovered through three capture sessions; its
checkpoints preserved roughly 1.5 GB and then 1.9 GB across the two fresh-tab
retries. It completed without a lower-resolution fallback.

Completed jobs: `3c2a24af958b3182536db9e1d4abd0e0` (1080) and
`a19a5cb8a2208ab87c5ba182a6525ab5` (4K). Publication verification logs are
`src/server/.cache/auth-published-1080-verification.log` and
`src/server/.cache/auth-published-4k-verification.log`. Diagnostic tracing was
disabled after verification.

The running candidate is `6d65025-sabr-repair-final` (SHA-256
`8141b50d2df1e302af6a38a0fb0e3b3daa9875ad08e8df79803c7b85ea4aa81c`).
The prior executable and database are backed up under ignored
`.vite-cache/auth-fix/`; original failed job records and application preferences
are preserved. Raw before/after traces and job IDs are in the same directory;
isolated reproduction logs are under ignored `src/server/.cache/authorization-*`.
