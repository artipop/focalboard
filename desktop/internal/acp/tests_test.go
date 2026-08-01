package acp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolvePreviewURL(t *testing.T) {
	m := agentManager(t, "")
	m.cfg.Repos = []RepoEntry{{Name: "webapp", Path: "/repos/webapp"}}
	// A target is a host now, so the single registered entry answers for the
	// card without being tied to its repository.
	m.cfg.Deploys = []DeployEntry{deployEntry("preview")}

	// 1. An explicit preview_url on the card wins — whatever put it there.
	url, branch, err := m.resolvePreviewURL(CardMoved{
		Props: map[string]string{"preview_url": "https://feat-x.api.example.com", "branch": "feat/x"},
	}, "/repos/webapp")
	if err != nil || url != "https://feat-x.api.example.com" || branch != "feat/x" {
		t.Fatalf("explicit url: %q, %q, %v", url, branch, err)
	}
	// The property is also accepted under the name a board is likely to show.
	url, _, err = m.resolvePreviewURL(CardMoved{
		Props: map[string]string{"preview url": "https://feat-y.api.example.com/app"},
	}, "/repos/webapp")
	if err != nil || url != "https://feat-y.api.example.com/app" {
		t.Fatalf("spaced property name: %q, %v", url, err)
	}
	// Something that is not an address is an error, not a browser crash later.
	if _, _, err := m.resolvePreviewURL(CardMoved{
		Props: map[string]string{"preview_url": "feat-x.api.example.com"},
	}, "/repos/webapp"); err == nil {
		t.Fatal("a scheme-less preview_url should be rejected")
	}

	// 2. Otherwise the address the deploy registry gives the card's branch.
	url, branch, err = m.resolvePreviewURL(CardMoved{
		Props: map[string]string{"branch": "feat/Big Thing"},
	}, "/repos/webapp")
	if err != nil || branch != "feat/Big Thing" {
		t.Fatalf("derived branch: %q, %v", branch, err)
	}
	// The address is composed the way a deploy composes it: one label carrying
	// the app name (the repository, or the target's override) and the branch.
	if url != "http://api-feat-big-thing.example.com" {
		t.Fatalf("derived url: %q", url)
	}

	// 3. With neither, the error says what is missing.
	m.cfg.Deploys = nil
	if _, _, err := m.resolvePreviewURL(CardMoved{Props: map[string]string{"branch": "feat/x"}}, "/repos/webapp"); err == nil ||
		!strings.Contains(err.Error(), "preview_url") {
		t.Fatalf("error should mention preview_url: %v", err)
	}
}

func TestSessionArtifactsDir(t *testing.T) {
	m := agentManager(t, "")
	root := t.TempDir()
	m.cfg.ArtifactsDir = root

	dir, err := m.artifactsDir("sess-1")
	if err != nil || dir != filepath.Join(root, "sess-1") {
		t.Fatalf("artifacts dir: %q, %v", dir, err)
	}
	// The agent is told to write its report there, so the directory has to
	// exist before it tries.
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		t.Fatalf("artifacts dir not created: %v", err)
	}
	// No configured root means no artifacts, not a broken path.
	m.cfg.ArtifactsDir = ""
	if dir, err := m.artifactsDir("sess-1"); err != nil || dir != "" {
		t.Fatalf("without a root: %q, %v", dir, err)
	}
}

func TestResolveTestRun(t *testing.T) {
	m := agentManager(t, "")
	ev := CardMoved{Props: map[string]string{"preview_url": "https://feat-x.example.com"}}

	// An ordinary session resolves nothing, so the launch path can call this
	// unconditionally.
	if run, err := m.resolveTestRun(ev, "/repo", "/data/run", false); run != nil || err != nil {
		t.Fatalf("non-test session: %+v, %v", run, err)
	}

	run, err := m.resolveTestRun(ev, "/repo", "/data/run", true)
	if err != nil {
		t.Fatal(err)
	}
	if run.URL != "https://feat-x.example.com" || run.Artifacts != "/data/run" {
		t.Fatalf("test run: %+v", run)
	}
}

func TestComposeTestPromptCarriesTheScenario(t *testing.T) {
	run := TestRun{URL: "https://feat-x.example.com", Branch: "feat/x"}
	prompt := composeTestPrompt(
		CardMoved{Title: "Оформление заказа", Body: "Кнопка «Купить» должна вести в корзину"},
		AgentEntry{Prompt: "Ты работаешь в проекте Shop."},
		"Отвечай по-русски.", "", run,
	)
	for _, want := range []string{
		"Отвечай по-русски.",          // board system prompt
		"Ты работаешь в проекте Shop", // agent prompt
		ResultFile,                   // the default tester instructions
		"https://feat-x.example.com", // what to open
		"feat/x",
		"Кнопка «Купить»", // the card body is the scenario
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt is missing %q:\n%s", want, prompt)
		}
	}

	// A configured prompt replaces the default; a card without a description
	// still gets a job.
	prompt = composeTestPrompt(CardMoved{Title: "Смоук"}, AgentEntry{}, "", "Проверь только главную.", run)
	if strings.Contains(prompt, "browser_snapshot") || !strings.Contains(prompt, "Проверь только главную.") {
		t.Fatalf("custom prompt not used:\n%s", prompt)
	}
	if !strings.Contains(prompt, "Описания у карточки нет") {
		t.Fatalf("a card with no body should still be given a scenario:\n%s", prompt)
	}
}

