package main

import (
	"context"
	"errors"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/dop251/goja"
	"github.com/kkdai/youtube/v2"
)

func TestBrowserPrerollProgressDoesNotRequireRequestedMediaBytes(t *testing.T) {
	var progress browserPrerollProgress
	now := time.Now()
	if !progress.observe(now, true, 4, 20) {
		t.Fatal("advancing preroll was treated as idle")
	}
	if !progress.observe(now.Add(59*time.Second), true, 5, 20) {
		t.Fatal("slow preroll progress was lost")
	}
	if progress.observe(now.Add(120*time.Second), true, 5, 20) {
		t.Fatal("stalled preroll extended the idle deadline")
	}
	if progress.observe(now.Add(121*time.Second), false, 6, 216) {
		t.Fatal("main playback substituted for verified media progress")
	}
	if progress.observe(now.Add(122*time.Second), true, 0, 20) {
		t.Fatal("stale preroll state survived main playback")
	}
}

func TestBrowserPlaybackAdvancesOnlyThroughVerifiedBufferedMedia(t *testing.T) {
	for _, test := range []struct {
		name             string
		verified         int64
		start, end, want float64
	}{
		{"audio ahead of video", 250000, 0, 100, 98},
		{"saved prefix behind video", 30000, 0, 100, 28},
		{"missing media", 0, 0, 100, 10},
		{"buffer gap", 30000, 50, 100, 10},
	} {
		t.Run(test.name, func(t *testing.T) {
			vm := goja.New()
			_ = vm.Set("bufferStart", test.start)
			_ = vm.Set("bufferEnd", test.end)
			_, err := vm.RunString(`const video = {currentTime:10, paused:false, ended:false, buffered:{length:1, start:()=>bufferStart, end:()=>bufferEnd}}; const document = {querySelectorAll:()=>[],querySelector:()=>video, getElementById:()=>({seekTo:(time)=>{video.currentTime=time;}})};`)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := vm.RunString(browserCapturePlaybackScript(test.verified, 1, "")); err != nil {
				t.Fatal(err)
			}
			value, err := vm.RunString("video.currentTime")
			if err != nil || value.ToFloat() != test.want {
				t.Fatalf("seek=%v error=%v, want %v", value, err, test.want)
			}
		})
	}
}

