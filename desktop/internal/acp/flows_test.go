package acp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sampleFlow is the route the engine tests walk: agent → review → deploy.
func sampleFlow() FlowEntry {
	return FlowEntry{
		Name:     "feature",
		Property: "Status",
		Nodes: []FlowNode{
			{ID: "work", Column: "To Agent", Action: FlowActionAgent},
			{ID: "review", Column: "Review", Action: FlowActionNone},
			{ID: "blocked", Column: "Blocked", Action: FlowActionNone},
		},
		Edges: []FlowEdge{
			{From: "work", To: "review", On: TriggerSuccess},
			{From: "work", To: "blocked", On: TriggerFailure},
			{From: "review", To: "blocked", On: TriggerPRClosed},
		},
	}
}

func TestValidateFlow(t *testing.T) {
	repos := []RepoEntry{{Name: "webapp", Path: "/repos/webapp"}}
	agents := []AgentEntry{{Name: "claude-1", Kind: AgentKindClaude}}
	deploys := []DeployEntry{deployEntry("prod")}

	if _, err := validateFlow(sampleFlow(), repos, agents, deploys); err != nil {
		t.Fatalf("a well-formed flow was rejected: %v", err)
	}

	cases := map[string]func(*FlowEntry){
		"пустое имя":                  func(f *FlowEntry) { f.Name = "  " },
		"нет стадий":                  func(f *FlowEntry) { f.Nodes = nil },
		"стадия без колонки":          func(f *FlowEntry) { f.Nodes[1].Column = "" },
		"две стадии на одной колонке": func(f *FlowEntry) { f.Nodes[1].Column = "To Agent" },
		"дубль идентификатора":        func(f *FlowEntry) { f.Nodes[1].ID = "work" },
		"неизвестное действие":        func(f *FlowEntry) { f.Nodes[1].Action = "deploy-maybe" },
		"переход в никуда":            func(f *FlowEntry) { f.Edges[0].To = "ghost" },
		"переход ниоткуда":            func(f *FlowEntry) { f.Edges[0].From = "ghost" },
		"неизвестное событие":         func(f *FlowEntry) { f.Edges[0].On = "pr.reviewed" },
		"два перехода по одному событию": func(f *FlowEntry) {
			f.Edges = append(f.Edges, FlowEdge{From: "work", To: "blocked", On: TriggerSuccess})
		},
		"неизвестный репозиторий": func(f *FlowEntry) { f.RepoName = "nosuchrepo" },
		"неизвестный агент":       func(f *FlowEntry) { f.Nodes[0].AgentName = "nosuchagent" },
		"неизвестная цель":        func(f *FlowEntry) { f.Nodes[0].DeployName = "nosuchtarget" },
	}
	for name, break_ := range cases {
		f := sampleFlow()
		break_(&f)
		if _, err := validateFlow(f, repos, agents, deploys); err == nil {
			t.Errorf("%s: принято без ошибки", name)
		}
	}

	// References that do exist are accepted, and an empty action defaults to none.
	f := sampleFlow()
	f.RepoName = "WEBAPP"
	// A stage that named one agent becomes a stage with a crew of one.
	f.Nodes[0].AgentName = "claude-1"
	// And an empty action is kept as it is: the stage does whatever its column
	// does, which is not the same as doing nothing.
	f.Nodes[1].Action = ""
	got, err := validateFlow(f, repos, agents, deploys)
	if err != nil {
		t.Fatal(err)
	}
	if got.Nodes[1].Action != "" {
		t.Fatalf("an empty action must stay empty: %+v", got.Nodes[1])
	}
	if len(got.Nodes[0].AgentNames) != 1 || got.Nodes[0].AgentNames[0] != "claude-1" || got.Nodes[0].AgentName != "" {
		t.Fatalf("the old single agent was not folded into the crew: %+v", got.Nodes[0])
	}
}

func TestAddUpdateRemoveFlowPersists(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	m := agentManager(t, cfgPath)

	if _, err := m.AddFlow(sampleFlow()); err != nil {
		t.Fatal(err)
	}
	if _, err := m.AddFlow(sampleFlow()); err == nil {
		t.Error("duplicate name accepted")
	}

	updated := sampleFlow()
	updated.Nodes[1].Column = "Ревью"
	if _, err := m.UpdateFlow(updated); err != nil {
		t.Fatal(err)
	}
	if _, err := m.UpdateFlow(FlowEntry{Name: "ghost", Nodes: []FlowNode{{ID: "a", Column: "A"}}}); err == nil {
		t.Error("update of an unknown flow accepted")
	}

	loaded, err := LoadConfig(cfgPath, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Flows) != 1 || loaded.Flows[0].Nodes[1].Column != "Ревью" {
		t.Fatalf("config did not persist the update: %+v", loaded.Flows)
	}

	if err := m.RemoveFlow("FEATURE"); err != nil {
		t.Fatal(err)
	}
	if len(m.Flows()) != 0 {
		t.Fatalf("flow not removed: %+v", m.Flows())
	}
}

