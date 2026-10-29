package main

import (
	"errors"
	"testing"
	"time"

	"github.com/chromedp/cdproto/network"
)

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
