package acp

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// RepoEntry is one named local repository in the registry.
type RepoEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// AgentEntry is one named coding agent in the registry. A card is mapped to an
// agent when one of its select option names (e.g. an "Agent" field option)
// matches the entry name. Its Env is injected per-process at spawn time, which
// is how several agents (e.g. two Codex accounts) coexist on one machine: give
// each its own CODEX_HOME/OPENAI_API_KEY (or CLAUDE_CONFIG_DIR/ANTHROPIC_API_KEY).
type AgentEntry struct {
	Name    string            `json:"name"`              // registry key; matches the card "Agent" option
	Kind    string            `json:"kind"`              // "claude" | "codex" | "antigravity" | "acp"
	BinPath string            `json:"binPath,omitempty"` // overrides binary discovery
	Model   string            `json:"model,omitempty"`   // --model passed to the CLI
	Prompt  string            `json:"prompt,omitempty"`  // per-agent system prompt prepended to the task
	Env     map[string]string `json:"env,omitempty"`     // per-process env (CODEX_HOME, OPENAI_API_KEY, …)
	Args    []string          `json:"args,omitempty"`    // extra CLI args (sandbox/approval, etc.)

	// Command is the launch argv. For the ACP-native kinds it is the whole
	// agent command (required for "acp"). For claude/codex it replaces the
	// binary the bridge builds its invocation on: the last element is the CLI
	// and anything before it is a wrapper — `proxychains4 -f myproxy.conf claude`,
	// a per-account shim script — while the bridge still appends its own
	// protocol flags. Takes precedence over BinPath.
	Command []string `json:"command,omitempty"`

	// ProxyName selects a named entry from the proxy registry (Config.Proxies).
	// Network settings live there rather than on the agent, so several agents
	// share one configuration and it is edited in a single place. Empty means
	// the agent inherits the app's own environment.
	ProxyName string `json:"proxyName,omitempty"`
}

// NetworkSettings is one network path: the proxy an agent's traffic takes and
// the trust material that goes with it. Expanded into the standard proxy
// environment variables at spawn time (see spawnEnv).
type NetworkSettings struct {
	Proxy   string `json:"proxy,omitempty"`   // http(s)/socks5 URL → HTTP(S)_PROXY, ALL_PROXY
	NoProxy string `json:"noProxy,omitempty"` // comma-separated hosts/suffixes → NO_PROXY
	CACert  string `json:"caCert,omitempty"`  // PEM bundle for a TLS-inspecting proxy

	// Username/Password are the proxy's basic-auth credentials, kept apart from
	// the URL so they are entered raw (percent-encoding is applied when the URL
	// is composed), masked in the UI and never rendered in a proxy list. They
	// still live in the config file, which is why SaveConfig keeps it 0600; to
	// keep the secret out of it entirely, point Proxy at a local relay that
	// holds the credentials itself (cntlm, px).
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
}

// ProxyURL returns the proxy address with credentials applied, percent-encoding
// whatever the password contains. Credentials given as fields win over any
// carried by the URL itself.
func (n NetworkSettings) ProxyURL() (string, error) {
	raw := strings.TrimSpace(n.Proxy)
	if raw == "" {
		return "", nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("не удалось разобрать адрес прокси %q: %w", raw, err)
	}
	if u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("в адресе прокси %q не хватает схемы или хоста, например http://%s", raw, raw)
	}
	if user := strings.TrimSpace(n.Username); user != "" {
		if n.Password == "" {
			u.User = url.User(user)
		} else {
			u.User = url.UserPassword(user, n.Password)
		}
	}
	return u.String(), nil
}

// redactProxySecret replaces the proxy password wherever it appears in text
// (raw or percent-encoded), so a CLI error echoing the proxy URL cannot carry
// the credential into a card comment or the session log.
func (n NetworkSettings) redactProxySecret(text string) string {
	if n.Password == "" {
		return text
	}
	forms := []string{n.Password, url.QueryEscape(n.Password)}
	// The form that actually travels in the URL: userinfo escaping is its own
	// set (":" becomes %3A, unlike PathEscape), and Userinfo.String() is the
	// only way to get exactly what ProxyURL emitted. The username is escaped
	// the same way, so the first literal ":" is the separator.
	if enc := url.UserPassword("u", n.Password).String(); strings.Contains(enc, ":") {
		forms = append(forms, enc[strings.Index(enc, ":")+1:])
	}
	for _, secret := range forms {
		if secret != "" {
			text = strings.ReplaceAll(text, secret, "***")
		}
	}
	return text
}

