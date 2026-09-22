// sabr.go decodes selected YouTube UMP/SABR media fragments, orders them by
// sequence, enforces byte and segment limits, and finalizes only complete tracks.
package main

import (
	"crypto/sha256"
	"errors"
	"io"
	"log"
	"math"
	"os"
	"sync"
)

const (
	umpPartMediaHeader                  = 20
	umpPartMedia                        = 21
	umpPartMediaEnd                     = 22
	umpPartFormatInitializationMetadata = 42
	umpPartStreamProtectionStatus       = 58
	// Individual UMP parts are bounded even when a SABR response itself is
	// multi-gigabyte. Responses are parsed as streams so their total size does
	// not need an artificial in-memory ceiling.
	maxSABRPartBytes = 32 * 1024 * 1024
	maxSABRParts     = 10000
)

var errSABR = errors.New("browser SABR media capture failed")

type sabrCapture struct {
	mu                 sync.Mutex
	file               *os.File
	itag               int
	expectedDurationMs int64
	budget             int64
	progress           func(int64)
	done               chan error
	doneOnce           sync.Once
	handlers           sync.WaitGroup

	formatVerified bool
	formatDigest   [sha256.Size]byte
	acceptSelected bool
	initWritten    bool
	initDigest     [sha256.Size]byte
	active         map[uint64][]*sabrSegment
	ready          map[uint64]*sabrCompletedSegment
	written        map[uint64][sha256.Size]byte
	nextSequence   uint64
	sequenceSet    bool
	endSegment     uint64
	cumulativeMs   int64
	totalWritten   int64
	complete       bool
	trace          bool
}

// sabrResponseCapture is the lifecycle shared by one-track and multiplexed
// SABR captures. A browser response is consumed once and then routed to the
// selected tracks by the implementation.
type sabrResponseCapture interface {
	consumeReader(io.Reader) error
	fail(error)
	addHandler()
	doneHandler()
	waitHandlers()
	isTrace() bool
}

// sabrCaptureSet routes one parsed UMP response to independent selected-track
// state machines. Each capture retains its own header IDs and format digest:
// header IDs are scoped to a UMP response and must not be shared across tracks.
type sabrCaptureSet struct {
	mu       sync.Mutex
	captures []*sabrCapture
	done     chan error
	doneOnce sync.Once
	handlers sync.WaitGroup
	trace    bool
}

func newSABRCaptureSet(captures ...*sabrCapture) *sabrCaptureSet {
	return &sabrCaptureSet{
		captures: captures,
		done:     make(chan error, 1),
		trace:    os.Getenv("YTDL_TRACE_SABR") != "",
	}
}

func (set *sabrCaptureSet) fail(err error) {
	if err == nil {
		err = errSABR
	}
	for _, capture := range set.captures {
		capture.fail(err)
	}
	set.doneOnce.Do(func() { set.done <- err })
}

func (set *sabrCaptureSet) succeed() {
	set.doneOnce.Do(func() { set.done <- nil })
}

func (set *sabrCaptureSet) addHandler()  { set.handlers.Add(1) }
func (set *sabrCaptureSet) doneHandler() { set.handlers.Done() }
func (set *sabrCaptureSet) waitHandlers() {
	set.handlers.Wait()
}
func (set *sabrCaptureSet) isTrace() bool { return set.trace }

func (set *sabrCaptureSet) consume(body []byte) error {
	if len(body) == 0 {
		return errSABR
	}
	return set.consumeParsed(func(consume func(uint64, []byte) error) error {
		return parseUMP(body, consume)
	})
}

func (set *sabrCaptureSet) consumeReader(reader io.Reader) error {
	if reader == nil {
		return errSABR
	}
	return set.consumeParsed(func(consume func(uint64, []byte) error) error {
		return parseUMPReader(reader, consume)
	})
}

