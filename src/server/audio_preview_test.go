package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// An independent decoder verifies sound, rather than just file validity.
func TestMP3PreviewPreservesAudioSignal(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg is required for the independent audio oracle")
	}
	for _, rate := range []string{"44100", "48000"} {
		t.Run(rate, func(t *testing.T) {
			dir := t.TempDir()
			sourcePath := filepath.Join(dir, "source.m4a")
			run := func(args ...string) []byte {
				t.Helper()
				cmd := exec.Command(ffmpeg, append([]string{"-v", "error", "-nostdin"}, args...)...)
				var stderr bytes.Buffer
				cmd.Stderr = &stderr
				out, err := cmd.Output()
				if err != nil || stderr.Len() != 0 {
					t.Fatalf("ffmpeg: %v %s", err, stderr.String())
				}
				return out
			}
			run("-f", "lavfi", "-i", "aevalsrc=0.25*sin(2*PI*440*t)+0.15*sin(2*PI*1760*t)|0.25*sin(2*PI*659*t)+0.15*sin(2*PI*2200*t):s="+rate+":d=2", "-c:a", "aac", "-b:a", "192k", sourcePath)
			source, err := os.Open(sourcePath)
			if err != nil {
				t.Fatal(err)
			}
			defer source.Close()
			tag, err := buildID3v23Tag(mp3TagMetadata{Title: "Preview fidelity", PublishDate: "2026-09-20"})
			if err != nil {
				t.Fatal(err)
			}
			var encoded bytes.Buffer
			if _, err := convertAACToMP3Tagged(context.Background(), source, &encoded, "192k", math.MaxInt64, tag, nil); err != nil {
				t.Fatal(err)
			}
			outputPath := filepath.Join(dir, "preview.mp3")
			if err := os.WriteFile(outputPath, encoded.Bytes(), 0600); err != nil {
				t.Fatal(err)
			}
			reference := run("-i", sourcePath, "-f", "s16le", "-acodec", "pcm_s16le", "-")
			actual := run("-i", outputPath, "-f", "s16le", "-acodec", "pcm_s16le", "-")
			if len(reference)/4 < 18144 || len(actual)/4 < 18144 {
				t.Fatal("decoded audio is truncated")
			}
			sample := func(data []byte, index, channel int) float64 {
				offset := (index*2 + channel) * 2
				return float64(int16(binary.LittleEndian.Uint16(data[offset:offset+2]))) / 32768
			}
			for channel := 0; channel < 2; channel++ {
				best := 0.0
				for lag := -4096; lag <= 4096; lag++ {
					var cross, x2, y2 float64
					for i := 12000; i < 14048; i++ {
						x := sample(reference, i, channel)
						y := sample(actual, i+lag, channel)
						cross += x * y
						x2 += x * x
						y2 += y * y
					}
					if x2 > 0 && y2 > 0 {
						best = math.Max(best, cross/math.Sqrt(x2*y2))
					}
				}
				t.Logf("channel %d source/preview correlation %.4f", channel, best)
				if best < 0.95 {
					t.Errorf("garbled MP3 channel %d: correlation %.4f", channel, best)
				}
			}
		})
	}
}
