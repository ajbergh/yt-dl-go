package main

import (
	"context"
	"sync"
	"time"

	"github.com/chromedp/chromedp"
)

const (
	browserPoolIdleTimeout  = 2 * time.Minute
	browserPoolStartTimeout = 20 * time.Second
)

type browserPoolLauncher func(context.Context) (context.Context, context.CancelFunc, context.CancelFunc, error)
type browserPoolTabFactory func(context.Context) (context.Context, context.CancelFunc)

// browserPool owns one Chrome process. Each acquired provider has an isolated
// tab and capture state; closing a provider never closes the shared process.
type browserPool struct {
	ctx         context.Context
	mu          sync.Mutex
	closed      bool
	active      int
	root        context.Context
	rootClose   context.CancelFunc
	allocClose  context.CancelFunc
	idleTimer   *time.Timer
	idleTimeout time.Duration
	launch      browserPoolLauncher
	newTab      browserPoolTabFactory
}

func newChromeBrowserPool(ctx context.Context, executable string) *browserPool {
	return &browserPool{
		ctx: ctx, idleTimeout: browserPoolIdleTimeout,
		launch: func(parent context.Context) (context.Context, context.CancelFunc, context.CancelFunc, error) {
			return launchChromeBrowser(parent, executable)
		},
		newTab: func(parent context.Context) (context.Context, context.CancelFunc) {
			return chromedp.NewContext(parent)
		},
	}
}

func (p *browserPool) Acquire(ctx context.Context) (browserMediaProvider, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.ctx.Err() != nil || ctx.Err() != nil {
		return nil, errBrowserUnavailable
	}
	if p.idleTimer != nil {
		p.idleTimer.Stop()
		p.idleTimer = nil
	}
	if p.root == nil {
		root, rootClose, allocClose, err := p.launch(p.ctx)
		if err != nil {
			if rootClose != nil {
				rootClose()
			}
			if allocClose != nil {
				allocClose()
			}
			return nil, errBrowserUnavailable
		}
		p.root, p.rootClose, p.allocClose = root, rootClose, allocClose
	}
	leaseCtx, leaseCancel := p.newTab(p.root)
	p.active++
	stopCaller := context.AfterFunc(ctx, leaseCancel)
	var releaseOnce sync.Once
	release := func() {
		releaseOnce.Do(func() {
			stopCaller()
			leaseCancel()
			p.release()
		})
	}
	return &chromeBrowserProvider{ctx: leaseCtx, cancel: leaseCancel, release: release}, nil
}

func (p *browserPool) release() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.active > 0 {
		p.active--
	}
	if p.closed || p.active != 0 || p.root == nil {
		return
	}
	root := p.root
	p.idleTimer = time.AfterFunc(p.idleTimeout, func() {
		p.mu.Lock()
		if p.closed || p.active != 0 || p.root != root {
			p.mu.Unlock()
			return
		}
		p.closeRootLocked()
		p.mu.Unlock()
	})
}

func (p *browserPool) closeRootLocked() {
	rootClose, allocClose := p.rootClose, p.allocClose
	p.root, p.rootClose, p.allocClose = nil, nil, nil
	if p.idleTimer != nil {
		p.idleTimer.Stop()
		p.idleTimer = nil
	}
	if rootClose != nil {
		rootClose()
	}
	if allocClose != nil {
		allocClose()
	}
}

func (p *browserPool) Close() error {
	p.mu.Lock()
	p.closed = true
	p.closeRootLocked()
	p.mu.Unlock()
	return nil
}
