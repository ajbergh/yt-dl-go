package main

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	mp3pcm "github.com/tphakala/go-mp3/pcm"
)

type previewContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r previewContextReader) Read(data []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(data)
}

// Decode with the Go decoder, rather than Chromium's MP3 decoder: some
// reservoir-backed joint-stereo transitions otherwise become silent gaps.
// Spool PCM to a private temporary WAV so memory remains bounded and its
// declared length and HTTP byte ranges describe the complete decoded stream.
func prepareMP3Preview(ctx context.Context, root string, source io.Reader) (result *os.File, err error) {
	decoder, err := mp3pcm.NewDecoder(previewContextReader{ctx, source})
	if err != nil {
		return nil, err
	}
	info := decoder.Info()
	if info.SampleRate <= 0 || info.SampleRate > 192000 || info.Channels < 1 || info.Channels > 2 {
		return nil, errors.New("unsupported audio preview format")
	}
	output, err := os.CreateTemp(root, ".audio-preview-*.wav")
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			_ = output.Close()
			_ = os.Remove(output.Name())
		}
	}()
	if _, err = output.Write(make([]byte, 44)); err != nil {
		return nil, err
	}
	// RIFF/WAV has 32-bit size fields; this is a container bound, not a job quota.
	const maxPCMBytes = int64(1<<32 - 1 - 36)
	count, err := io.Copy(output, previewContextReader{ctx, io.LimitReader(decoder, maxPCMBytes+1)})
	if err != nil {
		return nil, err
	}
	if count == 0 || count > maxPCMBytes || count%int64(info.Channels*2) != 0 {
		return nil, errors.New("audio preview is empty, incomplete, or too long for WAV")
	}
	header := make([]byte, 44)
	copy(header[0:4], "RIFF")
	binary.LittleEndian.PutUint32(header[4:8], uint32(count+36))
	copy(header[8:16], "WAVEfmt ")
	binary.LittleEndian.PutUint32(header[16:20], 16)
	binary.LittleEndian.PutUint16(header[20:22], 1) // interleaved signed 16-bit PCM
	binary.LittleEndian.PutUint16(header[22:24], uint16(info.Channels))
	binary.LittleEndian.PutUint32(header[24:28], uint32(info.SampleRate))
	binary.LittleEndian.PutUint32(header[28:32], uint32(info.SampleRate*info.Channels*2))
	binary.LittleEndian.PutUint16(header[32:34], uint16(info.Channels*2))
	binary.LittleEndian.PutUint16(header[34:36], 16)
	copy(header[36:40], "data")
	binary.LittleEndian.PutUint32(header[40:44], uint32(count))
	if _, err = output.WriteAt(header, 0); err != nil {
		return nil, err
	}
	if _, err = output.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	return output, nil
}

func (s *server) serveMP3Preview(w http.ResponseWriter, r *http.Request, source io.Reader, name string) {
	preview, err := prepareMP3Preview(r.Context(), s.cfg.root, source)
	if err != nil {
		if r.Context().Err() == nil {
			fail(w, http.StatusUnprocessableEntity, "Audio preview could not be decoded. The saved MP3 is still available.")
		}
		return
	}
	defer func() { _ = preview.Close(); _ = os.Remove(preview.Name()) }()
	info, err := preview.Stat()
	if err != nil {
		fail(w, 500, "Audio preview could not be read")
		return
	}
	if !validRange(r.Header.Get("Range"), info.Size()) {
		w.Header().Set("Content-Range", "bytes */"+strconv.FormatInt(info.Size(), 10))
		fail(w, http.StatusRequestedRangeNotSatisfiable, "Only one valid byte range is supported")
		return
	}
	name = strings.TrimSuffix(name, filepath.Ext(name)) + ".wav"
	w.Header().Set("Content-Type", "audio/wav")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": name}))
	http.ServeContent(w, r, name, time.Time{}, preview)
}
