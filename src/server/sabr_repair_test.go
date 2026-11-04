package main

import (
	"os"
	"path/filepath"
	"testing"
)

func repairCaptureFixture(t *testing.T) (*sabrCapture, []byte, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "track")
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { file.Close() })
	capture := newSABRCapture(file, 137, 9000, 1024, nil)
	format := testProtoVarintField(nil, 1, 137)
	body := testUMPPart(nil, umpPartFormatInitializationMetadata, testProtoBytesField(nil, 2, format))
	body = testSelectedSegment(body, 0, 137, true, 0, 0, []byte("init"), format)
	if err := capture.consume(body); err != nil {
		t.Fatal(err)
	}
	return capture, format, path
}

func TestSABRQualitySwitchRefetchesMissingBeginning(t *testing.T) {
	capture, format, path := repairCaptureFixture(t)
	if err := capture.consume(testSelectedSegment(nil, 0, 137, false, 3, 3000, []byte("three"), format)); err != nil {
		t.Fatal(err)
	}
	if position, needed := capture.recoveryPositionMs(); !needed || position != 0 {
		t.Fatalf("recovery=%d %t", position, needed)
	}
	if got, _ := os.ReadFile(path); string(got) != "init" {
		t.Fatalf("skipped missing media: %q", got)
	}
	if _, err := capture.finish(); err == nil {
		t.Fatal("accepted a missing beginning")
	}
	body := testSelectedSegment(nil, 0, 137, false, 1, 3000, []byte("one"), format)
	body = testSelectedSegment(body, 1, 137, false, 2, 3000, []byte("two"), format)
	if err := capture.consume(body); err != nil {
		t.Fatal(err)
	}
	if _, err := capture.finish(); err != nil {
		t.Fatal(err)
	}
	if _, needed := capture.recoveryPositionMs(); needed {
		t.Fatal("completed track still requested repair")
	}
	if got, _ := os.ReadFile(path); string(got) != "initonetwothree" {
		t.Fatalf("repaired output %q", got)
	}
}

func TestSABRPartialMediaNeverAdvancesVerifiedPrefix(t *testing.T) {
	capture, format, path := repairCaptureFixture(t)
	if err := capture.consume(testSelectedSegment(nil, 0, 137, false, 1, 3000, []byte("one"), format)); err != nil {
		t.Fatal(err)
	}
	partial := testUMPPart(nil, umpPartMediaHeader, testMediaHeader(0, 137, false, 2, 3000, 3, format))
	partial = testUMPPart(partial, umpPartMedia, append(testUMPVarint(0), []byte("tw")...))
	partial = testUMPPart(partial, umpPartMediaEnd, testUMPVarint(0))
	partial = testSelectedSegment(partial, 1, 137, false, 3, 3000, []byte("three"), format)
	if err := capture.consume(partial); err != nil {
		t.Fatal(err)
	}
	if capture.verifiedDurationMs() != 3000 {
		t.Fatal("partial media advanced the verified prefix")
	}
	if position, needed := capture.recoveryPositionMs(); !needed || position != 1000 {
		t.Fatalf("repair=%d %t", position, needed)
	}
	if got, _ := os.ReadFile(path); string(got) != "initone" {
		t.Fatalf("partial bytes entered output: %q", got)
	}
	if err := capture.consume(testSelectedSegment(nil, 0, 137, false, 2, 3000, []byte("two"), format)); err != nil {
		t.Fatal(err)
	}
	if _, err := capture.finish(); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); string(got) != "initonetwothree" {
		t.Fatalf("repaired output %q", got)
	}
}

func TestSABRResponseHeaderIDsDoNotRetainIncompleteMedia(t *testing.T) {
	capture, format, path := repairCaptureFixture(t)
	partial := testUMPPart(nil, umpPartMediaHeader, testMediaHeader(0, 137, false, 1, 3000, 3, format))
	partial = testUMPPart(partial, umpPartMedia, append(testUMPVarint(0), []byte("o")...))
	if err := capture.consume(partial); err != nil {
		t.Fatal(err)
	}
	if len(capture.active) != 0 {
		t.Fatal("response-scoped partial header was retained")
	}
	body := testSelectedSegment(nil, 0, 137, false, 1, 3000, []byte("one"), format)
	body = testSelectedSegment(body, 1, 137, false, 2, 3000, []byte("two"), format)
	body = testSelectedSegment(body, 2, 137, false, 3, 3000, []byte("three"), format)
	if err := capture.consume(body); err != nil {
		t.Fatal(err)
	}
	if _, err := capture.finish(); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); string(got) != "initonetwothree" {
		t.Fatalf("mixed response bytes: %q", got)
	}
}

func TestSABRMissingInitializationRequestsReloadWithoutAcceptingMedia(t *testing.T) {
	path := filepath.Join(t.TempDir(), "track")
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	capture := newSABRCapture(file, 137, 9000, 1024, nil)
	format := testProtoVarintField(nil, 1, 137)
	if err := capture.consume(testSelectedSegment(nil, 0, 137, false, 3, 3000, []byte("three"), format)); err != nil {
		t.Fatal(err)
	}
	if _, needed := capture.recoveryPositionMs(); !needed || !capture.recoveryNeedsReload() {
		t.Fatal("missing initialization did not request reload")
	}
	if capture.totalWritten != 0 || capture.formatVerified {
		t.Fatal("media without source metadata was trusted")
	}
	body := testUMPPart(nil, umpPartFormatInitializationMetadata, testProtoBytesField(nil, 2, format))
	body = testSelectedSegment(body, 0, 137, false, 1, 3000, []byte("one"), format)
	if err := capture.consume(body); err != nil {
		t.Fatal(err)
	}
	if !capture.recoveryNeedsReload() {
		t.Fatal("missing init bytes did not request reload")
	}
	body = testSelectedSegment(nil, 0, 137, true, 0, 0, []byte("init"), format)
	body = testSelectedSegment(body, 1, 137, false, 2, 3000, []byte("two"), format)
	body = testSelectedSegment(body, 2, 137, false, 3, 3000, []byte("three"), format)
	if err := capture.consume(body); err != nil {
		t.Fatal(err)
	}
	if _, err := capture.finish(); err != nil {
		t.Fatal(err)
	}
	if capture.recoveryNeedsReload() {
		t.Fatal("complete initialization still requested reload")
	}
}
