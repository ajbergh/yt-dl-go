package main

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
)

func id3Frames(t *testing.T, tag []byte) map[string][]byte {
	t.Helper()
	if len(tag) < 10 || string(tag[:3]) != "ID3" || tag[3] != 3 {
		t.Fatalf("invalid ID3v2.3 header: %x", tag)
	}
	size := int(tag[6])<<21 | int(tag[7])<<14 | int(tag[8])<<7 | int(tag[9])
	if size != len(tag)-10 {
		t.Fatalf("syncsafe payload size = %d, want %d", size, len(tag)-10)
	}
	frames := map[string][]byte{}
	for offset := 10; offset+10 <= len(tag); {
		id := string(tag[offset : offset+4])
		frameSize := int(binary.BigEndian.Uint32(tag[offset+4 : offset+8]))
		offset += 10
		if frameSize < 0 || offset+frameSize > len(tag) {
			t.Fatalf("invalid %s frame size %d", id, frameSize)
		}
		frames[id] = append([]byte(nil), tag[offset:offset+frameSize]...)
		offset += frameSize
	}
	return frames
}

func TestBuildID3v23TagIncludesTrackSourceAndArtwork(t *testing.T) {
	artwork := []byte{0xff, 0xd8, 0xff, 0xe0, 1, 2, 3, 4}
	tag, err := buildID3v23Tag(mp3TagMetadata{
		Title: "Título – 你好",
		Artist: "Fixture Artist",
		Album: "Fixture Playlist",
		Track: 2, TrackTotal: 12,
		PublishDate: "2026-09-20",
		SourceURL: "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
		Artwork: artwork, ArtworkMIME: "image/jpeg",
	})
	if err != nil {
		t.Fatal(err)
	}
	frames := id3Frames(t, tag)
	for _, id := range []string{"TIT2", "TPE1", "TALB", "TRCK", "TYER", "TXXX", "WOAS", "APIC"} {
		if len(frames[id]) == 0 {
			t.Fatalf("missing ID3 frame %s", id)
		}
	}
	if !bytes.Contains(frames["WOAS"], []byte("youtube.com/watch?v=dQw4w9WgXcQ")) {
		t.Fatal("source URL frame omitted canonical YouTube URL")
	}
	if !bytes.Contains(frames["APIC"], []byte("image/jpeg")) || !bytes.HasSuffix(frames["APIC"], artwork) {
		t.Fatal("cover-art frame omitted MIME type or artwork bytes")
	}
	// UTF-16LE stores ASCII track text with a zero byte after each character.
	if !bytes.Contains(frames["TRCK"], []byte{'2', 0, '/', 0, '1', 0, '2', 0}) {
		t.Fatal("track number/total was not encoded")
	}
}

func TestBuildID3v23TagSkipsOversizedOrUnsupportedArtwork(t *testing.T) {
	for name, metadata := range map[string]mp3TagMetadata{
		"oversized": {Title: "Track", Artwork: make([]byte, maxEmbeddedArtworkBytes+1), ArtworkMIME: "image/jpeg"},
		"unsupported": {Title: "Track", Artwork: []byte("GIF89a"), ArtworkMIME: "image/gif"},
	} {
		t.Run(name, func(t *testing.T) {
			tag, err := buildID3v23Tag(metadata)
			if err != nil {
				t.Fatal(err)
			}
			if len(id3Frames(t, tag)["APIC"]) != 0 {
				t.Fatal("unsupported artwork was embedded")
			}
		})
	}
}

func TestMP3MetadataForPlaylist(t *testing.T) {
	selectedTotal := 2
	j := &jobState{
		Job: Job{Kind: "playlist", Title: "Road Trip", TotalCount: &selectedTotal},
		playlistItemCount: 8,
	}
	file := mediaFile{Title: "Song", Author: "Artist", PublishDate: "2026-09-20"}
	metadata := mp3MetadataFor(j, file, "dQw4w9WgXcQ", 3)
	if metadata.Title != "Song" || metadata.Artist != "Artist" || metadata.Album != "Road Trip" || metadata.Track != 3 || metadata.TrackTotal != 8 {
		t.Fatalf("playlist metadata = %+v", metadata)
	}
	if !strings.HasSuffix(metadata.SourceURL, "dQw4w9WgXcQ") || metadata.PublishDate != "2026-09-20" {
		t.Fatalf("source/date metadata = %+v", metadata)
	}
}
