// browser_provider.go manages pooled headless Chrome tabs and captures the
// selected adaptive media track when direct streams are insufficient.
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
	"sync/atomic"
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

// browserDualTrackProvider is deliberately optional so existing test and
// alternate providers retain the serial CaptureTrack contract.
type browserDualTrackProvider interface {
	CaptureTracks(context.Context, string, *youtube.Format, []*youtube.Format, *youtube.Format, string, string, int64, int64, func(int64, int64)) (int64, int64, error)
}

type browserProviderFactory func(context.Context) (browserMediaProvider, error)

type chromeBrowserProvider struct {
	ctx         context.Context
	cancel      context.CancelFunc
	allocCancel context.CancelFunc
	release     func()
	closeOnce   sync.Once
	fetchOnce   sync.Once
	fetchErr    error
	uaOnce      sync.Once
	uaErr       error
	openMu      sync.Mutex
	networkOnce sync.Once
	networkErr  error
	networkMu   sync.Mutex
	networkSABR map[network.RequestID]*networkSABRResponse
	pendingMu   sync.Mutex
	pending     *browserRangeCapture
	captureMu   sync.Mutex
	capture     sabrResponseCapture
	captureCtx  context.Context
	captureStop context.CancelFunc
	metrics     *browserCaptureMetrics
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

// browserCaptureMetrics separates browser transport work from final track
// bytes. It is trace-only to keep the public job API stable.
type browserCaptureMetrics struct {
	mu                                   sync.Mutex
	started                              time.Time
	fetchSetup, identity, codec, prepare time.Duration
	wait, body, parse, resume, finish    time.Duration
	responses, responseBytes             int64
}

func newBrowserCaptureMetrics() *browserCaptureMetrics {
	return &browserCaptureMetrics{started: time.Now()}
}

func (metrics *browserCaptureMetrics) addPhase(target *time.Duration, duration time.Duration) {
	metrics.mu.Lock()
	*target += duration
	metrics.mu.Unlock()
}

func (metrics *browserCaptureMetrics) recordResponse(bodyBytes int64, body, parse, resume time.Duration) {
	metrics.mu.Lock()
	metrics.responses++
	metrics.responseBytes += bodyBytes
	metrics.body += body
	metrics.parse += parse
	metrics.resume += resume
	metrics.mu.Unlock()
}

func (metrics *browserCaptureMetrics) log(mode string, videoBytes, audioBytes int64, err error) {
	if os.Getenv("YTDL_TRACE_PERFORMANCE") == "" {
		return
	}
	metrics.mu.Lock()
	defer metrics.mu.Unlock()
	log.Printf("download metrics mode=%s elapsed=%s setup_fetch=%s identity=%s codec=%s prepare=%s wait=%s response_body=%s response_parse=%s response_resume=%s finish=%s responses=%d response_bytes=%d video_bytes=%d audio_bytes=%d err=%v",
		mode, time.Since(metrics.started).Round(time.Millisecond), metrics.fetchSetup.Round(time.Millisecond), metrics.identity.Round(time.Millisecond), metrics.codec.Round(time.Millisecond), metrics.prepare.Round(time.Millisecond), metrics.wait.Round(time.Millisecond), metrics.body.Round(time.Millisecond), metrics.parse.Round(time.Millisecond), metrics.resume.Round(time.Millisecond), metrics.finish.Round(time.Millisecond), metrics.responses, metrics.responseBytes, videoBytes, audioBytes, err)
}

func chromeBrowserOptions(executable string) []chromedp.ExecAllocatorOption {
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
	return options
}

func launchChromeBrowser(parent context.Context, executable string) (context.Context, context.CancelFunc, context.CancelFunc, error) {
	allocCtx, allocCancel := chromedp.NewExecAllocator(parent, chromeBrowserOptions(executable)...)
	browserCtx, cancel := chromedp.NewContext(allocCtx)
	startCtx, startCancel := context.WithTimeout(browserCtx, browserPoolStartTimeout)
	err := chromedp.Run(startCtx, chromedp.Navigate("about:blank"))
	startCancel()
	if err != nil {
		cancel()
		allocCancel()
		return nil, nil, nil, err
	}
	return browserCtx, cancel, allocCancel, nil
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
		captureStop := p.captureStop
		p.capture = nil
		p.captureCtx = nil
		p.captureStop = nil
		p.metrics = nil
		p.captureMu.Unlock()
		if captureStop != nil {
			captureStop()
		}
		if capture != nil {
			capture.fail(errBrowserUnavailable)
		}
		if p.cancel != nil {
			p.cancel()
		}
		if p.allocCancel != nil {
			p.allocCancel()
		}
		if p.release != nil {
			p.release()
		}
	})
	return nil
}

