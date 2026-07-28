package acp

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mattermost/focalboard/desktop/internal/vcs"
)

// fakeWatcher reports the same state on every poll, the way a real watcher does:
// a merged branch stays merged.
type fakeWatcher struct {
	events  []vcs.Event
	targets []vcs.Target
}

func (w *fakeWatcher) Name() string { return "fake" }

func (w *fakeWatcher) Poll(_ context.Context, t vcs.Target) ([]vcs.Event, error) {
	w.targets = append(w.targets, t)
	out := make([]vcs.Event, 0, len(w.events))
	for _, e := range w.events {
		if t.Wants(e.Kind) && e.Branch == t.Branch {
			e.RepoPath = t.RepoPath
			out = append(out, e)
		}
	}
	return out, nil
}

func TestPollVCSMovesTheCardOnceAndOnlyWhereSomebodyWaits(t *testing.T) {
	flow := sampleFlow()
	flow.Nodes = append(flow.Nodes, FlowNode{ID: "done", Column: "Done", Action: FlowActionNone})
	flow.Edges = append(flow.Edges, FlowEdge{From: "review", To: "done", On: TriggerBranchMerged})
	m, writer, events, repo := flowManager(t, fakeClaudeHappy, flow)

	watcher := &fakeWatcher{events: []vcs.Event{{
		Kind: TriggerBranchMerged, Branch: "feat/x", Marker: "abc123", Detail: "ветка влита в main",
	}}}
	m.SetWatchers(watcher)

	// Nothing is parked yet, so nothing is polled.
	m.PollVCS()
	if len(watcher.targets) != 0 {
		t.Fatalf("polled with no card waiting: %+v", watcher.targets)
	}

	events.ch <- flowEvent("cardV", repo, "Backlog", "To Agent")
	waitFor(t, 20*time.Second, "card parked on Review", func() bool {
		st, ok, _ := m.store.FlowStateForCard("cardV")
		return ok && st.NodeID == "review"
	})

	m.PollVCS()
	waitFor(t, 10*time.Second, "card advanced to Done", func() bool {
		moves := writer.cardMoves()
		return len(moves) == 2 && moves[1].option == "Done"
	})
	if got := watcher.targets[0]; got.Branch != "feat/x" || got.RepoPath != repo ||
		!got.Wants(TriggerBranchMerged) || got.Remote != "origin" {
		t.Fatalf("poll target: %+v", got)
	}

	// The same state on the next poll must not move anything again.
	m.PollVCS()
	time.Sleep(300 * time.Millisecond)
	if moves := writer.cardMoves(); len(moves) != 2 {
		t.Fatalf("the branch is still merged, but the card must not move twice: %+v", moves)
	}

	// The comment says what moved the card, in the watcher's own words.
	comments := writer.cardComments("cardV")
	last := comments[len(comments)-1]
	if !strings.Contains(last, "ветка влита в main") {
		t.Fatalf("transition comment should carry the reason: %q", last)
	}
}

func TestClaimVCSEvent(t *testing.T) {
	st := openTestStore(t)

	fresh, err := st.ClaimVCSEvent("/repo", "feat/x", TriggerBranchMerged, "sha1")
	if err != nil || !fresh {
		t.Fatalf("first sighting: %v, %v", fresh, err)
	}
	fresh, err = st.ClaimVCSEvent("/repo", "feat/x", TriggerBranchMerged, "sha1")
	if err != nil || fresh {
		t.Fatalf("the same state must not fire twice: %v, %v", fresh, err)
	}
	// A new commit is a new occurrence.
	fresh, err = st.ClaimVCSEvent("/repo", "feat/x", TriggerBranchMerged, "sha2")
	if err != nil || !fresh {
		t.Fatalf("a changed marker is a new event: %v, %v", fresh, err)
	}
	// Другая ветка, другое событие и другой репозиторий учитываются отдельно.
	for _, args := range [][3]string{
		{"/repo", "feat/y", TriggerBranchMerged},
		{"/repo", "feat/x", TriggerBranchPushed},
		{"/other", "feat/x", TriggerBranchMerged},
	} {
		if fresh, err := st.ClaimVCSEvent(args[0], args[1], args[2], "sha2"); err != nil || !fresh {
			t.Fatalf("%v should be tracked separately: %v, %v", args, fresh, err)
		}
	}
}
