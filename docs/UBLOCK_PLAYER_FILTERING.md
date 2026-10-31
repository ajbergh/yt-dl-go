# YouTube player-data filtering

Investigated 2026-10-31 for the recurring preroll failures on `41ZY18JqI2A`.

## Upstream method

uBlock Origin's [YouTube filters](https://github.com/uBlockOrigin/uAssets/blob/master/filters/filters.txt) modify initial player globals and selected response bodies. They suppress ad configuration fields including `playerAds`, `adPlacements`, and `adSlots`. Its [JSON scriptlets](https://github.com/gorhill/uBlock/blob/master/src/js/resources/json-prune.js) cover JSON parsing, Fetch responses, and XHR responses.

The [quick-fixes list](https://github.com/uBlockOrigin/uAssets/blob/master/filters/quick-fixes.txt) includes additional browser-specific response rules and changing measures for playback/anti-adblock behavior. Its declared expiry is eight hours. A small static filter cannot be described as equivalent to the full blocker or guaranteed to keep working.

The [upstream assets license](https://github.com/uBlockOrigin/uAssets/blob/master/LICENSE) is GPLv3. This branch implements targeted player-data filtering independently; it does not vendor upstream scriptlet code or filter lists into this MIT-licensed repository.

## Implementation

`browser_ad_filter.js` is installed at document start in each temporary capture tab. It only activates on YouTube hosts. It handles initial player globals, recognized player envelopes parsed as JSON, and matching same-origin player API Fetch/XHR responses. It removes the three ad configuration fields while preserving video identity, stream configuration, content, and playability errors. Fetch replacement preserves status and response metadata and removes obsolete encoding/length headers. Unrelated requests, non-JSON bodies, and HTTP failure responses retain their original behavior.

This is player metadata filtering. SABR media payloads and signed media requests are not rewritten. Existing video-identity checks, track assembly, duration/size verification, and publication gates remain authoritative. Visible Skip and normal preroll playback remain fallback behavior when filtering does not prevent an ad.

## Verification

Focused tests passed for initial globals, nested player envelopes, unrelated JSON/content, JSON parse errors, XHR text/JSON, unrelated origins/APIs, HTTP failures, Fetch metadata, and malformed bodies. The full offline suite passed in 32.328 seconds and `go vet -tags=e2e ./...` passed. See [the sample investigation](VEVO_DOWNLOAD_FIX_2026-10-31.md) for earlier failed submissions and recoveries.

All three filtered production UI submissions completed at actual 1080p Compatibility: 76,548,552 bytes each, visible in Library, independently verified published video/audio tracks and endpoints through the expected 216 seconds. Diagnostics recorded 15 filtered ad fields during the first video capture and 17 during its successful audio capture. Its first audio attempt failed due to target navigation during preparation; a fresh-tab retry recovered. The next two submissions completed without capture retries. Verification does not decode every frame.

The final-source matrix passed both requested cases:

| Requested mode | Actual output | Final bytes | Case time | Internal recovery |
| --- | --- | --- | --- | --- |
| 480p, Best strategy | 480p MP4 | 18,394,340 | 64.86 s | One video-tab navigation retry |
| Best | 2160p WebM | 458,764,159 | 104.23 s | First dual capture failed SABR assembly after 37.037 s; fresh-tab capture completed |

All five final-candidate submissions/cases passed, with the internal failures above retained in the record. These results establish improvement for this sample, not universal reliability. Ignored evidence: `.vite-cache/vevo-patched/production-ui-filter*/` and `src/server/.cache/vevo-filter-matrix.log`.

The same packaged candidate was left running on `http://127.0.0.1:8080/` with tracing disabled. Health reports ready and the expected build tag. The four originally reported failed jobs remain in history; the original executable backup is retained.

The installed candidate reports `9fc0c8d-vevo-filter`, SHA-256 `d2208ea5e74831412b6607ae59f7eee00258072c50a9e093dc2a12baedadf488`.

Before PR readiness, test repeated runs with and without ads, consecutive ads, player replacement, multiple codec strategies/resolutions, audio-only capture, and browser-pool reuse. Record failed submissions and internal retries. An observed lack of ads in one run is not proof that filtering prevented them; use the filtered-field diagnostic counter and media identity evidence together.
