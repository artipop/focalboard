package acp

import (
	"encoding/json"
	"fmt"
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
	// and anything before it is a wrapper — `proxychains4 -f corp.conf claude`,
	// a per-account shim script — while the bridge still appends its own
	// protocol flags. Takes precedence over BinPath.
	Command []string `json:"command,omitempty"`

	// Proxy/NoProxy/CACert are per-agent network settings, expanded into the
	// standard proxy environment variables at spawn time (see spawnEnv). They
	// let one agent reach the internet through a corporate proxy while another
	// goes direct; Env wins over them on conflicts.
	Proxy   string `json:"proxy,omitempty"`   // http(s)/socks5 URL → HTTP(S)_PROXY, ALL_PROXY
	NoProxy string `json:"noProxy,omitempty"` // comma-separated hosts/suffixes → NO_PROXY
	CACert  string `json:"caCert,omitempty"`  // PEM bundle for a TLS-inspecting proxy
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
// values win over whatever the desktop app itself was launched with. Network
// settings are expanded first and the explicit Env map last, so Env can
// override or blank out any of them (an empty value means "present but empty",
// which is how an agent opts out of an inherited corporate proxy).
func (a AgentEntry) spawnEnv() (env []string, drop []string) {
	add := func(k, v string) {
		env = append(env, k+"="+v)
		drop = append(drop, k)
	}
	if p := strings.TrimSpace(a.Proxy); p != "" {
		for _, k := range proxyEnvNames {
			add(k, p)
		}
		// Managed as a pair: an inherited NO_PROXY must not leak into an agent
		// that declares its own proxy.
		add("NO_PROXY", a.NoProxy)
		add("no_proxy", a.NoProxy)
	} else if n := strings.TrimSpace(a.NoProxy); n != "" {
		add("NO_PROXY", n)
		add("no_proxy", n)
	}
	if c := strings.TrimSpace(a.CACert); c != "" {
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

	// SystemPrompt is the board/column-level instruction prepended to every
	// triggered session's prompt (before the agent's own system prompt and the
	// card task). One trigger column today; may become a per-column map later.
	SystemPrompt string `json:"systemPrompt"`

	// WorktreeMode controls where sessions run: "never" (default) — directly
	// in the repository working tree, with concurrent sessions per repo
	// rejected; "always" — a dedicated git worktree per session. A smarter
	// "auto" (escalate to a worktree when the repo is busy/dirty) may come later.
	WorktreeMode string `json:"worktreeMode"`

	MaxConcurrent            int      `json:"maxConcurrent"`
	SessionTimeoutMinutes    int      `json:"sessionTimeoutMinutes"`
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
		WorktreeMode:             "never",
		MaxConcurrent:            3,
		SessionTimeoutMinutes:    15,
		PermissionTimeoutMinutes: 5,
		IdempotencyWindowSeconds: 10,
		AutoAllowTools:           []string{"Read", "Grep", "Glob", "Edit", "Write", "MultiEdit", "NotebookEdit", "TodoWrite"},
		WorktreeDir:              filepath.Join(dataDir, "worktrees"),
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
		if err := os.WriteFile(path, append(out, '\n'), 0o644); err != nil {
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

func (c Config) SessionTimeout() time.Duration {
	return time.Duration(c.SessionTimeoutMinutes) * time.Minute
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
	return os.WriteFile(path, append(out, '\n'), 0o644)
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
