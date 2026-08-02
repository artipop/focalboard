package acp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mattermost/focalboard/desktop/internal/dokku"
)

func deployEntry(name string) DeployEntry {
	return DeployEntry{
		Name: name,
		Target: dokku.Target{
			SSHHost:    "dokku.example.com",
			BaseApp:    "api",
			BaseDomain: "example.com",
		},
	}
}

func TestAddUpdateRemoveDeployPersists(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	m := agentManager(t, cfgPath)

	if _, err := m.AddDeploy(deployEntry("staging")); err != nil {
		t.Fatal(err)
	}
	if _, err := m.AddDeploy(deployEntry("STAGING")); err == nil {
		t.Error("duplicate name accepted")
	}

	// The Dokku half is validated through dokku.Target.
	bad := deployEntry("broken")
	bad.SSHHost = ""
	if _, err := m.AddDeploy(bad); err == nil {
		t.Error("target without an ssh host accepted")
	}
	missingKey := deployEntry("keyed")
	missingKey.SSHKey = filepath.Join(t.TempDir(), "nope.pem")
	if _, err := m.AddDeploy(missingKey); err == nil {
		t.Error("missing ssh key accepted")
	}

	updated := deployEntry("staging")
	updated.SSHUser = "deployer"
	if _, err := m.UpdateDeploy(updated); err != nil {
		t.Fatal(err)
	}
	if _, err := m.UpdateDeploy(deployEntry("ghost")); err == nil {
		t.Error("update of an unknown target accepted")
	}

	loaded, err := LoadConfig(cfgPath, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Deploys) != 1 || loaded.Deploys[0].SSHUser != "deployer" {
		t.Fatalf("config did not persist the update: %+v", loaded.Deploys)
	}

	if err := m.RemoveDeploy("STAGING"); err != nil {
		t.Fatal(err)
	}
	if len(m.Deploys()) != 0 {
		t.Errorf("remove left %v", m.Deploys())
	}
	if err := m.RemoveDeploy("staging"); err == nil {
		t.Error("removing a missing target should fail")
	}
}

func TestResolveDeployTarget(t *testing.T) {
	m := agentManager(t, "")
	prod := deployEntry("prod")
	preview := deployEntry("preview")
	m.cfg.Deploys = []DeployEntry{prod, preview}

	// A card option naming a target wins.
	got, err := m.resolveDeployTarget(CardMoved{OptionNames: []string{"webapp", "prod"}})
	if err != nil || got.Name != "prod" {
		t.Fatalf("option match: %+v, %v", got, err)
	}
	// With several hosts and nothing naming one, guessing would be wrong.
	if _, err := m.resolveDeployTarget(CardMoved{}); err == nil {
		t.Error("two unrelated targets should not resolve")
	}
	// A single registered target is the answer by default, which is the usual
	// case now that a target is a host rather than a per-repository setting.
	m.cfg.Deploys = []DeployEntry{prod}
	got, err = m.resolveDeployTarget(CardMoved{})
	if err != nil || got.Name != "prod" {
		t.Fatalf("single target: %+v, %v", got, err)
	}

	m.cfg.Deploys = nil
	if _, err := m.resolveDeployTarget(CardMoved{}); err == nil {
		t.Error("an empty registry should be an error")
	}
}

func TestResolveDeployNamesTheAppAfterTheRepository(t *testing.T) {
	repo := initTestRepo(t)
	m := agentManager(t, "")
	entry := deployEntry("preview")
	entry.Target.BaseApp = "" // the ordinary case: one target, many repositories
	m.cfg.Deploys = []DeployEntry{entry}
	m.cfg.Repos = []RepoEntry{{Name: "My Webapp", Path: repo}}

	got, branch, err := m.resolveDeploy(CardMoved{}, repo, true, "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Target.BaseApp != "my-webapp" {
		t.Errorf("base app %q, want the registry name folded", got.Target.BaseApp)
	}
	if host := got.Target.Domain("feat"); host != "my-webapp-feat.example.com" {
		t.Errorf("hostname %q", host)
	}
	if branch != "main" {
		t.Errorf("branch %q", branch)
	}

	// An unregistered repository is named after its directory.
	m.cfg.Repos = nil
	got, _, err = m.resolveDeploy(CardMoved{}, repo, true, "")
	if err != nil {
		t.Fatal(err)
	}
	if want := dokku.AppLabel(filepath.Base(repo)); got.Target.BaseApp != want {
		t.Errorf("base app %q, want %q from the directory name", got.Target.BaseApp, want)
	}

	// An explicit name is left alone, and an ordinary session resolves nothing.
	entry.Target.BaseApp = "api"
	m.cfg.Deploys = []DeployEntry{entry}
	got, _, err = m.resolveDeploy(CardMoved{}, repo, true, "")
	if err != nil || got.Target.BaseApp != "api" {
		t.Errorf("explicit base app: %+v, %v", got.Target, err)
	}
	if d, b, err := m.resolveDeploy(CardMoved{}, repo, false, ""); d != nil || b != "" || err != nil {
		t.Errorf("a non-deploy session resolved a target: %+v, %q, %v", d, b, err)
	}
}

