# Native Go download service

This directory contains the local HTTP service compiled into the `youtube-downloader` executable (`youtube-downloader.exe` on Windows). The executable serves both this API and the embedded React UI from one origin; the UI connects automatically and reads/writes its job history and preferences through the service. The server validates requests, manages download jobs, and streams finalized files. It uses `github.com/kkdai/youtube/v2` for metadata and compatible direct streams, `chromedp` for browser-assisted adaptive video delivery, and `gomedia` to remux MP4 tracks. Audio-only jobs can either preserve the source AAC-in-MP4 stream as M4A with no transcoding or decode AAC and encode tagged MP3 entirely in Go; no FFmpeg or other audio executable is required.

This is a YouTube-only, local/single-user downloader. Download only content you own or are authorized to save. It does not import credentials or cookies from your normal browser profile, perform DRM decryption or paywall bypasses, or record live/HLS/DASH streams. Adaptive capture uses a temporary browser session that YouTube may authorize for that run.

## Run and build

The bundled UI is embedded from `dist/` in this directory. For development, run:

~~~
go mod download
go test ./...
go run .
~~~

To build the Windows executable after refreshing the frontend assets:

~~~
$env:CGO_ENABLED = '0'
go build -buildvcs=false -trimpath -ldflags='-s -w' -o ..\..\dist\youtube-downloader.exe .
~~~

The repository-level packaging scripts build the embedded frontend, validate the native host, and package Windows/Linux/macOS output with checksums. See [../../docs/PACKAGING.md](../../docs/PACKAGING.md).

Go 1.26 or newer is required. The process listens on `127.0.0.1:8080` by default, serves the SPA at `/`, and asks the operating system to open that address in the default browser. `ADDR` changes the listener and browser URL. If automatic browser launch is blocked, visit the configured address manually. The built-in UI uses the same origin, so it also follows a custom `ADDR`.

## Format selection and adaptive HD / 4K path

`best`, `2160`, `1440`, `1080`, `720`, and `480` are maximum heights. Video jobs also carry `videoStrategy`: `best`, `compatibility`, `vp9`, or `av1`. `compatibility` is strict MP4 and selects only progressive MP4 or adaptive H.264 + AAC MP4. `best` preserves the automatic policy: through 1080p it favors the compatibility-oriented H.264/AAC path when competitive, while higher ceilings can use adaptive VP9 or AV1 WebM video with Opus audio. At equal WebM resolution and frame rate the automatic policy prefers VP9 for broader decoder compatibility. `vp9` and `av1` explicitly prefer that codec within the quality ceiling; when unavailable, the selector falls back to the automatic best-supported representation and adds a disclosure to the job note. Audio-only jobs select a compatible AAC-in-MP4 stream. `audioFormat:"m4a"` preserves that stream directly with no lossy generation; `audioFormat:"mp3"` converts it at the requested bitrate using the in-process Go AAC decoder and MP3 encoder. Other source audio codecs are not currently selected.

For adaptive video, the service creates a temporary, headless Chrome-compatible session when direct/range delivery is insufficient. The browser obtains its own short-lived authorization while loading YouTube, and the service configures playback toward the requested H.264, VP9, AV1, or Opus codec family. It captures the selected SABR/UMP media fragments through the declared final fragment and verifies the selected itag and completion boundary. H.264/AAC tracks are remuxed to MP4; VP9/AV1 + Opus tracks are timestamp-interleaved into seekable WebM with the pure-Go `ebml-go` library.

Chrome, Chromium, or Edge must be installed for this path. Discovery is automatic, or set `CHROME_PATH` to the executable. The temporary profile is not the user's normal profile and is discarded at the end of the job; the downloader does not import or persist the user's account credentials. This is distinct from the optional Go API bearer token documented below.

If browser capture or adaptive byte-range delivery fails, the service uses the highest verified progressive MP4 when one is available and adds a fallback explanation to the job note. A finished lower-resolution fallback is preferable to a corrupt partial HD file. Some videos cannot expose an eligible compatible track; their item fails explicitly.

## Caption sidecars

Inspection exposes the validated caption tracks reported by YouTube. A job may opt into one `subtitleLanguage` and choose `subtitleFormat` as `vtt` or `srt` (VTT is the default once a language is selected). Manual tracks are preferred over auto-generated ASR for the same language.

