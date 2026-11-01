# Downloader reliability improvements — November 1, 2026

This implements the six production improvements prioritized in
[the downloader research](DOWNLOADER_RESEARCH_2026-11-01.md). The Go extraction,
transfer, and muxing engine remains in use.

## Implementation

| Change | Behavior and safeguards |
| --- | --- |
| Shared MP4 capture | Compatible stereo H.264/AAC tracks now use one browser playback, as WebM already does. Codec policy allows both selected codecs. Both files, exact MP4 source lengths, and the combined reservation are checked before muxing. Multichannel AAC retains its native/stereo fallback policy. |
| Durable SABR recovery | SQLite migration 31 stores compact capture state and a row per committed segment: source identity, format/init digests, sequence, byte offsets, duration, and SHA-256. Only complete, contiguous segments are checkpointed after file sync. Restore rehashes disk bytes, truncates an uncommitted tail, and rejects different representations. Pause and shutdown retain verified data; cancellation removes it. |
| Failure-aware native retries | Distinguishes rejected authorization, proven URL expiry, and HTTP 429. Retries are bounded; server Retry-After is honored, and long waits are surfaced rather than shortened. Source refresh bypasses metadata caching. Sustained low throughput can refresh the source once, without mistaking a configured bandwidth cap for server throttling. |
| Representation identity | Native checkpoints and URL refresh include audio-track ID, extended tags, DRC, and voice boost alongside the existing media properties/profile. Refresh matches the complete fingerprint, rather than the first matching itag. Default audio is preferred over nondefault dubbed tracks. Browser recovery also pins its original SABR format/init digests. |
| Bounded caches and isolated profiles | Concurrent identical metadata requests share a fetch and receive independent values. Metadata caching is limited to 32 entries, 1 MiB per entry, and at most 20 seconds, shortened by media URL expiry. Public player scripts use a version/profile-scoped cache capped at eight 4 MiB entries and 15 minutes, respecting server cache directives and bypassing authenticated requests. Each SDK client owns its profile; requests no longer change a package-global default or serialize unrelated profiles. |
| Parallel native ranges | Two verified 8 MiB ranges per window, with at most four active requests per host across jobs. Responses are bounded and checked before ordered positional writes, sync, and contiguous checkpoint advancement. A failed window discards its uncommitted results. |

The pinned MIT-licensed `kkdai/youtube/v2` dependency is copied locally with two
small compatibility changes: retain the omitted variant fields and support
per-instance profiles. Its original license and
[patch provenance](../src/server/third_party/youtube/UPSTREAM.md) are included.
Generated notices identify the local patch.

Extended validation also exposed two lifecycle problems, now covered by
regressions: shared capture removed checkpoints on pause, and failed browser
attempts kept their tabs playing while a replacement opened. Failed tabs are
now closed before recovery. The browser pool owns process shutdown separately
from job cancellation, requests a graceful close, and bounds the cleanup wait.
If OS teardown stalls, cleanup continues in the background and logs that fact
instead of retaining the database indefinitely.

## Validation

The live matrix downloads complete files through the job API and reads their
actual containers. It checks video dimensions, both tracks, and timestamps near
the source endpoint; successful job metadata alone does not count as a pass.

| Video | Requested ceiling / strategy | Verified output | Bytes |
| --- | --- | --- | ---: |
| Bam Bam (`-8VfKZCOo_I`) | 480 / Best | MP4, 1280×480 | 24,628,033 |
| Bam Bam | 720 / Best | MP4, 1920×720 | 62,585,643 |
| Bam Bam | 1080 / Compatibility | MP4, 1920×720 | 62,585,643 |
| Bam Bam | Best / Best | MP4, 1920×720 | 62,585,643 |
| Hips Don't Lie (`41ZY18JqI2A`) | 480 / Best | MP4, 854×480 | 18,394,340 |
| Hips Don't Lie | 720 / Best | WebM, 1280×720 | 28,952,974 |
| Hips Don't Lie | 1080 / Compatibility | MP4, 1920×1080 | 76,548,552 |
| Hips Don't Lie | Best / Best | WebM, 3840×2160 | 458,764,159 |

Bam Bam's 1080 playback tier is a cropped 1920×720 representation. The height
checks use its selected source dimensions.

All eight cases passed. The extended matrix included a successful checkpoint
recovery and a slow Bam Bam Best case that exhausted two shared-capture setup
attempts before succeeding through serial capture. That observation prompted the
failed-tab cleanup fix. Both final packaged UI checks passed on their first
capture attempt after that fix: Bam Bam Best (44.605-second shared capture) and
Shakira 1080 Compatibility (38.492-second shared capture). Their published
containers passed the same independent track, dimension, and duration checks.

The real restart test pauses after both tracks have durable media, closes and
reopens the SQLite-backed server, then resumes and validates the finished file.
One run restored 6,534,179 video bytes and 809,301 audio bytes and produced the
same 62,585,643-byte MP4. It also verifies completed checkpoints are removed.
Synthetic tests cover modified bytes, different format digests, duplicate and
reordered segments, uncommitted tails, pause retention, parallel range ordering,
combined quota checks, profile isolation, cache expiry, and cancelable 429 waits.

Other checks passed:

- Full offline Go suite and `go vet ./...`; E2E-tagged vet also passed during validation.
- Pinned `golangci-lint`: zero issues.
- `govulncheck ./...`: no reachable findings. One required-module advisory was
  reported outside the code paths used by the application.
- Frontend typecheck, production build check, and lint; 69 integration tests.
  Existing React refresh/test warnings remain.
- License checks and notice generation completed for Linux amd64, Windows
  amd64, and macOS arm64 (28 packages). Generated license files had no content
  drift; the report adds only the intended local-patch disclosure. Windows
  cleanup initially failed on copied read-only files; the generator now clears
  those attributes in its owned temporary output and retries transient removal.

Raw test logs are under ignored `src/server/.cache/improvements-*`; packaged UI
reports, executable backups, and upgrade evidence are under ignored
`.vite-cache/improvements/`.

The installed local build is `6d65025-reliability`, SHA-256
`09f68798f06be94e9f88b55c1722593237b4b0b588652caf74dca2b3be67befc`.
It is running at `http://127.0.0.1:8080/` with tracing disabled. The previous
executable and SQLite files are backed up. The migration also created its
automatic version-30 snapshot before applying version 31. A read-only comparison
of the actual pre-upgrade backup and current database confirmed both existing
job/library records survived; the two new successful UI jobs bring the total to
four. All saved application preferences are unchanged.

## Performance and remaining work

The initial shared-capture run took 45.838 seconds for Bam Bam and 44.885 seconds
for Shakira. The earlier Bam Bam serial run spent 42.941 seconds on video and
41.599 seconds on audio. These are observations from individual runs, not a
controlled median/p95 comparison or a general speed guarantee. The slow recovery
case above remains part of the results.

The research's seventh recommendation, direct SABR independent of rendering,
remains a separate architectural follow-up as proposed there. This change does
not add an incomplete protocol client or change the default session/attestation
provider. A useful prototype still needs session bootstrap, request scheduling,
redirect/backoff handling, token refresh, and independent live equivalence tests.

Before a release, CI should run the Linux race suite and all supported release
targets. Broader repeated tests of long videos, multiple audio variants, account
restrictions, and interrupted 4K transfers are still needed. These changes do
not make private, unavailable, live, or unsupported manifest-only videos
downloadable, nor guarantee that every public YouTube video will succeed.
