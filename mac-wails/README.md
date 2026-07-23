# Focalboard macOS app (Wails)

A single-binary macOS desktop wrapper for Focalboard, built with
[Wails v2](https://wails.io). It replaces the legacy Swift/`WKWebView` wrapper in
`mac/`: the Focalboard server runs **in-process** (no spawned `focalboard-server`
subprocess), so the app ships as one code-signed executable.

## How it works

- `server.go` — starts the Focalboard server in-process in single-user mode on a
  free port. The SQLite database and uploaded files live under
  `~/Library/Application Support/Focalboard/server` because the `.app` bundle is
  read-only once signed. The webapp `pack` is loaded from `Contents/MacOS/pack`.
- `proxy.go` — a reverse proxy to the in-process server, wired into Wails as the
  `AssetServer.Handler`, so HTML/assets/API share the Wails origin. It injects a
  bootstrap `<script>` into the served HTML that: seeds the single-user session
  token into `localStorage`; sets `window.webSocketBaseURL` to the real server;
  and wires `window.openInNewBrowser` to open external links in the system
  browser.

  **WebSockets do not go through this proxy.** On macOS Wails serves the page
  from a WKWebView custom-scheme origin, and that scheme handler cannot carry a
  WebSocket upgrade. So the injected `window.webSocketBaseURL` makes the webapp's
  WS client connect straight to `ws://localhost:<port>/ws` (a real socket the
  webview opens directly). This relies on a small, additive hook in the shared
  webapp: `wsclient.ts` honors `window.webSocketBaseURL` when set (inert in
  browser/plugin builds). Plain HTTP/`fetch` still flows through the proxy.
- `app.go` — `App.OpenInBrowser`, bound into JS and called by that script.
- `main.go` — wires it all together and runs the Wails window.

This mirrors the already-working in-process approach in `linux/main.go`.

Apple Silicon (`arm64`) only — Intel Macs are not supported.

## Prerequisites

- macOS on Apple Silicon, with Xcode Command Line Tools (for the cgo SQLite
  build and signing).
- Wails CLI:
  ```
  go install github.com/wailsapp/wails/v2/cmd/wails@latest
  ```

## Develop

```
make webapp                 # build webapp/pack (once, or after frontend changes)
cd mac-wails
wails dev -tags "json1 sqlite3"
```

## Build a release bundle

From the repo root:

```
make mac-app-wails
```

Output: `mac-wails/build/bin/Focalboard.app` (arm64).

## Sign & notarize (single pass — one binary)

```
codesign --deep --force --options runtime \
  --entitlements build/darwin/entitlements.plist \
  --sign "Developer ID Application: <TEAM>" \
  mac-wails/build/bin/Focalboard.app

xcrun notarytool submit mac-wails/build/bin/Focalboard.app \
  --apple-id <id> --team-id <team> --password <app-specific-pw> --wait

xcrun stapler staple mac-wails/build/bin/Focalboard.app
```

## Out of scope (MVP)

Not yet ported from the Swift wrapper: the `nativeApp` bridge for persisting user
settings, the What's New dialog, multi-window, in-app downloads / file picker, and
window-position autosave.
