# RF-010: Audio startup timer waits for a whole range chunk

Severity: medium

Status: Fixed

Reviewed: 2026-11-09

Source: src/server/audio_transport.go; src/server/native_ranges.go: readNativeRange, runNativeRangePipeline

Evidence: The startup callback is invoked on completed written chunks. readNativeRange uses io.ReadFull before a chunk can report progress. Healthy incremental bytes alone cannot stop the 20-second timer.

Resolution: Native range reads report actual arriving bytes separately from verified chunk progress. Audio startup probing stops on first bytes; normal idle deadlines and range verification continue.

Validation: TestReviewNativeBytesArriveBeforeWholeChunk proves startup notification arrives while the server still withholds the rest of the first chunk.
