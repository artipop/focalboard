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
	f.Nodes[0].AgentName = "claude-1"
	f.Nodes[1].Action = ""
	got, err := validateFlow(f, repos, agents, deploys)
	if err != nil {
		t.Fatal(err)
	}
	if got.Nodes[1].Action != FlowActionNone {
		t.Fatalf("action not defaulted: %+v", got.Nodes[1])
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

func TestDefaultFlowMirrorsTheLegacyColumns(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	cfg.TriggerColumn = "К агенту"
	cfg.TestColumn = "На тест"
	cfg.TestPassColumn = "Проверено"

	f := DefaultFlow(cfg)
	if f.Property != cfg.TriggerProperty {
		t.Fatalf("property: %q", f.Property)
	}
	if n, ok := f.NodeByColumn("К агенту"); !ok || n.Action != FlowActionAgent {
		t.Fatalf("agent node: %+v", n)
	}
	// Only the transition that exists today is wired: upgrading must not start
	// moving cards on its own.
	if n, ok := f.Next("test", TriggerSuccess); !ok || n.Column != "Проверено" {
		t.Fatalf("test success edge: %+v", n)
	}
	if _, ok := f.Next("agent", TriggerSuccess); ok {
		t.Fatal("the agent stage must not be wired automatically")
	}

	// A column the config does not name produces no node.
	cfg.DeployColumn = ""
	if _, ok := DefaultFlow(cfg).NodeByColumn("Deploy"); ok {
		t.Fatal("an empty deployColumn should not become a stage")
	}
}

func TestLoadConfigSeedsAndRespectsAnEmptyRegistry(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	// A config written before flows existed gets the default route.
	if err := os.WriteFile(path, []byte(`{"triggerColumn":"К агенту","testColumn":"На тест"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Flows) != 1 {
		t.Fatalf("no default flow seeded: %+v", cfg.Flows)
	}
	if _, ok := cfg.Flows[0].NodeByColumn("К агенту"); !ok {
		t.Fatalf("the seeded flow ignored the config's own columns: %+v", cfg.Flows[0])
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
