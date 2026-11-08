# Audio download performance

## Implemented acquisition and processing policy

- MP3 sources and original M4A prefer standalone AAC URLs. Successful native
  transfers request only the audio representation, without a watch-page player
  or video payload.
- Browser recovery is acquired lazily. Native startup has a 20-second budget
  only when browser recovery is possible; the budget stops once a verified
  prefix arrives. Normal transfer idle deadlines then apply. Storage, budget,
  cancellation, and rate-limit failures do not trigger browser recovery.
- Authorization rejection is remembered for two minutes per host, extraction
  profile, and itag, with at most 64 entries per server. Other playlist items
  can recover without repeating the same rejected native route. Expiry or a
  native success permits native delivery again.
- Persisted native/browser checkpoints take precedence over this temporary
  transport preference. Native ranges and browser SABR fragments are never
  appended to one another. A failed native attempt is not repeated after its
  browser recovery also fails.
- Audio up to 512 KiB uses one range. Larger sources use four workers with
  256 KiB–1 MiB chunks and separate small buffer pooling. The existing shared
  four-request host cap, global bandwidth limiter, durable-prefix checkpoints,
  and 8 MiB video range policy remain in effect.
- Browser audio recovery requests minimum video quality, including recovery
  playback. This reduces discarded video traffic; it is **not** a guarantee of
  zero video transfer. Browser acquisition remains disabled under a configured
  bandwidth cap because Chrome is outside the shared native byte limiter.
- Artwork fetching overlaps MP3/M4A acquisition, with cancellation and joining
  before cleanup or embedding.
- AAC decoding overlaps ordered MP3 encoding through three reusable PCM buffers.
  Output writes use a 64 KiB buffer, and conversion progress is coalesced to
  200 ms plus a final update. Independent playlist items already run concurrently
  within the configured global item-slot limit.

MP3 encoding uses the encoder's `RateControlFast` gain search. On a 20-second dense stereo
synthetic signal it took 11.5 s instead of 161 s with exact search, with identical CBR output
size; the encoder documents a small, bounded quality cost on well under 0.1% of granules.

A new audio job reuses an already published track instead of downloading and encoding it
again (which previously produced `... (2).mp3`). Reuse requires the same video, output format,
MP3 bitrate, the same Library folder, no captions, no chapter split, and a published file still on disk with its recorded
size. The new Library entry shares that file, so deleting files from one entry removes it for both.

Finished tracks appear in the Library while the remaining playlist items are still running.
Preview, Reveal, and Copy path operate on those finalized files immediately; archive downloads
and file deletion still require the job to stop.

Downloading and converting use separate queues. An item returns its download slot as soon
its AAC source is on disk, so the next track starts downloading while finished sources wait for a
conversion slot. Conversion concurrency defaults to half the CPU cores (1-4); set `CONVERT_SLOTS`
(1-16) to override. Items waiting for conversion keep the `processing` status and carry `waitingToConvert: true`, which the Queue shows as "Waiting to convert". For MP3 jobs the worker pool
is widened by twice the conversion limit so waiting sources never starve downloads; the backlog of
waiting sources is bounded by that pool.

MP3 encoding stays ordered and stateful. Original M4A skips encoding entirely.
The MP3 bitrate controls the output, not the size or quality of the source AAC
download. Source quality is preserved rather than silently lowered.

## Research: obtaining actual audio-only streams

Reviewed 2026-11-08. These are upstream implementations, not guarantees that any
particular client/session will be accepted by YouTube.

