// browser_provider.go creates a temporary headless Chrome session and captures
// the selected adaptive media track when direct streams are insufficient.
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
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
	ctx          context.Context
	cancel       context.CancelFunc
	allocCancel  context.CancelFunc
	closeOnce    sync.Once
	fetchOnce    sync.Once
	fetchErr     error
	uaOnce       sync.Once
	uaErr        error
	openMu       sync.Mutex
	pendingMu    sync.Mutex
	pending      *browserRangeCapture
	replayMu     sync.Mutex
	replayServer *http.Server
	replayAddr   string
	replayBodies map[string]browserReplayBody
	captureMu    sync.Mutex
	capture      sabrResponseCapture
	captureCtx   context.Context
	captureStop  context.CancelFunc
	metrics      *browserCaptureMetrics
	sabrReplay   chan struct{}
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

type browserReplayBody struct {
	status  int
	headers []*fetch.HeaderEntry
	body    []byte
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
	return &chromeBrowserProvider{ctx: browserCtx, cancel: cancel, allocCancel: allocCancel, sabrReplay: make(chan struct{}, 1), replayBodies: make(map[string]browserReplayBody)}, nil
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
		p.replayMu.Lock()
		replayServer := p.replayServer
		p.replayBodies = nil
		p.replayMu.Unlock()
		if replayServer != nil {
			ctx, stop := context.WithTimeout(context.Background(), 2*time.Second)
			_ = replayServer.Shutdown(ctx)
			stop()
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
	configureQualityScript := `(() => {
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
  if (video) { video.pause(); video.muted = true; }
  return !!video;
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
	playScript := `(() => {
  const player = document.getElementById('movie_player');
  if (player) {
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
	if err := chromedp.Run(p.ctx,
		network.Enable(),
		chromedp.Navigate(videoURL),
		chromedp.Sleep(5*time.Second),
		chromedp.Evaluate(configureQualityScript, nil),
		chromedp.Evaluate(qualityMenuScript, nil),
		chromedp.Sleep(3*time.Second),
		chromedp.Evaluate(playScript, nil),
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
	metrics := newBrowserCaptureMetrics()
	defer func() { metrics.log("single", result, 0, err) }()
	phaseStart := time.Now()
	if err := p.ensureFetch(); err != nil {
		metrics.addPhase(&metrics.fetchSetup, time.Since(phaseStart))
		if os.Getenv("YTDL_TRACE_SABR") != "" {
			log.Printf("browser fetch setup failed: %v", err)
		}
		return 0, errBrowserUnavailable
	}
	metrics.addPhase(&metrics.fetchSetup, time.Since(phaseStart))
	phaseStart = time.Now()
	if err := p.normalizeHeadlessIdentity(); err != nil {
		metrics.addPhase(&metrics.identity, time.Since(phaseStart))
		if os.Getenv("YTDL_TRACE_SABR") != "" {
			log.Printf("browser identity setup failed: %v", err)
		}
		return 0, errBrowserUnavailable
	}
	metrics.addPhase(&metrics.identity, time.Since(phaseStart))
	phaseStart = time.Now()
	if err := p.configurePlaybackCodec(format); err != nil {
		metrics.addPhase(&metrics.codec, time.Since(phaseStart))
		if os.Getenv("YTDL_TRACE_SABR") != "" {
			log.Printf("browser codec setup failed: %v", err)
		}
		return 0, errBrowserUnavailable
	}
	metrics.addPhase(&metrics.codec, time.Since(phaseStart))
	execCtx, contextErr := p.targetContext()
	if contextErr != nil {
		return 0, errBrowserUnavailable
	}
	captureCtx, captureStop := context.WithCancel(execCtx)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		captureStop()
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
	p.captureMu.Lock()
	if p.capture != nil {
		p.captureMu.Unlock()
		captureStop()
		_ = file.Close()
		_ = os.Remove(path)
		return 0, errBrowserUnavailable
	}
	p.capture = capture
	p.captureCtx = captureCtx
	p.captureStop = captureStop
	p.metrics = metrics
	p.captureMu.Unlock()
	completed := false
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
	phaseStart = time.Now()
	if err := p.Prepare(ctx, id, format); err != nil {
		metrics.addPhase(&metrics.prepare, time.Since(phaseStart))
		return 0, err
	}
	metrics.addPhase(&metrics.prepare, time.Since(phaseStart))
	maximum := browserCaptureTimeout(expectedDurationMs, format.ContentLength)
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
	for {
		select {
		case captureErr := <-capture.done:
			if captureErr != nil {
				if os.Getenv("YTDL_TRACE_SABR") != "" {
					log.Printf("browser capture ended with error: %v", captureErr)
				}
				return 0, errBrowserUnavailable
			}
			detach()
			capture.waitHandlers()
			phaseStart = time.Now()
			result, err = capture.finish()
			metrics.addPhase(&metrics.finish, time.Since(phaseStart))
			if err != nil {
				return result, errBrowserUnavailable
			}
			completed = true
			return result, nil
		case <-ticker.C:
			if time.Since(time.Unix(0, lastProgress.Load())) >= browserStreamIdleTimeout {
				if os.Getenv("YTDL_TRACE_SABR") != "" {
					log.Printf("browser capture stalled without new media for %s", browserStreamIdleTimeout)
				}
				return 0, errBrowserUnavailable
			}
			if runErr := chromedp.Run(p.ctx, chromedp.Evaluate(keepAlive, nil)); runErr != nil {
				if os.Getenv("YTDL_TRACE_SABR") != "" {
					log.Printf("browser keep-alive failed: %v", runErr)
				}
				return 0, errBrowserUnavailable
			}
		case <-timer.C:
			return 0, errBrowserUnavailable
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
}

// CaptureTracks captures the selected WebM video and Opus audio from one
// playback. Fetch pauses each SABR response once; sabrCaptureSet parses it once
// and fan-outs the UMP parts to independent verified track assemblers.
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

	phaseStart := time.Now()
	if setupErr := p.ensureFetch(); setupErr != nil {
		metrics.addPhase(&metrics.fetchSetup, time.Since(phaseStart))
		return 0, 0, errBrowserUnavailable
	}
	metrics.addPhase(&metrics.fetchSetup, time.Since(phaseStart))
	phaseStart = time.Now()
	if identityErr := p.normalizeHeadlessIdentity(); identityErr != nil {
		metrics.addPhase(&metrics.identity, time.Since(phaseStart))
		return 0, 0, errBrowserUnavailable
	}
	metrics.addPhase(&metrics.identity, time.Since(phaseStart))
	phaseStart = time.Now()
	if codecErr := p.configurePlaybackCodec(videoFormat); codecErr != nil {
		metrics.addPhase(&metrics.codec, time.Since(phaseStart))
		return 0, 0, errBrowserUnavailable
	}
	metrics.addPhase(&metrics.codec, time.Since(phaseStart))
	execCtx, contextErr := p.targetContext()
	if contextErr != nil {
		return 0, 0, errBrowserUnavailable
	}
	captureCtx, captureStop := context.WithCancel(execCtx)
	videoFile, openErr := os.OpenFile(videoPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if openErr != nil {
		captureStop()
		return 0, 0, errStorage
	}
	audioFile, openErr := os.OpenFile(audioPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if openErr != nil {
		captureStop()
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
	p.captureMu.Lock()
	if p.capture != nil {
		p.captureMu.Unlock()
		captureStop()
		_ = videoFile.Close()
		_ = audioFile.Close()
		_ = os.Remove(videoPath)
		_ = os.Remove(audioPath)
		return 0, 0, errBrowserUnavailable
	}
	p.capture = capture
	p.captureCtx = captureCtx
	p.captureStop = captureStop
	p.metrics = metrics
	p.captureMu.Unlock()
	completed := false
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
	phaseStart = time.Now()
	if prepareErr := p.Prepare(ctx, id, videoFormat); prepareErr != nil {
		metrics.addPhase(&metrics.prepare, time.Since(phaseStart))
		return 0, 0, prepareErr
	}
	metrics.addPhase(&metrics.prepare, time.Since(phaseStart))
	maximumDuration := videoDuration
	if audioDuration > maximumDuration {
		maximumDuration = audioDuration
	}
	videoLength := videoFormat.ContentLength
	for _, candidate := range videoCandidates {
		if candidate != nil && candidate.ContentLength > videoLength {
			videoLength = candidate.ContentLength
		}
	}
	maximum := browserCaptureTimeout(maximumDuration, videoLength+audioFormat.ContentLength)
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
	for {
		select {
		case captureErr := <-capture.done:
			if captureErr != nil {
				if os.Getenv("YTDL_TRACE_SABR") != "" {
					log.Printf("dual browser capture ended with error: %v", captureErr)
				}
				return 0, 0, errBrowserUnavailable
			}
			detach()
			capture.waitHandlers()
			phaseStart = time.Now()
			videoResult, err = videoCapture.finish()
			if err == nil {
				audioResult, err = audioCapture.finish()
			}
			metrics.addPhase(&metrics.finish, time.Since(phaseStart))
			if err != nil {
				return videoResult, audioResult, errBrowserUnavailable
			}
			completed = true
			return videoResult, audioResult, nil
		case <-ticker.C:
			now := time.Now()
			videoStalled := !videoCapture.isComplete() && now.Sub(time.Unix(0, lastVideoProgress.Load())) >= browserStreamIdleTimeout
			audioStalled := !audioCapture.isComplete() && now.Sub(time.Unix(0, lastAudioProgress.Load())) >= browserStreamIdleTimeout
			if videoStalled || audioStalled {
				if os.Getenv("YTDL_TRACE_SABR") != "" {
					log.Printf("dual browser capture stalled video=%t audio=%t idle_timeout=%s", videoStalled, audioStalled, browserStreamIdleTimeout)
				}
				return 0, 0, errBrowserUnavailable
			}
			if runErr := chromedp.Run(p.ctx, chromedp.Evaluate(keepAlive, nil)); runErr != nil {
				if os.Getenv("YTDL_TRACE_SABR") != "" {
					log.Printf("dual browser keep-alive failed: %v", runErr)
				}
				return 0, 0, errBrowserUnavailable
			}
		case <-timer.C:
			return 0, 0, errBrowserUnavailable
		case <-ctx.Done():
			return 0, 0, ctx.Err()
		}
	}
}

// browserCaptureTimeout allows for both accelerated playback and the selected
// track's actual transfer volume. Long 4K streams can require more wall time
// than duration/4 even while Chrome keeps playback at 16x.
func browserCaptureTimeout(durationMs, contentLength int64) time.Duration {
	maximum := time.Duration(durationMs)*time.Millisecond/4 + 2*time.Minute
	// SABR's Fetch response can be delivered as a long-lived 4K transfer rather
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
	if isSABRMediaURL(paused.Request.URL) {
		p.captureMu.Lock()
		capture := p.capture
		captureCtx := p.captureCtx
		metrics := p.metrics
		if capture != nil && captureCtx != nil {
			capture.addHandler()
		}
		p.captureMu.Unlock()
		if capture != nil && captureCtx != nil {
			go p.captureSABRResponse(paused, capture, captureCtx, metrics)
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

func (p *chromeBrowserProvider) registerReplayBody(status int, headers []*fetch.HeaderEntry, body []byte) (string, string, error) {
	p.replayMu.Lock()
	defer p.replayMu.Unlock()
	if p.replayServer == nil {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return "", "", err
		}
		mux := http.NewServeMux()
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodOptions {
				w.Header().Set("Access-Control-Allow-Origin", "*")
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "*")
				w.Header().Set("Access-Control-Allow-Private-Network", "true")
				w.WriteHeader(http.StatusNoContent)
				return
			}
			token := strings.TrimPrefix(r.URL.Path, "/")
			p.replayMu.Lock()
			replay, ok := p.replayBodies[token]
			if ok {
				delete(p.replayBodies, token)
			}
			p.replayMu.Unlock()
			if !ok {
				http.NotFound(w, r)
				return
			}
			for _, header := range replay.headers {
				name := http.CanonicalHeaderKey(header.Name)
				if name == "Content-Length" || name == "Connection" || name == "Transfer-Encoding" || name == "Access-Control-Allow-Origin" {
					continue
				}
				w.Header().Add(name, header.Value)
			}
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Private-Network", "true")
			w.Header().Set("Content-Length", strconv.Itoa(len(replay.body)))
			w.WriteHeader(replay.status)
			_, _ = w.Write(replay.body)
		})
		server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
		p.replayServer = server
		p.replayAddr = listener.Addr().String()
		go func() { _ = server.Serve(listener) }()
	}
	var random [24]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", "", err
	}
	token := hex.EncodeToString(random[:])
	p.replayBodies[token] = browserReplayBody{status: status, headers: headers, body: body}
	return "http://" + p.replayAddr + "/" + token, token, nil
}

func (p *chromeBrowserProvider) removeReplayBody(token string) {
	p.replayMu.Lock()
	delete(p.replayBodies, token)
	p.replayMu.Unlock()
}

func (p *chromeBrowserProvider) captureSABRResponse(paused *fetch.EventRequestPaused, capture sabrResponseCapture, captureCtx context.Context, metrics *browserCaptureMetrics) {
	defer capture.doneHandler()
	if paused.ResponseStatusCode < 200 || paused.ResponseStatusCode >= 300 || paused.ResponseErrorReason != "" {
		if capture.isTrace() {
			log.Printf("SABR response rejected status=%d error=%q", paused.ResponseStatusCode, paused.ResponseErrorReason)
		}
		p.continuePaused(paused)
		capture.fail(errBrowserUnavailable)
		return
	}
	select {
	case p.sabrReplay <- struct{}{}:
		defer func() { <-p.sabrReplay }()
	case <-captureCtx.Done():
		p.continuePaused(paused)
		capture.fail(captureCtx.Err())
		return
	}
	phaseStart := time.Now()
	streamHandle, streamErr := fetch.TakeResponseBodyAsStream(paused.RequestID).Do(captureCtx)
	setupDuration := time.Since(phaseStart)
	if streamErr != nil {
		_ = fetch.FailRequest(paused.RequestID, network.ErrorReasonFailed).Do(captureCtx)
		if capture.isTrace() {
			log.Printf("SABR response stream setup failed: %v", streamErr)
		}
		capture.fail(streamErr)
		return
	}
	readCtx, readCancel := context.WithCancel(captureCtx)
	stream := &cdpBrowserStream{handle: streamHandle, readCtx: readCtx, readCancel: readCancel, browserCtx: captureCtx}
	phaseStart = time.Now()
	body, consumeErr := io.ReadAll(io.LimitReader(stream, maxSABRRelayBytes+1))
	if consumeErr == nil && int64(len(body)) > maxSABRRelayBytes {
		consumeErr = errLimit
	}
	if consumeErr == nil {
		consumeErr = capture.consumeReader(bytes.NewReader(body))
	}
	streamDuration := time.Since(phaseStart)
	var resumeDuration time.Duration
	if capture.isTrace() {
		log.Printf("SABR response status=%d bytes=%d stream=%s", paused.ResponseStatusCode, len(body), streamDuration.Round(time.Millisecond))
	}
	if consumeErr != nil {
		if commandCtx, contextErr := p.targetContext(); contextErr == nil {
			_ = fetch.FailRequest(paused.RequestID, network.ErrorReasonFailed).Do(commandCtx)
		}
		_ = stream.Close()
		if capture.isTrace() {
			log.Printf("SABR consume failed: %v", consumeErr)
		}
		capture.fail(consumeErr)
	} else {
		phaseStart = time.Now()
		replayCtx, replayContextErr := p.targetContext()
		fulfillErr := replayContextErr
		var replayURL, replayToken string
		if fulfillErr == nil {
			replayURL, replayToken, fulfillErr = p.registerReplayBody(int(paused.ResponseStatusCode), paused.ResponseHeaders, body)
		}
		if fulfillErr == nil {
			fulfillErr = fetch.FulfillRequest(paused.RequestID, 307).
				WithResponseHeaders([]*fetch.HeaderEntry{{Name: "Location", Value: replayURL}, {Name: "Content-Length", Value: "1"}, {Name: "Cache-Control", Value: "no-store"}}).
				WithBody(base64.StdEncoding.EncodeToString([]byte{0})).
				Do(replayCtx)
			if fulfillErr != nil {
				p.removeReplayBody(replayToken)
			}
		}
		resumeDuration += time.Since(phaseStart)
		closeErr := stream.Close()
		if fulfillErr != nil {
			if capture.isTrace() {
				log.Printf("SABR response replay failed: %v capture_context=%v provider_context=%v", fulfillErr, captureCtx.Err(), p.ctx.Err())
			}
			capture.fail(fulfillErr)
		} else if capture.isTrace() {
			log.Printf("SABR response replayed through loopback bytes=%d", len(body))
			if closeErr != nil {
				log.Printf("SABR response stream close failed after replay: %v", closeErr)
			}
		}
	}
	if metrics != nil {
		// Streaming combines CDP transfer and UMP parsing in one phase; setup and
		// close remain separate so slow large responses are visible in telemetry.
		metrics.recordResponse(int64(len(body)), setupDuration, streamDuration, resumeDuration)
	}
}

const maxSABRRelayBytes int64 = 128 * 1024 * 1024

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

// browserPlaybackRate speeds up playback without overloading Chrome while its
// response bodies are being replayed through the media pipeline.
const browserPlaybackRate = 1

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
