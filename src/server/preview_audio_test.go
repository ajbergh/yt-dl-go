package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kkdai/youtube/v2"
	mp3 "github.com/tphakala/go-mp3"
)

// Independent synthetic stereo material that switches between center-panned
// and decorrelated channels, exercising joint-stereo transitions/reservoirs.
func previewMP3Fixture(t *testing.T) []byte {
	t.Helper()
	encoder, err := mp3.NewEncoder(mp3.EncoderConfig{SampleRate: 44100, Channels: 2, Bitrate: 192000, RateControl: mp3.RateControlFast})
	if err != nil {
		t.Fatal(err)
	}
	var encoded []byte
	channels := [][]float32{make([]float32, mp3.FrameSize), make([]float32, mp3.FrameSize)}
	for frame := 0; frame < 120; frame++ {
		for sample := 0; sample < mp3.FrameSize; sample++ {
			time := float64(frame*mp3.FrameSize+sample) / 44100
			left := 0.35*math.Sin(2*math.Pi*440*time) + 0.1*math.Sin(2*math.Pi*1760*time)
			right := left
			if frame/6%2 != 0 {
				right = 0.35*math.Sin(2*math.Pi*659*time) + 0.1*math.Sin(2*math.Pi*2200*time)
			}
			channels[0][sample] = float32(left)
			channels[1][sample] = float32(right)
		}
		encoded, err = encoder.EncodeFrame(encoded, channels)
		if err != nil {
			t.Fatal(err)
		}
	}
	encoded, err = encoder.EncodeFrame(encoded, nil)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func TestPCMPreviewWAVMatchesIndependentDecoder(t *testing.T) {
	source := previewMP3Fixture(t)
	dir := t.TempDir()
	wave, err := prepareMP3Preview(context.Background(), dir, bytes.NewReader(source))
	if err != nil {
		t.Fatal(err)
	}
	defer wave.Close()
	data, err := io.ReadAll(wave)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) < 44 || string(data[:4]) != "RIFF" || string(data[8:16]) != "WAVEfmt " || string(data[36:40]) != "data" {
		t.Fatal("invalid preview WAV")
	}
	if binary.LittleEndian.Uint32(data[4:8]) != uint32(len(data)-8) || binary.LittleEndian.Uint32(data[40:44]) != uint32(len(data)-44) {
		t.Fatal("preview WAV sizes do not match decoded output")
	}
	if binary.LittleEndian.Uint16(data[22:24]) != 2 || binary.LittleEndian.Uint32(data[24:28]) != 44100 || binary.LittleEndian.Uint16(data[34:36]) != 16 {
		t.Fatal("preview changed sample format")
	}
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Log("ffmpeg absent; WAV structure checked, independent signal comparison skipped")
		return
	}
	input := filepath.Join(dir, "stereo-switches.mp3")
	if err := os.WriteFile(input, source, 0600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(ffmpeg, "-v", "error", "-nostdin", "-i", input, "-f", "s16le", "-acodec", "pcm_s16le", "-")
	var stderr bytes.Buffer
	command.Stderr = &stderr
	reference, err := command.Output()
	if err != nil || stderr.Len() != 0 {
		t.Fatalf("independent decode: %v %s", err, stderr.String())
	}
	actual := data[44:]
	if len(actual) != len(reference) {
		t.Fatalf("decoded length %d, want %d", len(actual), len(reference))
	}
	var cross, x2, y2 float64
	for offset := 0; offset+1 < len(actual); offset += 2 {
		x := float64(int16(binary.LittleEndian.Uint16(reference[offset : offset+2])))
		y := float64(int16(binary.LittleEndian.Uint16(actual[offset : offset+2])))
		cross += x * y
		x2 += x * x
		y2 += y * y
	}
	correlation := cross / math.Sqrt(x2*y2)
	if correlation < 0.999 {
		t.Fatalf("preview audio diverges from reference: %.6f", correlation)
	}
	t.Logf("stereo-transition preview correlation %.9f", correlation)
}

type previewCancellingReader struct {
	reader io.Reader
	cancel context.CancelFunc
}

func (r previewCancellingReader) Read(data []byte) (int, error) {
	n, err := r.reader.Read(data)
	if n > 0 {
		r.cancel()
	}
	return n, err
}

