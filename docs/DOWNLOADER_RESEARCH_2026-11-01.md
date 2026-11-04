# Downloader reliability and speed research, 2026-11-01

The highest-value changes for this branch are one-pass H.264/AAC capture, retained verified SABR segments, and explicit handling of authorization/expiry failures. Bounded native range concurrency is useful after a stream is demonstrably downloadable. A direct SABR transport is the larger architectural opportunity.

These are recommendations from source review, not implemented performance improvements. No comparative download benchmark was run for this research.

## Maintenance verification

Public GitHub API checks verified archive status, default-branch commits, and licenses. The ignored snapshot is `.vite-cache/downloader-research/maintenance.json`. Functional changes were checked separately from dependency-bot commits.

| Project | Verified activity | Relevance |
| --- | --- | --- |
| [yt-dlp](https://github.com/yt-dlp/yt-dlp) | [2026.10.26 release](https://github.com/yt-dlp/yt-dlp/releases/tag/2026.10.26); default branch updated October 29 | Main reference for extraction, transfer policy and regression coverage |
| [YouTube.js](https://github.com/LuanRT/YouTube.js) | [18.3.0 release](https://github.com/LuanRT/YouTube.js/releases/tag/v18.3.0), October 23; branch updated October 31 | Player interpretation, client/session handling and caching; an extraction library rather than a standalone downloader |
| [googlevideo](https://github.com/LuanRT/googlevideo) | [5.0.1 release](https://github.com/LuanRT/googlevideo/releases/tag/googlevideo-v5.0.1), October 23; branch updated October 31 | Direct SABR/UMP transport and downloader examples |
| [NewPipeExtractor](https://github.com/TeamNewPipe/NewPipeExtractor) | [Functional YouTube throttling fix](https://github.com/TeamNewPipe/NewPipeExtractor/commit/e8f89145a830b4d6cae32412007f24ea8b36c73a), October 22; branch updated October 26 | Current extraction fixes, explicit stream variants and error types; extraction library |
| [kkdai/youtube / youtubedr](https://github.com/kkdai/youtube) | [Download change](https://github.com/kkdai/youtube/commit/3876742b95fd1cd96f836f396c2d54be8912a297), August 29; latest release v2.10.6 dated March 21 | Existing Go dependency, useful transfer implementation; slower cadence than the October-active projects |

[Cobalt's latest public main-branch commit](https://github.com/imputnet/cobalt/commits/main/) was April 6. It remains a useful secondary design reference, but is not evidence of current rapid maintenance. Its [YouTube implementation](https://github.com/imputnet/cobalt/blob/main/api/src/processing/services/youtube.js) separates sessions from requests, periodically refreshes player data, and bounds extracted-JavaScript execution. Those patterns are useful without selecting it as our primary upstream.

## What the maintained projects actually do

**yt-dlp:** its [YouTube extractor](https://github.com/yt-dlp/yt-dlp/blob/master/yt_dlp/extractor/youtube/_video.py) batches signature and `n` challenges, caches results, and skips formats lacking required tokens. Its [HTTP downloader](https://github.com/yt-dlp/yt-dlp/blob/master/yt_dlp/downloader/http.py) validates range responses, resumes partial transfers, varies chunk lengths, and detects sustained low throughput. Its [fragment downloader](https://github.com/yt-dlp/yt-dlp/blob/master/yt_dlp/downloader/fragment.py) tracks fragment progress and supports bounded concurrency. `-N` applies to fragment downloads; it is not a universal parallel-HTTP switch.

Its [PO Token Guide](https://github.com/yt-dlp/yt-dlp/wiki/PO-Token-Guide) distinguishes player, media-server and subtitle token requirements and explains client/video binding. Its [EJS documentation](https://github.com/yt-dlp/yt-dlp/wiki/EJS) describes maintained challenge solvers running in a JavaScript runtime. A valid-looking URL is not evidence that its session, token, and challenge state are usable.

**YouTube.js:** [Player.ts](https://github.com/LuanRT/YouTube.js/blob/main/src/core/Player.ts) extracts player logic through an AST-based analyzer, caches by player identity, and rejects cache entries from a different library version. That is a useful model for a versioned challenge-provider interface and cache invalidation, instead of repeatedly downloading/interpreting player data.

**googlevideo:** its [VOD example](https://github.com/LuanRT/googlevideo/blob/main/examples/downloader/vod-example.ts) consumes audio and video concurrently. [SabrStream](https://github.com/LuanRT/googlevideo/blob/main/src/core/SabrStream.ts) directly requests both tracks, handles server backoff, redirects, expired streaming data and token refresh callbacks, and supports snapshot restoration. This offers a route away from driving a rendered video element. The downloader still needs working extraction, session and attestation inputs; installing a protocol library does not supply them automatically.

Its [segment buffer](https://github.com/LuanRT/googlevideo/blob/main/src/utils/SabrSegmentBuffer.ts) rejects partial segments and tracks completed ones. Its [format utilities](https://github.com/LuanRT/googlevideo/blob/main/src/utils/formatUtils.ts) distinguish extended tags, language, DRC, voice boost and audio identity. [NewPipe's stream extractor](https://github.com/TeamNewPipe/NewPipeExtractor/blob/dev/extractor/src/main/java/org/schabi/newpipe/extractor/services/youtube/extractors/YoutubeStreamExtractor.java) likewise retains audio-track IDs, DRC and extended tags and distinguishes several unavailable-content errors.

**kkdai/youtube:** [client.go](https://github.com/kkdai/youtube/blob/master/client.go) has a bounded chunk-worker implementation. We already configure its stream client with four routines and 1 MiB chunks. Our separate adaptive range loop does not use that worker pool, so increasing `MaxRoutines` alone would not parallelize that path.

## Gaps in this branch and proposed order

### 1. Capture H.264 video and AAC audio together

`transferAdaptiveMP4` downloads the video track, then starts a separate audio capture. WebM already uses `CaptureTracks` to assemble both from one playback. Generalize that mechanism to compatible H.264/AAC pairs, with a combined codec policy and shared verification/cleanup logic.

This is the most contained speed experiment. In the recent Bam Bam isolated run, video capture took 42.941 seconds and audio 41.599 seconds. Overlapping equivalent work suggests a theoretical capture-stage improvement from their sum toward their maximum; that is not a measured speedup or a promise of twice the throughput. Browser/server scheduling and codec negotiation can change the result.

Acceptance: same selected formats and output quality, verified tracks/endpoints, no increased failure or recovery rate, and reduced median/p95 total time. Cover multichannel AAC, stereo fallback, cancellation, one-track failure and combined quota enforcement.

### 2. Retain verified SABR segments across recovery

Our native range path persists checkpoints, but failed browser captures are discarded. Browser-produced parts are not eligible for the native-range resume path. Apply the snapshot/completed-segment model to our SQLite-backed recovery: persist track identity, initialization data, contiguous verified prefix, segment hashes and byte offsets. A new session must revalidate identity before reuse. Publish only after both tracks are complete.

This is our persistence design proposal, not a claim that the upstream example already implements our durable-storage requirements. Test late interruption, browser crash, process restart, duplicated/reordered segments and changed format identity. Measure re-downloaded bytes as well as completion time.

### 3. Handle failure classes and session validity explicitly

The custom range loop has three attempts and refreshes metadata on retry, but treats many transfer failures similarly. Add distinct decisions for expired URLs, missing/rejected authorization, throttling, transient transport/5xx failures and unavailable content. A 403 alone does not identify which condition occurred. Repeatedly requesting a forbidden URL with more workers is not a remedy.

Keep extraction profile, headers, visitor/session context, player version, video ID and token scope together. Re-extract on proven expiry or sustained throttling; use bounded backoff for transient failures and honor server-directed delay. Permanent unavailable-content errors should stop cleanly. Existing source-fingerprint checks must continue to prevent appending bytes from a different representation.

### 4. Preserve full format identity during selection and refresh

`refreshFormat` currently selects the first matching itag. The native fingerprint includes useful properties and last-modified time, but not audio-track ID or extended tags; our pinned dependency's format model also omits explicit DRC/xtags fields. SABR already pins a format digest and video identity—retain that protection.

Extend normalized metadata and selection to distinguish original/dubbed/descriptive audio, language, DRC and other variants. Refresh the exact requested variant, and restart safely if its identity changes. Test two same-itag audio streams with different languages/variants and equal sizes; byte count alone must not establish equivalence.

### 5. Reuse player/session caches with bounded invalidation

`withYouTubeProfile` currently creates a new library client for each call, throwing away its per-client player cache. Inspection, job startup and URL refresh can repeat work. Introduce per-profile client/session state, versioned player caches and deduplication of concurrent requests for identical metadata. Cache lifetime must respect URL expiry and challenge changes.

Our adapter temporarily changes the dependency's global default client while holding a mutex. Removing that lock would introduce races. Per-instance profile configuration or a narrowly reviewed upstream adapter change is needed before expecting concurrent metadata extraction to scale.

### 6. Add conservative parallelism to verified native ranges

The custom fallback downloads 8 MiB ranges sequentially. Experiment with two to four workers, a bounded reorder buffer or verified positional writes, a single metadata-refresh operation per source, and global per-host/job limits. Preserve range validation, quota accounting, cancellation and resume integrity. Consider measured throughput and rate-limit responses when adjusting concurrency.

We already have HTTP chunking, backoff, a browser pool and some parallel streaming. The proposed improvement is specific to the custom adaptive range path. Do not expect it to fix browser negotiation or authorization failures.

### 7. Prototype direct SABR behind the current provider interface

Use the MIT-licensed protocol implementation as a reference for a Go transport, while preserving the existing browser provider as fallback. Bootstrap current session/attestation inputs through an explicit provider; fetch media independently of video rendering. Keep raw-media assembly and current verification gates.

This has the largest architectural scope: session lifecycle, protocol requests, redirects, server policy, checkpoints and token callbacks all need coverage. Start with a diagnostic prototype and compare exact outputs before choosing it as the default. It should be a separate change from the MP4 optimization.

## How to establish actual improvement

Use both recent failing music videos, existing animation/long-video samples, multi-audio and higher-resolution cases. Separate cold browser/session runs from warm ones and run each configuration repeatedly on the same network. Compare the same codec, resolution, audio variant and final duration.

Record first-attempt success, final success, internal recovery count, stage timings, median/p95 completion time, useful throughput, total upstream bytes, CPU and peak memory. Exercise interrupted transfers and restart/resume. Preserve failed attempts in results. A faster lower-quality file or a file with missing media is a failed comparison.

Use a pinned yt-dlp configuration as an external reference benchmark when its runtime dependencies are available. Its [documented dependencies](https://github.com/yt-dlp/yt-dlp#dependencies) include a JS runtime for current full YouTube support and FFmpeg for merging separate tracks. Bundling it would change this project's deployment model; borrowing its transport/extraction patterns can preserve our Go engine and muxers.

The current licenses verified through repository metadata are MIT for YouTube.js, googlevideo and kkdai/youtube; GPL-3.0 for NewPipeExtractor; AGPL-3.0 for Cobalt; and Unlicense for yt-dlp's repository, with additional licenses in some distributions. Review the actual files and distribution notices before copying code. This research adds no third-party implementation or runtime dependency.
