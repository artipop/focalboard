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
	case knownAdapter(a.Kind):
	default:
		return AgentEntry{}, fmt.Errorf("неизвестный тип агента %q (допустимо: %s)", a.Kind, strings.Join(AgentKinds, ", "))
	}
	cliArgs, err := validateCLIArgs(a)
	if err != nil {
		return AgentEntry{}, err
	}
	a.CLIArgs = cliArgs
	a.Options = normalizeOptions(a.Options)
	servers, err := validateMCPServers(a.MCPServers)
	if err != nil {
		return AgentEntry{}, fmt.Errorf("агент %q: %w", a.Name, err)
	}
	a.MCPServers = servers
	a.ProxyName = strings.TrimSpace(a.ProxyName)
	return a, nil
}

// validateMCPServers normalizes the agent's own MCP servers. A name has to be
// usable as a tool prefix and must not shadow one we spawn ourselves.
func validateMCPServers(servers map[string]AgentMCPServer) (map[string]AgentMCPServer, error) {
	if len(servers) == 0 {
		return nil, nil
	}
	out := make(map[string]AgentMCPServer, len(servers))
	for name, srv := range servers {
		name = strings.TrimSpace(name)
		if name == "" {
			return nil, fmt.Errorf("у MCP-сервера не задано имя")
		}
		if !validMCPName(name) {
			return nil, fmt.Errorf("имя MCP-сервера %q может состоять только из латиницы, цифр, дефиса и подчёркивания", name)
		}
		for _, builtin := range builtinMCPNames {
			if strings.EqualFold(name, builtin) {
				return nil, fmt.Errorf("имя %q занято встроенным сервером", name)
			}
		}
		for existing := range out {
			if strings.EqualFold(existing, name) {
				return nil, fmt.Errorf("MCP-сервер с именем %q уже задан", name)
			}
		}

		// A remote server is a normal thing to paste from a README, and it
		// would otherwise fail as "no command" — which explains nothing.
		if strings.TrimSpace(srv.URL) != "" || strings.EqualFold(strings.TrimSpace(srv.Type), "http") || strings.EqualFold(strings.TrimSpace(srv.Type), "sse") {
			return nil, fmt.Errorf("сервер %q удалённый (url/type), а поддерживаются пока только локальные: command + args", name)
		}
		srv.Command = strings.TrimSpace(srv.Command)
		if srv.Command == "" {
			return nil, fmt.Errorf("для MCP-сервера %q не задана команда запуска (command)", name)
		}
		args := srv.Args[:0:0]
		for _, arg := range srv.Args {
			if arg = strings.TrimSpace(arg); arg != "" {
				args = append(args, arg)
			}
		}
		srv.Args = args
		srv.Type = ""
		out[name] = srv
	}
	return out, nil
}

