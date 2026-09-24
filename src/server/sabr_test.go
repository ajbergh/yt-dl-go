package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSABRCaptureAssemblesCheckedInFixture(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "youtube", "sabr-selected-track.ump"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "track.mp4")
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	capture := newSABRCapture(file, 299, 1000, 1024, nil)
	if err := capture.consume(body); err != nil {
		t.Fatal(err)
	}
	if err := <-capture.done; err != nil {
		t.Fatal(err)
	}
	size, err := capture.finish()
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if size != 9 || !bytes.Equal(data, []byte("initvideo")) {
		t.Fatalf("assembled fixture size=%d data=%q, want selected init+video", size, data)
	}
}

func TestSABRCaptureAssemblesSelectedTrack(t *testing.T) {
	path := t.TempDir() + "\\track.mp4"
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal(err)
	}
	capture := newSABRCapture(file, 299, 1000, 1024, nil)
	format := testProtoVarintField(nil, 1, 299)
	formatInit := testProtoBytesField(nil, 2, format)
	initHeader := testMediaHeader(1, 299, true, 0, 0, 4, format)
	videoHeader := testMediaHeader(2, 299, false, 1, 1000, 5, format)
	audioHeader := testMediaHeader(3, 140, false, 0, 1000, 5, testProtoVarintField(nil, 1, 140))
	body := testUMPPart(nil, umpPartFormatInitializationMetadata, formatInit)
	body = testUMPPart(body, umpPartMediaHeader, audioHeader)
	body = testUMPPart(body, umpPartMedia, append(testUMPVarint(3), []byte("audio")...))
	body = testUMPPart(body, umpPartMediaEnd, testUMPVarint(3))
	body = testUMPPart(body, umpPartMediaHeader, initHeader)
	body = testUMPPart(body, umpPartMedia, append(testUMPVarint(1), []byte("init")...))
	body = testUMPPart(body, umpPartMediaEnd, testUMPVarint(1))
	body = testUMPPart(body, umpPartMediaHeader, videoHeader)
	body = testUMPPart(body, umpPartMedia, append(testUMPVarint(2), []byte("video")...))
	body = testUMPPart(body, umpPartMediaEnd, testUMPVarint(2))
	if err := capture.consume(body); err != nil {
		t.Fatal(err)
	}
	if err := <-capture.done; err != nil {
		t.Fatal(err)
	}
	size, err := capture.finish()
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if size != 9 || !bytes.Equal(data, []byte("initvideo")) {
		t.Fatalf("assembled size=%d data=%q, want selected init+video", size, data)
	}
}

func TestSABRCapturePinsAllowedAlternateItag(t *testing.T) {
	path := t.TempDir() + "\\track.webm"
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	// Chrome may choose itag 315 while metadata selection chose the same-size,
	// same-codec-family representation 337. Capture may accept 315, then pins
	// that exact itag and format digest for the rest of the track.
	capture := newSABRCapture(file, 337, 1000, 1024, nil, 315)
	format315 := testProtoVarintField(nil, 1, 315)
	format337 := testProtoVarintField(nil, 1, 337)
	body := testUMPPart(nil, umpPartFormatInitializationMetadata, testProtoBytesField(nil, 2, format315))
	body = testSelectedSegment(body, 1, 315, true, 0, 0, []byte("init"), format315)
	body = testUMPPart(body, umpPartFormatInitializationMetadata, testProtoBytesField(nil, 2, format337))
	body = testSelectedSegment(body, 2, 337, true, 0, 0, []byte("wrong-init"), format337)
	body = testSelectedSegment(body, 3, 315, false, 1, 1000, []byte("video"), format315)
	if err := capture.consume(body); err != nil {
		t.Fatal(err)
	}
	if err := <-capture.done; err != nil {
		t.Fatal(err)
	}
	if capture.selectedItag != 315 {
		t.Fatalf("pinned itag = %d, want first eligible itag 315", capture.selectedItag)
	}
	if _, err := capture.finish(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, []byte("initvideo")) {
		t.Fatalf("captured %q, want only pinned itag 315", data)
	}
}

