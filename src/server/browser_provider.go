// browser_provider.go creates a temporary headless Chrome session and captures
// the selected adaptive media track when direct streams are insufficient.
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/fetch"
	cdpio "github.com/chromedp/cdproto/io"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	cdpruntime "github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
	"github.com/kkdai/youtube/v2"
)

var errBrowserUnavailable = errors.New("browser-assisted media authorization unavailable")

type browserMediaProvider interface {
	CaptureTrack(context.Context, string, *youtube.Format, string, int64, func(int64)) (int64, error)
	Close() error
}

type browserProviderFactory func(context.Context) (browserMediaProvider, error)

type chromeBrowserProvider struct {
	ctx         context.Context
	cancel      context.CancelFunc
	allocCancel context.CancelFunc
	closeOnce   sync.Once
	fetchOnce   sync.Once
	fetchErr    error
	uaOnce      sync.Once
	uaErr       error
	openMu      sync.Mutex
	pendingMu   sync.Mutex
	pending     *browserRangeCapture
	captureMu   sync.Mutex
	capture     *sabrCapture
}

type browserRangeCapture struct {
	targetURL string
	done      chan browserStreamResult
	claimed   bool
}

type browserStreamResult struct {
	stream io.ReadCloser
	length int64
	err    error
}

// newChromeBrowserProvider prepares a browser context, optionally using the
// executable selected by CHROME_PATH. The browser process starts on capture.
func newChromeBrowserProvider(parent context.Context, executable string) (browserMediaProvider, error) {
	options := append([]chromedp.ExecAllocatorOption{}, chromedp.DefaultExecAllocatorOptions[:]...)
	if executable != "" {
		options = append(options, chromedp.ExecPath(executable))
	}
	options = append(options, chromedp.Flag("autoplay-policy", "no-user-gesture-required"))
	options = append(options,
		chromedp.Flag("enable-automation", false),
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
		chromedp.Flag("headless", "new"),
	)
	allocCtx, allocCancel := chromedp.NewExecAllocator(parent, options...)
	browserCtx, cancel := chromedp.NewContext(allocCtx)
	return &chromeBrowserProvider{ctx: browserCtx, cancel: cancel, allocCancel: allocCancel}, nil
}

func (p *chromeBrowserProvider) Close() error {
	p.closeOnce.Do(func() {
		p.pendingMu.Lock()
		pending := p.pending
		p.pending = nil
		p.pendingMu.Unlock()
		if pending != nil {
			select {
			case pending.done <- browserStreamResult{err: errBrowserUnavailable}:
			default:
			}
		}
		p.captureMu.Lock()
		capture := p.capture
		p.capture = nil
		p.captureMu.Unlock()
		if capture != nil {
			capture.fail(errBrowserUnavailable)
		}
		p.cancel()
		p.allocCancel()
	})
	return nil
}

func (p *chromeBrowserProvider) Prepare(_ context.Context, id string, format *youtube.Format) error {
	if format == nil || format.ItagNo <= 0 || !videoID.MatchString(id) {
		return errBrowserUnavailable
	}
	videoURL := "https://www.youtube.com/watch?v=" + url.QueryEscape(id)
	quality := "hd" + strconv.Itoa(format.Height)
	qualityLabel := strconv.Itoa(format.Height) + "p"
	playScript := `(() => {
  const labels = ['Accept all', 'I agree', 'Agree'];
  for (const button of document.querySelectorAll('button')) {
    const text = (button.innerText || '').trim();
    if (labels.some(label => text === label || text.includes(label))) button.click();
  }
  const player = document.getElementById('movie_player');
  if (player) {
    try { player.setPlaybackQualityRange('` + quality + `'); } catch (_) {}
    try { player.setPlaybackQuality('` + quality + `'); } catch (_) {}
  }
  const video = document.querySelector('video');
  if (!video) return false;
  video.muted = true;
  video.playbackRate = 16;
  try { video.play(); } catch (_) {}
  clearInterval(window.__ytdlKeepAlive);
  window.__ytdlKeepAlive = setInterval(() => {
    const current = document.querySelector('video');
    if (!current) return;
    current.muted = true;
    current.playbackRate = 16;
    if (current.paused && !current.ended) current.play().catch(() => {});
  }, 1000);
  return true;
})()`
	qualityMenuScript := `(() => {
  const settings = document.querySelector('.ytp-settings-button');
  if (!settings) return false;
  settings.click();
  setTimeout(() => {
    const quality = [...document.querySelectorAll('.ytp-menuitem')].find(item => (item.innerText || '').includes('Quality'));
    if (!quality) return;
    quality.click();
    setTimeout(() => {
      const target = [...document.querySelectorAll('.ytp-menuitem')].find(item => (item.innerText || '').includes('` + qualityLabel + `'));
      if (target) target.click();
    }, 500);
  }, 500);
  return true;
})()`
	if err := chromedp.Run(p.ctx,
		network.Enable(),
		chromedp.Navigate(videoURL),
		chromedp.Sleep(5*time.Second),
		chromedp.Evaluate(playScript, nil),
		chromedp.Evaluate(qualityMenuScript, nil),
		chromedp.Sleep(8*time.Second),
	); err != nil {
		return errBrowserUnavailable
	}
	return nil
}

