package acp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mattermost/focalboard/desktop/internal/dokku"
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
	Kind    string            `json:"kind"`              // "claude" | "codex" | "antigravity" | "copilot" | "junie" | "acp"
	BinPath string            `json:"binPath,omitempty"` // overrides adapter discovery
	Model   string            `json:"model,omitempty"`   // the model the adapter is asked for
	Prompt  string            `json:"prompt,omitempty"`  // per-agent system prompt prepended to the task
	Env     map[string]string `json:"env,omitempty"`     // per-process env (CODEX_HOME, OPENAI_API_KEY, …)
	Args    []string          `json:"args,omitempty"`    // extra CLI args (sandbox/approval, etc.)

	// AutoAllowTools overrides the global policy for this agent, so a trusted
	// one can be let loose and a new one kept on a short leash without changing
	// anything for the rest. Entries take the same form as autoAllowTools,
	// including argument patterns such as "Bash(git *)".
	AutoAllowTools []string `json:"autoAllowTools,omitempty"`

	// Command is the launch argv, and it is the whole agent command (required
	// for "acp"): what it replaces is the adapter binary we would have looked
	// up, so a wrapper gets in front of it — `proxychains4 -f myproxy.conf
	// codex-acp`, a per-account shim script. Takes precedence over BinPath, and
	// with it set nothing of ours is appended, so the flags the kind would have
	// carried (the ACP switch, the model) have to be spelled out.
	Command []string `json:"command,omitempty"`

	// MCPServers are the agent's own MCP servers, spawned alongside the one a
	// deploy session configures itself. This is how a Node-based server such as
	// @playwright/mcp plugs in without the app depending on Node: the user wires
	// it per agent, we only pass it on.
	//
	// The shape is the one every MCP client uses — name → {command, args, env} —
	// so an entry can be pasted straight from a server's README, and so the
	// config file reads the same as the agent's own.
	MCPServers MCPServerSet `json:"mcpServers,omitempty"`

	// ProxyName selects a named entry from the proxy registry (Config.Proxies).
	// Network settings live there rather than on the agent, so several agents
	// share one configuration and it is edited in a single place. Empty means
	// the agent inherits the app's own environment.
	ProxyName string `json:"proxyName,omitempty"`
}

// AgentMCPServer is one MCP server an agent carries of its own, in the standard
// client shape: a command with its arguments and environment.
//
// Configuring one is consent to use it: its tools (mcp__<name>__…) run without
// asking, for the same reason our own server's do — a card-triggered session
// has no console, and asking nobody means rejecting.
//
// Type and URL exist only to recognise a remote server pasted from a README and
// say what is wrong: everything here is spawned over stdio.
type AgentMCPServer struct {
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`

	Type string `json:"type,omitempty"`
	URL  string `json:"url,omitempty"`
}

// UnmarshalJSON reads "command" as either the binary alone, with the rest in
// "args", or as the whole argv — which is how a good few clients write it, and
// what a person copying from one of them will produce. An argv is split into
// the two fields, since that is what the bridges pass on.
func (s *AgentMCPServer) UnmarshalJSON(data []byte) error {
	var raw struct {
		Command json.RawMessage   `json:"command,omitempty"`
		Args    []string          `json:"args,omitempty"`
		Env     map[string]string `json:"env,omitempty"`
		Type    string            `json:"type,omitempty"`
		URL     string            `json:"url,omitempty"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*s = AgentMCPServer{Args: raw.Args, Env: raw.Env, Type: raw.Type, URL: raw.URL}
	if len(bytes.TrimSpace(raw.Command)) == 0 {
		return nil
	}

	var command string
	if err := json.Unmarshal(raw.Command, &command); err == nil {
		s.Command = command
		return nil
	}
	var argv []string
	if err := json.Unmarshal(raw.Command, &argv); err != nil {
		return fmt.Errorf("\"command\" должен быть строкой или списком аргументов, а не %s", raw.Command)
	}
	if len(argv) == 0 {
		return nil
	}
	s.Command = argv[0]
	s.Args = append(append([]string(nil), argv[1:]...), s.Args...)
	return nil
}

