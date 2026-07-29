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

// fakeEmitter records UI events with their payloads.
type fakeEmitter struct {
	mu       sync.Mutex
	events   []string
	payloads []map[string]any
}

func (e *fakeEmitter) Emit(event string, payload any) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.events = append(e.events, event)
	p, _ := payload.(map[string]any)
	e.payloads = append(e.payloads, p)
}

// pendingPermissionID returns the request id of the permission prompt the UI
// was asked to answer, or "" while none is waiting.
func (e *fakeEmitter) pendingPermissionID() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	for i, name := range e.events {
		if name != EventPermission {
			continue
		}
		p := e.payloads[i]
		if p == nil || p["pending"] != true {
			continue
		}
		if id, ok := p["requestId"].(string); ok {
			return id
		}
	}
	return ""
}

// fakeReader serves one card to the "open a console" path.
type fakeReader struct{ ev CardMoved }

func (r *fakeReader) CardByID(ctx context.Context, cardID string) (CardMoved, error) {
	ev := r.ev
	ev.CardID = cardID
	return ev, nil
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
	m, w, ev, repo, _ := testManagerWithEmitter(t, claudeScript, mutate)
	return m, w, ev, repo
}

func testManagerWithEmitter(t *testing.T, claudeScript string, mutate func(*Config)) (*Manager, *fakeWriter, *fakeEvents, string, *fakeEmitter) {
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
	emitter := &fakeEmitter{}
	m := NewManager(cfg, "", st, writer, emitter, nil)
	m.SetBoardReader(&fakeReader{ev: CardMoved{
		BoardID: "board1",
		Title:   "Test task",
		Body:    "Do nothing useful.",
		Props:   map[string]string{"repo_path": repo},
	}})
	if err := m.Start(context.Background(), events); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Shutdown(3 * time.Second) })
	return m, writer, events, repo, emitter
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

// fakeClaudeMultiTurn stays alive across turns the way the real CLI does,
// answering every user message it reads with one text delta and a result.
const fakeClaudeMultiTurn = `#!/bin/sh
turn=0
while read line; do
  turn=$((turn+1))
  printf '%s\n' '{"type":"system","subtype":"init","session_id":"fake-multi"}'
  printf '{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"turn %s done"}}}\n' "$turn"
  printf '{"type":"result","is_error":false,"result":"turn %s done"}\n' "$turn"
done
`

// fakeClaudeAsksPermission requests a tool that is not on autoAllowTools, then waits
// for the control_response before finishing the turn.
const fakeClaudeAsksPermission = `#!/bin/sh
read line
printf '%s\n' '{"type":"system","subtype":"init","session_id":"fake-perm"}'
printf '%s\n' '{"type":"control_request","request_id":"r1","request":{"subtype":"can_use_tool","tool_name":"WebFetch","tool_use_id":"tu1","description":"fetch https://example.com","input":{}}}'
read response
printf '%s\n' '{"type":"result","is_error":false,"result":"finished"}'
`

func liveSession(t *testing.T, m *Manager, cardID string) *Session {
	t.Helper()
	s, err := m.StartSessionForCard(cardID)
	if err != nil {
		t.Fatalf("open console session: %v", err)
	}
	return s
}

func waitStatus(t *testing.T, s *Session, want SessionStatus) {
	t.Helper()
	waitFor(t, 15*time.Second, "session status "+string(want), func() bool {
		return s.Status() == want
	})
}

