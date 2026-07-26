// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

//go:build frontend

// Package main, `frontend` build tag: the webapp `pack` bundle is compiled into
// the binary. The Makefile stages it as `desktop/pack` (copied from
// `webapp/pack` — `go:embed` can't reach `../webapp`) before building. Release
// builds pass `-tags frontend`; plain `go build`/tests omit it and fall back to
// the on-disk pack (see frontend_disk.go), so the module still builds without a
// staged frontend.
package main

import (
	"embed"
	"io/fs"
)

//go:embed all:pack
var frontendFS embed.FS

// embeddedFrontend returns the compiled-in webapp pack rooted at its top level.
func embeddedFrontend() (fs.FS, bool) {
	sub, err := fs.Sub(frontendFS, "pack")
	if err != nil {
		return nil, false
	}
	return sub, true
}
