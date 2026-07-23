# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project status

This repository (`mattermost/focalboard`) is **standalone Focalboard** and is no longer actively maintained by Mattermost. The Mattermost plugin lives in a separate repo (`mattermost/mattermost-plugin-boards`). Much of the code is shared/adapted with the Mattermost server via `github.com/mattermost/mattermost/server/public` imports.

## Build & run

A `.env` file in the repo root with `EXCLUDE_ENTERPRISE="1"` is expected for OSS development.

- `make prebuild` — install webapp npm deps. Only needed after dependency changes.
- `make` (or `make all`) — build webapp + server. Server binary lands at `bin/focalboard-server`.
- `make server` — build server only. `make webapp` — build+pack webapp only (`cd webapp; npm run pack`).
- `./bin/focalboard-server` — run server (serves on `http://localhost:8000`, port set in `config.json`).
- `make watch` — run server + webapp with hot reload via `modd` (requires `modd` installed). `make watch-single-user` for single-user mode.
- After the server is running, `make webapp` in another terminal rebuilds the frontend; reload the browser to see changes.

Desktop app builds package the server against SQLite (each needs `make prebuild` first; cross-compilation is not supported — build on the target platform):

- **macOS — `make mac-app-wails`** (current): a [Wails v2](https://wails.io) app in `mac-wails/` that runs the Focalboard server **in-process** in a single Apple-Silicon (`arm64`) binary — no spawned `focalboard-server` subprocess — which collapses signing/notarization to one binary. Needs the Wails CLI (`go install github.com/wailsapp/wails/v2/cmd/wails@latest`). See `mac-wails/README.md` for architecture (in-process server, reverse-proxy AssetServer, and the WebKit WebSocket workaround). The legacy Swift/`WKWebView` wrapper (`mac/`, `make mac-app`) is kept as a fallback.
- **Windows — `make win-wpf-app`** (C#/WPF, `win-wpf/`) and **Linux — `make linux-app`** (Go/WebKitGTK, `linux/`): unchanged. See README.md for platform prerequisites.

## Test & lint

- `make ci` — full local CI (`webapp-ci` + `server-test`). Run before committing.
- **Server tests run against all four databases**: `make server-test` runs sqlite, mysql, mariadb, and postgres in sequence. The mysql/mariadb/postgres targets spin up Docker containers (`docker-testing/docker-compose-*.yml`). For fast local iteration use only sqlite:
  - `make server-test-sqlite` — full server suite on sqlite.
  - `make server-test-mini-sqlite` — just `server/integrationtests`.
  - Single Go test: `cd server; FOCALBOARD_UNIT_TESTING=1 go test -tags 'json1 sqlite3' -run TestName ./app/...` (the `json1 sqlite3` build tags are required everywhere).
- **Webapp**: `cd webapp; npm run test` (jest), `npm run check` (eslint + stylelint), `npm run fix` (auto-fix). Single jest test: `npm run test -- -t "test name"`. Update snapshots: `npm run updatesnapshot`.
- **Server lint**: `make server-lint` (requires `golangci-lint`).
- **E2E**: `cd webapp; npm run cypress:ci` (builds server + runs it on :8088 + runs Cypress).

## Code generation

The server relies on `go:generate`. After changing the `Store` interface or DB access patterns:

- `make generate` — installs `mockgen` and runs `go generate ./...`.
- `server/services/store/store.go` drives two generators: `mockgen` produces `mockstore/mockstore.go`, and `generators/main.go` reads `// @withTransaction` annotations on interface methods to generate transaction-wrapping boilerplate. When adding a store method that mutates data, add the `// @withTransaction` comment and regenerate.
- `make templates-archive` regenerates `server/assets/templates.boardarchive` from the board templates.
- `make swagger` regenerates the API spec and generated clients from annotations.

## Architecture

### Server (`server/`, Go module `github.com/mattermost/focalboard/server`)

Layered, with dependencies pointing downward:

- `main/` → `server/` — process entry point and HTTP server wiring.
- `api/` — REST handlers, one file per resource (`boards.go`, `blocks.go`, `cards.go`, …). Thin; delegates to the app layer. `context.go` holds request context/auth plumbing.
- `app/` — business logic, one file per domain concept. This is where board/block/card/team/category/sharing rules live. Handlers here operate through the store interface, not raw SQL.
- `services/store/` — **the data-access abstraction**. `store.go` defines the `Store` interface. Implementations:
  - `sqlstore/` — the real backend, supporting **sqlite, mysql, mariadb, and postgres** from one codebase (SQL built with squirrel; migrations in `sqlstore/migrations/`). DB-specific quirks are branched inside these files.
  - `mattermostauthlayer/` — decorator that delegates user/auth to a Mattermost server.
  - `mockstore/` — generated mocks for tests.
- `ws/` — WebSocket layer for real-time block updates. `adapter.go`/`server.go` for standalone; `plugin_adapter*.go` for the plugin/clustered deployment.
- `model/` — shared domain types (Block, Board, Card, Team, User, etc.) and query-options structs, used by both api and app layers.
- `services/` — cross-cutting: `auth`, `config`, `audit`, `metrics`, `notify`, `permissions`, `scheduler`, `telemetry`, `webhook`.
- `integrationtests/` — end-to-end server tests exercising the full stack against a real store.

**Blocks are the core data model.** Boards contain blocks; cards, views, text, comments, and content are all block types (or board-adjacent entities). Most mutations funnel through `boards_and_blocks.go` so boards and their blocks stay consistent transactionally.

### Webapp (`webapp/src/`, React + TypeScript + Redux)

- `store/` — Redux slices (`boards.ts`, `cards.ts`, `contents.ts`, `comments.ts`, …). The single source of client state; components read via selectors/hooks (`store/hooks.ts`).
- `octoClient.ts` — the REST client wrapping every server API call. All HTTP goes through here.
- `wsclient.ts` — WebSocket client; applies real-time block changes into the Redux store. Honors `window.webSocketBaseURL` when set, so the Wails desktop wrapper can point the socket straight at the in-process server (its webview origin can't carry a WS upgrade); unset/inert in browser and plugin builds.
- `mutator.ts` — **the mutation layer**. UI actions call the mutator, which performs the octoClient call *and* registers an undo/redo step with `undomanager.ts`. Prefer mutating through `mutator` rather than calling `octoClient` directly, so undo and optimistic updates stay correct.
- `blocks/` — TypeScript models mirroring the server block types.
- `components/`, `pages/`, `widgets/`, `properties/` — UI. `properties/` implements the per-type card property editors.
- Webpack configs: `webpack.dev.js` / `webpack.prod.js` (packed into `webapp/pack/`, which the server serves). `webpack.editor.js` powers a standalone block editor dev server (`npm run deveditor`).
- i18n strings extracted with `npm run i18n-extract` into `webapp/i18n/`.
