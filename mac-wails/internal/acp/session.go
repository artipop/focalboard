package acp

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/mattermost/focalboard/mac-wails/internal/acp/claudebridge"
	"github.com/mattermost/focalboard/mac-wails/internal/procgroup"
)

// Session is one agent run bound to a card.
type Session struct {
	ID         string
	CardID     string
	BoardID    string
	RepoPath   string
	BaseBranch string
	PromptText string

	Worktree WorktreeInfo

	mu         sync.Mutex
	status     SessionStatus
	cancel     context.CancelFunc
	cancelSent bool
	allowTools map[string]bool

	finalMu  sync.Mutex
	finalBuf strings.Builder

	seq atomic.Int64
}

func (s *Session) Status() SessionStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status
}

func (s *Session) toolAllowed(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.allowTools[name]
}

// appendEvent persists a session event with the next sequence number.
func (s *Session) appendEvent(m *Manager, kind string, payload any) {
	if err := m.store.AppendEvent(s.ID, s.seq.Add(1), kind, payload); err != nil {
		m.log.Warn("acp: failed to persist session event", "session", s.ID, "err", err)
	}
}

// runSession is the whole session lifecycle; it runs on its own goroutine.
func (m *Manager) runSession(s *Session) {
	defer m.wg.Done()
	defer m.releaseSession(s)

	// Wait for a concurrency slot.
	select {
	case m.sem <- struct{}{}:
		defer func() { <-m.sem }()
	case <-m.rootCtx.Done():
		m.finishSession(s, StatusCancelled, "приложение завершается")
		return
	}
	if m.rootCtx.Err() != nil {
		m.finishSession(s, StatusCancelled, "приложение завершается")
		return
	}

	ctx, cancel := context.WithTimeout(m.rootCtx, m.cfg.SessionTimeout())
	defer cancel()
	s.mu.Lock()
	s.cancel = cancel
	s.status = StatusRunning
	s.mu.Unlock()
	m.persistStatus(s, StatusRunning, "")

	// 1. Worktree.
	wt, err := CreateWorktree(ctx, s.RepoPath, s.BaseBranch, s.CardID, s.ID, m.cfg.WorktreeDir)
	if err != nil {
		m.finishSession(s, StatusFailed, fmt.Sprintf("не удалось создать git worktree: %v", err))
		return
	}
	s.Worktree = wt
	if err := m.store.UpdateSession(s.ID, StatusRunning, "", wt.Path, wt.Path, wt.Branch, "", nil); err != nil {
		m.log.Warn("acp: failed to persist worktree info", "session", s.ID, "err", err)
	}
	m.comment(s, fmt.Sprintf("🤖 Агент запущен.\nWorktree: `%s`\nВетка: `%s` (от `%s`)", wt.Path, wt.Branch, wt.BaseRef))

	// 2. Agent connection.
	finalText, runErr := m.runAgentTurn(ctx, s)

	// 3. Outcome.
	switch {
	case runErr == nil:
		m.finishSession(s, StatusDone, "")
		m.comment(s, doneComment(s, finalText))
	case ctx.Err() != nil && m.rootCtx.Err() != nil:
		m.finishSession(s, StatusCancelled, "приложение завершается")
	case s.wasCancelled():
		m.finishSession(s, StatusCancelled, "сессия отменена")
		m.comment(s, cancelComment(s))
	case ctx.Err() == context.DeadlineExceeded:
		m.finishSession(s, StatusFailed, fmt.Sprintf("таймаут сессии (%s)", m.cfg.SessionTimeout()))
		m.comment(s, failComment(s, fmt.Sprintf("таймаут %s", m.cfg.SessionTimeout())))
	default:
		m.finishSession(s, StatusFailed, runErr.Error())
		m.comment(s, failComment(s, runErr.Error()))
	}

	// 4. Worktree cleanup for unsuccessful sessions.
	if s.Status() != StatusDone && !m.cfg.KeepFailedWorktrees {
		if removed, err := RemoveWorktreeIfClean(context.Background(), s.RepoPath, s.Worktree); err != nil {
			m.log.Warn("acp: worktree cleanup failed", "session", s.ID, "err", err)
		} else if removed {
			s.Worktree = WorktreeInfo{}
		}
	}
}

