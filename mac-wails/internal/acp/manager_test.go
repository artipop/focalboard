package acp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeWriter records board writes.
type fakeWriter struct {
	mu       sync.Mutex
	comments map[string][]string // cardID → comments
}

func newFakeWriter() *fakeWriter { return &fakeWriter{comments: map[string][]string{}} }

func (w *fakeWriter) AddComment(ctx context.Context, cardID, text string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.comments[cardID] = append(w.comments[cardID], text)
	return nil
}

func (w *fakeWriter) MoveCard(ctx context.Context, cardID, optionID string) error { return nil }

func (w *fakeWriter) cardComments(cardID string) []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string(nil), w.comments[cardID]...)
}

// fakeEmitter records UI events.
type fakeEmitter struct {
	mu     sync.Mutex
	events []string
}

func (e *fakeEmitter) Emit(event string, payload any) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.events = append(e.events, event)
}

// fakeEvents is a manual BoardEvents feed.
type fakeEvents struct{ ch chan CardMoved }

func (f *fakeEvents) Subscribe(ctx context.Context) (<-chan CardMoved, error) { return f.ch, nil }

// writeFakeClaude installs a shell script speaking just enough of the
// stream-json protocol for the bridge: reads the user message, streams one
// text delta, asks one permission, finishes with a result.
func writeFakeClaude(t *testing.T, script string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-claude")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

const fakeClaudeHappy = `#!/bin/sh
read line
printf '%s\n' '{"type":"system","subtype":"init","session_id":"fake-1"}'
printf '%s\n' '{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"fake work done"}}}'
printf '%s\n' '{"type":"result","is_error":false,"result":"fake work done"}'
`

const fakeClaudeHang = `#!/bin/sh
read line
printf '%s\n' '{"type":"system","subtype":"init","session_id":"fake-2"}'
sleep 300
`

func testManager(t *testing.T, claudeScript string, mutate func(*Config)) (*Manager, *fakeWriter, *fakeEvents, string) {
	t.Helper()
	repo := initTestRepo(t)
	dir := t.TempDir()
	cfg := DefaultConfig(dir)
	cfg.ClaudePath = writeFakeClaude(t, claudeScript)
	cfg.RepoWhitelist = []string{filepath.Dir(repo)}
	cfg.WorktreeDir = filepath.Join(dir, "wt")
	if mutate != nil {
		mutate(&cfg)
	}

	st, err := OpenStore(filepath.Join(dir, "acp.db"))
	if err != nil {
		t.Fatal(err)
	}
	writer := newFakeWriter()
	events := &fakeEvents{ch: make(chan CardMoved, 16)}
	m := NewManager(cfg, "", st, writer, &fakeEmitter{}, nil)
	if err := m.Start(context.Background(), events); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Shutdown(3 * time.Second) })
	return m, writer, events, repo
}

func moveEvent(cardID, repo, from, to string) CardMoved {
	return CardMoved{
		EventID:    "ev-" + cardID + to,
		CardID:     cardID,
		BoardID:    "board1",
		Title:      "Test task",
		Body:       "Do nothing useful.",
		Props:      map[string]string{"repo_path": repo},
		FromColumn: Column{PropertyID: "p1", PropertyName: "Status", OptionID: from, Name: columnName(from)},
		ToColumn:   Column{PropertyID: "p1", PropertyName: "Status", OptionID: to, Name: columnName(to)},
		At:         time.Now(),
	}
}

func columnName(optionID string) string {
	if optionID == "opt-agent" {
		return "To Agent"
	}
	return "Backlog"
}