// ProxyEntry is one named network configuration in the registry, referenced by
// agents through AgentEntry.ProxyName.
type ProxyEntry struct {
	Name string `json:"name"` // registry key; matches AgentEntry.ProxyName
	NetworkSettings
}

// IsZero reports whether nothing is configured.
func (n NetworkSettings) IsZero() bool {
	return strings.TrimSpace(n.Proxy) == "" &&
		strings.TrimSpace(n.NoProxy) == "" &&
		strings.TrimSpace(n.CACert) == ""
}

// Validate normalizes and checks the settings. kind is the agent kind they will
// be used with, or "" to skip the kind-specific checks.
func (n NetworkSettings) Validate(kind string) (NetworkSettings, error) {
	n.Proxy = strings.TrimSpace(n.Proxy)
	n.NoProxy = strings.TrimSpace(n.NoProxy)
	n.CACert = strings.TrimSpace(n.CACert)
	n.Username = strings.TrimSpace(n.Username)
	if n.Proxy == "" && (n.Username != "" || n.Password != "") {
		return n, fmt.Errorf("логин/пароль заданы без адреса прокси")
	}
	if n.Proxy != "" {
		// The CLIs read the proxy variables as URLs; a bare host:port is
		// silently ignored, which looks like "the proxy setting does nothing".
		if _, err := n.ProxyURL(); err != nil {
			return n, err
		}
		// Claude Code documents no SOCKS support (code.claude.com/docs/en/network-config),
		// so a socks:// value would be accepted here and then quietly ignored.
		if kind == AgentKindClaude && strings.HasPrefix(strings.ToLower(n.Proxy), "socks") {
			return n, fmt.Errorf("Claude Code не поддерживает SOCKS-прокси: укажи http(s):// или заверни CLI в команду запуска (command)")
		}
	}
	return n, nil
}

// proxyEnvNames are the variables spawnEnv manages when Proxy is set. Both
// cases are covered: Node-based CLIs (claude, gemini) read the upper-case ones,
// most Rust/Go/curl-based ones accept either.
var proxyEnvNames = []string{
	"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY",
	"http_proxy", "https_proxy", "all_proxy",
}

// caCertEnvNames map a PEM bundle onto the per-runtime variables: Node
// (claude/gemini), Rust/Python (codex and friends), curl.
var caCertEnvNames = []string{
	"NODE_EXTRA_CA_CERTS", "SSL_CERT_FILE", "REQUESTS_CA_BUNDLE", "CURL_CA_BUNDLE",
}

// spawnEnv returns the "KEY=value" pairs injected into the agent process and
// the names dropped from the inherited environment first, so the agent's own
// values win over whatever the desktop app itself was launched with. net is the
// resolved network configuration; it is expanded first and the agent's Env map
// last, so Env can override or blank out any of it (an empty value means
// "present but empty", which is how an agent opts out of an inherited proxy).
func spawnEnv(a AgentEntry, net NetworkSettings) (env []string, drop []string) {
	add := func(k, v string) {
		env = append(env, k+"="+v)
		drop = append(drop, k)
	}
	// Validate has already rejected an unparseable address, so a late error
	// here would only mean a hand-edited config: fall back to the raw value.
	proxy, err := net.ProxyURL()
	if err != nil {
		proxy = strings.TrimSpace(net.Proxy)
	}
	if proxy != "" {
		for _, k := range proxyEnvNames {
			add(k, proxy)
		}
		// Managed as a pair: an inherited NO_PROXY must not leak into an agent
		// that goes through its own proxy.
		add("NO_PROXY", net.NoProxy)
		add("no_proxy", net.NoProxy)
	} else if n := strings.TrimSpace(net.NoProxy); n != "" {
		add("NO_PROXY", n)
		add("no_proxy", n)
	}
	if c := strings.TrimSpace(net.CACert); c != "" {
		for _, k := range caCertEnvNames {
			add(k, c)
		}
	}
	for k, v := range a.Env {
		add(k, v)
	}
	return env, drop
}

