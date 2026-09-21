package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"strconv"
	"strings"
	"unicode/utf16"
)

const maxEmbeddedArtworkBytes int64 = 1 << 20

type mp3TagMetadata struct {
	Title       string
	Artist      string
	Album       string
	Track       int
	TrackTotal  int
	PublishDate string
	SourceURL   string
	Artwork     []byte
	ArtworkMIME string
}

func syncsafeSize(value int) ([4]byte, error) {
	var result [4]byte
	if value < 0 || value > 0x0fffffff {
		return result, errors.New("ID3 tag is too large")
	}
	result[0] = byte((value >> 21) & 0x7f)
	result[1] = byte((value >> 14) & 0x7f)
	result[2] = byte((value >> 7) & 0x7f)
	result[3] = byte(value & 0x7f)
	return result, nil
}

func id3UTF16(value string) []byte {
	if value == "" {
		return nil
	}
	units := utf16.Encode([]rune(value))
	data := make([]byte, 3+len(units)*2)
	data[0] = 1 // UTF-16 with BOM, supported by ID3v2.3.
	data[1], data[2] = 0xff, 0xfe
	offset := 3
	for _, unit := range units {
		binary.LittleEndian.PutUint16(data[offset:offset+2], unit)
		offset += 2
	}
	return data
}

func appendID3Frame(dst *bytes.Buffer, id string, payload []byte) {
	if len(id) != 4 || len(payload) == 0 {
		return
	}
	_, _ = dst.WriteString(id)
	var size [4]byte
	binary.BigEndian.PutUint32(size[:], uint32(len(payload)))
	_, _ = dst.Write(size[:])
	_, _ = dst.Write([]byte{0, 0})
	_, _ = dst.Write(payload)
}

func appendID3TextFrame(dst *bytes.Buffer, id, value string) {
	appendID3Frame(dst, id, id3UTF16(strings.TrimSpace(value)))
}

func appendID3UserTextFrame(dst *bytes.Buffer, description, value string) {
	description = strings.TrimSpace(description)
	value = strings.TrimSpace(value)
	if description == "" || value == "" {
		return
	}
	payload := id3UTF16(description)
	payload = append(payload, 0, 0)
	encodedValue := id3UTF16(value)
	if len(encodedValue) >= 3 {
		encodedValue = encodedValue[3:] // one frame encoding marker/BOM is enough.
	}
	payload = append(payload, encodedValue...)
	appendID3Frame(dst, "TXXX", payload)
}

func buildID3v23Tag(metadata mp3TagMetadata) ([]byte, error) {
	var frames bytes.Buffer
	appendID3TextFrame(&frames, "TIT2", metadata.Title)
	appendID3TextFrame(&frames, "TPE1", metadata.Artist)
	appendID3TextFrame(&frames, "TALB", metadata.Album)
	if metadata.Track > 0 {
		track := strconv.Itoa(metadata.Track)
		if metadata.TrackTotal > 0 {
			track += "/" + strconv.Itoa(metadata.TrackTotal)
		}
		appendID3TextFrame(&frames, "TRCK", track)
	}
	if len(metadata.PublishDate) >= 4 {
		appendID3TextFrame(&frames, "TYER", metadata.PublishDate[:4])
		appendID3UserTextFrame(&frames, "Publish Date", metadata.PublishDate)
	}
	sourceURL := strings.TrimSpace(metadata.SourceURL)
	if sourceURL != "" {
		appendID3Frame(&frames, "WOAS", []byte(sourceURL))
	}
	if len(metadata.Artwork) > 0 && int64(len(metadata.Artwork)) <= maxEmbeddedArtworkBytes && allowedThumbnailMime(metadata.ArtworkMIME) {
		payload := make([]byte, 0, len(metadata.Artwork)+len(metadata.ArtworkMIME)+4)
		payload = append(payload, 0) // ISO-8859-1 description encoding; description is empty.
		payload = append(payload, metadata.ArtworkMIME...)
		payload = append(payload, 0)
		payload = append(payload, 3, 0) // Front cover, then empty description.
		payload = append(payload, metadata.Artwork...)
		appendID3Frame(&frames, "APIC", payload)
	}
	if frames.Len() == 0 {
		return nil, nil
	}
	size, err := syncsafeSize(frames.Len())
	if err != nil {
		return nil, err
	}
	tag := make([]byte, 10+frames.Len())
	copy(tag[:3], "ID3")
	tag[3], tag[4], tag[5] = 3, 0, 0
	copy(tag[6:10], size[:])
	copy(tag[10:], frames.Bytes())
	return tag, nil
}

func readThumbnailArtwork(j *jobState, file mediaFile) ([]byte, string) {
	if !file.ThumbnailLocalAvailable || !allowedThumbnailMime(file.ThumbnailMimeType) {
		return nil, ""
	}
	handle, err := openTrackedThumbnail(j, file)
	if err != nil {
		return nil, ""
	}
	defer handle.Close()
	info, err := handle.Stat()
	if err != nil || info.Size() <= 0 || info.Size() > maxEmbeddedArtworkBytes {
		return nil, ""
	}
	data, err := io.ReadAll(io.LimitReader(handle, maxEmbeddedArtworkBytes+1))
	if err != nil || int64(len(data)) != info.Size() {
		return nil, ""
	}
	return data, file.ThumbnailMimeType
}

func mp3MetadataFor(j *jobState, file mediaFile, videoID string, index int) mp3TagMetadata {
	metadata := mp3TagMetadata{
		Title:       file.Title,
		Artist:      file.Author,
		Track:       index,
		PublishDate: file.PublishDate,
		SourceURL:   "https://www.youtube.com/watch?v=" + videoID,
	}
	if j != nil && j.Kind == "playlist" {
		metadata.Album = j.Title
		if j.TotalCount != nil {
			metadata.TrackTotal = *j.TotalCount
		}
	}
	metadata.Artwork, metadata.ArtworkMIME = readThumbnailArtwork(j, file)
	return metadata
}
