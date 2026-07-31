package acp

import (
	"errors"
	"fmt"
	"strings"
)

// A column of the board and what happens in it: the action a card entering it
// starts, the agents who work it, and how many of them may work it at once.
//
// This is where "the To Test column is tested by the Tester agent" is said —
// once, for the whole board, rather than repeated inside every route that
// passes through the column. A flow then only says where a card goes next
// (see flows.go): the column is the behaviour, the flow is the route.

// ColumnSpec is one configured column.
type ColumnSpec struct {
	// The board it belongs to and the option that is the column itself.
	// Written by the editor, which knows them; a spec migrated from the old
	// column-name config keys has them empty until an event fills them in.
	BoardID    string `json:"boardId,omitempty"`
	PropertyID string `json:"propertyId,omitempty"`
	OptionID   string `json:"optionId,omitempty"`

	// Property and Column are the names — what the user reads, and what a spec
	// without ids is matched by.
	Property string `json:"property"`
	Column   string `json:"column"`

	Action string `json:"action"` // FlowAction*

	// Agents is the roster: everyone who works this column. A card picks one of
	// them when it does not name an agent itself. Empty leaves the choice to
	// the card, exactly as before.
	Agents []string `json:"agents,omitempty"`

	// DeployName pins the deploy target for an "deploy" column.
	DeployName string `json:"deployName,omitempty"`

	// MaxRunning bounds how many sessions this column runs at once. Zero means
	// no limit of its own (the global maxConcurrent still applies).
	MaxRunning int `json:"maxRunning,omitempty"`
}

// Key identifies the column for the queue and for counting what is running in
// it. Ids are used where known, since names change and ids do not.
func (c ColumnSpec) Key() string {
	if c.OptionID != "" {
		return c.BoardID + "|" + c.OptionID
	}
	return strings.ToLower(c.Property + "|" + c.Column)
}

// columnKey is Key for a column as it arrives in an event.
func columnKey(boardID string, c Column) string {
	if c.OptionID != "" {
		return boardID + "|" + c.OptionID
	}
	return strings.ToLower(c.PropertyName + "|" + c.Name)
}

// Columns returns a snapshot of the registry.
func (m *Manager) Columns() []ColumnSpec {
	m.cfgMu.RLock()
	defer m.cfgMu.RUnlock()
	return append([]ColumnSpec(nil), m.cfg.Columns...)
}

// BoardColumns returns the columns configured for one board — the specs the
// editor shows. A spec that has never seen an event carries no board id yet, so
// it is offered to every board: it came from the config's own column names.
func (m *Manager) BoardColumns(boardID string) []ColumnSpec {
	m.cfgMu.RLock()
	defer m.cfgMu.RUnlock()
	out := make([]ColumnSpec, 0, len(m.cfg.Columns))
	for _, c := range m.cfg.Columns {
		if c.BoardID == "" || c.BoardID == boardID {
			out = append(out, c)
		}
	}
	return out
}

// matchColumn finds the spec for a column of an event, most precise first:
// the board's own option id, then the board's property/column names, then a
// spec that names no board at all (a migrated one).
func matchColumn(specs []ColumnSpec, boardID string, c Column) (ColumnSpec, int, bool) {
	byName := func(s ColumnSpec) bool {
		return strings.EqualFold(s.Property, c.PropertyName) && strings.EqualFold(s.Column, c.Name)
	}
	if c.OptionID != "" {
		for i, s := range specs {
			if s.OptionID == c.OptionID && (s.BoardID == "" || s.BoardID == boardID) {
				return s, i, true
			}
		}
	}
	for i, s := range specs {
		if s.BoardID == boardID && s.OptionID == "" && byName(s) {
			return s, i, true
		}
	}
	for i, s := range specs {
		if s.BoardID == "" && byName(s) {
			return s, i, true
		}
	}
	return ColumnSpec{}, -1, false
}

// columnFor is what the trigger loop asks: does anything happen when a card
// lands here? It also backfills the ids of a spec matched by name, so the very
// first move teaches the config which option the column actually is — after
// that, renaming the column on the board changes nothing.
func (m *Manager) columnFor(boardID string, c Column) (ColumnSpec, bool) {
	m.cfgMu.RLock()
	specs := append([]ColumnSpec(nil), m.cfg.Columns...)
	m.cfgMu.RUnlock()

	spec, _, ok := matchColumn(specs, boardID, c)
	if !ok {
		return ColumnSpec{}, false
	}
	if spec.OptionID == "" && c.OptionID != "" {
		spec = m.learnColumnIDs(spec, boardID, c)
	}
	return spec, true
}

// learnColumnIDs records which option a name-matched spec turned out to be.
func (m *Manager) learnColumnIDs(spec ColumnSpec, boardID string, c Column) ColumnSpec {
	m.cfgMu.Lock()
	defer m.cfgMu.Unlock()
	_, i, ok := matchColumn(m.cfg.Columns, boardID, c)
	if !ok || m.cfg.Columns[i].OptionID != "" {
		return spec // somebody else got there first
	}
	m.cfg.Columns[i].BoardID = boardID
	m.cfg.Columns[i].PropertyID = c.PropertyID
	m.cfg.Columns[i].OptionID = c.OptionID
	if err := m.persistConfigLocked(); err != nil {
		m.log.Warn("acp: cannot persist column ids", "column", c.Name, "err", err)
	}
	m.log.Info("acp: column bound to its option", "column", c.Name, "board", boardID, "option", c.OptionID)
	return m.cfg.Columns[i]
}

