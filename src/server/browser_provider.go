// browser_provider.go manages pooled headless Chrome tabs and captures the
// selected adaptive media track when direct streams are insufficient.
package main

import (
	"context"
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

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/fetch"
	cdpio "github.com/chromedp/cdproto/io"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
	"github.com/kkdai/youtube/v2"
)

var errBrowserUnavailable = errors.New("browser-assisted media authorization unavailable")

type browserMediaProvider interface {
	CaptureTrack(context.Context, string, *youtube.Format, string, int64, func(int64)) (int64, error)
	Close() error
}

type browserRetryProvider interface {
	Reopen(context.Context) (browserMediaProvider, error)
}

// browserDualTrackProvider is deliberately optional so existing test and
// alternate providers retain the serial CaptureTrack contract.
type browserDualTrackProvider interface {
	CaptureTracks(context.Context, string, *youtube.Format, []*youtube.Format, *youtube.Format, string, string, int64, int64, func(int64, int64)) (int64, int64, error)
}

type browserProviderFactory func(context.Context) (browserMediaProvider, error)

type chromeBrowserProvider struct {
	ctx                 context.Context
	cancel              context.CancelFunc
	allocCancel         context.CancelFunc
	release             func()
	reopen              browserProviderFactory
	closeOnce           sync.Once
	fetchOnce           sync.Once
	fetchErr            error
	uaOnce              sync.Once
	uaErr               error
	adFilterOnce        sync.Once
	adFilterErr         error
	openMu              sync.Mutex
	networkOnce         sync.Once
	networkErr          error
	networkMu           sync.Mutex
	networkSABR         map[network.RequestID]*networkSABRResponse
	networkDone         map[network.RequestID]networkCompletion
	networkEarlyData    map[network.RequestID]networkByteCounts
	pendingMu           sync.Mutex
	pending             *browserRangeCapture
	captureMu           sync.Mutex
	capture             sabrResponseCapture
	captureCtx          context.Context
	captureStop         context.CancelFunc
	metrics             *browserCaptureMetrics
	playbackRate        int
	playbackQuality     string
	playbackDurationMs  int64
	playbackVideoID     string
	lastNetworkProgress atomic.Int64
	checkpoints         sabrCheckpointIO
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
	started := time.Now()
	allocCtx, allocCancel := chromedp.NewExecAllocator(parent, chromeBrowserOptions(executable)...)
	browserCtx, cancel := chromedp.NewContext(allocCtx)
	// ChromeDP 0.19 ties the first Do call's context lifetime to Chrome itself.
	// Bound startup by cancelling the persistent browser context on timeout
	// rather than passing a disposable timeout context to the first Do.
	stopStartupTimeout := time.AfterFunc(browserPoolStartTimeout, cancel)
	err := chromedp.Do(browserCtx, chromedp.Navigate("about:blank"))
	stopStartupTimeout.Stop()
	if err != nil {
		log.Printf("browser launch failed elapsed=%s error=%v", time.Since(started).Round(time.Millisecond), err)
		cancel()
		allocCancel()
		return nil, nil, nil, err
	}
	return browserCtx, cancel, allocCancel, nil
}