func (set *sabrCaptureSet) consumeParsed(parse func(func(uint64, []byte) error) error) error {
	set.mu.Lock()
	defer set.mu.Unlock()
	for _, capture := range set.captures {
		capture.mu.Lock()
		if !capture.complete {
			capture.acceptSelected = capture.formatVerified
		}
		capture.mu.Unlock()
	}
	if err := parse(func(partType uint64, payload []byte) error {
		for _, capture := range set.captures {
			capture.mu.Lock()
			if capture.complete {
				capture.mu.Unlock()
				continue
			}
			err := capture.consumePart(partType, payload)
			capture.mu.Unlock()
			if err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}
	for _, capture := range set.captures {
		capture.mu.Lock()
		complete := capture.complete
		capture.mu.Unlock()
		if !complete {
			return nil
		}
	}
	set.succeed()
	return nil
}

func (capture *sabrCapture) addHandler()   { capture.handlers.Add(1) }
func (capture *sabrCapture) doneHandler()  { capture.handlers.Done() }
func (capture *sabrCapture) waitHandlers() { capture.handlers.Wait() }
func (capture *sabrCapture) isTrace() bool { return capture.trace }

type sabrSegment struct {
	header   sabrMediaHeader
	selected bool
	data     []byte
}

type sabrCompletedSegment struct {
	data       []byte
	durationMs int64
}

type sabrMediaHeader struct {
	headerID      uint64
	itag          int
	isInit        bool
	sequence      uint64
	durationMs    int64
	contentLength int64
	timeDuration  int64
	timeScale     int64
	formatDigest  [sha256.Size]byte
}

// newSABRCapture initializes bounded capture state for one selected video itag.
func newSABRCapture(file *os.File, itag int, expectedDurationMs, budget int64, progress func(int64)) *sabrCapture {
	if progress == nil {
		progress = func(int64) {}
	}
	return &sabrCapture{
		file: file, itag: itag, expectedDurationMs: expectedDurationMs, budget: budget, progress: progress,
		done: make(chan error, 1), active: map[uint64][]*sabrSegment{}, ready: map[uint64]*sabrCompletedSegment{}, written: map[uint64][sha256.Size]byte{},
		trace: os.Getenv("YTDL_TRACE_SABR") != "",
	}
}

func (capture *sabrCapture) fail(err error) {
	if err == nil {
		err = errSABR
	}
	capture.doneOnce.Do(func() { capture.done <- err })
}

func (capture *sabrCapture) succeed() {
	capture.doneOnce.Do(func() { capture.done <- nil })
}

func (capture *sabrCapture) consume(body []byte) error {
	if len(body) == 0 {
		return errSABR
	}
	return capture.consumeParsed(func(consume func(uint64, []byte) error) error {
		return parseUMP(body, consume)
	})
}

func (capture *sabrCapture) consumeReader(reader io.Reader) error {
	if reader == nil {
		return errSABR
	}
	return capture.consumeParsed(func(consume func(uint64, []byte) error) error {
		return parseUMPReader(reader, consume)
	})
}

func (capture *sabrCapture) consumeParsed(parse func(func(uint64, []byte) error) error) error {
	capture.mu.Lock()
	defer capture.mu.Unlock()
	if capture.complete {
		return nil
	}
	capture.acceptSelected = capture.formatVerified
	return parse(capture.consumePart)
}

func (capture *sabrCapture) consumePart(partType uint64, payload []byte) error {
	switch partType {
	case umpPartFormatInitializationMetadata:
		metadata, err := decodeSABRFormatInitialization(payload)
		if err != nil {
			return err
		}
		selected := false
		if metadata.itag == capture.itag {
			if !capture.formatVerified {
				capture.formatVerified = true
				capture.formatDigest = metadata.formatDigest
				capture.endSegment = metadata.endSegment
				selected = true
			} else {
				selected = metadata.formatDigest == capture.formatDigest &&
					(capture.endSegment == 0 || metadata.endSegment == 0 || metadata.endSegment == capture.endSegment)
				if selected && capture.endSegment == 0 {
					capture.endSegment = metadata.endSegment
				}
			}
			capture.acceptSelected = selected
		}
		if capture.trace {
			log.Printf("SABR format init itag=%d selected=%t end_segment=%d", metadata.itag, selected, metadata.endSegment)
		}
	case umpPartMediaHeader:
		header, err := decodeSABRMediaHeader(payload)
		if err != nil || header.headerID > math.MaxUint32 || header.itag <= 0 || header.contentLength < 0 {
			return errSABR
		}
		selected := header.itag == capture.itag && capture.acceptSelected && header.formatDigest == capture.formatDigest
		segments := capture.active[header.headerID]
		if len(segments) >= 8 || (selected && len(segments) > 0 && segments[len(segments)-1].selected) {
			return errSABR
		}
		capture.active[header.headerID] = append(segments, &sabrSegment{header: header, selected: selected})
		if capture.trace {
			log.Printf("SABR media header id=%d itag=%d init=%t sequence=%d length=%d", header.headerID, header.itag, header.isInit, header.sequence, header.contentLength)
		}
	case umpPartMedia:
		headerID, offset, err := readUMPVarint(payload)
		if err != nil {
			return err
		}
		segments := capture.active[headerID]
		if len(segments) == 0 {
			return errSABR
		}
		segment := segments[0]
		if segment.selected {
			if int64(len(segment.data))+int64(len(payload)-offset) > capture.budget {
				return errLimit
			}
			segment.data = append(segment.data, payload[offset:]...)
		}
	case umpPartMediaEnd:
		headerID, offset, err := readUMPVarint(payload)
		if err != nil || offset != len(payload) {
			return errSABR
		}
		segments := capture.active[headerID]
		if len(segments) == 0 {
			return errSABR
		}
		segment := segments[0]
		if len(segments) == 1 {
			delete(capture.active, headerID)
		} else {
			capture.active[headerID] = segments[1:]
		}
		if !segment.selected {
			return nil
		}
		if segment.header.contentLength > 0 && int64(len(segment.data)) != segment.header.contentLength {
			return errSABR
		}
		return capture.commitSegment(segment)
	case umpPartStreamProtectionStatus:
		status, err := sabrProtoVarintField(payload, 1)
		if err != nil {
			return err
		}
		if capture.trace {
			log.Printf("SABR stream protection status=%d", status)
		}
		if status >= 3 {
			return errSABR
		}
	}
	return nil
}

func (capture *sabrCapture) commitSegment(segment *sabrSegment) error {
	if !capture.formatVerified {
		return errSABR
	}
	digest := sha256.Sum256(segment.data)
	if segment.header.isInit {
		if capture.initWritten {
			if digest != capture.initDigest {
				return errSABR
			}
			return nil
		}
		if err := capture.write(segment.data); err != nil {
			return err
		}
		capture.initDigest = digest
		capture.initWritten = true
		return capture.flushReady()
	}
	if prior, ok := capture.written[segment.header.sequence]; ok {
		if prior != digest {
			return errSABR
		}
		return nil
	}
	if prior, ok := capture.ready[segment.header.sequence]; ok {
		if sha256.Sum256(prior.data) != digest {
			return errSABR
		}
		return nil
	}
	duration := segment.header.durationMs
	if duration <= 0 && segment.header.timeDuration > 0 && segment.header.timeScale > 0 {
		duration = (segment.header.timeDuration*1000 + segment.header.timeScale - 1) / segment.header.timeScale
	}
	if duration <= 0 {
		return errSABR
	}
	if !capture.sequenceSet {
		if segment.header.sequence > 1 {
			return errSABR
		}
		capture.nextSequence = segment.header.sequence
		capture.sequenceSet = true
	}
	capture.ready[segment.header.sequence] = &sabrCompletedSegment{data: segment.data, durationMs: duration}
	return capture.flushReady()
}

func (capture *sabrCapture) flushReady() error {
	if !capture.initWritten {
		return nil
	}
	for {
		segment := capture.ready[capture.nextSequence]
		if segment == nil {
			return nil
		}
		if err := capture.write(segment.data); err != nil {
			return err
		}
		capture.written[capture.nextSequence] = sha256.Sum256(segment.data)
		delete(capture.ready, capture.nextSequence)
		capture.nextSequence++
		capture.cumulativeMs += segment.durationMs
		if capture.endSegment > 0 && capture.nextSequence > capture.endSegment &&
			capture.expectedDurationMs > 0 && capture.cumulativeMs+1500 < capture.expectedDurationMs {
			return capture.resetShortCandidate()
		}
		if (capture.endSegment > 0 && capture.nextSequence > capture.endSegment) ||
			(capture.endSegment == 0 && capture.expectedDurationMs > 0 && capture.cumulativeMs+1500 >= capture.expectedDurationMs) {
			capture.complete = true
			capture.succeed()
			return nil
		}
	}
}

func (capture *sabrCapture) resetShortCandidate() error {
	if err := capture.file.Truncate(0); err != nil {
		return errStorage
	}
	if _, err := capture.file.Seek(0, io.SeekStart); err != nil {
		return errStorage
	}
	for _, segments := range capture.active {
		for _, segment := range segments {
			segment.selected = false
		}
	}
	capture.formatVerified = false
	capture.formatDigest = [sha256.Size]byte{}
	capture.acceptSelected = false
	capture.initWritten = false
	capture.initDigest = [sha256.Size]byte{}
	capture.ready = map[uint64]*sabrCompletedSegment{}
	capture.written = map[uint64][sha256.Size]byte{}
	capture.nextSequence = 0
	capture.sequenceSet = false
	capture.endSegment = 0
	capture.cumulativeMs = 0
	capture.totalWritten = 0
	capture.progress(0)
	return nil
}

func (capture *sabrCapture) write(data []byte) error {
	if len(data) == 0 {
		return errSABR
	}
	if int64(len(data)) > capture.budget-capture.totalWritten {
		return errLimit
	}
	written, err := capture.file.Write(data)
	if err != nil || written != len(data) {
		return errStorage
	}
	capture.totalWritten += int64(written)
	capture.progress(capture.totalWritten)
	return nil
}

// parseUMP iterates length-prefixed UMP parts and passes each type and payload
// to consume; malformed or oversized parts return an error.
func parseUMP(body []byte, consume func(uint64, []byte) error) error {
	parts := 0
	for offset := 0; offset < len(body); {
		partType, used, err := readUMPVarint(body[offset:])
		if err != nil {
			return err
		}
		offset += used
		partSize, used, err := readUMPVarint(body[offset:])
		if err != nil || partSize > maxSABRPartBytes || partSize > uint64(len(body)-offset-used) {
			return errSABR
		}
		offset += used
		parts++
		if parts > maxSABRParts {
			return errSABR
		}
		end := offset + int(partSize)
		if err := consume(partType, body[offset:end]); err != nil {
			return err
		}
		offset = end
	}
	return nil
}

// parseUMPReader is the streamed equivalent of parseUMP. It retains only one
// bounded UMP part at a time, allowing Chrome Fetch responses larger than RAM
// or the old whole-response limit to be captured safely.
func parseUMPReader(reader io.Reader, consume func(uint64, []byte) error) error {
	parts := 0
	for {
		partType, err := readUMPVarintReader(reader)
		if errors.Is(err, io.EOF) {
			if parts == 0 {
				return errSABR
			}
			return nil
		}
		if err != nil {
			return err
		}
		partSize, err := readUMPVarintReader(reader)
		if err != nil || partSize > maxSABRPartBytes {
			return errSABR
		}
		parts++
		if parts > maxSABRParts {
			return errSABR
		}
		payload := make([]byte, int(partSize))
		if _, err := io.ReadFull(reader, payload); err != nil {
			return err
		}
		if err := consume(partType, payload); err != nil {
			return err
		}
	}
}

func readUMPVarintReader(reader io.Reader) (uint64, error) {
	var data [5]byte
	if _, err := io.ReadFull(reader, data[:1]); err != nil {
		return 0, err
	}
	size := 1
	for bit := 7; bit >= 1 && data[0]&(1<<uint(bit)) != 0; bit-- {
		size++
	}
	if size > len(data) {
		size = len(data)
	}
	if _, err := io.ReadFull(reader, data[1:size]); err != nil {
		return 0, err
	}
	value, _, err := readUMPVarint(data[:size])
	return value, err
}

func readUMPVarint(data []byte) (uint64, int, error) {
	if len(data) == 0 {
		return 0, 0, io.ErrUnexpectedEOF
	}
	size := 1
	for bit := 7; bit >= 1 && data[0]&(1<<uint(bit)) != 0; bit-- {
		size++
	}
	if size > 5 {
		size = 5
	}
	if len(data) < size {
		return 0, 0, io.ErrUnexpectedEOF
	}
	if size == 5 {
		if data[0]&0x07 != 0 {
			return 0, 0, errSABR
		}
		return uint64(data[1]) | uint64(data[2])<<8 | uint64(data[3])<<16 | uint64(data[4])<<24, size, nil
	}
	mask := byte(1<<(8-uint(size))) - 1
	value := uint64(data[0] & mask)
	shift := uint(8 - size)
	for index := 1; index < size; index++ {
		value |= uint64(data[index]) << shift
		shift += 8
	}
	return value, size, nil
}

type protoReader struct {
	data   []byte
	offset int
}

func (reader *protoReader) next() (uint64, int, bool, error) {
	if reader.offset == len(reader.data) {
		return 0, 0, false, nil
	}
	key, err := reader.varint()
	if err != nil || key>>3 == 0 {
		return 0, 0, false, errSABR
	}
	return key >> 3, int(key & 7), true, nil
}

func (reader *protoReader) varint() (uint64, error) {
	var value uint64
	for shift := uint(0); shift < 64; shift += 7 {
		if reader.offset >= len(reader.data) {
			return 0, io.ErrUnexpectedEOF
		}
		current := reader.data[reader.offset]
		reader.offset++
		value |= uint64(current&0x7f) << shift
		if current < 0x80 {
			return value, nil
		}
	}
	return 0, errSABR
}

func (reader *protoReader) bytes() ([]byte, error) {
	length, err := reader.varint()
	if err != nil || length > uint64(len(reader.data)-reader.offset) || length > maxSABRPartBytes {
		return nil, errSABR
	}
	value := reader.data[reader.offset : reader.offset+int(length)]
	reader.offset += int(length)
	return value, nil
}

func (reader *protoReader) skip(wire int) error {
	switch wire {
	case 0:
		_, err := reader.varint()
		return err
	case 1:
		if reader.offset+8 > len(reader.data) {
			return io.ErrUnexpectedEOF
		}
		reader.offset += 8
		return nil
	case 2:
		_, err := reader.bytes()
		return err
	case 5:
		if reader.offset+4 > len(reader.data) {
			return io.ErrUnexpectedEOF
		}
		reader.offset += 4
		return nil
	default:
		return errSABR
	}
}

type sabrFormatInitialization struct {
	itag         int
	endSegment   uint64
	formatDigest [sha256.Size]byte
}

func decodeSABRFormatInitialization(data []byte) (sabrFormatInitialization, error) {
	var metadata sabrFormatInitialization
	reader := protoReader{data: data}
	for {
		field, wire, ok, err := reader.next()
		if err != nil {
			return metadata, err
		}
		if !ok {
			if metadata.itag == 0 {
				return metadata, errSABR
			}
			return metadata, nil
		}
		if field == 2 && wire == 2 {
			format, err := reader.bytes()
			if err != nil {
				return metadata, err
			}
			metadata.itag, err = sabrFormatItag(format)
			if err != nil {
				return metadata, err
			}
			metadata.formatDigest = sha256.Sum256(format)
			continue
		}
		if field == 4 && wire == 0 {
			metadata.endSegment, err = reader.varint()
			if err != nil {
				return metadata, err
			}
			continue
		}
		if err := reader.skip(wire); err != nil {
			return metadata, err
		}
	}
}

func sabrProtoVarintField(data []byte, wanted uint64) (uint64, error) {
	reader := protoReader{data: data}
	for {
		field, wire, ok, err := reader.next()
		if err != nil || !ok {
			return 0, err
		}
		if field == wanted && wire == 0 {
			return reader.varint()
		}
		if err := reader.skip(wire); err != nil {
			return 0, err
		}
	}
}

func sabrFormatItag(data []byte) (int, error) {
	reader := protoReader{data: data}
	for {
		field, wire, ok, err := reader.next()
		if err != nil || !ok {
			return 0, err
		}
		if field == 1 && wire == 0 {
			value, err := reader.varint()
			if err != nil || value > math.MaxInt32 {
				return 0, errSABR
			}
			return int(value), nil
		}
		if err := reader.skip(wire); err != nil {
			return 0, err
		}
	}
}

func decodeSABRMediaHeader(data []byte) (sabrMediaHeader, error) {
	var header sabrMediaHeader
	reader := protoReader{data: data}
	for {
		field, wire, ok, err := reader.next()
		if err != nil {
			return header, err
		}
		if !ok {
			if header.itag == 0 {
				return header, errSABR
			}
			return header, nil
		}
		switch {
		case field == 1 && wire == 0:
			header.headerID, err = reader.varint()
		case field == 3 && wire == 0:
			var value uint64
			value, err = reader.varint()
			header.itag = int(value)
		case field == 8 && wire == 0:
			var value uint64
			value, err = reader.varint()
			header.isInit = value != 0
		case field == 9 && wire == 0:
			header.sequence, err = reader.varint()
		case field == 12 && wire == 0:
			var value uint64
			value, err = reader.varint()
			header.durationMs = int64(value)
		case field == 13 && wire == 2:
			var format []byte
			format, err = reader.bytes()
			if err == nil {
				header.formatDigest = sha256.Sum256(format)
				var formatItag int
				formatItag, err = sabrFormatItag(format)
				if formatItag != 0 {
					header.itag = formatItag
				}
			}
		case field == 14 && wire == 0:
			var value uint64
			value, err = reader.varint()
			header.contentLength = int64(value)
		case field == 15 && wire == 2:
			var timeRange []byte
			timeRange, err = reader.bytes()
			if err == nil {
				header.timeDuration, header.timeScale, err = decodeSABRTimeRange(timeRange)
			}
		default:
			err = reader.skip(wire)
		}
		if err != nil {
			return header, err
		}
	}
}

func decodeSABRTimeRange(data []byte) (duration, scale int64, err error) {
	reader := protoReader{data: data}
	for {
		field, wire, ok, nextErr := reader.next()
		if nextErr != nil || !ok {
			return duration, scale, nextErr
		}
		if field == 2 && wire == 0 {
			value, valueErr := reader.varint()
			duration, err = int64(value), valueErr
		} else if field == 3 && wire == 0 {
			value, valueErr := reader.varint()
			scale, err = int64(value), valueErr
		} else {
			err = reader.skip(wire)
		}
		if err != nil {
			return 0, 0, err
		}
	}
}

func (capture *sabrCapture) finish() (int64, error) {
	capture.mu.Lock()
	defer capture.mu.Unlock()
	if !capture.complete || !capture.formatVerified || !capture.initWritten || capture.totalWritten <= 0 {
		return capture.totalWritten, errSABR
	}
	if err := capture.file.Sync(); err != nil {
		return capture.totalWritten, errStorage
	}
	return capture.totalWritten, nil
}
