# Focalboard desktop app (Wails)

A single-binary desktop wrapper for Focalboard, built with
[Wails v2](https://wails.io). One Go codebase (this `desktop/` module) builds
**macOS, Windows and Linux**, replacing the three former native wrappers (Swift,
C#/WPF, WebKitGTK): the Focalboard server runs **in-process** (no spawned
`focalboard-server` subprocess), so each platform ships as one executable.

## How it works

The Go code is platform-agnostic — the same files build for every OS:

- `server.go` — starts the Focalboard server in-process in single-user mode on a
  free port. The SQLite database and uploaded files live under the OS user config
  dir (`os.UserConfigDir()` → `~/Library/Application Support/Focalboard` on macOS,
  `%AppData%\Focalboard` on Windows, `~/.config/Focalboard` on Linux), **not** next
  to the binary, because a signed/packaged app dir is read-only.
- `frontend_embed.go` / `frontend_disk.go` — the webapp `pack` is compiled into the
  binary with `go:embed` (release builds, `-tags frontend`). Because `go:embed`
  can't reach `../webapp`, the Makefile first copies `webapp/pack` → `desktop/pack`
  (the `desktop-pack` target) and embeds that. At startup `resolveWebPath` extracts
  the embedded pack to `<dataDir>/web` and points the server there (the server
  templates `index.html` on read, so it needs files on disk). Without the tag
  (`go build ./...`, tests) it falls back to on-disk `pack`.
- `proxy.go` — a reverse proxy to the in-process server, wired into Wails as the
  `AssetServer.Handler`, so HTML/assets/API share the Wails origin. It injects a
  bootstrap `<script>` into the served HTML that: seeds the single-user session
  token into `localStorage`; sets `window.webSocketBaseURL` to the real server;
  and wires `window.openInNewBrowser` to open external links in the system
  browser. A capture-phase click handler sends **every** absolute http(s) anchor
  there, same-origin ones excepted. It deliberately does not defer to the inline
  `onclick` that `Utils.htmlFromMarkdown` puts on markdown links: when that
  handler does not run, the click is simply lost — the webview cannot navigate
  to an outside origin either — which is what made preview links in card
  comments dead.

  **WebSockets do not go through this proxy.** On macOS (and Linux) Wails serves
  the page from a WebKit custom-scheme origin, and that scheme handler cannot
  carry a WebSocket upgrade — this is a WebKit limitation, not a Wails bug, and it
  is unchanged in Wails v3. So the injected `window.webSocketBaseURL` makes the
  webapp's WS client connect straight to `ws://localhost:<port>/ws` (a real socket
  the webview opens directly). On Windows (WebView2/Chromium) the same direct
  socket simply works. This relies on a small, additive hook in the shared webapp:
  `wsclient.ts` honors `window.webSocketBaseURL` when set (inert in browser/plugin
  builds). Plain HTTP/`fetch` still flows through the proxy.
- `app.go` — `App.OpenInBrowser`, bound into JS and called by that script.
- `main.go` — wires it all together and runs the Wails window.

Builds are **native per platform** (each on its own machine/CI runner) — cgo
SQLite is not cross-compiled. macOS targets Apple Silicon (`arm64`); Windows and
Linux target `amd64`. The Makefile targets pass `-skipbindings`: Wails' generated
JS bindings are unused (the webapp calls `window.go.main.App.*` via the injected
runtime).

## Prerequisites

- Wails CLI:
  ```
  go install github.com/wailsapp/wails/v2/cmd/wails@latest
  ```
- A C toolchain for the cgo SQLite build:
  - **macOS**: Xcode Command Line Tools.
  - **Linux**: `gcc`, plus `libgtk-3-dev` and `libwebkit2gtk-4.0-dev` (Wails).
  - **Windows**: MinGW-w64 `gcc` on `PATH`, NSIS (`makensis`) for the installer,
    and the WebView2 runtime (bundled with modern Windows).
- **Linux installers**: `nfpm` for `.deb` (`go install
  github.com/goreleaser/nfpm/v2/cmd/nfpm@latest`); the AppImage script downloads
  `appimagetool` if it isn't already on `PATH`.
- **Browser-testing sessions** (the "To Test" column) need a browser MCP server
  on the agent — under *Agents → MCP servers*, paste the same JSON any MCP
  client takes, e.g. `{"mcpServers": {"playwright": {"command": "npx", "args":
  ["-y", "@playwright/mcp@latest", "--headless"]}}}`. The app ships no
  browser driver of its own and stays Node-free; the server is the user's
  choice, and a test session refuses to start for an agent without one. Each run
  gets a directory under `artifactsDir` (default
  `<dataDir>/artifacts/<session-id>`) where the agent is asked to save its
  screenshots and write `result.json` — that verdict is what moves the card.
- **First run**: a board made from the template opens a setup wizard by itself
  when the registries are still empty — a repository and an agent are asked for
  (nothing runs without them), Dokku and a browser MCP server are offered and
  skippable. It can be reopened from the board menu (*Set up this board…*), and
  closing it is remembered for that board.
