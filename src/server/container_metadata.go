package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/at-wat/ebml-go"
	"github.com/at-wat/ebml-go/webm"
)

const containerMetadataHeadroom int64 = maxEmbeddedArtworkBytes + 80*1024

type containerTagMetadata struct {
	Title       string
	Artist      string
	Album       string
	PublishDate string
	SourceURL   string
	Artwork     []byte
	ArtworkMIME string
	Chapters    []mediaChapter
}

func containerMetadataFor(j *jobState, file mediaFile, videoID string) containerTagMetadata {
	metadata := containerTagMetadata{
		Title:       file.Title,
		Artist:      file.Author,
		PublishDate: file.PublishDate,
		Chapters:    file.Chapters,
		SourceURL:   "https://www.youtube.com/watch?v=" + url.PathEscape(videoID),
	}
	if j != nil && j.Kind == "playlist" {
		metadata.Album = j.Title
	}
	metadata.Artwork, metadata.ArtworkMIME = readThumbnailArtwork(j, file)
	return metadata
}

func hasContainerTags(metadata containerTagMetadata) bool {
	return strings.TrimSpace(metadata.Title) != "" || strings.TrimSpace(metadata.Artist) != "" ||
		strings.TrimSpace(metadata.Album) != "" || strings.TrimSpace(metadata.PublishDate) != "" ||
		strings.TrimSpace(metadata.SourceURL) != "" || len(metadata.Artwork) > 0 || len(metadata.Chapters) > 0
}

func supportsContainerMetadata(mimeType string) bool {
	switch mimeType {
	case "video/mp4", "audio/mp4", "video/webm", "audio/webm":
		return true
	default:
		return false
	}
}

func rewriteContainerMetadata(ctx context.Context, j *jobState, file mediaFile, videoID string, maxBytes int64) (int64, error) {
	if !supportsContainerMetadata(file.MimeType) {
		return file.Size, nil
	}
	metadata := containerMetadataFor(j, file, videoID)
	if !hasContainerTags(metadata) {
		return file.Size, nil
	}
	switch file.MimeType {
	case "video/mp4", "audio/mp4":
		return rewriteISOContainerFile(ctx, filepath.Join(j.dir, file.Name), metadata, maxBytes)
	case "video/webm", "audio/webm":
		return rewriteWebMContainerFile(ctx, filepath.Join(j.dir, file.Name), metadata, maxBytes)
	default:
		return file.Size, nil
	}
}

func replaceManagedFile(path string, maxBytes int64, rewrite func(*os.File, *os.File) error) (int64, error) {
	source, err := os.Open(path)
	if err != nil {
		return 0, errStorage
	}
	defer source.Close()
	info, err := source.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return 0, errStorage
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".yt-dl-go-metadata-*.part")
	if err != nil {
		return 0, errStorage
	}
	temporaryPath := temporary.Name()
	keepTemporary := false
	defer func() {
		if !keepTemporary {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := rewrite(source, temporary); err != nil {
		_ = temporary.Close()
		if errors.Is(err, errLimit) || errors.Is(err, errMux) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return 0, err
		}
		return 0, errStorage
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return 0, errStorage
	}
	outputInfo, err := temporary.Stat()
	if err != nil || outputInfo.Size() <= 0 {
		_ = temporary.Close()
		return 0, errStorage
	}
	if maxBytes > 0 && outputInfo.Size() > maxBytes {
		_ = temporary.Close()
		return 0, errLimit
	}
	if err := temporary.Chmod(info.Mode().Perm()); err != nil {
		_ = temporary.Close()
		return 0, errStorage
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return 0, errStorage
	}
	if err := temporary.Close(); err != nil {
		return 0, errStorage
	}
	if err := source.Close(); err != nil {
		return 0, errStorage
	}
	if err := durableReplace(temporaryPath, path); err != nil {
		return 0, errStorage
	}
	keepTemporary = true
	return outputInfo.Size(), nil
}