Caption retrieval stays inside the native Go service: only HTTPS YouTube `/api/timedtext` URLs are accepted, redirects are rejected, responses are capped at 5 MiB, and SRT conversion is performed in process. Caption URLs are never persisted. A caption failure is nonfatal to the media item; the finalized file receives `subtitleError` so the UI can disclose the sidecar-specific problem without converting a successful media download into a failed job.

Managed sidecars use the finalized media basename plus `.<language>.vtt` or `.<language>.srt`. Published sidecars use the resolved published media basename and live in the same output folder. Whole-job ZIP tickets include available managed sidecars. Playlist inspection samples an accessible entry to present language choices, while the worker resolves that selected language independently for every selected playlist item.

## Configuration

| Variable | Default | Validation / effect |
| --- | --- | --- |
| `ADDR` | `127.0.0.1:8080` | Valid `host:port`; non-loopback requires a token and explicit hosts |
| `DATA_DIR` | `./downloads` | Private real directory; each job receives a random subdirectory |
| `API_TOKEN` | unset | Bearer token for protected API routes; non-loopback bindings require at least 32 non-whitespace characters. The bundled UI does not send a token, so leave unset when using it. |
| `ALLOWED_ORIGINS` | local Vite and service origins | Exact comma-separated HTTP(S) origins; no wildcards or trailing slash |
| `ALLOWED_HOSTS` | loopback authorities at listener port | Additional exact `host[:port]` authorities |
| `MAX_JOBS` | `32` | 1 through 1000 retained, queued, and active jobs combined |
| `MAX_JOB_BYTES` | `10737418240` | Positive per-job media byte budget (10 GiB by default) |
| `JOB_TIMEOUT` | `6h` | Whole-job deadline; at least one second |
| `RETENTION` | `24h` | Terminal-job retention; at least five minutes |
| `CHROME_PATH` | unset | Chrome/Chromium/Edge executable for adaptive capture |
| `NO_BROWSER` | unset | Legacy/automation switch; `1` suppresses automatic OS browser launch |

Runtime arguments `--background` and `--no-browser` are supported aliases that suppress automatic browser launch while keeping the HTTP service/workers in the foreground process. The service logs the UI URL so it can be opened manually later. Unknown runtime arguments are rejected instead of being silently ignored. `NO_BROWSER=1` remains supported for CI and existing automation.

The scheduler defaults to three concurrent media items and allows a persisted `maxConcurrentDownloads` preference from 1 to 6. Queued jobs carry a persisted `queuePosition`; the in-memory channel only wakes the scheduler, which always selects the lowest-position queued job next. Reordering never interrupts or changes already-active jobs. A persisted `bandwidthLimitBytesPerSec` preference applies one global inbound-media cap across active Go-managed transfers; `0` is unlimited. Limited reads use FIFO grants so concurrent transfers share the cap rather than allowing one stream to monopolize tokens. Because browser-assisted adaptive capture occurs outside the Go read loop, that fast path is bypassed while a bandwidth cap is active and the Go range downloader is used instead. Playlist entries use the same shared limit as separate jobs, so one playlist can download multiple entries at once. Explicit queued-item order is preserved for playlist execution, while original playlist positions remain attached to each item for filenames and metadata. Progress reports the combined active transfers. Each native stream can use up to four transfer routines. It writes exclusive temporary files, checks byte limits and declared stream sizes, then atomically finalizes files only after successful completion. Job configuration, history, finalized-file metadata, user preferences, and resumable item state are stored in the pure-Go SQLite database at `DATA_DIR/state.db`; bearer tokens and signed media URLs are never stored. A restart re-queues interrupted jobs, leaves explicitly paused jobs paused, skips validated finalized playlist items, and resumes completed adaptive byte ranges when the source still permits them. Service shutdown preserves a safe adaptive source part, while explicit user cancellation removes partial output but keeps files finalized earlier in the same job.

The MP3 encoder emits constant-bitrate audio with native ID3v2.3 metadata written before the encoded frames. Tags include available title, artist/channel, playlist album and track position, publish date, canonical source URL, and locally captured cover artwork when it is JPEG/PNG/WebP and no larger than 1 MiB. Tagging is best-effort and never makes otherwise valid audio fail. Encoding quality and compression efficiency differ from LAME; available bitrates are 128, 192, 256, and 320 kb/s. The API health response reports `mp3AudioSupported` and `pureGoAudioConversion` when this built-in path is available.

Ticket links last five minutes. Individual-file transfers support one byte range; ZIP downloads stream finalized files without building a duplicate archive in memory. Completed jobs are removed after retention unless an active transfer or unexpired ticket still holds them.

## API

