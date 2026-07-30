package acp

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/mattermost/focalboard/desktop/internal/acp/claudebridge"
	"github.com/mattermost/focalboard/desktop/internal/dokku"
)

// Manager owns all agent sessions: it consumes board events, enforces limits
// and policies, and reports results back to the board and the UI.
type Manager struct {
	cfg     Config
	cfgMu   sync.RWMutex // guards the UI-mutable parts of cfg (Repos, Agents, SystemPrompt)
	cfgPath string       // where registry edits are persisted; empty in tests
	store   *Store
	writer  BoardWriter
	reader  BoardReader // optional; enables opening a console on a card
	users   BoardUsers  // optional; enables assigning cards to an agent
	ui      UIEmitter
	log     *slog.Logger
	tr      *Tracer

	mu     sync.Mutex
	active map[string]*Session // session ID → session
	byCard map[string]*Session // card ID → live (non-terminal) session

	permMu sync.Mutex
	perms  map[string]pendingPermission // request ID → prompt awaiting a human

	sem     chan struct{}
	rootCtx context.Context
	stop    context.CancelFunc
	wg      sync.WaitGroup
}

// pendingPermission is one permission prompt waiting for a human decision.
type pendingPermission struct {
	sessionID string
	answer    chan string // receives the chosen option id
}

// SetBoardReader supplies on-demand card reads, which the "open a console on
// this card" path needs. Optional: without it, sessions start only on a move.
func (m *Manager) SetBoardReader(r BoardReader) { m.reader = r }

// SetBoardUsers supplies account provisioning, which "assign a card to an
// agent" needs. Optional: without it only the "Agent" field routes cards.
func (m *Manager) SetBoardUsers(u BoardUsers) { m.users = u }

// NewManager wires the manager. cfgPath is where repo-registry edits are
// persisted (may be empty in tests). Call Start to begin consuming events.
func NewManager(cfg Config, cfgPath string, st *Store, w BoardWriter, ui UIEmitter, log *slog.Logger) *Manager {
	if log == nil {
		log = slog.Default()
	}
	maxConc := cfg.MaxConcurrent
	if maxConc <= 0 {
		maxConc = 1
	}
	tr := newTracer(cfg, log)
	if tr.Enabled() {
		ui = &tracingEmitter{inner: ui, tr: tr}
	}
	return &Manager{
		cfg:     cfg,
		cfgPath: cfgPath,
		store:   st,
		writer:  w,
		ui:      ui,
		log:     log,
		tr:      tr,
		active:  make(map[string]*Session),
		byCard:  make(map[string]*Session),
		perms:   make(map[string]pendingPermission),
		sem:     make(chan struct{}, maxConc),
	}
}

// Start recovers interrupted sessions and launches the trigger loop.
func (m *Manager) Start(ctx context.Context, events BoardEvents) error {
	m.rootCtx, m.stop = context.WithCancel(ctx)

	// Probe the built-in claude binary only when the empty-registry fallback
	// would use it; registered agents resolve their own binaries at run time.
	if len(m.cfg.Agents) == 0 && m.cfg.AgentMode != "acp-command" {
		if _, err := m.findClaude(); err != nil {
			m.log.Warn("acp: claude binary not found; sessions will fail until claudePath is configured", "err", err)
		}
	}

	m.recover()
	PruneStale(m.rootCtx, m.cfg.RepoWhitelist)

	ch, err := events.Subscribe(m.rootCtx)
	if err != nil {
		return fmt.Errorf("subscribe to board events: %w", err)
	}
	m.wg.Add(1)
	go m.triggerLoop(ch)
	return nil
}

// recover marks sessions left non-terminal by a previous run as failed.
func (m *Manager) recover() {
	stale, err := m.store.StaleSessions()
	if err != nil {
		m.log.Error("acp: recovery query failed", "err", err)
		return
	}
	for _, r := range stale {
		if err := m.store.SetSessionStatus(r.ID, StatusFailed, "прервано перезапуском приложения"); err != nil {
			m.log.Warn("acp: recovery update failed", "session", r.ID, "err", err)
			continue
		}
		m.commentCard(r.CardID, "Сессия агента была прервана перезапуском приложения.")
	}
}

