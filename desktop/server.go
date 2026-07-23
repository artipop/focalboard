// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"fmt"
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

// webPath resolves the bundled webapp `pack` directory, shipped next to the
// executable on every platform (Contents/MacOS/pack in the macOS bundle,
// alongside Focalboard.exe / the Linux binary otherwise).
func webPath() string {
	executable, err := os.Executable()
	if err != nil {
		return "./pack"
	}
	executableDir, err := filepath.EvalSymlinks(filepath.Dir(executable))
	if err != nil {
		executableDir = filepath.Dir(executable)
	}
	return path.Join(executableDir, "pack")
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
		WebPath:                 webPath(),
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
