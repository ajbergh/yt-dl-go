package main

import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log"
	"os"
	"strconv"

	"github.com/chromedp/chromedp"

	"github.com/kkdai/youtube/v2"
)

type sabrSavedSegment struct {
	Sequence             int64
	Start, End, Duration int64
	Hash                 [sha256.Size]byte
}
type sabrCheckpoint struct {
	VideoID                     string
	Itag                        int
	Duration, Bytes, Cumulative int64
	FormatDigest, InitDigest    [sha256.Size]byte
	NextSequence, EndSegment    uint64
	SequenceSet, Complete       bool
	Segments                    []sabrSavedSegment `json:"-"`
}
type sabrCheckpointIO interface {
	Load(string, *youtube.Format) (*sabrCheckpoint, error)
	Save(string, *youtube.Format, sabrCheckpoint) error
	Discard(string) error
}
type browserCheckpointProvider interface{ SetCheckpoints(sabrCheckpointIO) }

func (p *chromeBrowserProvider) SetCheckpoints(checkpoints sabrCheckpointIO) {
	p.checkpoints = checkpoints
}
func (p *chromeBrowserProvider) seedRestoredPrefix(durationMs int64) error {
	return chromedp.Do(p.ctx, chromedp.Evaluate[chromedp.Void]("window.__ytdlRestoredPrefix="+strconv.FormatFloat(float64(durationMs)/1000, 'f', 3, 64)))
}

type jobSABRCheckpoints struct {
	server           *server
	job              *jobState
	videoID, profile string
	item             int
	parts            map[string]string
}

func (s *server) bindBrowserCheckpoints(provider browserMediaProvider, j *jobState, videoID, profile string, item int, parts map[string]string) bool {
	checkpointed, ok := provider.(browserCheckpointProvider)
	if !ok || s.store == nil {
		return false
	}
	checkpointed.SetCheckpoints(&jobSABRCheckpoints{server: s, job: j, videoID: videoID, profile: profile, item: item, parts: parts})
	return true
}
func (checkpoints *jobSABRCheckpoints) Load(path string, format *youtube.Format) (*sabrCheckpoint, error) {
	key, ok := checkpoints.parts[path]
	if !ok {
		return nil, errStorage
	}
	part, err := checkpoints.server.store.loadPart(checkpoints.job.ID, checkpoints.item, key)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, checkpoints.Discard(path)
	}
	if err != nil {
		return nil, errStorage
	}
	if part.Path != path || part.Method != "browser-sabr" || part.SourceFingerprint != downloadSourceFingerprint(checkpoints.videoID, format, checkpoints.profile) || part.ExpectedBytes != format.ContentLength {
		return nil, checkpoints.Discard(path)
	}
	state, err := checkpoints.server.store.loadSABRCheckpoint(checkpoints.job.ID, checkpoints.item, key)
	if err != nil && !errors.Is(err, errSABR) && !errors.Is(err, sql.ErrNoRows) {
		return nil, errStorage
	}
	if err != nil || state == nil || state.VideoID != checkpoints.videoID || state.Bytes != part.CompletedBytes {
		return nil, checkpoints.Discard(path)
	}
	return state, nil
}
func (checkpoints *jobSABRCheckpoints) Save(path string, format *youtube.Format, state sabrCheckpoint) error {
	key, ok := checkpoints.parts[path]
	if !ok {
		return errStorage
	}
	if state.Bytes == 0 {
		return checkpoints.server.deleteDownloadPart(checkpoints.job, checkpoints.item, key)
	}
	part := downloadPart{Path: path, CompletedBytes: state.Bytes, ExpectedBytes: format.ContentLength, Itag: format.ItagNo, SourceFingerprint: downloadSourceFingerprint(checkpoints.videoID, format, checkpoints.profile), Method: "browser-sabr"}
	if err := checkpoints.server.store.saveSABRCheckpoint(checkpoints.job.ID, checkpoints.item, key, part, state); err != nil {
		checkpoints.server.recordPersistenceFailure("save SABR checkpoint", checkpoints.job.ID, err)
		return errStorage
	}
	checkpoints.server.recordPersistenceSuccess()
	return nil
}
func (checkpoints *jobSABRCheckpoints) Discard(path string) error {
	key, ok := checkpoints.parts[path]
	if !ok {
		return errStorage
	}
	if err := checkpoints.server.deleteDownloadPart(checkpoints.job, checkpoints.item, key); err != nil {
		return errStorage
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return errStorage
	}
	return nil
}

