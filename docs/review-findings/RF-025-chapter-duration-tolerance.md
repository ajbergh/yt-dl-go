# RF-025: Chapter MP3 conversion rejects slightly shorter decoded audio

Severity: low

Status: Fixed

Reviewed: 2026-11-09

Source: src/server/audio_chapters.go: EOF branch

Evidence: At EOF the last chapter requires decodedSamples to reach the metadata-derived EndMs exactly; any shortfall returns errLength.

Resolution: EOF accepts up to one second of metadata/encoder-padding shortfall only in the final chapter, provided it contains audio. Earlier/missing chapters and larger truncations still fail. Final chapter duration metadata is shortened to the decoded duration.

Validation: TestReviewChapterEOFAllowsOnlySmallFinalShortfall covers accepted padding and rejected earlier, large and empty-final-chapter shortfalls.
