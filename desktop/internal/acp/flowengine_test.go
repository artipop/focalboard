package acp

import (
	"context"
	"strings"
	"testing"
	"time"
)

// flowEvent is a card move onto a named column of the flow property.
func flowEvent(cardID, repo, from, to string) CardMoved {
	return CardMoved{
		EventID: "ev-" + cardID + "-" + to,
		CardID:  cardID,
		BoardID: "board1",
		Title:   "Test task",
		Body:    "Do nothing useful.",
		Props:   map[string]string{"repo_path": repo, "branch": flowTestBranch},
		FromColumn: Column{PropertyID: "p1", PropertyName: "Status",
			OptionID: "opt-" + strings.ToLower(from), Name: from},
		ToColumn: Column{PropertyID: "p1", PropertyName: "Status",
			OptionID: "opt-" + strings.ToLower(to), Name: to},
		At: time.Now(),
	}
}

// flowManager is testManager with one route registered.
func flowManager(t *testing.T, scenario string, flow FlowEntry) (*Manager, *fakeWriter, *fakeEvents, string) {
	t.Helper()
	m, w, ev, repo := testManager(t, scenario, func(c *Config) { c.Flows = []FlowEntry{flow} })
	// Re-reading a card gives back what it says, branch included — the real
	// reader does, and the route asks it again on every transition.
	m.SetBoardReader(&fakeReader{ev: CardMoved{
		BoardID: "board1",
		Title:   "Test task",
		Props:   map[string]string{"repo_path": repo, "branch": flowTestBranch},
	}})
	// The cards below name the branch their work lives on, and a session bases
	// its worktree on it — so it has to exist, as it would on a real board.
	if _, err := gitCmd(context.Background(), repo, "branch", flowTestBranch); err != nil {
		t.Fatal(err)
	}
	return m, w, ev, repo
}

// flowTestBranch is the branch the cards in these tests carry.
const flowTestBranch = "feat/x"

func TestFlowAdvancesOnSessionSuccess(t *testing.T) {
	m, writer, events, repo := flowManager(t, fakeClaudeHappy, sampleFlow())

	events.ch <- flowEvent("card1", repo, "Backlog", "To Agent")

	waitFor(t, 20*time.Second, "card advanced to Review", func() bool {
		moves := writer.cardMoves()
		return len(moves) == 1 && moves[0].option == "Review"
	})
	if move := writer.cardMoves()[0]; move.cardID != "card1" || move.property != "Status" {
		t.Fatalf("move: %+v", move)
	}

	// The route says out loud why the card moved.
	comments := strings.Join(writer.cardComments("card1"), "\n")
	if !strings.Contains(comments, "Флоу «feature»") || !strings.Contains(comments, "Review") {
		t.Fatalf("no transition comment: %s", comments)
	}

	// And the card's position is remembered, so the next event knows where it is.
	st, ok, err := m.store.FlowStateForCard("card1")
	if err != nil || !ok || st.NodeID != "review" || st.Flow != "feature" {
		t.Fatalf("flow state: %+v, %v, %v", st, ok, err)
	}
	if st.Branch != "feat/x" || st.RepoPath != repo {
		t.Fatalf("flow state lost the card's repository/branch: %+v", st)
	}

	events2, err := m.store.FlowEvents("card1")
	if err != nil || len(events2) != 2 {
		t.Fatalf("route history: %+v, %v", events2, err)
	}
	if events2[1].On != TriggerSuccess || events2[1].ToNode != "review" {
		t.Fatalf("history entry: %+v", events2[1])
	}
}

func TestFlowTakesTheFailureBranch(t *testing.T) {
	_, writer, events, repo := flowManager(t, fakeClaudeCrash, sampleFlow())

	events.ch <- flowEvent("card2", repo, "Backlog", "To Agent")

	waitFor(t, 20*time.Second, "card advanced to Blocked", func() bool {
		moves := writer.cardMoves()
		return len(moves) == 1 && moves[0].option == "Blocked"
	})
}

func TestFlowWithoutAnEdgeLeavesTheCardPut(t *testing.T) {
	flow := sampleFlow()
	flow.Edges = nil // the stage runs, but nothing follows it
	_, writer, events, repo := flowManager(t, fakeClaudeHappy, flow)

	events.ch <- flowEvent("card3", repo, "Backlog", "To Agent")

	waitFor(t, 20*time.Second, "the route reports the dead end", func() bool {
		return strings.Contains(strings.Join(writer.cardComments("card3"), "\n"), "нет перехода")
	})
	if moves := writer.cardMoves(); len(moves) != 0 {
		t.Fatalf("the card should have stayed put: %+v", moves)
	}
}