// MCPServerSet is how an agent's servers are stored: name → server, the shape
// every MCP client uses. It reads more than it writes, because the file is
// edited by hand as often as by us and the same servers are written differently
// in the wild:
//
//	{"playwright": {"command": "npx", …}}                  the canonical shape
//	[{"name": "playwright", "command": "npx", …}]          a list of named ones
//	{"mcpServers": {"playwright": {…}}}                    the whole client file
//
// All three mean the same thing, so all three are accepted rather than
// disabling the integration over the punctuation. Anything else is reported
// with the name of the field, since a config that cannot be read stops
// everything.
type MCPServerSet map[string]AgentMCPServer

func (s *MCPServerSet) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		*s = nil
		return nil
	}

	if trimmed[0] == '[' {
		var list []json.RawMessage
		if err := json.Unmarshal(trimmed, &list); err != nil {
			return fmt.Errorf("mcpServers: %w", err)
		}
		out := make(MCPServerSet, len(list))
		for i, item := range list {
			var named struct {
				Name string `json:"name"`
			}
			if err := json.Unmarshal(item, &named); err != nil {
				return fmt.Errorf("mcpServers[%d]: %w", i+1, err)
			}
			name := strings.TrimSpace(named.Name)
			if name == "" {
				return fmt.Errorf("mcpServers: у сервера №%d нет имени — добавьте \"name\" или запишите их объектом {\"имя\": {…}}", i+1)
			}
			var server AgentMCPServer
			if err := json.Unmarshal(item, &server); err != nil {
				return fmt.Errorf("mcpServers[%q]: %w", name, err)
			}
			out[name] = server
		}
		*s = out
		return nil
	}

	// Plain object — either the servers themselves, or a whole client config
	// with them nested under "mcpServers".
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &raw); err != nil {
		return fmt.Errorf("mcpServers: %w", err)
	}
	if nested, ok := raw["mcpServers"]; ok && len(raw) == 1 {
		return s.UnmarshalJSON(nested)
	}
	out := make(MCPServerSet, len(raw))
	for name, value := range raw {
		var server AgentMCPServer
		if err := json.Unmarshal(value, &server); err != nil {
			return fmt.Errorf("mcpServers[%q]: %w", name, err)
		}
		out[name] = server
	}
	*s = out
	return nil
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

