package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

// networkSABRResponse holds one browser response until Chrome has delivered all
// of its bytes. The browser receives the original response without Fetch
// interception; CDP only streams a copy for verified media assembly.
const maxNetworkSABRBytes int64 = 128 * 1024 * 1024

var errNetworkResponseCanceled = errors.New("browser canceled a media response")
var errBrowserResponseLimit = errors.New("browser media response exceeded capture buffer")

type networkCompletion struct {
	err          error
	encodedBytes int64
}

type networkByteCounts struct {
	decoded, encoded int64
}

func browserNetworkParams() network.EnableParams {
	// Keep completed large media responses available until their body is read.
	// Prepare must preserve these limits when it enables Network again.
	total, resource := 2*maxNetworkSABRBytes, maxNetworkSABRBytes
	return network.EnableParams{MaxTotalBufferSize: &total, MaxResourceBufferSize: &resource}
}

type networkSABRResponse struct {
	mu                   sync.Mutex
	body                 bytes.Buffer
	pending              [][]byte
	pendingBytes         int64
	ready                bool
	closed               bool
	err                  error
	done                 chan struct{}
	updated              chan struct{}
	expectedBytes        int64
	headerBytes          int64
	expectedEncodedBytes int64
	encodedBytes         int64
	decodedBytes         int64
}

func (r *networkSABRResponse) notifyLocked() {
	if r.updated != nil {
		close(r.updated)
	}
	r.updated = make(chan struct{})
}

func (r *networkSABRResponse) appendData(data []byte, counts ...int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return
	}
	if len(counts) == 2 {
		r.decodedBytes += counts[0]
		r.encodedBytes += counts[1]
	}
	if int64(r.body.Len())+r.pendingBytes+int64(len(data)) > maxNetworkSABRBytes {
		r.err = errBrowserResponseLimit
		if !r.closed {
			r.closed = true
			close(r.done)
		}
		r.notifyLocked()
		return
	}
	if r.ready {
		_, _ = r.body.Write(data)
	} else {
		r.pending = append(r.pending, data)
		r.pendingBytes += int64(len(data))
	}
	r.notifyLocked()
}

func (r *networkSABRResponse) setBuffered(data []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if int64(len(data))+r.pendingBytes > maxNetworkSABRBytes {
		r.err = errBrowserResponseLimit
		if !r.closed {
			r.closed = true
			close(r.done)
		}
		r.notifyLocked()
		return
	}
	_, _ = r.body.Write(data)
	for _, pending := range r.pending {
		_, _ = r.body.Write(pending)
	}
	r.pending = nil
	r.pendingBytes = 0
	r.ready = true
	r.notifyLocked()
}

func (r *networkSABRResponse) finish(err error, encoded ...int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	r.err = err
	if r.expectedBytes < 0 && r.headerBytes >= 0 && len(encoded) > 0 && encoded[0] >= r.headerBytes {
		// Encoded counts include HTTP chunk framing. Wait for all encoded data
		// events, then compare the copied body with their decoded byte count.
		r.expectedEncodedBytes = encoded[0] - r.headerBytes
	}
	r.closed = true
	close(r.done)
	r.notifyLocked()
}