func (p *chromeBrowserProvider) CaptureTrack(ctx context.Context, id string, format *youtube.Format, path string, budget int64, progress func(int64)) (result int64, err error) {
	if format == nil || format.ItagNo <= 0 || budget <= 0 || !videoID.MatchString(id) {
		return 0, errBrowserUnavailable
	}
	expectedDurationMs, parseErr := strconv.ParseInt(format.ApproxDurationMs, 10, 64)
	if parseErr != nil || expectedDurationMs <= 0 {
		return 0, errBrowserUnavailable
	}
	if err := p.ensureFetch(); err != nil {
		if os.Getenv("YTDL_TRACE_SABR") != "" {
			log.Printf("browser fetch setup failed: %v", err)
		}
		return 0, errBrowserUnavailable
	}
	if err := p.normalizeHeadlessIdentity(); err != nil {
		if os.Getenv("YTDL_TRACE_SABR") != "" {
			log.Printf("browser identity setup failed: %v", err)
		}
		return 0, errBrowserUnavailable
	}
	if err := p.forceH264Playback(); err != nil {
		if os.Getenv("YTDL_TRACE_SABR") != "" {
			log.Printf("browser codec setup failed: %v", err)
		}
		return 0, errBrowserUnavailable
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return 0, errStorage
	}
	capture := newSABRCapture(file, format.ItagNo, expectedDurationMs, budget, progress)
	p.captureMu.Lock()
	if p.capture != nil {
		p.captureMu.Unlock()
		_ = file.Close()
		_ = os.Remove(path)
		return 0, errBrowserUnavailable
	}
	p.capture = capture
	p.captureMu.Unlock()
	completed := false
	defer func() {
		p.captureMu.Lock()
		if p.capture == capture {
			p.capture = nil
		}
		p.captureMu.Unlock()
		capture.handlers.Wait()
		if closeErr := file.Close(); closeErr != nil && err == nil {
			err = errStorage
		}
		if !completed {
			_ = os.Remove(path)
		}
	}()
	if err := p.Prepare(ctx, id, format); err != nil {
		return 0, err
	}
	maximum := time.Duration(expectedDurationMs)*time.Millisecond/8 + 2*time.Minute
	if maximum < 3*time.Minute {
		maximum = 3 * time.Minute
	}
	if maximum > 45*time.Minute {
		maximum = 45 * time.Minute
	}
	timer := time.NewTimer(maximum)
	defer timer.Stop()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	keepAlive := `(() => {
  const video = document.querySelector('video');
  if (!video) return false;
  video.muted = true;
  video.playbackRate = 16;
  if (video.paused && !video.ended) video.play().catch(() => {});
  return !video.ended;
})()`
	for {
		select {
		case captureErr := <-capture.done:
			if captureErr != nil {
				return 0, errBrowserUnavailable
			}
			p.captureMu.Lock()
			if p.capture == capture {
				p.capture = nil
			}
			p.captureMu.Unlock()
			capture.handlers.Wait()
			result, err = capture.finish()
			if err != nil {
				return result, errBrowserUnavailable
			}
			completed = true
			return result, nil
		case <-ticker.C:
			if runErr := chromedp.Run(p.ctx, chromedp.Evaluate(keepAlive, nil)); runErr != nil {
				return 0, errBrowserUnavailable
			}
		case <-timer.C:
			return 0, errBrowserUnavailable
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
}

type browserUABrand struct {
	Brand   string `json:"brand"`
	Version string `json:"version"`
}

type browserUAMetadata struct {
	UA       string           `json:"ua"`
	Brands   []browserUABrand `json:"brands"`
	Mobile   bool             `json:"mobile"`
	Platform string           `json:"platform"`
	Hints    struct {
		Architecture    string           `json:"architecture"`
		Bitness         string           `json:"bitness"`
		FullVersionList []browserUABrand `json:"fullVersionList"`
		Model           string           `json:"model"`
		PlatformVersion string           `json:"platformVersion"`
		UAFullVersion   string           `json:"uaFullVersion"`
		Wow64           bool             `json:"wow64"`
	} `json:"hints"`
}

func (p *chromeBrowserProvider) normalizeHeadlessIdentity() error {
	p.uaOnce.Do(func() {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			p.uaErr = err
			return
		}
		server := &http.Server{
			Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, "<!doctype html><title>browser setup</title>")
			}),
			ReadHeaderTimeout: 5 * time.Second,
		}
		go func() { _ = server.Serve(listener) }()
		defer func() { _ = server.Close() }()

		const metadataScript = `(async () => {
  const data = navigator.userAgentData;
  if (!data) return '';
  const hints = await data.getHighEntropyValues(['architecture','bitness','fullVersionList','model','platformVersion','uaFullVersion','wow64']);
  return JSON.stringify({ua:navigator.userAgent,brands:data.brands,mobile:data.mobile,platform:data.platform,hints});
})()`
		var raw string
		if err := chromedp.Run(p.ctx,
			chromedp.Navigate("http://"+listener.Addr().String()+"/"),
			chromedp.Evaluate(metadataScript, &raw, func(params *cdpruntime.EvaluateParams) *cdpruntime.EvaluateParams {
				return params.WithAwaitPromise(true)
			}),
		); err != nil {
			p.uaErr = err
			return
		}
		var metadata browserUAMetadata
		if raw == "" || json.Unmarshal([]byte(raw), &metadata) != nil || metadata.UA == "" || len(metadata.Brands) == 0 {
			p.uaErr = errBrowserUnavailable
			return
		}
		brands := make([]*emulation.UserAgentBrandVersion, 0, len(metadata.Brands))
		for _, brand := range metadata.Brands {
			brands = append(brands, &emulation.UserAgentBrandVersion{Brand: strings.ReplaceAll(brand.Brand, "HeadlessChrome", "Chrome"), Version: brand.Version})
		}
		fullVersions := make([]*emulation.UserAgentBrandVersion, 0, len(metadata.Hints.FullVersionList))
		for _, brand := range metadata.Hints.FullVersionList {
			fullVersions = append(fullVersions, &emulation.UserAgentBrandVersion{Brand: strings.ReplaceAll(brand.Brand, "HeadlessChrome", "Chrome"), Version: brand.Version})
		}
		userAgent := strings.ReplaceAll(metadata.UA, "HeadlessChrome", "Chrome")
		execCtx, err := p.targetContext()
		if err != nil {
			p.uaErr = err
			return
		}
		p.uaErr = emulation.SetUserAgentOverride(userAgent).
			WithAcceptLanguage("en-US,en;q=0.9").
			WithUserAgentMetadata(&emulation.UserAgentMetadata{
				Brands: brands, FullVersionList: fullVersions,
				Platform: metadata.Platform, PlatformVersion: metadata.Hints.PlatformVersion,
				Architecture: metadata.Hints.Architecture, Model: metadata.Hints.Model,
				Mobile: metadata.Mobile, Bitness: metadata.Hints.Bitness, Wow64: metadata.Hints.Wow64,
			}).Do(execCtx)
	})
	return p.uaErr
}

