package main

import (
	"context"
	"database/sql"
	"errors"
	"os"

	"github.com/kkdai/youtube/v2"
)

// captureAdaptivePair shares the same bounded, verified pair capture between
// MP4 and WebM. Unsupported providers retain their serial transfer path.
func (s *server) captureAdaptivePair(ctx context.Context, j *jobState, engine nativeClient, video *youtube.Video, selection streamSelection, videoPart, audioPart string, item int, budget int64, progress func(int64, int64), provider browserMediaProvider) (videoSize, audioSize int64, used bool, err error) {
	if _, ok := provider.(browserDualTrackProvider); !ok || selection.video == nil || selection.audio == nil {
		return 0, 0, false, nil
	}
	if selection.audio.ContentLength <= 0 || selection.audio.ContentLength >= budget {
		return 0, 0, false, errItemReservation
	}
	// The reservation includes container headroom. Give audio a share as well:
	// browser segments can contain more framing than the advertised source file.
	audioBudget := selection.audio.ContentLength
	if selection.video.ContentLength > 0 && selection.video.ContentLength < budget-audioBudget {
		spare := budget - selection.video.ContentLength - audioBudget
		audioBudget += min(spare/2, max(int64(64*1024), audioBudget/20))
	}
	for key, part := range map[string]string{"video": videoPart, "audio": audioPart} {
		if s.store != nil {
			checkpoint, err := s.store.loadPart(j.ID, item, key)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return 0, 0, false, errStorage
			}
			if err == nil && checkpoint.Method == "native-range" && checkpoint.Path == part && checkpoint.CompletedBytes > 0 {
				return 0, 0, false, nil
			}
		}
		if _, statErr := os.Stat(part); statErr == nil {
			if _, checkpointed := provider.(browserCheckpointProvider); !checkpointed || s.store == nil {
				return 0, 0, false, nil
			}
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return 0, 0, false, errStorage
		}
	}
	candidates := []*youtube.Format{selection.video}
	if selection.kind == "video/webm" {
		candidates = browserWebMVideoCandidates(video, selection.video)
	}
	var verifiedProgress func() int64
	if _, supports := provider.(browserCheckpointProvider); supports && s.store != nil {
		verifiedProgress = func() int64 {
			var common int64
			for index, key := range []string{"video", "audio"} {
				state, loadErr := s.store.loadSABRCheckpoint(j.ID, item, key)
				if loadErr != nil || state == nil {
					return 0
				}
				if index == 0 || state.Cumulative < common {
					common = state.Cumulative
				}
			}
			return common
		}
	}
	err = retryBrowserCapture(ctx, provider, func(current browserMediaProvider) error {
		pair, ok := current.(browserDualTrackProvider)
		if !ok {
			return errBrowserUnavailable
		}
		checkpointed := s.bindBrowserCheckpoints(current, j, video.ID, nativeProfileID(engine), item, map[string]string{videoPart: "video", audioPart: "audio"})
		for key, part := range map[string]string{"video": videoPart, "audio": audioPart} {
			if checkpointed {
				continue
			}
			format := selection.video
			if key == "audio" {
				format = selection.audio
			}
			if saveErr := s.saveDownloadPart(j, video.ID, item, key, part, "browser-sabr", format, 0, nativeProfileID(engine)); saveErr != nil {
				return errStorage
			}
		}
		var captureErr error
		videoSize, audioSize, captureErr = pair.CaptureTracks(ctx, video.ID, selection.video, candidates, selection.audio, videoPart, audioPart, budget-audioBudget, audioBudget, progress)
		if captureErr == nil {
			v, ve := os.Stat(videoPart)
			a, ae := os.Stat(audioPart)
			if ve != nil || ae != nil || !v.Mode().IsRegular() || !a.Mode().IsRegular() || videoSize <= 0 || audioSize <= 0 || videoSize > budget-audioSize || v.Size() != videoSize || a.Size() != audioSize || (selection.kind == "video/mp4" && (videoSize != selection.video.ContentLength || audioSize != selection.audio.ContentLength)) {
				captureErr = errLength
			}
		}
		for key, part := range map[string]string{"video": videoPart, "audio": audioPart} {
			if checkpointed && (captureErr == nil || errors.Is(captureErr, errBrowserUnavailable) || errors.Is(captureErr, errSABRReadyLimit) || (ctx.Err() != nil && s.keepPartial(j))) {
				continue
			}
			if captureErr != nil {
				if removeErr := os.Remove(part); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
					return errStorage
				}
			}
			if checkpointErr := s.deleteDownloadPart(j, item, key); checkpointErr != nil {
				return errStorage
			}
		}
		return captureErr
	}, verifiedProgress)
	if err != nil && !s.keepPartial(j) {
		for key, part := range map[string]string{"video": videoPart, "audio": audioPart} {
			if removeErr := os.Remove(part); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
				return 0, 0, false, errStorage
			}
			if deleteErr := s.deleteDownloadPart(j, item, key); deleteErr != nil {
				return 0, 0, false, errStorage
			}
		}
	}
	return videoSize, audioSize, err == nil, err
}
