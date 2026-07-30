package acp

import (
	"context"
	"fmt"
	"strings"
)

// Agent registry: named coding agents (claude/codex with their own prompt,
// model and env), edited from the desktop UI and persisted into the config
// file. Cards are mapped to an agent when one of their select option names
// (the "Agent" field) matches an entry name — mirroring the repo registry.

// Agents returns a snapshot of the registry.
func (m *Manager) Agents() []AgentEntry {
	m.cfgMu.RLock()
	defer m.cfgMu.RUnlock()
	return append([]AgentEntry(nil), m.cfg.Agents...)
}

// validateAgent normalizes and checks a registry entry.
func validateAgent(a AgentEntry) (AgentEntry, error) {
	a.Name = strings.TrimSpace(a.Name)
	if a.Name == "" {
		return AgentEntry{}, fmt.Errorf("имя агента не может быть пустым")
	}
	// An empty element (a stray space in the UI input, a hand-edited config)
	// would become an empty argv[0] and a confusing exec error.
	command := a.Command[:0:0]
	for _, arg := range a.Command {
		if arg = strings.TrimSpace(arg); arg != "" {
			command = append(command, arg)
		}
	}
	a.Command = command
	a.Kind = strings.TrimSpace(strings.ToLower(a.Kind))
	switch {
	case a.Kind == AgentKindACP:
		if len(a.Command) == 0 {
			return AgentEntry{}, fmt.Errorf("для агента типа %q нужно задать команду запуска (argv ACP-агента)", AgentKindACP)
		}
	case a.Kind == AgentKindClaude || a.Kind == AgentKindCodex:
	case IsExternalACP(a.Kind):
	default:
		return AgentEntry{}, fmt.Errorf("неизвестный тип агента %q (допустимо: %s)", a.Kind, strings.Join(AgentKinds, ", "))
	}
	a.ProxyName = strings.TrimSpace(a.ProxyName)
	return a, nil
}

// AddAgent registers a new agent and persists the config.
func (m *Manager) AddAgent(a AgentEntry) (AgentEntry, error) {
	a, err := validateAgent(a)
	if err != nil {
		return AgentEntry{}, err
	}
	m.cfgMu.Lock()
	defer m.cfgMu.Unlock()
	for _, e := range m.cfg.Agents {
		if strings.EqualFold(e.Name, a.Name) {
			return AgentEntry{}, fmt.Errorf("агент с именем %q уже существует", e.Name)
		}
	}
	if _, err := resolveNetworkIn(m.cfg.Proxies, a); err != nil {
		return AgentEntry{}, err
	}
	m.cfg.Agents = append(m.cfg.Agents, a)
	return a, m.persistConfigLocked()
}

// UpdateAgent replaces an existing agent (matched by name) and persists.
func (m *Manager) UpdateAgent(a AgentEntry) (AgentEntry, error) {
	a, err := validateAgent(a)
	if err != nil {
		return AgentEntry{}, err
	}
	m.cfgMu.Lock()
	defer m.cfgMu.Unlock()
	for i, e := range m.cfg.Agents {
		if strings.EqualFold(e.Name, a.Name) {
			if _, err := resolveNetworkIn(m.cfg.Proxies, a); err != nil {
				return AgentEntry{}, err
			}
			m.cfg.Agents[i] = a
			return a, m.persistConfigLocked()
		}
	}
	return AgentEntry{}, fmt.Errorf("агент %q не найден", a.Name)
}

// AgentUsers returns the registry as board accounts, in registry order.
func (m *Manager) AgentUsers() []AgentUser {
	m.cfgMu.RLock()
	defer m.cfgMu.RUnlock()
	out := make([]AgentUser, 0, len(m.cfg.Agents))
	for _, a := range m.cfg.Agents {
		if username := AgentUsername(a.Name); username != "" {
			out = append(out, AgentUser{Name: a.Name, Username: username})
		}
	}
	return out
}

// SyncAgentUsers gives every registered agent a board account and makes it a
// member of the board, so a card can be assigned to an agent in a person
// property. Accounts are never removed: a card may still name one long after
// the registry entry is gone.
func (m *Manager) SyncAgentUsers(ctx context.Context, boardID string) ([]AgentUser, error) {
	if m.users == nil {
		return nil, fmt.Errorf("создание пользователей-агентов недоступно")
	}
	agents := m.AgentUsers()
	if len(agents) == 0 {
		return nil, fmt.Errorf("реестр агентов пуст: сначала добавьте агента")
	}
	return m.users.EnsureAgentUsers(ctx, boardID, agents)
}

