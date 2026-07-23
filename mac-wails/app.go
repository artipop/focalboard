// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"context"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// App holds Wails lifecycle state and exposes methods bound into the frontend
// (reachable from JS as window.go.main.App.*).
type App struct {
	ctx context.Context
}

func NewApp() *App {
	return &App{}
}

// startup captures the Wails runtime context once the app is ready.
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
}

// OpenInBrowser opens the given URL in the user's default system browser.
// Bound to JS and invoked by the target=_blank click handler injected in
// bootstrapScript.
func (a *App) OpenInBrowser(url string) {
	if a.ctx == nil || url == "" {
		return
	}
	wruntime.BrowserOpenURL(a.ctx, url)
}
