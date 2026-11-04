package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

func TestBrowserNavigationRecoveryRetainsDeadlineAndErrors(t *testing.T) {
	t.Run("document change", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		attempts := 0
		err := retryBrowserNavigation(ctx, func() error {
			attempts++
			if attempts == 1 {
				return fmt.Errorf("metadata-ready: %w", errors.New("Inspected target navigated or closed (-32000)"))
			}
			return nil
		})
		if err != nil || attempts != 2 {
			t.Fatalf("attempts=%d err=%v", attempts, err)
		}
	})
	t.Run("permanent failure", func(t *testing.T) {
		attempts := 0
		err := retryBrowserNavigation(context.Background(), func() error { attempts++; return errStorage })
		if !errors.Is(err, errStorage) || attempts != 1 {
			t.Fatalf("attempts=%d err=%v", attempts, err)
		}
	})
	t.Run("bounded navigation", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
		defer cancel()
		err := retryBrowserNavigation(ctx, func() error { return errors.New("Execution context was destroyed") })
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("err=%v", err)
		}
	})
}

// A real browser fixture makes two document changes while metadata and playback
// polls are pending. The final document never fires load because an image stays
// pending, but the player is ready and must not wait for that unrelated request.
func TestLiveBrowserPreparationSurvivesNavigationAndPendingLoad(t *testing.T) {
	binary := os.Getenv("YTDL_TEST_BROWSER")
	if binary == "" {
		t.Skip("set YTDL_TEST_BROWSER to a Chrome/Edge executable")
	}
	pending := make(chan struct{})
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/start":
			fmt.Fprint(w, `<script>setTimeout(()=>location.replace('/middle'),250)</script>`)
		case "/middle":
			fmt.Fprint(w, `<script>window.fixtureReady=true;setTimeout(()=>location.replace('/ready'),250)</script>`)
		case "/ready":
			fmt.Fprint(w, `<script>window.fixtureReady=true;window.fixturePlaying=true;</script><img src="/pending">`)
		case "/pending":
			close(pending)
			<-r.Context().Done()
		}
	}))
	defer fixture.Close()
	parent, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	browser, closeBrowser, closeAllocator, err := launchChromeBrowser(parent, binary)
	if err != nil {
		t.Fatal(err)
	}
	defer closeAllocator()
	defer closeBrowser()
	provider := &chromeBrowserProvider{ctx: browser}
	ctx, stop := context.WithTimeout(browser, 5*time.Second)
	defer stop()
	if err := provider.preparePlayback(ctx, fixture.URL+"/start", "window.fixtureReady===true", "window.fixtureStarted=true", "window.fixtureStarted===true && window.fixturePlaying===true"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-pending:
	case <-ctx.Done():
		t.Fatal("fixture did not leave a page resource pending")
	}
	loaded, err := chromedp.Run(ctx, chromedp.Evaluate[bool]("document.readyState === 'complete'"))
	if err != nil || loaded {
		t.Fatalf("playback should be ready before page load completes: loaded=%t err=%v", loaded, err)
	}
}
