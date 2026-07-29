package acp

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/mattermost/focalboard/desktop/internal/acp/claudebridge"
	"github.com/mattermost/focalboard/desktop/internal/acp/codexbridge"
	"github.com/mattermost/focalboard/desktop/internal/procgroup"
)

// turnRequest is one user message queued onto a live session.
type turnRequest struct {
	text string
	done chan turnOutcome // buffered(1); receives the turn's outcome
}

// turnOutcome is what a turn produced: the agent's final message and the error
// that ended it, if any.
type turnOutcome struct {
	text string
	err  error
}

// Session is one agent conversation bound to a card. A session triggered by a
// card move runs a single turn and finishes, as it always has; a session a
// console is attached to stays alive between turns so the user can keep
// talking to the agent.
type Session struct {
	ID         string
	CardID     string
	BoardID    string
	RepoPath   string
	BaseBranch string
	PromptText string
	Agent      AgentEntry      // resolved agent (kind/bin/model/env/prompt)
	Net        NetworkSettings // resolved proxy configuration (Agent.ProxyName)

	Worktree     WorktreeInfo
	usedWorktree bool // a dedicated worktree was actually created

	// Planning is a session with no card behind it: it exists only to talk
	// through a task before one is created. It reads the repository but never
	// writes, so it neither takes the repo lock nor reports to a card.
	Planning bool
	// AutoAllow overrides the global autoAllowTools for this session; a
	// planning session narrows it to the read-only tools.
	AutoAllow []string
	// scratchDir is a throwaway working directory made for a session that has
	// no repository, removed when the session ends.
	scratchDir string

	mu          sync.Mutex
	status      SessionStatus
	turnCancel  context.CancelFunc // cancels the in-flight turn
	cancelSent  bool
	allowTools  map[string]bool
	interactive bool // opened as a console, or attached to while running
	attached    int  // consoles currently watching
	turnNo      int

	turns     chan turnRequest
	closeCh   chan struct{}
	closeOnce sync.Once

	finalMu  sync.Mutex
	finalBuf strings.Builder

	seq atomic.Int64
}

func (s *Session) Status() SessionStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status
}

// isInteractive reports whether the session should stay alive between turns.
func (s *Session) isInteractive() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.interactive
}

// attach marks a console as watching; the session then survives its turns.
// Every attach must be paired with a detach, or the session would outlive the
// consoles and go on holding its repository.
func (s *Session) attach() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.attached++
	s.interactive = true
}

// markInteractive records that the session is being driven by a user without
// claiming a console slot — talking to a session already implies one is open.
func (s *Session) markInteractive() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.interactive = true
}

// detach drops a console. The last console leaving an idle session ends it:
// an unattended session would otherwise hold its repository lock forever.
func (s *Session) detach() (lastLeft bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.attached > 0 {
		s.attached--
	}
	return s.attached == 0
}

// requestClose ends the turn loop after the current turn.
func (s *Session) requestClose() {
	s.closeOnce.Do(func() { close(s.closeCh) })
}

// hasConsole reports whether a human is watching and could answer a prompt.
// Unattended sessions must never block on one.
func (s *Session) hasConsole() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.attached > 0
}

// allowToolAlways remembers a tool the user approved for the rest of the session.
func (s *Session) allowToolAlways(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.allowTools[name] = true
}

func (s *Session) toolAllowed(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.allowTools[name]
}

