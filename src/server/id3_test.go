package main

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
	"unicode/utf16"
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
		Title:  "Título – 你好",
		Artist: "Fixture Artist",
		Album:  "Fixture Playlist",
		Track:  2, TrackTotal: 12,
		PublishDate: "2026-09-20",
		SourceURL:   "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
		Artwork:     artwork, ArtworkMIME: "image/jpeg",
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
		"oversized":   {Title: "Track", Artwork: make([]byte, maxEmbeddedArtworkBytes+1), ArtworkMIME: "image/jpeg"},
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
		Job:               Job{Kind: "playlist", Title: "Road Trip", TotalCount: &selectedTotal},
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

// Each UTF-16 string in a TXXX frame needs a BOM. A frame-wide encoding
// marker does not supply the byte order for the value after the terminator.
func TestID3UserTextIncludesByteOrderForBothStrings(t *testing.T) {
	var frames bytes.Buffer
	appendID3UserTextFrame(&frames, "Publish Date", "2026-09-20")
	data := frames.Bytes()
	size := int(binary.BigEndian.Uint32(data[4:8]))
	if string(data[:4]) != "TXXX" || size != len(data)-10 {
		t.Fatal("invalid user-text frame")
	}
	payload := data[10:]
	if len(payload) < 3 || payload[0] != 1 || !bytes.Equal(payload[1:3], []byte{0xff, 0xfe}) {
		t.Fatal("missing description byte order")
	}
	end := 3
	for end+1 < len(payload) && (payload[end] != 0 || payload[end+1] != 0) {
		end += 2
	}
	value := payload[end+2:]
	if len(value) < 2 || !bytes.Equal(value[:2], []byte{0xff, 0xfe}) {
		t.Fatal("missing value byte order; strict MP3 readers reject this tag")
	}
	var units []uint16
	for i := 2; i+1 < len(value); i += 2 {
		units = append(units, binary.LittleEndian.Uint16(value[i:i+2]))
	}
	if got := string(utf16.Decode(units)); got != "2026-09-20" {
		t.Fatalf("date value = %q", got)
	}
}