func TestBrowserPlaybackMaintainsRequested4KQuality(t *testing.T) {
	vm := goja.New()
	_, err := vm.RunString(`let minimum, maximum, selected; const video = {currentTime:0, paused:false, ended:false, buffered:{length:0}}; const player = {setPlaybackQualityRange:(min,max)=>{minimum=min; maximum=max;}, setPlaybackQuality:(quality)=>{selected=quality;}}; const document = {querySelectorAll:()=>[],querySelector:()=>video, getElementById:()=>player};`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := vm.RunString(browserCapturePlaybackScript(0, 1, "hd2160")); err != nil {
		t.Fatal(err)
	}
	value, err := vm.RunString("JSON.stringify([minimum,maximum,selected,video.playbackRate])")
	if err != nil || value.String() != `["hd2160","hd2160","hd2160",1]` {
		t.Fatalf("playback policy=%v error=%v", value, err)
	}
}

func TestBrowserPlaybackDoesNotResetAlreadySelectedSettings(t *testing.T) {
	vm := goja.New()
	_, err := vm.RunString(`let resets=0; const video={currentTime:0,paused:false,ended:false,buffered:{length:0},get playbackRate(){return 1;},set playbackRate(value){resets++;}}; const player={getPlaybackQuality:()=>"hd2160",setPlaybackQualityRange:()=>{resets++;},setPlaybackQuality:()=>{resets++;}}; const document={querySelectorAll:()=>[],querySelector:()=>video,getElementById:()=>player};`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := vm.RunString(browserCapturePlaybackScript(0, 1, "hd2160")); err != nil {
		t.Fatal(err)
	}
	value, err := vm.RunString("resets")
	if err != nil || value.ToInteger() != 0 {
		t.Fatalf("unchanged settings reset %v times: %v", value, err)
	}
}

func TestBrowserPlaybackStartsMainVideoAfterEndedPreroll(t *testing.T) {
	for _, state := range []int{-1, 2, 5, 0} {
		vm := goja.New()
		_ = vm.Set("playerState", state)
		_, err := vm.RunString(`let starts=0; const video={duration:20,currentTime:20,paused:true,ended:true,buffered:{length:0},play:()=>{throw new Error('ended media must not replay');}}; const player={getPlayerState:()=>playerState,playVideo:()=>{starts++;}}; const document={querySelectorAll:()=>[],querySelector:()=>video,getElementById:()=>player};`)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := vm.RunString(browserCapturePlaybackScript(0, 4, "", 216000)); err != nil {
			t.Fatal(err)
		}
		starts, _ := vm.RunString("starts")
		want := int64(1)
		if state == 0 {
			want = 0
		}
		if starts.ToInteger() != want {
			t.Fatalf("player state=%d starts=%d, want %d", state, starts.ToInteger(), want)
		}
	}
}

func TestBrowserPlaybackDoesNotSeekOrAcceleratePreroll(t *testing.T) {
	vm := goja.New()
	_, err := vm.RunString(`let seeks=0, starts=0; const video={duration:20,currentTime:5,playbackRate:1,paused:false,ended:false,buffered:{length:1,start:()=>0,end:()=>20}}; const player={getPlayerState:()=>-1,seekTo:()=>{seeks++;},playVideo:()=>{starts++;}}; const document={querySelectorAll:()=>[],querySelector:()=>video,getElementById:()=>player};`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := vm.RunString(browserCapturePlaybackScript(200000, 4, "", 216000)); err != nil {
		t.Fatal(err)
	}
	got, _ := vm.RunString("JSON.stringify([video.currentTime,video.playbackRate,seeks,starts])")
	if got.String() != "[5,1,0,0]" {
		t.Fatalf("preroll was controlled using main-video progress: %s", got.String())
	}
	if _, err := vm.RunString(`video.duration=216;`); err != nil {
		t.Fatal(err)
	}
	if _, err := vm.RunString(browserCapturePlaybackScript(200000, 4, "", 216000)); err != nil {
		t.Fatal(err)
	}
	got, _ = vm.RunString("video.playbackRate")
	if got.ToInteger() != 4 {
		t.Fatal("requested video did not receive its playback policy")
	}
	if _, err := vm.RunString(`video.playbackRate=1; video.currentTime=5; seeks=0; player.classList={contains:(name)=>name==='ad-showing'};`); err != nil {
		t.Fatal(err)
	}
	if _, err := vm.RunString(browserCapturePlaybackScript(200000, 4, "", 216000)); err != nil {
		t.Fatal(err)
	}
	got, _ = vm.RunString("JSON.stringify([video.currentTime,video.playbackRate,seeks])")
	if got.String() != "[5,1,0]" {
		t.Fatal("active preroll with a matching duration was treated as the main video")
	}
}

func TestBrowserPlaybackResumesPausedPrerollThroughPlayer(t *testing.T) {
	vm := goja.New()
	_, err := vm.RunString(`let starts=0; const video={duration:20,currentTime:5,playbackRate:1,paused:true,ended:false,buffered:{length:0},play:()=>{throw new Error('DOM playback bypasses player state');}}; const player={getPlayerState:()=>-1,playVideo:()=>{starts++;}}; const document={querySelectorAll:()=>[],querySelector:()=>video,getElementById:()=>player};`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := vm.RunString(browserCapturePlaybackScript(30000, 4, "", 216000)); err != nil {
		t.Fatal(err)
	}
	got, _ := vm.RunString("starts")
	if got.ToInteger() != 1 {
		t.Fatal("paused preroll was not resumed through the player")
	}
}

func TestBrowserPlaybackUsesOnlyEnabledVisibleSkipControl(t *testing.T) {
	for _, tc := range []struct {
		disabled, visible bool
		want              int64
	}{{false, true, 1}, {true, true, 0}, {false, false, 0}} {
		vm := goja.New()
		_ = vm.Set("disabled", tc.disabled)
		_ = vm.Set("visible", tc.visible)
		_, err := vm.RunString(`let skips=0; const window={}; const getComputedStyle=()=>({visibility:'visible',pointerEvents:'auto'}); const video={duration:20,currentTime:5,playbackRate:1,paused:false,ended:false}; const skip={getAttribute:()=>null,disabled:disabled,getClientRects:()=>visible?[{}]:[],click:()=>{skips++;}}; const document={getElementById:()=>null,querySelector:()=>video,querySelectorAll:()=>[skip]};`)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := vm.RunString(browserCapturePlaybackScript(0, 4, "", 216000)); err != nil {
			t.Fatal(err)
		}
		got, _ := vm.RunString("skips")
		if got.ToInteger() != tc.want {
			t.Fatalf("disabled=%t visible=%t skips=%d", tc.disabled, tc.visible, got.ToInteger())
		}
	}
}

func TestBrowserPlaybackFindsVisibleSkipAfterHiddenLegacyControl(t *testing.T) {
	vm := goja.New()
	_, err := vm.RunString(`let clicks=0; const window={}; const video={duration:20}; const hidden={disabled:false,getAttribute:()=>null,getClientRects:()=>[],click:()=>{throw new Error('hidden legacy control clicked');}}; const enabled={disabled:false,getAttribute:()=>null,getClientRects:()=>[{}],click:()=>{clicks++;}}; const getComputedStyle=()=>({visibility:'visible',display:'block',pointerEvents:'auto'}); const document={getElementById:()=>null,querySelector:()=>video,querySelectorAll:()=>[hidden,enabled]};`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := vm.RunString(browserCapturePlaybackScript(0, 4, "", 216000)); err != nil {
		t.Fatal(err)
	}
	got, _ := vm.RunString("clicks")
	if got.ToInteger() != 1 {
		t.Fatal("visible replacement Skip control was masked by a hidden control")
	}
}

func TestBrowserPlaybackResumesAfterSkipClickLeavesPrerollPaused(t *testing.T) {
	vm := goja.New()
	_, err := vm.RunString(`let clicks=0, starts=0; const window={}; const video={duration:30,currentTime:6,paused:true,ended:false,playbackRate:1}; const skip={disabled:false,getAttribute:()=>null,getClientRects:()=>[{}],click:()=>{clicks++;}}; const getComputedStyle=()=>({visibility:'visible',display:'block',pointerEvents:'auto'}); const player={getPlayerState:()=>-1,playVideo:()=>{starts++;}}; const document={getElementById:()=>player,querySelector:()=>video,querySelectorAll:()=>[skip]};`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := vm.RunString(browserCapturePlaybackScript(0, 4, "", 216000)); err != nil {
		t.Fatal(err)
	}
	got, _ := vm.RunString("JSON.stringify([clicks,starts,video.playbackRate])")
	if got.String() != "[1,1,1]" {
		t.Fatalf("skip did not retain normal preroll resume policy: %s", got.String())
	}
}

func TestBrowserPlaybackQualityUsesRepresentationName(t *testing.T) {
	for _, test := range []struct {
		name   string
		format *youtube.Format
		want   string
	}{
		{"cropped", &youtube.Format{Height: 816, Quality: "hd1080", QualityLabel: "1080p"}, "hd1080"},
		{"480p", &youtube.Format{Height: 480, Quality: "large"}, "large"},
		{"cropped label", &youtube.Format{Height: 1080, QualityLabel: "1440p60"}, "hd1440"},
		{"360p fallback", &youtube.Format{Height: 360}, "medium"},
		{"4K fallback", &youtube.Format{Height: 2160}, "hd2160"},
		{"AAC audio", &youtube.Format{MimeType: `audio/mp4; codecs="mp4a.40.2"`}, "tiny"},
		{"Opus audio", &youtube.Format{MimeType: `audio/webm; codecs="opus"`, Quality: "hd2160"}, "tiny"},
		{"unknown representation", &youtube.Format{Quality: "tiny"}, ""},
		{"untrusted quality", &youtube.Format{Height: 720, Quality: "');alert(1);//"}, "hd720"},
		{"nil", nil, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := browserPlaybackQuality(test.format); got != test.want {
				t.Fatalf("quality=%q, want %q", got, test.want)
			}
		})
	}
}

func TestBrowserAudioPlaybackKeepsVideoAtMinimumQuality(t *testing.T) {
	format := &youtube.Format{ItagNo: 140, MimeType: `audio/mp4; codecs="mp4a.40.2"`}
	vm := goja.New()
	_, err := vm.RunString(`let minimum, maximum, selected; const video={currentTime:0,paused:false,ended:false,buffered:{length:0}}; const player={getPlaybackQuality:()=>"hd1080",setPlaybackQualityRange:(min,max)=>{minimum=min;maximum=max;},setPlaybackQuality:(quality)=>{selected=quality;}}; const document={querySelectorAll:()=>[],querySelector:()=>video,getElementById:()=>player};`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := vm.RunString(browserCapturePlaybackScript(0, browserPlaybackRate, browserPlaybackQuality(format))); err != nil {
		t.Fatal(err)
	}
	value, err := vm.RunString("JSON.stringify([minimum,maximum,selected])")
	if err != nil || value.String() != `["tiny","tiny","tiny"]` {
		t.Fatalf("audio playback policy=%v error=%v", value, err)
	}
}

func TestBrowserReadIdleDeadlineReturnsRetryableError(t *testing.T) {
	started := time.Now()
	n, err := browserReadWithIdleDeadline(context.Background(), 25*time.Millisecond, func(ctx context.Context) (int, error) {
		<-ctx.Done()
		return 0, ctx.Err()
	})
	if n != 0 || !errors.Is(err, errRead) {
		t.Fatalf("idle read = (%d, %v), want (0, errRead)", n, err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("idle read took %s, want under 1s", elapsed)
	}
}

func TestBrowserReadIdleDeadlinePreservesParentCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := browserReadWithIdleDeadline(ctx, time.Second, func(readCtx context.Context) (int, error) {
		<-readCtx.Done()
		return 0, readCtx.Err()
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("parent cancellation = %v, want context.Canceled", err)
	}
}

func TestRangeURLReplacesOnlyRange(t *testing.T) {
	raw := "https://rr1---sn-example.googlevideo.com/videoplayback?expire=1&itag=299&range=1-2&spc=token"
	got, err := rangeURL(raw, 100, 199)
	if err != nil {
		t.Fatal(err)
	}
	want := "https://rr1---sn-example.googlevideo.com/videoplayback?expire=1&itag=299&range=100-199&spc=token"
	if got != want {
		t.Fatalf("rangeURL() = %q, want %q", got, want)
	}
}

func TestBrowserWatchURLPinsEnglishLocale(t *testing.T) {
	u, err := url.Parse(browserWatchURL("dQw4w9WgXcQ"))
	if err != nil {
		t.Fatal(err)
	}
	if u.Query().Get("v") != "dQw4w9WgXcQ" || u.Query().Get("hl") != "en" {
		t.Fatalf("browser watch URL query = %v, want requested video and English locale", u.Query())
	}
}

func TestSameBrowserRangeURLCanonicalizesQueryOrder(t *testing.T) {
	left := "https://rr1---sn-example.googlevideo.com/videoplayback?itag=299&range=0-9&spc=token"
	right := "https://rr1---sn-example.googlevideo.com/videoplayback?spc=token&range=0-9&itag=299"
	if !sameBrowserRangeURL(left, right) {
		t.Fatal("sameBrowserRangeURL() rejected equivalent query strings")
	}
	if sameBrowserRangeURL(left, "https://rr1---sn-example.googlevideo.com/videoplayback?itag=299&range=10-19&spc=token") {
		t.Fatal("sameBrowserRangeURL() accepted a different range")
	}
}

func TestTargetBrowserMediaURL(t *testing.T) {
	valid := "https://rr1---sn-example.googlevideo.com/videoplayback?expire=1&itag=299&range=0-99"
	for _, test := range []struct {
		name string
		raw  string
		want bool
	}{
		{name: "selected adaptive stream", raw: valid, want: true},
		{name: "wrong itag", raw: "https://rr1---sn-example.googlevideo.com/videoplayback?itag=140", want: false},
		{name: "wrong path", raw: "https://rr1---sn-example.googlevideo.com/watch?itag=299", want: false},
		{name: "untrusted host", raw: "https://googlevideo.com.evil.invalid/videoplayback?itag=299", want: false},
		{name: "non media host", raw: "https://youtube.com/videoplayback?itag=299", want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := isTargetBrowserMediaURL(test.raw, 299); got != test.want {
				t.Fatalf("isTargetBrowserMediaURL(%q) = %t, want %t", test.raw, got, test.want)
			}
		})
	}
}

func TestPlaybackPolicyTargetsRequestedCodecFamily(t *testing.T) {
	tests := []struct {
		name       string
		mime       string
		pattern    string
		preference string
		ok         bool
	}{
		{name: "H264 MP4", mime: `video/mp4; codecs="avc1.64002a"`, pattern: "(?:av01|av1|vp09|vp9|vp8)", preference: "480", ok: true},
		{name: "VP9 WebM", mime: `video/webm; codecs="vp09.00.51.08"`, pattern: "(?:av01|av1)", preference: "480", ok: true},
		{name: "AV1 WebM", mime: `video/webm; codecs="av01.0.12M.08"`, pattern: "(?:vp09|vp9|vp8)", ok: true},
		{name: "Opus WebM", mime: `audio/webm; codecs="opus"`, pattern: "(?:mp4a|audio\\/mp4)", ok: true},
		{name: "AAC MP4", mime: `audio/mp4; codecs="mp4a.40.2"`, pattern: "(?:opus|audio\\/webm)", ok: true},
		{name: "unsupported VP8", mime: `video/webm; codecs="vp8"`, ok: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			policy, ok := playbackPolicy(&youtube.Format{MimeType: test.mime})
			if ok != test.ok {
				t.Fatalf("playbackPolicy(%q) ok=%t, want %t", test.mime, ok, test.ok)
			}
			if !ok {
				return
			}
			if policy.unsupportedPattern != test.pattern || policy.av1Preference != test.preference {
				t.Fatalf("playbackPolicy(%q) = %+v, want pattern=%q preference=%q", test.mime, policy, test.pattern, test.preference)
			}
		})
	}
}

func TestBrowserCaptureTimeoutAccountsFor4KTransferBytes(t *testing.T) {
	if got := browserCaptureTimeout(30_000, 0); got != 4*time.Minute {
		t.Fatalf("minimum capture timeout = %s, want 4m", got)
	}
	if got := browserCaptureTimeout(15*60*1000, 0); got < 17*time.Minute {
		t.Fatalf("long video timeout = %s, must allow real-time playback plus setup", got)
	}
	// A 2.6 GiB AV1 track needs substantially more time than accelerated
	// playback duration alone when throughput is constrained.
	if got := browserCaptureTimeout(30_000, 2_600*1024*1024); got < 24*time.Minute {
		t.Fatalf("4K transfer timeout = %s, want at least 24m", got)
	}
	if got := browserCaptureTimeout(24*60*60*1000, 0); got < 24*time.Hour {
		t.Fatalf("capture timeout = %s, must allow long-form playback", got)
	}
	if got := browserCaptureTimeout(8*24*60*60*1000, 0); got != maximumItemTimeout {
		t.Fatalf("capture timeout cap = %s, want %s", got, maximumItemTimeout)
	}
}

func TestBrowserPoolReplacesDeadProcessBeforeOldLeaseCloses(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	launches := 0
	pool := &browserPool{
		ctx: ctx, idleTimeout: time.Hour,
		launch: func(parent context.Context) (context.Context, context.CancelFunc, context.CancelFunc, error) {
			launches++
			root, closeRoot := context.WithCancel(parent)
			return root, closeRoot, closeRoot, nil
		},
		newTab: func(parent context.Context) (context.Context, context.CancelFunc) { return context.WithCancel(parent) },
	}
	defer pool.Close()
	oldLease, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	pool.rootClose() // simulate a browser crash while a download owns a tab
	replacement, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer replacement.Close()
	if launches != 2 || replacement.(*chromeBrowserProvider).ctx.Err() != nil {
		t.Fatalf("dead browser was reused: launches=%d error=%v", launches, replacement.(*chromeBrowserProvider).ctx.Err())
	}
	_ = oldLease.Close()
	if pool.active != 1 || pool.idleTimer != nil {
		t.Fatalf("old lease affected replacement: active=%d idleTimer=%v", pool.active, pool.idleTimer != nil)
	}
	_ = replacement.Close()
	if pool.active != 0 || pool.idleTimer == nil {
		t.Fatal("replacement lease was not released normally")
	}
}

func TestBrowserPoolOwnsRootUntilExplicitShutdown(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool := &browserPool{ctx: parent, idleTimeout: time.Hour,
		launch: func(parent context.Context) (context.Context, context.CancelFunc, context.CancelFunc, error) {
			root, stop := context.WithCancel(parent)
			return root, stop, stop, nil
		},
		newTab: func(parent context.Context) (context.Context, context.CancelFunc) { return context.WithCancel(parent) },
	}
	lease, err := pool.Acquire(parent)
	if err != nil {
		t.Fatal(err)
	}
	root := pool.root
	cancel()
	if root.Err() != nil {
		t.Fatal("server cancellation killed root before graceful shutdown")
	}
	_ = lease.Close()
	_ = pool.Close()
	if root.Err() == nil {
		t.Fatal("pool close left its browser context active")
	}
}

func TestBrowserRootShutdownDoesNotWaitIndefinitely(t *testing.T) {
	blocked := make(chan struct{})
	finished := make(chan struct{})
	defer close(blocked)
	go func() {
		closeBrowserRoot(context.Background(), func() { <-blocked }, nil, 10*time.Millisecond)
		close(finished)
	}()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("browser shutdown blocked the service")
	}
}

func TestBrowserPoolSharesProcessAndIsolatesLeases(t *testing.T) {
	rootCtx, cancelRoot := context.WithCancel(context.Background())
	defer cancelRoot()
	var mu sync.Mutex
	launches := 0
	pool := &browserPool{
		ctx: rootCtx, idleTimeout: 25 * time.Millisecond,
		launch: func(parent context.Context) (context.Context, context.CancelFunc, context.CancelFunc, error) {
			mu.Lock()
			launches++
			mu.Unlock()
			root, cancel := context.WithCancel(parent)
			return root, cancel, cancel, nil
		},
		newTab: func(parent context.Context) (context.Context, context.CancelFunc) {
			return context.WithCancel(parent)
		},
	}

	const concurrentLeases = 6
	start := make(chan struct{})
	providers := make(chan browserMediaProvider, concurrentLeases)
	errorsCh := make(chan error, concurrentLeases)
	var wg sync.WaitGroup
	for range concurrentLeases {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			provider, err := pool.Acquire(context.Background())
			if err != nil {
				errorsCh <- err
				return
			}
			providers <- provider
		}()
	}
	close(start)
	wg.Wait()
	close(providers)
	close(errorsCh)
	for err := range errorsCh {
		t.Fatal(err)
	}
	leases := make([]*chromeBrowserProvider, 0, concurrentLeases)
	seenTabs := make(map[context.Context]bool)
	for provider := range providers {
		lease := provider.(*chromeBrowserProvider)
		if seenTabs[lease.ctx] {
			t.Fatal("concurrent leases share a tab context")
		}
		seenTabs[lease.ctx] = true
		leases = append(leases, lease)
	}
	mu.Lock()
	gotLaunches := launches
	mu.Unlock()
	if gotLaunches != 1 || len(leases) != concurrentLeases {
		t.Fatalf("launches=%d leases=%d, want one browser and %d leases", gotLaunches, len(leases), concurrentLeases)
	}
	root := pool.root
	if err := leases[0].Close(); err != nil {
		t.Fatal(err)
	}
	if root.Err() != nil || leases[1].ctx.Err() != nil {
		t.Fatal("closing one lease shut down the shared browser or a sibling tab")
	}
	for _, lease := range leases[1:] {
		_ = lease.Close()
	}
	select {
	case <-root.Done():
	case <-time.After(time.Second):
		t.Fatal("idle browser did not close")
	}

	restarted, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatalf("acquire after idle shutdown: %v", err)
	}
	mu.Lock()
	gotLaunches = launches
	mu.Unlock()
	if gotLaunches != 2 {
		t.Fatalf("launches after idle restart=%d, want 2", gotLaunches)
	}
	if err := pool.Close(); err != nil {
		t.Fatal(err)
	}
	if restarted.(*chromeBrowserProvider).ctx.Err() == nil {
		t.Fatal("pool shutdown did not close active tabs")
	}
	if _, err := pool.Acquire(context.Background()); !errors.Is(err, errBrowserUnavailable) {
		t.Fatalf("acquire after pool shutdown error=%v, want unavailable", err)
	}
}