// autoAllowed reports whether the tool runs without asking. A session may carry
// its own list — a planning session is held to read-only tools whatever the
// global policy permits.
func (s *Session) autoAllowed(name string, cfg Config) bool {
	if s.AutoAllow == nil {
		return cfg.ToolAllowed(name)
	}
	for _, t := range s.AutoAllow {
		if strings.EqualFold(t, name) {
			return true
		}
	}
	return false
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

	if m.rootCtx.Err() != nil {
		m.finishSession(s, StatusCancelled, "приложение завершается")
		return
	}

	// 1. Working directory: a dedicated worktree, or the repo itself.
	if err := m.prepareWorkdir(s); err != nil {
		m.finishSession(s, StatusFailed, err.Error())
		m.comment(s, failComment(s, err.Error()))
		return
	}

	// 2. Agent connection, held for the whole session so turns share context.
	conn, acpSessionID, cleanup, err := m.openConnection(m.rootCtx, s)
	if err != nil {
		m.finishSession(s, StatusFailed, err.Error())
		m.comment(s, failComment(s, err.Error()))
		m.cleanupWorktree(s)
		return
	}
	defer cleanup()

	// 3. Turns, starting with the card task.
	m.turnLoop(s, conn, acpSessionID)

	// 4. Worktree cleanup for unsuccessful sessions.
	m.cleanupWorktree(s)
}

// prepareWorkdir sets up the session's working directory and announces it.
func (m *Manager) prepareWorkdir(s *Session) error {
	// A planning session only reads, so it runs in the repository itself even
	// under worktreeMode "always": a worktree would cost a checkout and leave
	// a branch behind for a conversation that changes nothing.
	if m.cfg.UseWorktrees() && !s.Planning {
		wt, err := CreateWorktree(m.rootCtx, s.RepoPath, s.BaseBranch, s.CardID, s.ID, m.cfg.WorktreeDir)
		if err != nil {
			return fmt.Errorf("не удалось создать git worktree: %w", err)
		}
		s.Worktree = wt
		s.usedWorktree = true
		if err := m.store.UpdateSession(s.ID, StatusRunning, "", wt.Path, wt.Path, wt.Branch, "", nil); err != nil {
			m.log.Warn("acp: failed to persist worktree info", "session", s.ID, "err", err)
		}
		m.comment(s, fmt.Sprintf("🤖 Агент запущен.\nWorktree: `%s`\nВетка: `%s` (от `%s`)", wt.Path, wt.Branch, wt.BaseRef))
		return nil
	}
	s.Worktree = WorktreeInfo{Path: s.RepoPath, BaseRef: "HEAD"}
	if err := m.store.UpdateSession(s.ID, StatusRunning, "", s.RepoPath, "", "", "", nil); err != nil {
		m.log.Warn("acp: failed to persist session cwd", "session", s.ID, "err", err)
	}
	m.comment(s, fmt.Sprintf("🤖 Агент запущен прямо в репозитории `%s`.", s.RepoPath))
	return nil
}

func (m *Manager) cleanupWorktree(s *Session) {
	if s.scratchDir != "" {
		if err := os.RemoveAll(s.scratchDir); err != nil {
			m.log.Warn("acp: failed to remove planning scratch dir", "session", s.ID, "err", err)
		}
	}
	if !s.usedWorktree || s.Status() == StatusDone || m.cfg.KeepFailedWorktrees {
		return
	}
	if removed, err := RemoveWorktreeIfClean(context.Background(), s.RepoPath, s.Worktree); err != nil {
		m.log.Warn("acp: worktree cleanup failed", "session", s.ID, "err", err)
	} else if removed {
		s.Worktree = WorktreeInfo{}
	}
}