| Project | Actual audio-only mechanism | Integration implications |
| --- | --- | --- |
| [yt-dlp](https://github.com/yt-dlp/yt-dlp#format-selection) | `bestaudio` selects a representation with no video codec. | Same principle as our native standalone AAC path. Selecting audio and extracting audio from a combined stream have different bandwidth costs. |
| [YouTube.js](https://github.com/LuanRT/YouTube.js/blob/main/src/utils/FormatUtils.ts) | Selects an audio-only adaptive format, resolves its URL, and fetches that representation. | Confirms URL-based zero-video acquisition; adopting the package would introduce a JavaScript runtime and another extraction stack. |
| [googlevideo](https://github.com/LuanRT/googlevideo/blob/main/src/core/SabrStream.ts) | SABR supports an audio-only track mode with no selected video format. | A useful protocol reference for a future authenticated native SABR transport. It is more than filtering video out of received responses. |
| [WaxTap](https://github.com/ColeSpringer/WaxTap) | Go audio extraction/direct downloading and URL-less WEB-family SABR audio. | Its documented WEB path needs a PO-token provider and matching attested identity/egress. Importing the library alone does not provide that session. No dependency was added. |

The googlevideo request schema identifies
[selected audio/video formats](https://github.com/LuanRT/googlevideo/blob/main/protos/video_streaming/video_playback_abr_request.proto)
and the
[enabled-track bitfield](https://github.com/LuanRT/googlevideo/blob/main/protos/video_streaming/client_abr_state.proto).
The [yt-dlp PO-token guide](https://github.com/yt-dlp/yt-dlp/wiki/PO-Token-Guide)
documents why client, session, and video-specific authorization still matter.

### YTSpoofingStream

Static review of
[YTSpoofingStream at 42b5711](https://github.com/alithw/YTSpoofingStream/tree/42b57116456affea84223f90e1260000d2bbc47d)
adds a smaller browser-side reference implementation. It was not account/live
tested during this review.

- Its `UmpMseController774.buildAbrRequestBytes` sets ClientAbrState field 40 to
  audio-only, selects the Opus audio format in field 16, and does not select a
  video format. It retains signed ustreamer configuration, client information,
  and playback-cookie state. This is an actual audio-only SABR request builder.
- Its parser carries forward NextRequestPolicy playback cookies and follows
  bounded SABR redirects without deciphering their already-signed URLs again.
  These mechanisms are useful for a future standalone audio transport.
- The controller is hardcoded to premium Opus itag 774. It does not supply a
  reusable AAC-140 acquisition backend. TV resolution requires linked TV OAuth;
  the alternative YTM URL harvester relies on an authenticated offscreen Music
  session. The request flags alone do not solve authorization.
- Its separate page-level audio-only mode hides the existing video and requests
  `tiny` quality. That mode alone does not establish zero-video network traffic.
- It paces requests against a 25-second playback buffer, which is a playback
  policy rather than a downloader throughput policy. A downloader should advance
  from verified media coverage and respect server pacing instead of copying
  real-time playback waits.
- The README mentions protection-status parsing, but the inspected demuxer has
  no corresponding part-58 handler. Its empty-response/end heuristics should not
  replace this application's explicit completeness verification.

Relevant source:
[request builder and demuxer](https://github.com/alithw/YTSpoofingStream/blob/42b57116456affea84223f90e1260000d2bbc47d/src/inject.js#L1344),
[session/client resolution](https://github.com/alithw/YTSpoofingStream/blob/42b57116456affea84223f90e1260000d2bbc47d/src/background.js#L748),
[page audio-only mode](https://github.com/alithw/YTSpoofingStream/blob/42b57116456affea84223f90e1260000d2bbc47d/src/inject.js#L5457).
It is Apache-2.0 licensed; this change incorporates the findings, not its code.

A dedicated authenticated SABR audio client is not implemented here. It needs
request generation, session/token handoff and refresh, server redirects and
policy handling, buffering/range state, and complete-delivery verification.
Changing a browser request's track flags alone can stall its player, which
still expects video buffering. The recovery player therefore remains an
explicit minimum-video fallback rather than an unverified audio-only hack.

Network acquisition still completes before M4A decoding: the current decoder
requires a seekable, validated MP4 source. Decoding and encoding overlap locally;
decoding an unverified growing network file is not enabled.

## Validation and measurement

Unit/integration coverage includes parallel ranges, retry and nonaligned resume,
transport selection and cooldown expiry, browser session renewal, persisted
browser resume, rate-limit and bandwidth-cap behavior, artwork joining, bounded
PCM ordering/cancellation, and final progress reporting.

A three-second synthetic stereo AAC comparison produced byte-identical MP3
output: 73,981 bytes. Progress callbacks fell from 117 to 3 and underlying writes
from 117 to 2. Encoding elapsed time was essentially unchanged (about 345 ms);
this establishes lower overhead and fidelity, not a measured speedup.

Live validation of the playlist's first video (`82-jTNka3uc`, about 300 seconds)
completed for both output formats. Native access was rejected, so both used the
minimum-quality browser fallback. MP3 acquisition took 25.655 seconds and
conversion took 204.140 seconds; the job completed in 231.172 seconds with a
7,237,270-byte file. M4A completed in 27.313 seconds with a 4,858,313-byte file.
ffprobe confirmed stereo 44.1 kHz MP3/AAC and the expected duration. This is a
single live sample, not a controlled before/after throughput benchmark; the MP3
encoder remains the dominant cost. Browser capture reported about 7.0 MB of SABR
response data for the 4.86 MB audio source, so zero-video transfer cannot be
claimed for the fallback.

Enable `YTDL_TRACE_PERFORMANCE=1` to log acquisition transport/elapsed time and
conversion elapsed time separately. The opt-in Go test uses test-owned output
directories and can optionally probe the completed file when ffprobe is present:

```powershell
cd src/server
$env:YTDL_LIVE_AUDIO_URL = 'https://www.youtube.com/watch?v=82-jTNka3uc'
$env:YTDL_TRACE_PERFORMANCE = '1'
go test -run '^TestLiveAudioDownload$' -count=1 -v -timeout 12m
```

Set `YTDL_LIVE_AUDIO_FORMAT=m4a` to exercise original AAC passthrough. This test
accepts a single video; it does not download an entire playlist.