func waitFor(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestTriggerRunsSessionToDone(t *testing.T) {
	m, writer, events, repo := testManager(t, fakeClaudeHappy, nil)

	events.ch <- moveEvent("card1", repo, "opt-backlog", "opt-agent")

	waitFor(t, 15*time.Second, "session done", func() bool {
		sessions, _, err := m.store.SessionsForCard("card1")
		return err == nil && len(sessions) == 1 && sessions[0].Status == StatusDone
	})

	comments := writer.cardComments("card1")
	if len(comments) < 2 {
		t.Fatalf("expected start + result comments, got %v", comments)
	}
	last := comments[len(comments)-1]
	if !strings.Contains(last, "fake work done") || !strings.Contains(last, repo) {
		t.Errorf("final comment lacks agent output or repo path: %q", last)
	}

	// Default mode runs in the repo itself: no worktree is created.
	sessions, _, _ := m.store.SessionsForCard("card1")
	if sessions[0].Cwd != repo {
		t.Errorf("expected session cwd %q, got %q", repo, sessions[0].Cwd)
	}
	if sessions[0].WorktreePath != "" {
		t.Errorf("no worktree expected in default mode, got %q", sessions[0].WorktreePath)
	}
}

func TestWorktreeModeAlways(t *testing.T) {
	m, writer, events, repo := testManager(t, fakeClaudeHappy, func(c *Config) {
		c.WorktreeMode = "always"
	})

	events.ch <- moveEvent("cardWT", repo, "opt-backlog", "opt-agent")
	waitFor(t, 15*time.Second, "worktree session done", func() bool {
		sessions, _, err := m.store.SessionsForCard("cardWT")
		return err == nil && len(sessions) == 1 && sessions[0].Status == StatusDone
	})

	sessions, _, _ := m.store.SessionsForCard("cardWT")
	if wt := sessions[0].WorktreePath; wt == "" {
		t.Error("worktree path missing in always mode")
	} else if _, err := os.Stat(wt); err != nil {
		t.Errorf("worktree of done session was removed: %v", err)
	}
	comments := writer.cardComments("cardWT")
	if last := comments[len(comments)-1]; !strings.Contains(last, "Worktree") {
		t.Errorf("final comment lacks worktree info: %q", last)
	}
}

func TestRepoBusyRejectedWithoutWorktrees(t *testing.T) {
	m, writer, events, repo := testManager(t, fakeClaudeHang, nil)

	events.ch <- moveEvent("cardA", repo, "opt-backlog", "opt-agent")
	waitFor(t, 10*time.Second, "first session running", func() bool {
		sessions, _, err := m.store.SessionsForCard("cardA")
		return err == nil && len(sessions) == 1 && sessions[0].Status == StatusRunning
	})

	events.ch <- moveEvent("cardB", repo, "opt-backlog", "opt-agent")
	waitFor(t, 5*time.Second, "busy-repo comment on second card", func() bool {
		return len(writer.cardComments("cardB")) >= 1
	})
	if got := writer.cardComments("cardB")[0]; !strings.Contains(got, "уже работает") {
		t.Errorf("expected busy-repo error comment, got %q", got)
	}
	if sessions, _, _ := m.store.SessionsForCard("cardB"); len(sessions) != 0 {
		t.Errorf("second card must not get a session, got %d", len(sessions))
	}
}

func TestRapidMovesStartOneSession(t *testing.T) {
	m, _, events, repo := testManager(t, fakeClaudeHappy, nil)

	// Spec acceptance §10.4: five rapid back-and-forth moves → one session.
	for i := 0; i < 5; i++ {
		events.ch <- moveEvent("card2", repo, "opt-backlog", "opt-agent")
	}

	waitFor(t, 15*time.Second, "exactly one session, terminal", func() bool {
		sessions, _, err := m.store.SessionsForCard("card2")
		return err == nil && len(sessions) == 1 && sessions[0].Status.Terminal()
	})
	// Give the trigger loop a beat to (incorrectly) start more.
	time.Sleep(200 * time.Millisecond)
	sessions, _, err := m.store.SessionsForCard("card2")
	if err != nil || len(sessions) != 1 {
		t.Fatalf("expected exactly 1 session, got %d (err=%v)", len(sessions), err)
	}
}

func TestInvalidRepoPathComments(t *testing.T) {
	m, writer, events, _ := testManager(t, fakeClaudeHappy, nil)

	ev := moveEvent("card3", "/nonexistent/path", "opt-backlog", "opt-agent")
	events.ch <- ev

	waitFor(t, 5*time.Second, "error comment", func() bool {
		return len(writer.cardComments("card3")) >= 1
	})
	if got := writer.cardComments("card3")[0]; !strings.Contains(got, "Агент не запущен") {
		t.Errorf("expected clear error comment, got %q", got)
	}
	if sessions, _, _ := m.store.SessionsForCard("card3"); len(sessions) != 0 {
		t.Errorf("no session should have been created, got %d", len(sessions))
	}
}

func TestMoveBackCancelsSession(t *testing.T) {
	m, _, events, repo := testManager(t, fakeClaudeHang, nil)

	events.ch <- moveEvent("card4", repo, "opt-backlog", "opt-agent")
	waitFor(t, 10*time.Second, "session running", func() bool {
		sessions, _, err := m.store.SessionsForCard("card4")
		return err == nil && len(sessions) == 1 && sessions[0].Status == StatusRunning
	})

	// Let the fake agent actually start before yanking the card back.
	time.Sleep(300 * time.Millisecond)
	events.ch <- moveEvent("card4", repo, "opt-agent", "opt-backlog")

	start := time.Now()
	waitFor(t, 10*time.Second, "session cancelled", func() bool {
		sessions, _, err := m.store.SessionsForCard("card4")
		return err == nil && len(sessions) == 1 && sessions[0].Status.Terminal()
	})
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("cancellation took %s", elapsed)
	}
	sessions, _, _ := m.store.SessionsForCard("card4")
	if sessions[0].Status != StatusCancelled {
		t.Errorf("expected cancelled, got %s", sessions[0].Status)
	}
}

func TestRecoveryMarksStaleFailed(t *testing.T) {
	repo := initTestRepo(t)
	dir := t.TempDir()
	cfg := DefaultConfig(dir)
	cfg.ClaudePath = writeFakeClaude(t, fakeClaudeHappy)
	cfg.RepoWhitelist = []string{filepath.Dir(repo)}

	dbPath := filepath.Join(dir, "acp.db")
	st, err := OpenStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.InsertSession(SessionRecord{
		ID: "stale1", CardID: "card9", BoardID: "b", AgentKind: "claude",
		Status: StatusRunning, StartedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	writer := newFakeWriter()
	m := NewManager(cfg, "", st, writer, &fakeEmitter{}, nil)
	if err := m.Start(context.Background(), &fakeEvents{ch: make(chan CardMoved)}); err != nil {
		t.Fatal(err)
	}
	defer m.Shutdown(time.Second)

	sessions, _, err := m.store.SessionsForCard("card9")
	if err != nil || len(sessions) != 1 || sessions[0].Status != StatusFailed {
		t.Fatalf("stale session not recovered to failed: %+v err=%v", sessions, err)
	}
	if got := writer.cardComments("card9"); len(got) != 1 || !strings.Contains(got[0], "прервана") {
		t.Errorf("expected interruption comment, got %v", got)
	}
}

func TestFindClaudeErrorIsClear(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	cfg.ClaudePath = "/definitely/not/here"
	m := NewManager(cfg, "", nil, newFakeWriter(), &fakeEmitter{}, nil)
	if _, err := m.findClaude(); err == nil || !strings.Contains(err.Error(), "claudePath") {
		t.Errorf("expected clear claudePath error, got %v", err)
	}
}