func (p *chromeBrowserProvider) forceH264Playback() error {
	execCtx, err := p.targetContext()
	if err != nil {
		return err
	}
	const script = `(() => {
  const unsupported = /(?:av01|av1|vp09|vp9|vp8)/i;
  if (window.MediaSource && typeof window.MediaSource.isTypeSupported === 'function') {
    const originalIsTypeSupported = window.MediaSource.isTypeSupported.bind(window.MediaSource);
    window.MediaSource.isTypeSupported = type => unsupported.test(type || '') ? false : originalIsTypeSupported(type);
  }
  if (window.HTMLMediaElement && typeof window.HTMLMediaElement.prototype.canPlayType === 'function') {
    const originalCanPlayType = window.HTMLMediaElement.prototype.canPlayType;
    window.HTMLMediaElement.prototype.canPlayType = function(type) {
      return unsupported.test(type || '') ? '' : originalCanPlayType.call(this, type);
    };
  }
  try { localStorage.setItem('yt-player-av1-pref', '480'); } catch (_) {}
})()`
	_, err = page.AddScriptToEvaluateOnNewDocument(script).WithRunImmediately(true).Do(execCtx)
	return err
}

func (p *chromeBrowserProvider) ensureFetch() error {
	p.fetchOnce.Do(func() {
		chromedp.ListenTarget(p.ctx, p.handleFetchEvent)
		pattern := &fetch.RequestPattern{
			URLPattern:   "*googlevideo.com/videoplayback*",
			RequestStage: fetch.RequestStageResponse,
		}
		p.fetchErr = chromedp.Run(p.ctx, fetch.Enable().WithPatterns([]*fetch.RequestPattern{pattern}))
	})
	return p.fetchErr
}