// Agent kinds. claude/codex run through in-process bridges; antigravity and the
// generic acp kind are ACP-native CLIs spawned over stdio (no bridge).
const (
	AgentKindClaude      = "claude"
	AgentKindCodex       = "codex"
	AgentKindAntigravity = "antigravity"
	AgentKindACP         = "acp"
)

// IsExternalACP reports whether the kind is an ACP-native external agent
// (spawned over stdio and talked to in pure ACP, no bridge translation).
func IsExternalACP(kind string) bool {
	return kind == AgentKindAntigravity || kind == AgentKindACP
}

// Config controls the agent integration. It is stored as JSON in the app data
// directory; the repo registry is edited through the desktop UI, the rest by
// hand for now.
type Config struct {
	Enabled bool `json:"enabled"`

	// AgentMode selects how sessions run: "claude" (built-in bridge spawning
	// the claude binary) or "acp-command" (arbitrary external ACP agent).
	AgentMode string `json:"agentMode"`
	// ClaudePath overrides claude binary discovery for agentMode "claude".
	ClaudePath string `json:"claudePath,omitempty"`
	// AgentCommand is the argv of an external ACP agent for agentMode "acp-command".
	AgentCommand []string `json:"agentCommand,omitempty"`

	TriggerProperty string `json:"triggerProperty"`
	TriggerColumn   string `json:"triggerColumn"`

	// RepoWhitelist lists directory roots a card's repo_path must be under.
	// Empty means every repo_path is rejected (explicit opt-in).
	RepoWhitelist []string `json:"repoWhitelist"`

	// Repos is the registry of named local repositories. A card is mapped to
	// a repo when one of its select/multiSelect option names (e.g. a tag)
	// matches a registry entry name. Registered paths are implicitly allowed.
	Repos []RepoEntry `json:"repos"`

	// Agents is the registry of named coding agents (claude/codex, with their
	// own prompt, model and env). A card is mapped to an agent when one of its
	// select option names (the "Agent" field) matches an entry name. When empty,
	// AgentMode below drives the (single) built-in agent for backward compat.
	Agents []AgentEntry `json:"agents"`

	// Proxies is the registry of named network configurations. Agents pick one
	// by name (AgentEntry.ProxyName), so a proxy is described once and shared.
	Proxies []ProxyEntry `json:"proxies"`

	// SystemPrompt is the board/column-level instruction prepended to every
	// triggered session's prompt (before the agent's own system prompt and the
	// card task). One trigger column today; may become a per-column map later.
	SystemPrompt string `json:"systemPrompt"`

	// WorktreeMode controls where sessions run: "never" (default) — directly
	// in the repository working tree, with concurrent sessions per repo
	// rejected; "always" — a dedicated git worktree per session. A smarter
	// "auto" (escalate to a worktree when the repo is busy/dirty) may come later.
	WorktreeMode string `json:"worktreeMode"`

	MaxConcurrent int `json:"maxConcurrent"`
	// SessionTimeoutMinutes bounds one agent turn; SessionIdleMinutes bounds
	// how long an interactive session sits between turns before closing.
	SessionTimeoutMinutes    int      `json:"sessionTimeoutMinutes"`
	SessionIdleMinutes       int      `json:"sessionIdleMinutes"`
	PermissionTimeoutMinutes int      `json:"permissionTimeoutMinutes"`
	IdempotencyWindowSeconds int      `json:"idempotencyWindowSeconds"`
	AutoAllowTools           []string `json:"autoAllowTools"`
	ShowThoughts             bool     `json:"showThoughts"`
	WorktreeDir              string   `json:"worktreeDir"`
	KeepFailedWorktrees      bool     `json:"keepFailedWorktrees"`
}

