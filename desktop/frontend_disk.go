// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

//go:build !frontend

// Package main, default (no `frontend` tag): nothing is embedded, so the server
// serves the webapp from on-disk pack (dev flow — `wails dev`). This keeps
// `go build ./...` and tests working without a staged frontend bundle.
package main

import "io/fs"

// embeddedFrontend reports that no frontend is compiled in.
func embeddedFrontend() (fs.FS, bool) { return nil, false }
