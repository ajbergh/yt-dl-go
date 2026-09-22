package main

import (
	"bytes"
	"os"
	"testing"
)

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

	// This models a 4K browser response that multiplexes enough unrelated
	// track data to exceed the old 64 MiB whole-response ceiling. Individual
	// UMP parts remain bounded independently.
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

func TestSABRCaptureFlushesMediaReceivedBeforeInit(t *testing.T) {
	path := t.TempDir() + "\\track.webm"
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal(err)
	}
	capture := newSABRCapture(file, 315, 1000, 1024, nil)
	format := testProtoVarintField(nil, 1, 315)
	body := testUMPPart(nil, umpPartFormatInitializationMetadata, testProtoBytesField(nil, 2, format))
	body = testSelectedSegment(body, 2, 315, false, 1, 1000, []byte("video"), format)
	body = testSelectedSegment(body, 1, 315, true, 0, 0, []byte("init"), format)
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
	if !bytes.Equal(data, []byte("initvideo")) {
		t.Fatalf("assembled data=%q, want init followed by queued media", data)
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