func TestResolveFlow(t *testing.T) {
	m := agentManager(t, "")
	m.cfg.Repos = []RepoEntry{{Name: "webapp", Path: "/repos/webapp"}}
	feature := sampleFlow()
	hotfix := sampleFlow()
	hotfix.Name = "hotfix"
	hotfix.RepoName = "webapp"
	m.cfg.Flows = []FlowEntry{feature, hotfix}

	// 1. A card option naming a flow wins.
	if f := m.resolveFlow(CardMoved{OptionNames: []string{"webapp", "feature"}}, "/repos/webapp"); f == nil || f.Name != "feature" {
		t.Fatalf("option match: %+v", f)
	}
	// 2. Otherwise the flow tied to the card's repository.
	if f := m.resolveFlow(CardMoved{OptionNames: []string{"webapp"}}, "/repos/webapp"); f == nil || f.Name != "hotfix" {
		t.Fatalf("repo match: %+v", f)
	}
	// 3. Ambiguity means no route rather than a guess — the legacy columns then
	//    keep working for that card.
	if f := m.resolveFlow(CardMoved{}, "/repos/other"); f != nil {
		t.Fatalf("two unrelated flows should not resolve: %+v", f)
	}
	// 4. A single registered flow is the answer by default.
	m.cfg.Flows = []FlowEntry{feature}
	if f := m.resolveFlow(CardMoved{}, "/repos/other"); f == nil || f.Name != "feature" {
		t.Fatalf("single flow: %+v", f)
	}
	m.cfg.Flows = nil
	if f := m.resolveFlow(CardMoved{}, "/repos/other"); f != nil {
		t.Fatalf("empty registry: %+v", f)
	}
}

func TestFlowGraphLookups(t *testing.T) {
	f := sampleFlow()

	if n, ok := f.NodeByColumn("to agent"); !ok || n.ID != "work" {
		t.Fatalf("column lookup is case-sensitive: %+v", n)
	}
	if n, ok := f.Next("work", TriggerSuccess); !ok || n.ID != "review" {
		t.Fatalf("success edge: %+v", n)
	}
	if _, ok := f.Next("review", TriggerSuccess); ok {
		t.Fatal("a node without an edge must not resolve one")
	}
	// Only the VCS triggers make a node worth polling for.
	if waits := f.WaitsFor("review"); len(waits) != 1 || waits[0] != TriggerPRClosed {
		t.Fatalf("waits: %v", waits)
	}
	if waits := f.WaitsFor("work"); len(waits) != 0 {
		t.Fatalf("outcome edges are not polled for: %v", waits)
	}
	if f.PropertyOr("Status2") != "Status" {
		t.Fatal("the flow's own property should win")
	}
	f.Property = ""
	if f.PropertyOr("Status2") != "Status2" {
		t.Fatal("fallback property not used")
	}
}

func TestTriggerMetadata(t *testing.T) {
	if !IsVCSTrigger(TriggerPRMerged) || !IsGitHubTrigger(TriggerPRMerged) {
		t.Fatal("pr.merged comes from GitHub")
	}
	if !IsVCSTrigger(TriggerBranchMerged) || IsGitHubTrigger(TriggerBranchMerged) {
		t.Fatal("branch.merged comes from local git")
	}
	if IsVCSTrigger(TriggerSuccess) || IsGitHubTrigger(TriggerSuccess) {
		t.Fatal("success is produced by the stage itself")
	}
	if _, ok := Trigger("pr.rebased"); ok {
		t.Fatal("the trigger set must be closed")
	}
	if TriggerLabel(TriggerSuccess) == TriggerSuccess {
		t.Fatal("a known trigger should have a human label")
	}
}