// startOptions are the ways a session can differ from a plain card task.
type startOptions struct {
	// interactive keeps the session alive between turns (a console is open).
	interactive bool
	// deploy makes this a deploy session: it resolves a Dokku target, is given
	// the dokku MCP tools and gets the deploy prompt instead of the card task.
	deploy bool
	// repoName picks a repository explicitly, for a console opened on a card
	// that does not say which one it is about.
	repoName string
}

// StartSessionForEvent creates and launches a session for a validated trigger
// event. Callers must have passed idempotency/liveness checks.
func (m *Manager) StartSessionForEvent(ev CardMoved) (*Session, error) {
	return m.startSession(ev, startOptions{})
}

// StartDeploySessionForEvent launches the session behind the deploy column: the
// same machinery as a card task, pointed at publishing the card's branch.
func (m *Manager) StartDeploySessionForEvent(ev CardMoved) (*Session, error) {
	return m.startSession(ev, startOptions{deploy: true})
}

// startSession is the shared launch path. An interactive session survives its
// turns and waits for the user; a triggered one runs the card task and ends.
func (m *Manager) startSession(ev CardMoved, opts startOptions) (*Session, error) {
	repoPath, err := m.resolveRepo(ev)
	if opts.repoName != "" {
		// An explicit choice wins: the console offers one exactly when the card
		// itself does not say which repository it is about.
		repoPath, err = m.resolveNamedRepo(opts.repoName)
	}
	if err != nil {
		return nil, err
	}
	deploy, deployBranch, err := m.resolveDeploy(ev, repoPath, opts.deploy)
	if err != nil {
		return nil, err
	}
	agent, err := m.resolveAgent(ev)
	if err != nil {
		return nil, err
	}
	net, err := m.resolveNetwork(agent)
	if err != nil {
		return nil, err
	}
	// Without worktrees, two agents must never share one working tree
	// (spec §7): reject while another live session uses the same repo. A deploy
	// session is exempt for the same reason a planning one is — it only pushes
	// an existing branch and never touches the checkout.
	if !m.cfg.UseWorktrees() && !opts.deploy {
		m.mu.Lock()
		var busyCard string
		for _, other := range m.active {
			// A planning session only reads, so it neither claims the working
			// copy nor keeps a card's session out of it.
			if other.RepoPath == repoPath && !other.Planning {
				busyCard = other.CardID
				break
			}
		}
		m.mu.Unlock()
		if busyCard != "" {
			return nil, fmt.Errorf("в репозитории %s уже работает сессия другой карточки (%s) — дождитесь её завершения или закройте её консоль", repoPath, busyCard)
		}
	}

	m.cfgMu.RLock()
	systemPrompt := m.cfg.SystemPrompt
	m.cfgMu.RUnlock()
	prompt := composePrompt(ev, agent, systemPrompt, m.cfg.UseWorktrees())
	if deploy != nil {
		m.cfgMu.RLock()
		deployPrompt := m.cfg.DeployPrompt
		m.cfgMu.RUnlock()
		prompt = composeDeployPrompt(ev, agent, systemPrompt, deployPrompt, *deploy, deployBranch)
	}
	s := &Session{
		ID:           uuid.NewString(),
		CardID:       ev.CardID,
		Title:        ev.Title,
		BoardID:      ev.BoardID,
		RepoPath:     repoPath,
		BaseBranch:   ev.Props["branch"],
		Agent:        agent,
		Net:          net,
		Deploy:       deploy,
		DeployBranch: deployBranch,
		PromptText:   prompt,
		Policy:       agentPolicy(agent),
		status:       StatusQueued,
		allowTools:   make(map[string]bool),
		interactive:  opts.interactive,
		turns:        make(chan turnRequest, 1),
		closeCh:      make(chan struct{}),
	}
	if opts.interactive {
		s.attached = 1
	}
	rec := SessionRecord{
		ID:        s.ID,
		CardID:    s.CardID,
		BoardID:   s.BoardID,
		AgentKind: agent.Kind,
		Status:    StatusQueued,
		StartedAt: time.Now(),
	}
	if err := m.store.InsertSession(rec); err != nil {
		return nil, fmt.Errorf("persist session: %w", err)
	}

	m.mu.Lock()
	m.active[s.ID] = s
	// byCard is the card's *own* session — the one its console talks to and the
	// one leaving the column cancels. A deploy started from the card while that
	// session is alive must not take its place.
	if live := m.byCard[s.CardID]; live == nil || !opts.deploy {
		m.byCard[s.CardID] = s
	}
	m.mu.Unlock()
	m.emitSession(s, "")

	m.wg.Add(1)
	go m.runSession(s)
	return s, nil
}

