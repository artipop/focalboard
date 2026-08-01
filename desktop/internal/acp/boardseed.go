package acp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// A board can bring its own automation. The "My Project Tasks" template ships
// the columns it runs and the routes cards take across it, in the board's own
// properties, so a board made from it works before anything is configured — the
// columns are already the ones the routes name, and the ids match, because both
// halves were written together.
//
// What the board brings is a **seed**, not a second source of truth: it is
// imported into the registry once, tagged with the board it came from, and from
// then on the registry is what runs and what the editors edit. A template that
// changes later does not silently rewrite what somebody has since adjusted.

// Board properties the template writes its automation into.
const (
	BoardPropColumns = "acpColumns"
	BoardPropFlows   = "acpFlows"
)

// BoardMeta reads a board's own properties, which is where a template leaves
// the automation it ships. Optional: without it a board simply brings nothing.
type BoardMeta interface {
	BoardProperties(ctx context.Context, boardID string) (map[string]any, error)
}

// SetBoardMeta supplies the board-property reader.
func (m *Manager) SetBoardMeta(b BoardMeta) { m.meta = b }

// SeedBoard takes a board's own automation into the registry on demand — what
// the first card move would do anyway, asked for early by the setup wizard.
func (m *Manager) SeedBoard(boardID string) { m.seedFromBoard(boardID) }

// seedFromBoard imports whatever automation a board carries. It runs once per
// board per run: the import is idempotent anyway (anything already registered
// is left alone), and the point of the flag is to not read the board on every
// card move.
func (m *Manager) seedFromBoard(boardID string) {
	if boardID == "" || m.meta == nil {
		return
	}
	m.seededMu.Lock()
	if m.seeded == nil {
		m.seeded = make(map[string]bool)
	}
	if m.seeded[boardID] {
		m.seededMu.Unlock()
		return
	}
	m.seeded[boardID] = true
	m.seededMu.Unlock()

	// The wizard may ask before the trigger loop has started, and a nil parent
	// context is a panic rather than a timeout.
	parent := m.rootCtx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	props, err := m.meta.BoardProperties(ctx, boardID)
	cancel()
	if err != nil {
		m.log.Warn("acp: cannot read the board's own settings", "board", boardID, "err", err)
		return
	}
	if len(props) == 0 {
		return
	}

	columns, flows, err := parseBoardAutomation(props)
	if err != nil {
		m.log.Warn("acp: the board's own settings are unreadable", "board", boardID, "err", err)
		return
	}
	if added := m.adoptColumns(boardID, columns); added > 0 {
		m.log.Info("acp: columns taken from the board itself", "board", boardID, "count", added)
	}
	if added := m.adoptFlows(boardID, flows); added > 0 {
		m.log.Info("acp: routes taken from the board itself", "board", boardID, "count", added)
	}
}

// parseBoardAutomation reads the two properties a template writes. They arrive
// as whatever the board store decoded them into, so they are re-encoded and
// read back into the real types rather than picked apart by hand.
func parseBoardAutomation(props map[string]any) ([]ColumnSpec, []FlowEntry, error) {
	var columns []ColumnSpec
	if raw, ok := props[BoardPropColumns]; ok {
		if err := reinterpret(raw, &columns); err != nil {
			return nil, nil, fmt.Errorf("%s: %w", BoardPropColumns, err)
		}
	}
	var flows []FlowEntry
	if raw, ok := props[BoardPropFlows]; ok {
		if err := reinterpret(raw, &flows); err != nil {
			return nil, nil, fmt.Errorf("%s: %w", BoardPropFlows, err)
		}
	}
	return columns, flows, nil
}

func reinterpret(from any, into any) error {
	// A property may also arrive as the JSON text of itself: the board store
	// keeps free-form properties as strings in some paths.
	if text, ok := from.(string); ok {
		if strings.TrimSpace(text) == "" {
			return nil
		}
		return json.Unmarshal([]byte(text), into)
	}
	encoded, err := json.Marshal(from)
	if err != nil {
		return err
	}
	return json.Unmarshal(encoded, into)
}

// adoptColumns registers the board's columns, skipping any the registry already
// has an answer for — an edited column is the user's, not the template's.
func (m *Manager) adoptColumns(boardID string, columns []ColumnSpec) int {
	if len(columns) == 0 {
		return 0
	}
	m.cfgMu.Lock()
	defer m.cfgMu.Unlock()

	added := 0
	for _, c := range columns {
		c.BoardID = boardID
		valid, err := validateColumn(c, m.cfg.Agents, m.cfg.Deploys)
		if err != nil {
			m.log.Warn("acp: the board offers a column that cannot be used", "board", boardID, "column", c.Column, "err", err)
			continue
		}
		// A column the registry already answers for stays as it is — what
		// somebody edited is theirs. But a spec migrated from the old config
		// keys knows only a name, and the board knows exactly which option that
		// name turned out to be: take the binding, leave the behaviour.
		known := false
		for i, existing := range m.cfg.Columns {
			if !sameColumn(existing, valid) {
				continue
			}
			known = true
			if existing.OptionID == "" && valid.OptionID != "" {
				m.cfg.Columns[i].BoardID = valid.BoardID
				m.cfg.Columns[i].PropertyID = valid.PropertyID
				m.cfg.Columns[i].OptionID = valid.OptionID
				added++
			}
			break
		}
		if known {
			continue
		}
		m.cfg.Columns = append(m.cfg.Columns, valid)
		added++
	}
	if added > 0 {
		if err := m.persistConfigLocked(); err != nil {
			m.log.Warn("acp: cannot persist the board's columns", "board", boardID, "err", err)
		}
	}
	return added
}

// adoptFlows registers the board's routes under its own id, skipping names the
// board already has.
func (m *Manager) adoptFlows(boardID string, flows []FlowEntry) int {
	if len(flows) == 0 {
		return 0
	}
	m.cfgMu.Lock()
	defer m.cfgMu.Unlock()

	added := 0
	for _, f := range flows {
		f.BoardID = boardID
		valid, err := validateFlow(f, m.cfg.Repos, m.cfg.Agents, m.cfg.Deploys)
		if err != nil {
			m.log.Warn("acp: the board offers a route that cannot be used", "board", boardID, "flow", f.Name, "err", err)
			continue
		}
		known := false
		for _, existing := range m.cfg.Flows {
			if strings.EqualFold(existing.Name, valid.Name) &&
				(existing.BoardID == boardID || existing.BoardID == "") {
				known = true
				break
			}
		}
		if known {
			continue
		}
		m.cfg.Flows = append(m.cfg.Flows, valid)
		added++
	}
	if added > 0 {
		if err := m.persistConfigLocked(); err != nil {
			m.log.Warn("acp: cannot persist the board's routes", "board", boardID, "err", err)
		}
	}
	return added
}

// BoardAutomation is what a board carries: the columns it runs and the routes
// across it. Exported so a test — and a future "export this board's automation"
// — speaks the same shape the template does.
type BoardAutomation struct {
	Columns []ColumnSpec `json:"acpColumns,omitempty"`
	Flows   []FlowEntry  `json:"acpFlows,omitempty"`
}
