// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"context"
	"log"
	"sync"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// wailsEmitter implements acp.UIEmitter over the Wails event bus. Events
// emitted before the runtime context is ready are dropped with a log line;
// the UI re-hydrates persisted session state on mount anyway.
type wailsEmitter struct {
	mu  sync.RWMutex
	ctx context.Context
}

func newWailsEmitter() *wailsEmitter { return &wailsEmitter{} }

// SetContext is called from App.startup once the Wails runtime is ready.
func (e *wailsEmitter) SetContext(ctx context.Context) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.ctx = ctx
}

func (e *wailsEmitter) Emit(event string, payload any) {
	e.mu.RLock()
	ctx := e.ctx
	e.mu.RUnlock()
	if ctx == nil {
		log.Printf("acp: dropping UI event %s (runtime not ready)", event)
		return
	}
	wruntime.EventsEmit(ctx, event, payload)
}
