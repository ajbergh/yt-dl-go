# YouTube parser fixtures

`player-response.html` is a small synthetic watch-page fixture shaped for the pinned `kkdai/youtube/v2` parser. Its video, channel, dates, and media URLs are test data; it is not a captured YouTube response. `TestSyntheticPlayerResponseFixtureParsesThroughYouTubeClient` runs this fixture through the pinned parser. `TestSyntheticPlayerResponseFormatDriftCases` builds minimal inline JSON cases to cover progressive formats, adaptive formats, combined bitrate ordering, omitted optional fields, ignored unknown fields, and rejection of an empty format list. These inputs are protocol-shape test data, not captures.

`sabr-selected-track.ump` is a synthetic UMP protocol transcript assembled from the same minimal test metadata and segment bytes used by the unit tests. It contains only `initvideo`, not playable media. It is not a captured YouTube stream. Other SABR cases are hand-authored with the test protocol builders. They cover buffered/streamed parser parity with multi-byte part lengths and unknown part types, plus media-header decoding with unknown protobuf fields and time-range duration data; these are not captured payloads.

These fixtures keep parser and assembly regression tests deterministic and offline. Captured YouTube player/UMP samples remain a separate follow-up because no verified, redistributable captures are included here.