func TestResolveDeployBranch(t *testing.T) {
	repo := initTestRepo(t)

	// The card property wins, so a card can deploy a branch that is not checked out.
	branch, err := resolveDeployBranch(CardMoved{Props: map[string]string{"branch": "feat/x"}}, repo)
	if err != nil || branch != "feat/x" {
		t.Fatalf("card branch: %q, %v", branch, err)
	}
	branch, err = resolveDeployBranch(CardMoved{}, repo)
	if err != nil || branch != "main" {
		t.Fatalf("checked-out branch: %q, %v", branch, err)
	}
}

// A stage names the crew that may work it; the card chooses among them, and an
// agent who is not on the crew does not get the card.
func TestResolveSessionAgentPrefersTheStagesCrew(t *testing.T) {
	m := agentManager(t, "", AgentEntry{Name: "claude-1", Kind: "claude"}, AgentEntry{Name: "deployer", Kind: "codex"})

	agent, busy, err := m.resolveSessionAgent(CardMoved{OptionNames: []string{"claude-1"}}, []string{"deployer"})
	if err != nil || busy || agent.Name != "deployer" {
		t.Fatalf("the crew should decide: %+v, %v, %v", agent, busy, err)
	}

	// On the crew, the card's own choice stands.
	agent, _, err = m.resolveSessionAgent(CardMoved{OptionNames: []string{"claude-1"}}, []string{"claude-1", "deployer"})
	if err != nil || agent.Name != "claude-1" {
		t.Fatalf("card agent ignored: %+v, %v", agent, err)
	}

	// Without a crew the card decides, exactly as before.
	agent, _, err = m.resolveSessionAgent(CardMoved{OptionNames: []string{"claude-1"}}, nil)
	if err != nil || agent.Name != "claude-1" {
		t.Fatalf("card agent ignored without a crew: %+v, %v", agent, err)
	}

	if _, _, err := m.resolveSessionAgent(CardMoved{}, []string{"gone"}); err == nil {
		t.Error("a crew of unregistered agents should fail")
	}
}

func TestComposeDeployPromptCarriesTheFacts(t *testing.T) {
	target := deployEntry("prod")
	prompt := composeDeployPrompt(
		CardMoved{Title: "Логин через SSO", Body: "детали"},
		AgentEntry{Name: "claude", Kind: "claude", Prompt: "агентский промпт"},
		"системный промпт", "", target, "feature/SSO",
	)
	for _, want := range []string{
		"системный промпт", "агентский промпт",
		"mcp__dokku__", // the default deploy prompt
		"Логин через SSO", "feature/SSO", "prod",
		"api-feature-sso", "http://api-feature-sso.example.com", "детали",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt is missing %q:\n%s", want, prompt)
		}
	}
}

