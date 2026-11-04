package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

func browserNavigationInterrupted(err error) bool {
	if err == nil {
		return false
	}
	message := err.Error()
	for _, text := range []string{"Inspected target navigated or closed", "Execution context was destroyed", "Cannot find context with specified id", "Cannot find default execution context"} {
		if strings.Contains(message, text) {
			return true
		}
	}
	return false
}

// Poll's execution context belongs to one document. Restart the complete
// playback setup after a document change, without reloading the page or
// extending the caller's preparation deadline.
func retryBrowserNavigation(ctx context.Context, prepare func() error) error {
	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := prepare()
		if err == nil {
			if attempt > 0 && os.Getenv("YTDL_TRACE_PERFORMANCE") != "" {
				log.Printf("browser playback setup recovered navigation interruptions=%d", attempt)
			}
			return nil
		}
		if !browserNavigationInterrupted(err) {
			return err
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (p *chromeBrowserProvider) preparePlayback(ctx context.Context, videoURL, configureScript, playScript, playingScript string) error {
	// Page load can wait on unrelated requests after playable media is ready.
	// Start navigation directly and use the player's readiness as the gate.
	navigation, err := chromedp.Call(ctx, page.Navigate, page.NavigateParams{URL: videoURL})
	if err != nil {
		return fmt.Errorf("navigate: %w", err)
	}
	if navigation.ErrorText != "" {
		return fmt.Errorf("navigate: %s", navigation.ErrorText)
	}
	return retryBrowserNavigation(ctx, func() error {
		ready, err := chromedp.Run(ctx, chromedp.Poll[bool](configureScript, chromedp.WithPollingInterval(100*time.Millisecond), chromedp.WithPollingTimeout(browserPrepareTimeout)))
		if err != nil {
			return fmt.Errorf("metadata-ready: %w", err)
		}
		if !ready {
			return errBrowserUnavailable
		}
		if err := chromedp.Do(ctx, chromedp.Evaluate[chromedp.Void](playScript)); err != nil {
			return fmt.Errorf("start-playback: %w", err)
		}
		playing, err := chromedp.Run(ctx, chromedp.Poll[bool](playingScript, chromedp.WithPollingInterval(100*time.Millisecond), chromedp.WithPollingTimeout(browserPlaybackReadyTimeout)))
		if err != nil {
			return fmt.Errorf("playing: %w", err)
		}
		if !playing {
			return errBrowserUnavailable
		}
		return nil
	})
}