// DeployEntry is one named Dokku destination in the registry: where the branch
// of a card moved into the deploy column is published. A card is mapped to an
// entry by a select option carrying its name, by the repository it resolved to,
// or — with a single entry registered — by default.
//
// The Dokku half is dokku.Target verbatim, because that is exactly what the MCP
// subprocess is handed at session start.
type DeployEntry struct {
	Name string `json:"name"` // registry key; matches the card "Deploy target" option

	// An entry is the host and the domain, nothing else: what a preview needs
	// beyond that — environment, TLS, how long a build may take — is a property
	// of the repository being deployed, not of the machine it lands on.
	dokku.Target
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

// Agent kinds. Every one of them is an ACP agent spawned over stdio and talked
// to in pure ACP — claude and codex through the adapters their vendors ship,
// the rest through CLIs that speak ACP themselves.
const (
	AgentKindClaude      = "claude"
	AgentKindCodex       = "codex"
	AgentKindAntigravity = "antigravity"
	AgentKindCopilot     = "copilot"
	AgentKindJunie       = "junie"
	AgentKindACP         = "acp"
)

// AgentKinds lists every accepted kind, in the order the UI offers them.
var AgentKinds = []string{
	AgentKindClaude, AgentKindCodex,
	AgentKindAntigravity, AgentKindCopilot, AgentKindJunie,
	AgentKindACP,
}

// acpAdapter is everything that differs between one ACP agent and another: the
// binary to look for, the package that provides it when it is missing, the
// flags that select ACP-over-stdio, how a model is asked for, what the process
// must not inherit, and the mode to switch it into once connected.
//
// Everything else — the connection, MCP servers, permissions, cancellation,
// turn budgets — is the same code for every kind, which is the point of the
// table: adding an agent is a row, not a branch.
type acpAdapter struct {
	// bin is the executable, looked up on PATH and in the usual install spots.
	bin string
	// npmPackage provides bin when it is not installed. The two vendor adapters
	// are published there and nowhere else, so it doubles as the install
	// instruction we show and the argument to npx when we fall back to it.
	npmPackage string
	// acpArgs put the CLI into ACP-over-stdio mode. Empty when it has no other
	// mode to be in.
	acpArgs []string
	// modelArgs and modelEnv are the two ways an agent is told which model to
	// use; a kind uses one or the other.
	modelArgs func(model string) []string
	modelEnv  string
	// dropEnv names variables the process must not inherit from ours.
	dropEnv []string
	// mode is the session mode to select after session/new, when the agent's
	// default is not what a card wants.
	mode string
}

// dashDashModel is the spelling every ACP-native CLI uses.
func dashDashModel(model string) []string { return []string{"--model", model} }

// tomlString quotes a value for a TOML basic string, which is what the right
// half of codex's `-c key=value` is parsed as. Go's own quoting escapes the
// same characters TOML does, and leaves printable non-ASCII alone, which TOML
// allows.
func tomlString(s string) string { return strconv.Quote(s) }

// acpNative is the table of agents we know how to launch. The generic acp kind
// is deliberately absent — it carries its own Command.
var acpNative = map[string]acpAdapter{
	// The Claude Code adapter embeds the Claude Agent SDK, which embeds the CLI,
	// so the claude binary is not needed alongside it. It is a Node package and
	// there is no other build of it, which is why the desktop app now needs
	// Node.js for this kind and only this kind.
	AgentKindClaude: {
		bin:        "claude-code-acp",
		npmPackage: "@zed-industries/claude-code-acp",
		// The adapter takes no flags at all: it is an ACP agent and nothing else.
		modelEnv: "ANTHROPIC_MODEL",
		// Claude Code refuses to start inside another Claude Code session, and
		// the desktop app may well have been launched from one.
		dropEnv: []string{"CLAUDECODE"},
	},
	// The Codex adapter is a self-contained native binary (the npm package only
	// picks the right one per platform), so this kind needs neither the codex
	// CLI nor Node.js once it is installed.
	AgentKindCodex: {
		bin:        "codex-acp",
		npmPackage: "@zed-industries/codex-acp",
		// Its only option is codex's own config override, so that is also how
		// the model is chosen.
		modelArgs: func(model string) []string { return []string{"-c", "model=" + tomlString(model)} },
		// It starts read-only, which is not what a card asked for: a session
		// that may not edit anything would spend its turn saying so.
		mode: "auto",
	},
	AgentKindAntigravity: {bin: "antigravity", acpArgs: []string{"--acp"}, modelArgs: dashDashModel},
	AgentKindCopilot:     {bin: "copilot", acpArgs: []string{"--acp"}, modelArgs: dashDashModel}, // github/copilot-cli, stdio is its default transport
	AgentKindJunie:       {bin: "junie", acpArgs: []string{"--acp=true"}, modelArgs: dashDashModel},
}

// agentModeCommand is the AgentMode that names no kind: the agent is whatever
// AgentCommand spells out.
const agentModeCommand = "acp-command"

// knownAdapter reports whether the kind is one we know how to launch ourselves.
func knownAdapter(kind string) bool {
	_, ok := acpNative[kind]
	return ok
}

// Config controls the agent integration. It is stored as JSON in the app data
// directory; the repo registry is edited through the desktop UI, the rest by
// hand for now.
type Config struct {
	Enabled bool `json:"enabled"`

	// AgentMode is the kind a card falls back to when the agent registry is
	// empty: one of the kinds above, or "acp-command" for the argv in
	// AgentCommand.
	AgentMode string `json:"agentMode"`
	// AgentCommand is the argv of an external ACP agent for agentMode "acp-command".
	AgentCommand []string `json:"agentCommand,omitempty"`

	TriggerProperty string `json:"triggerProperty"`
	TriggerColumn   string `json:"triggerColumn"`

	// DeployColumn is the second trigger on the same property: a card dragged
	// into it starts a session whose job is to publish the card's branch to the
	// Dokku target it resolves to. Empty disables the deploy trigger.
	DeployColumn string `json:"deployColumn"`

	// TestColumn is the third trigger on the same property: a card dragged into
	// it starts a session that opens the card's preview in a real browser and
	// checks it against the card's description. Empty disables the test trigger.
	TestColumn string `json:"testColumn"`

	// TestPassColumn and TestFailColumn are where the card goes once the verdict
	// is in. Empty means the card stays put and a human decides.
	TestPassColumn string `json:"testPassColumn"`
	TestFailColumn string `json:"testFailColumn"`

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

	// Deploys is the registry of named Dokku destinations used by the deploy
	// column. The matching target is handed to the session's dokku MCP server.
	Deploys []DeployEntry `json:"deploys"`

	// Columns is what happens in each column of a board: the action a card
	// entering it starts, who works it, how many at once. It is the single
	// answer to "what does this column do" — the TriggerColumn/DeployColumn/
	// TestColumn keys above are only the seed it is migrated from. See columns.go.
	Columns []ColumnSpec `json:"columns"`

	// Flows is the registry of named routes across the board: which column
	// follows which, and on what event. A card without a matching flow still
	// gets whatever its column does — a flow adds the transitions, not the
	// behaviour. See flows.go.
	Flows []FlowEntry `json:"flows"`

	// SystemPrompt is the board/column-level instruction prepended to every
	// triggered session's prompt (before the agent's own system prompt and the
	// card task). One trigger column today; may become a per-column map later.
	SystemPrompt string `json:"systemPrompt"`

	// DeployPrompt is what a deploy session is told to do; the concrete facts
	// (repository, branch, target, expected URL) are appended to it.
	DeployPrompt string `json:"deployPrompt"`

	// TestPrompt is what a test session is told to do; the preview URL and the
	// card's own description (which is the scenario) are appended to it.
	TestPrompt string `json:"testPrompt"`

	// TestTimeoutMinutes replaces SessionTimeoutMinutes for a test turn, which
	// clicks through a whole scenario and needs longer than a code edit. How the
	// browser itself is launched — headless, which binary, what viewport — is
	// the business of the MCP server the agent carries, not of this config.
	TestTimeoutMinutes int `json:"testTimeoutMinutes"`

	// ArtifactsDir is where screenshots and result.json of test runs are kept.
	ArtifactsDir string `json:"artifactsDir"`

	// VCSPollSeconds is how often the repositories cards wait on are polled for
	// branch and pull-request events. Zero disables repository watching.
	VCSPollSeconds int `json:"vcsPollSeconds"`
	// GitRemote is the remote consulted for those events.
	GitRemote string `json:"gitRemote"`
	// GithubToken authorizes the pull-request triggers. Empty falls back to
	// GITHUB_TOKEN in the environment; without either, only public repositories
	// answer, and slowly (60 requests an hour).
	GithubToken string `json:"githubToken,omitempty"`

	// WorktreeMode controls where sessions run: "always" (default) — a
	// dedicated git worktree per session, which is what gives a card its own
	// branch to show and to deploy; "never" — directly in the repository
	// working tree, with concurrent sessions per repo rejected. A smarter
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
	// PlanningTools is the policy for planning sessions; empty means the
	// built-in read-only set.
	PlanningTools []string `json:"planningTools,omitempty"`
	ShowThoughts  bool     `json:"showThoughts"`
	// DebugLog records every ACP message to DebugLogPath (default
	// <dataDir>/acp-debug.jsonl). Also switched on by FOCALBOARD_ACP_DEBUG.
	DebugLog            bool   `json:"debugLog,omitempty"`
	DebugLogPath        string `json:"debugLogPath,omitempty"`
	WorktreeDir         string `json:"worktreeDir"`
	KeepFailedWorktrees bool   `json:"keepFailedWorktrees"`
}

// The column a card is dropped into to hand it to an agent. Work starts where
// work normally starts on a board, rather than in a lane invented for agents.
const (
	DefaultTriggerColumn = "In Progress"
	// legacyTriggerColumn is the column earlier versions triggered on; configs
	// still carrying it are migrated on load.
	legacyTriggerColumn = "To Agent"
)

// DefaultConfig returns the defaults written on first run. dataDir is the ACP
// data directory (worktrees live under it).
func DefaultConfig(dataDir string) Config {
	return Config{
		Enabled:                  true,
		AgentMode:                "claude",
		TriggerProperty:          "Status",
		TriggerColumn:            DefaultTriggerColumn,
		DeployColumn:             "Deploy",
		TestColumn:               "To Test",
		TestPassColumn:           "Tested",
		TestFailColumn:           "Failed",
		RepoWhitelist:            []string{},
		Repos:                    []RepoEntry{},
		Agents:                   []AgentEntry{},
		Proxies:                  []ProxyEntry{},
		Deploys:                  []DeployEntry{},
		Columns:                  []ColumnSpec{},
		Flows:                    []FlowEntry{},
		DeployPrompt:             DefaultDeployPrompt,
		TestPrompt:               DefaultTestPrompt,
		WorktreeMode:             "always",
		MaxConcurrent:            3,
		SessionTimeoutMinutes:    15,
		TestTimeoutMinutes:       30,
		SessionIdleMinutes:       30,
		ShowThoughts:             true,
		PermissionTimeoutMinutes: 5,
		IdempotencyWindowSeconds: 10,
		// Bash is on the list because a coding agent cannot do its job without a
		// shell (tests, git, build), and a session with no console open has
		// nobody to ask — every prompt would simply be rejected. Edit/Write are
		// already allowed, so withholding the shell bought little in practice.
		// The dokku tools are on the list for the same reason: a deploy started
		// by a card move usually has no console watching, and asking nobody
		// means rejecting. destroy_deployment is deliberately absent — deleting
		// an environment is always worth a human answer.
		AutoAllowTools: []string{
			"Read", "Grep", "Glob", "Edit", "Write", "MultiEdit", "NotebookEdit", "TodoWrite", "Bash", "Skill",
			"mcp__dokku__deploy_branch", "mcp__dokku__app_logs",
			"mcp__dokku__deployment_status", "mcp__dokku__list_deployments",
		},
		VCSPollSeconds: 60,
		GitRemote:      "origin",
		WorktreeDir:    filepath.Join(dataDir, "worktrees"),
		ArtifactsDir:   filepath.Join(dataDir, "artifacts"),
	}
}

// DefaultDeployPrompt is the task text a deploy session starts with.
const DefaultDeployPrompt = `Задача: опубликовать ветку этой карточки на Dokku.

Делай это только инструментами mcp__dokku__*: deploy_branch публикует ветку,
app_logs показывает логи сборки и приложения, deployment_status — состояние
процессов. Не запускай ssh и git push руками и не переключай ветки.

Если сборка упала: прочитай логи, назови причину и почини её, только если
исправление очевидно и относится к деплою (Procfile, переменные окружения,
конфиг сборки). Не переписывай логику приложения — вместо этого опиши проблему.

В конце ответа дай URL превью.`

// DefaultTestPrompt is the task text a test session starts with. It is written
// for a tester, not a developer: the job is to find what is broken on the
// preview, not to fix it.
const DefaultTestPrompt = `Задача: проверить в браузере превью этой карточки — вместо ручного тестировщика.

Сценарий бери из описания карточки: что должно было измениться, то и проверяй,
плюс убедись, что рядом ничего не развалилось. Браузер води инструментами
браузерного MCP-сервера, который у тебя есть (mcp__…__browser_navigate,
browser_snapshot, browser_click, browser_type и прочие): snapshot показывает
страницу текстом со ссылками на элементы, действия делаются по этим ссылкам.
После действия, меняющего страницу, бери новый snapshot — ссылки протухают.

Открывай только адрес превью, указанный ниже, и страницы под ним — на другие
хосты не ходи. Посмотри консоль и сетевые запросы: ошибки JS и упавшие запросы
— это дефекты, даже если внешне всё нарисовалось. Делай скриншоты на ключевых
шагах и на каждом найденном дефекте, сохраняя их в каталог screenshots рядом
с отчётом: они попадут в карточку.

Ничего не чини и не меняй код — ты тестируешь. В самом конце запиши отчёт
в файл result.json (путь указан ниже) — без него результат прогона не
засчитывается:

{"verdict": "pass|fail|blocked", "summary": "итог в одну-две фразы",
 "steps": ["что проделал, по шагам"], "bugs": ["что ожидалось и что произошло"]}

pass — сценарий прошёл, fail — есть дефекты (перечисли их в bugs),
blocked — проверить не удалось (превью не открывается, нет доступа).`

// TestTimeout bounds one test turn. A browser scenario takes much longer than a
// code edit, so it has its own budget instead of SessionTimeoutMinutes.
func (c Config) TestTimeout() time.Duration {
	if c.TestTimeoutMinutes <= 0 {
		return c.SessionTimeout()
	}
	return time.Duration(c.TestTimeoutMinutes) * time.Minute
}

// LoadConfig reads path, creating it with defaults when absent.
func LoadConfig(path, dataDir string) (Config, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		// A new install seeds no routes: the board brings its own (the "My
		// Project Tasks" template ships them), and the editor offers the same
		// ones to a board that does not. Columns are still derived from the
		// trigger-column keys, so a hand-made board behaves as it always did.
		cfg := withColumns(DefaultConfig(dataDir))
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
	// An existing config keeps whatever it says, so the old default would live
	// on forever in installs that never touched it. Only the abandoned default
	// is rewritten; a column the user chose is left alone. It happens before the
	// routes are seeded, so their stages name the column cards land in now.
	if strings.EqualFold(strings.TrimSpace(cfg.TriggerColumn), legacyTriggerColumn) {
		cfg.TriggerColumn = DefaultTriggerColumn
	}
	// Seed the routes and the columns only when the file has no such key at
	// all. An empty list is a decision — the user deleted every route, or
	// cleared every column — and must survive restarts, which an emptiness
	// check could not tell from a config written before either existed.
	var probe struct {
		Flows   *[]FlowEntry  `json:"flows"`
		Columns *[]ColumnSpec `json:"columns"`
	}
	if err := json.Unmarshal(b, &probe); err != nil {
		probe.Flows, probe.Columns = nil, nil
	}
	if probe.Columns == nil {
		cfg = withColumns(cfg)
	}
	if probe.Flows == nil && len(cfg.Columns) > 0 {
		// An install that predates flows keeps the routes it would have been
		// given then; a fresh one gets them from its board instead.
		cfg = withTemplateFlows(cfg)
	}
	return cfg, nil
}

// withColumns fills the column registry from the trigger-column keys the config
// already carries, so an install that predates it keeps behaving exactly as it
// did: the trigger column runs an agent, the deploy column deploys, the test
// column tests. The keys stay in the file as the seed; from here on the
// registry is what the trigger loop reads.
func withColumns(cfg Config) Config {
	if len(cfg.Columns) == 0 {
		cfg.Columns = migratedColumns(cfg)
	}
	return cfg
}

// withTemplateFlows seeds the registry with the template routes, built from the
// trigger columns the config already names. It runs after unmarshalling so the
// routes reflect the user's own column names rather than the defaults.
func withTemplateFlows(cfg Config) Config {
	if flows := TemplateFlows(cfg); len(flows) > 0 {
		cfg.Flows = flows
	}
	return cfg
}

// GithubTokenValue is the token to authorize pull-request polling with: the
// configured one, else whatever the environment already holds.
func (c Config) GithubTokenValue() string {
	if t := strings.TrimSpace(c.GithubToken); t != "" {
		return t
	}
	return strings.TrimSpace(os.Getenv("GITHUB_TOKEN"))
}

// VCSPoll is how often repositories are polled; zero turns watching off.
func (c Config) VCSPoll() time.Duration {
	if c.VCSPollSeconds <= 0 {
		return 0
	}
	return time.Duration(c.VCSPollSeconds) * time.Second
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

// ToolAllowed reports whether the call runs without asking, under the global
// policy. input is the tool's raw input, which entries carrying an argument
// pattern are matched against; pass nil when it is not available.
func (c Config) ToolAllowed(toolName string, input any) bool {
	return ToolPolicy(c.AutoAllowTools).Allows(toolName, input)
}

// PlanningPolicy is what a planning session may do unasked. It is deliberately
// separate from AutoAllowTools: planning reads a repository to argue about it,
// so it needs to look around freely but must not be able to change anything,
// whatever the global policy has been relaxed to.
func (c Config) PlanningPolicy() ToolPolicy {
	if len(c.PlanningTools) > 0 {
		return ToolPolicy(c.PlanningTools)
	}
	return ToolPolicy(defaultPlanningTools())
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