- **Columns** (column menu → *Agents in this column…*) say what happens when a
  card lands in one: the action, the crew of agents who work it, and how many of
  them at once. A card without an agent of its own goes to whoever of the crew is
  free; when they are all busy, or the limit is reached, the card waits in place
  and starts by itself as soon as a place frees up. The old
  `triggerColumn`/`deployColumn`/`testColumn` keys are migrated into this
  registry on first load, so nothing changes until you edit it. A crew of several
  agents needs `worktreeMode: "always"` (the default) — without worktrees two
  agents cannot share one repository, and the crew works one card at a time.
- **Taking a card yourself**: assign it to yourself and no agent starts on it —
  the card keeps its place on the route and waits for you to move it on. Deploy
  and test still run, since that is machine work; assigning a registered agent,
  or nobody, hands the card back to automation.
- **Flows** (board "…" menu → *Workflows*) join those columns into a route and
  move cards along it. Repository events are polled from the branches parked
  cards wait on: plain git needs nothing, while `pr.*`, `review.approved` and
  `checks.*` call the GitHub API and want a token in `githubToken` (or
  `GITHUB_TOKEN`) — public repositories work without one, more slowly. The
  interval is `vcsPollSeconds` (default 60) and the remote is `gitRemote`
  (default `origin`). Which branch is watched: the card's `branch` property if
  it has one, otherwise the branch the card's own sessions worked on — with
  worktrees that is the agent's branch, which the card never names itself. A fresh config is seeded with three routes — `Feature`,
  `Hotfix` and `Review only` — and the "My Project Tasks" board template ships
  the columns they name plus a `Workflow` property to pick one with, so a new
  board runs them without any setup. Picking a route stays optional: a card with
  no `Workflow` option takes none, and the trigger columns work as they always
  did. The editor draws the route as a graph and offers whichever shipped route
  the registry is missing. A card shows its own route: which stage it stands on
  and what that stage is waiting for. Routes belong to the board they were made
  on, and a board made from the "My Project Tasks" template arrives with them:
  the template carries its columns and routes in the board's own properties, and
  the first card moved on it takes them into the registry. The Workflows dialog
  is both the map and the builder: it draws each route with the number of cards
  standing on every stage, and editing one turns the same canvas into the place
  the graph is drawn — stages are dragged, and pulling from a stage's right edge
  joins it to another.

## Develop

From the repo root:

```
make dev-wails
```

`wails.json` points `frontend:dir` at `../webapp` and declares
`frontend:dev:watcher: "npm run watchdev"`, so `wails dev` starts
`vite build --watch` for you and kills it on exit. Go changes trigger a
`wails dev` rebuild/restart; frontend changes are rebuilt into `webapp/pack`
and `-reloaddirs ../webapp/pack` reloads the window. Needs webapp deps — run
`make prebuild` once if `webapp/node_modules` is missing.

`wails dev` runs without the `frontend` tag, so nothing is embedded and the Go
side serves the on-disk `pack` (`diskWebPath` falls back to `../webapp/pack`).
The page still goes through `proxy.go` exactly as in a release build, which is
why nothing here needs to know the server port or the session token — both stay
random per launch, and the bootstrap script is injected the usual way.

There is deliberately no `frontend:dev:serverUrl`: pointing the webview at the
Vite dev server would buy HMR but would mean pinning the port and token up
front and re-implementing `proxy.go`'s bootstrap in `vite.config.mjs`.

To run it manually instead:

```
cd desktop && wails dev -tags "json1 sqlite3" -reloaddirs ../webapp/pack
```

For pure webapp/CSS iteration the browser loop is still faster than the webview:
`make watch` (server + Vite build watch via `modd`), then open
http://localhost:8000 — or `cd webapp && npm run dev` for HMR at
http://localhost:5173.

## Build release installers

From the repo root, per platform. The Makefile stages `webapp/pack` → `desktop/pack`
and embeds it, so the artifacts are single-binary:

```
make mac-dmg-wails          # macOS   → desktop/build/bin/Focalboard.dmg (+ Focalboard.app)
make win-app-wails          # Windows → desktop/build/bin/Focalboard-amd64-installer.exe (NSIS)
make linux-installers-wails # Linux   → desktop/build/bin/Focalboard-x86_64.AppImage (+ .deb via nfpm)
```

`make mac-app-wails` / `linux-app-wails` build just the `.app` / bare binary.
Installer configs live in `desktop/build/` (`darwin/`, `windows/` NSIS,
`linux/` `appimage.sh` + `nfpm.yaml` + `focalboard.desktop`).

## Sign & notarize macOS (single pass — one binary)

```
codesign --deep --force --options runtime \
  --entitlements build/darwin/entitlements.plist \
  --sign "Developer ID Application: <TEAM>" \
  desktop/build/bin/Focalboard.app

xcrun notarytool submit desktop/build/bin/Focalboard.app \
  --apple-id <id> --team-id <team> --password <app-specific-pw> --wait

xcrun stapler staple desktop/build/bin/Focalboard.app
```

## Out of scope (MVP)

Not yet implemented: the `nativeApp` bridge for persisting user settings, the
What's New dialog, multi-window, in-app downloads / file picker, and
window-position autosave.
