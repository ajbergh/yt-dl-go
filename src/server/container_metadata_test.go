package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/at-wat/ebml-go"
	"github.com/at-wat/ebml-go/webm"
)

func mustISOBox(t *testing.T, kind string, payload []byte) []byte {
	t.Helper()
	box, err := makeISOBox(kind, payload, false)
	if err != nil {
		t.Fatal(err)
	}
	return box
}

func buildMetadataMP4Fixture(t *testing.T) ([]byte, []byte, int64) {
	t.Helper()
	ftyp := mustISOBox(t, "ftyp", append([]byte("isom"), 0, 0, 0, 0, 'i', 's', 'o', 'm'))
	makeMoov := func(offset uint32) []byte {
		stcoPayload := make([]byte, 12)
		binary.BigEndian.PutUint32(stcoPayload[4:8], 1)
		binary.BigEndian.PutUint32(stcoPayload[8:12], offset)
		stco := mustISOBox(t, "stco", stcoPayload)
		stbl := mustISOBox(t, "stbl", stco)
		minf := mustISOBox(t, "minf", stbl)
		mdia := mustISOBox(t, "mdia", minf)
		trak := mustISOBox(t, "trak", mdia)
		return mustISOBox(t, "moov", trak)
	}
	moov := makeMoov(0)
	chunkOffset := uint32(len(ftyp) + len(moov) + 8)
	moov = makeMoov(chunkOffset)
	mdatPayload := []byte("preserved media payload")
	mdat := mustISOBox(t, "mdat", mdatPayload)
	input := append(append(append([]byte(nil), ftyp...), moov...), mdat...)
	return input, mdatPayload, int64(len(ftyp) + 8)
}

func findISOBox(raw []byte, kind string) ([]byte, bool) {
	for offset := 0; offset < len(raw); {
		header, err := decodeISOBoxHeader(raw[offset:])
		if err != nil {
			return nil, false
		}
		box := raw[offset : offset+int(header.size)]
		if header.kind == kind {
			return box, true
		}
		prefix, container := isoContainerAtoms[header.kind]
		if container && prefix <= int(header.size)-header.headerBytes {
			if found, ok := findISOBox(box[header.headerBytes+prefix:], kind); ok {
				return found, true
			}
		}
		offset += int(header.size)
	}
	return nil, false
}

