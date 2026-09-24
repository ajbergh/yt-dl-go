# YouTube parser fixtures

`player-response.html` is a small synthetic watch-page fixture shaped for the pinned `kkdai/youtube/v2` parser. Its video, channel, dates, and media URLs are test data; it is not a captured YouTube response.

`sabr-selected-track.ump` is a synthetic UMP protocol transcript assembled from the same minimal test metadata and segment bytes used by the unit tests. It contains only `initvideo`, not playable media. It is not a captured YouTube stream.

These fixtures keep parser and assembly regression tests deterministic and offline. Captured YouTube player/UMP samples remain a separate follow-up because no verified, redistributable captures are included here.