func TestPCMPreviewCancellationAndInvalidMediaCleanUp(t *testing.T) {
	dir := t.TempDir()
	for _, source := range [][]byte{[]byte("not MP3 audio"), nil} {
		if output, err := prepareMP3Preview(context.Background(), dir, bytes.NewReader(source)); err == nil {
			output.Close()
			t.Fatal("invalid MP3 accepted")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := prepareMP3Preview(ctx, dir, bytes.NewReader(previewMP3Fixture(t))); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled preview: %v", err)
	}
	during, stop := context.WithCancel(context.Background())
	if _, err := prepareMP3Preview(during, dir, previewCancellingReader{bytes.NewReader(previewMP3Fixture(t)), stop}); !errors.Is(err, context.Canceled) {
		t.Fatalf("mid-decode cancellation: %v", err)
	}
	files, _ := os.ReadDir(dir)
	if len(files) != 0 {
		t.Fatalf("failed previews retained temporary files: %v", files)
	}
}

func TestInlineMP3PreviewServesWAVAndDownloadsPreserveMP3(t *testing.T) {
	data, err := os.ReadFile("testdata/audio/silence.m4a")
	if err != nil {
		t.Fatal(err)
	}
	fake := fixtureClient(1)
	fake.videoFn = func(_ context.Context, id string) (*youtube.Video, error) {
		video := fixtureVideo(id)
		video.Formats = youtube.FormatList{{ItagNo: 140, MimeType: `audio/mp4; codecs="mp4a.40.2"`, AudioChannels: 2, AudioSampleRate: "44100", ContentLength: int64(len(data))}}
		return video, nil
	}
	fake.streamFn = func(context.Context, *youtube.Video, *youtube.Format) (io.ReadCloser, int64, error) {
		return io.NopCloser(bytes.NewReader(data)), int64(len(data)), nil
	}
	s := testServer(t, fake, nil)
	response := request(s, "POST", "/api/jobs", `{"url":"`+testVideo+`","quality":"best","mediaType":"audio","audioFormat":"mp3","audioBitrate":"192k","rightsConfirmed":true}`, nil)
	var created Job
	if json.Unmarshal(response.Body.Bytes(), &created) != nil || created.ID == "" {
		t.Fatalf("create: %s", response.Body.String())
	}
	job := waitTerminal(t, s, created.ID)
	if job.Status != "completed" || len(job.Files) != 1 {
		t.Fatalf("audio fixture failed: %+v", job)
	}
	original, err := os.ReadFile(trackedOutputPath(t, s, job.ID, job.Files[0].ID))
	if err != nil {
		t.Fatal(err)
	}
	path := ticketPath(t, s, job.ID, `{"fileId":"`+job.Files[0].ID+`","inline":true}`)
	preview := request(s, "GET", path, "", map[string]string{"Authorization": ""})
	if preview.Code != 200 || preview.Header().Get("Content-Type") != "audio/wav" || !bytes.HasPrefix(preview.Body.Bytes(), []byte("RIFF")) {
		t.Fatalf("PCM preview: %d %s", preview.Code, preview.Body.String())
	}
	if !strings.HasPrefix(preview.Header().Get("Content-Disposition"), "inline") {
		t.Fatal("preview not inline")
	}
	ranged := request(s, "GET", path, "", map[string]string{"Authorization": "", "Range": "bytes=0-43"})
	if ranged.Code != http.StatusPartialContent || !bytes.Equal(ranged.Body.Bytes(), preview.Body.Bytes()[:44]) {
		t.Fatalf("WAV range: %d", ranged.Code)
	}
	downloadPath := ticketPath(t, s, job.ID, `{"fileId":"`+job.Files[0].ID+`"}`)
	download := request(s, "GET", downloadPath, "", map[string]string{"Authorization": ""})
	if download.Code != 200 || download.Header().Get("Content-Type") != "audio/mpeg" || !bytes.Equal(download.Body.Bytes(), original) {
		t.Fatal("saved/downloaded MP3 changed")
	}
	remaining, _ := filepath.Glob(filepath.Join(s.cfg.root, ".audio-preview-*.wav"))
	if len(remaining) != 0 {
		t.Fatalf("temporary preview files retained: %v", remaining)
	}
}