func TestTemplateFlowsUseTheConfigsOwnColumns(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	cfg.TriggerColumn = "К агенту"
	cfg.TestColumn = "На тест"
	cfg.TestPassColumn = "Проверено"

	flows := TemplateFlows(cfg)
	if len(flows) < 2 {
		t.Fatalf("a template with one route offers no choice: %+v", flows)
	}
	// Every seeded route must be one the engine would accept from the editor.
	for _, f := range flows {
		if _, err := validateFlow(f, nil, nil, nil); err != nil {
			t.Fatalf("template flow %q is invalid: %v", f.Name, err)
		}
		if f.Property != cfg.TriggerProperty {
			t.Fatalf("%s: property %q", f.Name, f.Property)
		}
	}

	feature := flows[0]
	if feature.Name != TemplateFlowFeature {
		t.Fatalf("the full route should come first: %q", feature.Name)
	}
	if n, ok := feature.NodeByColumn("К агенту"); !ok || n.Action != FlowActionAgent {
		t.Fatalf("agent stage: %+v", n)
	}
	if n, ok := feature.Next("test", TriggerSuccess); !ok || n.Column != "Проверено" {
		t.Fatalf("test success edge: %+v", n)
	}
	// A failed check goes back to the agent rather than to a person.
	if n, ok := feature.Next("test", TriggerFailure); !ok || n.Column != "К агенту" {
		t.Fatalf("test failure edge: %+v", n)
	}
	// Waiting for the merge needs no token: it is the local git watcher.
	if !IsVCSTrigger(TriggerBranchMerged) || IsGitHubTrigger(TriggerBranchMerged) {
		t.Fatal("the seeded routes must work without GitHub credentials")
	}
	if n, ok := feature.Next("review", TriggerBranchMerged); !ok || n.Column != cfg.DeployColumn {
		t.Fatalf("review edge: %+v", n)
	}

	// A column the config does not name produces no stage, and the transitions
	// that would have led there go with it.
	cfg.DeployColumn = ""
	for _, f := range TemplateFlows(cfg) {
		if _, ok := f.NodeByColumn("Deploy"); ok {
			t.Fatalf("%s: an empty deployColumn should not become a stage", f.Name)
		}
		if _, err := validateFlow(f, nil, nil, nil); err != nil {
			t.Fatalf("%s: dropping a column left a dangling edge: %v", f.Name, err)
		}
	}
}

func TestLoadConfigSeedsAndRespectsAnEmptyRegistry(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	// A config written before flows existed gets the template routes.
	if err := os.WriteFile(path, []byte(`{"triggerColumn":"К агенту","testColumn":"На тест"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Flows) != len(TemplateFlows(cfg)) || len(cfg.Flows) == 0 {
		t.Fatalf("template flows not seeded: %+v", cfg.Flows)
	}
	if _, ok := cfg.Flows[0].NodeByColumn("К агенту"); !ok {
		t.Fatalf("the seeded flows ignored the config's own columns: %+v", cfg.Flows[0])
	}
	// Seeding several routes also means no card is silently adopted by one:
	// resolveFlow's single-entry fallback only fires when there is exactly one.
	if len(cfg.Flows) < 2 {
		t.Fatalf("a single seeded route would adopt every card: %+v", cfg.Flows)
	}

	// Deleting every route is a decision and must survive a restart.
	if err := os.WriteFile(path, []byte(`{"flows":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err = LoadConfig(path, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Flows) != 0 {
		t.Fatalf("an empty registry was re-seeded: %+v", cfg.Flows)
	}
}

func TestFlowEntryJSONRoundTrip(t *testing.T) {
	b, err := json.Marshal(sampleFlow())
	if err != nil {
		t.Fatal(err)
	}
	// The editor round-trips this shape, so the field names matter.
	for _, want := range []string{`"nodes"`, `"edges"`, `"column"`, `"action"`, `"from"`, `"to"`, `"on"`} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("serialized flow lacks %s: %s", want, b)
		}
	}
	var back FlowEntry
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if len(back.Nodes) != 3 || len(back.Edges) != 3 {
		t.Fatalf("round trip lost data: %+v", back)
	}
}

// The builder puts a stage where the reader wants it, and that has to survive
// the round trip — otherwise the canvas silently rearranges itself on reload.
func TestFlowNodeKeepsWhereItWasPut(t *testing.T) {
	f := sampleFlow()
	f.Nodes[0].X, f.Nodes[0].Y = 420, 80

	encoded, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"x":420`) {
		t.Fatalf("the position was not written: %s", encoded)
	}
	var back FlowEntry
	if err := json.Unmarshal(encoded, &back); err != nil {
		t.Fatal(err)
	}
	saved, err := validateFlow(back, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Nodes[0].X != 420 || saved.Nodes[0].Y != 80 {
		t.Fatalf("validation lost the position: %+v", saved.Nodes[0])
	}
	// A stage nobody placed stays unplaced, so it is laid out rather than
	// pinned to the top-left corner.
	if saved.Nodes[1].X != 0 || saved.Nodes[1].Y != 0 {
		t.Fatalf("an unplaced stage gained a position: %+v", saved.Nodes[1])
	}
}