// CancelSessionForCard cancels the live session of a card, if any. An
// interactive session goes back to idle and keeps its conversation; a
// card-triggered one ends.
func (m *Manager) CancelSessionForCard(cardID, reason string) bool {
	m.mu.Lock()
	s := m.byCard[cardID]
	m.mu.Unlock()
	if s == nil {
		return false
	}
	m.log.Info("acp: cancelling session", "session", s.ID, "card", cardID, "reason", reason)
	s.mu.Lock()
	s.cancelSent = true
	cancel := s.turnCancel
	running := s.status == StatusRunning || s.status == StatusWaitingPermission
	s.mu.Unlock()
	if cancel != nil && running {
		cancel()
	} else if !s.isInteractive() {
		// Queued, or idle and nobody is talking to it: end it outright.
		m.finishSession(s, StatusCancelled, reason)
		s.requestClose()
	}
	return true
}

// StartSessionForCard opens an interactive session on a card without moving it
// into the trigger column — the "open a console" path from the UI.
func (m *Manager) StartSessionForCard(cardID, repoName string) (*Session, error) {
	if m.reader == nil {
		return nil, fmt.Errorf("чтение карточек недоступно")
	}
	m.mu.Lock()
	live := m.byCard[cardID]
	m.mu.Unlock()
	if live != nil {
		live.attach()
		m.emitSession(live, "")
		return live, nil
	}

	ctx, cancel := context.WithTimeout(m.rootCtx, 10*time.Second)
	defer cancel()
	ev, err := m.reader.CardByID(ctx, cardID)
	if err != nil {
		return nil, fmt.Errorf("не удалось прочитать карточку: %w", err)
	}
	s, err := m.startSession(ev, startOptions{interactive: true, repoName: repoName})
	if err != nil {
		return nil, err
	}
	return s, nil
}

// StartDeployForCard publishes a card's branch without moving the card into the
// deploy column — the "Deploy" button next to the branch the card is working on.
// branch overrides the card's own "branch" property, which is how the session's
// worktree branch (the one the agent is actually committing to) gets deployed;
// empty falls back to the card property and then to the checked-out branch.
//
// The branch lives in the repository's shared object store even when it was
// created in a worktree, so pushing it from the repository itself — which is
// where a deploy session always runs — reaches it.
func (m *Manager) StartDeployForCard(cardID, branch string) (*Session, error) {
	if m.reader == nil {
		return nil, fmt.Errorf("чтение карточек недоступно")
	}
	ctx, cancel := context.WithTimeout(m.rootCtx, 10*time.Second)
	defer cancel()
	ev, err := m.reader.CardByID(ctx, cardID)
	if err != nil {
		return nil, fmt.Errorf("не удалось прочитать карточку: %w", err)
	}
	if b := strings.TrimSpace(branch); b != "" {
		if ev.Props == nil {
			ev.Props = map[string]string{}
		}
		ev.Props["branch"] = b
	}
	return m.startSession(ev, startOptions{deploy: true})
}

