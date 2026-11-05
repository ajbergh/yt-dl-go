# YouTube player-data filtering

Technical reference for the temporary capture browser. This is not a general-purpose ad blocker or a guarantee of YouTube availability.

## Upstream method

uBlock Origin's [YouTube filters](https://github.com/uBlockOrigin/uAssets/blob/master/filters/filters.txt) modify initial player globals and selected response bodies. They suppress ad configuration fields including `playerAds`, `adPlacements`, and `adSlots`. Its [JSON scriptlets](https://github.com/gorhill/uBlock/blob/master/src/js/resources/json-prune.js) cover JSON parsing, Fetch responses, and XHR responses.

The [quick-fixes list](https://github.com/uBlockOrigin/uAssets/blob/master/filters/quick-fixes.txt) includes additional browser-specific response rules and changing measures for playback/anti-adblock behavior. Its declared expiry is eight hours. A small static filter cannot be described as equivalent to the full blocker or guaranteed to keep working.

The [upstream assets license](https://github.com/uBlockOrigin/uAssets/blob/master/LICENSE) is GPLv3. This branch implements targeted player-data filtering independently; it does not vendor upstream scriptlet code or filter lists into this MIT-licensed repository.

## Implementation

`browser_ad_filter.js` is installed at document start in each temporary capture tab. It only activates on YouTube hosts. It handles initial player globals, recognized player envelopes parsed as JSON, and matching same-origin player API Fetch/XHR responses. It removes the three ad configuration fields while preserving video identity, stream configuration, content, and playability errors. Fetch replacement preserves status and response metadata and removes obsolete encoding/length headers. Unrelated requests, non-JSON bodies, and HTTP failure responses retain their original behavior.

This is player metadata filtering. SABR media payloads and signed media requests are not rewritten. Existing video-identity checks, track assembly, duration/size verification, and publication gates remain authoritative. Visible Skip and normal preroll playback remain fallback behavior when filtering does not prevent an ad.

## Verification and limits

Offline tests cover initial globals, nested envelopes, unrelated JSON/content,
malformed bodies, XHR text/JSON, unrelated origins/APIs, HTTP failures, and Fetch
metadata. Run focused and full Go checks when changing `browser_ad_filter.js`.

Live checks require authorized media. Exercise repeated runs, preroll and normal
playback, player replacement, codec/quality strategies, audio-only capture,
browser-pool reuse, retries, source identity, and complete tracks. Filtered-field
counters help diagnose behavior but cannot prove an ad was prevented. Never
publish signed media URLs or ticket secrets. Historical sample byte counts and
temporary investigation paths remain in Git history, not current release evidence.
