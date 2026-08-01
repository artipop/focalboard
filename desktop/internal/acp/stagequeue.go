package acp

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// A column holds as many sessions at once as it was given crew and permission
// for. A card that arrives at a full column is not refused and not failed — it
// waits, exactly as a card waits for a person, and starts by itself as soon as
// somebody finishes. That is what a WIP limit means on a board.

// runningInColumn counts the live sessions of one column.
func (m *Manager) runningInColumn(key string) int {
	if key == "" {
		return 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, s := range m.active {
		if s.ColumnKey == key {
			n++
		}
	}
	return n
}

// columnByKey finds a configured column by the key its sessions carry.
func (m *Manager) columnByKey(key string) (ColumnSpec, bool) {
	m.cfgMu.RLock()
	defer m.cfgMu.RUnlock()
	for _, c := range m.cfg.Columns {
		if c.Key() == key {
			return c, true
		}
	}
	return ColumnSpec{}, false
}

// enqueueStage parks a card whose stage could not start because the column is
// full. It says so on the card once — a card re-queued by a later attempt has
// already been explained.
func (m *Manager) enqueueStage(ev CardMoved, spec ColumnSpec, flowName, nodeID string) {
	fresh, err := m.store.EnqueueStage(QueuedStage{
		CardID:    ev.CardID,
		BoardID:   ev.BoardID,
		ColumnKey: spec.Key(),
		Flow:      flowName,
		NodeID:    nodeID,
	})
	if err != nil {
		m.log.Error("acp: cannot queue the card", "card", ev.CardID, "err", err)
		return
	}
	m.log.Info("acp: card waiting for a place in the column",
		"card", ev.CardID, "column", spec.Column, "fresh", fresh)
	if fresh {
		m.commentCard(ev.CardID, fmt.Sprintf(
			"Колонка «%s» занята — стадия начнётся, как только освободится место.", spec.Column))
	}
}

// dequeueStage forgets a card that no longer waits: it started, or somebody
// moved it somewhere else.
func (m *Manager) dequeueStage(cardID string) {
	if err := m.store.DequeueStage(cardID); err != nil {
		m.log.Warn("acp: cannot clear the queue entry", "card", cardID, "err", err)
	}
}

// drainColumn starts the card that has waited longest for this column, if the
// column has room again. Called where a session releases its resources, so the
// place a session frees is filled by the next card rather than by nothing.
func (m *Manager) drainColumn(key string) {
	if key == "" || m.reader == nil {
		return
	}
	q, ok, err := m.store.NextQueuedStage(key)
	if err != nil {
		m.log.Error("acp: cannot read the stage queue", "err", err)
		return
	}
	if !ok {
		return
	}
	spec, ok := m.columnByKey(key)
	if !ok {
		// The column was reconfigured out of existence while the card waited.
		m.dequeueStage(q.CardID)
		return
	}

	ctx, cancel := context.WithTimeout(m.rootCtx, 10*time.Second)
	ev, err := m.reader.CardByID(ctx, q.CardID)
	cancel()
	if err != nil {
		m.log.Warn("acp: cannot read a queued card", "card", q.CardID, "err", err)
		return
	}

	opts := startOptions{column: spec, flowName: q.Flow, flowNodeID: q.NodeID}
	action := spec.Action
	if q.Flow != "" {
		if flow, found := m.FlowByName(q.Flow); found {
			if node, found := flow.Node(q.NodeID); found {
				opts.deployOverride = node.DeployName
				if node.AgentName != "" {
					opts.agentCrew = []string{node.AgentName}
				}
				if node.Action != "" {
					action = node.Action
				}
			}
		}
	}
	switch action {
	case FlowActionDeploy:
		opts.deploy = true
	case FlowActionTest:
		opts.test = true
	case FlowActionNone, "":
		m.dequeueStage(q.CardID)
		return
	}

	s, err := m.startSession(ev, opts)
	if errors.Is(err, errStageBusy) {
		return // somebody else took the place; the card keeps its turn
	}
	m.dequeueStage(q.CardID)
	// Somebody took the card while it waited: it leaves the queue and stays
	// where it is, rather than failing its stage.
	var mine AssignedToHumanError
	if errors.As(err, &mine) {
		m.sayCardIsTaken(q.CardID, mine)
		return
	}
	if err != nil {
		m.log.Warn("acp: queued stage not started", "card", q.CardID, "column", spec.Column, "err", err)
		m.commentCard(q.CardID, fmt.Sprintf("Стадия «%s» не запустилась: %v", spec.Column, err))
		if q.Flow != "" {
			m.advanceFlow(q.CardID, TriggerFailure, "шаг не удалось запустить")
		}
		return
	}
	m.log.Info("acp: queued stage started", "session", s.ID, "card", q.CardID, "column", spec.Column)
}