// normalizeOptions drops the empties. An option left blank means "whatever the
// agent starts with", which is what leaving it out says, and saying it twice
// only leaves the config file harder to read. Values are kept verbatim: what a
// value id may look like is the agent's business, not ours.
func normalizeOptions(options map[string]string) map[string]string {
	if len(options) == 0 {
		return nil
	}
	out := make(map[string]string, len(options))
	for id, value := range options {
		id, value = strings.TrimSpace(id), strings.TrimSpace(value)
		if id == "" || value == "" {
			continue
		}
		out[id] = value
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// validMCPName mirrors what a tool name may carry: the server name becomes the
// mcp__<name>__<tool> prefix the agent reports calls under.
func validMCPName(name string) bool {
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			return false
		}
	}
	return true
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
// property. Idempotent, and a no-op on an empty registry — the UI runs it
// whenever the registry or the board it is looking at changes, not on demand.
// Accounts are never removed: a card may still name one long after the registry
// entry is gone.
func (m *Manager) SyncAgentUsers(ctx context.Context, boardID string) ([]AgentUser, error) {
	if m.users == nil {
		return nil, fmt.Errorf("создание пользователей-агентов недоступно")
	}
	agents := m.AgentUsers()
	if len(agents) == 0 {
		return nil, nil
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

// RemoveAgent deletes a registry entry by name, persists the config and takes
// the agent's account off the boards it was a member of — an unregistered agent
// must stop being offered as an assignee. The account survives (cards may still
// name it), so re-registering the agent restores the same identity.
func (m *Manager) RemoveAgent(name string) error {
	removed, err := m.removeAgentEntry(name)
	if err != nil {
		return err
	}
	if m.users == nil {
		return nil
	}
	if _, err := m.users.RetireAgentUser(context.Background(), removed); err != nil {
		// The entry is already gone; the account is the part left dangling, so
		// say so rather than pretend the removal failed.
		return fmt.Errorf("агент %q удалён из реестра, но его учётная запись осталась в участниках доски: %w", removed.Name, err)
	}
	return nil
}

// removeAgentEntry drops the entry from the registry and returns it as an
// account, so the caller can retire it outside the config lock.
func (m *Manager) removeAgentEntry(name string) (AgentUser, error) {
	m.cfgMu.Lock()
	defer m.cfgMu.Unlock()
	for i, e := range m.cfg.Agents {
		if strings.EqualFold(e.Name, name) {
			m.cfg.Agents = append(m.cfg.Agents[:i], m.cfg.Agents[i+1:]...)
			return AgentUser{Name: e.Name, Username: AgentUsername(e.Name)}, m.persistConfigLocked()
		}
	}
	return AgentUser{}, fmt.Errorf("агент %q не найден", name)
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

// resolveAgent maps a trigger event to an agent, with no stage to speak for
// itself. See resolveSessionAgent for the whole rule.
func (m *Manager) resolveAgent(ev CardMoved) (AgentEntry, error) {
	a, _, err := m.resolveSessionAgent(ev, nil)
	return a, err
}

// resolveSessionAgent picks who runs the session.
//
// roster is the stage's own crew — the flow node's, else the column's. It is
// not a pin but a membership list: it says who may work this column at all, and
// the card chooses among them. So an assignee who is on the crew gets the card,
// an assignee who is not is passed over (the *Deploy* column is not worked by
// the developer the card is assigned to), and a column with no crew leaves the
// choice to the card exactly as before:
//
//  1. the card's own choice — the `agent` property, an assignee, an "Agent"
//     option — narrowed to the crew when there is one;
//  2. the crew itself: the first member with nothing else running;
//  3. the single registered agent, when exactly one exists;
//  4. a synthesized entry from the global AgentMode (backward compat: the
//     built-in claude bridge, or an external acp-command agent).
//
// busy reports that a crew exists and every one of its members is already
// working: the caller parks the card instead of piling a second session onto
// the same agent.
func (m *Manager) resolveSessionAgent(ev CardMoved, roster []string) (AgentEntry, bool, error) {
	m.cfgMu.RLock()
	agents := append([]AgentEntry(nil), m.cfg.Agents...)
	mode := m.cfg.AgentMode
	m.cfgMu.RUnlock()

	crew, err := crewOf(roster, agents)
	if err != nil {
		return AgentEntry{}, false, err
	}
	find := func(name string) (AgentEntry, bool) {
		for _, a := range crew {
			if sameAgentName(name, a.Name) {
				return a, true
			}
		}
		return AgentEntry{}, false
	}

	if explicit := strings.TrimSpace(ev.Props["agent"]); explicit != "" {
		if a, ok := find(explicit); ok {
			return a, false, nil
		}
		if len(roster) == 0 {
			return AgentEntry{}, false, fmt.Errorf("агент %q из свойства карточки не найден в реестре (%s)", explicit, agentNames(agents))
		}
		m.log.Info("acp: card agent is not on the column's crew, the crew decides",
			"card", ev.CardID, "agent", explicit)
	}

	// An assignee is a deliberate choice on the card, so it outranks the tags.
	for _, person := range ev.PersonNames {
		if a, ok := find(person); ok {
			return a, false, nil
		}
	}

	for _, opt := range ev.OptionNames {
		if a, ok := find(opt); ok {
			return a, false, nil
		}
	}

	if len(roster) > 0 {
		if a, ok := m.freeAgent(crew); ok {
			return a, false, nil
		}
		return AgentEntry{}, true, nil
	}

	if len(agents) == 1 {
		return agents[0], false, nil
	}
	if len(agents) > 1 {
		return AgentEntry{}, false, fmt.Errorf("не удалось выбрать агента: назначьте агента исполнителем карточки, задайте поле \"Agent\" или укажите состав колонки (доступно: %s)", agentNames(agents))
	}

	// Empty registry: fall back to the global AgentMode.
	kind := mode
	if kind == "" {
		kind = AgentKindClaude
	}
	if kind == agentModeCommand {
		// The mode names no kind of its own — the argv in the config is the
		// whole agent, which is exactly what the generic acp kind is.
		m.cfgMu.RLock()
		command := append([]string(nil), m.cfg.AgentCommand...)
		m.cfgMu.RUnlock()
		if len(command) == 0 {
			return AgentEntry{}, false, fmt.Errorf("agentMode = %q, но agentCommand пуст", agentModeCommand)
		}
		return AgentEntry{Name: kind, Kind: AgentKindACP, Command: command}, false, nil
	}
	return AgentEntry{Name: kind, Kind: kind}, false, nil
}

// crewOf resolves the names of a stage's crew against the registry. Unknown
// names are skipped — a registry edited after the column was configured should
// not stop the board — but a crew where nobody is left is a mistake worth
// reporting rather than quietly ignoring.
func crewOf(roster []string, agents []AgentEntry) ([]AgentEntry, error) {
	if len(roster) == 0 {
		return agents, nil
	}
	crew := make([]AgentEntry, 0, len(roster))
	for _, name := range roster {
		for _, a := range agents {
			if sameAgentName(name, a.Name) {
				crew = append(crew, a)
				break
			}
		}
	}
	if len(crew) == 0 {
		return nil, fmt.Errorf("состав колонки (%s) не найден в реестре агентов (%s)",
			strings.Join(roster, ", "), agentNames(agents))
	}
	return crew, nil
}

// freeAgent is the first member of the crew with no live session, in the order
// the crew was listed — so the choice is repeatable and the first name in the
// list is the one that normally works.
func (m *Manager) freeAgent(crew []AgentEntry) (AgentEntry, bool) {
	m.mu.Lock()
	busy := make(map[string]bool, len(m.active))
	for _, s := range m.active {
		busy[strings.ToLower(s.Agent.Name)] = true
	}
	m.mu.Unlock()

	for _, a := range crew {
		if !busy[strings.ToLower(a.Name)] {
			return a, true
		}
	}
	return AgentEntry{}, false
}

func agentNames(agents []AgentEntry) string {
	names := make([]string, len(agents))
	for i, a := range agents {
		names[i] = a.Name
	}
	return strings.Join(names, ", ")
}

// AssignedToHumanError is why a stage did not start: the card is somebody's.
// It is not a failure — the work is being done, only not by us — so the card
// waits where it stands rather than taking a failure branch.
type AssignedToHumanError struct {
	Who string
}

func (e AssignedToHumanError) Error() string {
	return fmt.Sprintf("карточка назначена на %s", e.Who)
}

// humanAssignee is who took the card, when that is a person rather than an
// agent. Assigning yourself is how you say "this one is mine": an agent picking
// the same card up would do the same work twice, and — on a route — would move
// the card on the moment it decided it was finished.
//
// An assignee that *is* a registered agent means the opposite (that agent runs
// it), so a card with one is not somebody's in this sense.
func humanAssignee(ev CardMoved, agents []AgentEntry) string {
	human := ""
	for _, person := range ev.PersonNames {
		person = strings.TrimSpace(person)
		if person == "" {
			continue
		}
		for _, a := range agents {
			if sameAgentName(person, a.Name) {
				return "" // an agent was assigned; it works
			}
		}
		if human == "" {
			human = person
		}
	}
	return human
}
