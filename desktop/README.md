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
- **Browser-testing sessions** (the "To Test" column, `internal/webtest`) drive a
  real browser over CDP: an installed Chrome/Chromium/Edge is used when there is
  one, otherwise rod downloads a managed Chromium into its own cache on first
  run. Set `browserPath` in the agent config to pin a binary, and
  `browserHeadless: false` to watch the run. Screenshots and `result.json` of
  each run land in `artifactsDir` (default `<dataDir>/artifacts/<session-id>`).

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
