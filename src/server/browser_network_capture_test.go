package main

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/chromedp/cdproto/network"
)

func TestCompletedNetworkBodyWaitsForLateData(t *testing.T) {
	provider := &chromeBrowserProvider{}
	response := provider.registerNetworkResponse("media", 6)
	response.setBuffered([]byte("abc"))
	response.finish(nil) // LoadingFinished overtakes the final data subscription.
	reading := make(chan struct{})
	result := make(chan []byte, 1)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go func() {
		body, err := completedNetworkBody(ctx, response, func() (network.GetResponseBodyResult, error) {
			close(reading)
			return network.GetResponseBodyResult{}, errors.New("inspector cache evicted body")
		})
		if err != nil {
			t.Error(err)
		}
		result <- body
	}()
	<-reading
	response.appendData([]byte("def"))
	if body := <-result; !bytes.Equal(body, []byte("abcdef")) {
		t.Fatalf("assembled truncated response: %q", body)
	}
}

func TestCompletedNetworkBodyRejectsUnverifiedStream(t *testing.T) {
	for _, length := range []int64{-1, 2, 4} {
		response := (&chromeBrowserProvider{}).registerNetworkResponse("media", length)
		response.setBuffered([]byte("abc"))
		response.finish(nil)
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		body, err := completedNetworkBody(ctx, response, func() (network.GetResponseBodyResult, error) {
			return network.GetResponseBodyResult{}, errors.New("body unavailable")
		})
		cancel()
		if err == nil || body != nil {
			t.Fatalf("length=%d accepted uncertified bytes: %q, %v", length, body, err)
		}
	}
}

func TestChunkedNetworkResponseUsesFinalByteCount(t *testing.T) {
	provider := &chromeBrowserProvider{}
	// Completion can precede response registration on separate subscriptions.
	// Local Chrome fixture: 6 decoded bytes produce 21 encoded chunk bytes.
	provider.handleNetworkCaptureEvent(&network.EventDataReceived{RequestID: "media", DataLength: 3, EncodedDataLength: 8})
	provider.finishNetworkResponse("media", nil, 121)
	response := provider.registerNetworkResponse("media", -1, 100)
	response.appendData(nil, 3, 13)
	response.setBuffered([]byte("abcdef"))
	body, err := completedNetworkBody(context.Background(), response, func() (network.GetResponseBodyResult, error) {
		return network.GetResponseBodyResult{}, errors.New("body evicted")
	})
	if err != nil || string(body) != "abcdef" {
		t.Fatalf("chunked response lost its verified body: %q, %v", body, err)
	}
	if len(provider.networkEarlyData) != 0 {
		t.Fatal("early data accounting was not released")
	}
}

func TestNetworkResponseLengthRejectsCompressedOrMissingLength(t *testing.T) {
	for _, headers := range []network.Headers{{}, {"Content-Length": "invalid"}, {"Content-Length": "12", "Content-Encoding": "gzip"}} {
		if got := networkResponseLength(headers); got != -1 {
			t.Fatalf("unsafe stream length %d for %v", got, headers)
		}
	}
	if got := networkResponseLength(network.Headers{"Content-Length": "12"}); got != 12 {
		t.Fatalf("length=%d, want 12", got)
	}
}

func TestBrowserNetworkActivityWhileResponseIsIncomplete(t *testing.T) {
	provider := &chromeBrowserProvider{}
	old := time.Now().Add(-2 * time.Minute).UnixNano()
	provider.lastNetworkProgress.Store(old)
	provider.handleNetworkCaptureEvent(&network.EventDataReceived{RequestID: "unrelated", Data: []byte("page data")})
	if provider.lastNetworkProgress.Load() != old {
		t.Fatal("unrelated page data reset the media idle deadline")
	}
	response := provider.registerNetworkResponse("media")
	provider.handleNetworkCaptureEvent(&network.EventDataReceived{RequestID: "media", Data: []byte("partial media")})
	if provider.lastNetworkProgress.Load() <= old {
		t.Fatal("in-flight media did not update the idle deadline")
	}
	if response.closed || response.pendingBytes == 0 {
		t.Fatal("test must exercise a response that has not completed or been assembled")
	}
}

func TestBrowserNetworkCompletionBeforeResponseRegistration(t *testing.T) {
	for _, completion := range []error{nil, errNetworkResponseCanceled, errBrowserUnavailable} {
		provider := &chromeBrowserProvider{}
		provider.finishNetworkResponse("early", completion)
		response := provider.registerNetworkResponse("early")
		select {
		case <-response.done:
		default:
			t.Fatal("early completion was lost; capture would wait forever")
		}
		if !errors.Is(response.err, completion) {
			t.Fatalf("completion=%v, want %v", response.err, completion)
		}
		if len(provider.networkDone) != 0 {
			t.Fatal("replayed completion was not released")
		}
	}
}

func TestBrowserNetworkCompletionReplayIsBounded(t *testing.T) {
	provider := &chromeBrowserProvider{}
	for index := 0; index < 2048; index++ {
		provider.finishNetworkResponse(network.RequestID(string(rune(index))), nil)
	}
	if len(provider.networkDone) > 1024 {
		t.Fatalf("completion cache grew to %d", len(provider.networkDone))
	}
}