func TestSessionMCPServersOnlyForDeploySessions(t *testing.T) {
	if specs, err := sessionMCPServers(&Session{RepoPath: "/repo"}, Config{}); err != nil || specs != nil {
		t.Fatalf("an ordinary session must get no MCP servers: %+v, %v", specs, err)
	}

	target := deployEntry("prod")
	target.SSHKey = "/keys/id_ed25519"
	specs, err := sessionMCPServers(&Session{RepoPath: "/repo", Deploy: &target, DeployBranch: "feat/x"}, Config{})
	if err != nil {
		t.Fatal(err)
	}
	if len(specs) != 1 || specs[0].Name != "dokku" {
		t.Fatalf("specs: %+v", specs)
	}
	spec := specs[0]
	self, _ := os.Executable()
	if spec.Command != self || strings.Join(spec.Args, " ") != "mcp dokku" {
		t.Errorf("the server must be this binary re-invoked: %s %v", spec.Command, spec.Args)
	}
	if spec.Env[dokku.EnvRepo] != "/repo" || spec.Env[dokku.EnvBranch] != "feat/x" {
		t.Errorf("env: %v", spec.Env)
	}
	var decoded dokku.Target
	if err := json.Unmarshal([]byte(spec.Env[dokku.EnvTarget]), &decoded); err != nil {
		t.Fatalf("target env is not valid JSON: %v", err)
	}
	if decoded.SSHKey != "/keys/id_ed25519" || decoded.BaseApp != "api" {
		t.Errorf("target lost fields in transit: %+v", decoded)
	}
}

func TestDeploySessionMayUseItsOwnTools(t *testing.T) {
	// Nobody is watching a card-triggered deploy, so the tools it was started
	// for have to be allowed without asking — including on an install whose
	// config predates them.
	allow := deployTools()
	for _, tool := range []string{"deploy_branch", "deployment_status", "app_logs", "list_deployments"} {
		if !allow["mcp__dokku__"+tool] {
			t.Errorf("%s is not allowed for a deploy session", tool)
		}
	}
	// Tearing a deployment down still asks.
	if allow["mcp__dokku__destroy_deployment"] {
		t.Error("destroy_deployment must not be auto-allowed")
	}

	// Both servers we configure ourselves answer the agent's "may I launch
	// MCP?" prompt, which some agents send with no tool name to match on.
	target := deployEntry("prod")
	cfg := DefaultConfig(t.TempDir())
	deploySession := &Session{RepoPath: "/repo", Deploy: &target, DeployBranch: "feat/x"}
	if _, err := sessionMCPServers(deploySession, cfg); err != nil {
		t.Fatal(err)
	}
	if !deploySession.usesOurMCP() {
		t.Error("the deploy session should know it was given an MCP server")
	}
	// A test session brings its own browser server; without one it is given
	// nothing, and startSession refuses it before it can start.
	testSession := &Session{RepoPath: "/repo", Test: &TestRun{URL: "http://preview.example.com", Artifacts: t.TempDir()}}
	specs, err := sessionMCPServers(testSession, cfg)
	if err != nil || len(specs) != 0 {
		t.Errorf("a test session without a browser server: %+v, %v", specs, err)
	}

	// An ordinary session gets neither.
	plain := &Session{RepoPath: "/repo"}
	if _, err := sessionMCPServers(plain, cfg); err != nil {
		t.Fatal(err)
	}
	if plain.usesOurMCP() {
		t.Error("an ordinary session must not be marked as having our MCP server")
	}
}

func TestMCPLaunchPromptIsOurs(t *testing.T) {
	// Junie asks this with no tool name at all, so the title is the whole
	// request; a name-based policy has nothing to match and would reject the
	// server we ourselves configured.
	for _, title := range []string{"Allow running MCP?", "allow launching mcp endpoint"} {
		if !isMCPLaunchPrompt(title, title) {
			t.Errorf("%q should read as an MCP launch prompt", title)
		}
	}
	if isMCPLaunchPrompt("Allow running this command?", "Allow running this command?") {
		t.Error("a shell prompt must stay the user's decision")
	}
	if isMCPLaunchPrompt("Bash", "git push") {
		t.Error("an ordinary tool call must stay the user's decision")
	}
}

