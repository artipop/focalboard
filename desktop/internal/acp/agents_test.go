package acp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func agentManager(t *testing.T, cfgPath string, agents ...AgentEntry) *Manager {
	t.Helper()
	cfg := DefaultConfig(t.TempDir())
	cfg.Agents = agents
	return NewManager(cfg, cfgPath, nil, newFakeWriter(), &fakeEmitter{}, nil)
}

func TestAddUpdateRemoveAgentPersists(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	m := agentManager(t, cfgPath)

	if _, err := m.AddAgent(AgentEntry{Name: "codex-a", Kind: "codex", Env: map[string]string{"CODEX_HOME": "/tmp/a"}}); err != nil {
		t.Fatal(err)
	}

	// Empty name and unknown kind are rejected.
	if _, err := m.AddAgent(AgentEntry{Name: "", Kind: "claude"}); err == nil {
		t.Error("empty name accepted")
	}
	if _, err := m.AddAgent(AgentEntry{Name: "bad", Kind: "gemini"}); err == nil {
		t.Error("unknown kind accepted")
	}
	// Duplicate name (case-insensitive) rejected.
	if _, err := m.AddAgent(AgentEntry{Name: "CODEX-A", Kind: "codex"}); err == nil {
		t.Error("duplicate name accepted")
	}

	// Persisted and reloadable.
	loaded, err := LoadConfig(cfgPath, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Agents) != 1 || loaded.Agents[0].Env["CODEX_HOME"] != "/tmp/a" {
		t.Fatalf("agent not persisted: %+v", loaded.Agents)
	}

	// Update replaces fields for the matching name.
	if _, err := m.UpdateAgent(AgentEntry{Name: "codex-a", Kind: "codex", Model: "gpt-5"}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.UpdateAgent(AgentEntry{Name: "missing", Kind: "codex"}); err == nil {
		t.Error("updating missing agent should fail")
	}
	loaded, _ = LoadConfig(cfgPath, t.TempDir())
	if loaded.Agents[0].Model != "gpt-5" {
		t.Fatalf("update not persisted: %+v", loaded.Agents)
	}

	if err := m.RemoveAgent("codex-a"); err != nil {
		t.Fatal(err)
	}
	if err := m.RemoveAgent("codex-a"); err == nil {
		t.Error("removing missing agent should fail")
	}
	loaded, _ = LoadConfig(cfgPath, t.TempDir())
	if len(loaded.Agents) != 0 {
		t.Fatalf("removal not persisted: %+v", loaded.Agents)
	}
}

func TestAgentKindValidation(t *testing.T) {
	m := agentManager(t, "")

	// The ACP-native kinds we know how to launch need no command; the generic
	// acp kind does.
	for _, kind := range []string{"antigravity", "copilot", "junie"} {
		if _, err := m.AddAgent(AgentEntry{Name: kind, Kind: kind}); err != nil {
			t.Errorf("%s without command should be valid: %v", kind, err)
		}
	}
	if _, err := m.AddAgent(AgentEntry{Name: "gen", Kind: "acp"}); err == nil {
		t.Error("acp kind without command should be rejected")
	}
	if _, err := m.AddAgent(AgentEntry{Name: "gem", Kind: "acp", Command: []string{"gemini", "--acp"}}); err != nil {
		t.Errorf("acp kind with command should be valid: %v", err)
	}
}

func TestExternalACPCommand(t *testing.T) {
	m := agentManager(t, "")

	// Explicit command overrides everything and appends Args.
	argv, err := m.externalACPCommand(AgentEntry{Name: "gem", Kind: "acp", Command: []string{"gemini", "--acp"}, Args: []string{"--yolo"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(argv, " "); got != "gemini --acp --yolo" {
		t.Errorf("acp command argv = %q", got)
	}

	// A known ACP-native kind with an explicit binPath defaults to
	// `<bin> <acp flag>` + model. Junie's flag takes a boolean value.
	bin := writeFakeClaude(t, "#!/bin/sh\n") // any existing executable
	for kind, want := range map[string]string{
		"antigravity": bin + " --acp --model m1",
		"copilot":     bin + " --acp --model m1",
		"junie":       bin + " --acp=true --model m1",
	} {
		argv, err = m.externalACPCommand(AgentEntry{Name: "g", Kind: kind, BinPath: bin, Model: "m1"})
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Join(argv, " "); got != want {
			t.Errorf("%s argv = %q, want %q", kind, got, want)
		}

		// A missing binary errors clearly, naming the CLI.
		_, err = m.externalACPCommand(AgentEntry{Name: "g", Kind: kind, BinPath: "/no/such/" + kind})
		if err == nil {
			t.Errorf("missing %s binary should error", kind)
		}
	}
}

func TestAgentLaunchArgvCustomCommand(t *testing.T) {
	m := agentManager(t, "")
	bin := writeFakeClaude(t, "#!/bin/sh\n")

	// No command: the resolved binary, as before.
	argv, err := agentLaunchArgv(AgentEntry{Name: "c", Kind: "claude", BinPath: bin}, m.resolveClaudeBin)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(argv, " "); got != bin {
		t.Errorf("launch argv = %q, want %q", got, bin)
	}

	// A wrapper command replaces the binary and keeps its own args; the bridge
	// appends its protocol flags after it.
	wrapper := []string{"/bin/sh", "-c", "exec " + bin}
	argv, err = agentLaunchArgv(AgentEntry{Name: "c", Kind: "claude", BinPath: bin, Command: wrapper}, m.resolveClaudeBin)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(argv, " "); got != strings.Join(wrapper, " ") {
		t.Errorf("wrapped launch argv = %q", got)
	}

	// A missing binary still errors clearly when no command is set.
	if _, err := agentLaunchArgv(AgentEntry{Name: "c", Kind: "codex", BinPath: "/no/such/codex"}, m.resolveCodexBin); err == nil {
		t.Error("missing codex binary should error")
	}
}

func TestAgentSpawnEnvProxy(t *testing.T) {
	envOf := func(a AgentEntry, net NetworkSettings) (map[string]string, map[string]bool) {
		env, drop := spawnEnv(a, net)
		vals := map[string]string{}
		for _, kv := range env {
			eq := strings.Index(kv, "=")
			vals[kv[:eq]] = kv[eq+1:] // later entries win, as they do in the child env
		}
		dropped := map[string]bool{}
		for _, k := range drop {
			dropped[k] = true
		}
		return vals, dropped
	}

	// A proxy expands to both cases and manages NO_PROXY as its pair.
	vals, dropped := envOf(AgentEntry{}, NetworkSettings{Proxy: "http://proxy.example.com:8080", NoProxy: "git.internal,localhost"})
	for _, k := range proxyEnvNames {
		if vals[k] != "http://proxy.example.com:8080" {
			t.Errorf("%s = %q, want the proxy URL", k, vals[k])
		}
		if !dropped[k] {
			t.Errorf("%s should be dropped from the inherited env", k)
		}
	}
	if vals["NO_PROXY"] != "git.internal,localhost" || vals["no_proxy"] != "git.internal,localhost" {
		t.Errorf("NO_PROXY = %q/%q", vals["NO_PROXY"], vals["no_proxy"])
	}

	// A CA bundle reaches every runtime's variable.
	vals, _ = envOf(AgentEntry{}, NetworkSettings{CACert: "/etc/my-ca.pem"})
	for _, k := range caCertEnvNames {
		if vals[k] != "/etc/my-ca.pem" {
			t.Errorf("%s = %q, want the CA path", k, vals[k])
		}
	}
	if _, ok := vals["HTTPS_PROXY"]; ok {
		t.Error("no proxy configured, HTTPS_PROXY should be left alone")
	}

	// The explicit env map wins over the expanded settings, including blanking
	// a proxy out; unrelated inherited proxy vars are still overridden.
	vals, dropped = envOf(
		AgentEntry{Env: map[string]string{"HTTPS_PROXY": "", "CODEX_HOME": "/tmp/a"}},
		NetworkSettings{Proxy: "http://proxy.example.com:8080"},
	)
	if vals["HTTPS_PROXY"] != "" {
		t.Errorf("Env should override the expanded proxy, got %q", vals["HTTPS_PROXY"])
	}
	if vals["HTTP_PROXY"] != "http://proxy.example.com:8080" || vals["CODEX_HOME"] != "/tmp/a" {
		t.Errorf("unexpected env: %v", vals)
	}
	if !dropped["CODEX_HOME"] || !dropped["HTTPS_PROXY"] {
		t.Error("agent env keys must be dropped from the inherited env")
	}

	// NoProxy alone does not invent a proxy.
	vals, _ = envOf(AgentEntry{}, NetworkSettings{NoProxy: "*.internal"})
	if vals["NO_PROXY"] != "*.internal" {
		t.Errorf("NO_PROXY = %q", vals["NO_PROXY"])
	}
	if _, ok := vals["ALL_PROXY"]; ok {
		t.Error("ALL_PROXY set without a proxy")
	}
}

func TestResolveAgentByOption(t *testing.T) {
	m := agentManager(t, "",
		AgentEntry{Name: "claude", Kind: "claude"},
		AgentEntry{Name: "codex-acct1", Kind: "codex"},
	)

	got, err := m.resolveAgent(CardMoved{OptionNames: []string{"urgent", "codex-acct1"}, Props: map[string]string{}})
	if err != nil || got.Name != "codex-acct1" {
		t.Fatalf("option match failed: got=%+v err=%v", got, err)
	}

	// Explicit `agent` property wins.
	got, err = m.resolveAgent(CardMoved{OptionNames: []string{"codex-acct1"}, Props: map[string]string{"agent": "claude"}})
	if err != nil || got.Name != "claude" {
		t.Fatalf("explicit agent property failed: got=%+v err=%v", got, err)
	}

	// Unknown explicit agent errors, listing the registry.
	_, err = m.resolveAgent(CardMoved{Props: map[string]string{"agent": "nope"}})
	if err == nil || !strings.Contains(err.Error(), "codex-acct1") {
		t.Errorf("expected error listing agents, got %v", err)
	}

	// Ambiguous (multiple agents, no selection) errors.
	if _, err := m.resolveAgent(CardMoved{Props: map[string]string{}}); err == nil {
		t.Error("ambiguous agent selection should error")
	}
}

func TestResolveAgentByAssignee(t *testing.T) {
	m := agentManager(t, "",
		AgentEntry{Name: "claude", Kind: "claude"},
		AgentEntry{Name: "Codex Acct1", Kind: "codex"},
	)

	// The assignee's username routes the card; the account carries the folded
	// form of the registry name.
	got, err := m.resolveAgent(CardMoved{PersonNames: []string{"artem", "codex-acct1"}, Props: map[string]string{}})
	if err != nil || got.Name != "Codex Acct1" {
		t.Fatalf("assignee match failed: got=%+v err=%v", got, err)
	}

	// An assignee outranks a tag: it is the more deliberate choice.
	got, err = m.resolveAgent(CardMoved{
		PersonNames: []string{"claude"},
		OptionNames: []string{"Codex Acct1"},
		Props:       map[string]string{},
	})
	if err != nil || got.Name != "claude" {
		t.Fatalf("assignee should win over the option: got=%+v err=%v", got, err)
	}

	// The explicit property still wins over both.
	got, err = m.resolveAgent(CardMoved{
		PersonNames: []string{"claude"},
		Props:       map[string]string{"agent": "codex-acct1"},
	})
	if err != nil || got.Name != "Codex Acct1" {
		t.Fatalf("explicit agent property should win: got=%+v err=%v", got, err)
	}

	// A human assignee is simply not an agent.
	if _, err := m.resolveAgent(CardMoved{PersonNames: []string{"artem"}, Props: map[string]string{}}); err == nil {
		t.Error("a non-agent assignee should not resolve an agent")
	}
}

func TestAgentUsername(t *testing.T) {
	for in, want := range map[string]string{
		"claude":       "claude",
		"Codex Acct1":  "codex-acct1",
		"  My Agent  ": "my-agent",
		"claude/main":  "claude-main",
		"agent.two_3":  "agent.two_3",
		"---":          "",
		"":             "",
	} {
		if got := AgentUsername(in); got != want {
			t.Errorf("AgentUsername(%q) = %q, want %q", in, got, want)
		}
	}
}

// fakeBoardUsers records what the manager asked to provision and retire.
type fakeBoardUsers struct {
	boardID  string
	agents   []AgentUser
	retired  []AgentUser
	err      error
	retryErr error
}

func (f *fakeBoardUsers) RetireAgentUser(_ context.Context, agent AgentUser) (int, error) {
	if f.retryErr != nil {
		return 0, f.retryErr
	}
	f.retired = append(f.retired, agent)
	return 1, nil
}

func (f *fakeBoardUsers) EnsureAgentUsers(_ context.Context, boardID string, agents []AgentUser) ([]AgentUser, error) {
	f.boardID = boardID
	f.agents = agents
	if f.err != nil {
		return nil, f.err
	}
	out := make([]AgentUser, 0, len(agents))
	for i, a := range agents {
		a.UserID = fmt.Sprintf("uid-%d", i)
		a.Created = true
		out = append(out, a)
	}
	return out, nil
}

func TestSyncAgentUsers(t *testing.T) {
	m := agentManager(t, "", AgentEntry{Name: "Codex Acct1", Kind: "codex"}, AgentEntry{Name: "claude", Kind: "claude"})

	// Without a board-users implementation the feature is simply unavailable.
	if _, err := m.SyncAgentUsers(context.Background(), "board1"); err == nil {
		t.Error("expected an error without a BoardUsers implementation")
	}

	users := &fakeBoardUsers{}
	m.SetBoardUsers(users)
	got, err := m.SyncAgentUsers(context.Background(), "board1")
	if err != nil {
		t.Fatal(err)
	}
	if users.boardID != "board1" || len(got) != 2 {
		t.Fatalf("sync passed board=%q agents=%+v", users.boardID, got)
	}
	if got[0].Name != "Codex Acct1" || got[0].Username != "codex-acct1" {
		t.Errorf("first account = %+v, want the folded username", got[0])
	}
	if got[0].UserID == "" || !got[0].Created {
		t.Errorf("provisioning result not returned: %+v", got[0])
	}

	// An empty registry is a no-op, not an error: the UI syncs on every change,
	// including the ones that leave nothing to provision.
	empty := agentManager(t, "")
	empty.SetBoardUsers(&fakeBoardUsers{})
	synced, err := empty.SyncAgentUsers(context.Background(), "board1")
	if err != nil || len(synced) != 0 {
		t.Errorf("empty registry sync = %+v, err = %v", synced, err)
	}
}

func TestRemoveAgentRetiresItsAccount(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	users := &fakeBoardUsers{}
	m := agentManager(t, cfgPath, AgentEntry{Name: "Codex Acct1", Kind: "codex"})
	m.SetBoardUsers(users)

	if err := m.RemoveAgent("codex acct1"); err != nil { // name match is loose
		t.Fatal(err)
	}
	if len(m.Agents()) != 0 {
		t.Errorf("entry not removed: %+v", m.Agents())
	}
	if len(users.retired) != 1 || users.retired[0].Username != "codex-acct1" {
		t.Fatalf("account not retired: %+v", users.retired)
	}

	// A retirement that fails is reported, but the entry stays removed — the
	// registry is the source of truth and it has already been written.
	m2 := agentManager(t, cfgPath, AgentEntry{Name: "claude", Kind: "claude"})
	m2.SetBoardUsers(&fakeBoardUsers{retryErr: fmt.Errorf("board app is not ready")})
	err := m2.RemoveAgent("claude")
	if err == nil || !strings.Contains(err.Error(), "board app is not ready") {
		t.Errorf("expected the retirement failure to surface, got %v", err)
	}
	if len(m2.Agents()) != 0 {
		t.Errorf("entry should still be removed: %+v", m2.Agents())
	}

	// Removing an unknown agent is still an error, and touches no account.
	if err := m.RemoveAgent("nope"); err == nil {
		t.Error("removing a missing agent should fail")
	}
	if len(users.retired) != 1 {
		t.Errorf("a failed removal must not retire anything: %+v", users.retired)
	}
}

func TestResolveAgentSingleAndFallback(t *testing.T) {
	// Exactly one agent → used without a card selection.
	m := agentManager(t, "", AgentEntry{Name: "only", Kind: "codex"})
	got, err := m.resolveAgent(CardMoved{Props: map[string]string{}})
	if err != nil || got.Name != "only" {
		t.Fatalf("single-agent resolution failed: got=%+v err=%v", got, err)
	}

	// Empty registry → synthesized from AgentMode (default claude).
	m2 := agentManager(t, "")
	got, err = m2.resolveAgent(CardMoved{Props: map[string]string{}})
	if err != nil || got.Kind != AgentKindClaude {
		t.Fatalf("empty-registry fallback failed: got=%+v err=%v", got, err)
	}

	// Empty registry with acp-command mode → external kind.
	m3 := agentManager(t, "")
	m3.cfg.AgentMode = "acp-command"
	got, _ = m3.resolveAgent(CardMoved{Props: map[string]string{}})
	if got.Kind != "acp-command" {
		t.Fatalf("expected acp-command fallback, got %+v", got)
	}
}

func TestLoadConfigMigratesLegacyTriggerColumn(t *testing.T) {
	dir := t.TempDir()
	write := func(triggerColumn string) Config {
		t.Helper()
		path := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(path, []byte(`{"triggerColumn":"`+triggerColumn+`"}`), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg, err := LoadConfig(path, dir)
		if err != nil {
			t.Fatal(err)
		}
		return cfg
	}

	// The abandoned default is rewritten, whatever its case…
	if got := write("To Agent").TriggerColumn; got != DefaultTriggerColumn {
		t.Errorf("legacy column = %q, want %q", got, DefaultTriggerColumn)
	}
	if got := write("to agent").TriggerColumn; got != DefaultTriggerColumn {
		t.Errorf("legacy column (lowercase) = %q, want %q", got, DefaultTriggerColumn)
	}
	// …but a column the user picked is theirs.
	if got := write("Ready for agent").TriggerColumn; got != "Ready for agent" {
		t.Errorf("custom column was rewritten to %q", got)
	}
}

func TestAgentMCPServersValidation(t *testing.T) {
	ok, err := validateAgent(AgentEntry{Name: "jojo", Kind: "junie", MCPServers: []AgentMCPServer{
		{Name: "playwright", Command: []string{"npx", " -y ", "@playwright/mcp@latest", "  "}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	// Stray whitespace would become an empty argv element and a puzzling exec error.
	if got := strings.Join(ok.MCPServers[0].Command, "|"); got != "npx|-y|@playwright/mcp@latest" {
		t.Errorf("command not cleaned: %q", got)
	}

	bad := []struct {
		why    string
		server AgentMCPServer
	}{
		{"без имени", AgentMCPServer{Command: []string{"npx"}}},
		{"без команды", AgentMCPServer{Name: "playwright"}},
		{"имя не годится в префикс инструмента", AgentMCPServer{Name: "play wright", Command: []string{"npx"}}},
		{"имя занято встроенным сервером", AgentMCPServer{Name: "dokku", Command: []string{"npx"}}},
	}
	for _, c := range bad {
		if _, err := validateAgent(AgentEntry{Name: "a", Kind: "claude", MCPServers: []AgentMCPServer{c.server}}); err == nil {
			t.Errorf("принят сервер %s: %+v", c.why, c.server)
		}
	}
	if _, err := validateAgent(AgentEntry{Name: "a", Kind: "claude", MCPServers: []AgentMCPServer{
		{Name: "pw", Command: []string{"npx"}}, {Name: "PW", Command: []string{"npx"}},
	}}); err == nil {
		t.Error("принято два сервера с одним именем")
	}
}

func TestAgentMCPServersTravelWithEverySession(t *testing.T) {
	agent := AgentEntry{Name: "jojo", Kind: "junie", MCPServers: []AgentMCPServer{
		{Name: "playwright", Command: []string{"npx", "-y", "@playwright/mcp@latest", "--headless"}, Env: map[string]string{"X": "1"}},
	}}
	cfg := DefaultConfig(t.TempDir())

	// An ordinary card task gets the agent's own server even though the card
	// itself configures none.
	s := &Session{RepoPath: "/repo", Agent: agent}
	specs, err := sessionMCPServers(s, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(specs) != 1 || specs[0].Name != "playwright" || specs[0].Command != "npx" {
		t.Fatalf("specs: %+v", specs)
	}
	if strings.Join(specs[0].Args, " ") != "-y @playwright/mcp@latest --headless" || specs[0].Env["X"] != "1" {
		t.Errorf("argv/env lost: %+v", specs[0])
	}
	// Wiring a server in is consent to use it: an unattended session has nobody
	// to ask, and the tool names only exist at run time.
	if !s.toolPrefixAllowed("mcp__playwright__browser_click") {
		t.Error("tools of a configured server should run unasked")
	}
	if s.toolPrefixAllowed("mcp__other__thing") {
		t.Error("only the configured server's tools may be allowed")
	}

	// A deploy session keeps ours first and appends the agent's.
	target := deployEntry("prod")
	deploySession := &Session{RepoPath: "/repo", Agent: agent, Deploy: &target, DeployBranch: "feat/x"}
	specs, err = sessionMCPServers(deploySession, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(specs) != 2 || specs[0].Name != "dokku" || specs[1].Name != "playwright" {
		t.Fatalf("deploy session specs: %+v", specs)
	}
}
