package acp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The point of binding a column by its option id: renaming the column on the
// board must not stop anything working. The name is only how a spec is found
// the first time.
func TestColumnMatchedByIDSurvivesARename(t *testing.T) {
	m := agentManager(t, "")
	m.cfg.Columns = []ColumnSpec{{Property: "Status", Column: "To Test", Action: FlowActionTest}}

	moved := Column{PropertyID: "prop-status", PropertyName: "Status", OptionID: "opt-totest", Name: "To Test"}
	spec, ok := m.columnFor("board1", moved)
	if !ok || spec.Action != FlowActionTest {
		t.Fatalf("column not matched by name: %+v, %v", spec, ok)
	}
	// The first match teaches the registry which option this was.
	if got := m.cfg.Columns[0]; got.OptionID != "opt-totest" || got.BoardID != "board1" || got.PropertyID != "prop-status" {
		t.Fatalf("ids not learned: %+v", got)
	}

	renamed := Column{PropertyID: "prop-status", PropertyName: "Status", OptionID: "opt-totest", Name: "На проверку"}
	if spec, ok := m.columnFor("board1", renamed); !ok || spec.Action != FlowActionTest {
		t.Fatalf("a renamed column lost its settings: %+v, %v", spec, ok)
	}
	// And a different column that happens to carry the old name is not it.
	other := Column{PropertyID: "prop-status", PropertyName: "Status", OptionID: "opt-other", Name: "To Test"}
	if _, ok := m.columnFor("board1", other); ok {
		t.Fatal("a foreign option matched by a stale name")
	}
}

// A board's own spec wins over one that names no board, so two boards can hold
// the same column name and disagree about what happens in it.
func TestColumnPrefersTheBoardsOwnSpec(t *testing.T) {
	m := agentManager(t, "")
	m.cfg.Columns = []ColumnSpec{
		{Property: "Status", Column: "Deploy", Action: FlowActionDeploy},
		{BoardID: "board2", PropertyID: "p", OptionID: "opt-deploy", Property: "Status", Column: "Deploy", Action: FlowActionNone},
	}
	moved := Column{PropertyID: "p", PropertyName: "Status", OptionID: "opt-deploy", Name: "Deploy"}

	if spec, _ := m.columnFor("board2", moved); spec.Action != FlowActionNone {
		t.Fatalf("board2 should use its own spec: %+v", spec)
	}
	if spec, _ := m.columnFor("board9", Column{PropertyName: "Status", Name: "Deploy"}); spec.Action != FlowActionDeploy {
		t.Fatalf("another board should fall back to the shared spec: %+v", spec)
	}
}