// Stream and completion subscriptions have independent queues. A successful
// completion does not mean the final DataReceived payload has been consumed.
// A declared length or complete encoded/decoded event counts certify the copy.
func (r *networkSABRResponse) streamedBody(ctx context.Context) ([]byte, error) {
	for {
		r.mu.Lock()
		if r.err != nil {
			err := r.err
			r.mu.Unlock()
			return nil, err
		}
		expected := r.expectedBytes
		if expected < 0 && r.expectedEncodedBytes >= 0 && r.encodedBytes >= r.expectedEncodedBytes {
			if r.encodedBytes != r.expectedEncodedBytes {
				r.mu.Unlock()
				return nil, errLength
			}
			expected = r.decodedBytes
		}
		if expected < 0 && r.expectedEncodedBytes < 0 {
			r.mu.Unlock()
			return nil, errBrowserUnavailable
		}
		if expected >= 0 && r.ready && r.closed && int64(r.body.Len()) >= expected {
			body := bytes.Clone(r.body.Bytes())
			r.mu.Unlock()
			if int64(len(body)) != expected {
				return nil, errLength
			}
			return body, nil
		}
		if r.updated == nil {
			r.updated = make(chan struct{})
		}
		updated := r.updated
		r.mu.Unlock()
		select {
		case <-updated:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func networkResponseLength(headers network.Headers) int64 {
	length := int64(-1)
	for name, value := range headers {
		if strings.EqualFold(name, "content-encoding") && !strings.EqualFold(fmt.Sprint(value), "identity") {
			return -1
		}
		if strings.EqualFold(name, "content-length") {
			if parsed, err := strconv.ParseInt(fmt.Sprint(value), 10, 64); err == nil && parsed >= 0 {
				length = parsed
			}
		}
	}
	return length
}

func (p *chromeBrowserProvider) ensureNetworkCapture() error {
	p.networkOnce.Do(func() {
		p.networkMu.Lock()
		p.networkSABR = make(map[network.RequestID]*networkSABRResponse)
		p.networkMu.Unlock()

		// Subscribe before enabling Network so no response events are lost.
		responseEvents := chromedp.Events(p.ctx, network.ResponseReceived)
		dataEvents := chromedp.Events(p.ctx, network.DataReceived)
		finishedEvents := chromedp.Events(p.ctx, network.LoadingFinished)
		failedEvents := chromedp.Events(p.ctx, network.LoadingFailed)
		go func() {
			for event, err := range responseEvents {
				if err != nil {
					return
				}
				p.handleNetworkCaptureEvent(&event)
			}
		}()
		go func() {
			for event, err := range dataEvents {
				if err != nil {
					return
				}
				p.handleNetworkCaptureEvent(&event)
			}
		}()
		go func() {
			for event, err := range finishedEvents {
				if err != nil {
					return
				}
				p.handleNetworkCaptureEvent(&event)
			}
		}()
		go func() {
			for event, err := range failedEvents {
				if err != nil {
					return
				}
				p.handleNetworkCaptureEvent(&event)
			}
		}()
		_, p.networkErr = chromedp.Call(p.ctx, network.Enable, browserNetworkParams())
	})
	return p.networkErr
}

func (p *chromeBrowserProvider) registerNetworkResponse(id network.RequestID, expected ...int64) *networkSABRResponse {
	p.networkMu.Lock()
	defer p.networkMu.Unlock()
	if p.networkSABR == nil {
		p.networkSABR = make(map[network.RequestID]*networkSABRResponse)
	}
	response := &networkSABRResponse{done: make(chan struct{}), expectedBytes: -1, headerBytes: -1, expectedEncodedBytes: -1}
	if len(expected) > 0 {
		response.expectedBytes = expected[0]
	}
	if len(expected) > 1 {
		response.headerBytes = expected[1]
	}
	p.networkSABR[id] = response
	if early, ok := p.networkEarlyData[id]; ok {
		// These events preceded StreamResourceContent, so their payload is
		// included in BufferedData. Replay only their accounting, not bytes.
		response.appendData(nil, early.decoded, early.encoded)
		delete(p.networkEarlyData, id)
	}
	if completion, finished := p.networkDone[id]; finished {
		response.finish(completion.err, completion.encodedBytes)
		delete(p.networkDone, id)
	}
	return response
}

func (p *chromeBrowserProvider) finishNetworkResponse(id network.RequestID, err error, encoded ...int64) {
	p.networkMu.Lock()
	defer p.networkMu.Unlock()
	if response := p.networkSABR[id]; response != nil {
		response.finish(err, encoded...)
		return
	}
	// Separate typed subscriptions may deliver completion before the response
	// consumer has registered the request. Keep a bounded replay cache.
	if p.networkDone == nil {
		p.networkDone = make(map[network.RequestID]networkCompletion)
	}
	if len(p.networkDone) >= 1024 {
		for old := range p.networkDone {
			delete(p.networkDone, old)
			break
		}
	}
	completion := networkCompletion{err: err, encodedBytes: -1}
	if len(encoded) > 0 {
		completion.encodedBytes = encoded[0]
	}
	p.networkDone[id] = completion
}

func (p *chromeBrowserProvider) handleNetworkCaptureEvent(event interface{}) {
	switch event := event.(type) {
	case *network.EventResponseReceived:
		if event.Response == nil || !isSABRMediaURL(event.Response.URL) {
			return
		}
		p.captureMu.Lock()
		capture, captureCtx, metrics := p.capture, p.captureCtx, p.metrics
		if capture != nil && captureCtx != nil {
			capture.addHandler()
		}
		p.captureMu.Unlock()
		if capture == nil || captureCtx == nil {
			return
		}
		if event.Response.Status < 200 || event.Response.Status >= 300 {
			capture.doneHandler()
			capture.fail(errBrowserUnavailable)
			return
		}
		headerBytes := int64(event.Response.EncodedDataLength)
		for name, value := range event.Response.Headers {
			if strings.EqualFold(name, "content-encoding") && !strings.EqualFold(fmt.Sprint(value), "identity") {
				headerBytes = -1
			}
		}
		response := p.registerNetworkResponse(event.RequestID, networkResponseLength(event.Response.Headers), headerBytes)
		go p.streamNetworkSABR(event.RequestID, response, capture, captureCtx, metrics)
	case *network.EventDataReceived:
		p.networkMu.Lock()
		response := p.networkSABR[event.RequestID]
		if response == nil {
			// ResponseReceived has an independent queue too. Preserve the tiny
			// accounting record until registration; do not retain page payloads.
			if p.networkEarlyData == nil {
				p.networkEarlyData = make(map[network.RequestID]networkByteCounts)
			}
			if _, exists := p.networkEarlyData[event.RequestID]; !exists && len(p.networkEarlyData) >= 1024 {
				for old := range p.networkEarlyData {
					delete(p.networkEarlyData, old)
					break
				}
			}
			counts := p.networkEarlyData[event.RequestID]
			counts.decoded += event.DataLength
			counts.encoded += event.EncodedDataLength
			p.networkEarlyData[event.RequestID] = counts
			p.networkMu.Unlock()
			return
		}
		p.networkMu.Unlock()
		// The typed CDProto API decodes base64 event payloads into bytes.
		response.appendData(event.Data, event.DataLength, event.EncodedDataLength)
		if len(event.Data) > 0 || event.DataLength > 0 {
			p.lastNetworkProgress.Store(time.Now().UnixNano())
		}
	case *network.EventLoadingFinished:
		p.finishNetworkResponse(event.RequestID, nil, int64(event.EncodedDataLength))
	case *network.EventLoadingFailed:
		if event.Canceled {
			p.finishNetworkResponse(event.RequestID, errNetworkResponseCanceled)
		} else {
			p.finishNetworkResponse(event.RequestID, errBrowserUnavailable)
		}
	}
}

func (p *chromeBrowserProvider) streamNetworkSABR(id network.RequestID, response *networkSABRResponse, capture sabrResponseCapture, captureCtx context.Context, metrics *browserCaptureMetrics) {
	defer capture.doneHandler()
	defer func() {
		p.networkMu.Lock()
		delete(p.networkSABR, id)
		p.networkMu.Unlock()
	}()
	commandCtx, cancel := context.WithTimeout(captureCtx, 30*time.Second)
	defer cancel()
	start := time.Now()
	streamResult, err := chromedp.Call(commandCtx, network.StreamResourceContent, network.StreamResourceContentParams{RequestID: id})
	setupDuration := time.Since(start)
	getCompletedBody := func() (network.GetResponseBodyResult, error) {
		// A long response may outlive the initial stream-command deadline.
		readCtx, cancelRead := context.WithTimeout(captureCtx, 30*time.Second)
		defer cancelRead()
		return chromedp.Call(readCtx, network.GetResponseBody, network.GetResponseBodyParams{RequestID: id})
	}
	if err == nil {
		response.setBuffered(streamResult.BufferedData)
		if len(streamResult.BufferedData) > 0 {
			p.lastNetworkProgress.Store(time.Now().UnixNano())
		}
	}
	body, err := completedNetworkBody(captureCtx, response, getCompletedBody)
	if errors.Is(err, errNetworkResponseCanceled) {
		return
	}
	if err != nil {
		if os.Getenv("YTDL_TRACE_PERFORMANCE") != "" {
			log.Printf("browser response could not be verified: %v", err)
		}
		capture.fail(err)
		return
	}
	// Empty successful control responses contain no media to assemble. They
	// must not abort otherwise healthy captures; the media idle deadline still
	// bounds sessions that never deliver another fragment.
	if len(body) == 0 {
		return
	}
	start = time.Now()
	parseErr := capture.consumeReader(bytes.NewReader(body))
	parseDuration := time.Since(start)
	if metrics != nil {
		metrics.recordResponse(int64(len(body)), setupDuration, parseDuration, 0)
	}
	if parseErr != nil {
		capture.fail(parseErr)
	}
}

// Prefer Chrome's completed body. When its inspector cache evicts that body,
// wait for a length-verified streamed copy, including any late data events.
func completedNetworkBody(ctx context.Context, response *networkSABRResponse, read func() (network.GetResponseBodyResult, error)) ([]byte, error) {
	select {
	case <-response.done:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	response.mu.Lock()
	responseErr := response.err
	response.mu.Unlock()
	if responseErr != nil {
		return nil, responseErr
	}
	completed, err := read()
	if err == nil {
		if int64(len(completed.Body)) > maxNetworkSABRBytes {
			return nil, errBrowserResponseLimit
		}
		return completed.Body, nil
	}
	if os.Getenv("YTDL_TRACE_PERFORMANCE") != "" {
		log.Printf("browser completed response unavailable; waiting for verified stream: %v", err)
	}
	readCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	body, streamErr := response.streamedBody(readCtx)
	if streamErr != nil && os.Getenv("YTDL_TRACE_PERFORMANCE") != "" {
		response.mu.Lock()
		log.Printf("browser stream verification failed: buffered_bytes=%d expected_bytes=%d decoded_bytes=%d encoded_bytes=%d expected_encoded_bytes=%d error=%v", response.body.Len(), response.expectedBytes, response.decodedBytes, response.encodedBytes, response.expectedEncodedBytes, streamErr)
		response.mu.Unlock()
	}
	return body, streamErr
}