Control-plane API responses and errors return JSON. `GET /api/downloads/{ticket}` instead streams a file or ZIP. When `API_TOKEN` is configured, send `Authorization: Bearer TOKEN` except for `GET /api/health`, approved CORS preflight requests, and ticket download links. API JSON bodies must be one object no larger than 4096 bytes and cannot include unknown fields. The bundled UI sends no bearer token; if one is configured, use a token-capable external client or leave the token unset for the built-in UI.

`GET /api/events` is a long-lived `text/event-stream` endpoint and remains bearer-protected like the rest of the control plane. It sends an initial `snapshot` containing the current jobs and settings, then emits `job-created`, throttled `job-progress`, `job-status`, `job-file-finalized`, `job-error`, `job-deleted`, and `settings-changed` events. Heartbeat comments keep idle connections alive. Slow clients use a bounded event buffer; the bundled UI also performs a 30-second full-job reconciliation so a dropped event cannot permanently desynchronize state. The frontend consumes SSE through authenticated `fetch` rather than native `EventSource`, preserving bearer-header support for external/tokenized use. SSE requests are exempt from the normal short response write deadline so a healthy stream can remain connected indefinitely.

| Endpoint | Purpose |
| --- | --- |
| `GET /api/health` | Reports the native engine and capabilities |
| `POST /api/inspect` | Inspects a video or playlist and returns metadata, supported quality ceilings, and available/sample caption tracks |
| `GET /api/settings` | Reads persisted UI preferences |
| `PUT /api/settings` | Saves validated preferences to SQLite |
| `POST /api/folders/select` | Opens the local OS folder picker and returns the selected absolute folder; `{}` body |
| `POST /api/jobs` | Creates a download job; returns `202` and the job |
| `GET /api/jobs` | Lists jobs, newest first |
| `GET /api/events` | Authenticated Server-Sent Events stream with an initial snapshot and live job/settings updates |
| `GET /api/jobs/{id}` | Returns one job |
| `POST /api/jobs/{id}/pause` | Pauses a queued or active job |
| `POST /api/jobs/{id}/resume` | Resumes a paused job |
| `POST /api/jobs/{id}/cancel` | Cancels a queued or active job |
| `POST /api/jobs/{id}/retry` | Creates a fresh job from a failed, partial, or cancelled job |
| `POST /api/jobs/{id}/retry-item` | Re-queues exactly one failed/cancelled playlist item using `{"index":N}` while preserving successful sibling files |
| `PUT /api/queue/order` | Reorders all currently queued jobs atomically using `{"jobIds":[...]}` |
| `POST /api/jobs/{id}/next` | Moves one queued job to the front of the queued-job order |
| `PUT /api/jobs/{id}/items` | Reorders a queued playlist using original `playlistIndexes` |
| `DELETE /api/jobs/{id}` | Removes a stopped job from the Library and deletes app-managed copies while preserving published output |
| `DELETE /api/jobs/{id}/managed` | Deletes only app-managed media copies and keeps Library history plus published output |
| `DELETE /api/jobs/{id}/published` | Deletes only tracked copies in the configured output directory and keeps managed Library media |
| `DELETE /api/jobs/{id}/all` | Explicitly deletes managed media, published output, and Library history |
| `POST /api/jobs/{id}/filesystem` | Performs a path-validated `copy-path`, `reveal`, or `open-folder` action for one tracked published file |
| `POST /api/jobs/{id}/ticket` | Creates a five-minute link for a job ZIP or one file |
| `GET /api/jobs/{id}/thumbnail?fileId=FILE_ID` | Serves captured local thumbnail artwork for one finalized file through authenticated API access |
| `GET /api/downloads/{ticket}` | Streams the ticket's archive or file |

Create a video job with:

~~~json
{
  "url": "https://www.youtube.com/watch?v=VIDEO_ID",
  "quality": "1080",
  "rightsConfirmed": true
}
~~~

For audio-only output, include `"mediaType":"audio"` and optionally set `"audioFormat":"mp3"` (the backward-compatible default) or `"audioFormat":"m4a"`. MP3 jobs may choose a bitrate, for example `"audioBitrate":"256k"`; M4A jobs preserve the selected AAC source stream and ignore MP3 bitrate. A job may also include `"category":"Music"`; the value must match one of the configured `userCategories`. If `category` is omitted, the current `defaultCategory` is captured for backward compatibility. The service reports built-in pure-Go MP3 capability through `GET /api/health`.