func (capture *sabrCapture) checkpointState() sabrCheckpoint {
	return sabrCheckpoint{VideoID: capture.expectedVideoID, Itag: capture.selectedItag, Duration: capture.expectedDurationMs, Bytes: capture.totalWritten, Cumulative: capture.cumulativeMs, FormatDigest: capture.formatDigest, InitDigest: capture.initDigest, NextSequence: capture.nextSequence, EndSegment: capture.endSegment, SequenceSet: capture.sequenceSet, Complete: capture.complete, Segments: capture.savedSegments}
}
func (capture *sabrCapture) persistCheckpoint() error {
	if capture.saveCheckpoint == nil {
		return nil
	}
	if err := capture.file.Sync(); err != nil {
		return errStorage
	}
	return capture.saveCheckpoint(capture.checkpointState())
}
func (capture *sabrCapture) restoreCheckpoint(state *sabrCheckpoint) error {
	if state == nil {
		return nil
	}
	if state.VideoID != capture.expectedVideoID || state.Duration != capture.expectedDurationMs || state.Bytes <= 0 || state.Bytes > capture.budget || len(state.Segments) == 0 || len(state.Segments) > maxSABRParts {
		return errSABR
	}
	if _, ok := capture.allowedItags[state.Itag]; !ok {
		return errSABR
	}
	position := int64(0)
	cumulative := int64(0)
	var next uint64
	sequenceSet := false
	for index, segment := range state.Segments {
		if segment.Start != position || segment.End <= segment.Start || segment.End > state.Bytes {
			return errSABR
		}
		hash := sha256.New()
		if _, err := io.Copy(hash, io.NewSectionReader(capture.file, segment.Start, segment.End-segment.Start)); err != nil {
			return errStorage
		}
		if !equalDigest(hash.Sum(nil), segment.Hash) {
			return errSABR
		}
		if index == 0 {
			if segment.Sequence != -1 || segment.Hash != state.InitDigest {
				return errSABR
			}
		} else {
			if segment.Sequence < 0 || segment.Duration <= 0 {
				return errSABR
			}
			if !sequenceSet {
				if segment.Sequence > 1 {
					return errSABR
				}
				next = uint64(segment.Sequence)
				sequenceSet = true
			}
			if uint64(segment.Sequence) != next {
				return errSABR
			}
			next++
			cumulative += segment.Duration
		}
		position = segment.End
	}
	if position != state.Bytes || cumulative != state.Cumulative || sequenceSet != state.SequenceSet || next != state.NextSequence {
		return errSABR
	}
	if state.Complete && ((state.EndSegment > 0 && next <= state.EndSegment) || cumulative+1500 < state.Duration) {
		return errSABR
	}
	if err := capture.file.Truncate(state.Bytes); err != nil {
		return errStorage
	}
	if _, err := capture.file.Seek(state.Bytes, io.SeekStart); err != nil {
		return errStorage
	}
	capture.selectedItag = state.Itag
	capture.formatVerified = true
	capture.formatDigest = state.FormatDigest
	capture.acceptSelected = true
	capture.initWritten = true
	capture.initDigest = state.InitDigest
	capture.nextSequence = state.NextSequence
	capture.sequenceSet = state.SequenceSet
	capture.endSegment = state.EndSegment
	capture.cumulativeMs = state.Cumulative
	capture.totalWritten = state.Bytes
	capture.complete = state.Complete
	capture.restored = true
	if os.Getenv("YTDL_TRACE_PERFORMANCE") != "" {
		log.Printf("SABR checkpoint restored itag=%d bytes=%d duration_ms=%d complete=%t", state.Itag, state.Bytes, state.Cumulative, state.Complete)
	}
	capture.savedSegments = state.Segments
	for _, segment := range state.Segments[1:] {
		capture.written[uint64(segment.Sequence)] = segment.Hash
	}
	capture.progress(state.Bytes)
	if state.Complete {
		capture.succeed()
	}
	return nil
}
func equalDigest(data []byte, digest [sha256.Size]byte) bool {
	if len(data) != sha256.Size {
		return false
	}
	var value [sha256.Size]byte
	copy(value[:], data)
	return value == digest
}

func (p *chromeBrowserProvider) openCaptureFile(path string, format *youtube.Format) (*os.File, *sabrCheckpoint, error) {
	var state *sabrCheckpoint
	if p.checkpoints != nil {
		var err error
		state, err = p.checkpoints.Load(path, format)
		if err != nil {
			return nil, nil, err
		}
	}
	if state != nil {
		info, err := os.Lstat(path)
		if err == nil && info.Mode().IsRegular() && info.Size() >= state.Bytes {
			file, err := os.OpenFile(path, os.O_RDWR, 0600)
			if err != nil {
				return nil, nil, errStorage
			}
			return file, state, nil
		}
		if err := p.checkpoints.Discard(path); err != nil {
			return nil, nil, err
		}
	}
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, nil, errStorage
	}
	return file, nil, nil
}
func (p *chromeBrowserProvider) configureCaptureCheckpoint(capture *sabrCapture, path string, format *youtube.Format, state *sabrCheckpoint) error {
	if p.checkpoints != nil {
		capture.saveCheckpoint = func(state sabrCheckpoint) error { return p.checkpoints.Save(path, format, state) }
	}
	if err := capture.restoreCheckpoint(state); err != nil {
		if errors.Is(err, errStorage) {
			return err
		}
		_ = capture.file.Close()
		if p.checkpoints != nil {
			if discardErr := p.checkpoints.Discard(path); discardErr != nil {
				return discardErr
			}
		}
		return errLength
	}
	return nil
}

// Keep serialized state validation local; segments are stored as normalized
// rows and hashes, not repeatedly copied into a growing JSON document.
func encodeSABRCheckpoint(state sabrCheckpoint) (string, error) {
	data, err := json.Marshal(state)
	return string(data), err
}
