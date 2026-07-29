// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"path"
	"path/filepath"

	"github.com/mattermost/focalboard/server/server"
	"github.com/mattermost/focalboard/server/services/config"
	"github.com/mattermost/focalboard/server/services/notify"
	"github.com/mattermost/focalboard/server/services/permissions/localpermissions"

	"github.com/mattermost/mattermost/server/public/shared/mlog"
)

// getFreePort asks the kernel for a free open port that is ready to use.
func getFreePort() (int, error) {
	addr, err := net.ResolveTCPAddr("tcp", "localhost:0")
	if err != nil {
		return 0, err
	}

	l, err := net.ListenTCP("tcp", addr)
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// dataDir returns the writable location for the database and uploaded files.
// A packaged/signed app directory is read-only, so persistent state must live in
// the OS user config dir instead of next to the binary. os.UserConfigDir()
// resolves per platform: ~/Library/Application Support on macOS,
// %AppData% on Windows, ~/.config (or $XDG_CONFIG_HOME) on Linux.
func dataDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "Focalboard", "server")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", err
	}
	return dir, nil
}

// resolveWebPath returns a real on-disk directory the Focalboard server can
// serve the webapp from (it templates index.html on read, so it needs files on
// disk, not an fs.FS). Release builds (`-tags frontend`) compile the webapp
// `pack` into the binary; it is extracted once per launch under the data dir.
// Dev builds have nothing embedded and fall back to on-disk pack.
func resolveWebPath(logger mlog.LoggerIFace) string {
	if src, ok := embeddedFrontend(); ok {
		dir, err := extractFrontend(src)
		if err == nil {
			return dir
		}
		logger.Error("failed to extract embedded frontend, falling back to disk", mlog.Err(err))
	}
	return diskWebPath()
}

// extractFrontend writes the embedded pack tree into <dataDir>/web, replacing
// any previous extraction, and returns that directory.
func extractFrontend(src fs.FS) (string, error) {
	data, err := dataDir()
	if err != nil {
		return "", err
	}
	dst := filepath.Join(data, "web")
	if err := os.RemoveAll(dst); err != nil {
		return "", err
	}
	err = fs.WalkDir(src, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		target := filepath.Join(dst, filepath.FromSlash(p))
		if d.IsDir() {
			return os.MkdirAll(target, 0o750)
		}
		in, err := src.Open(p)
		if err != nil {
			return err
		}
		defer in.Close()
		if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o640)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			out.Close()
			return err
		}
		return out.Close()
	})
	if err != nil {
		return "", err
	}
	return dst, nil
}

// diskWebPath resolves the webapp `pack` for dev/unpackaged runs: the bundle
// next to the executable, else `pack` (desktop/pack) or `../webapp/pack`
// relative to the working dir (`wails dev` runs from the desktop/ module dir).
func diskWebPath() string {
	if executable, err := os.Executable(); err == nil {
		executableDir, err := filepath.EvalSymlinks(filepath.Dir(executable))
		if err != nil {
			executableDir = filepath.Dir(executable)
		}
		if next := path.Join(executableDir, "pack"); dirExists(next) {
			return next
		}
	}
	// ../webapp/pack exists only in a source checkout, and there it is the
	// bundle the dev watcher keeps rebuilding — so it wins over ./pack, which
	// in that layout is desktop/pack: a copy staged by an earlier release build
	// that would otherwise shadow it and serve a stale frontend for good.
	for _, cand := range []string{filepath.Join("..", "webapp", "pack"), "pack"} {
		if dirExists(cand) {
			return cand
		}
	}
	return "./pack"
}

func dirExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

// newServerLogger builds the logger shared by the server and the ACP backend.
func newServerLogger() mlog.LoggerIFace {
	logger, _ := mlog.NewLogger()
	return logger
}

// runServerWithLogger starts the Focalboard server in-process (single-user
// mode) on the given port, returning the running server so the caller can
// Shutdown() it. notifyBackends are registered with the server's notification
// service (used by the ACP agent integration to observe card moves).
func runServerWithLogger(logger mlog.LoggerIFace, port int, sessionToken string, notifyBackends []notify.Backend) (*server.Server, error) {
	data, err := dataDir()
	if err != nil {
		return nil, fmt.Errorf("resolving data dir: %w", err)
	}

	cfg := &config.Configuration{
		ServerRoot:              fmt.Sprintf("http://localhost:%d", port),
		Port:                    port,
		DBType:                  "sqlite3",
		DBConfigString:          path.Join(data, "focalboard.db"),
		UseSSL:                  false,
		SecureCookie:            true,
		WebPath:                 resolveWebPath(logger),
		FilesDriver:             "local",
		FilesPath:               path.Join(data, "files"),
		Telemetry:               true,
		WebhookUpdate:           []string{},
		SessionExpireTime:       259200000000,
		SessionRefreshTime:      18000,
		LocalOnly:               false,
		EnableLocalMode:         false,
		LocalModeSocketLocation: "",
		AuthMode:                "native",
	}

	singleUser := len(sessionToken) > 0
	db, err := server.NewStore(cfg, singleUser, logger)
	if err != nil {
		return nil, fmt.Errorf("initializing store: %w", err)
	}

	permissionsService := localpermissions.New(db, logger)

	params := server.Params{
		Cfg:                cfg,
		SingleUserToken:    sessionToken,
		DBStore:            db,
		Logger:             logger,
		PermissionsService: permissionsService,
		NotifyBackends:     notifyBackends,
	}

	srv, err := server.New(params)
	if err != nil {
		return nil, fmt.Errorf("initializing server: %w", err)
	}

	if err := srv.Start(); err != nil {
		return nil, fmt.Errorf("starting server: %w", err)
	}
	return srv, nil
}