To request a caption sidecar, include a language code returned by inspection, for example `"subtitleLanguage":"en"`, and optionally `"subtitleFormat":"srt"`. Supported formats are `vtt` and `srt`; VTT is used when a language is supplied without an explicit format. `subtitleFormat` is rejected when no language is selected.

Valid qualities are `best`, `2160`, `1440`, `1080`, `720`, and `480`. `mediaType` is optional and defaults to `video`; set it to `audio` for audio-only output. For audio jobs, `audioFormat` defaults to `mp3` and accepts `mp3` or `m4a`. MP3 `audioBitrate` defaults to `192k`; accepted values are `128k`, `192k`, `256k`, and `320k`. M4A output uses the source AAC bitrate without transcoding. `category` is optional; when supplied it must match an active configured user category and is captured with the job for output naming and category-based folder routing. The URL must be an HTTPS YouTube or `youtu.be` video, shorts, live, or playlist URL with valid IDs. A watch URL containing `list=` is treated as a playlist. Playlist inspection returns the full exposed `entries` array with one-based original indexes. A playlist job may omit `items` to process every exposed entry, or provide a selected subset where each item includes its original `index` and 11-character video `id`. The worker re-fetches playlist metadata and rejects stale or mismatched selections before downloading. Selected items default to original playlist order. Before a job starts, users may explicitly reorder queued playlist items; queue indexes are reassigned contiguously for progress/resume while managed filenames and MP3 track numbers still retain each item's original playlist index. Inaccessible or hidden entries cannot be independently counted and are disclosed in the job note.

Inspect a link with `POST /api/inspect` and `{"url":"https://www.youtube.com/watch?v=VIDEO_ID"}`. Video inspection returns title, channel, duration, safe thumbnail URL, publish date, supported quality ceilings, whether a standalone audio stream is available, and validated caption-track metadata. Playlist inspection returns its exposed item count and up to ten preview entries, and samples up to five accessible entries until it finds caption tracks for the selection UI; it does not expose signed media or caption URLs. Preferences support `defaultQuality` (`best`, `2160`, `1440`, `1080`, `720`, or `480`), `defaultVideoStrategy` (`best`, `compatibility`, `vp9`, or `av1`), `maxConcurrentDownloads` (1–6), `bandwidthLimitBytesPerSec` (`0` for unlimited, otherwise up to 1 GiB/s), an absolute `downloadLocation`, a `namingPattern` with `{channel}`, `{title}`, `{resolution}`, and `{category}` tokens, `subfolderSorting` (`channel`, `category`, or `flat`), a `defaultCategory`, up to 50 `userCategories`, and `storageMode` (`managed-published`, `published-only`, or `managed-only`). They are stored in SQLite. Each job captures its selected `videoStrategy` plus output and storage preferences when queued, and whole-job retries preserve that strategy. `managed-published` keeps both copies, `published-only` removes the private finalized media after a successful publish, and `managed-only` skips publishing to the configured output folder. Published output is treated as user-owned media: normal Library removal and retention pruning preserve it. Explicit scoped delete endpoints are required to remove published files. Finalized jobs also make a best-effort capture of validated YouTube thumbnail artwork (JPEG, PNG, or WebP, maximum 5 MiB) through the guarded HTTPS client. Artwork capture failure does not fail the media job, and the original approved remote thumbnail URL remains fallback metadata.

Pause and resume use `POST` with `{}`. A paused active job keeps finalized files; an interrupted progressive item may restart on resume, while supported adaptive byte ranges can resume. Retry requires `{"rightsConfirmed":true}` and creates a new job so the original result and error history remain inspectable. For stopped playlist jobs, `POST /api/jobs/{id}/retry-item` accepts `{"index":N}` only for a failed or cancelled queue row. It reuses the same job, preserves valid finalized siblings and their file IDs, persists the retry marker in `queue_items`, retries only the targeted row, and can survive a restart without expanding into a full-playlist retry. `DELETE /api/jobs/{id}` is allowed only after the worker stops and no file transfer is active; it removes Library history and private app-managed media but preserves published output. `DELETE /managed` removes only the managed copies, `DELETE /published` removes only validated tracked output copies, and `DELETE /all` explicitly removes both copy sets plus history.