func TestSABRCaptureRejectsLengthMismatch(t *testing.T) {
	path := t.TempDir() + "\\track.mp4"
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	capture := newSABRCapture(file, 299, 1000, 1024, nil)
	format := testProtoVarintField(nil, 1, 299)
	body := testUMPPart(nil, umpPartFormatInitializationMetadata, testProtoBytesField(nil, 2, format))
	body = testUMPPart(body, umpPartMediaHeader, testMediaHeader(1, 299, true, 0, 0, 99, format))
	body = testUMPPart(body, umpPartMedia, append(testUMPVarint(1), []byte("short")...))
	body = testUMPPart(body, umpPartMediaEnd, testUMPVarint(1))
	if err := capture.consume(body); err == nil {
		t.Fatal("expected content-length mismatch")
	}
}

func TestSABRCaptureRejectsRequiredAttestation(t *testing.T) {
	path := t.TempDir() + "\\track.mp4"
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	capture := newSABRCapture(file, 299, 1000, 1024, nil)
	status := testProtoVarintField(nil, 1, 3)
	if err := capture.consume(testUMPPart(nil, umpPartStreamProtectionStatus, status)); err == nil {
		t.Fatal("expected required-attestation failure")
	}
}

func TestSABRCaptureUsesDeclaredFinalSegment(t *testing.T) {
	path := t.TempDir() + "\\track.mp4"
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	capture := newSABRCapture(file, 299, 1000, 1024, nil)
	format := testProtoVarintField(nil, 1, 299)
	formatInit := testProtoBytesField(nil, 2, format)
	formatInit = testProtoVarintField(formatInit, 4, 2)
	body := testUMPPart(nil, umpPartFormatInitializationMetadata, formatInit)
	body = testSelectedSegment(body, 1, 299, true, 0, 0, []byte("init"), format)
	body = testSelectedSegment(body, 2, 299, false, 1, 1000, []byte("one"), format)
	if err := capture.consume(body); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-capture.done:
		t.Fatalf("capture completed before declared final segment: %v", err)
	default:
	}
	body = testSelectedSegment(nil, 3, 299, false, 2, 1000, []byte("two"), format)
	if err := capture.consume(body); err != nil {
		t.Fatal(err)
	}
	if err := <-capture.done; err != nil {
		t.Fatal(err)
	}
	if _, err := capture.finish(); err != nil {
		t.Fatal(err)
	}
}

func TestSABRCaptureAcceptsLargeMultiplexedResponse(t *testing.T) {
	path := t.TempDir() + "\\track.webm"
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	capture := newSABRCapture(file, 315, 1000, 1024, nil)
	format := testProtoVarintField(nil, 1, 315)
	formatInit := testProtoBytesField(nil, 2, format)

	// This models a 4K browser response that multiplexes unrelated track data.
	// It is intentionally consumed through an io.Reader: browser responses are
	// no longer assembled into one in-memory byte slice before UMP parsing.
	const ignoredPartSize = 22 * 1024 * 1024
	ignored := make([]byte, ignoredPartSize)
	body := make([]byte, 0, 3*ignoredPartSize+1024)
	for range 3 {
		body = testUMPPart(body, 99, ignored)
	}
	body = testUMPPart(body, umpPartFormatInitializationMetadata, formatInit)
	body = testSelectedSegment(body, 1, 315, true, 0, 0, []byte("init"), format)
	body = testSelectedSegment(body, 2, 315, false, 1, 1000, []byte("video"), format)
	if len(body) <= 64*1024*1024 {
		t.Fatalf("test response is only %d bytes", len(body))
	}
	if err := capture.consumeReader(bytes.NewReader(body)); err != nil {
		t.Fatal(err)
	}
	if err := <-capture.done; err != nil {
		t.Fatal(err)
	}
	if _, err := capture.finish(); err != nil {
		t.Fatal(err)
	}
}