// StartPlanningSession opens a card-less session for talking a task through
// before it exists. An empty repoName plans without one — useful when the task
// is not about existing code yet; agentName may be empty when the agent
// registry holds exactly one entry.
func (m *Manager) StartPlanningSession(repoName, agentName string) (*Session, error) {
	repo, err := m.planningRepo(repoName)
	if err != nil {
		return nil, err
	}
	agent, err := m.planningAgent(agentName)
	if err != nil {
		return nil, err
	}
	// Without a repository the agent still needs somewhere to run; a scratch
	// directory keeps it away from anything of the user's.
	scratch := ""
	if repo.Path == "" {
		if scratch, err = os.MkdirTemp("", "focalboard-planning-*"); err != nil {
			return nil, fmt.Errorf("не удалось создать каталог для сессии: %w", err)
		}
	}
	net, err := m.resolveNetwork(agent)
	if err != nil {
		return nil, err
	}

	m.cfgMu.RLock()
	systemPrompt := m.cfg.SystemPrompt
	m.cfgMu.RUnlock()

	s := &Session{
		ID:          uuid.NewString(),
		RepoPath:    firstNonEmpty(repo.Path, scratch),
		Agent:       agent,
		Net:         net,
		PromptText:  planningPrompt(systemPrompt, agent, repo),
		Planning:    true,
		scratchDir:  scratch,
		Policy:      m.cfg.PlanningPolicy(),
		status:      StatusQueued,
		allowTools:  make(map[string]bool),
		interactive: true,
		attached:    1,
		turns:       make(chan turnRequest, 1),
		closeCh:     make(chan struct{}),
	}
	rec := SessionRecord{
		ID:        s.ID,
		AgentKind: agent.Kind,
		Status:    StatusQueued,
		StartedAt: time.Now(),
	}
	if err := m.store.InsertSession(rec); err != nil {
		return nil, fmt.Errorf("persist session: %w", err)
	}

	m.mu.Lock()
	m.active[s.ID] = s
	m.mu.Unlock()
	m.emitSession(s, "")
	m.log.Info("acp: planning session started", "session", s.ID, "repo", repo.Path, "agent", agent.Name)

	m.wg.Add(1)
	go m.runSession(s)
	return s, nil
}

// planningRepo picks the registry entry to plan against. An empty name means
// planning without a repository, which is a valid choice rather than a default:
// the dialog preselects a lone entry, so nothing here has to guess.
func (m *Manager) planningRepo(name string) (RepoEntry, error) {
	if name == "" {
		return RepoEntry{}, nil
	}
	for _, r := range m.Repos() {
		if strings.EqualFold(r.Name, name) {
			return r, nil
		}
	}
	return RepoEntry{}, fmt.Errorf("репозиторий %q не найден в реестре", name)
}

// planningAgent picks the registry entry that will do the planning.
func (m *Manager) planningAgent(name string) (AgentEntry, error) {
	agents := m.Agents()
	if len(agents) == 0 {
		return AgentEntry{}, fmt.Errorf("не зарегистрировано ни одного агента (меню доски → Agents)")
	}
	if name == "" {
		if len(agents) > 1 {
			return AgentEntry{}, fmt.Errorf("укажи агента: зарегистрировано несколько")
		}
		return agents[0], nil
	}
	for _, a := range agents {
		if strings.EqualFold(a.Name, name) {
			return a, nil
		}
	}
	return AgentEntry{}, fmt.Errorf("агент %q не найден в реестре", name)
}

// composeTaskPrompt asks for the conversation to be boiled down to a card. The
// shape is fixed because the UI splits the answer on the first line.
const composeTaskPrompt = `Оформи то, о чём мы договорились, как задачу для трекера.
Ответь ровно в таком виде, без markdown-заголовков и без вступления:
первая строка — краткий заголовок задачи (до 80 символов),
далее с новой строки — описание: что нужно сделать, где в коде и как проверить.`

// ComposeTask runs one more turn asking the agent to boil the conversation down
// to a task, and returns its answer: first line the title, the rest the body.
func (m *Manager) ComposeTask(sessionID string) (string, error) {
	s := m.session(sessionID)
	if s == nil {
		return "", fmt.Errorf("сессия %s не активна", sessionID)
	}
	req := turnRequest{text: composeTaskPrompt, done: make(chan turnOutcome, 1)}
	select {
	case s.turns <- req:
	case <-s.closeCh:
		return "", fmt.Errorf("сессия закрывается")
	default:
		return "", fmt.Errorf("агент ещё занят предыдущим сообщением")
	}

	s.appendEvent(m, "prompt", map[string]any{"text": composeTaskPrompt})
	m.ui.Emit(EventPrompt, map[string]any{
		"sessionId": s.ID, "cardId": s.CardID, "text": composeTaskPrompt,
	})

	select {
	case out := <-req.done:
		if out.err != nil {
			return "", out.err
		}
		if strings.TrimSpace(out.text) == "" {
			return "", fmt.Errorf("агент вернул пустой ответ")
		}
		return out.text, nil
	case <-m.rootCtx.Done():
		return "", fmt.Errorf("приложение завершается")
	}
}

