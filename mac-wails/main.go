// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"context"
	"log"

	"github.com/google/uuid"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
)

func main() {
	sessionToken := "su-" + uuid.New().String()

	port, err := getFreePort()
	if err != nil {
		log.Fatalf("failed to find a free port: %v", err)
	}

	srv, err := runServer(port, sessionToken)
	if err != nil {
		log.Fatalf("failed to start the server: %v", err)
	}

	handler, err := newServerProxy(port, sessionToken)
	if err != nil {
		log.Fatalf("failed to create the server proxy: %v", err)
	}

	app := NewApp()

	err = wails.Run(&options.App{
		Title:  "Focalboard",
		Width:  1024,
		Height: 768,
		AssetServer: &assetserver.Options{
			Handler: handler,
		},
		OnStartup: app.startup,
		OnShutdown: func(_ context.Context) {
			_ = srv.Shutdown()
		},
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
		_ = srv.Shutdown()
		log.Fatalf("wails run error: %v", err)
	}
}