func TestFlowVCSEventAdvancesAndOnlyOnce(t *testing.T) {
	flow := sampleFlow()
	// Review waits for the branch to be merged, then the card is done.
	flow.Nodes = append(flow.Nodes, FlowNode{ID: "done", Column: "Done", Action: FlowActionNone})
	flow.Edges = append(flow.Edges, FlowEdge{From: "review", To: "done", On: TriggerBranchMerged})
	m, writer, events, repo := flowManager(t, fakeClaudeHappy, flow)

	events.ch <- flowEvent("card4", repo, "Backlog", "To Agent")
	waitFor(t, 20*time.Second, "card parked on Review", func() bool {
		st, ok, _ := m.store.FlowStateForCard("card4")
		return ok && st.NodeID == "review"
	})

	// Only what somebody is waiting for is polled for.
	targets := m.FlowTargets()
	if len(targets) != 1 || targets[0].Branch != "feat/x" || targets[0].RepoPath != repo {
		t.Fatalf("poll targets: %+v", targets)
	}
	if len(targets[0].Triggers) != 2 ||
		!containsString(targets[0].Triggers, TriggerBranchMerged) ||
		!containsString(targets[0].Triggers, TriggerPRClosed) {
		t.Fatalf("poll triggers: %+v", targets[0].Triggers)
	}

	ev := VCSEvent{Kind: TriggerBranchMerged, RepoPath: repo, Branch: "feat/x", Detail: "ветка влита"}
	m.OnVCSEvent(ev)
	waitFor(t, 10*time.Second, "card advanced to Done", func() bool {
		moves := writer.cardMoves()
		return len(moves) == 2 && moves[1].option == "Done"
	})

	// The same event again must not move anything: the card already left.
	m.OnVCSEvent(ev)
	time.Sleep(300 * time.Millisecond)
	if moves := writer.cardMoves(); len(moves) != 2 {
		t.Fatalf("a repeated event moved the card twice: %+v", moves)
	}
	// A card parked where nothing is awaited needs no polling at all.
	if targets := m.FlowTargets(); len(targets) != 0 {
		t.Fatalf("nothing should be polled for now: %+v", targets)
	}
}

func TestFlowDraggedOffTheRouteForgetsTheCard(t *testing.T) {
	m, _, events, repo := flowManager(t, fakeClaudeHappy, sampleFlow())

	events.ch <- flowEvent("card5", repo, "Backlog", "To Agent")
	waitFor(t, 20*time.Second, "card parked on Review", func() bool {
		st, ok, _ := m.store.FlowStateForCard("card5")
		return ok && st.NodeID == "review"
	})

	// A column outside the route: the card leaves the flow, and nothing drags
	// it back.
	events.ch <- flowEvent("card5", repo, "Review", "Backlog")
	waitFor(t, 10*time.Second, "flow state cleared", func() bool {
		_, ok, _ := m.store.FlowStateForCard("card5")
		return !ok
	})
}

func TestFlowIgnoresOtherProperties(t *testing.T) {
	m, writer, events, repo := flowManager(t, fakeClaudeHappy, sampleFlow())

	ev := flowEvent("card6", repo, "Low", "High")
	ev.FromColumn.PropertyName = "Priority"
	ev.ToColumn.PropertyName = "Priority"
	events.ch <- ev

	time.Sleep(500 * time.Millisecond)
	if _, ok, _ := m.store.FlowStateForCard("card6"); ok {
		t.Fatal("a change of another select property must not start a route")
	}
	if moves := writer.cardMoves(); len(moves) != 0 {
		t.Fatalf("moves: %+v", moves)
	}
}

func TestLegacyColumnsStillWorkWithoutAFlow(t *testing.T) {
	// No flow registered: the standalone trigger columns keep their behaviour.
	m, _, events, repo := testManager(t, fakeClaudeHappy, nil)

	events.ch <- moveEvent("card7", repo, "opt-backlog", "opt-agent")

	waitFor(t, 20*time.Second, "legacy session done", func() bool {
		sessions, _, err := m.store.SessionsForCard("card7")
		return err == nil && len(sessions) == 1 && sessions[0].Status == StatusDone
	})
	if _, ok, _ := m.store.FlowStateForCard("card7"); ok {
		t.Fatal("a card without a route must not get flow state")
	}
}

// The card that never names a branch is the usual case: with worktrees on, the
// agent commits to a branch of its own and the card never learns its name. The
// route has to follow that branch anyway — otherwise it waits for a merge of
// whatever the repository happened to have checked out, which never comes.
func TestRouteFollowsTheBranchTheAgentWorkedOn(t *testing.T) {
	m, _, events, repo := flowManager(t, fakeClaudeHappy, sampleFlow())

	// This card says nothing about branches, and neither does re-reading it.
	m.SetBoardReader(&fakeReader{ev: CardMoved{
		BoardID: "board1",
		Title:   "Test task",
		Props:   map[string]string{"repo_path": repo},
	}})
	ev := flowEvent("cardNoBranch", repo, "Backlog", "To Agent")
	delete(ev.Props, "branch")
	events.ch <- ev

	waitFor(t, 20*time.Second, "the card advances to Review", func() bool {
		st, ok, _ := m.store.FlowStateForCard("cardNoBranch")
		return ok && st.NodeID == "review"
	})

	st, _, err := m.store.FlowStateForCard("cardNoBranch")
	if err != nil {
		t.Fatal(err)
	}
	worked, err := m.store.LatestBranchForCard("cardNoBranch")
	if err != nil || worked == "" {
		t.Fatalf("the session recorded no branch: %q, %v", worked, err)
	}
	if st.Branch != worked {
		t.Fatalf("the route follows %q while the agent worked on %q", st.Branch, worked)
	}

	// And that is the branch a deploy stage would publish.
	m.cfgMu.Lock()
	m.cfg.Deploys = []DeployEntry{deployEntry("prod")}
	m.cfgMu.Unlock()
	_, branch, err := m.resolveDeploy(CardMoved{CardID: "cardNoBranch"}, repo, true, "")
	if err != nil {
		t.Fatal(err)
	}
	if branch != worked {
		t.Fatalf("a deploy would publish %q instead of %q", branch, worked)
	}
}