// validateColumn normalizes and checks one spec against the registries it may
// reference.
func validateColumn(c ColumnSpec, agents []AgentEntry, deploys []DeployEntry) (ColumnSpec, error) {
	c.Property = strings.TrimSpace(c.Property)
	c.Column = strings.TrimSpace(c.Column)
	if c.Column == "" {
		return ColumnSpec{}, fmt.Errorf("не указана колонка")
	}
	if c.Property == "" {
		return ColumnSpec{}, fmt.Errorf("не указано свойство колонки %q", c.Column)
	}
	c.Action = strings.TrimSpace(c.Action)
	if c.Action == "" {
		c.Action = FlowActionNone
	}
	switch c.Action {
	case FlowActionNone, FlowActionAgent, FlowActionDeploy, FlowActionTest:
	default:
		return ColumnSpec{}, fmt.Errorf("неизвестное действие %q у колонки %q", c.Action, c.Column)
	}

	roster := make([]string, 0, len(c.Agents))
	seen := make(map[string]bool, len(c.Agents))
	for _, name := range c.Agents {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if !hasAgent(agents, name) {
			return ColumnSpec{}, fmt.Errorf("агент %q не найден в реестре (%s)", name, agentNames(agents))
		}
		if seen[strings.ToLower(name)] {
			continue
		}
		seen[strings.ToLower(name)] = true
		roster = append(roster, name)
	}
	c.Agents = roster

	c.DeployName = strings.TrimSpace(c.DeployName)
	if c.DeployName != "" && !hasDeploy(deploys, c.DeployName) {
		return ColumnSpec{}, fmt.Errorf("цель деплоя %q не найдена в реестре (%s)", c.DeployName, deployNames(deploys))
	}
	if c.MaxRunning < 0 {
		return ColumnSpec{}, fmt.Errorf("лимит одновременных сессий не может быть отрицательным")
	}
	return c, nil
}

// SaveColumn adds or replaces a column spec (matched by board+option, else by
// names) and persists the config.
func (m *Manager) SaveColumn(c ColumnSpec) (ColumnSpec, error) {
	m.cfgMu.Lock()
	defer m.cfgMu.Unlock()

	c, err := validateColumn(c, m.cfg.Agents, m.cfg.Deploys)
	if err != nil {
		return ColumnSpec{}, err
	}
	for i, existing := range m.cfg.Columns {
		if sameColumn(existing, c) {
			m.cfg.Columns[i] = c
			return c, m.persistConfigLocked()
		}
	}
	m.cfg.Columns = append(m.cfg.Columns, c)
	return c, m.persistConfigLocked()
}

// RemoveColumn forgets a column's settings. The column itself stays on the
// board — only what happens in it goes away.
func (m *Manager) RemoveColumn(boardID, optionID, column string) error {
	m.cfgMu.Lock()
	defer m.cfgMu.Unlock()
	target := ColumnSpec{BoardID: boardID, OptionID: optionID, Column: column}
	for i, existing := range m.cfg.Columns {
		if sameColumn(existing, target) {
			m.cfg.Columns = append(m.cfg.Columns[:i], m.cfg.Columns[i+1:]...)
			return m.persistConfigLocked()
		}
	}
	return fmt.Errorf("настройки колонки не найдены")
}

// sameColumn reports whether two specs describe one column: the same option of
// the same board, or — for a spec that has no ids yet — the same name.
func sameColumn(a, b ColumnSpec) bool {
	if a.OptionID != "" && b.OptionID != "" {
		return a.OptionID == b.OptionID && a.BoardID == b.BoardID
	}
	return strings.EqualFold(a.Column, b.Column) &&
		(a.BoardID == b.BoardID || a.BoardID == "" || b.BoardID == "")
}

// migratedColumns turns the config's legacy column keys into specs, so an
// install that has never seen this feature behaves exactly as it did: the
// trigger column runs an agent, the deploy column deploys, the test column
// tests. They carry no ids — the first card moved into one fills those in.
func migratedColumns(cfg Config) []ColumnSpec {
	out := make([]ColumnSpec, 0, 3)
	add := func(column, action string) {
		column = strings.TrimSpace(column)
		if column == "" {
			return
		}
		for _, c := range out {
			if strings.EqualFold(c.Column, column) {
				return // two keys naming one column: the first wins
			}
		}
		out = append(out, ColumnSpec{Property: cfg.TriggerProperty, Column: column, Action: action})
	}
	add(cfg.TriggerColumn, FlowActionAgent)
	add(cfg.DeployColumn, FlowActionDeploy)
	add(cfg.TestColumn, FlowActionTest)
	return out
}

// errStageBusy is not a failure: the column's crew is fully occupied, so the
// card waits for a free member instead of taking its failure branch.
var errStageBusy = errors.New("состав колонки занят")

// WorktreeMode is where sessions run: "always" — each in its own git worktree,
// "never" — directly in the repository. It decides whether a column's crew can
// work in parallel at all, which is why the editor asks.
func (m *Manager) WorktreeMode() string {
	m.cfgMu.RLock()
	defer m.cfgMu.RUnlock()
	if m.cfg.UseWorktrees() {
		return "always"
	}
	return "never"
}
