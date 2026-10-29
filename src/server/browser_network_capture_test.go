package main

import (
	"errors"
	"testing"

	"github.com/chromedp/cdproto/network"
)

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
