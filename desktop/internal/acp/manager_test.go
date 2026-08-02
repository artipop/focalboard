package acp

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// testLogger keeps test output quiet unless something goes wrong.
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// fakeWriter records board writes.
type fakeWriter struct {
	mu          sync.Mutex
	comments    map[string][]string // cardID → comments
	moves       []cardMove          // moves by column name, in order
	attachments []attachment
}

// cardMove is one MoveCardByOptionName call.
type cardMove struct {
	cardID   string
	property string
	option   string
}

// attachment is one AttachFile call; the bytes are kept so tests can tell the
// files apart.
type attachment struct {
	cardID string
	name   string
	mime   string
	data   []byte
}

func newFakeWriter() *fakeWriter { return &fakeWriter{comments: map[string][]string{}} }

func (w *fakeWriter) AddComment(ctx context.Context, cardID, text string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.comments[cardID] = append(w.comments[cardID], text)
	return nil
}

func (w *fakeWriter) MoveCard(ctx context.Context, cardID, optionID string) error { return nil }

func (w *fakeWriter) MoveCardByOptionName(ctx context.Context, cardID, property, option string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.moves = append(w.moves, cardMove{cardID: cardID, property: property, option: option})
	return nil
}

func (w *fakeWriter) AttachFile(ctx context.Context, cardID, filename, mimeType string, data []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.attachments = append(w.attachments, attachment{cardID: cardID, name: filename, mime: mimeType, data: data})
	return nil
}

func (w *fakeWriter) cardComments(cardID string) []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string(nil), w.comments[cardID]...)
}

func (w *fakeWriter) cardMoves() []cardMove {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]cardMove(nil), w.moves...)
}

func (w *fakeWriter) cardAttachments() []attachment {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]attachment(nil), w.attachments...)
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

func testManager(t *testing.T, scenario string, mutate func(*Config)) (*Manager, *fakeWriter, *fakeEvents, string) {
	t.Helper()
	m, w, ev, repo, _ := testManagerWithEmitter(t, scenario, mutate)
	return m, w, ev, repo
}

