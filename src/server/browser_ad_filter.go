package main

import (
	_ "embed"

	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

//go:embed browser_ad_filter.js
var browserAdFilterScript string

func (p *chromeBrowserProvider) ensurePlayerDataFilter() error {
	p.adFilterOnce.Do(func() {
		execCtx, err := p.targetContext()
		if err != nil {
			p.adFilterErr = err
			return
		}
		_, p.adFilterErr = chromedp.Call(execCtx, page.AddScriptToEvaluateOnNewDocument, page.AddScriptToEvaluateOnNewDocumentParams{Source: browserAdFilterScript})
	})
	return p.adFilterErr
}