func TestConsoleSessionRunsSecondTurn(t *testing.T) {
	m, writer, _, _, _ := testManagerWithEmitter(t, fakeClaudeMultiTurn, nil)

	s := liveSession(t, m, "cardConsole")

	// A console session parks between turns instead of finishing.
	waitStatus(t, s, StatusIdle)
	if got := writer.cardComments("cardConsole"); len(got) < 2 || !strings.Contains(got[len(got)-1], "turn 1 done") {
		t.Fatalf("first turn should still report to the card, got %v", got)
	}

	if err := m.PromptSession(s.ID, "keep going"); err != nil {
		t.Fatalf("prompt: %v", err)
	}
	waitFor(t, 15*time.Second, "second turn to run", func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.turnNo >= 2
	})
	waitStatus(t, s, StatusIdle)

	// Only the first turn comments; the rest belongs to the console.
	if got := writer.cardComments("cardConsole"); strings.Contains(strings.Join(got, "\n"), "turn 2 done") {
		t.Errorf("second turn should not comment on the card, got %v", got)
	}

	if err := m.CloseSession(s.ID); err != nil {
		t.Fatalf("close: %v", err)
	}
	waitStatus(t, s, StatusDone)
	if last := writer.cardComments("cardConsole"); !strings.Contains(last[len(last)-1], "ходов: 2") {
		t.Errorf("closing comment should record the turn count, got %v", last)
	}
}

func TestPermissionPromptAnsweredByUser(t *testing.T) {
	m, _, _, _, emitter := testManagerWithEmitter(t, fakeClaudeAsksPermission, nil)

	s := liveSession(t, m, "cardPerm")

	// A watched session asks instead of rejecting by policy.
	waitStatus(t, s, StatusWaitingPermission)
	var requestID string
	waitFor(t, 5*time.Second, "permission prompt on the console", func() bool {
		requestID = emitter.pendingPermissionID()
		return requestID != ""
	})

	if err := m.AnswerPermission(s.ID, requestID, "allow"); err != nil {
		t.Fatalf("answer permission: %v", err)
	}
	waitStatus(t, s, StatusIdle)

	// The slot is gone once answered.
	if err := m.AnswerPermission(s.ID, requestID, "allow"); err == nil {
		t.Error("answering the same prompt twice should fail")
	}
}

func TestPermissionRejectedWithoutConsole(t *testing.T) {
	m, _, events, repo, emitter := testManagerWithEmitter(t, fakeClaudeAsksPermission, nil)

	// A card-triggered session has nobody watching: it must decide by policy
	// straight away rather than block until the prompt times out.
	events.ch <- moveEvent("cardAuto", repo, "opt-backlog", "opt-agent")
	waitFor(t, 15*time.Second, "session done", func() bool {
		sessions, _, err := m.store.SessionsForCard("cardAuto")
		return err == nil && len(sessions) == 1 && sessions[0].Status == StatusDone
	})
	if id := emitter.pendingPermissionID(); id != "" {
		t.Errorf("unattended session should not have prompted the user, got request %s", id)
	}
}

func TestDetachEndsIdleSession(t *testing.T) {
	m, _, _, _, _ := testManagerWithEmitter(t, fakeClaudeMultiTurn, nil)

	s := liveSession(t, m, "cardDetach")
	waitStatus(t, s, StatusIdle)

	// The last console leaving must not leave the repository held.
	m.DetachSession(s.ID)
	waitStatus(t, s, StatusDone)
}

func TestIdleConsoleFreesConcurrencySlot(t *testing.T) {
	m, _, events, repo, _ := testManagerWithEmitter(t, fakeClaudeMultiTurn, func(c *Config) {
		c.MaxConcurrent = 1
		c.WorktreeMode = "always" // otherwise the repo lock, not the slot, would block
	})

	s := liveSession(t, m, "cardIdle")
	waitStatus(t, s, StatusIdle)

	// The idle console holds no slot, so another card still runs.
	events.ch <- moveEvent("cardOther", repo, "opt-backlog", "opt-agent")
	waitFor(t, 15*time.Second, "second card to finish while a console idles", func() bool {
		sessions, _, err := m.store.SessionsForCard("cardOther")
		return err == nil && len(sessions) == 1 && sessions[0].Status == StatusDone
	})
	if s.Status() != StatusIdle {
		t.Errorf("console session should still be idle, got %s", s.Status())
	}
}