type isoBoxHeader struct {
	kind        string
	headerBytes int
	size        uint64
}

func decodeISOBoxHeader(raw []byte) (isoBoxHeader, error) {
	if len(raw) < 8 {
		return isoBoxHeader{}, errMux
	}
	size32 := binary.BigEndian.Uint32(raw[:4])
	header := isoBoxHeader{kind: string(raw[4:8]), headerBytes: 8, size: uint64(size32)}
	switch size32 {
	case 1:
		if len(raw) < 16 {
			return isoBoxHeader{}, errMux
		}
		header.size = binary.BigEndian.Uint64(raw[8:16])
		header.headerBytes = 16
	case 0:
		return isoBoxHeader{}, errMux
	}
	if header.size < uint64(header.headerBytes) || header.size > uint64(len(raw)) {
		return isoBoxHeader{}, errMux
	}
	return header, nil
}

func readISOBoxHeader(source io.ReaderAt, offset, limit int64) (isoBoxHeader, error) {
	var header [16]byte
	if offset < 0 || offset+8 > limit {
		return isoBoxHeader{}, errMux
	}
	if _, err := source.ReadAt(header[:8], offset); err != nil {
		return isoBoxHeader{}, err
	}
	size32 := binary.BigEndian.Uint32(header[:4])
	headerBytes := 8
	size := uint64(size32)
	switch size32 {
	case 1:
		if offset+16 > limit {
			return isoBoxHeader{}, errMux
		}
		if _, err := source.ReadAt(header[8:16], offset+8); err != nil {
			return isoBoxHeader{}, err
		}
		headerBytes = 16
		size = binary.BigEndian.Uint64(header[8:16])
	case 0:
		size = uint64(limit - offset)
	}
	if size < uint64(headerBytes) || size > uint64(limit-offset) {
		return isoBoxHeader{}, errMux
	}
	return isoBoxHeader{kind: string(header[4:8]), headerBytes: headerBytes, size: size}, nil
}

func makeISOBox(kind string, payload []byte, forceExtended bool) ([]byte, error) {
	if len(kind) != 4 {
		return nil, errMux
	}
	useExtended := forceExtended || uint64(len(payload))+8 > math.MaxUint32
	headerBytes := 8
	if useExtended {
		headerBytes = 16
	}
	result := make([]byte, headerBytes+len(payload))
	if useExtended {
		binary.BigEndian.PutUint32(result[:4], 1)
		copy(result[4:8], kind)
		binary.BigEndian.PutUint64(result[8:16], uint64(len(result)))
	} else {
		binary.BigEndian.PutUint32(result[:4], uint32(len(result)))
		copy(result[4:8], kind)
	}
	copy(result[headerBytes:], payload)
	return result, nil
}

func makeMP4DataBox(kind uint32, value []byte) ([]byte, error) {
	payload := make([]byte, 8+len(value))
	binary.BigEndian.PutUint32(payload[:4], kind)
	copy(payload[8:], value)
	return makeISOBox("data", payload, false)
}

func makeMP4Item(kind string, value []byte, dataKind uint32) ([]byte, error) {
	data, err := makeMP4DataBox(dataKind, value)
	if err != nil {
		return nil, err
	}
	return makeISOBox(kind, data, false)
}