func (p *chromeBrowserProvider) Prepare(ctx context.Context, id string, format *youtube.Format) error {
	if format == nil || format.ItagNo <= 0 || !videoID.MatchString(id) {
		return errBrowserUnavailable
	}
	videoURL := "https://www.youtube.com/watch?v=" + url.QueryEscape(id)
	quality := ""
	if format.Height > 0 {
		quality = "hd" + strconv.Itoa(format.Height)
	}
	configureQualityScript := `(() => {
  const labels = ['Accept all', 'I agree', 'Agree'];
  for (const button of document.querySelectorAll('button')) {
    const text = (button.innerText || '').trim();
    if (labels.some(label => text === label || text.includes(label))) button.click();
  }
  const player = document.getElementById('movie_player');
  if (player && '` + quality + `' !== '') {
    try { player.setPlaybackQualityRange('` + quality + `'); } catch (_) {}
    try { player.setPlaybackQuality('` + quality + `'); } catch (_) {}
  }
  const video = document.querySelector('video');
  if (video) { video.pause(); video.muted = true; }
  return !!video && video.readyState >= HTMLMediaElement.HAVE_METADATA;
})()`
	playScript := `(() => {
  const player = document.getElementById('movie_player');
  if (player && '` + quality + `' !== '') {
    try { player.setPlaybackQualityRange('` + quality + `'); } catch (_) {}
    try { player.setPlaybackQuality('` + quality + `'); } catch (_) {}
  }
  const video = document.querySelector('video');
  if (!video) return false;
  video.muted = true;
  video.playbackRate = ` + strconv.Itoa(browserPlaybackRate) + `;
  try { video.play(); } catch (_) {}
  clearInterval(window.__ytdlKeepAlive);
  window.__ytdlKeepAlive = setInterval(() => {
    const current = document.querySelector('video');
    if (!current) return;
    current.muted = true;
    current.playbackRate = ` + strconv.Itoa(browserPlaybackRate) + `;
    if (current.paused && !current.ended) current.play().catch(() => {});
  }, 1000);
  return true;
})()`
	playingScript := `(() => {
  const video = document.querySelector('video');
  return !!video && !video.paused && (video.readyState >= HTMLMediaElement.HAVE_CURRENT_DATA || video.ended);
})()`
	if ctx == nil {
		ctx = context.Background()
	}
	prepareCtx, cancelPrepare := context.WithTimeout(p.ctx, browserPrepareTimeout)
	stopCaller := context.AfterFunc(ctx, cancelPrepare)
	defer func() {
		stopCaller()
		cancelPrepare()
	}()
	var mediaReady, playing bool
	if err := chromedp.Run(prepareCtx,
		network.Enable(),
		chromedp.Navigate(videoURL),
		chromedp.Poll(configureQualityScript, &mediaReady, chromedp.WithPollingInterval(100*time.Millisecond), chromedp.WithPollingTimeout(browserPrepareTimeout)),
		chromedp.Evaluate(playScript, nil),
		chromedp.Poll(playingScript, &playing, chromedp.WithPollingInterval(100*time.Millisecond), chromedp.WithPollingTimeout(browserPlaybackReadyTimeout)),
	); err != nil {
		return errBrowserUnavailable
	}
	if !mediaReady || !playing {
		return errBrowserUnavailable
	}
	return nil
}