// turnLoop runs the card task, then — for a session a console is attached to —
// every follow-up message the user sends, until the console closes, the session
// goes idle for too long, or the app shuts down.
func (m *Manager) turnLoop(s *Session, conn *acpsdk.ClientSideConnection, acpSessionID acpsdk.SessionId) {
	req := turnRequest{text: s.PromptText}
	for {
		finalText, err := m.runTurn(s, conn, acpSessionID, req.text)
		if req.done != nil {
			req.done <- turnOutcome{text: finalText, err: err}
		}

		s.mu.Lock()
		turn := s.turnNo
		s.mu.Unlock()

		// The first turn always reports to the card: that is the whole point of
		// a session triggered by a card move. Later turns belong to the console.
		if turn == 1 {
			m.commentFirstTurn(s, finalText, err)
		}

		switch {
		case m.rootCtx.Err() != nil:
			m.finishSession(s, StatusCancelled, "приложение завершается")
			return
		case err != nil && !s.isInteractive():
			return // commentFirstTurn already recorded the terminal status
		case err != nil && connectionLost(conn):
			m.finishSession(s, StatusFailed, err.Error())
			m.comment(s, failComment(s, err.Error()))
			return
		case !s.isInteractive():
			m.finishSession(s, StatusDone, "")
			return
		case err != nil:
			// A failed follow-up turn keeps the session usable, but the console
			// has to say so — nothing else surfaces this error.
			s.appendEvent(m, "error", map[string]any{"text": err.Error()})
			m.emitSession(s, err.Error())
		}

		next, ok := m.waitForTurn(s)
		if !ok {
			return
		}
		req = next
	}
}

// commentFirstTurn records the outcome of the card-triggered turn and, for a
// one-shot session, the terminal status that goes with it.
func (m *Manager) commentFirstTurn(s *Session, finalText string, err error) {
	interactive := s.isInteractive()
	switch {
	case m.rootCtx.Err() != nil:
		// Shutdown: turnLoop reports it.
	case s.wasCancelled():
		// A cancelled turn ends with StopReason "cancelled", not an error.
		if !interactive {
			m.finishSession(s, StatusCancelled, "сессия отменена")
		}
		m.comment(s, cancelComment(s))
	case err != nil:
		if !interactive {
			m.finishSession(s, StatusFailed, err.Error())
		}
		m.comment(s, failComment(s, err.Error()))
	default:
		if !interactive {
			m.finishSession(s, StatusDone, "")
		}
		m.comment(s, doneComment(s, finalText))
	}
}

// waitForTurn parks an interactive session between turns. It reports false when
// the session should end.
func (m *Manager) waitForTurn(s *Session) (turnRequest, bool) {
	// Parking is only justified while a console is watching. A card closed
	// mid-turn detaches without being able to end the session — it was not idle
	// yet — so the check belongs here, or the session would sit on its
	// repository until the idle timeout with nobody looking at it.
	if !s.hasConsole() {
		m.finishSession(s, StatusDone, "")
		return turnRequest{}, false
	}

	s.mu.Lock()
	s.status = StatusIdle
	s.mu.Unlock()
	m.persistStatus(s, StatusIdle, "")

	idle := time.NewTimer(m.cfg.SessionIdle())
	defer idle.Stop()

	select {
	case req := <-s.turns:
		return req, true
	case <-s.closeCh:
		m.finishSession(s, StatusDone, "")
		m.comment(s, closeComment(s))
		return turnRequest{}, false
	case <-idle.C:
		m.finishSession(s, StatusDone, "")
		m.comment(s, idleComment(s, m.cfg.SessionIdle()))
		return turnRequest{}, false
	case <-m.rootCtx.Done():
		m.finishSession(s, StatusCancelled, "приложение завершается")
		return turnRequest{}, false
	}
}

// connectionLost reports whether the agent connection is gone, which makes
// every further turn pointless.
func connectionLost(conn *acpsdk.ClientSideConnection) bool {
	select {
	case <-conn.Done():
		return true
	default:
		return false
	}
}