func (p *chromeBrowserProvider) handleFetchEvent(event interface{}) {
	paused, ok := event.(*fetch.EventRequestPaused)
	if !ok || paused.Request == nil {
		return
	}
	if isSABRMediaURL(paused.Request.URL) {
		p.captureMu.Lock()
		capture := p.capture
		if capture != nil {
			capture.handlers.Add(1)
		}
		p.captureMu.Unlock()
		if capture != nil {
			go p.captureSABRResponse(paused, capture)
			return
		}
	}
	p.pendingMu.Lock()
	pending := p.pending
	if pending != nil && !pending.claimed && sameBrowserRangeURL(paused.Request.URL, pending.targetURL) {
		if paused.ResponseStatusCode >= 200 && paused.ResponseStatusCode < 300 && paused.ResponseErrorReason == "" {
			pending.claimed = true
			p.pendingMu.Unlock()
			go p.capturePausedResponse(paused, pending)
			return
		}
		if paused.ResponseErrorReason != "" || paused.ResponseStatusCode >= 400 {
			pending.claimed = true
			p.pendingMu.Unlock()
			go p.capturePausedFailure(paused, pending)
			return
		}
	}
	p.pendingMu.Unlock()
	go p.continuePaused(paused)
}

func (p *chromeBrowserProvider) captureSABRResponse(paused *fetch.EventRequestPaused, capture *sabrCapture) {
	defer capture.handlers.Done()
	execCtx, err := p.targetContext()
	if err != nil {
		capture.fail(errBrowserUnavailable)
		return
	}
	if paused.ResponseStatusCode < 200 || paused.ResponseStatusCode >= 300 || paused.ResponseErrorReason != "" {
		p.continuePaused(paused)
		capture.fail(errBrowserUnavailable)
		return
	}
	body, bodyErr := fetch.GetResponseBody(paused.RequestID).Do(execCtx)
	if bodyErr != nil {
		_ = fetch.FailRequest(paused.RequestID, network.ErrorReasonFailed).Do(execCtx)
		capture.fail(bodyErr)
		return
	}
	if capture.trace {
		log.Printf("SABR response status=%d bytes=%d", paused.ResponseStatusCode, len(body))
	}
	fulfill := fetch.FulfillRequest(paused.RequestID, paused.ResponseStatusCode).
		WithResponseHeaders(paused.ResponseHeaders).
		WithBody(base64.StdEncoding.EncodeToString(body))
	if paused.ResponseStatusText != "" {
		fulfill = fulfill.WithResponsePhrase(paused.ResponseStatusText)
	}
	if fulfillErr := fulfill.Do(execCtx); fulfillErr != nil {
		capture.fail(fulfillErr)
		return
	}
	if consumeErr := capture.consume(body); consumeErr != nil {
		if capture.trace {
			log.Printf("SABR consume failed: %v", consumeErr)
		}
		capture.fail(consumeErr)
	}
}