func (p *chromeBrowserProvider) prepareCapture(id string, format *youtube.Format, metrics *browserCaptureMetrics) error {
	phaseStart := time.Now()
	if err := p.ensureNetworkCapture(); err != nil {
		metrics.addPhase(&metrics.fetchSetup, time.Since(phaseStart))
		if os.Getenv("YTDL_TRACE_SABR") != "" {
			log.Printf("browser network capture setup failed: %v", err)
		}
		return errBrowserUnavailable
	}
	metrics.addPhase(&metrics.fetchSetup, time.Since(phaseStart))
	phaseStart = time.Now()
	if err := p.normalizeHeadlessIdentity(); err != nil {
		metrics.addPhase(&metrics.identity, time.Since(phaseStart))
		if os.Getenv("YTDL_TRACE_SABR") != "" {
			log.Printf("browser identity setup failed: %v", err)
		}
		return errBrowserUnavailable
	}
	metrics.addPhase(&metrics.identity, time.Since(phaseStart))
	phaseStart = time.Now()
	if err := p.configurePlaybackCodec(format); err != nil {
		metrics.addPhase(&metrics.codec, time.Since(phaseStart))
		if os.Getenv("YTDL_TRACE_SABR") != "" {
			log.Printf("browser codec setup failed: %v", err)
		}
		return errBrowserUnavailable
	}
	metrics.addPhase(&metrics.codec, time.Since(phaseStart))
	return nil
}

func (p *chromeBrowserProvider) attachCapture(capture sabrResponseCapture, metrics *browserCaptureMetrics) (context.CancelFunc, func(), error) {
	execCtx, err := p.targetContext()
	if err != nil {
		return nil, nil, errBrowserUnavailable
	}
	captureCtx, captureStop := context.WithCancel(execCtx)
	p.captureMu.Lock()
	if p.capture != nil {
		p.captureMu.Unlock()
		captureStop()
		return nil, nil, errBrowserUnavailable
	}
	p.capture = capture
	p.captureCtx = captureCtx
	p.captureStop = captureStop
	p.metrics = metrics
	p.captureMu.Unlock()
	var detachOnce sync.Once
	detach := func() {
		detachOnce.Do(func() {
			p.captureMu.Lock()
			if p.capture == capture {
				p.capture = nil
				p.captureCtx = nil
				p.captureStop = nil
				p.metrics = nil
			}
			p.captureMu.Unlock()
			captureStop()
		})
	}
	return captureStop, detach, nil
}

func (p *chromeBrowserProvider) waitForCapture(ctx context.Context, capture sabrResponseCapture, captureDone <-chan error, detach func(), metrics *browserCaptureMetrics, maximum time.Duration, stalled func() bool, finish func() error) error {
	timer := time.NewTimer(maximum)
	defer timer.Stop()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	keepAlive := `(() => {
  const video = document.querySelector('video');
  if (!video) return false;
  video.muted = true;
  video.playbackRate = ` + strconv.Itoa(browserPlaybackRate) + `;
  if (video.paused && !video.ended) video.play().catch(() => {});
  return !video.ended;
})()`
	waitStart := time.Now()
	defer func() { metrics.addPhase(&metrics.wait, time.Since(waitStart)) }()
	ticks := 0
	for {
		select {
		case captureErr := <-captureDone:
			if captureErr != nil {
				if os.Getenv("YTDL_TRACE_SABR") != "" {
					log.Printf("browser capture ended with error: %v", captureErr)
				}
				return errBrowserUnavailable
			}
			detach()
			capture.waitHandlers()
			phaseStart := time.Now()
			if err := finish(); err != nil {
				metrics.addPhase(&metrics.finish, time.Since(phaseStart))
				return errBrowserUnavailable
			}
			metrics.addPhase(&metrics.finish, time.Since(phaseStart))
			return nil
		case <-ticker.C:
			ticks++
			if ticks%10 == 0 {
				p.tracePlaybackState()
			}
			if stalled() {
				p.tracePlaybackState()
				if os.Getenv("YTDL_TRACE_SABR") != "" {
					log.Printf("browser capture stalled without new media for %s", browserStreamIdleTimeout)
				}
				return errBrowserUnavailable
			}
			if err := chromedp.Run(p.ctx, chromedp.Evaluate(keepAlive, nil)); err != nil {
				if os.Getenv("YTDL_TRACE_SABR") != "" {
					log.Printf("browser keep-alive failed: %v", err)
				}
				return errBrowserUnavailable
			}
		case <-timer.C:
			return errBrowserUnavailable
		case <-ctx.Done():
			return ctx.Err()
		case <-p.ctx.Done():
			return errBrowserUnavailable
		}
	}
}