func makeMP4MetadataBox(metadata containerTagMetadata) ([]byte, error) {
	items := make([][]byte, 0, 6)
	var chapterBox []byte
	textItems := []struct{ atom, value string }{
		{string([]byte{0xA9, 'n', 'a', 'm'}), strings.TrimSpace(metadata.Title)},
		{string([]byte{0xA9, 'A', 'R', 'T'}), strings.TrimSpace(metadata.Artist)},
		{string([]byte{0xA9, 'a', 'l', 'b'}), strings.TrimSpace(metadata.Album)},
		{string([]byte{0xA9, 'd', 'a', 'y'}), strings.TrimSpace(metadata.PublishDate)},
		{string([]byte{0xA9, 'c', 'm', 't'}), strings.TrimSpace(metadata.SourceURL)},
	}
	for _, item := range textItems {
		if item.value == "" {
			continue
		}
		atom, err := makeMP4Item(item.atom, []byte(item.value), 1)
		if err != nil {
			return nil, err
		}
		items = append(items, atom)
	}
	if int64(len(metadata.Artwork)) <= maxEmbeddedArtworkBytes && len(metadata.Artwork) > 0 {
		artworkKind := uint32(0)
		switch metadata.ArtworkMIME {
		case "image/jpeg":
			artworkKind = 13
		case "image/png":
			artworkKind = 14
		}
		if artworkKind != 0 {
			artwork, err := makeMP4Item("covr", metadata.Artwork, artworkKind)
			if err != nil {
				return nil, err
			}
			items = append(items, artwork)
		}
	}
	if len(metadata.Chapters) > 0 {
		var err error
		chapterBox, err = makeMP4ChapterList(metadata.Chapters)
		if err != nil {
			return nil, err
		}
	}
	if len(items) == 0 && len(chapterBox) == 0 {
		return nil, nil
	}
	var udtaPayload []byte
	if len(items) > 0 {
		var ilstPayload bytes.Buffer
		for _, item := range items {
			_, _ = ilstPayload.Write(item)
		}
		ilst, err := makeISOBox("ilst", ilstPayload.Bytes(), false)
		if err != nil {
			return nil, err
		}
		handlerPayload := make([]byte, 24)
		copy(handlerPayload[8:12], "mdir")
		handler, err := makeISOBox("hdlr", handlerPayload, false)
		if err != nil {
			return nil, err
		}
		metaPayload := make([]byte, 4, 4+len(handler)+len(ilst)) // FullBox version/flags.
		metaPayload = append(metaPayload, handler...)
		metaPayload = append(metaPayload, ilst...)
		meta, err := makeISOBox("meta", metaPayload, false)
		if err != nil {
			return nil, err
		}
		udtaPayload = append(udtaPayload, meta...)
	}
	udtaPayload = append(udtaPayload, chapterBox...)
	return makeISOBox("udta", udtaPayload, false)
}

func makeMP4ChapterList(chapters []mediaChapter) ([]byte, error) {
	if len(chapters) > 255 {
		chapters = chapters[:255]
	}
	if len(chapters) == 0 {
		return nil, nil
	}
	payload := make([]byte, 9)
	payload[8] = byte(len(chapters))
	for _, chapter := range chapters {
		if chapter.StartMs < 0 || chapter.StartMs > math.MaxUint64/10000 || chapter.Title == "" {
			return nil, errMux
		}
		title := truncateUTF8(chapter.Title, 255)
		entry := make([]byte, 9+len(title))
		binary.BigEndian.PutUint64(entry[:8], uint64(chapter.StartMs)*10000)
		entry[8] = byte(len(title))
		copy(entry[9:], title)
		payload = append(payload, entry...)
	}
	return makeISOBox("chpl", payload, false)
}

func rewriteISOContainerFile(ctx context.Context, path string, metadata containerTagMetadata, maxBytes int64) (int64, error) {
	metadataBox, err := makeMP4MetadataBox(metadata)
	if err != nil {
		return 0, errMux
	}
	if len(metadataBox) == 0 {
		info, err := os.Stat(path)
		if err != nil {
			return 0, errStorage
		}
		return info.Size(), nil
	}
	return replaceManagedFile(path, maxBytes, func(source *os.File, destination *os.File) error {
		return rewriteISOContainer(ctx, source, destination, metadataBox)
	})
}

