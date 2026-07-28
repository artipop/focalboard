// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/mattermost/focalboard/desktop/internal/dokku"
)

// maybeRunMCP handles `<binary> mcp <server>`: the same executable doubles as
// the MCP servers an agent session spawns, which keeps the desktop app a single
// binary with nothing extra to install. It must run before the Focalboard
// server and Wails are touched, and it never returns — stdout belongs to the
// JSON-RPC stream from here on.
func maybeRunMCP(args []string) {
	if len(args) == 0 || args[0] != "mcp" {
		return
	}
	if len(args) < 2 {
		fmt.Fprintf(os.Stderr, "usage: focalboard mcp %s\n", dokku.ServerName)
		os.Exit(2)
	}
	var err error
	switch args[1] {
	case dokku.ServerName:
		err = runDokkuMCP()
	default:
		fmt.Fprintf(os.Stderr, "неизвестный MCP-сервер %q (есть только %q)\n", args[1], dokku.ServerName)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "mcp %s: %v\n", args[1], err)
		os.Exit(1)
	}
	os.Exit(0)
}

func runDokkuMCP() error {
	raw := os.Getenv(dokku.EnvTarget)
	if raw == "" {
		return fmt.Errorf("не задан %s", dokku.EnvTarget)
	}
	var target dokku.Target
	if err := json.Unmarshal([]byte(raw), &target); err != nil {
		return fmt.Errorf("не удалось разобрать %s: %w", dokku.EnvTarget, err)
	}
	cl, err := dokku.New(target, os.Getenv(dokku.EnvRepo), os.Getenv(dokku.EnvBranch))
	if err != nil {
		return err
	}

	// The agent kills the server by closing stdio; signals are the fallback for
	// a session torn down from the outside.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// The agent closing our stdio is how a session ends, so EOF is success.
	if err := dokku.ServeStdio(ctx, cl, os.Getenv(dokku.EnvArtifacts)); err != nil &&
		!errors.Is(err, context.Canceled) && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

// runWebtestMCP serves the browser tools. The browser is launched up front
// rather than on the first tool call: a session whose browser cannot start is
// better off failing at once, with the reason on the card, than half-way
// through a scenario.