// SystemPrompt returns the board/column-level system prompt.
func (m *Manager) SystemPrompt() string {
	m.cfgMu.RLock()
	defer m.cfgMu.RUnlock()
	return m.cfg.SystemPrompt
}

// SetSystemPrompt stores the board/column-level system prompt and persists.
func (m *Manager) SetSystemPrompt(text string) error {
	m.cfgMu.Lock()
	defer m.cfgMu.Unlock()
	m.cfg.SystemPrompt = text
	return m.persistConfigLocked()
}

// RemoveAgent deletes a registry entry by name and persists the config.
func (m *Manager) RemoveAgent(name string) error {
	m.cfgMu.Lock()
	defer m.cfgMu.Unlock()
	for i, e := range m.cfg.Agents {
		if strings.EqualFold(e.Name, name) {
			m.cfg.Agents = append(m.cfg.Agents[:i], m.cfg.Agents[i+1:]...)
			return m.persistConfigLocked()
		}
	}
	return fmt.Errorf("агент %q не найден", name)
}

// AgentUsername is the board username an agent is provisioned under: the
// registry name folded to what a username can carry, so the entry "My Agent"
// and the account "my-agent" are the same agent. Also used for matching, which
// is why it must stay deterministic.
func AgentUsername(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '.' || r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

// sameAgentName reports whether a name from the board (a select option, an
// assignee's username) refers to the registry entry named entryName.
func sameAgentName(name, entryName string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	if strings.EqualFold(name, entryName) {
		return true
	}
	// The account carries the folded form of the name, so "My Agent" on the
	// board and the assignee "my-agent" both reach the same entry.
	folded := AgentUsername(name)
	return folded != "" && folded == AgentUsername(entryName)
}

// resolveAgent maps a trigger event to an agent. Priority:
//  1. explicit `agent` card property matching a registry entry;
//  2. an assignee (person property) whose username matches a registry entry;
//  3. a select option name (the "Agent" field) matching a registry entry;
//  4. the single registered agent, when exactly one exists;
//  5. a synthesized entry from the global AgentMode (backward compat: the
//     built-in claude bridge, or an external acp-command agent).
func (m *Manager) resolveAgent(ev CardMoved) (AgentEntry, error) {
	m.cfgMu.RLock()
	agents := append([]AgentEntry(nil), m.cfg.Agents...)
	mode := m.cfg.AgentMode
	m.cfgMu.RUnlock()

	find := func(name string) (AgentEntry, bool) {
		for _, a := range agents {
			if sameAgentName(name, a.Name) {
				return a, true
			}
		}
		return AgentEntry{}, false
	}

	if explicit := strings.TrimSpace(ev.Props["agent"]); explicit != "" {
		if a, ok := find(explicit); ok {
			return a, nil
		}
		return AgentEntry{}, fmt.Errorf("агент %q из свойства карточки не найден в реестре (%s)", explicit, agentNames(agents))
	}

	// An assignee is a deliberate choice on the card, so it outranks the tags.
	for _, person := range ev.PersonNames {
		if a, ok := find(person); ok {
			return a, nil
		}
	}

	for _, opt := range ev.OptionNames {
		if a, ok := find(opt); ok {
			return a, nil
		}
	}

	if len(agents) == 1 {
		return agents[0], nil
	}
	if len(agents) > 1 {
		return AgentEntry{}, fmt.Errorf("не удалось выбрать агента: назначьте агента исполнителем карточки или задайте поле \"Agent\" (доступно: %s)", agentNames(agents))
	}

	// Empty registry: fall back to the global AgentMode.
	kind := mode
	if kind == "" {
		kind = AgentKindClaude
	}
	return AgentEntry{Name: kind, Kind: kind}, nil
}

func agentNames(agents []AgentEntry) string {
	names := make([]string, len(agents))
	for i, a := range agents {
		names[i] = a.Name
	}
	return strings.Join(names, ", ")
}