func (p *chromeBrowserProvider) CaptureTrack(ctx context.Context, id string, format *youtube.Format, path string, budget int64, progress func(int64)) (result int64, err error) {
	if format == nil || format.ItagNo <= 0 || budget <= 0 || !videoID.MatchString(id) {
		return 0, errBrowserUnavailable
	}
	expectedDurationMs, parseErr := strconv.ParseInt(format.ApproxDurationMs, 10, 64)
	if parseErr != nil || expectedDurationMs <= 0 {
		return 0, errBrowserUnavailable
	}
	metrics := newBrowserCaptureMetrics()
	defer func() { metrics.log("single", result, 0, err) }()
	if err := p.prepareCapture(id, format, metrics); err != nil {
		return 0, err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return 0, errStorage
	}
	if progress == nil {
		progress = func(int64) {}
	}
	var lastProgress atomic.Int64
	lastProgress.Store(time.Now().UnixNano())
	capture := newSABRCapture(file, format.ItagNo, expectedDurationMs, budget, func(value int64) {
		lastProgress.Store(time.Now().UnixNano())
		progress(value)
	})
	_, detach, err := p.attachCapture(capture, metrics)
	if err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return 0, err
	}
	completed := false
	defer func() {
		detach()
		capture.waitHandlers()
		if closeErr := file.Close(); closeErr != nil && err == nil {
			err = errStorage
		}
		if !completed {
			_ = os.Remove(path)
		}
	}()
	phaseStart := time.Now()
	if err := p.Prepare(ctx, id, format); err != nil {
		metrics.addPhase(&metrics.prepare, time.Since(phaseStart))
		return 0, err
	}
	metrics.addPhase(&metrics.prepare, time.Since(phaseStart))
	var captured int64
	err = p.waitForCapture(ctx, capture, capture.done, detach, metrics, browserCaptureTimeout(expectedDurationMs, format.ContentLength), func() bool {
		return time.Since(time.Unix(0, lastProgress.Load())) >= browserStreamIdleTimeout
	}, func() error {
		var finishErr error
		captured, finishErr = capture.finish()
		return finishErr
	})
	if err != nil {
		return 0, err
	}
	completed = true
	return captured, nil
}

