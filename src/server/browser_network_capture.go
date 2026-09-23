package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"log"
	"os"
	"sync"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

// networkSABRResponse holds one browser response until Chrome has delivered all
// of its bytes. The browser receives the original response without Fetch
// interception; CDP only streams a copy for verified media assembly.
const maxNetworkSABRBytes int64 = 128 * 1024 * 1024

type networkSABRResponse struct {
	mu           sync.Mutex
	body         bytes.Buffer
	pending      [][]byte
	pendingBytes int64
	ready        bool
	closed       bool
	err          error
	done         chan struct{}
}

func (r *networkSABRResponse) appendData(data []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	if int64(r.body.Len())+r.pendingBytes+int64(len(data)) > maxNetworkSABRBytes {
		r.err = errLimit
		r.closed = true
		close(r.done)
		return
	}
	if r.ready {
		_, _ = r.body.Write(data)
	} else {
		r.pending = append(r.pending, data)
		r.pendingBytes += int64(len(data))
	}
}

func (r *networkSABRResponse) setBuffered(data []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if int64(len(data))+r.pendingBytes > maxNetworkSABRBytes {
		if !r.closed {
			r.err = errLimit
			r.closed = true
			close(r.done)
		}
		return
	}
	_, _ = r.body.Write(data)
	for _, pending := range r.pending {
		_, _ = r.body.Write(pending)
	}
	r.pending = nil
	r.pendingBytes = 0
	r.ready = true
}

func (r *networkSABRResponse) finish(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	r.err = err
	r.closed = true
	close(r.done)
}

func (p *chromeBrowserProvider) ensureNetworkCapture() error {
	p.networkOnce.Do(func() {
		p.networkMu.Lock()
		p.networkSABR = make(map[network.RequestID]*networkSABRResponse)
		p.networkMu.Unlock()
		chromedp.ListenTarget(p.ctx, p.handleNetworkCaptureEvent)
		p.networkErr = chromedp.Run(p.ctx, network.Enable())
	})
	return p.networkErr
}

func (p *chromeBrowserProvider) networkResponse(id network.RequestID) *networkSABRResponse {
	p.networkMu.Lock()
	defer p.networkMu.Unlock()
	return p.networkSABR[id]
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
		response := &networkSABRResponse{done: make(chan struct{})}
		p.networkMu.Lock()
		p.networkSABR[event.RequestID] = response
		p.networkMu.Unlock()
		go p.streamNetworkSABR(event.RequestID, response, capture, captureCtx, metrics)
	case *network.EventDataReceived:
		response := p.networkResponse(event.RequestID)
		if response == nil || event.Data == "" {
			return
		}
		data, err := base64.StdEncoding.DecodeString(event.Data)
		if err != nil {
			response.finish(errSABR)
			return
		}
		response.appendData(data)
	case *network.EventLoadingFinished:
		if response := p.networkResponse(event.RequestID); response != nil {
			response.finish(nil)
		}
	case *network.EventLoadingFailed:
		if response := p.networkResponse(event.RequestID); response != nil {
			response.finish(errBrowserUnavailable)
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
	buffered, err := network.StreamResourceContent(id).Do(commandCtx)
	setupDuration := time.Since(start)
	var body []byte
	if err != nil {
		// Tiny responses can finish before the stream command reaches Chrome.
		// Network.getResponseBody is sufficient for those completed responses.
		body, err = network.GetResponseBody(id).Do(commandCtx)
		if err != nil || len(body) == 0 || int64(len(body)) > maxNetworkSABRBytes {
			if os.Getenv("YTDL_TRACE_PERFORMANCE") != "" {
				log.Printf("browser network response unavailable: %v", err)
			}
			capture.fail(errBrowserUnavailable)
			return
		}
	} else {
		response.setBuffered(buffered)
		select {
		case <-response.done:
		case <-captureCtx.Done():
			capture.fail(captureCtx.Err())
			return
		}
		response.mu.Lock()
		responseErr, ready := response.err, response.ready
		body = response.body.Bytes()
		response.mu.Unlock()
		if responseErr != nil || !ready || len(body) == 0 {
			if responseErr == nil {
				responseErr = errSABR
			}
			capture.fail(responseErr)
			return
		}
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