func TestMCPServersForSessionNew(t *testing.T) {
	specs := []mcpServerSpec{{
		Name:    "dokku",
		Command: "/Applications/Focalboard.app/Contents/MacOS/Focalboard",
		Args:    []string{"mcp", "dokku"},
		Env:     map[string]string{"B_VAR": `{"json":"value"}`, "A_VAR": "1"},
	}}

	// One road for every kind: the protocol carries the server itself.
	servers := acpMCPServers(specs)
	if len(servers) != 1 || servers[0].Stdio == nil {
		t.Fatalf("acp servers: %+v", servers)
	}
	stdio := servers[0].Stdio
	if stdio.Name != "dokku" || stdio.Command != specs[0].Command || len(stdio.Env) != 2 {
		t.Errorf("acp stdio server: %+v", stdio)
	}
	if stdio.Args[0] != "mcp" || stdio.Args[1] != "dokku" {
		t.Errorf("acp stdio args: %+v", stdio.Args)
	}
	if stdio.Env[0].Name != "A_VAR" {
		t.Errorf("env order is not stable: %+v", stdio.Env)
	}
	if stdio.Env[1].Value != `{"json":"value"}` {
		t.Errorf("env value was mangled: %+v", stdio.Env[1])
	}
}

func deployMoveEvent(cardID, repo, column string) CardMoved {
	return CardMoved{
		EventID:    "ev-" + cardID + column,
		CardID:     cardID,
		BoardID:    "board1",
		Title:      "Deploy me",
		Props:      map[string]string{"repo_path": repo},
		FromColumn: Column{PropertyID: "p1", PropertyName: "Status", OptionID: "opt-backlog", Name: "Backlog"},
		ToColumn:   Column{PropertyID: "p1", PropertyName: "Status", OptionID: "opt-deploy", Name: column},
		At:         time.Now(),
	}
}

