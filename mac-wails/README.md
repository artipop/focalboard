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

## ACP agent integration

Moving a card into the trigger column (default: select property **Status**,
option **To Agent**) starts a Claude Code session and reports progress/results
as comments on the card. Spec: `../TZ_ACP_wails_v0.2.md`; protocol findings:
`cmd/acpspike/NOTES.md`.

By default the agent works **directly in the repository working tree**
(`worktreeMode: "never"`): it is instructed to leave changes uncommitted for
review, and a second session for a busy repo is rejected with a clear card
comment. Set `worktreeMode: "always"` to give every session a dedicated git
worktree (branch `acp/<card>-<sess>`, kept after success). A smarter `auto`
mode (escalate to a worktree when the repo is busy or dirty) is a candidate
for later.

- **No Node.js.** The app talks ACP in pure Go (`coder/acp-go-sdk`);
  `internal/acp/claudebridge` translates ACP ⇄ the `claude` binary's native
  stream-json stdio protocol in-process. The only external dependency is the
  `claude` CLI (native installer).
- Layout: `internal/acp` (board-agnostic: manager, sessions, trigger policy,
  worktrees, SQLite state) + `internal/boardadapter` (the only package that
  imports both the Focalboard server and `internal/acp`: a `notify.Backend`
  observing card moves, and a writer posting comments via `srv.App()`).
- **Mapping cards to repositories**: register local repos in the board menu
  (“…” → *Agent repositories…*, desktop only) — each entry is a name + a path
  chosen with the native folder picker. The dialog’s *Sync to board* button
  finds or creates a dedicated **Repositories** multiSelect property on the
  board and adds the registry names as options (the “My Project Tasks”
  template ships this field already). A card is matched to a repo when one of
  its select/multiSelect option names (typically a Repositories tag, but any
  option works) equals a registry entry name (case-insensitive), or when the
  card is dragged out of a column named after a repo. A `repo_path` text
  property remains as an explicit per-card override (validated against
  `repoWhitelist` + registered paths). Optional card property `branch` picks
  the worktree base (worktree mode only).
- **Mapping cards to agents**: register named agents in the board menu (“…” →
  *Agents*, desktop only); a card routes to one by its **Agent** select option.
  Each entry carries its own kind, model, system prompt and, for running several
  accounts or network segments side by side on one machine:
  - `env` — per-process environment, e.g. `CLAUDE_CONFIG_DIR` /
    `ANTHROPIC_API_KEY` or `CODEX_HOME` / `OPENAI_API_KEY` for a second account.
    It is injected at spawn (`procgroup.Spawn`), no terminal isolation needed.
  - `command` — the launch argv. For `claude`/`codex` it replaces the binary the
    bridge invokes, so the CLI can be wrapped: `proxychains4 -q -f corp.conf
    claude` routes one agent through the corporate segment, a shim script can do
    anything else (`tsh`, `nsenter`, a VPN namespace). The bridge still appends
    its own protocol flags, and `args` still appends CLI flags. For the
    ACP-native kinds it is the whole command, as before.
  - `proxy` / `noProxy` / `caCert` — expanded into `HTTP(S)_PROXY`, `ALL_PROXY`,
    `NO_PROXY` and the per-runtime CA variables (`NODE_EXTRA_CA_CERTS`,
    `SSL_CERT_FILE`, …). This covers a plain corporate proxy without a wrapper;
    `env` overrides them (an empty value opts an agent out of an inherited
    proxy). A per-agent *VPN* needs the `command` wrapper — env alone cannot
    change routing.
- Config: `~/Library/Application Support/Focalboard/acp/config.json` (created
  with defaults on first run; the repo registry is stored there too). If the
  app can't find `claude` (GUI apps get a minimal `PATH`), set `claudePath`
  to an absolute path.
- Session state and logs: `~/Library/Application Support/Focalboard/acp/acp.db`;
  worktrees (in `worktreeMode: "always"`): `.../acp/worktrees`, kept after
  successful sessions for review — the result comment includes the path,
  branch and a diff hint.
- Not yet wired: streaming panel and permission modal in the UI (Phase 2);
  until then permissions are auto-decided by the `autoAllowTools` list and
  everything else is denied.

## Out of scope (MVP)

Not yet ported from the Swift wrapper: the `nativeApp` bridge for persisting user
settings, the What's New dialog, multi-window, in-app downloads / file picker, and
window-position autosave.