func testManagerWithEmitter(t *testing.T, scenario string, mutate func(*Config)) (*Manager, *fakeWriter, *fakeEvents, string, *fakeEmitter) {
	t.Helper()
	repo := initTestRepo(t)
	dir := t.TempDir()
	cfg := DefaultConfig(dir)
	// Every kind is an ACP process now, so the fallback path is the one that
	// spells the agent out: the fake agent is the whole command.
	cfg.AgentMode = agentModeCommand
	cfg.AgentCommand = []string{writeFakeAgent(t, scenario)}
	cfg.RepoWhitelist = []string{filepath.Dir(repo)}
	cfg.WorktreeDir = filepath.Join(dir, "wt")
	if mutate != nil {
		mutate(&cfg)
	}
	// LoadConfig is what fills the column registry from the trigger-column keys
	// on a real install; a config built in code needs the same step.
	cfg = withColumns(cfg)

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
		return DefaultTriggerColumn
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
	if !strings.Contains(last, "fake work done") {
		t.Errorf("final comment lacks agent output: %q", last)
	}

	// The default gives the card its own worktree, on a branch named after the
	// card — which is what the card displays and what its deploy publishes.
	sessions, _, _ := m.store.SessionsForCard("card1")
	if sessions[0].WorktreePath == "" {
		t.Error("expected a worktree in the default mode")
	}
	if sessions[0].Cwd != sessions[0].WorktreePath {
		t.Errorf("session ran in %q, not in its worktree %q", sessions[0].Cwd, sessions[0].WorktreePath)
	}
	if branch := sessions[0].Branch; !strings.HasPrefix(branch, "acp/test-task-") {
		t.Errorf("branch %q is not named after the card", branch)
	}
	if !strings.Contains(last, sessions[0].WorktreePath) {
		t.Errorf("final comment lacks the worktree: %q", last)
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
	// Worktrees are the default now, so this rule only applies to an install
	// that has turned them off.
	m, writer, events, repo := testManager(t, fakeClaudeHang, func(c *Config) {
		c.WorktreeMode = "never"
	})

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
	cfg.AgentMode = agentModeCommand
	cfg.AgentCommand = []string{writeFakeAgent(t, fakeClaudeHappy)}
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

// A missing adapter has to be said in the words that fix it — the package to
// install — because it is the first thing a machine without one runs into.
func TestMissingAdapterErrorIsActionable(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	m := NewManager(cfg, "", nil, newFakeWriter(), &fakeEmitter{}, nil)
	if _, err := m.agentLaunch(AgentEntry{Name: "c", Kind: "claude", BinPath: "/definitely/not/here"}); err == nil {
		t.Fatal("a binPath that does not exist should error")
	}
	// Nothing installed and no npx: the message names the package.
	t.Setenv("PATH", t.TempDir())
	_, err := m.agentLaunch(AgentEntry{Name: "c", Kind: "claude"})
	if err == nil || !strings.Contains(err.Error(), "@zed-industries/claude-code-acp") {
		t.Errorf("expected the npm package in the error, got %v", err)
	}
}

func liveSession(t *testing.T, m *Manager, cardID string) *Session {
	t.Helper()
	s, err := m.StartSessionForCard(cardID, "")
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

// planningManager wires a manager whose registries hold one repo and one agent,
// which is what a planning session resolves against.
func planningManager(t *testing.T, script string) (*Manager, string) {
	t.Helper()
	m, _, _, repo, _ := testManagerWithEmitter(t, script, func(c *Config) {
		// The fake agent is the whole command, so the registry entry names it
		// the way a user names an arbitrary ACP agent.
		c.Agents = []AgentEntry{{Name: "planner", Kind: AgentKindACP, Command: c.AgentCommand}}
	})
	// The repo is created by the helper, so its path is only known now.
	m.cfgMu.Lock()
	m.cfg.Repos = []RepoEntry{{Name: "app", Path: repo}}
	m.cfgMu.Unlock()
	return m, repo
}

func TestPlanningSessionComposesTask(t *testing.T) {
	m, _ := planningManager(t, fakeClaudeEcho)

	s, err := m.StartPlanningSession("app", "planner")
	if err != nil {
		t.Fatalf("start planning session: %v", err)
	}
	if s.CardID != "" {
		t.Errorf("a planning session must not be bound to a card, got %q", s.CardID)
	}
	waitStatus(t, s, StatusIdle)

	text, err := m.ComposeTask(s.ID)
	if err != nil {
		t.Fatalf("compose task: %v", err)
	}
	title, _, found := strings.Cut(text, "\n")
	if !found || !strings.Contains(title, "Кэшировать") {
		t.Errorf("expected a title on the first line, got %q", text)
	}
	waitStatus(t, s, StatusIdle)
}

// Planning only reads, so it must not take the working copy away from cards.
func TestPlanningSessionDoesNotHoldRepo(t *testing.T) {
	m, repo := planningManager(t, fakeClaudeEcho)

	s, err := m.StartPlanningSession("app", "planner")
	if err != nil {
		t.Fatalf("start planning session: %v", err)
	}
	waitStatus(t, s, StatusIdle)

	if _, err := m.StartSessionForEvent(moveEvent("cardWhilePlanning", repo, "opt-backlog", "opt-agent")); err != nil {
		t.Fatalf("a card session was refused while planning: %v", err)
	}
}

// Whatever the global policy allows, planning is held to read-only tools.
func TestPlanningSessionIsReadOnly(t *testing.T) {
	m, _ := planningManager(t, fakeClaudeEcho)

	s, err := m.StartPlanningSession("app", "planner")
	if err != nil {
		t.Fatalf("start planning session: %v", err)
	}
	for _, tool := range []string{"Read", "Grep", "Glob"} {
		if !s.autoAllowed(tool, nil, m.cfg) {
			t.Errorf("%s should run unasked while planning", tool)
		}
	}
	// Looking around is allowed, changing things is not — even though the
	// global policy permits both.
	if !s.autoAllowed("Bash", bashInput("git status"), m.cfg) {
		t.Error("planning should be able to inspect the repository with the shell")
	}
	for _, tool := range []string{"Write", "Edit"} {
		if s.autoAllowed(tool, nil, m.cfg) {
			t.Errorf("%s must not run unasked while planning", tool)
		}
		if !m.cfg.ToolAllowed(tool, nil) {
			t.Errorf("test premise broken: %s should be on the global allow list", tool)
		}
	}
	if s.autoAllowed("Bash", bashInput("rm -rf build"), m.cfg) {
		t.Error("planning must ask before a destructive command")
	}
}

func TestPlanningSessionNeedsAnAgent(t *testing.T) {
	m, _, _, _, _ := testManagerWithEmitter(t, fakeClaudeEcho, nil)

	// A repository is optional; an agent is not.
	if _, err := m.StartPlanningSession("", ""); err == nil {
		t.Fatal("expected an error when no agent is registered")
	} else if !strings.Contains(err.Error(), "агент") {
		t.Errorf("error should name the missing registry, got %v", err)
	}
}

// Planning is useful before there is any code to point at, so a session must
// start with no repository and run in a scratch directory of its own.
func TestPlanningSessionWithoutRepository(t *testing.T) {
	m, repo := planningManager(t, fakeClaudeEcho)

	s, err := m.StartPlanningSession("", "planner")
	if err != nil {
		t.Fatalf("start repo-less planning session: %v", err)
	}
	if s.RepoPath == repo {
		t.Fatal("session should not have picked the registered repository")
	}
	if s.scratchDir == "" || s.RepoPath != s.scratchDir {
		t.Fatalf("expected a scratch working directory, got %q", s.RepoPath)
	}
	if _, err := os.Stat(s.scratchDir); err != nil {
		t.Fatalf("scratch directory should exist while the session runs: %v", err)
	}
	waitStatus(t, s, StatusIdle)

	// It goes away with the session rather than piling up in the temp dir.
	if err := m.CloseSession(s.ID); err != nil {
		t.Fatalf("close: %v", err)
	}
	waitStatus(t, s, StatusDone)
	waitFor(t, 5*time.Second, "scratch dir to be removed", func() bool {
		_, err := os.Stat(s.scratchDir)
		return os.IsNotExist(err)
	})
}

func TestPlanningSessionRejectsUnknownRepository(t *testing.T) {
	m, _ := planningManager(t, fakeClaudeEcho)

	if _, err := m.StartPlanningSession("nope", "planner"); err == nil {
		t.Fatal("expected an error for a repository that is not registered")
	}
}

// lastAgentText returns what the agent finally said, as stored in the log.
func lastAgentText(t *testing.T, m *Manager, s *Session) string {
	t.Helper()
	_, events, err := m.store.SessionsForCard(s.CardID)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, ev := range events {
		if ev.Kind == "chunk" {
			var p struct {
				Text string `json:"text"`
			}
			_ = json.Unmarshal(ev.Payload, &p)
			b.WriteString(p.Text)
		}
	}
	return b.String()
}

// A card with no repository tag is exactly when someone wants to open a console
// on it, so the choice can be supplied instead of failing.
func TestConsoleCanNameTheRepository(t *testing.T) {
	m, repo := planningManager(t, fakeClaudeMultiTurn)

	// The fake card carries repo_path, so drop it to get an untagged card.
	m.reader = &fakeReader{ev: CardMoved{BoardID: "board1", Title: "Untagged", Props: map[string]string{}}}

	if _, err := m.StartSessionForCard("cardNoRepo", ""); err == nil {
		t.Fatal("expected the untagged card to be refused without a choice")
	}

	s, err := m.StartSessionForCard("cardNoRepo", "app")
	if err != nil {
		t.Fatalf("naming the repository should start the session: %v", err)
	}
	if s.RepoPath != repo {
		t.Errorf("session should run in the named repository, got %q", s.RepoPath)
	}

	if _, err := m.StartSessionForCard("cardOther", "nope"); err == nil {
		t.Error("an unknown repository name should be refused")
	}
}
