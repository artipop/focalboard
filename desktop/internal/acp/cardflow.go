package acp

import (
	"strings"
	"time"
)

// What a card can say about itself: which route it is on, where along it it
// stands, and what it is waiting for. The card shows this instead of making
// somebody read the comments backwards to work out why it has not moved.

// CardFlowStage is one stage of the route as the card sees it.
type CardFlowStage struct {
	NodeID  string   `json:"nodeId"`
	Column  string   `json:"column"`
	Action  string   `json:"action"`         // resolved: the stage's own, else its column's
	Crew    []string `json:"crew,omitempty"` // who works it
	Current bool     `json:"current"`
	Done    bool     `json:"done"` // the card has already been through it
}

// CardFlow is the whole answer for one card.
type CardFlow struct {
	Flow       string          `json:"flow"`
	Stages     []CardFlowStage `json:"stages"`
	CurrentID  string          `json:"currentNodeId"`
	Since      time.Time       `json:"since"`
	Branch     string          `json:"branch,omitempty"`
	WaitingFor []string        `json:"waitingFor,omitempty"` // human labels of the events the stage waits on
	Queued     bool            `json:"queued,omitempty"`     // waiting for a place in the column
	Running    bool            `json:"running,omitempty"`    // a session of this stage is working now
}

// CardFlowFor describes where a card stands on its route. It returns nothing —
// and no error — for a card that is not on one, since most cards are not.
func (m *Manager) CardFlowFor(cardID string) (*CardFlow, error) {
	st, ok, err := m.flowState(cardID)
	if err != nil || !ok {
		return nil, err
	}
	flow, found := m.FlowByName(st.Flow)
	if !found {
		return nil, nil
	}

	out := &CardFlow{Flow: flow.Name, CurrentID: st.NodeID, Since: st.EnteredAt, Branch: st.Branch}

	// "Done" is what the card's own history says it has been through, not what
	// the graph makes possible: a route with a loop has no linear order.
	visited := make(map[string]bool)
	if m.store != nil {
		if events, err := m.store.FlowEvents(cardID); err == nil {
			for _, e := range events {
				if e.FromNode != "" {
					visited[e.FromNode] = true
				}
			}
		}
	}

	for _, n := range flow.Nodes {
		action := n.Action
		if action == "" {
			if spec, ok := m.columnByName(flow.PropertyOr(m.triggerProperty()), n.Column); ok {
				action = spec.Action
			}
		}
		crew := n.Crew()
		if len(crew) == 0 {
			if spec, ok := m.columnByName(flow.PropertyOr(m.triggerProperty()), n.Column); ok {
				crew = spec.Agents
			}
		}
		out.Stages = append(out.Stages, CardFlowStage{
			NodeID:  n.ID,
			Column:  n.Column,
			Action:  action,
			Crew:    crew,
			Current: n.ID == st.NodeID,
			Done:    visited[n.ID] && n.ID != st.NodeID,
		})
	}

	for _, kind := range flow.WaitsFor(st.NodeID) {
		out.WaitingFor = append(out.WaitingFor, TriggerLabel(kind))
	}

	m.mu.Lock()
	_, out.Running = m.byCard[cardID]
	m.mu.Unlock()
	if !out.Running {
		out.Queued = m.cardIsQueued(cardID)
	}
	return out, nil
}

// cardIsQueued reports whether the card is waiting for a place in its column.
func (m *Manager) cardIsQueued(cardID string) bool {
	st, ok, err := m.flowState(cardID)
	if err != nil || !ok {
		return false
	}
	flow, found := m.FlowByName(st.Flow)
	if !found {
		return false
	}
	node, found := flow.Node(st.NodeID)
	if !found {
		return false
	}
	spec, found := m.columnByName(flow.PropertyOr(m.triggerProperty()), node.Column)
	if !found {
		return false
	}
	q, ok, err := m.store.NextQueuedStage(spec.Key())
	return err == nil && ok && q.CardID == cardID
}

// columnByName finds a configured column the way a flow node names one.
func (m *Manager) columnByName(property, column string) (ColumnSpec, bool) {
	m.cfgMu.RLock()
	defer m.cfgMu.RUnlock()
	for _, c := range m.cfg.Columns {
		if strings.EqualFold(c.Column, column) &&
			(property == "" || c.Property == "" || strings.EqualFold(c.Property, property)) {
			return c, true
		}
	}
	return ColumnSpec{}, false
}

// FlowStageCount is how busy one stage of a route is right now.
type FlowStageCount struct {
	NodeID  string `json:"nodeId"`
	Cards   int    `json:"cards"`   // cards standing on this stage
	Running int    `json:"running"` // of those, being worked on now
	Queued  int    `json:"queued"`  // of those, waiting for a place in the column
}

// FlowOverview is one route and where the board's cards are along it.
type FlowOverview struct {
	Flow   string           `json:"flow"`
	Stages []FlowStageCount `json:"stages"`
	Cards  int              `json:"cards"`
}

// BoardFlowOverview answers "where is everything right now" for a board: for
// each route it may use, how many cards stand on each of its stages and which
// of them are moving. It is the map the workflow view draws — the same data the
// engine runs on, rather than a second bookkeeping of it.
func (m *Manager) BoardFlowOverview(boardID string) ([]FlowOverview, error) {
	flows := m.BoardFlows(boardID)
	if len(flows) == 0 {
		return nil, nil
	}
	states, err := m.flowStates()
	if err != nil {
		return nil, err
	}

	// One pass over the live sessions, rather than one lookup per card.
	m.mu.Lock()
	running := make(map[string]bool, len(m.active))
	for cardID := range m.byCard {
		running[cardID] = true
	}
	m.mu.Unlock()

	out := make([]FlowOverview, 0, len(flows))
	for _, flow := range flows {
		view := FlowOverview{Flow: flow.Name}
		counts := make(map[string]*FlowStageCount, len(flow.Nodes))
		for _, n := range flow.Nodes {
			counts[n.ID] = &FlowStageCount{NodeID: n.ID}
		}
		for _, st := range states {
			if !strings.EqualFold(st.Flow, flow.Name) {
				continue
			}
			if boardID != "" && st.BoardID != "" && st.BoardID != boardID {
				continue
			}
			count, ok := counts[st.NodeID]
			if !ok {
				continue // the card stands on a stage the route no longer has
			}
			count.Cards++
			view.Cards++
			switch {
			case running[st.CardID]:
				count.Running++
			case m.cardIsQueued(st.CardID):
				count.Queued++
			}
		}
		for _, n := range flow.Nodes {
			view.Stages = append(view.Stages, *counts[n.ID])
		}
		out = append(out, view)
	}
	return out, nil
}