func TestSABRCaptureFlushesMediaReceivedBeforeInit(t *testing.T) {
	path := t.TempDir() + "\\track.webm"
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal(err)
	}
	capture := newSABRCapture(file, 315, 1000, 1024, nil)
	format := testProtoVarintField(nil, 1, 315)
	body := testUMPPart(nil, umpPartFormatInitializationMetadata, testProtoBytesField(nil, 2, format))
	if err := capture.consume(body); err != nil {
		t.Fatal(err)
	}
	body = testUMPPart(nil, umpPartMediaHeader, testMediaHeader(2, 315, false, 1, 1000, 5, format))
	body = testUMPPart(body, umpPartMedia, append(testUMPVarint(2), []byte("video")...))
	body = testUMPPart(body, umpPartMediaEnd, testUMPVarint(2))
	if err := capture.consume(body); err != nil {
		t.Fatal(err)
	}
	if capture.readyBytes != int64(len("video")) || capture.ready[1] == nil {
		t.Fatalf("out-of-order ready bytes=%d entries=%d; want 5 bytes retained", capture.readyBytes, len(capture.ready))
	}
	body = testSelectedSegment(nil, 1, 315, true, 0, 0, []byte("init"), format)
	if err := capture.consume(body); err != nil {
		t.Fatal(err)
	}
	if capture.readyBytes != 0 || len(capture.ready) != 0 {
		t.Fatalf("ready buffer after ordered flush: bytes=%d entries=%d", capture.readyBytes, len(capture.ready))
	}
	if err := <-capture.done; err != nil {
		t.Fatal(err)
	}
	if _, err := capture.finish(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, []byte("initvideo")) {
		t.Fatalf("assembled data=%q, want init followed by queued media", data)
	}
}

func TestSABRCaptureEnforcesAggregateReadyLimitAndResetAccounting(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "sabr-ready-limit-*.webm")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	capture := newSABRCapture(file, 315, 1000, int64(maxSABRReadyBytes)*2, nil)
	capture.formatVerified = true
	capture.sequenceSet = true
	capture.nextSequence = 1
	capture.readyBytes = int64(maxSABRReadyBytes) - 2
	segment := &sabrSegment{header: sabrMediaHeader{sequence: 3, durationMs: 1000}, selected: true, data: []byte("abc")}
	if err := capture.commitSegment(segment); !errors.Is(err, errSABRReadyLimit) {
		t.Fatalf("commit over ready limit error = %v, want %v", err, errSABRReadyLimit)
	}
	if capture.ready[3] != nil || capture.readyBytes != int64(maxSABRReadyBytes)-2 {
		t.Fatalf("over-limit segment was retained: entries=%d bytes=%d", len(capture.ready), capture.readyBytes)
	}
	capture.readyBytes = 17
	if err := capture.resetShortCandidate(); err != nil {
		t.Fatal(err)
	}
	if capture.readyBytes != 0 || len(capture.ready) != 0 {
		t.Fatalf("short-candidate reset retained ready accounting: entries=%d bytes=%d", len(capture.ready), capture.readyBytes)
	}
}

func TestParseUMPPartCountSupportsLongMediaAndKeepsHardLimit(t *testing.T) {
	body := make([]byte, maxSABRParts*2)
	for index := 0; index < maxSABRParts; index++ {
		body[index*2] = 99
		body[index*2+1] = 0
	}
	consume := func(uint64, []byte) error { return nil }
	if err := parseUMP(body, consume); err != nil {
		t.Fatalf("parse %d UMP parts: %v", maxSABRParts, err)
	}
	if err := parseUMPReader(bytes.NewReader(body), consume); err != nil {
		t.Fatalf("stream-parse %d UMP parts: %v", maxSABRParts, err)
	}
	tooMany := append(body, 99, 0)
	if err := parseUMP(tooMany, consume); !errors.Is(err, errSABR) {
		t.Fatalf("parse over part limit error = %v, want %v", err, errSABR)
	}
	if err := parseUMPReader(bytes.NewReader(tooMany), consume); !errors.Is(err, errSABR) {
		t.Fatalf("stream-parse over part limit error = %v, want %v", err, errSABR)
	}
}