// Upgrading an install must not change what its columns do: the old keys become
// specs saying exactly what they said before.
func TestColumnsMigratedFromTheLegacyKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"triggerColumn":"К агенту","deployColumn":"Деплой","testColumn":"На тест"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path, dir)
	if err != nil {
		t.Fatal(err)
	}

	want := map[string]string{"К агенту": FlowActionAgent, "Деплой": FlowActionDeploy, "На тест": FlowActionTest}
	if len(cfg.Columns) != len(want) {
		t.Fatalf("columns: %+v", cfg.Columns)
	}
	for _, c := range cfg.Columns {
		if want[c.Column] != c.Action {
			t.Errorf("column %q does what it should not: %q", c.Column, c.Action)
		}
		if c.Property != cfg.TriggerProperty {
			t.Errorf("column %q landed on property %q", c.Column, c.Property)
		}
		if c.OptionID != "" {
			t.Errorf("a migrated column cannot know its option yet: %+v", c)
		}
	}

	// Clearing every column is a decision and must survive a restart, exactly
	// as an emptied flow registry does.
	if err := os.WriteFile(path, []byte(`{"columns":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err = LoadConfig(path, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Columns) != 0 {
		t.Fatalf("an emptied column registry was re-seeded: %+v", cfg.Columns)
	}
}

func TestSaveColumnValidatesAndReplaces(t *testing.T) {
	m := agentManager(t, "", AgentEntry{Name: "claude-1", Kind: "claude"})
	m.cfg.Columns = nil

	spec := ColumnSpec{BoardID: "board1", PropertyID: "p", OptionID: "opt-work", Property: "Status",
		Column: "In Progress", Action: FlowActionAgent, Agents: []string{"claude-1", "claude-1", " "}, MaxRunning: 2}
	saved, err := m.SaveColumn(spec)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.Agents) != 1 {
		t.Fatalf("the roster should be deduplicated: %+v", saved.Agents)
	}

	// Saving the same column again replaces it rather than piling up.
	spec.MaxRunning = 3
	if _, err := m.SaveColumn(spec); err != nil {
		t.Fatal(err)
	}
	if len(m.cfg.Columns) != 1 || m.cfg.Columns[0].MaxRunning != 3 {
		t.Fatalf("columns: %+v", m.cfg.Columns)
	}

	// A roster naming somebody who is not registered is refused here, where it
	// is typed, rather than when a card lands in the column.
	spec.Agents = []string{"ghost"}
	if _, err := m.SaveColumn(spec); err == nil {
		t.Fatal("an unregistered agent must be refused")
	}
	spec.Agents = nil
	spec.Action = "sing"
	if _, err := m.SaveColumn(spec); err == nil {
		t.Fatal("an unknown action must be refused")
	}

	if err := m.RemoveColumn("board1", "opt-work", "In Progress"); err != nil {
		t.Fatal(err)
	}
	if len(m.cfg.Columns) != 0 {
		t.Fatalf("column not removed: %+v", m.cfg.Columns)
	}
}

// Several developers work one column: the card goes to whoever is free, and the
// choice is repeatable rather than random.
func TestCrewTakesTheFreeAgent(t *testing.T) {
	m := agentManager(t, "",
		AgentEntry{Name: "dev-1", Kind: "claude"},
		AgentEntry{Name: "dev-2", Kind: "claude"},
		AgentEntry{Name: "dev-3", Kind: "claude"})
	crew := []string{"dev-1", "dev-2", "dev-3"}

	agent, busy, err := m.resolveSessionAgent(CardMoved{}, crew)
	if err != nil || busy || agent.Name != "dev-1" {
		t.Fatalf("the first of the crew should take it: %+v, %v, %v", agent, busy, err)
	}

	// With dev-1 working, the next card goes to dev-2 rather than queueing.
	m.mu.Lock()
	m.active["s1"] = &Session{ID: "s1", Agent: AgentEntry{Name: "dev-1"}}
	m.mu.Unlock()
	if agent, _, _ := m.resolveSessionAgent(CardMoved{}, crew); agent.Name != "dev-2" {
		t.Fatalf("a busy agent should be passed over: %+v", agent)
	}

	// Everybody working: the card waits instead of piling onto somebody.
	m.mu.Lock()
	m.active["s2"] = &Session{ID: "s2", Agent: AgentEntry{Name: "dev-2"}}
	m.active["s3"] = &Session{ID: "s3", Agent: AgentEntry{Name: "dev-3"}}
	m.mu.Unlock()
	if _, busy, err := m.resolveSessionAgent(CardMoved{}, crew); !busy || err != nil {
		t.Fatalf("a full crew should report busy: %v, %v", busy, err)
	}
}

// The column's limit is what a WIP limit is on a board: the third card waits in
// place and says so, and starts by itself when somebody finishes.
func TestColumnLimitQueuesTheCard(t *testing.T) {
	m, writer, events, repo := testManager(t, fakeClaudeHang, func(c *Config) {
		c.Columns = []ColumnSpec{{
			Property: c.TriggerProperty, Column: c.TriggerColumn,
			Action: FlowActionAgent, MaxRunning: 1,
		}}
	})

	events.ch <- moveEvent("cardOne", repo, "opt-backlog", "opt-agent")
	waitFor(t, 10*time.Second, "the first card is working", func() bool {
		sessions, _, err := m.store.SessionsForCard("cardOne")
		return err == nil && len(sessions) == 1 && sessions[0].Status == StatusRunning
	})

	events.ch <- moveEvent("cardTwo", repo, "opt-backlog", "opt-agent")
	waitFor(t, 10*time.Second, "the second card says it is waiting", func() bool {
		return len(writer.cardComments("cardTwo")) > 0
	})
	if got := writer.cardComments("cardTwo")[0]; !strings.Contains(got, "занята") {
		t.Fatalf("the card should say why it waits, got %q", got)
	}
	if sessions, _, _ := m.store.SessionsForCard("cardTwo"); len(sessions) != 0 {
		t.Fatalf("the second card must not have started: %d sessions", len(sessions))
	}
	if _, ok, _ := m.store.NextQueuedStage(m.cfg.Columns[0].Key()); !ok {
		t.Fatal("the card is not in the queue")
	}

	// The place frees up: the queue is what fills it, without anybody dragging
	// the card again.
	m.CancelSessionForCard("cardOne", "тест")
	waitFor(t, 15*time.Second, "the queued card starts by itself", func() bool {
		sessions, _, err := m.store.SessionsForCard("cardTwo")
		return err == nil && len(sessions) == 1
	})
	if _, ok, _ := m.store.NextQueuedStage(m.cfg.Columns[0].Key()); ok {
		t.Fatal("a started card must leave the queue")
	}
}

// What the card shows about itself: the route, where it stands on it, and what
// it is waiting for.
func TestCardFlowDescribesWhereTheCardStands(t *testing.T) {
	m, _, events, repo := flowManager(t, fakeClaudeHappy, sampleFlow())

	events.ch <- flowEvent("cardF", repo, "Backlog", "To Agent")
	waitFor(t, 20*time.Second, "the card reaches Review", func() bool {
		st, ok, _ := m.store.FlowStateForCard("cardF")
		return ok && st.NodeID == "review"
	})

	flow, err := m.CardFlowFor("cardF")
	if err != nil || flow == nil {
		t.Fatalf("card flow: %+v, %v", flow, err)
	}
	if flow.Flow != "feature" || flow.CurrentID != "review" {
		t.Fatalf("card flow: %+v", flow)
	}
	if flow.Branch != flowTestBranch {
		t.Fatalf("the strip should name the branch being watched: %+v", flow)
	}

	var current, done int
	for _, s := range flow.Stages {
		if s.Current {
			current++
		}
		if s.Done {
			done++
		}
	}
	if current != 1 || done == 0 {
		t.Fatalf("stages: %+v", flow.Stages)
	}
	// The stage waits for the branch to be merged, and says so in words.
	if len(flow.WaitingFor) == 0 {
		t.Fatalf("the card does not say what it waits for: %+v", flow)
	}

	// A card that is on no route says nothing at all, rather than erroring.
	if got, err := m.CardFlowFor("cardNobodyKnows"); got != nil || err != nil {
		t.Fatalf("a card with no route: %+v, %v", got, err)
	}
}

// fakeBoardMeta is a board that carries its own automation, the way the
// template does.
type fakeBoardMeta struct{ props map[string]any }

func (f *fakeBoardMeta) BoardProperties(context.Context, string) (map[string]any, error) {
	return f.props, nil
}

// A board made from the template works before anything is configured: it brings
// the columns it runs and the routes across it, and they are taken once.
func TestBoardBringsItsOwnAutomation(t *testing.T) {
	m := agentManager(t, "")
	m.cfg.Columns = nil
	m.cfg.Flows = nil
	m.rootCtx = context.Background()

	automation, err := json.Marshal(BoardAutomation{
		Columns: []ColumnSpec{{
			PropertyID: "p", OptionID: "opt-work", Property: "Status",
			Column: "In Progress", Action: FlowActionAgent, MaxRunning: 2,
		}},
		Flows: []FlowEntry{{
			Name: "Feature", Property: "Status",
			Nodes: []FlowNode{
				{ID: "work", Column: "In Progress", OptionID: "opt-work"},
				{ID: "review", Column: "In Review", OptionID: "opt-review", Action: FlowActionNone},
			},
			Edges: []FlowEdge{{From: "work", To: "review", On: TriggerSuccess}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var props map[string]any
	if err := json.Unmarshal(automation, &props); err != nil {
		t.Fatal(err)
	}
	m.SetBoardMeta(&fakeBoardMeta{props: props})

	m.seedFromBoard("board1")
	if len(m.cfg.Columns) != 1 || m.cfg.Columns[0].BoardID != "board1" || m.cfg.Columns[0].MaxRunning != 2 {
		t.Fatalf("columns not taken from the board: %+v", m.cfg.Columns)
	}
	if len(m.cfg.Flows) != 1 || m.cfg.Flows[0].BoardID != "board1" {
		t.Fatalf("routes not taken from the board: %+v", m.cfg.Flows)
	}

	// The stage inherits its column, which is the whole point of shipping both.
	spec, ok := m.columnFor("board1", Column{PropertyID: "p", PropertyName: "Status", OptionID: "opt-work", Name: "In Progress"})
	if !ok || spec.Action != FlowActionAgent {
		t.Fatalf("column: %+v, %v", spec, ok)
	}

	// Reading the board again changes nothing, and an edit made since is kept.
	m.cfg.Columns[0].MaxRunning = 5
	m.seededMu.Lock()
	m.seeded = nil
	m.seededMu.Unlock()
	m.seedFromBoard("board1")
	if len(m.cfg.Columns) != 1 || m.cfg.Columns[0].MaxRunning != 5 {
		t.Fatalf("a second import overwrote what the user changed: %+v", m.cfg.Columns)
	}
	if len(m.cfg.Flows) != 1 {
		t.Fatalf("routes duplicated on a second import: %+v", m.cfg.Flows)
	}

	// Another board does not see this one's routes.
	if got := m.BoardFlows("board2"); len(got) != 0 {
		t.Fatalf("board2 sees board1's routes: %+v", got)
	}
	if got := m.BoardFlows("board1"); len(got) != 1 {
		t.Fatalf("board1 lost its own routes: %+v", got)
	}
}