func (p *chromeBrowserProvider) continuePaused(paused *fetch.EventRequestPaused) {
	execCtx, err := p.targetContext()
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(execCtx, 10*time.Second)
	defer cancel()
	if paused.ResponseStatusCode > 0 {
		_ = fetch.ContinueResponse(paused.RequestID).Do(ctx)
	} else {
		_ = fetch.ContinueRequest(paused.RequestID).Do(ctx)
	}
}

func (p *chromeBrowserProvider) capturePausedResponse(paused *fetch.EventRequestPaused, pending *browserRangeCapture) {
	execCtx, err := p.targetContext()
	if err != nil {
		pending.done <- browserStreamResult{err: errBrowserUnavailable}
		return
	}
	if paused.ResponseStatusCode < 200 || paused.ResponseStatusCode >= 300 {
		p.continuePaused(paused)
		pending.done <- browserStreamResult{err: errBrowserUnavailable}
		return
	}
	streamHandle, err := fetch.TakeResponseBodyAsStream(paused.RequestID).Do(execCtx)
	if err != nil {
		_ = fetch.FailRequest(paused.RequestID, network.ErrorReasonFailed).Do(execCtx)
		pending.done <- browserStreamResult{err: err}
		return
	}
	readCtx, cancel := context.WithCancel(execCtx)
	stream := &cdpBrowserStream{
		handle:     streamHandle,
		readCtx:    readCtx,
		readCancel: cancel,
		browserCtx: execCtx,
		onClose:    func() { p.openMu.Unlock() },
	}
	pending.done <- browserStreamResult{stream: stream, length: contentLength(paused.ResponseHeaders)}
}

func (p *chromeBrowserProvider) capturePausedFailure(paused *fetch.EventRequestPaused, pending *browserRangeCapture) {
	p.continuePaused(paused)
	pending.done <- browserStreamResult{err: errBrowserUnavailable}
}

func (p *chromeBrowserProvider) targetContext() (context.Context, error) {
	c := chromedp.FromContext(p.ctx)
	if c == nil || c.Target == nil {
		return nil, errBrowserUnavailable
	}
	return cdp.WithExecutor(p.ctx, c.Target), nil
}

func (p *chromeBrowserProvider) OpenRange(ctx context.Context, _ string, format *youtube.Format, start, end int64) (io.ReadCloser, int64, error) {
	if format == nil || start < 0 || end < start || !isTargetBrowserMediaURL(format.URL, format.ItagNo) {
		return nil, 0, errBrowserUnavailable
	}
	if err := p.ensureFetch(); err != nil {
		return nil, 0, errBrowserUnavailable
	}
	p.openMu.Lock()
	locked := true
	defer func() {
		if locked {
			p.openMu.Unlock()
		}
	}()
	targetURL, err := rangeURL(format.URL, start, end)
	if err != nil {
		return nil, 0, errBrowserUnavailable
	}
	pending := &browserRangeCapture{targetURL: targetURL, done: make(chan browserStreamResult, 1)}
	p.pendingMu.Lock()
	p.pending = pending
	p.pendingMu.Unlock()
	clearPending := func() {
		p.pendingMu.Lock()
		if p.pending == pending {
			p.pending = nil
		}
		p.pendingMu.Unlock()
	}
	defer func() {
		if locked {
			clearPending()
		}
	}()
	encodedURL, _ := json.Marshal(targetURL)
	script := `fetch(` + string(encodedURL) + `, {credentials:'include', cache:'no-store'}).catch(() => {})`
	if err := chromedp.Run(p.ctx, chromedp.Evaluate(script, nil)); err != nil {
		return nil, 0, errBrowserUnavailable
	}
	select {
	case result := <-pending.done:
		clearPending()
		if result.err != nil || result.stream == nil {
			return nil, 0, errBrowserUnavailable
		}
		locked = false
		return result.stream, result.length, nil
	case <-ctx.Done():
		clearPending()
		go p.closeOrphanedResult(pending)
		return nil, 0, ctx.Err()
	}
}

