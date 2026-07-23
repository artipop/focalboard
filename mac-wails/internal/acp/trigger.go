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
	case m.isTriggerColumn(ev.FromColumn):
		if m.CancelSessionForCard(ev.CardID, "карточка убрана из триггерной колонки") {
			m.log.Info("acp: session cancelled by card move", "card", ev.CardID)
		}
	}
}

func (m *Manager) handleEnter(ev CardMoved) {
	// A drag-and-drop can produce a burst of patches: one move = one session.
	key := fmt.Sprintf("%s|%s|%s", ev.CardID, ev.FromColumn.OptionID, ev.ToColumn.OptionID)
	fresh, err := m.store.ClaimIdempotency(key, "", m.cfg.IdempotencyWindow())
	if err != nil {
		m.log.Error("acp: idempotency check failed", "err", err)
		return
	}
	if !fresh {
		m.log.Debug("acp: duplicate move suppressed", "card", ev.CardID)
		return
	}

	m.mu.Lock()
	_, live := m.byCard[ev.CardID]
	m.mu.Unlock()
	if live {
		m.log.Info("acp: card already has a live session, skipping", "card", ev.CardID)
		return
	}

	s, err := m.StartSessionForEvent(ev)
	if err != nil {
		m.log.Warn("acp: session not started", "card", ev.CardID, "err", err)
		m.commentCard(ev.CardID, fmt.Sprintf("⚠️ Агент не запущен: %v", err))
		return
	}
	m.log.Info("acp: session started", "session", s.ID, "card", ev.CardID, "repo", s.RepoPath)
}

// isTriggerColumn matches the configured trigger property/option by name,
// case-insensitively.
func (m *Manager) isTriggerColumn(c Column) bool {
	return strings.EqualFold(c.PropertyName, m.cfg.TriggerProperty) &&
		strings.EqualFold(c.Name, m.cfg.TriggerColumn)
}