// CaptureTracks captures the selected WebM video and Opus audio from one
// playback. Network response streaming copies each SABR response once;
// sabrCaptureSet fans out its UMP parts to verified track assemblers.
func (p *chromeBrowserProvider) CaptureTracks(ctx context.Context, id string, videoFormat *youtube.Format, videoCandidates []*youtube.Format, audioFormat *youtube.Format, videoPath, audioPath string, videoBudget, audioBudget int64, progress func(int64, int64)) (videoResult, audioResult int64, err error) {
	if videoFormat == nil || audioFormat == nil || videoFormat.ItagNo <= 0 || audioFormat.ItagNo <= 0 || videoBudget <= 0 || audioBudget <= 0 || !videoID.MatchString(id) {
		return 0, 0, errBrowserUnavailable
	}
	videoDuration, videoDurationErr := strconv.ParseInt(videoFormat.ApproxDurationMs, 10, 64)
	audioDuration, audioDurationErr := strconv.ParseInt(audioFormat.ApproxDurationMs, 10, 64)
	if videoDurationErr != nil || audioDurationErr != nil || videoDuration <= 0 || audioDuration <= 0 {
		return 0, 0, errBrowserUnavailable
	}
	if progress == nil {
		progress = func(int64, int64) {}
	}
	metrics := newBrowserCaptureMetrics()
	defer func() { metrics.log("dual", videoResult, audioResult, err) }()
	if err := p.prepareCapture(id, videoFormat, metrics); err != nil {
		return 0, 0, err
	}
	videoFile, err := os.OpenFile(videoPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return 0, 0, errStorage
	}
	audioFile, err := os.OpenFile(audioPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		_ = videoFile.Close()
		_ = os.Remove(videoPath)
		return 0, 0, errStorage
	}

	var progressMu sync.Mutex
	var videoCurrent, audioCurrent int64
	var lastVideoProgress, lastAudioProgress atomic.Int64
	now := time.Now().UnixNano()
	lastVideoProgress.Store(now)
	lastAudioProgress.Store(now)
	videoItags := make([]int, 0, len(videoCandidates))
	for _, candidate := range videoCandidates {
		if candidate != nil {
			videoItags = append(videoItags, candidate.ItagNo)
		}
	}
	videoCapture := newSABRCapture(videoFile, videoFormat.ItagNo, videoDuration, videoBudget, func(value int64) {
		lastVideoProgress.Store(time.Now().UnixNano())
		progressMu.Lock()
		videoCurrent = value
		progress(videoCurrent, audioCurrent)
		progressMu.Unlock()
	}, videoItags...)
	audioCapture := newSABRCapture(audioFile, audioFormat.ItagNo, audioDuration, audioBudget, func(value int64) {
		lastAudioProgress.Store(time.Now().UnixNano())
		progressMu.Lock()
		audioCurrent = value
		progress(videoCurrent, audioCurrent)
		progressMu.Unlock()
	})
	capture := newSABRCaptureSet(videoCapture, audioCapture)
	_, detach, err := p.attachCapture(capture, metrics)
	if err != nil {
		_ = videoFile.Close()
		_ = audioFile.Close()
		_ = os.Remove(videoPath)
		_ = os.Remove(audioPath)
		return 0, 0, err
	}
	completed := false
	defer func() {
		detach()
		capture.waitHandlers()
		if closeErr := videoFile.Close(); closeErr != nil && err == nil {
			err = errStorage
		}
		if closeErr := audioFile.Close(); closeErr != nil && err == nil {
			err = errStorage
		}
		if !completed {
			_ = os.Remove(videoPath)
			_ = os.Remove(audioPath)
		}
	}()
	phaseStart := time.Now()
	if err := p.Prepare(ctx, id, videoFormat); err != nil {
		metrics.addPhase(&metrics.prepare, time.Since(phaseStart))
		return 0, 0, err
	}
	metrics.addPhase(&metrics.prepare, time.Since(phaseStart))
	maximumDuration := max(videoDuration, audioDuration)
	videoLength := videoFormat.ContentLength
	for _, candidate := range videoCandidates {
		if candidate != nil && candidate.ContentLength > videoLength {
			videoLength = candidate.ContentLength
		}
	}
	maximum := browserCaptureTimeout(maximumDuration, videoLength+audioFormat.ContentLength)
	err = p.waitForCapture(ctx, capture, capture.done, detach, metrics, maximum, func() bool {
		now := time.Now()
		videoStalled := !videoCapture.isComplete() && now.Sub(time.Unix(0, lastVideoProgress.Load())) >= browserStreamIdleTimeout
		audioStalled := !audioCapture.isComplete() && now.Sub(time.Unix(0, lastAudioProgress.Load())) >= browserStreamIdleTimeout
		return videoStalled || audioStalled
	}, func() error {
		var finishErr error
		videoResult, finishErr = videoCapture.finish()
		if finishErr == nil {
			audioResult, finishErr = audioCapture.finish()
		}
		return finishErr
	})
	if err != nil {
		return 0, 0, err
	}
	completed = true
	return videoResult, audioResult, nil
}

