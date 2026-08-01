package acp

import (
	"errors"
	"fmt"
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
	// A board may ship its own columns and routes (the template does); take
	// them once, before anything asks what this column does.
	m.seedFromBoard(ev.BoardID)

	// A card with a route is driven by its flow; a card without one still gets
	// whatever its column does — the flow adds transitions, not behaviour.
	if m.handleFlowMove(ev) {
		return
	}
	to, entered := m.columnFor(ev.BoardID, ev.ToColumn)
	if entered && to.Action != FlowActionNone {
		m.handleEnter(ev, to)
		return
	}
	if from, left := m.columnFor(ev.BoardID, ev.FromColumn); left && from.Action != FlowActionNone {
		// A card that leaves stops waiting for the column it left.
		m.dequeueStage(ev.CardID)
		if m.CancelSessionForCard(ev.CardID, "карточка убрана из триггерной колонки") {
			m.log.Info("acp: session cancelled by card move", "card", ev.CardID)
		}
	}
}

// handleEnter starts whatever the column does. The three kinds differ only in
// the options they start the session with and in what they say when they fail,
// so the guards, the idempotency key and the logging are shared.
func (m *Manager) handleEnter(ev CardMoved, spec ColumnSpec) {
	opts := startOptions{column: spec}
	kind, failed := "agent", "Агент не запущен"
	switch spec.Action {
	case FlowActionDeploy:
		opts.deploy, kind, failed = true, "deploy", "Деплой не запущен"
	case FlowActionTest:
		opts.test, kind, failed = true, "test", "Тестирование не запущено"
	}
	if !m.claimMove(ev, kind) {
		return
	}
	s, err := m.startSession(ev, opts)
	if errors.Is(err, errStageBusy) {
		m.enqueueStage(ev, spec, "", "")
		return
	}
	var mine AssignedToHumanError
	if errors.As(err, &mine) {
		m.sayCardIsTaken(ev.CardID, mine)
		return
	}
	if err != nil {
		m.log.Warn("acp: session not started", "card", ev.CardID, "kind", kind, "err", err)
		m.commentCard(ev.CardID, fmt.Sprintf("%s: %v", failed, err))
		return
	}
	m.log.Info("acp: session started", "session", s.ID, "card", ev.CardID, "kind", kind, "repo", s.RepoPath)
}

// claimIdempotent collapses the burst of patches one drag-and-drop produces
// into a single move. kind namespaces the key, so the agent, deploy, test and
// flow paths cannot suppress each other's events.
func (m *Manager) claimIdempotent(ev CardMoved, kind string) bool {
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
	return true
}

// claimMove adds the guard the standalone trigger columns need on top: a card
// with a live session is left alone.
func (m *Manager) claimMove(ev CardMoved, kind string) bool {
	if !m.claimIdempotent(ev, kind) {
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

// sayCardIsTaken explains, once per move, why nothing started: somebody has the
// card. It also says how to hand it back, since "nothing happened" is otherwise
// indistinguishable from a broken setup.
func (m *Manager) sayCardIsTaken(cardID string, err AssignedToHumanError) {
	m.log.Info("acp: card is assigned to a person, no agent started", "card", cardID, "assignee", err.Who)
	m.commentCard(cardID, fmt.Sprintf(
		"%s — агент не запускается. Снимите исполнителя или назначьте агента, если работу должен взять он.",
		err.Error()))
}