// DefaultConfig returns the defaults written on first run. dataDir is the ACP
// data directory (worktrees live under it).
func DefaultConfig(dataDir string) Config {
	return Config{
		Enabled:                  true,
		AgentMode:                "claude",
		TriggerProperty:          "Status",
		TriggerColumn:            "To Agent",
		RepoWhitelist:            []string{},
		Repos:                    []RepoEntry{},
		Agents:                   []AgentEntry{},
		Proxies:                  []ProxyEntry{},
		WorktreeMode:             "never",
		MaxConcurrent:            3,
		SessionTimeoutMinutes:    15,
		SessionIdleMinutes:       30,
		PermissionTimeoutMinutes: 5,
		IdempotencyWindowSeconds: 10,
		// Bash is on the list because a coding agent cannot do its job without a
		// shell (tests, git, build), and a session with no console open has
		// nobody to ask — every prompt would simply be rejected. Edit/Write are
		// already allowed, so withholding the shell bought little in practice.
		AutoAllowTools: []string{"Read", "Grep", "Glob", "Edit", "Write", "MultiEdit", "NotebookEdit", "TodoWrite", "Bash"},
		WorktreeDir:    filepath.Join(dataDir, "worktrees"),
	}
}

// LoadConfig reads path, creating it with defaults when absent.
func LoadConfig(path, dataDir string) (Config, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		cfg := DefaultConfig(dataDir)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return cfg, err
		}
		out, _ := json.MarshalIndent(cfg, "", "  ")
		if err := os.WriteFile(path, append(out, '\n'), 0o600); err != nil {
			return cfg, err
		}
		return cfg, nil
	}
	if err != nil {
		return Config{}, err
	}
	cfg := DefaultConfig(dataDir)
	if err := json.Unmarshal(b, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return cfg, nil
}

// SessionTimeout bounds a single agent turn.
func (c Config) SessionTimeout() time.Duration {
	return time.Duration(c.SessionTimeoutMinutes) * time.Minute
}

// SessionIdle bounds how long an interactive session waits between turns
// before closing itself and releasing its repository.
func (c Config) SessionIdle() time.Duration {
	if c.SessionIdleMinutes <= 0 {
		return 30 * time.Minute
	}
	return time.Duration(c.SessionIdleMinutes) * time.Minute
}

func (c Config) PermissionTimeout() time.Duration {
	return time.Duration(c.PermissionTimeoutMinutes) * time.Minute
}

func (c Config) IdempotencyWindow() time.Duration {
	return time.Duration(c.IdempotencyWindowSeconds) * time.Second
}

// UseWorktrees reports whether sessions get a dedicated git worktree.
func (c Config) UseWorktrees() bool {
	return c.WorktreeMode == "always"
}

// ToolAllowed reports whether toolName is on the auto-allow list.
func (c Config) ToolAllowed(toolName string) bool {
	for _, t := range c.AutoAllowTools {
		if strings.EqualFold(t, toolName) {
			return true
		}
	}
	return false
}

// SaveConfig writes cfg to path (used when the UI edits the repo registry).
func SaveConfig(path string, cfg Config) error {
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, append(out, '\n'), 0o600); err != nil {
		return err
	}
	// WriteFile's mode only applies when it creates the file, so an existing
	// config keeps whatever it had — tighten it, the file can hold proxy
	// credentials and API keys (agent env).
	return os.Chmod(path, 0o600)
}

// ValidateRepoPath checks a card's repo_path against the whitelist, the repo
// registry and the filesystem. It returns the cleaned absolute path.
func (c Config) ValidateRepoPath(repoPath string) (string, error) {
	if strings.TrimSpace(repoPath) == "" {
		return "", fmt.Errorf("repo_path is empty")
	}
	if !filepath.IsAbs(repoPath) {
		return "", fmt.Errorf("repo_path must be absolute: %s", repoPath)
	}
	clean := filepath.Clean(repoPath)
	info, err := os.Stat(clean)
	if err != nil {
		return "", fmt.Errorf("repo_path does not exist: %s", clean)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("repo_path is not a directory: %s", clean)
	}
	roots := append([]string(nil), c.RepoWhitelist...)
	for _, r := range c.Repos {
		roots = append(roots, r.Path)
	}
	for _, root := range roots {
		rootClean := filepath.Clean(root)
		if clean == rootClean || strings.HasPrefix(clean, rootClean+string(filepath.Separator)) {
			return clean, nil
		}
	}
	return "", fmt.Errorf("repo_path %s is not under any whitelisted root (repoWhitelist / repos in acp config)", clean)
}
