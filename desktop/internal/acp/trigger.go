package acp

import (
	"fmt"
	"strings"
)

// triggerLoop consumes normalized card-move events and applies the trigger
// policy: enter the trigger column → start a session (idempotently); leave it
// while a session is live → cancel.
func (m *Manager) triggerLoop(ch <-chan CardMoved) {
	defer m.wg.Done()
	for {
		select {
		case <-m.rootCtx.Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			m.handleEvent(ev)
		}
	}
}

func (m *Manager) handleEvent(ev CardMoved) {
	switch {
	case m.isTriggerColumn(ev.ToColumn):
		m.handleEnter(ev)
	case m.isDeployColumn(ev.ToColumn):
		m.handleDeployEnter(ev)
	case m.isTestColumn(ev.ToColumn):
		m.handleTestEnter(ev)
	case m.isTriggerColumn(ev.FromColumn), m.isDeployColumn(ev.FromColumn), m.isTestColumn(ev.FromColumn):
		if m.CancelSessionForCard(ev.CardID, "карточка убрана из триггерной колонки") {
			m.log.Info("acp: session cancelled by card move", "card", ev.CardID)
		}
	}
}

func (m *Manager) handleEnter(ev CardMoved) {
	if !m.claimMove(ev, "agent") {
		return
	}
	s, err := m.StartSessionForEvent(ev)
	if err != nil {
		m.log.Warn("acp: session not started", "card", ev.CardID, "err", err)
		m.commentCard(ev.CardID, fmt.Sprintf("Агент не запущен: %v", err))
		return
	}
	m.log.Info("acp: session started", "session", s.ID, "card", ev.CardID, "repo", s.RepoPath)
}

// handleDeployEnter is handleEnter for the deploy column: same guards, a session
// pointed at publishing the card's branch instead of working on the card.
func (m *Manager) handleDeployEnter(ev CardMoved) {
	if !m.claimMove(ev, "deploy") {
		return
	}
	s, err := m.StartDeploySessionForEvent(ev)
	if err != nil {
		m.log.Warn("acp: deploy session not started", "card", ev.CardID, "err", err)
		m.commentCard(ev.CardID, fmt.Sprintf("Деплой не запущен: %v", err))
		return
	}
	m.log.Info("acp: deploy session started", "session", s.ID, "card", ev.CardID, "repo", s.RepoPath, "branch", s.DeployBranch)
}

// handleTestEnter is handleEnter for the test column: a session pointed at the
// card's deployed preview, checking it instead of writing it.
func (m *Manager) handleTestEnter(ev CardMoved) {
	if !m.claimMove(ev, "test") {
		return
	}
	s, err := m.StartTestSessionForEvent(ev)
	if err != nil {
		m.log.Warn("acp: test session not started", "card", ev.CardID, "err", err)
		m.commentCard(ev.CardID, fmt.Sprintf("Тестирование не запущено: %v", err))
		return
	}
	m.log.Info("acp: test session started", "session", s.ID, "card", ev.CardID, "url", s.Test.URL)
}

// claimMove applies the guards every trigger column shares: one move is one
// session (a drag-and-drop produces a burst of patches), and a card with a live
// session is left alone. kind namespaces the idempotency key, so the agent and
// deploy columns cannot suppress each other's events.
func (m *Manager) claimMove(ev CardMoved, kind string) bool {
	key := fmt.Sprintf("%s|%s|%s|%s", kind, ev.CardID, ev.FromColumn.OptionID, ev.ToColumn.OptionID)
	fresh, err := m.store.ClaimIdempotency(key, "", m.cfg.IdempotencyWindow())
	if err != nil {
		m.log.Error("acp: idempotency check failed", "err", err)
		return false
	}
	if !fresh {
		m.log.Debug("acp: duplicate move suppressed", "card", ev.CardID, "kind", kind)
		return false
	}

	m.mu.Lock()
	_, live := m.byCard[ev.CardID]
	m.mu.Unlock()
	if live {
		m.log.Info("acp: card already has a live session, skipping", "card", ev.CardID, "kind", kind)
		return false
	}
	return true
}

// isTriggerColumn matches the configured trigger property/option by name,
// case-insensitively.
func (m *Manager) isTriggerColumn(c Column) bool {
	return strings.EqualFold(c.PropertyName, m.cfg.TriggerProperty) &&
		strings.EqualFold(c.Name, m.cfg.TriggerColumn)
}

// isDeployColumn matches the deploy column on the same property. An empty
// deployColumn disables the trigger rather than matching every unnamed column.
func (m *Manager) isDeployColumn(c Column) bool {
	m.cfgMu.RLock()
	property, column := m.cfg.TriggerProperty, m.cfg.DeployColumn
	m.cfgMu.RUnlock()
	if strings.TrimSpace(column) == "" {
		return false
	}
	return strings.EqualFold(c.PropertyName, property) &&
		strings.EqualFold(c.Name, column)
}

// isTestColumn matches the test column on the same property. As with deploy, an
// empty testColumn disables the trigger rather than matching every unnamed one.
func (m *Manager) isTestColumn(c Column) bool {
	m.cfgMu.RLock()
	property, column := m.cfg.TriggerProperty, m.cfg.TestColumn
	m.cfgMu.RUnlock()
	if strings.TrimSpace(column) == "" {
		return false
	}
	return strings.EqualFold(c.PropertyName, property) &&
		strings.EqualFold(c.Name, column)
}
