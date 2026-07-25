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
)

// Manager owns all agent sessions: it consumes board events, enforces limits
// and policies, and reports results back to the board and the UI.
type Manager struct {
	cfg     Config
	cfgMu   sync.RWMutex // guards the UI-mutable parts of cfg (Repos, Agents, SystemPrompt)
	cfgPath string       // where registry edits are persisted; empty in tests
	store   *Store
	writer  BoardWriter
	ui      UIEmitter
	log     *slog.Logger

	mu     sync.Mutex
	active map[string]*Session // session ID → session
	byCard map[string]*Session // card ID → live (non-terminal) session

	sem     chan struct{}
	rootCtx context.Context
	stop    context.CancelFunc
	wg      sync.WaitGroup
}

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
	return &Manager{
		cfg:     cfg,
		cfgPath: cfgPath,
		store:   st,
		writer:  w,
		ui:      ui,
		log:     log,
		active:  make(map[string]*Session),
		byCard:  make(map[string]*Session),
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
		m.commentCard(r.CardID, "⚠️ Сессия агента была прервана перезапуском приложения.")
	}
}

// StartSessionForEvent creates and launches a session for a validated trigger
// event. Callers must have passed idempotency/liveness checks.
func (m *Manager) StartSessionForEvent(ev CardMoved) (*Session, error) {
	repoPath, err := m.resolveRepo(ev)
	if err != nil {
		return nil, err
	}
	agent, err := m.resolveAgent(ev)
	if err != nil {
		return nil, err
	}
	// Without worktrees, two agents must never share one working tree
	// (spec §7): reject while another live session uses the same repo.
	if !m.cfg.UseWorktrees() {
		m.mu.Lock()
		var busyCard string
		for _, other := range m.active {
			if other.RepoPath == repoPath {
				busyCard = other.CardID
				break
			}
		}
		m.mu.Unlock()
		if busyCard != "" {
			return nil, fmt.Errorf("в репозитории %s уже работает сессия другой карточки (%s) — дождитесь её завершения", repoPath, busyCard)
		}
	}

	m.cfgMu.RLock()
	systemPrompt := m.cfg.SystemPrompt
	m.cfgMu.RUnlock()
	s := &Session{
		ID:         uuid.NewString(),
		CardID:     ev.CardID,
		BoardID:    ev.BoardID,
		RepoPath:   repoPath,
		BaseBranch: ev.Props["branch"],
		Agent:      agent,
		PromptText: composePrompt(ev, agent, systemPrompt, m.cfg.UseWorktrees()),
		status:     StatusQueued,
		allowTools: make(map[string]bool),
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
	m.byCard[s.CardID] = s
	m.mu.Unlock()
	m.emitSession(s, "")

	m.wg.Add(1)
	go m.runSession(s)
	return s, nil
}

// CancelSessionForCard cancels the live session of a card, if any.
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
	cancel := s.cancel
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	} else {
		// Still queued: mark terminal right away; runSession will observe.
		m.finishSession(s, StatusCancelled, reason)
	}
	return true
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
}

// ---- internals ----

// finishSession transitions to a terminal status exactly once.
func (m *Manager) finishSession(s *Session, status SessionStatus, errText string) {
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

func (m *Manager) persistStatus(s *Session, status SessionStatus, errText string) {
	if err := m.store.SetSessionStatus(s.ID, status, errText); err != nil {
		m.log.Warn("acp: failed to persist status", "session", s.ID, "status", status, "err", err)
	}
	m.emitSession(s, errText)
}

func (m *Manager) emitSession(s *Session, errText string) {
	m.ui.Emit(EventSession, map[string]any{
		"sessionId": s.ID,
		"cardId":    s.CardID,
		"status":    string(s.Status()),
		"error":     errText,
	})
}

func (m *Manager) comment(s *Session, text string) {
	m.commentCard(s.CardID, text)
}

func (m *Manager) commentCard(cardID, text string) {
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

// agentSpawnEnv turns an agent's Env map into the "KEY=value" slice and the
// list of names to drop from the inherited environment, so per-agent values
// (CODEX_HOME, OPENAI_API_KEY, CLAUDE_CONFIG_DIR, …) override the parent's.
func agentSpawnEnv(a AgentEntry) (env []string, drop []string) {
	for k, v := range a.Env {
		env = append(env, k+"="+v)
		drop = append(drop, k)
	}
	return env, drop
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