func TestRewriteISOContainerEmbedsMetadataAndMovesChunkOffsets(t *testing.T) {
	input, mdatPayload, wantChunkOffset := buildMetadataMP4Fixture(t)
	directory := t.TempDir()
	path := filepath.Join(directory, "fixture.m4a")
	if err := os.WriteFile(path, input, 0600); err != nil {
		t.Fatal(err)
	}
	metadata := containerTagMetadata{
		Title: "Títle – 你好", Artist: "Fixture Artist", Album: "Travel Mix", PublishDate: "2026-09-23",
		SourceURL: "https://www.youtube.com/watch?v=dQw4w9WgXcQ", Artwork: []byte{0xff, 0xd8, 0xff, 0xe0, 1, 2}, ArtworkMIME: "image/jpeg",
	}
	size, err := rewriteISOContainerFile(context.Background(), path, metadata, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if int64(len(data)) != size || size <= int64(len(input)) {
		t.Fatalf("rewritten size = %d, reported %d; original = %d", len(data), size, len(input))
	}
	first, err := readISOBoxHeader(bytes.NewReader(data), 0, int64(len(data)))
	if err != nil || first.kind != "ftyp" {
		t.Fatalf("first top-level box = %s, err=%v", first.kind, err)
	}
	second, err := readISOBoxHeader(bytes.NewReader(data), int64(first.size), int64(len(data)))
	if err != nil || second.kind != "mdat" {
		t.Fatalf("second top-level box = %s, err=%v", second.kind, err)
	}
	mdatStart := int(first.size) + second.headerBytes
	mdatEnd := int(first.size + second.size)
	mdat := data[mdatStart:mdatEnd]
	if !bytes.Equal(mdat, mdatPayload) {
		t.Fatalf("media payload changed: %q", mdat)
	}
	moovOffset := int64(first.size + second.size)
	moovHeader, err := readISOBoxHeader(bytes.NewReader(data), moovOffset, int64(len(data)))
	if err != nil || moovHeader.kind != "moov" {
		t.Fatalf("final top-level box = %s, err=%v", moovHeader.kind, err)
	}
	moov := data[moovOffset : moovOffset+int64(moovHeader.size)]
	stco, ok := findISOBox(moov[moovHeader.headerBytes:], "stco")
	if !ok {
		t.Fatal("rewritten moov has no stco box")
	}
	stcoHeader, _ := decodeISOBoxHeader(stco)
	gotChunkOffset := binary.BigEndian.Uint32(stco[stcoHeader.headerBytes+8 : stcoHeader.headerBytes+12])
	if int64(gotChunkOffset) != wantChunkOffset {
		t.Fatalf("stco chunk offset = %d, want %d", gotChunkOffset, wantChunkOffset)
	}
	udta, ok := findISOBox(moov[moovHeader.headerBytes:], "udta")
	if !ok {
		t.Fatal("moov has no udta metadata")
	}
	meta, ok := findISOBox(udta[8:], "meta")
	if !ok {
		t.Fatal("udta has no meta atom")
	}
	ilst, ok := findISOBox(meta[12:], "ilst")
	if !ok {
		t.Fatal("meta has no ilst atom")
	}
	for _, atom := range []string{string([]byte{0xA9, 'n', 'a', 'm'}), string([]byte{0xA9, 'A', 'R', 'T'}), string([]byte{0xA9, 'a', 'l', 'b'}), string([]byte{0xA9, 'd', 'a', 'y'}), string([]byte{0xA9, 'c', 'm', 't'}), "covr"} {
		if _, ok := findISOBox(ilst[8:], atom); !ok {
			t.Errorf("ilst is missing %q", atom)
		}
	}
	covr, _ := findISOBox(ilst[8:], "covr")
	dataBox, ok := findISOBox(covr[8:], "data")
	if !ok {
		t.Fatal("covr has no data atom")
	}
	dataHeader, _ := decodeISOBoxHeader(dataBox)
	if kind := binary.BigEndian.Uint32(dataBox[dataHeader.headerBytes : dataHeader.headerBytes+4]); kind != 13 {
		t.Fatalf("JPEG covr data type = %d, want 13", kind)
	}
	if !bytes.Contains(data, []byte(metadata.Title)) || !bytes.Contains(data, []byte(metadata.SourceURL)) {
		t.Fatal("Unicode title or canonical source URL is absent")
	}
}

func TestRewriteISOContainerBudgetFailurePreservesOriginal(t *testing.T) {
	input, _, _ := buildMetadataMP4Fixture(t)
	path := filepath.Join(t.TempDir(), "fixture.mp4")
	if err := os.WriteFile(path, input, 0600); err != nil {
		t.Fatal(err)
	}
	_, err := rewriteISOContainerFile(context.Background(), path, containerTagMetadata{Title: "Title"}, int64(len(input)))
	if !errors.Is(err, errLimit) {
		t.Fatalf("rewrite error = %v, want errLimit", err)
	}
	got, readErr := os.ReadFile(path)
	if readErr != nil || !bytes.Equal(got, input) {
		t.Fatalf("budget failure changed original file: err=%v", readErr)
	}
}

func TestMP4MetadataSkipsNonPortableOrOversizedArtwork(t *testing.T) {
	for name, metadata := range map[string]containerTagMetadata{
		"webp":      {Title: "Title", Artwork: []byte("RIFFwebp"), ArtworkMIME: "image/webp"},
		"oversized": {Title: "Title", Artwork: make([]byte, maxEmbeddedArtworkBytes+1), ArtworkMIME: "image/jpeg"},
	} {
		t.Run(name, func(t *testing.T) {
			box, err := makeMP4MetadataBox(metadata)
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := findISOBox(box, "covr"); ok {
				t.Fatal("non-portable or oversized artwork was embedded")
			}
		})
	}
}

func TestRewriteWebMContainerAddsTagsAttachmentAndAdjustsCues(t *testing.T) {
	directory := t.TempDir()
	videoPath := filepath.Join(directory, "video.webm")
	audioPath := filepath.Join(directory, "audio.webm")
	outputPath := filepath.Join(directory, "output.webm")
	videoTrack := webm.TrackEntry{
		TrackNumber: 7, TrackUID: 700, TrackType: webMVideoTrackType,
		CodecID: "V_VP9", DefaultDuration: 33333333, Video: &webm.Video{PixelWidth: 640, PixelHeight: 360},
	}
	audioTrack := webm.TrackEntry{
		TrackNumber: 9, TrackUID: 900, TrackType: webMAudioTrackType, CodecID: "A_OPUS",
		CodecPrivate: []byte("OpusHead\x01\x02"), CodecDelay: 6500000, SeekPreRoll: 80000000,
		Audio: &webm.Audio{SamplingFrequency: 48000, Channels: 2},
	}
	writeWebMFixture(t, videoPath, videoTrack, []webMFrame{{data: []byte{0x10, 0x11}, keyframe: true, timestamp: 0}, {data: []byte{0x12}, timestamp: 40}})
	writeWebMFixture(t, audioPath, audioTrack, []webMFrame{{data: []byte{0x20}, keyframe: true, timestamp: 0}, {data: []byte{0x21}, keyframe: true, timestamp: 40}})
	if err := muxWebM(context.Background(), videoPath, audioPath, outputPath); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	originalFile, err := os.Open(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	_, cuesStart, cuesEnd, clusterStart, err := readWebMLayout(originalFile, int64(len(original)))
	if err != nil {
		_ = originalFile.Close()
		t.Fatal(err)
	}
	var oldCueEnvelope struct {
		Cues webm.Cues `ebml:"Cues"`
	}
	if err := ebml.Unmarshal(bytes.NewReader(original[cuesStart:cuesEnd]), &oldCueEnvelope); err != nil {
		_ = originalFile.Close()
		t.Fatal(err)
	}
	_ = originalFile.Close()
	metadata := containerTagMetadata{
		Title: "Títle – 你好", Artist: "Fixture Artist", Album: "Fixture Playlist", PublishDate: "2026-09-23",
		SourceURL: "https://www.youtube.com/watch?v=dQw4w9WgXcQ", Artwork: []byte{0xff, 0xd8, 0xff, 0xe0, 1, 2}, ArtworkMIME: "image/jpeg",
	}
	encodedMetadata, err := makeWebMMetadataBox(metadata)
	if err != nil {
		t.Fatal(err)
	}
	size, err := rewriteWebMContainerFile(context.Background(), outputPath, metadata, int64(len(original))+int64(len(encodedMetadata)))
	if err != nil {
		t.Fatal(err)
	}
	updated, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if size != int64(len(original)+len(encodedMetadata)) {
		t.Fatalf("rewritten size = %d, want %d", size, len(original)+len(encodedMetadata))
	}
	updatedFile, err := os.Open(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	_, updatedCuesStart, updatedCuesEnd, updatedClusterStart, err := readWebMLayout(updatedFile, int64(len(updated)))
	if err != nil {
		_ = updatedFile.Close()
		t.Fatal(err)
	}
	var updatedCueEnvelope struct {
		Cues webm.Cues `ebml:"Cues"`
	}
	if err := ebml.Unmarshal(bytes.NewReader(updated[updatedCuesStart:updatedCuesEnd]), &updatedCueEnvelope); err != nil {
		_ = updatedFile.Close()
		t.Fatal(err)
	}
	_ = updatedFile.Close()
	if updatedClusterStart != clusterStart+int64(len(encodedMetadata)) {
		t.Fatalf("cluster moved to %d, want %d", updatedClusterStart, clusterStart+int64(len(encodedMetadata)))
	}
	if !bytes.Equal(updated[updatedClusterStart:], original[clusterStart:]) {
		t.Fatal("cluster and media data changed while adding metadata")
	}
	if len(oldCueEnvelope.Cues.CuePoint) == 0 || len(updatedCueEnvelope.Cues.CuePoint) == 0 {
		t.Fatal("test mux emitted no cue points")
	}
	oldPosition := oldCueEnvelope.Cues.CuePoint[0].CueTrackPositions[0].CueClusterPosition
	newPosition := updatedCueEnvelope.Cues.CuePoint[0].CueTrackPositions[0].CueClusterPosition
	if newPosition != oldPosition+uint64(len(encodedMetadata)) {
		t.Fatalf("cue cluster position = %d, want %d", newPosition, oldPosition+uint64(len(encodedMetadata)))
	}
	metadataOffset := updatedClusterStart - int64(len(encodedMetadata))
	if !bytes.Equal(updated[metadataOffset:updatedClusterStart], encodedMetadata) {
		t.Fatal("Tags and Attachments were not placed immediately before the first Cluster")
	}
	var parsed webMMetadataEnvelope
	if err := ebml.Unmarshal(bytes.NewReader(encodedMetadata), &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.Tags == nil || len(parsed.Tags.Tag) != 1 || len(parsed.Tags.Tag[0].SimpleTag) != 5 {
		t.Fatalf("WebM Tags = %+v", parsed.Tags)
	}
	gotTags := make(map[string]string, len(parsed.Tags.Tag[0].SimpleTag))
	for _, tag := range parsed.Tags.Tag[0].SimpleTag {
		gotTags[tag.Name] = tag.Text
	}
	for name, want := range map[string]string{
		"TITLE": metadata.Title, "ARTIST": metadata.Artist, "ALBUM": metadata.Album,
		"DATE_RELEASED": metadata.PublishDate, "COMMENT": metadata.SourceURL,
	} {
		if gotTags[name] != want {
			t.Errorf("WebM tag %s = %q, want %q", name, gotTags[name], want)
		}
	}
	if parsed.Attachments == nil || len(parsed.Attachments.AttachedFile) != 1 {
		t.Fatalf("WebM Attachments = %+v", parsed.Attachments)
	}
	cover := parsed.Attachments.AttachedFile[0]
	if cover.MIME != "image/jpeg" || !bytes.Equal(cover.Data, metadata.Artwork) {
		t.Fatalf("cover attachment = %+v", cover)
	}
}

func TestRewriteWebMContainerBudgetFailurePreservesOriginal(t *testing.T) {
	directory := t.TempDir()
	videoPath := filepath.Join(directory, "video.webm")
	audioPath := filepath.Join(directory, "audio.webm")
	outputPath := filepath.Join(directory, "output.webm")
	videoTrack := webm.TrackEntry{TrackNumber: 1, TrackUID: 1, TrackType: webMVideoTrackType, CodecID: "V_VP9", Video: &webm.Video{PixelWidth: 16, PixelHeight: 16}}
	audioTrack := webm.TrackEntry{TrackNumber: 2, TrackUID: 2, TrackType: webMAudioTrackType, CodecID: "A_OPUS", CodecPrivate: []byte("OpusHead\x01\x02"), Audio: &webm.Audio{SamplingFrequency: 48000, Channels: 2}}
	writeWebMFixture(t, videoPath, videoTrack, []webMFrame{{data: []byte{1}, keyframe: true}})
	writeWebMFixture(t, audioPath, audioTrack, []webMFrame{{data: []byte{2}, keyframe: true}})
	if err := muxWebM(context.Background(), videoPath, audioPath, outputPath); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = rewriteWebMContainerFile(context.Background(), outputPath, containerTagMetadata{Title: "Title"}, int64(len(original)))
	if !errors.Is(err, errLimit) {
		t.Fatalf("rewrite error = %v, want errLimit", err)
	}
	got, err := os.ReadFile(outputPath)
	if err != nil || !bytes.Equal(got, original) {
		t.Fatalf("budget failure changed original WebM: %v", err)
	}
}