// runAgentTurn builds the in-process ACP stack, runs one prompt and returns
// the agent's final message text.
func (m *Manager) runAgentTurn(ctx context.Context, s *Session) (string, error) {
	var (
		conn    *acpsdk.ClientSideConnection
		cleanup func()
		err     error
	)
	switch m.cfg.AgentMode {
	case "acp-command":
		conn, cleanup, err = m.connectExternal(ctx, s)
	default:
		conn, cleanup, err = m.connectClaude(ctx, s)
	}
	if err != nil {
		return "", err
	}
	defer cleanup()

	if _, err := conn.Initialize(ctx, acpsdk.InitializeRequest{
		ProtocolVersion: acpsdk.ProtocolVersionNumber,
		ClientCapabilities: acpsdk.ClientCapabilities{
			Fs: acpsdk.FileSystemCapabilities{ReadTextFile: true, WriteTextFile: true},
		},
	}); err != nil {
		return "", fmt.Errorf("initialize: %w", err)
	}

	sess, err := conn.NewSession(ctx, acpsdk.NewSessionRequest{Cwd: s.Worktree.Path, McpServers: []acpsdk.McpServer{}})
	if err != nil {
		return "", fmt.Errorf("session/new: %w", err)
	}
	if err := m.store.UpdateSession(s.ID, StatusRunning, string(sess.SessionId), s.Worktree.Path, s.Worktree.Path, s.Worktree.Branch, "", nil); err != nil {
		m.log.Warn("acp: failed to persist acp session id", "session", s.ID, "err", err)
	}

	// Register the ACP session id so cancellation can address it.
	s.mu.Lock()
	acpSessionID := sess.SessionId
	s.mu.Unlock()

	cancelACP := func() {
		s.mu.Lock()
		already := s.cancelSent
		s.cancelSent = true
		s.mu.Unlock()
		if !already {
			_ = conn.Cancel(context.Background(), acpsdk.CancelNotification{SessionId: acpSessionID})
		}
	}
	stop := context.AfterFunc(ctx, cancelACP)
	defer stop()

	resp, err := conn.Prompt(ctx, acpsdk.PromptRequest{
		SessionId: acpSessionID,
		Prompt:    []acpsdk.ContentBlock{acpsdk.TextBlock(s.PromptText)},
	})
	if err != nil {
		return "", fmt.Errorf("session/prompt: %w", err)
	}
	if resp.StopReason == acpsdk.StopReasonCancelled {
		s.markCancelled()
	}

	s.finalMu.Lock()
	final := s.finalBuf.String()
	s.finalMu.Unlock()
	return final, nil
}

// connectClaude wires the in-process claude bridge over io.Pipe.
func (m *Manager) connectClaude(ctx context.Context, s *Session) (*acpsdk.ClientSideConnection, func(), error) {
	claudeBin, err := m.findClaude()
	if err != nil {
		return nil, nil, err
	}
	bridge := claudebridge.New(claudebridge.Options{ClaudeBin: claudeBin, Logger: m.log})

	clientIn, agentOut := io.Pipe() // agent writes → client reads
	agentIn, clientOut := io.Pipe() // client writes → agent reads

	agentConn := acpsdk.NewAgentSideConnection(bridge, agentOut, agentIn)
	bridge.SetConn(agentConn)
	clientConn := acpsdk.NewClientSideConnection(&sessionClient{m: m, s: s}, clientOut, clientIn)

	cleanup := func() {
		bridge.KillAll(2 * time.Second)
		_ = clientOut.Close()
		_ = agentOut.Close()
	}
	return clientConn, cleanup, nil
}

// connectExternal spawns an arbitrary external ACP agent (config agentMode
// "acp-command") and connects over its stdio.
func (m *Manager) connectExternal(ctx context.Context, s *Session) (*acpsdk.ClientSideConnection, func(), error) {
	if len(m.cfg.AgentCommand) == 0 {
		return nil, nil, fmt.Errorf("agentMode is acp-command but agentCommand is empty")
	}
	proc, err := procgroup.Spawn(m.rootCtx, m.cfg.AgentCommand, s.Worktree.Path, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("spawn agent %q: %w", m.cfg.AgentCommand[0], err)
	}
	conn := acpsdk.NewClientSideConnection(&sessionClient{m: m, s: s}, proc.Stdin, proc.Stdout)
	cleanup := func() {
		proc.KillGroup(2 * time.Second)
		_ = proc.Wait()
	}
	return conn, cleanup, nil
}

func (s *Session) markCancelled() {
	s.mu.Lock()
	s.cancelSent = true
	s.mu.Unlock()
}

func (s *Session) wasCancelled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cancelSent
}

// ---- card comments ----

func doneComment(s *Session, finalText string) string {
	var b strings.Builder
	b.WriteString("✅ Агент завершил работу.\n\n")
	if t := strings.TrimSpace(finalText); t != "" {
		b.WriteString(truncateRunes(t, 4000))
		b.WriteString("\n\n")
	}
	fmt.Fprintf(&b, "Worktree: `%s`\nВетка: `%s`\n", s.Worktree.Path, s.Worktree.Branch)
	fmt.Fprintf(&b, "Посмотреть дифф: `git -C %s diff %s`", s.Worktree.Path, s.Worktree.BaseRef)
	return b.String()
}

func failComment(s *Session, reason string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "❌ Сессия агента завершилась с ошибкой: %s", truncateRunes(reason, 1500))
	if s.Worktree.Path != "" {
		fmt.Fprintf(&b, "\nWorktree (если остался): `%s`", s.Worktree.Path)
	}
	return b.String()
}

func cancelComment(s *Session) string {
	return "🛑 Сессия агента отменена."
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