func TestSABRCaptureIgnoresConflictingSameItagStream(t *testing.T) {
	path := t.TempDir() + "\\track.webm"
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal(err)
	}
	capture := newSABRCapture(file, 251, 2000, 1024, nil)
	format := testProtoVarintField(nil, 1, 251)
	formatInit := testProtoBytesField(nil, 2, format)
	formatInit = testProtoVarintField(formatInit, 4, 2)
	body := testUMPPart(nil, umpPartFormatInitializationMetadata, formatInit)
	body = testSelectedSegment(body, 1, 251, true, 0, 0, []byte("init"), format)
	body = testSelectedSegment(body, 2, 251, false, 1, 1000, []byte("one"), format)
	if err := capture.consume(body); err != nil {
		t.Fatal(err)
	}

	alternateFormat := testProtoVarintField(nil, 1, 251)
	alternateFormat = testProtoVarintField(alternateFormat, 2, 999)
	alternateInit := testProtoBytesField(nil, 2, alternateFormat)
	alternateInit = testProtoVarintField(alternateInit, 4, 1)
	alternate := testUMPPart(nil, umpPartFormatInitializationMetadata, alternateInit)
	alternate = testSelectedSegment(alternate, 3, 251, true, 0, 0, []byte("other-init"), alternateFormat)
	alternate = testSelectedSegment(alternate, 4, 251, false, 1, 1000, []byte("other"), alternateFormat)
	if err := capture.consume(alternate); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-capture.done:
		t.Fatalf("conflicting stream completed capture: %v", err)
	default:
	}

	final := testSelectedSegment(nil, 5, 251, false, 2, 1000, []byte("two"), format)
	if err := capture.consume(final); err != nil {
		t.Fatal(err)
	}
	if err := <-capture.done; err != nil {
		t.Fatal(err)
	}
	if _, err := capture.finish(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, []byte("initonetwo")) {
		t.Fatalf("assembled data=%q, want only the verified stream", data)
	}
}

func TestSABRCaptureReplacesShortCandidate(t *testing.T) {
	path := t.TempDir() + "\\track.webm"
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal(err)
	}
	capture := newSABRCapture(file, 251, 2000, 1024, nil)
	shortFormat := testProtoVarintField(nil, 1, 251)
	shortFormat = testProtoVarintField(shortFormat, 2, 999)
	shortInit := testProtoBytesField(nil, 2, shortFormat)
	shortInit = testProtoVarintField(shortInit, 4, 1)
	short := testUMPPart(nil, umpPartFormatInitializationMetadata, shortInit)
	short = testSelectedSegment(short, 1, 251, true, 0, 0, []byte("short-init"), shortFormat)
	short = testSelectedSegment(short, 2, 251, false, 1, 100, []byte("short"), shortFormat)
	if err := capture.consume(short); err != nil {
		t.Fatal(err)
	}
	if capture.totalWritten != 0 || capture.formatVerified {
		t.Fatalf("short candidate was not reset: bytes=%d verified=%t", capture.totalWritten, capture.formatVerified)
	}

	format := testProtoVarintField(nil, 1, 251)
	formatInit := testProtoBytesField(nil, 2, format)
	formatInit = testProtoVarintField(formatInit, 4, 2)
	body := testUMPPart(nil, umpPartFormatInitializationMetadata, formatInit)
	body = testSelectedSegment(body, 3, 251, true, 0, 0, []byte("init"), format)
	body = testSelectedSegment(body, 4, 251, false, 1, 1000, []byte("one"), format)
	body = testSelectedSegment(body, 5, 251, false, 2, 1000, []byte("two"), format)
	if err := capture.consume(body); err != nil {
		t.Fatal(err)
	}
	if err := <-capture.done; err != nil {
		t.Fatal(err)
	}
	if _, err := capture.finish(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, []byte("initonetwo")) {
		t.Fatalf("assembled data=%q, want replacement stream only", data)
	}
}

