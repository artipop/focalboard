package acp

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/mattermost/focalboard/mac-wails/internal/acp/claudebridge"
	"github.com/mattermost/focalboard/mac-wails/internal/acp/codexbridge"
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
	Agent      AgentEntry // resolved agent (kind/bin/model/env/prompt)

	Worktree     WorktreeInfo
	usedWorktree bool // a dedicated worktree was actually created

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

	// 1. Working directory: a dedicated worktree, or the repo itself.
	if m.cfg.UseWorktrees() {
		wt, err := CreateWorktree(ctx, s.RepoPath, s.BaseBranch, s.CardID, s.ID, m.cfg.WorktreeDir)
		if err != nil {
			m.finishSession(s, StatusFailed, fmt.Sprintf("не удалось создать git worktree: %v", err))
			return
		}
		s.Worktree = wt
		s.usedWorktree = true
		if err := m.store.UpdateSession(s.ID, StatusRunning, "", wt.Path, wt.Path, wt.Branch, "", nil); err != nil {
			m.log.Warn("acp: failed to persist worktree info", "session", s.ID, "err", err)
		}
		m.comment(s, fmt.Sprintf("🤖 Агент запущен.\nWorktree: `%s`\nВетка: `%s` (от `%s`)", wt.Path, wt.Branch, wt.BaseRef))
	} else {
		s.Worktree = WorktreeInfo{Path: s.RepoPath, BaseRef: "HEAD"}
		if err := m.store.UpdateSession(s.ID, StatusRunning, "", s.RepoPath, "", "", "", nil); err != nil {
			m.log.Warn("acp: failed to persist session cwd", "session", s.ID, "err", err)
		}
		m.comment(s, fmt.Sprintf("🤖 Агент запущен прямо в репозитории `%s`.", s.RepoPath))
	}

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
	if s.usedWorktree && s.Status() != StatusDone && !m.cfg.KeepFailedWorktrees {
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
	switch {
	case s.Agent.Kind == AgentKindCodex:
		conn, cleanup, err = m.connectCodex(ctx, s)
	case s.Agent.Kind == AgentKindClaude:
		conn, cleanup, err = m.connectClaude(ctx, s)
	case IsExternalACP(s.Agent.Kind):
		var argv []string
		if argv, err = m.externalACPCommand(s.Agent); err == nil {
			conn, cleanup, err = m.connectACPAgent(ctx, s, argv)
		}
	default:
		// Backward-compat fallback: the global acp-command external agent.
		conn, cleanup, err = m.connectExternal(ctx, s)
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
	worktreePath := ""
	if s.usedWorktree {
		worktreePath = s.Worktree.Path
	}
	if err := m.store.UpdateSession(s.ID, StatusRunning, string(sess.SessionId), s.Worktree.Path, worktreePath, s.Worktree.Branch, "", nil); err != nil {
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

// sdkLogger returns a session-scoped logger for SDK connections that drops
// the SDK's routine INFO chatter (e.g. the "connection closed" notice emitted
// on normal io.Pipe teardown after every session) but keeps warnings/errors.
func (m *Manager) sdkLogger(sessionID string) *slog.Logger {
	return slog.New(&minLevelHandler{next: m.log.Handler(), min: slog.LevelWarn}).With("session", sessionID)
}

// minLevelHandler forwards only records at or above min.
type minLevelHandler struct {
	next slog.Handler
	min  slog.Level
}

func (h *minLevelHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return level >= h.min && h.next.Enabled(ctx, level)
}

func (h *minLevelHandler) Handle(ctx context.Context, r slog.Record) error {
	return h.next.Handle(ctx, r)
}

func (h *minLevelHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &minLevelHandler{next: h.next.WithAttrs(attrs), min: h.min}
}

func (h *minLevelHandler) WithGroup(name string) slog.Handler {
	return &minLevelHandler{next: h.next.WithGroup(name), min: h.min}
}

// connectClaude wires the in-process claude bridge over io.Pipe.
func (m *Manager) connectClaude(ctx context.Context, s *Session) (*acpsdk.ClientSideConnection, func(), error) {
	launch, err := agentLaunchArgv(s.Agent, m.resolveClaudeBin)
	if err != nil {
		return nil, nil, err
	}
	var extraArgs []string
	if s.Agent.Model != "" {
		extraArgs = append(extraArgs, "--model", s.Agent.Model)
	}
	extraArgs = append(extraArgs, s.Agent.Args...)
	env, drop := s.Agent.spawnEnv()
	bridge := claudebridge.New(claudebridge.Options{
		Launch:    launch,
		ExtraArgs: extraArgs,
		Env:       env,
		DropEnv:   drop,
		Logger:    m.log,
	})

	clientIn, agentOut := io.Pipe() // agent writes → client reads
	agentIn, clientOut := io.Pipe() // client writes → agent reads

	agentConn := acpsdk.NewAgentSideConnection(bridge, agentOut, agentIn)
	agentConn.SetLogger(m.sdkLogger(s.ID))
	bridge.SetConn(agentConn)
	clientConn := acpsdk.NewClientSideConnection(&sessionClient{m: m, s: s}, clientOut, clientIn)
	clientConn.SetLogger(m.sdkLogger(s.ID))

	cleanup := func() {
		bridge.KillAll(2 * time.Second)
		_ = clientOut.Close()
		_ = agentOut.Close()
	}
	return clientConn, cleanup, nil
}

// connectCodex wires the in-process codex bridge over io.Pipe. The codex CLI
// has no ACP mode, so the bridge drives `codex exec --json` and translates its
// event stream; per-agent env (CODEX_HOME/OPENAI_API_KEY) is injected at spawn.
func (m *Manager) connectCodex(ctx context.Context, s *Session) (*acpsdk.ClientSideConnection, func(), error) {
	launch, err := agentLaunchArgv(s.Agent, m.resolveCodexBin)
	if err != nil {
		return nil, nil, err
	}
	env, drop := s.Agent.spawnEnv()
	bridge := codexbridge.New(codexbridge.Options{
		Launch:    launch,
		Model:     s.Agent.Model,
		ExtraArgs: s.Agent.Args,
		Env:       env,
		DropEnv:   drop,
		Logger:    m.log,
	})

	clientIn, agentOut := io.Pipe() // agent writes → client reads
	agentIn, clientOut := io.Pipe() // client writes → agent reads

	agentConn := acpsdk.NewAgentSideConnection(bridge, agentOut, agentIn)
	agentConn.SetLogger(m.sdkLogger(s.ID))
	bridge.SetConn(agentConn)
	clientConn := acpsdk.NewClientSideConnection(&sessionClient{m: m, s: s}, clientOut, clientIn)
	clientConn.SetLogger(m.sdkLogger(s.ID))

	cleanup := func() {
		bridge.KillAll(2 * time.Second)
		_ = clientOut.Close()
		_ = agentOut.Close()
	}
	return clientConn, cleanup, nil
}

// connectExternal spawns the global acp-command external ACP agent (config
// agentMode "acp-command") — the empty-registry backward-compat fallback.
func (m *Manager) connectExternal(ctx context.Context, s *Session) (*acpsdk.ClientSideConnection, func(), error) {
	if len(m.cfg.AgentCommand) == 0 {
		return nil, nil, fmt.Errorf("agentMode is acp-command but agentCommand is empty")
	}
	return m.connectACPAgent(ctx, s, m.cfg.AgentCommand)
}

// externalACPCommand builds the argv for an ACP-native external agent
// (antigravity or the generic acp kind). Command overrides everything; kind
// "antigravity" defaults to `<bin> --acp`. Args are appended in both cases.
func (m *Manager) externalACPCommand(a AgentEntry) ([]string, error) {
	var argv []string
	switch {
	case len(a.Command) > 0:
		argv = append(argv, a.Command...)
	case a.Kind == AgentKindAntigravity:
		bin, err := lookupBin(firstNonEmpty(a.BinPath, "antigravity"), "antigravity binary not found (set binPath or command on the agent)")
		if err != nil {
			return nil, err
		}
		argv = append(argv, bin, "--acp")
		if a.Model != "" {
			argv = append(argv, "--model", a.Model)
		}
	default:
		return nil, fmt.Errorf("agent %q (kind %s) has no launch command", a.Name, a.Kind)
	}
	return append(argv, a.Args...), nil
}

// connectACPAgent spawns an ACP-native external agent (argv) over stdio with
// the agent's per-process env, and talks pure ACP to it — no bridge.
func (m *Manager) connectACPAgent(ctx context.Context, s *Session, argv []string) (*acpsdk.ClientSideConnection, func(), error) {
	if len(argv) == 0 {
		return nil, nil, fmt.Errorf("empty agent command")
	}
	env, drop := s.Agent.spawnEnv()
	argv = resolveArgv0(argv)
	proc, err := procgroup.Spawn(m.rootCtx, argv, s.Worktree.Path, env, drop...)
	if err != nil {
		return nil, nil, fmt.Errorf("spawn agent %q: %w", argv[0], err)
	}
	conn := acpsdk.NewClientSideConnection(&sessionClient{m: m, s: s}, proc.Stdin, proc.Stdout)
	conn.SetLogger(m.sdkLogger(s.ID))
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
	if s.usedWorktree {
		fmt.Fprintf(&b, "Worktree: `%s`\nВетка: `%s`\n", s.Worktree.Path, s.Worktree.Branch)
		fmt.Fprintf(&b, "Посмотреть дифф: `git -C %s diff %s`", s.Worktree.Path, s.Worktree.BaseRef)
	} else {
		fmt.Fprintf(&b, "Изменения не закоммичены и лежат в рабочей копии `%s`.\n", s.RepoPath)
		fmt.Fprintf(&b, "Посмотреть дифф: `git -C %s diff`", s.RepoPath)
	}
	return b.String()
}

func failComment(s *Session, reason string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "❌ Сессия агента завершилась с ошибкой: %s", truncateRunes(reason, 1500))
	if s.usedWorktree && s.Worktree.Path != "" {
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