// fakeClaudeSlowPermission waits before asking, leaving a window for a console
// to attach to an already-running session.
const fakeClaudeSlowPermission = `#!/bin/sh
read line
printf '%s\n' '{"type":"system","subtype":"init","session_id":"fake-slow"}'
sleep 1
printf '%s\n' '{"type":"control_request","request_id":"r1","request":{"subtype":"can_use_tool","tool_name":"WebFetch","tool_use_id":"tu1","description":"fetch https://example.com","input":{}}}'
read response
printf '%s\n' '{"type":"result","is_error":false,"result":"finished"}'
`

// A card-triggered session starts unattended, so opening its card mid-run must
// be enough to get the permission prompt instead of a policy rejection.
func TestConsoleAttachedAfterTriggerGetsPrompt(t *testing.T) {
	m, _, events, repo, emitter := testManagerWithEmitter(t, fakeClaudeSlowPermission, nil)

	events.ch <- moveEvent("cardLate", repo, "opt-backlog", "opt-agent")

	var s *Session
	waitFor(t, 10*time.Second, "session to start", func() bool {
		m.mu.Lock()
		defer m.mu.Unlock()
		s = m.byCard["cardLate"]
		return s != nil
	})
	if !m.AttachSession(s.ID) {
		t.Fatal("AttachSession refused a live session")
	}

	waitStatus(t, s, StatusWaitingPermission)
	var requestID string
	waitFor(t, 5*time.Second, "permission prompt on the console", func() bool {
		requestID = emitter.pendingPermissionID()
		return requestID != ""
	})
	if err := m.AnswerPermission(s.ID, requestID, "allow"); err != nil {
		t.Fatalf("answer permission: %v", err)
	}

	// Attaching also turned it into a console session: it waits rather than ending.
	waitStatus(t, s, StatusIdle)
}

// fakeClaudeSlowTurn keeps one turn running long enough to close the card
// while the agent is still working.
const fakeClaudeSlowTurn = `#!/bin/sh
while read line; do
  printf '%s\n' '{"type":"system","subtype":"init","session_id":"fake-slow-turn"}'
  sleep 1
  printf '%s\n' '{"type":"result","is_error":false,"result":"done"}'
done
`

// Closing the card mid-turn detaches a console that cannot end the session yet,
// because it is not idle. The session must still not park afterwards: it would
// hold its repository for the whole idle timeout with nobody watching, and the
// next card on that repo would be refused.
func TestDetachDuringTurnDoesNotParkSession(t *testing.T) {
	m, _, _, repo, _ := testManagerWithEmitter(t, fakeClaudeSlowTurn, nil)

	s := liveSession(t, m, "cardBusy")
	waitStatus(t, s, StatusRunning)
	m.DetachSession(s.ID)

	waitStatus(t, s, StatusDone)

	// The repository is free again, so another card can take it.
	waitFor(t, 5*time.Second, "repo to be released", func() bool {
		m.mu.Lock()
		defer m.mu.Unlock()
		for _, other := range m.active {
			if other.RepoPath == repo {
				return false
			}
		}
		return true
	})
}

// Talking to a session must not claim a console slot: an unpaired increment
// would keep it alive after the only console is gone.
func TestPromptDoesNotLeakConsoleCount(t *testing.T) {
	m, _, _, _, _ := testManagerWithEmitter(t, fakeClaudeMultiTurn, nil)

	s := liveSession(t, m, "cardCount")
	waitStatus(t, s, StatusIdle)

	if err := m.PromptSession(s.ID, "again"); err != nil {
		t.Fatalf("prompt: %v", err)
	}
	waitFor(t, 15*time.Second, "second turn", func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.turnNo >= 2
	})
	waitStatus(t, s, StatusIdle)

	// One console opened it, so one detach must be enough to end it.
	m.DetachSession(s.ID)
	waitStatus(t, s, StatusDone)
}