// browserCaptureTimeout allows for both accelerated playback and the selected
// track's actual transfer volume. Long 4K streams can require more wall time
// than duration/4 even while Chrome keeps playback at 4x.
func browserCaptureTimeout(durationMs, contentLength int64) time.Duration {
	maximum := time.Duration(durationMs)*time.Millisecond/4 + 2*time.Minute
	// A SABR response can be delivered as a long-lived 4K transfer rather
	// than the short media fragments that accelerated playback suggests. Allow
	// a verified, but modest, 2 MiB/s path before declaring that capture failed.
	const minimumThroughput = int64(2 * 1024 * 1024)
	if contentLength > 0 {
		transfer := time.Duration((contentLength+minimumThroughput-1)/minimumThroughput)*time.Second + 3*time.Minute
		if transfer > maximum {
			maximum = transfer
		}
	}
	if maximum < 4*time.Minute {
		return 4 * time.Minute
	}
	if maximum > 45*time.Minute {
		return 45 * time.Minute
	}
	return maximum
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

type playbackCodecPolicy struct {
	unsupportedPattern string
	av1Preference      string
}

func playbackPolicy(format *youtube.Format) (playbackCodecPolicy, bool) {
	if format == nil {
		return playbackCodecPolicy{}, false
	}
	kind, codecs, ok := formatType(format)
	if !ok {
		return playbackCodecPolicy{}, false
	}
	switch family := codecFamily(codecs); {
	case kind == "video/mp4" && family == "h264":
		return playbackCodecPolicy{unsupportedPattern: "(?:av01|av1|vp09|vp9|vp8)", av1Preference: "480"}, true
	case kind == "video/webm" && family == "vp9":
		return playbackCodecPolicy{unsupportedPattern: "(?:av01|av1)", av1Preference: "480"}, true
	case kind == "video/webm" && family == "av1":
		return playbackCodecPolicy{unsupportedPattern: "(?:vp09|vp9|vp8)"}, true
	case kind == "audio/webm" && family == "opus":
		return playbackCodecPolicy{unsupportedPattern: "(?:mp4a|audio\\/mp4)"}, true
	case kind == "audio/mp4" && family == "aac":
		return playbackCodecPolicy{unsupportedPattern: "(?:opus|audio\\/webm)"}, true
	default:
		return playbackCodecPolicy{}, false
	}
}

func (p *chromeBrowserProvider) configurePlaybackCodec(format *youtube.Format) error {
	policy, ok := playbackPolicy(format)
	if !ok {
		return errBrowserUnavailable
	}
	execCtx, err := p.targetContext()
	if err != nil {
		return err
	}
	script := `(() => {
  const unsupported = new RegExp(` + strconv.Quote(policy.unsupportedPattern) + `, 'i');
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
  ` + func() string {
		if policy.av1Preference == "" {
			return ""
		}
		return "try { localStorage.setItem('yt-player-av1-pref', " + strconv.Quote(policy.av1Preference) + "); } catch (_) {}"
	}() + `
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

const browserStreamIdleTimeout = 60 * time.Second
const browserPrepareTimeout = 30 * time.Second
const browserPlaybackReadyTimeout = 10 * time.Second

// browserPlaybackRate speeds up playback without overloading Chrome while its
// response bodies are being streamed to the media pipeline.
const browserPlaybackRate = 4

func (p *chromeBrowserProvider) tracePlaybackState() {
	if os.Getenv("YTDL_TRACE_PERFORMANCE") == "" {
		return
	}
	var state struct {
		CurrentTime float64 `json:"currentTime"`
		Duration    float64 `json:"duration"`
		Rate        float64 `json:"rate"`
		Paused      bool    `json:"paused"`
		Ended       bool    `json:"ended"`
		ReadyState  int     `json:"readyState"`
		BufferEnd   float64 `json:"bufferEnd"`
		PlayerState int     `json:"playerState"`
	}
	const script = `(() => {
  const video = document.querySelector('video');
  const player = document.getElementById('movie_player');
  return video ? {
    currentTime: video.currentTime, duration: video.duration,
    rate: video.playbackRate, paused: video.paused, ended: video.ended,
    readyState: video.readyState,
    bufferEnd: video.buffered.length ? video.buffered.end(video.buffered.length - 1) : 0,
    playerState: player && player.getPlayerState ? player.getPlayerState() : -1,
  } : null;
})()`
	stateCtx, cancel := context.WithTimeout(p.ctx, 3*time.Second)
	defer cancel()
	if err := chromedp.Run(stateCtx, chromedp.Evaluate(script, &state)); err != nil {
		log.Printf("browser playback state unavailable: %v", err)
		return
	}
	log.Printf("browser playback state current=%.1f duration=%.1f buffered=%.1f rate=%.1f paused=%t ended=%t ready=%d player=%d", state.CurrentTime, state.Duration, state.BufferEnd, state.Rate, state.Paused, state.Ended, state.ReadyState, state.PlayerState)
}

func browserReadWithIdleDeadline(ctx context.Context, timeout time.Duration, read func(context.Context) (int, error)) (int, error) {
	readCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	n, err := read(readCtx)
	if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
		return 0, errRead
	}
	return n, err
}

func (s *cdpBrowserStream) Read(dst []byte) (int, error) {
	if len(dst) == 0 {
		return 0, nil
	}
	return browserReadWithIdleDeadline(s.readCtx, browserStreamIdleTimeout, func(readCtx context.Context) (int, error) {
		return s.readWithContext(readCtx, dst)
	})
}

func (s *cdpBrowserStream) readWithContext(readCtx context.Context, dst []byte) (int, error) {
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
		err := cdp.Execute(readCtx, "IO.read", map[string]any{"handle": s.handle}, &response)
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
