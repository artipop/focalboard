// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"

	"github.com/mattermost/focalboard/server/services/notify"

	"github.com/mattermost/focalboard/desktop/internal/acp"
	"github.com/mattermost/focalboard/desktop/internal/boardadapter"
)

// acpDataDir returns the ACP integration's own state directory
// (~/Library/Application Support/Focalboard/acp).
func acpDataDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "Focalboard", "acp")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", err
	}
	return dir, nil
}

func main() {
	// `focalboard mcp dokku` runs this same binary as an MCP server for an agent
	// session; it must come first, before the board server or a window exists.
	maybeRunMCP(os.Args[1:])

	sessionToken := "su-" + uuid.New().String()

	port, err := getFreePort()
	if err != nil {
		log.Fatalf("failed to find a free port: %v", err)
	}

	// ACP integration: config + board-event backend, wired before the server
	// so the notify backend registers during server construction.
	var (
		acpCfg     acp.Config
		acpEnabled bool
		events     *boardadapter.EventsBackend
		backends   []notify.Backend
	)
	if dir, err := acpDataDir(); err != nil {
		log.Printf("acp: disabled, no data dir: %v", err)
	} else if acpCfg, err = acp.LoadConfig(filepath.Join(dir, "config.json"), dir); err != nil {
		log.Printf("acp: disabled, config error: %v", err)
	} else if acpCfg.Enabled {
		acpEnabled = true
	}

	logger := newServerLogger()
	if acpEnabled {
		events = boardadapter.NewEventsBackend(logger)
		backends = append(backends, events)
	}

	srv, err := runServerWithLogger(logger, port, sessionToken, backends)
	if err != nil {
		log.Fatalf("failed to start the server: %v", err)
	}

	handler, err := newServerProxy(port, sessionToken)
	if err != nil {
		log.Fatalf("failed to create the server proxy: %v", err)
	}

	emitter := newWailsEmitter()
	app := NewApp(emitter)

	// Manager lifecycle: created after the server (needs srv.App()), stopped
	// before it (agents may still post comments during the grace period).
	var mgr *acp.Manager
	if acpEnabled {
		events.SetApp(srv.App())
		dir, _ := acpDataDir()
		store, err := acp.OpenStore(filepath.Join(dir, "acp.db"))
		if err != nil {
			log.Printf("acp: disabled, store error: %v", err)
		} else {
			mgr = acp.NewManager(acpCfg, filepath.Join(dir, "config.json"), store, boardadapter.NewWriter(srv.App()), emitter, nil)
			// Lets the UI open a console on a card without moving it.
			mgr.SetBoardReader(events)
			mgr.SetBoardMeta(events)
			// Lets the UI give agents board accounts, so cards can be
			// assigned to them in a person property.
			mgr.SetBoardUsers(events)
			if err := mgr.Start(context.Background(), events); err != nil {
				log.Printf("acp: disabled, start error: %v", err)
				mgr = nil
			} else {
				app.mgr = mgr
				log.Printf("acp: enabled (trigger %q/%q)", acpCfg.TriggerProperty, acpCfg.TriggerColumn)
			}
		}
	}

	shutdown := func() {
		if mgr != nil {
			mgr.Shutdown(5 * time.Second)
		}
		_ = srv.Shutdown()
	}

	err = wails.Run(&options.App{
		Title:  "Focalboard",
		Width:  1024,
		Height: 768,
		AssetServer: &assetserver.Options{
			Handler: handler,
		},
		OnStartup:  app.startup,
		OnShutdown: func(_ context.Context) { shutdown() },
		Bind: []interface{}{
			app,
		},
		Mac: &mac.Options{
			WebviewIsTransparent: false,
			About: &mac.AboutInfo{
				Title:   "Focalboard",
				Message: "Focalboard personal desktop",
			},
		},
	})
	if err != nil {
		shutdown()
		log.Fatalf("wails run error: %v", err)
	}
}