func (p *chromeBrowserProvider) Reopen(ctx context.Context) (browserMediaProvider, error) {
	if p.reopen == nil || ctx.Err() != nil {
		return nil, errBrowserUnavailable
	}
	return p.reopen(ctx)
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
	failPrepare := func(stage string, err error) error {
		if os.Getenv("YTDL_TRACE_PERFORMANCE") != "" {
			log.Printf("browser prepare failed stage=%s error=%v", stage, err)
		}
		return errBrowserUnavailable
	}
	videoURL := browserWatchURL(id)
	p.playbackVideoID = id
	p.playbackRate = browserPlaybackRate
	if format.Height >= 2160 {
		p.playbackRate = 1
	}
	quality := browserPlaybackQuality(format)
	p.playbackQuality = quality
	p.playbackDurationMs, _ = strconv.ParseInt(format.ApproxDurationMs, 10, 64)
	mainPlayback := browserMainPlaybackScript(p.playbackDurationMs)
	configureQualityScript := `(() => {
  const labels = ['Accept all', 'I agree', 'Agree'];
  for (const button of document.querySelectorAll('button')) {
    const text = (button.innerText || '').trim();
    if (labels.some(label => text === label || text.includes(label))) button.click();
  }
  const player = document.getElementById('movie_player');
  const video = document.querySelector('video');
  if (player && '` + quality + `' !== '') {
    try { player.setPlaybackQualityRange('` + quality + `', '` + quality + `'); } catch (_) {}
    try { player.setPlaybackQuality('` + quality + `'); } catch (_) {}
  }
  if (video) { video.pause(); video.muted = true; }
  return !!video && video.readyState >= HTMLMediaElement.HAVE_METADATA;
})()`
	playScript := `(() => {
  const player = document.getElementById('movie_player');
  const video = document.querySelector('video');
  if (!video) return false;
  if (player && '` + quality + `' !== '') {
    try { player.setPlaybackQualityRange('` + quality + `', '` + quality + `'); } catch (_) {}
    try { player.setPlaybackQuality('` + quality + `'); } catch (_) {}
  }
  video.muted = true;
  const rate = ` + mainPlayback + ` ? ` + strconv.Itoa(p.playbackRate) + ` : 1;
  if (video.playbackRate !== rate) video.playbackRate = rate;
  if (player && typeof player.playVideo === 'function') player.playVideo();
  else { try { video.play(); } catch (_) {} }
  clearInterval(window.__ytdlKeepAlive);
  window.__ytdlKeepAlive = setInterval(() => {
    const video = document.querySelector('video');
    const player = document.getElementById('movie_player');
    if (!video) return;
    video.muted = true;
    const rate = ` + mainPlayback + ` ? ` + strconv.Itoa(p.playbackRate) + ` : 1;
    if (video.playbackRate !== rate) video.playbackRate = rate;
    if (video.paused && !video.ended) {
      if (player && typeof player.playVideo === 'function') player.playVideo();
      else video.play().catch(() => {});
    }
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
	if _, err := chromedp.Call(prepareCtx, network.Enable, browserNetworkParams()); err != nil {
		return failPrepare("network", err)
	}
	if err := p.preparePlayback(prepareCtx, videoURL, configureQualityScript, playScript, playingScript); err != nil {
		return failPrepare("playback", err)
	}
	return nil
}

func browserWatchURL(id string) string {
	query := url.Values{"v": []string{id}, "hl": []string{"en"}}
	return "https://www.youtube.com/watch?" + query.Encode()
}

// Player quality names describe the representation, not its cropped pixel
// height. A 1920x816 film can be labelled hd1080; hd816 is not a player quality.
func browserPlaybackQuality(format *youtube.Format) string {
	if format == nil {
		return ""
	}
	if kind, _, ok := formatType(format); ok && strings.HasPrefix(kind, "audio/") && format.Height == 0 && format.Width == 0 {
		// Browser fallback still needs a playing video element. Minimize its
		// discarded video traffic without changing the requested audio track.
		return "tiny"
	}
	if format.Height <= 0 {
		return ""
	}
	valid := map[string]bool{"tiny": true, "small": true, "medium": true, "large": true, "hd720": true, "hd1080": true, "hd1440": true, "hd2160": true, "highres": true}
	if valid[format.Quality] {
		return format.Quality
	}
	height := format.Height
	if label, _, ok := strings.Cut(format.QualityLabel, "p"); ok {
		if nominal, err := strconv.Atoi(label); err == nil && nominal > 0 {
			height = nominal
		}
	}
	switch {
	case height <= 144:
		return "tiny"
	case height <= 240:
		return "small"
	case height <= 360:
		return "medium"
	case height <= 480:
		return "large"
	case height <= 720:
		return "hd720"
	case height <= 1080:
		return "hd1080"
	case height <= 1440:
		return "hd1440"
	case height <= 2160:
		return "hd2160"
	default:
		return "highres"
	}
}

func (p *chromeBrowserProvider) prepareCapture(id string, format *youtube.Format, metrics *browserCaptureMetrics, paired ...*youtube.Format) error {
	phaseStart := time.Now()
	if err := p.ensureNetworkCapture(); err != nil {
		metrics.addPhase(&metrics.fetchSetup, time.Since(phaseStart))
		if os.Getenv("YTDL_TRACE_SABR") != "" {
			log.Printf("browser network capture setup failed: %v", err)
		}
		return errBrowserUnavailable
	}
	if err := p.ensurePlayerDataFilter(); err != nil {
		return errBrowserUnavailable
	}
	// A retry must not inherit buffered media or in-flight responses from the
	// previous playback. Every new capture requires its own initialization.
	if err := chromedp.Do(p.ctx, chromedp.Navigate("about:blank")); err != nil {
		return errBrowserUnavailable
	}
	if _, err := chromedp.Call(p.ctx, network.SetCacheDisabled, network.SetCacheDisabledParams{CacheDisabled: true}); err != nil {
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
	if err := p.configurePlaybackCodec(format, paired...); err != nil {
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
	p.lastNetworkProgress.Store(time.Now().UnixNano())
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

func browserMainPlaybackScript(expectedDurationMs int64) string {
	seconds := strconv.FormatFloat(float64(expectedDurationMs)/1000, 'f', 3, 64)
	return `(!(player && player.classList && player.classList.contains('ad-showing')) && (` + seconds + ` <= 0 || (Number.isFinite(video.duration) && Math.abs(video.duration - ` + seconds + `) <= 3)))`
}

func browserCapturePlaybackScript(verifiedMs int64, rate int, quality string, expectedDurationMs ...int64) string {
	expected := int64(0)
	if len(expectedDurationMs) > 0 {
		expected = expectedDurationMs[0]
	}
	return `(() => {
  const video = document.querySelector('video');
  if (!video) return false;
  video.muted = true;
  const player = document.getElementById('movie_player');
  const playerState = player && typeof player.getPlayerState === 'function' ? player.getPlayerState() : null;
  const needsPlayerStart = playerState === -1 || playerState === 2 || playerState === 5;
  const quality = ` + strconv.Quote(quality) + `;
  if (player && quality && (typeof player.getPlaybackQuality !== 'function' || player.getPlaybackQuality() !== quality)) {
    try { player.setPlaybackQualityRange(quality, quality); } catch (_) {}
    try { player.setPlaybackQuality(quality); } catch (_) {}
  }
  if (!` + browserMainPlaybackScript(expected) + `) {
    // Use the player's normal enabled Skip control when it is presented.
    // Hidden/disabled countdown controls must not be activated.
    const skips = document.querySelectorAll('.ytp-ad-skip-button, .ytp-ad-skip-button-modern, .ytp-skip-ad-button');
    for (const skip of skips) {
      if (skip.disabled || skip.getAttribute('aria-disabled') === 'true' || !skip.getClientRects().length || typeof skip.click !== 'function') continue;
      const style = getComputedStyle(skip);
      if (style.visibility === 'hidden' || style.display === 'none' || style.pointerEvents === 'none') continue;
      skip.click();
      window.__ytdlAdSkipClicks = (window.__ytdlAdSkipClicks || 0) + 1;
      // A click can leave the next ad or transition paused. Still apply the
      // normal player resume policy below instead of repeatedly clicking it.
      break;
    }
    // Let prerolls play normally. Their duration and buffer are unrelated to
    // the verified requested-media prefix, so never seek or accelerate them.
    if (video.playbackRate !== 1) video.playbackRate = 1;
    if ((video.paused && !video.ended) || (video.ended && needsPlayerStart)) {
      if (player && typeof player.playVideo === 'function') player.playVideo();
      else if (!video.ended) video.play().catch(() => {});
    }
    return true;
  }
  if (video.playbackRate !== ` + strconv.Itoa(rate) + `) video.playbackRate = ` + strconv.Itoa(rate) + `;
  const verified = ` + strconv.FormatFloat(float64(verifiedMs)/1000, 'f', 3, 64) + `;
  // Rendering can stall even while media is arriving. Advance only inside
  // the prefix already verified and saved for every requested track.
  const target = verified - 2;
  if (target > video.currentTime + 5) {
    if (typeof window !== 'undefined' && (window.__ytdlRestoredPrefix || 0) >= target) {
      // The prefix is verified on disk after a checkpoint restore. A seek
      // need not wait for the browser to download that same prefix again.
      if (player && typeof player.seekTo === 'function') player.seekTo(target, true);
      else video.currentTime = target;
      window.__ytdlRestoredPrefix = 0;
    } else {
    for (let i = video.buffered.length - 1; i >= 0; i--) {
      // An audio-only capture may run ahead of the browser's video buffer.
      // Advance to their overlap rather than waiting for video to catch up.
      const bufferedTarget = Math.min(target, video.buffered.end(i) - 2);
      if (bufferedTarget > video.currentTime + 5 && video.buffered.start(i) <= bufferedTarget) {
        if (player && typeof player.seekTo === 'function') player.seekTo(bufferedTarget, true);
        else video.currentTime = bufferedTarget;
        break;
      }
    }
    }
  }
  // An ended preroll can still occupy the video element while the requested
  // video is unstarted/cued in the player. Resume the player without replaying
  // an ended media element or restarting a completed main video (state 0).
  if ((video.ended && needsPlayerStart) || (video.paused && !video.ended)) {
    if (player && typeof player.playVideo === 'function') player.playVideo();
    else if (!video.ended) video.play().catch(() => {});
  }
  return !video.ended;
})()`
}

// Preroll playback can advance before the requested track receives another
// byte. This is liveness only: the capture deadline and SABR completeness
// checks still bound the operation and decide whether output can be published.
type browserPrerollProgress struct {
	position float64
	duration float64
	advanced time.Time
}

func (progress *browserPrerollProgress) observe(now time.Time, ad bool, position, duration float64) bool {
	if !ad {
		*progress = browserPrerollProgress{}
		return false
	}
	if duration != progress.duration {
		progress.position = 0
		progress.duration = duration
	}
	if position > progress.position {
		progress.position = position
		progress.advanced = now
	}
	return !progress.advanced.IsZero() && now.Sub(progress.advanced) < browserStreamIdleTimeout
}

func (p *chromeBrowserProvider) waitForCapture(ctx context.Context, capture sabrResponseCapture, captureDone <-chan error, detach func(), metrics *browserCaptureMetrics, maximum time.Duration, stalled func() bool, finish func() error) error {
	timer := time.NewTimer(maximum)
	defer timer.Stop()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	waitStart := time.Now()
	defer func() { metrics.addPhase(&metrics.wait, time.Since(waitStart)) }()
	ticks := 0
	var preroll browserPrerollProgress
	var lastRepair time.Time
	repairCursor := int64(-1)
	repairAttempts := 0
	lastVerified := int64(-1)
	var lastMonitor time.Time
	for {
		select {
		case captureErr := <-captureDone:
			if captureErr != nil {
				if os.Getenv("YTDL_TRACE_SABR") != "" {
					log.Printf("browser capture ended with error: %v", captureErr)
				}
				if errors.Is(captureErr, errStorage) || errors.Is(captureErr, errItemReservation) || errors.Is(captureErr, errLength) {
					return captureErr
				}
				return errBrowserUnavailable
			}
			detach()
			capture.waitHandlers()
			phaseStart := time.Now()
			if err := finish(); err != nil {
				metrics.addPhase(&metrics.finish, time.Since(phaseStart))
				if errors.Is(err, errStorage) || errors.Is(err, errItemReservation) {
					return err
				}
				return errBrowserUnavailable
			}
			metrics.addPhase(&metrics.finish, time.Since(phaseStart))
			return nil
		case <-ticker.C:
			ticks++
			if recovery, ok := capture.(interface{ recoveryPositionMs() (int64, bool) }); ok {
				position, needed := recovery.recoveryPositionMs()
				if needed && (lastRepair.IsZero() || time.Since(lastRepair) >= 15*time.Second) {
					if position != repairCursor {
						repairCursor = position
						repairAttempts = 0
					}
					if repairAttempts >= 3 {
						return errBrowserUnavailable
					}
					// A seek within the player's existing buffer need not issue a
					// request. Escalate an unsuccessful seek to a bounded reload.
					reload := repairAttempts > 0
					if initialization, ok := capture.(interface{ recoveryNeedsReload() bool }); ok {
						reload = reload || initialization.recoveryNeedsReload()
					}
					seeked, seekErr := chromedp.Run(p.ctx, chromedp.Evaluate[bool](browserRepairPlaybackScript(position, p.playbackDurationMs, p.playbackVideoID, reload)))
					if seekErr != nil {
						return errBrowserUnavailable
					}
					if seeked {
						repairAttempts++
						lastRepair = time.Now()
						if os.Getenv("YTDL_TRACE_PERFORMANCE") != "" {
							log.Printf("browser refetching missing media from verified prefix position_ms=%d attempt=%d reload=%t", position, repairAttempts, reload)
						}
					}
				}
			}
			if ticks%40 == 0 {
				p.tracePlaybackState()
			}
			// A large SABR response can still be receiving bytes before its
			// complete fragments reach the assembler. Do not call that an idle
			// transfer; the overall capture deadline remains the hard bound.
			verified := capture.verifiedDurationMs()
			// React promptly to saved media without increasing idle CDP traffic.
			// Seeking still uses only the verified overlap of all requested tracks.
			if verified == lastVerified && time.Since(lastMonitor) < 2*time.Second {
				continue
			}
			lastVerified, lastMonitor = verified, time.Now()
			rate := p.playbackRate
			if rate < 1 {
				rate = browserPlaybackRate
			}
			playbackScript := browserCapturePlaybackScript(verified, rate, p.playbackQuality, p.playbackDurationMs)
			monitorScript := `(() => { const player=document.getElementById('movie_player'); const source=player && typeof player.getVideoData==='function' ? player.getVideoData()?.video_id : ''; const wrongSource=!!source && source!==` + strconv.Quote(p.playbackVideoID) + ` && !(player.classList && player.classList.contains('ad-showing')); if(wrongSource) return {wrongSource:true}; ` + playbackScript + `; const video=document.querySelector('video'); return {ad:!!video && !` + browserMainPlaybackScript(p.playbackDurationMs) + `, position:video?.currentTime || 0, duration:Number.isFinite(video?.duration) ? video.duration : 0}; })()`
			state, err := chromedp.Run(p.ctx, chromedp.Evaluate[struct {
				WrongSource bool    `json:"wrongSource"`
				Ad          bool    `json:"ad"`
				Position    float64 `json:"position"`
				Duration    float64 `json:"duration"`
			}](monitorScript))
			if err != nil {
				if os.Getenv("YTDL_TRACE_SABR") != "" {
					log.Printf("browser keep-alive failed: %v", err)
				}
				return errBrowserUnavailable
			}
			if state.WrongSource {
				if lastRepair.IsZero() || time.Since(lastRepair) >= 15*time.Second {
					position := max(0, verified-2000)
					if position != repairCursor {
						repairCursor, repairAttempts = position, 0
					}
					if repairAttempts >= 3 {
						return errBrowserUnavailable
					}
					restored, restoreErr := chromedp.Run(p.ctx, chromedp.Evaluate[bool](browserRestoreRequestedVideoScript(position, p.playbackVideoID, p.playbackQuality)))
					if restoreErr != nil {
						return errBrowserUnavailable
					}
					if restored {
						repairAttempts++
						lastRepair = time.Now()
						if os.Getenv("YTDL_TRACE_PERFORMANCE") != "" {
							log.Printf("browser restoring requested video position_ms=%d attempt=%d", position, repairAttempts)
						}
					}
				}
				continue
			}
			adAdvancing := preroll.observe(time.Now(), state.Ad, state.Position, state.Duration)
			if stalled() && time.Since(time.Unix(0, p.lastNetworkProgress.Load())) >= browserStreamIdleTimeout && !adAdvancing {
				p.tracePlaybackState()
				if os.Getenv("YTDL_TRACE_SABR") != "" {
					log.Printf("browser capture stalled without media or preroll progress for %s", browserStreamIdleTimeout)
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
	file, state, err := p.openCaptureFile(path, format)
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
	capture.expectedVideoID = id
	if err := p.configureCaptureCheckpoint(capture, path, format, state); err != nil {
		_ = file.Close()
		return 0, err
	}
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
		if !completed && p.checkpoints == nil {
			_ = os.Remove(path)
		}
		if !completed && p.checkpoints != nil && (errors.Is(err, errLength) || errors.Is(err, errItemReservation)) {
			_ = p.checkpoints.Discard(path)
		}
	}()
	if capture.isComplete() {
		completed = true
		return capture.totalWritten, nil
	}
	phaseStart := time.Now()
	if err := p.Prepare(ctx, id, format); err != nil {
		metrics.addPhase(&metrics.prepare, time.Since(phaseStart))
		return 0, err
	}
	metrics.addPhase(&metrics.prepare, time.Since(phaseStart))
	if capture.restored {
		if err := p.seedRestoredPrefix(capture.verifiedDurationMs()); err != nil {
			return 0, err
		}
	}
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

// CaptureTracks captures the selected video and compatible audio from one
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
	if err := p.prepareCapture(id, videoFormat, metrics, audioFormat); err != nil {
		return 0, 0, err
	}
	videoFile, videoState, err := p.openCaptureFile(videoPath, videoFormat)
	if err != nil {
		return 0, 0, errStorage
	}
	audioFile, audioState, err := p.openCaptureFile(audioPath, audioFormat)
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
	videoCapture.expectedVideoID = id
	audioCapture := newSABRCapture(audioFile, audioFormat.ItagNo, audioDuration, audioBudget, func(value int64) {
		lastAudioProgress.Store(time.Now().UnixNano())
		progressMu.Lock()
		audioCurrent = value
		progress(videoCurrent, audioCurrent)
		progressMu.Unlock()
	})
	audioCapture.expectedVideoID = id
	if err := p.configureCaptureCheckpoint(videoCapture, videoPath, videoFormat, videoState); err != nil {
		_ = videoFile.Close()
		_ = audioFile.Close()
		return 0, 0, err
	}
	if err := p.configureCaptureCheckpoint(audioCapture, audioPath, audioFormat, audioState); err != nil {
		_ = videoFile.Close()
		_ = audioFile.Close()
		return 0, 0, err
	}
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
		if !completed && p.checkpoints == nil {
			_ = os.Remove(videoPath)
			_ = os.Remove(audioPath)
		}
		if !completed && p.checkpoints != nil && (errors.Is(err, errLength) || errors.Is(err, errItemReservation)) {
			_ = p.checkpoints.Discard(videoPath)
			_ = p.checkpoints.Discard(audioPath)
		}
	}()
	if videoCapture.isComplete() && audioCapture.isComplete() {
		completed = true
		return videoCapture.totalWritten, audioCapture.totalWritten, nil
	}
	phaseStart := time.Now()
	if err := p.Prepare(ctx, id, videoFormat); err != nil {
		metrics.addPhase(&metrics.prepare, time.Since(phaseStart))
		return 0, 0, err
	}
	metrics.addPhase(&metrics.prepare, time.Since(phaseStart))
	if videoCapture.restored || audioCapture.restored {
		if err := p.seedRestoredPrefix(capture.verifiedDurationMs()); err != nil {
			return 0, 0, err
		}
	}
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

// browserCaptureTimeout allows for real-time playback and transfer volume.
// The requested 4x rate is not a wall-clock guarantee: the player can buffer
// or throttle rendering while verified fragments are still arriving.
func browserCaptureTimeout(durationMs, contentLength int64) time.Duration {
	maximum := time.Duration(durationMs)*time.Millisecond + 2*time.Minute
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
	if maximum > maximumItemTimeout {
		return maximumItemTimeout
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

		if err := chromedp.Do(p.ctx, chromedp.Navigate("http://"+listener.Addr().String()+"/")); err != nil {
			p.uaErr = err
			return
		}
		raw, err := chromedp.Run(p.ctx, chromedp.Evaluate[string](metadataScript, chromedp.EvalAwaitPromise))
		if err != nil {
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

		_, p.uaErr = chromedp.Call(execCtx, emulation.SetUserAgentOverride, emulation.SetUserAgentOverrideParams{
			UserAgent: userAgent, AcceptLanguage: "en-US,en;q=0.9",
			UserAgentMetadata: &emulation.UserAgentMetadata{
				Brands: brands, FullVersionList: fullVersions,
				Platform: metadata.Platform, PlatformVersion: metadata.Hints.PlatformVersion,
				Architecture: metadata.Hints.Architecture, Model: metadata.Hints.Model,
				Mobile: metadata.Mobile, Bitness: metadata.Hints.Bitness, Wow64: metadata.Hints.Wow64,
			},
		})
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

func (p *chromeBrowserProvider) configurePlaybackCodec(format *youtube.Format, paired ...*youtube.Format) error {
	policy, ok := playbackPolicy(format)
	if !ok {
		return errBrowserUnavailable
	}
	for _, audio := range paired {
		audioPolicy, valid := playbackPolicy(audio)
		if !valid {
			return errBrowserUnavailable
		}
		policy.unsupportedPattern = "(?:" + policy.unsupportedPattern + "|" + audioPolicy.unsupportedPattern + ")"
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
	_, err = chromedp.Call(execCtx, page.AddScriptToEvaluateOnNewDocument, page.AddScriptToEvaluateOnNewDocumentParams{Source: script, RunImmediately: new(true)})
	return err
}

func (p *chromeBrowserProvider) ensureFetch() error {
	p.fetchOnce.Do(func() {

		events := chromedp.Events(p.ctx, fetch.RequestPaused)
		go func() {
			for event, err := range events {
				if err != nil {
					return
				}
				p.handleFetchEvent(&event)
			}
		}()
		pattern := &fetch.RequestPattern{
			URLPattern:   "*googlevideo.com/videoplayback*",
			RequestStage: fetch.RequestStageResponse,
		}
		_, p.fetchErr = chromedp.Call(p.ctx, fetch.Enable, fetch.EnableParams{Patterns: []*fetch.RequestPattern{pattern}})
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
		_, _ = chromedp.Call(ctx, fetch.ContinueResponse, fetch.ContinueResponseParams{RequestID: paused.RequestID})
	} else {
		_, _ = chromedp.Call(ctx, fetch.ContinueRequest, fetch.ContinueRequestParams{RequestID: paused.RequestID})
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

	streamResult, err := chromedp.Call(execCtx, fetch.TakeResponseBodyAsStream, fetch.TakeResponseBodyAsStreamParams{RequestID: paused.RequestID})
	if err != nil {
		_, _ = chromedp.Call(execCtx, fetch.FailRequest, fetch.FailRequestParams{RequestID: paused.RequestID, ErrorReason: network.ErrorReasonFailed})
		pending.done <- browserStreamResult{err: err}
		return
	}
	readCtx, cancel := context.WithCancel(execCtx)
	stream := &cdpBrowserStream{
		handle:     streamResult.Stream,
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
	return p.ctx, nil
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
	if err := chromedp.Do(p.ctx, chromedp.Evaluate[chromedp.Void](script)); err != nil {
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

	type playbackState struct {
		CurrentTime      float64  `json:"currentTime"`
		Duration         float64  `json:"duration"`
		Rate             float64  `json:"rate"`
		Paused           bool     `json:"paused"`
		Ended            bool     `json:"ended"`
		ReadyState       int      `json:"readyState"`
		BufferEnd        float64  `json:"bufferEnd"`
		PlayerState      int      `json:"playerState"`
		Quality          string   `json:"quality"`
		Qualities        []string `json:"qualities"`
		Width            int      `json:"width"`
		Height           int      `json:"height"`
		PlayerVideoID    string   `json:"playerVideoId"`
		AdSkipClicks     int      `json:"adSkipClicks"`
		FilteredAdFields int      `json:"filteredAdFields"`
		SkipControls     []string `json:"skipControls"`
		Videos           []struct {
			CurrentTime float64 `json:"currentTime"`
			Duration    float64 `json:"duration"`
			Paused      bool    `json:"paused"`
			Ended       bool    `json:"ended"`
			InPlayer    bool    `json:"inPlayer"`
			ClassName   string  `json:"className"`
		} `json:"videos"`
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
    quality: player?.getPlaybackQuality?.() || '',
    qualities: player?.getAvailableQualityLevels?.() || [],
    width: video.videoWidth, height: video.videoHeight,
    playerVideoId: player?.getVideoData?.()?.video_id || '',
    adSkipClicks: window.__ytdlAdSkipClicks || 0,
    filteredAdFields: window.__ytdlFilteredAdFields || 0,
    skipControls: [...document.querySelectorAll('[class*="skip-ad"], [class*="ad-skip"]')].slice(0, 12).map(b => JSON.stringify({className:b.className,disabled:!!b.disabled,ariaDisabled:b.getAttribute('aria-disabled'),rects:b.getClientRects().length,visibility:getComputedStyle(b).visibility,pointerEvents:getComputedStyle(b).pointerEvents})),
    videos: [...document.querySelectorAll('video')].map(v => ({currentTime: v.currentTime, duration: Number.isFinite(v.duration) ? v.duration : 0, paused: v.paused, ended: v.ended, inPlayer: !!v.closest('#movie_player'), className: v.className})),
  } : null;
})()`
	stateCtx, cancel := context.WithTimeout(p.ctx, 3*time.Second)
	defer cancel()

	state, err := chromedp.Run(stateCtx, chromedp.Evaluate[playbackState](script))
	if err != nil {
		log.Printf("browser playback state unavailable: %v", err)
		return
	}
	log.Printf("browser playback state current=%.1f duration=%.1f buffered=%.1f rate=%.1f paused=%t ended=%t ready=%d player=%d quality=%s levels=%v dimensions=%dx%d", state.CurrentTime, state.Duration, state.BufferEnd, state.Rate, state.Paused, state.Ended, state.ReadyState, state.PlayerState, state.Quality, state.Qualities, state.Width, state.Height)
	log.Printf("browser playback elements source_video=%q ad_skip_clicks=%d videos=%+v", state.PlayerVideoID, state.AdSkipClicks, state.Videos)
	log.Printf("browser skip controls=%v", state.SkipControls)
	log.Printf("browser player-data filtered_ad_fields=%d", state.FilteredAdFields)
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

		response, err := chromedp.Call(readCtx, cdpio.Read, cdpio.ReadParams{Handle: s.handle})
		if err != nil {
			return 0, err
		}
		if response.EOF {
			s.eof = true
		}
		if len(response.Data) == 0 {
			continue
		}
		n := copy(dst, response.Data)
		if n < len(response.Data) {
			s.buffer = append(s.buffer, response.Data[n:]...)
		}
		return n, nil
	}
	return 0, errRead
}

func (s *cdpBrowserStream) Close() error {
	s.closeOnce.Do(func() {
		s.readCancel()
		closeCtx, cancel := context.WithTimeout(s.browserCtx, 5*time.Second)
		_, s.closeErr = chromedp.Call(closeCtx, cdpio.Close, cdpio.CloseParams{Handle: s.handle})
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