func TestDeployColumnStartsASessionWithTheDokkuTools(t *testing.T) {
	m, writer, events, repo := testManager(t, fakeClaudeRecordingArgs, func(c *Config) {
		c.Deploys = []DeployEntry{deployEntry("prod")}
	})

	events.ch <- deployMoveEvent("cardD", repo, "Deploy")

	waitFor(t, 15*time.Second, "deploy session done", func() bool {
		sessions, _, err := m.store.SessionsForCard("cardD")
		return err == nil && len(sessions) == 1 && sessions[0].Status == StatusDone
	})

	comments := writer.cardComments("cardD")
	if len(comments) == 0 || !strings.Contains(comments[0], "Деплой ветки `main`") {
		t.Fatalf("expected a deploy start comment, got %v", comments)
	}
	if !strings.Contains(comments[0], "http://api-main.example.com") {
		t.Errorf("start comment should announce the address: %q", comments[0])
	}
	all := strings.Join(comments, "\n")
	if !strings.Contains(all, "Сессия деплоя завершена") || !strings.Contains(all, "api-main") {
		t.Errorf("comments should summarize the deployment: %q", all)
	}
	// The fake agent never calls deploy_branch, so nothing was published — and
	// the card has to say so rather than let "the session finished" pass for
	// "the branch is live".
	if !strings.Contains(all, "Деплой не подтверждён") {
		t.Errorf("an unconfirmed deploy must be called out: %q", all)
	}

	// The session must have been given the dokku MCP server in session/new.
	servers, err := os.ReadFile(filepath.Join(fakeAgentDir(m.cfg.AgentCommand[0]), "mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(servers), dokku.ServerName) || !strings.Contains(string(servers), dokku.EnvTarget) {
		t.Errorf("the agent was not given the dokku MCP server:\n%s", servers)
	}

	// The branch it deploys is recorded, unlike an ordinary in-repo session.
	sessions, _, _ := m.store.SessionsForCard("cardD")
	if sessions[0].Branch != "main" {
		t.Errorf("branch not persisted: %q", sessions[0].Branch)
	}
}

func TestDeployColumnIgnoredWhenDisabled(t *testing.T) {
	m, writer, events, repo := testManager(t, fakeClaudeRecordingArgs, func(c *Config) {
		c.DeployColumn = ""
		c.Deploys = []DeployEntry{deployEntry("prod")}
	})

	events.ch <- deployMoveEvent("cardOff", repo, "Deploy")

	// Nothing should happen at all — give the trigger loop a moment to prove it.
	time.Sleep(500 * time.Millisecond)
	sessions, _, err := m.store.SessionsForCard("cardOff")
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 0 || len(writer.cardComments("cardOff")) != 0 {
		t.Fatalf("an empty deployColumn must disable the trigger: %v %v", sessions, writer.cardComments("cardOff"))
	}
}

func TestDeployWithoutTargetsCommentsOnTheCard(t *testing.T) {
	m, writer, events, repo := testManager(t, fakeClaudeRecordingArgs, nil)

	events.ch <- deployMoveEvent("cardNoTarget", repo, "Deploy")

	waitFor(t, 5*time.Second, "failure comment", func() bool {
		return len(writer.cardComments("cardNoTarget")) > 0
	})
	comment := writer.cardComments("cardNoTarget")[0]
	if !strings.Contains(comment, "Деплой не запущен") || !strings.Contains(comment, "Deploy targets") {
		t.Errorf("comment should say what to configure: %q", comment)
	}
	if sessions, _, _ := m.store.SessionsForCard("cardNoTarget"); len(sessions) != 0 {
		t.Errorf("no session should have started: %v", sessions)
	}
}

func TestStartDeployForCardPublishesTheGivenBranch(t *testing.T) {
	m, writer, _, repo, emitter := testManagerWithEmitter(t, fakeClaudeHappy, func(c *Config) {
		c.Deploys = []DeployEntry{{Name: "prod", Target: dokku.Target{SSHHost: "dokku.example.com"}}}
	})

	s, err := m.StartDeployForCard("cardD", "acp/login-via-sso-1a2b3c4d")
	if err != nil {
		t.Fatal(err)
	}
	// The button's branch wins over the card property and the checked-out one:
	// it is the worktree branch the agent has been committing to.
	if s.DeployBranch != "acp/login-via-sso-1a2b3c4d" {
		t.Errorf("deploy branch %q", s.DeployBranch)
	}
	if s.Deploy == nil || s.Deploy.Name != "prod" {
		t.Fatalf("deploy target: %+v", s.Deploy)
	}
	// The app is named after the repository, and the host doubles as the domain.
	if want := dokku.AppLabel(filepath.Base(repo)); s.Deploy.BaseApp != want {
		t.Errorf("base app %q, want %q", s.Deploy.BaseApp, want)
	}
	if s.RepoPath != repo {
		t.Errorf("a deploy must run in the repository itself, ran in %q", s.RepoPath)
	}

	waitFor(t, 15*time.Second, "deploy session done", func() bool {
		return len(writer.cardComments("cardD")) >= 2
	})
	if got := writer.cardComments("cardD")[0]; !strings.Contains(got, "acp/login-via-sso-1a2b3c4d") {
		t.Errorf("first comment should name the branch being published: %q", got)
	}

	// The card is told this is a deploy, so its console does not mistake the
	// session for its own.
	emitter.mu.Lock()
	defer emitter.mu.Unlock()
	var sawDeploy bool
	for i, name := range emitter.events {
		if name == EventSession && emitter.payloads[i]["deploy"] == true {
			sawDeploy = true
		}
	}
	if !sawDeploy {
		t.Error("no session event marked as a deploy")
	}
}

func TestDeployRunsAlongsideTheCardsOwnSession(t *testing.T) {
	// Worktrees off is the strict case: the repo-busy rule would otherwise
	// refuse, even though a deploy only pushes an existing branch.
	m, _, events, repo := testManager(t, fakeClaudeHang, func(c *Config) {
		c.WorktreeMode = "never"
		c.Deploys = []DeployEntry{{Name: "prod", Target: dokku.Target{SSHHost: "dokku.example.com"}}}
	})

	events.ch <- moveEvent("cardE", repo, "opt-backlog", "opt-agent")
	waitFor(t, 10*time.Second, "agent session running", func() bool {
		sessions, _, err := m.store.SessionsForCard("cardE")
		return err == nil && len(sessions) == 1 && sessions[0].Status == StatusRunning
	})

	m.mu.Lock()
	agentSession := m.byCard["cardE"]
	m.mu.Unlock()

	deploy, err := m.StartDeployForCard("cardE", "acp/whatever-1a2b3c4d")
	if err != nil {
		t.Fatalf("deploy refused while the card's session runs: %v", err)
	}

	// The card's console keeps talking to the agent, not to the deploy.
	m.mu.Lock()
	still := m.byCard["cardE"]
	m.mu.Unlock()
	if still != agentSession {
		t.Error("the deploy took over the card's own session")
	}
	if deploy.ID == agentSession.ID {
		t.Error("expected a separate deploy session")
	}
}