// PromptSession queues a follow-up message onto a live session.
func (m *Manager) PromptSession(sessionID, text string) error {
	m.tr.Event(sessionID, TraceFromUI, "PromptSession", map[string]any{"text": text})
	if strings.TrimSpace(text) == "" {
		return fmt.Errorf("пустое сообщение")
	}
	s := m.session(sessionID)
	if s == nil {
		return fmt.Errorf("сессия %s не активна", sessionID)
	}
	// Typing into a session is what makes it interactive. It must not claim a
	// console slot: the console attached when it opened, and an unpaired
	// increment here would keep the session alive after that console left.
	s.markInteractive()
	s.appendEvent(m, "prompt", map[string]any{"text": text})
	m.ui.Emit(EventPrompt, map[string]any{
		"sessionId": s.ID, "cardId": s.CardID, "text": text,
	})

	// The queue holds one message, so typing while the agent is still working
	// is fine: the turn loop picks it up as soon as it goes idle.
	req := turnRequest{text: text, done: make(chan turnOutcome, 1)}
	select {
	case s.turns <- req:
		return nil
	case <-s.closeCh:
		return fmt.Errorf("сессия закрывается")
	default:
		return fmt.Errorf("предыдущее сообщение ещё не взято в работу")
	}
}

// AttachSession marks a console as watching the session, which keeps it alive
// between turns instead of finishing after the card task.
func (m *Manager) AttachSession(sessionID string) bool {
	m.tr.Event(sessionID, TraceFromUI, "AttachSession", nil)
	s := m.session(sessionID)
	if s == nil {
		m.log.Info("acp: console asked to attach to a session that is no longer live", "session", sessionID)
		return false
	}
	s.attach()
	m.log.Info("acp: console attached", "session", s.ID, "card", s.CardID)
	m.emitSession(s, "")
	return true
}

// DetachSession drops a console. The last one leaving ends an idle session so
// it stops holding its repository.
func (m *Manager) DetachSession(sessionID string) {
	m.tr.Event(sessionID, TraceFromUI, "DetachSession", nil)
	s := m.session(sessionID)
	if s == nil {
		return
	}
	if s.detach() && s.Status() == StatusIdle {
		s.requestClose()
	}
}

// CloseSession ends a session after its current turn.
func (m *Manager) CloseSession(sessionID string) error {
	m.tr.Event(sessionID, TraceFromUI, "CloseSession", nil)
	s := m.session(sessionID)
	if s == nil {
		return fmt.Errorf("сессия %s не активна", sessionID)
	}
	s.requestClose()
	return nil
}

// session looks up a live session by id.
func (m *Manager) session(sessionID string) *Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.active[sessionID]
}

// ---- permission prompts ----

// registerPermission opens a slot for a human decision and returns the channel
// the answer arrives on.
func (m *Manager) registerPermission(requestID, sessionID string) chan string {
	ch := make(chan string, 1)
	m.permMu.Lock()
	m.perms[requestID] = pendingPermission{sessionID: sessionID, answer: ch}
	m.permMu.Unlock()
	return ch
}

func (m *Manager) forgetPermission(requestID string) {
	m.permMu.Lock()
	delete(m.perms, requestID)
	m.permMu.Unlock()
}