func TestSessionMCPServersForATestSession(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	agent := AgentEntry{Name: "jojo", Kind: "junie", MCPServers: map[string]AgentMCPServer{
		"playwright": {Command: "npx", Args: []string{"-y", "@playwright/mcp@latest", "--headless"}},
	}}
	s := &Session{RepoPath: "/repo", Agent: agent, Test: &TestRun{URL: "https://feat-x.example.com", Artifacts: "/data/run"}}

	specs, err := sessionMCPServers(s, cfg)
	if err != nil {
		t.Fatal(err)
	}
	// The browser is the agent's own server; we add nothing of our own to a
	// test run any more.
	if len(specs) != 1 || specs[0].Name != "playwright" || specs[0].Command != "npx" {
		t.Fatalf("specs: %+v", specs)
	}
	// Its tools are allowed by prefix: their names only exist at run time, and
	// a card-triggered run has no console to ask.
	if !s.toolPrefixAllowed("mcp__playwright__browser_click") {
		t.Error("browser tools should run unasked")
	}
	if s.toolAllowed("mcp__dokku__deploy_branch") {
		t.Error("a test session should not get deploy tools")
	}
}

func TestTestSessionNeedsABrowserServer(t *testing.T) {
	repo := initTestRepo(t)
	m := agentManager(t, "", AgentEntry{Name: "bare", Kind: "claude"})
	m.cfg.Deploys = []DeployEntry{deployEntry("prod")}
	m.cfg.Repos = []RepoEntry{{Name: "webapp", Path: repo}}

	ev := CardMoved{CardID: "cardT", Title: "Проверить", Props: map[string]string{"repo_path": repo, "branch": "feat/x"}}
	_, err := m.startSession(ev, startOptions{test: true})
	if err == nil {
		t.Fatal("a test session without a browser server should not start")
	}
	// The message has to say what to do, since nothing else will notice.
	if !strings.Contains(err.Error(), "MCP-сервер браузера") {
		t.Errorf("unhelpful error: %v", err)
	}
}

func TestTestColumnRouting(t *testing.T) {
	m := agentManager(t, "")
	m.cfg.TriggerProperty = "Status"
	m.cfg.Columns = migratedColumns(m.cfg)

	col := func(name string) Column { return Column{PropertyName: "status", Name: name} }
	spec, ok := m.columnFor("board1", col("to test"))
	if !ok || spec.Action != FlowActionTest {
		t.Fatalf("the test column should match case-insensitively: %+v, %v", spec, ok)
	}
	if spec, _ := m.columnFor("board1", col(m.cfg.TriggerColumn)); spec.Action != FlowActionAgent {
		t.Fatalf("the trigger column should run an agent: %+v", spec)
	}
	if _, ok := m.columnFor("board1", Column{PropertyName: "Other", Name: "To Test"}); ok {
		t.Fatal("only the configured property may match")
	}

	// An empty name is not a column: it must not match every unnamed one.
	m.cfg.TestColumn = ""
	m.cfg.Columns = migratedColumns(m.cfg)
	if _, ok := m.columnFor("board1", Column{PropertyName: "Status"}); ok {
		t.Fatal("an empty column name must not become a trigger")
	}
}

func TestDefaultConfigShipsTheTestColumn(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	if cfg.TestColumn == "" || cfg.TestPassColumn == "" || cfg.TestFailColumn == "" {
		t.Fatalf("test columns: %+v", cfg)
	}
	if cfg.TestPrompt != DefaultTestPrompt || cfg.ArtifactsDir == "" {
		t.Fatalf("test defaults missing: %+v", cfg)
	}
	// A browser scenario gets its own, longer budget.
	if cfg.TestTimeout() <= cfg.SessionTimeout() {
		t.Fatalf("test timeout %s should exceed the session timeout %s", cfg.TestTimeout(), cfg.SessionTimeout())
	}
	// How the browser runs is the business of the agent's own MCP server, so
	// the config carries nothing about it.
	if strings.Contains(DefaultTestPrompt, "mcp__webtest__") {
		t.Error("the prompt still names a browser server we no longer ship")
	}
	if !strings.Contains(DefaultTestPrompt, ResultFile) {
		t.Error("the prompt must tell the agent to write the report file")
	}
}