func TestSABRCaptureSetRoutesMultiplexedTracksWithSharedHeaderIDs(t *testing.T) {
	dir := t.TempDir()
	videoFile, err := os.OpenFile(dir+"\\video.webm", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal(err)
	}
	audioFile, err := os.OpenFile(dir+"\\audio.webm", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal(err)
	}
	video := newSABRCapture(videoFile, 315, 1000, 1024, nil)
	audio := newSABRCapture(audioFile, 251, 1000, 1024, nil)
	set := newSABRCaptureSet(video, audio)

	videoFormat := testProtoVarintField(nil, 1, 315)
	audioFormat := testProtoVarintField(nil, 1, 251)
	body := testUMPPart(nil, umpPartFormatInitializationMetadata, testProtoBytesField(nil, 2, videoFormat))
	body = testUMPPart(body, umpPartFormatInitializationMetadata, testProtoBytesField(nil, 2, audioFormat))
	// Header IDs are scoped by the selected track state, so both tracks may use
	// the same IDs inside the one browser response.
	body = testSelectedSegment(body, 1, 315, true, 0, 0, []byte("video-init"), videoFormat)
	body = testSelectedSegment(body, 1, 251, true, 0, 0, []byte("audio-init"), audioFormat)
	body = testSelectedSegment(body, 2, 315, false, 1, 1000, []byte("video-media"), videoFormat)
	body = testSelectedSegment(body, 2, 251, false, 1, 1000, []byte("audio-media"), audioFormat)
	if err := set.consume(body); err != nil {
		t.Fatal(err)
	}
	if err := <-set.done; err != nil {
		t.Fatal(err)
	}
	videoSize, err := video.finish()
	if err != nil {
		t.Fatal(err)
	}
	audioSize, err := audio.finish()
	if err != nil {
		t.Fatal(err)
	}
	if err := videoFile.Close(); err != nil {
		t.Fatal(err)
	}
	if err := audioFile.Close(); err != nil {
		t.Fatal(err)
	}
	videoData, err := os.ReadFile(videoFile.Name())
	if err != nil {
		t.Fatal(err)
	}
	audioData, err := os.ReadFile(audioFile.Name())
	if err != nil {
		t.Fatal(err)
	}
	if videoSize != int64(len("video-initvideo-media")) || !bytes.Equal(videoData, []byte("video-initvideo-media")) {
		t.Fatalf("video size=%d data=%q", videoSize, videoData)
	}
	if audioSize != int64(len("audio-initaudio-media")) || !bytes.Equal(audioData, []byte("audio-initaudio-media")) {
		t.Fatalf("audio size=%d data=%q", audioSize, audioData)
	}
}

func testSelectedSegment(dst []byte, id, itag uint64, init bool, sequence, duration uint64, data, format []byte) []byte {
	dst = testUMPPart(dst, umpPartMediaHeader, testMediaHeader(id, itag, init, sequence, duration, uint64(len(data)), format))
	dst = testUMPPart(dst, umpPartMedia, append(testUMPVarint(id), data...))
	return testUMPPart(dst, umpPartMediaEnd, testUMPVarint(id))
}

func testMediaHeader(id, itag uint64, init bool, sequence, duration, length uint64, format []byte) []byte {
	var result []byte
	result = testProtoVarintField(result, 1, id)
	result = testProtoVarintField(result, 3, itag)
	if init {
		result = testProtoVarintField(result, 8, 1)
	}
	result = testProtoVarintField(result, 9, sequence)
	if duration > 0 {
		result = testProtoVarintField(result, 12, duration)
	}
	result = testProtoBytesField(result, 13, format)
	result = testProtoVarintField(result, 14, length)
	return result
}

func testUMPPart(dst []byte, partType uint64, payload []byte) []byte {
	dst = append(dst, testUMPVarint(partType)...)
	dst = append(dst, testUMPVarint(uint64(len(payload)))...)
	return append(dst, payload...)
}

func testUMPVarint(value uint64) []byte {
	switch {
	case value <= 0x7f:
		return []byte{byte(value)}
	case value <= 0x3fff:
		return []byte{byte(0x80 | value&0x3f), byte(value >> 6)}
	case value <= 0x1fffff:
		return []byte{byte(0xc0 | value&0x1f), byte(value >> 5), byte(value >> 13)}
	default:
		return []byte{byte(0xe0 | value&0x0f), byte(value >> 4), byte(value >> 12), byte(value >> 20)}
	}
}

func testProtoVarintField(dst []byte, field, value uint64) []byte {
	dst = testProtoVarint(dst, field<<3)
	return testProtoVarint(dst, value)
}

func testProtoBytesField(dst []byte, field uint64, value []byte) []byte {
	dst = testProtoVarint(dst, field<<3|2)
	dst = testProtoVarint(dst, uint64(len(value)))
	return append(dst, value...)
}

func testProtoVarint(dst []byte, value uint64) []byte {
	for value >= 0x80 {
		dst = append(dst, byte(value)|0x80)
		value >>= 7
	}
	return append(dst, byte(value))
}