func rewriteISOContainer(ctx context.Context, source *os.File, destination *os.File, metadataBox []byte) error {
	info, err := source.Stat()
	if err != nil {
		return err
	}
	fileSize := info.Size()
	var boxes []struct {
		start int64
		size  int64
		kind  string
	}
	var moovOffset, moovSize int64 = -1, 0
	for offset := int64(0); offset < fileSize; {
		if err := ctx.Err(); err != nil {
			return err
		}
		header, err := readISOBoxHeader(source, offset, fileSize)
		if err != nil {
			return errMux
		}
		boxSize := int64(header.size)
		if header.kind == "sidx" || header.kind == "moof" || header.kind == "mfra" {
			return errMux
		}
		boxes = append(boxes, struct {
			start int64
			size  int64
			kind  string
		}{start: offset, size: boxSize, kind: header.kind})
		if header.kind == "moov" {
			if moovOffset >= 0 || boxSize > 64<<20 {
				return errMux
			}
			moovOffset, moovSize = offset, boxSize
		}
		offset += boxSize
	}
	if moovOffset < 0 {
		return errMux
	}
	moovBytes := make([]byte, moovSize)
	if _, err := source.ReadAt(moovBytes, moovOffset); err != nil {
		return err
	}
	oldHeader, err := decodeISOBoxHeader(moovBytes)
	if err != nil {
		return err
	}
	rewrittenMoov, err := rewriteISOBox(moovBytes, uint64(moovOffset), uint64(moovOffset+moovSize))
	if err != nil {
		return err
	}
	rootHeader, err := decodeISOBoxHeader(rewrittenMoov)
	if err != nil || rootHeader.kind != "moov" {
		return errMux
	}
	moovPayload := append([]byte(nil), rewrittenMoov[rootHeader.headerBytes:]...)
	moovPayload = append(moovPayload, metadataBox...)
	rewrittenMoov, err = makeISOBox("moov", moovPayload, oldHeader.headerBytes == 16)
	if err != nil {
		return errMux
	}
	for _, box := range boxes {
		if box.kind == "moov" {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, err := io.CopyN(destination, io.NewSectionReader(source, box.start, box.size), box.size); err != nil {
			return err
		}
	}
	_, err = destination.Write(rewrittenMoov)
	return err
}

var isoContainerAtoms = map[string]int{
	"moov": 0, "trak": 0, "mdia": 0, "minf": 0, "stbl": 0, "edts": 0, "dinf": 0,
	"udta": 0, "meta": 4, "ilst": 0, "ipro": 0, "sinf": 0, "schi": 0,
}

func rewriteISOBox(raw []byte, oldMoovStart, oldMoovEnd uint64) ([]byte, error) {
	header, err := decodeISOBoxHeader(raw)
	if err != nil {
		return nil, fmt.Errorf("decode ISO box: %w", err)
	}
	boxBytes := raw[:header.size]
	payload := boxBytes[header.headerBytes:]
	switch header.kind {
	case "stco", "co64":
		return rewriteISOChunkOffsets(boxBytes, header, oldMoovStart, oldMoovEnd)
	}
	prefixBytes, isContainer := isoContainerAtoms[header.kind]
	if !isContainer || prefixBytes > len(payload) {
		return append([]byte(nil), boxBytes...), nil
	}
	children, err := rewriteISOBoxChildren(payload[prefixBytes:], oldMoovStart, oldMoovEnd)
	if err != nil {
		return nil, fmt.Errorf("rewrite %s children: %w", header.kind, err)
	}
	newPayload := make([]byte, 0, prefixBytes+len(children))
	newPayload = append(newPayload, payload[:prefixBytes]...)
	newPayload = append(newPayload, children...)
	return makeISOBox(header.kind, newPayload, header.headerBytes == 16)
}

func rewriteISOBoxChildren(payload []byte, oldMoovStart, oldMoovEnd uint64) ([]byte, error) {
	var result bytes.Buffer
	for offset := 0; offset < len(payload); {
		header, err := decodeISOBoxHeader(payload[offset:])
		if err != nil {
			return nil, fmt.Errorf("decode child at byte %d: %w", offset, err)
		}
		rewritten, err := rewriteISOBox(payload[offset:offset+int(header.size)], oldMoovStart, oldMoovEnd)
		if err != nil {
			return nil, fmt.Errorf("rewrite child %s at byte %d: %w", header.kind, offset, err)
		}
		if _, err := result.Write(rewritten); err != nil {
			return nil, err
		}
		offset += int(header.size)
	}
	return result.Bytes(), nil
}

func rewriteISOChunkOffsets(raw []byte, header isoBoxHeader, oldMoovStart, oldMoovEnd uint64) ([]byte, error) {
	payload := raw[header.headerBytes:int(header.size)]
	if len(payload) < 8 {
		return nil, errMux
	}
	count := uint64(binary.BigEndian.Uint32(payload[4:8]))
	entryBytes := uint64(4)
	if header.kind == "co64" {
		entryBytes = 8
	}
	if count > uint64(len(payload)-8)/entryBytes || 8+count*entryBytes != uint64(len(payload)) {
		return nil, errMux
	}
	updated := append([]byte(nil), payload...)
	for index := uint64(0); index < count; index++ {
		position := 8 + index*entryBytes
		var value uint64
		if entryBytes == 4 {
			value = uint64(binary.BigEndian.Uint32(updated[position : position+4]))
		} else {
			value = binary.BigEndian.Uint64(updated[position : position+8])
		}
		if value >= oldMoovStart && value < oldMoovEnd {
			return nil, errMux
		}
		if value >= oldMoovEnd {
			value -= oldMoovEnd - oldMoovStart
		}
		if entryBytes == 4 {
			if value > math.MaxUint32 {
				return nil, errMux
			}
			binary.BigEndian.PutUint32(updated[position:position+4], uint32(value))
		} else {
			binary.BigEndian.PutUint64(updated[position:position+8], value)
		}
	}
	return makeISOBox(header.kind, updated, header.headerBytes == 16)
}

type webMSimpleTag struct {
	Name     string `ebml:"TagName"`
	Language string `ebml:"TagLanguage,omitempty"`
	Default  uint64 `ebml:"TagDefault,omitempty"`
	Text     string `ebml:"TagString,omitempty"`
}

type webMTag struct {
	SimpleTag []webMSimpleTag `ebml:"SimpleTag"`
}

type webMTags struct {
	Tag []webMTag `ebml:"Tag"`
}

type webMAttachedFile struct {
	Description string `ebml:"FileDescription,omitempty"`
	Name        string `ebml:"FileName"`
	MIME        string `ebml:"FileMimeType"`
	Data        []byte `ebml:"FileData"`
	UID         uint64 `ebml:"FileUID"`
}

type webMAttachments struct {
	AttachedFile []webMAttachedFile `ebml:"AttachedFile"`
}

type webMChapterDisplay struct {
	String   string `ebml:"ChapString"`
	Language string `ebml:"ChapLanguage,omitempty"`
}

type webMChapterAtom struct {
	UID     uint64             `ebml:"ChapterUID"`
	Start   uint64             `ebml:"ChapterTimeStart"`
	End     uint64             `ebml:"ChapterTimeEnd"`
	Display webMChapterDisplay `ebml:"ChapterDisplay"`
}

type webMEditionEntry struct {
	ChapterAtom []webMChapterAtom `ebml:"ChapterAtom"`
}

type webMChapters struct {
	EditionEntry []webMEditionEntry `ebml:"EditionEntry"`
}

type webMMetadataEnvelope struct {
	Tags        *webMTags        `ebml:"Tags,omitempty"`
	Attachments *webMAttachments `ebml:"Attachments,omitempty"`
	Chapters    *webMChapters    `ebml:"Chapters,omitempty"`
}

func makeWebMMetadataBox(metadata containerTagMetadata) ([]byte, error) {
	var simpleTags []webMSimpleTag
	for _, field := range []struct{ name, value string }{
		{"TITLE", strings.TrimSpace(metadata.Title)},
		{"ARTIST", strings.TrimSpace(metadata.Artist)},
		{"ALBUM", strings.TrimSpace(metadata.Album)},
		{"DATE_RELEASED", strings.TrimSpace(metadata.PublishDate)},
		{"COMMENT", strings.TrimSpace(metadata.SourceURL)},
	} {
		if field.value != "" {
			simpleTags = append(simpleTags, webMSimpleTag{Name: field.name, Language: "und", Default: 1, Text: field.value})
		}
	}
	envelope := webMMetadataEnvelope{}
	if len(simpleTags) > 0 {
		envelope.Tags = &webMTags{Tag: []webMTag{{SimpleTag: simpleTags}}}
	}
	if len(metadata.Artwork) > 0 && int64(len(metadata.Artwork)) <= maxEmbeddedArtworkBytes && allowedThumbnailMime(metadata.ArtworkMIME) {
		extension := map[string]string{"image/jpeg": ".jpg", "image/png": ".png", "image/webp": ".webp"}[metadata.ArtworkMIME]
		if extension != "" {
			envelope.Attachments = &webMAttachments{AttachedFile: []webMAttachedFile{{
				Description: "Cover art", Name: "cover" + extension, MIME: metadata.ArtworkMIME,
				Data: metadata.Artwork, UID: 1,
			}}}
		}
	}
	chapterAtoms := make([]webMChapterAtom, 0, len(metadata.Chapters))
	for index, chapter := range metadata.Chapters {
		if chapter.StartMs < 0 || chapter.EndMs <= chapter.StartMs || chapter.Title == "" {
			continue
		}
		chapterAtoms = append(chapterAtoms, webMChapterAtom{
			UID: uint64(index + 1), Start: uint64(chapter.StartMs) * 1_000_000, End: uint64(chapter.EndMs) * 1_000_000,
			Display: webMChapterDisplay{String: chapter.Title, Language: "eng"},
		})
	}
	if len(chapterAtoms) > 0 {
		envelope.Chapters = &webMChapters{EditionEntry: []webMEditionEntry{{ChapterAtom: chapterAtoms}}}
	}
	if envelope.Tags == nil && envelope.Attachments == nil && envelope.Chapters == nil {
		return nil, nil
	}
	var encoded bytes.Buffer
	if err := ebml.Marshal(&envelope, &encoded); err != nil {
		return nil, err
	}
	return encoded.Bytes(), nil
}

type ebmlElementHeader struct {
	id        []byte
	headerLen int
	dataStart int64
	end       int64
	unknown   bool
}

func readEBMLVINT(r io.ReaderAt, offset, limit int64, identifier bool) ([]byte, uint64, int, bool, error) {
	if offset < 0 || offset >= limit {
		return nil, 0, 0, false, errMux
	}
	var first [1]byte
	if _, err := r.ReadAt(first[:], offset); err != nil {
		return nil, 0, 0, false, err
	}
	mask := byte(0x80)
	length := 1
	for first[0]&mask == 0 && mask > 1 {
		mask >>= 1
		length++
	}
	maximum := 8
	if identifier {
		maximum = 4
	}
	if first[0]&mask == 0 || length > maximum || offset+int64(length) > limit {
		return nil, 0, 0, false, errMux
	}
	encoded := make([]byte, length)
	if _, err := r.ReadAt(encoded, offset); err != nil {
		return nil, 0, 0, false, err
	}
	value := uint64(encoded[0] & (mask - 1))
	for _, b := range encoded[1:] {
		value = value<<8 | uint64(b)
	}
	unknown := false
	if !identifier {
		bits := uint(length * 7)
		maximumValue := uint64(1<<bits) - 1
		unknown = value == maximumValue
	}
	return encoded, value, length, unknown, nil
}

func readEBMLElementHeader(source io.ReaderAt, offset, limit int64) (ebmlElementHeader, error) {
	id, _, idLen, _, err := readEBMLVINT(source, offset, limit, true)
	if err != nil {
		return ebmlElementHeader{}, err
	}
	_, size, sizeLen, unknown, err := readEBMLVINT(source, offset+int64(idLen), limit, false)
	if err != nil {
		return ebmlElementHeader{}, err
	}
	dataStart := offset + int64(idLen+sizeLen)
	end := limit
	if !unknown {
		if size > uint64(limit-dataStart) {
			return ebmlElementHeader{}, errMux
		}
		end = dataStart + int64(size)
	}
	return ebmlElementHeader{id: id, headerLen: idLen + sizeLen, dataStart: dataStart, end: end, unknown: unknown}, nil
}

func readWebMLayout(source *os.File, fileSize int64) (segmentDataStart, cuesStart, cuesEnd, clusterStart int64, err error) {
	offset := int64(0)
	first, err := readEBMLElementHeader(source, offset, fileSize)
	if err != nil || !bytes.Equal(first.id, ebml.ElementEBML.Bytes()) || first.unknown {
		return 0, 0, 0, 0, errMux
	}
	offset = first.end
	segment, err := readEBMLElementHeader(source, offset, fileSize)
	if err != nil || !bytes.Equal(segment.id, ebml.ElementSegment.Bytes()) {
		return 0, 0, 0, 0, errMux
	}
	segmentDataStart = segment.dataStart
	for offset = segment.dataStart; offset < fileSize; {
		element, elementErr := readEBMLElementHeader(source, offset, fileSize)
		if elementErr != nil {
			return 0, 0, 0, 0, errMux
		}
		if bytes.Equal(element.id, ebml.ElementCluster.Bytes()) {
			clusterStart = offset
			break
		}
		if bytes.Equal(element.id, ebml.ElementCues.Bytes()) {
			cuesStart, cuesEnd = offset, element.end
		}
		if element.unknown {
			return 0, 0, 0, 0, errMux
		}
		offset = element.end
	}
	if clusterStart == 0 || cuesStart == 0 || cuesEnd <= cuesStart || cuesEnd > clusterStart {
		return 0, 0, 0, 0, errMux
	}
	return segmentDataStart, cuesStart, cuesEnd, clusterStart, nil
}

func encodeEBMLSize(value uint64) ([]byte, error) {
	for length := 1; length <= 8; length++ {
		maximum := uint64(1<<(length*7)) - 2
		if value > maximum {
			continue
		}
		encoded := make([]byte, length)
		for index := length - 1; index >= 0; index-- {
			encoded[index] = byte(value)
			value >>= 8
		}
		encoded[0] |= byte(1 << (8 - length))
		return encoded, nil
	}
	return nil, errMux
}

func makeEBMLVoid(totalSize int64) ([]byte, error) {
	if totalSize == 0 {
		return []byte{}, nil
	}
	for vintLength := int64(1); vintLength <= 8; vintLength++ {
		payloadSize := totalSize - 1 - vintLength
		if payloadSize < 0 {
			continue
		}
		encodedSize, err := encodeEBMLSize(uint64(payloadSize))
		if err != nil || int64(len(encodedSize)) != vintLength {
			continue
		}
		result := make([]byte, totalSize)
		result[0] = 0xEC
		copy(result[1:], encodedSize)
		return result, nil
	}
	return nil, errMux
}

func marshalWebMCues(cues webm.Cues) ([]byte, error) {
	var encoded bytes.Buffer
	envelope := struct {
		Cues webm.Cues `ebml:"Cues"`
	}{Cues: cues}
	if err := ebml.Marshal(&envelope, &encoded); err != nil {
		return nil, err
	}
	return encoded.Bytes(), nil
}

func rewriteWebMContainerFile(ctx context.Context, path string, metadata containerTagMetadata, maxBytes int64) (int64, error) {
	metadataBytes, err := makeWebMMetadataBox(metadata)
	if err != nil {
		return 0, errMux
	}
	if len(metadataBytes) == 0 {
		info, err := os.Stat(path)
		if err != nil {
			return 0, errStorage
		}
		return info.Size(), nil
	}
	return replaceManagedFile(path, maxBytes, func(source *os.File, destination *os.File) error {
		return rewriteWebMContainer(ctx, source, destination, metadataBytes)
	})
}

func rewriteWebMContainer(ctx context.Context, source *os.File, destination *os.File, metadataBytes []byte) error {
	info, err := source.Stat()
	if err != nil {
		return err
	}
	fileSize := info.Size()
	segmentStart, cuesStart, cuesEnd, clusterStart, err := readWebMLayout(source, fileSize)
	if err != nil {
		return errMux
	}
	if cuesEnd-cuesStart > 16<<20 {
		return errMux
	}
	cuesBytes := make([]byte, cuesEnd-cuesStart)
	if _, err := source.ReadAt(cuesBytes, cuesStart); err != nil {
		return err
	}
	var cueEnvelope struct {
		Cues webm.Cues `ebml:"Cues"`
	}
	if err := ebml.Unmarshal(bytes.NewReader(cuesBytes), &cueEnvelope); err != nil {
		return errMux
	}
	clusterRelative := uint64(clusterStart - segmentStart)
	shift := uint64(len(metadataBytes))
	for index := range cueEnvelope.Cues.CuePoint {
		for positionIndex := range cueEnvelope.Cues.CuePoint[index].CueTrackPositions {
			position := &cueEnvelope.Cues.CuePoint[index].CueTrackPositions[positionIndex]
			if position.CueClusterPosition >= clusterRelative {
				if math.MaxUint64-position.CueClusterPosition < shift {
					return errMux
				}
				position.CueClusterPosition += shift
			}
		}
	}
	regionSize := clusterStart - cuesStart
	var encodedCues, voidBytes []byte
	for len(cueEnvelope.Cues.CuePoint) > 0 {
		encodedCues, err = marshalWebMCues(cueEnvelope.Cues)
		if err == nil && int64(len(encodedCues)) <= regionSize {
			voidBytes, err = makeEBMLVoid(regionSize - int64(len(encodedCues)))
			if err == nil {
				break
			}
		}
		if len(cueEnvelope.Cues.CuePoint) == 1 {
			return errMux
		}
		downsampled := make([]webm.CuePoint, 0, (len(cueEnvelope.Cues.CuePoint)+1)/2)
		for index := 0; index < len(cueEnvelope.Cues.CuePoint); index += 2 {
			downsampled = append(downsampled, cueEnvelope.Cues.CuePoint[index])
		}
		cueEnvelope.Cues.CuePoint = downsampled
	}
	if err != nil {
		return errMux
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := io.CopyN(destination, io.NewSectionReader(source, 0, cuesStart), cuesStart); err != nil {
		return err
	}
	if _, err := destination.Write(encodedCues); err != nil {
		return err
	}
	if _, err := destination.Write(voidBytes); err != nil {
		return err
	}
	if _, err := destination.Write(metadataBytes); err != nil {
		return err
	}
	return copySection(ctx, destination, source, clusterStart, fileSize-clusterStart)
}

func copySection(ctx context.Context, destination io.Writer, source io.ReaderAt, offset, size int64) error {
	reader := io.NewSectionReader(source, offset, size)
	for size > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		chunk := int64(256 * 1024)
		if chunk > size {
			chunk = size
		}
		written, err := io.CopyN(destination, reader, chunk)
		size -= written
		if err != nil {
			return err
		}
	}
	return nil
}