// askUser shows the agent's questions on the console and waits for answers. It
// fails rather than blocks when nobody is watching: an unattended session must
// not sit on a question for the whole permission timeout.
func (m *Manager) askUser(ctx context.Context, s *Session, questions []claudebridge.Question) (string, error) {
	if !s.hasConsole() {
		m.tr.Event(s.ID, TraceApp, "question_refused", map[string]any{"reason": "no console attached"})
		return "", fmt.Errorf("консоль не открыта")
	}
	requestID := uuid.NewString()
	answer := m.registerPermission(requestID, s.ID)
	defer m.forgetPermission(requestID)

	payload := map[string]any{
		"sessionId": s.ID,
		"cardId":    s.CardID,
		"requestId": requestID,
		"questions": questions,
	}
	s.appendEvent(m, "question", payload)
	m.setStatus(s, StatusWaitingPermission)
	m.ui.Emit(EventQuestion, payload)
	defer m.setStatus(s, StatusRunning)

	timeout := time.NewTimer(m.cfg.PermissionTimeout())
	defer timeout.Stop()

	select {
	case answers := <-answer:
		s.appendEvent(m, "answer", map[string]any{"requestId": requestID, "text": answers})
		m.ui.Emit(EventAnswer, map[string]any{
			"sessionId": s.ID, "cardId": s.CardID, "requestId": requestID, "text": answers,
		})
		return answers, nil
	case <-timeout.C:
		m.tr.Event(s.ID, TraceApp, "question_timeout", map[string]any{"requestId": requestID, "after": m.cfg.PermissionTimeout().String()})
		return "", fmt.Errorf("пользователь не ответил за %s", m.cfg.PermissionTimeout())
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// AnswerQuestion delivers the user's answers to a pending question. The text is
// what the model will read, so the UI composes it from the picker.
func (m *Manager) AnswerQuestion(sessionID, requestID, answers string) error {
	m.tr.Event(sessionID, TraceFromUI, "AnswerQuestion", map[string]any{"requestId": requestID, "text": answers})
	if strings.TrimSpace(answers) == "" {
		return fmt.Errorf("пустой ответ")
	}
	return m.AnswerPermission(sessionID, requestID, answers)
}

// AnswerPermission delivers the user's choice for a pending permission prompt.
func (m *Manager) AnswerPermission(sessionID, requestID, optionID string) error {
	m.tr.Event(sessionID, TraceFromUI, "AnswerPermission", map[string]any{"requestId": requestID, "optionId": optionID})
	m.permMu.Lock()
	p, ok := m.perms[requestID]
	m.permMu.Unlock()
	if !ok || p.sessionID != sessionID {
		// The common shape of "I answered and nothing happened": the prompt had
		// already been resolved, most often by the permission timeout.
		m.tr.Event(sessionID, TraceApp, "answer_unmatched", map[string]any{"requestId": requestID})
		return fmt.Errorf("запрос разрешения %s больше не ждёт ответа", requestID)
	}
	select {
	case p.answer <- optionID:
		return nil
	default:
		return fmt.Errorf("на запрос разрешения %s уже ответили", requestID)
	}
}

// CardSessions returns persisted sessions and events for a card (UI hydration).
func (m *Manager) CardSessions(cardID string) ([]SessionRecord, []SessionEventRecord, error) {
	return m.store.SessionsForCard(cardID)
}

// Shutdown cancels everything and kills agent processes within grace.
func (m *Manager) Shutdown(grace time.Duration) {
	if m.stop != nil {
		m.stop()
	}
	done := make(chan struct{})
	go func() {
		m.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(grace):
		m.log.Warn("acp: shutdown grace expired with sessions still winding down")
	}
	if m.store != nil {
		_ = m.store.Close()
	}
	m.tr.Close()
}

// ---- internals ----

// finishSession transitions to a terminal status exactly once.
func (m *Manager) finishSession(s *Session, status SessionStatus, errText string) {
	// A CLI failing to reach its proxy may echo the proxy URL back at us.
	errText = s.Net.redactProxySecret(errText)
	s.mu.Lock()
	if s.status.Terminal() {
		s.mu.Unlock()
		return
	}
	s.status = status
	s.mu.Unlock()
	m.persistStatus(s, status, errText)
}

func (m *Manager) releaseSession(s *Session) {
	m.mu.Lock()
	delete(m.active, s.ID)
	if m.byCard[s.CardID] == s {
		delete(m.byCard, s.CardID)
	}
	m.mu.Unlock()
}

// setStatus moves a live (non-terminal) session between running states, e.g.
// in and out of a permission prompt. Terminal sessions are left alone.
func (m *Manager) setStatus(s *Session, status SessionStatus) {
	s.mu.Lock()
	if s.status.Terminal() {
		s.mu.Unlock()
		return
	}
	s.status = status
	s.mu.Unlock()
	m.persistStatus(s, status, "")
}

func (m *Manager) persistStatus(s *Session, status SessionStatus, errText string) {
	if err := m.store.SetSessionStatus(s.ID, status, errText); err != nil {
		m.log.Warn("acp: failed to persist status", "session", s.ID, "status", status, "err", err)
	}
	m.emitSession(s, errText)
}

func (m *Manager) emitSession(s *Session, errText string) {
	s.mu.Lock()
	status, interactive, turn := s.status, s.interactive, s.turnNo
	s.mu.Unlock()
	m.ui.Emit(EventSession, map[string]any{
		"sessionId": s.ID,
		"cardId":    s.CardID,
		"status":    string(status),
		"error":     errText,
		// The branch is what the card displays and what its deploy button
		// publishes; deploy tells a card's own session apart from the deploy
		// it started, which shares the card id but not the console.
		"branch":       s.recordedBranch(),
		"worktreePath": s.Worktree.Path,
		"deploy":       s.Deploy != nil,
		"interactive":  interactive,
		"turn":         turn,
	})
}

func (m *Manager) comment(s *Session, text string) {
	m.commentCard(s.CardID, text)
}

func (m *Manager) commentCard(cardID, text string) {
	if cardID == "" {
		return // a planning session has no card to report to
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := m.writer.AddComment(ctx, cardID, text); err != nil {
		m.log.Error("acp: failed to add card comment", "card", cardID, "err", err)
	}
}

// findClaude resolves the claude binary from the global config (no per-agent
// override); used by the empty-registry fallback path and the startup probe.
func (m *Manager) findClaude() (string, error) {
	return m.resolveClaudeBin("")
}

// resolveClaudeBin resolves the claude binary: per-agent override, global
// config, PATH, then common install locations (GUI apps get launchd's minimal
// PATH).
func (m *Manager) resolveClaudeBin(override string) (string, error) {
	label := "agent binPath"
	if override == "" {
		override = m.cfg.ClaudePath
		label = "claudePath"
	}
	if override != "" {
		if _, err := os.Stat(override); err != nil {
			return "", fmt.Errorf("%s %s: %w", label, override, err)
		}
		return override, nil
	}
	return lookupBin("claude", "claude binary not found (set binPath on the agent or claudePath in the acp config)")
}

// resolveCodexBin resolves the codex binary: per-agent override, PATH, then
// common install locations.
func (m *Manager) resolveCodexBin(override string) (string, error) {
	if override != "" {
		if _, err := os.Stat(override); err != nil {
			return "", fmt.Errorf("codex binPath %s: %w", override, err)
		}
		return override, nil
	}
	return lookupBin("codex", "codex binary not found (set binPath on the agent)")
}

// firstNonEmpty returns the first non-empty string.
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// lookupBin finds name on PATH or in common install locations. When name is an
// absolute/explicit path (contains a separator) it is stat-checked directly.
func lookupBin(name, notFoundMsg string) (string, error) {
	if strings.ContainsRune(name, filepath.Separator) {
		if _, err := os.Stat(name); err != nil {
			return "", fmt.Errorf("%s: %w", name, err)
		}
		return name, nil
	}
	if p, err := exec.LookPath(name); err == nil {
		return p, nil
	}
	home, _ := os.UserHomeDir()
	for _, p := range []string{
		filepath.Join(home, ".local", "bin", name),
		"/opt/homebrew/bin/" + name,
		"/usr/local/bin/" + name,
	} {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("%s", notFoundMsg)
}

// agentLaunchArgv returns the base argv a bridge builds its invocation on: the
// agent's explicit Command when set — which is how a wrapper (a proxy launcher,
// a per-account shim) gets in front of the CLI — otherwise the resolved binary.
// The bridge appends its own protocol flags after it.
func agentLaunchArgv(a AgentEntry, resolveBin func(override string) (string, error)) ([]string, error) {
	if len(a.Command) == 0 {
		bin, err := resolveBin(a.BinPath)
		if err != nil {
			return nil, err
		}
		return []string{bin}, nil
	}
	return resolveArgv0(a.Command), nil
}

// resolveArgv0 makes an argv runnable from a GUI process: a bare command name is
// looked up on PATH and in the common install locations, because launchd hands
// GUI apps a minimal PATH. Left as written when nothing matches, so the spawn
// error names the command the user actually typed.
func resolveArgv0(argv []string) []string {
	if len(argv) == 0 {
		return argv
	}
	out := append([]string(nil), argv...)
	if p, err := lookupBin(argv[0], "not found"); err == nil {
		out[0] = p
	}
	return out
}

// planningPrompt opens a planning conversation: the board/agent system prompts,
// then what this session is for. It is deliberately explicit that nothing is to
// be changed — the tool policy enforces it, but the agent should not try.
func planningPrompt(systemPrompt string, agent AgentEntry, repo RepoEntry) string {
	var b []byte
	if p := strings.TrimSpace(systemPrompt); p != "" {
		b = fmt.Appendf(b, "%s\n\n", p)
	}
	if p := strings.TrimSpace(agent.Prompt); p != "" {
		b = fmt.Appendf(b, "%s\n\n", p)
	}
	if repo.Path == "" {
		b = fmt.Appendf(b, "Мы планируем новую задачу. Репозиторий не выбран, кода под рукой нет — ")
		b = fmt.Appendf(b, "опирайся на то, что расскажет пользователь, и не пытайся ничего искать в файлах.\n\n")
		b = fmt.Appendf(b, "Начни с короткого вопроса о том, что нужно сделать.")
		return string(b)
	}
	b = fmt.Appendf(b, "Мы планируем новую задачу по репозиторию `%s` (%s).\n", repo.Name, repo.Path)
	b = fmt.Appendf(b, "Код у тебя есть — читай файлы, ищи по ним, смотри историю git: ")
	b = fmt.Appendf(b, "чтение и безопасные команды осмотра разрешены, опирайся на код, а не на догадки.\n")
	b = fmt.Appendf(b, "Не меняй ничего: ни файлов, ни состояния. Это обсуждение, а не выполнение. ")
	b = fmt.Appendf(b, "Если для ответа всё же нужна команда, меняющая состояние, — попроси, у пользователя спросят подтверждение.\n\n")
	b = fmt.Appendf(b, "Начни с короткого вопроса о том, что нужно сделать.")
	return string(b)
}

// composePrompt builds the agent task text from the card. The final prompt is
// the board/column system prompt, then the agent's own system prompt, then the
// card task.
func composePrompt(ev CardMoved, agent AgentEntry, systemPrompt string, useWorktree bool) string {
	var b []byte
	if p := strings.TrimSpace(systemPrompt); p != "" {
		b = fmt.Appendf(b, "%s\n\n", p)
	}
	if p := strings.TrimSpace(agent.Prompt); p != "" {
		b = fmt.Appendf(b, "%s\n\n", p)
	}
	b = fmt.Appendf(b, "Задача: %s\n", ev.Title)
	if ev.Body != "" {
		b = fmt.Appendf(b, "\n%s\n", ev.Body)
	}
	if useWorktree {
		b = fmt.Appendf(b, "\nРаботай в текущем каталоге — это отдельный git worktree, созданный специально для этой задачи. Можешь делать локальные коммиты. Не выполняй git push.")
	} else {
		b = fmt.Appendf(b, "\nРаботай в текущем каталоге — это рабочая копия репозитория пользователя. Не переключай ветки, не делай коммитов и git push: оставь изменения незакоммиченными для ревью.")
	}
	return string(b)
}

// composeDeployPrompt builds the task text of a deploy session: the same system
// prompts an ordinary task gets, then the deploy instructions, then the concrete
// facts — which branch goes where, and what the resulting address should be.
func composeDeployPrompt(ev CardMoved, agent AgentEntry, systemPrompt, deployPrompt string, target DeployEntry, branch string) string {
	var b []byte
	if p := strings.TrimSpace(systemPrompt); p != "" {
		b = fmt.Appendf(b, "%s\n\n", p)
	}
	if p := strings.TrimSpace(agent.Prompt); p != "" {
		b = fmt.Appendf(b, "%s\n\n", p)
	}
	if p := strings.TrimSpace(deployPrompt); p != "" {
		b = fmt.Appendf(b, "%s\n\n", p)
	} else {
		b = fmt.Appendf(b, "%s\n\n", DefaultDeployPrompt)
	}
	slug := dokku.AppSlug(branch)
	b = fmt.Appendf(b, "Карточка: %s\nВетка: %s\nЦель: %s\nПриложение Dokku: %s\nОжидаемый адрес: %s\n",
		ev.Title, branch, target.Name, target.AppName(slug), target.URL(slug))
	if ev.Body != "" {
		b = fmt.Appendf(b, "\nОписание карточки:\n%s\n", ev.Body)
	}
	return string(b)
}