A job includes `id`, `url`, `kind`, `quality`, `mediaType` (`video` or `audio`), `audioFormat`, `audioBitrate`, optional `subtitleLanguage`/`subtitleFormat`, `category`, `storageMode`, `status`, `title`, `progress`, byte counts, transfer speed, ETA, `currentItem`, `activeItemCount`, playlist counts, `files`, `error`, `createdAt`, `note`, and `failures`. For parallel transfers, progress and byte counts aggregate the active items; `currentItem` shows an active title and how many more are running. Audio jobs produce tagged `.mp3` files or original-AAC `.m4a` files according to `audioFormat`. File objects include `id`, `name`, `size`, `height`, and `mimeType`, plus available title, author, duration, remote thumbnail fallback, publish-date metadata, `managedAvailable`, and `publishedAvailable` so clients can distinguish the two copy locations. When requested, `subtitle` describes the finalized sidecar (language, label, format, size, managed/published availability and relative output path); `subtitleError` reports a nonfatal caption-specific failure. When durable artwork was captured, `thumbnailLocalAvailable` and `thumbnailMimeType` describe the private local copy; clients should fetch it through the authenticated thumbnail endpoint rather than using a filesystem path. Per-item `failures` use one-based indexes.

Create a ticket with `{}` for a ZIP or `{"fileId":"FILE_ID"}` for a single finalized file. Add `"inline":true` only with a `fileId` to create a 30-minute preview ticket; ordinary download tickets expire after five minutes. The response is `{"path":"/api/downloads/TICKET"}`. Open that path relative to the service origin without adding the bearer token to the URL. Single-file tickets support one HTTP byte range; inline tickets use `Content-Disposition: inline` so browser media controls can seek without loading the whole file into JavaScript memory.

For local filesystem actions, post `{"fileId":"FILE_ID","action":"reveal"}` (or `open-folder` / `copy-path`) to `/api/jobs/{id}/filesystem`. The server resolves the path exclusively from persisted file metadata, re-validates that it remains beneath the job’s configured output location, verifies the file still exists with the expected size, and never accepts an arbitrary client-supplied path. `POST /api/folders/select` invokes the native interactive folder chooser on Windows and macOS; Linux uses `zenity` when installed. A cancelled picker returns `204`. Reveal/open uses Explorer on Windows, `open`/`open -R` on macOS, and `xdg-open` on Linux; Linux reveal opens the containing folder because there is no portable freedesktop file-selection command.

## Version and update API

`GET /api/health` includes `version`, `commit`, and `buildDate`. Ordinary local/branch builds report `dev`, `unknown`, and `unknown`; tagged release builds inject immutable values through the package build scripts.

Authenticated `GET /api/update` returns the current version, the latest stable GitHub Release when available, whether an update exists, the validated release URL, and `automaticUpdate:false`. Development builds do not contact GitHub and return `developmentBuild:true`. Release checks use a six-second timeout, reject redirects, bound the response body to 64 KiB, require semantic-version tags, reject drafts/prereleases, and accept release links only under `https://github.com/ajbergh/yt-dl-go/releases/`.

The endpoint never downloads an executable and never applies an update.

## Operational guidance

Keep the listener on loopback where possible. The default bundled UI is intended for this local, tokenless mode. A network-visible deployment needs TLS, a long random token, exact origin and host allowlists, firewall/rate controls, and disk quotas; use an external API client that can send the bearer token because the bundled UI has no token field. Do not treat it as a public download service or run it under an account that exposes unrelated private files.

The native client permits public HTTPS media access and validates destination addresses and redirects before dialing. That is an application safeguard, not a replacement for operating-system or network egress policy.

## Tests

~~~
go test ./...
go vet ./...
~~~

The unit suite uses fake metadata and streams to cover format selection, download completion, limits, cancellation, playlist selection/reordering, single-item retry, restart persistence, caption inspection/conversion/publishing/archives, API validation, authenticated SSE framing/lifetime/event delivery, tickets, ranges, retention, and browser-assisted fallback logic.

The live test is opt-in, downloads real media, and requires working internet access plus a Chrome-compatible browser for the adaptive-HD case. Replace the sample URL with an accessible video you are authorized to download:

~~~
$env:YTDL_LIVE_DOWNLOAD_URL = 'https://youtu.be/y0KRrtfy2pY?si=ERBKJkjkY-SoPC4I'
$env:YTDL_LIVE_MIN_HEIGHT = '1080'
go test -run TestLiveDownload -count=1 -v
~~~

Live behavior depends on YouTube's current service and can change; a successful run does not guarantee future availability or a particular resolution.

See [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md) before distributing a build. AAC decoding uses an LGPL-2.1-or-later Go module; include its license and satisfy the applicable source and relinking terms when distributing binaries.
