package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kkdai/youtube/v2"
)

func TestSABRCheckpointSurvivesRestartAndRejectsModifiedBytes(t *testing.T) {
	s := newPersistenceTestServer(t)
	j := &jobState{Job: Job{ID: "sabr-restart", URL: testVideo, Kind: "video", Quality: "best", Status: "downloading", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}}
	if err := s.store.saveJob(j); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "video.part")
	format := &youtube.Format{ItagNo: 299, ContentLength: 15, ApproxDurationMs: "3000"}
	checkpoints := &jobSABRCheckpoints{server: s, job: j, videoID: "abcdefghijk", profile: "web", item: 1, parts: map[string]string{path: "video"}}
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal(err)
	}
	capture := newSABRCapture(file, 299, 3000, 1024, nil)
	capture.expectedVideoID = "abcdefghijk"
	capture.saveCheckpoint = func(state sabrCheckpoint) error { return checkpoints.Save(path, format, state) }
	id := testProtoVarintField(nil, 1, 299)
	metadata := testProtoBytesField(nil, 1, []byte("abcdefghijk"))
	metadata = testProtoBytesField(metadata, 2, id)
	metadata = testProtoVarintField(metadata, 4, 3)
	body := testUMPPart(nil, umpPartFormatInitializationMetadata, metadata)
	body = testSelectedSegment(body, 0, 299, true, 0, 0, []byte("init"), id)
	body = testSelectedSegment(body, 1, 299, false, 1, 1000, []byte("one"), id)
	body = testSelectedSegment(body, 3, 299, false, 3, 1000, []byte("three"), id)
	if err := capture.consume(body); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	// A crash may leave bytes written after the last durable database commit.
	file, err = os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = file.Write([]byte("uncommitted tail"))
	file.Close()
	state, err := checkpoints.Load(path, format)
	if err != nil || state == nil {
		t.Fatalf("load state: %v", err)
	}
	if state.Bytes != 7 || len(state.Segments) != 2 {
		t.Fatal("out-of-order or uncommitted data entered the checkpoint")
	}
	file, err = os.OpenFile(path, os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	restored := newSABRCapture(file, 299, 3000, 1024, nil)
	restored.expectedVideoID = "abcdefghijk"
	restored.saveCheckpoint = capture.saveCheckpoint
	if err := restored.restoreCheckpoint(state); err != nil {
		t.Fatal(err)
	}
	body = testUMPPart(nil, umpPartFormatInitializationMetadata, metadata)
	body = testSelectedSegment(body, 0, 299, true, 0, 0, []byte("init"), id)
	body = testSelectedSegment(body, 1, 299, false, 1, 1000, []byte("one"), id)
	body = testSelectedSegment(body, 2, 299, false, 2, 1000, []byte("two"), id)
	body = testSelectedSegment(body, 3, 299, false, 3, 1000, []byte("three"), id)
	if err := restored.consume(body); err != nil {
		t.Fatal(err)
	}
	if _, err := restored.finish(); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "initonetwothree" {
		t.Fatalf("restored output %q", got)
	}
	state, err = checkpoints.Load(path, format)
	if err != nil || !state.Complete {
		t.Fatalf("completed checkpoint: %v", err)
	}
	if _, err := file.WriteAt([]byte("X"), 4); err != nil {
		t.Fatal(err)
	}
	rejected := newSABRCapture(file, 299, 3000, 1024, nil)
	rejected.expectedVideoID = "abcdefghijk"
	if err := rejected.restoreCheckpoint(state); !errors.Is(err, errSABR) {
		t.Fatalf("tampered bytes accepted: %v", err)
	}
	file.Close()
	if err := checkpoints.Discard(path); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.store.db.QueryRow(`SELECT COUNT(*) FROM download_sabr_segments WHERE job_id=?`, j.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("orphan segment state: count=%d err=%v", count, err)
	}
}

func TestSABRCheckpointRejectsDifferentFormatAfterRestore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "track")
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	capture := newSABRCapture(file, 299, 3000, 1024, nil)
	capture.expectedVideoID = "abcdefghijk"
	id := testProtoVarintField(nil, 1, 299)
	metadata := testProtoBytesField(nil, 1, []byte("abcdefghijk"))
	metadata = testProtoBytesField(metadata, 2, id)
	metadata = testProtoVarintField(metadata, 4, 3)
	body := testUMPPart(nil, umpPartFormatInitializationMetadata, metadata)
	body = testSelectedSegment(body, 0, 299, true, 0, 0, []byte("init"), id)
	body = testSelectedSegment(body, 1, 299, false, 1, 1000, []byte("one"), id)
	if err := capture.consume(body); err != nil {
		t.Fatal(err)
	}
	state := capture.checkpointState()
	restored := newSABRCapture(file, 299, 3000, 1024, nil)
	restored.expectedVideoID = "abcdefghijk"
	if err := restored.restoreCheckpoint(&state); err != nil {
		t.Fatal(err)
	}
	changed := testProtoBytesField(id, 3, []byte("different-variant"))
	metadata = testProtoBytesField(nil, 1, []byte("abcdefghijk"))
	metadata = testProtoBytesField(metadata, 2, changed)
	if err := restored.consume(testUMPPart(nil, umpPartFormatInitializationMetadata, metadata)); !errors.Is(err, errLength) {
		t.Fatalf("different format spliced into saved prefix: %v", err)
	}
}