func (p *chromeBrowserProvider) closeOrphanedResult(pending *browserRangeCapture) {
	select {
	case result := <-pending.done:
		if result.stream != nil {
			_ = result.stream.Close()
		}
	case <-time.After(10 * time.Second):
	}
}

type cdpBrowserStream struct {
	handle     cdpio.StreamHandle
	readCtx    context.Context
	readCancel context.CancelFunc
	browserCtx context.Context
	buffer     []byte
	eof        bool
	closeOnce  sync.Once
	closeErr   error
	onClose    func()
}

func (s *cdpBrowserStream) Read(dst []byte) (int, error) {
	if len(dst) == 0 {
		return 0, nil
	}
	for emptyReads := 0; emptyReads < 100; emptyReads++ {
		if len(s.buffer) > 0 {
			n := copy(dst, s.buffer)
			s.buffer = s.buffer[n:]
			return n, nil
		}
		if s.eof {
			return 0, io.EOF
		}
		var response struct {
			Base64Encoded bool   `json:"base64Encoded"`
			Data          string `json:"data,omitempty"`
			EOF           bool   `json:"eof"`
		}
		err := cdp.Execute(s.readCtx, "IO.read", map[string]any{"handle": s.handle}, &response)
		if err != nil {
			return 0, err
		}
		var bytes []byte
		if response.Data != "" {
			if response.Base64Encoded {
				bytes, err = base64.StdEncoding.DecodeString(response.Data)
				if err != nil {
					return 0, err
				}
			} else {
				bytes = []byte(response.Data)
			}
		}
		if response.EOF {
			s.eof = true
		}
		if len(bytes) == 0 {
			continue
		}
		n := copy(dst, bytes)
		if n < len(bytes) {
			s.buffer = append(s.buffer, bytes[n:]...)
		}
		return n, nil
	}
	return 0, errRead
}

func (s *cdpBrowserStream) Close() error {
	s.closeOnce.Do(func() {
		s.readCancel()
		closeCtx, cancel := context.WithTimeout(s.browserCtx, 5*time.Second)
		s.closeErr = cdpio.Close(s.handle).Do(closeCtx)
		cancel()
		if s.onClose != nil {
			s.onClose()
		}
	})
	return s.closeErr
}

func rangeURL(raw string, start, end int64) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	query := u.Query()
	query.Set("range", strconv.FormatInt(start, 10)+"-"+strconv.FormatInt(end, 10))
	u.RawQuery = query.Encode()
	return u.String(), nil
}

func sameBrowserRangeURL(left, right string) bool {
	a, errA := url.Parse(left)
	b, errB := url.Parse(right)
	if errA != nil || errB != nil || a.Scheme != b.Scheme || a.Host != b.Host || a.Path != b.Path {
		return false
	}
	return a.Query().Encode() == b.Query().Encode()
}

func contentLength(headers []*fetch.HeaderEntry) int64 {
	for _, header := range headers {
		if strings.EqualFold(header.Name, "Content-Length") {
			value, err := strconv.ParseInt(strings.TrimSpace(header.Value), 10, 64)
			if err == nil && value >= 0 {
				return value
			}
		}
	}
	return 0
}

func isTargetBrowserMediaURL(raw string, itag int) bool {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Path != "/videoplayback" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if host != "googlevideo.com" && !strings.HasSuffix(host, ".googlevideo.com") {
		return false
	}
	return itag <= 0 || u.Query().Get("itag") == formatItag(itag)
}

func isSABRMediaURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Path != "/videoplayback" || u.Query().Get("sabr") != "1" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return host == "googlevideo.com" || strings.HasSuffix(host, ".googlevideo.com")
}

func formatItag(itag int) string {
	return strconv.Itoa(itag)
}