// openConnection builds the ACP stack for a session and negotiates the agent
// session. The connection is held for the session's whole life, so every turn
// runs against the same agent process and keeps the conversation.
func (m *Manager) openConnection(ctx context.Context, s *Session) (*acpsdk.ClientSideConnection, acpsdk.SessionId, func(), error) {
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
		return nil, "", nil, err
	}

	if _, err := conn.Initialize(ctx, acpsdk.InitializeRequest{
		ProtocolVersion: acpsdk.ProtocolVersionNumber,
		ClientCapabilities: acpsdk.ClientCapabilities{
			Fs: acpsdk.FileSystemCapabilities{ReadTextFile: true, WriteTextFile: true},
		},
	}); err != nil {
		cleanup()
		return nil, "", nil, fmt.Errorf("initialize: %w", err)
	}

	sess, err := conn.NewSession(ctx, acpsdk.NewSessionRequest{Cwd: s.Worktree.Path, McpServers: []acpsdk.McpServer{}})
	if err != nil {
		cleanup()
		return nil, "", nil, fmt.Errorf("session/new: %w", err)
	}
	worktreePath := ""
	if s.usedWorktree {
		worktreePath = s.Worktree.Path
	}
	if err := m.store.UpdateSession(s.ID, StatusRunning, string(sess.SessionId), s.Worktree.Path, worktreePath, s.Worktree.Branch, "", nil); err != nil {
		m.log.Warn("acp: failed to persist acp session id", "session", s.ID, "err", err)
	}
	return conn, sess.SessionId, cleanup, nil
}

// runTurn sends one prompt and returns the agent's final message text. It holds
// a concurrency slot only while the agent is actually working, so an idle
// console never starves other cards.
func (m *Manager) runTurn(s *Session, conn *acpsdk.ClientSideConnection, acpSessionID acpsdk.SessionId, prompt string) (string, error) {
	select {
	case m.sem <- struct{}{}:
		defer func() { <-m.sem }()
	case <-m.rootCtx.Done():
		return "", m.rootCtx.Err()
	}

	ctx, cancel := context.WithTimeout(m.rootCtx, m.cfg.SessionTimeout())
	defer cancel()

	s.mu.Lock()
	s.turnCancel = cancel
	s.cancelSent = false
	s.status = StatusRunning
	s.turnNo++
	s.mu.Unlock()
	m.persistStatus(s, StatusRunning, "")

	// Each turn reports only its own output.
	s.finalMu.Lock()
	s.finalBuf.Reset()
	s.finalMu.Unlock()

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
		Prompt:    []acpsdk.ContentBlock{acpsdk.TextBlock(prompt)},
	})
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded && !s.wasCancelled() {
			return "", fmt.Errorf("таймаут хода (%s)", m.cfg.SessionTimeout())
		}
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
	env, drop := spawnEnv(s.Agent, s.Net)
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
	env, drop := spawnEnv(s.Agent, s.Net)
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
	env, drop := spawnEnv(s.Agent, s.Net)
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
	reason = s.Net.redactProxySecret(reason)
	var b strings.Builder
	fmt.Fprintf(&b, "❌ Сессия агента завершилась с ошибкой: %s", truncateRunes(reason, 1500))
	// 407 arrives as a bare status code from the CLI, with no hint that the
	// proxy — not the model API — refused the request.
	if s.Net.Proxy != "" && strings.Contains(reason, "407") {
		b.WriteString("\n\nПрокси требует аутентификацию (407): задай логин и пароль в конфигурации прокси (меню доски → Proxy configurations).")
	}
	if s.usedWorktree && s.Worktree.Path != "" {
		fmt.Fprintf(&b, "\nWorktree (если остался): `%s`", s.Worktree.Path)
	}
	return b.String()
}

func cancelComment(s *Session) string {
	return "🛑 Сессия агента отменена."
}

// closeComment closes out an interactive session. Turns after the first are not
// commented one by one, so this records how long the conversation ran.
func closeComment(s *Session) string {
	s.mu.Lock()
	turns := s.turnNo
	s.mu.Unlock()
	if turns <= 1 {
		return "💬 Интерактивная сессия закрыта."
	}
	return fmt.Sprintf("💬 Интерактивная сессия закрыта, ходов: %d.", turns)
}

func idleComment(s *Session, idle time.Duration) string {
	return fmt.Sprintf("💤 Сессия закрыта: без сообщений дольше %s.", idle)
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
