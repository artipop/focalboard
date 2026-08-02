package acp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/mattermost/focalboard/desktop/internal/procgroup"
)

// What an agent can be told to do beyond "here is the task" differs from agent
// to agent, and from version to version of the same one: claude offers Fast
// mode, an effort level and a permission mode; codex offers a mode and a model
// and neither of the other two. Which is why none of it is a table of ours —
// ACP already has the answer. Every agent lists its settings in the response to
// session/new (configOptions) and takes them back through
// session/set_config_option, so the registry stores what the user chose and the
// dialog offers exactly what the agent said it has. An agent without Fast mode
// offers no Fast mode switch, and one that grows a new setting shows it here
// without a line of code changing.

// AgentOption is one setting an agent declares, in the shape the dialog draws.
type AgentOption struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// Type is "select" (one of Values) or "boolean" (on/off).
	Type string `json:"type"`
	// Category is the agent's own hint about what the setting is for ("mode",
	// "model", "thought_level", …); UX only.
	Category string `json:"category,omitempty"`
	// Current is what the agent starts with, as a value id or "true"/"false".
	Current string             `json:"current"`
	Values  []AgentOptionValue `json:"values,omitempty"`
}

// AgentOptionValue is one selectable value of a select option.
type AgentOptionValue struct {
	Value       string `json:"value"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
}

// Option types, as ACP spells them.
const (
	agentOptionSelect  = "select"
	agentOptionBoolean = "boolean"
)

// probeTimeout bounds asking an agent what it can do. It is generous because
// the first run of an adapter fetched through npx downloads it first, and the
// alternative to waiting is a dialog that says the agent has no settings.
const probeTimeout = 3 * time.Minute

// AgentOptions asks the agent itself which settings it has: it is started the
// way a session would start it, a throwaway session is opened, its configOptions
// are read, and the process is killed. Nothing is prompted, so the agent does no
// work and costs nothing beyond its own startup.
//
// The answer is cached per launch configuration, since the dialog asks whenever
// a form is opened and starting an agent takes seconds; refresh skips the cache,
// which is the "recheck" button (an account changed, an adapter was updated).
func (m *Manager) AgentOptions(a AgentEntry, refresh bool) ([]AgentOption, error) {
	// The form asks before the entry has a name — an agent is chosen and then
	// named — and what it is called has no bearing on what it can do.
	if strings.TrimSpace(a.Name) == "" {
		a.Name = "?"
	}
	a, err := validateAgent(a)
	if err != nil {
		return nil, err
	}
	key := agentLaunchKey(a)
	if !refresh {
		if cached, ok := m.cachedOptions(key); ok {
			return cached, nil
		}
	}
	options, err := m.probeAgentOptions(a)
	if err != nil {
		return nil, err
	}
	m.cacheOptions(key, options)
	return options, nil
}

func (m *Manager) cachedOptions(key string) ([]AgentOption, bool) {
	m.optionsMu.Lock()
	defer m.optionsMu.Unlock()
	options, ok := m.optionsCache[key]
	return options, ok
}

func (m *Manager) cacheOptions(key string, options []AgentOption) {
	m.optionsMu.Lock()
	defer m.optionsMu.Unlock()
	if m.optionsCache == nil {
		m.optionsCache = map[string][]AgentOption{}
	}
	m.optionsCache[key] = options
}

// agentLaunchKey fingerprints everything that decides which agent process the
// probe would start and what it would answer — the binary, the flags, the
// account and network it runs under. The name is deliberately absent: renaming
// an entry changes nothing about the agent.
func agentLaunchKey(a AgentEntry) string {
	h := sha256.New()
	write := func(parts ...string) {
		for _, p := range parts {
			fmt.Fprintf(h, "%q\x00", p)
		}
	}
	write(a.Kind, a.BinPath, a.Model, a.ProxyName)
	write(a.Command...)
	write(a.Args...)
	keys := make([]string, 0, len(a.Env))
	for k := range a.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		write(k, a.Env[k])
	}
	return hex.EncodeToString(h.Sum(nil))
}

// probeAgentOptions starts the agent, opens a session in a scratch directory
// and reads what it says it can be configured with.
func (m *Manager) probeAgentOptions(a AgentEntry) ([]AgentOption, error) {
	launch, err := m.agentLaunch(a)
	if err != nil {
		return nil, err
	}
	net, err := m.resolveNetwork(a)
	if err != nil {
		return nil, err
	}
	// A directory of its own rather than a repository: the agent is asked what
	// it can do, not pointed at anything it might touch.
	cwd, err := os.MkdirTemp("", "acp-probe-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(cwd)

	parent := m.rootCtx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, probeTimeout)
	defer cancel()

	env, drop := spawnEnv(a, net)
	env = append(append([]string{}, launch.env...), env...)
	drop = append(drop, launch.dropEnv...)
	argv := resolveArgv0(launch.argv)
	proc, err := procgroup.Spawn(ctx, argv, cwd, env, drop...)
	if err != nil {
		return nil, fmt.Errorf("не удалось запустить агента %q: %w", argv[0], err)
	}
	defer func() {
		proc.KillGroup(2 * time.Second)
		_ = proc.Wait()
	}()

	conn := acpsdk.NewClientSideConnection(probeClient{}, proc.Stdin, proc.Stdout)
	conn.SetLogger(m.sdkLogger("probe"))
	if _, err := conn.Initialize(ctx, acpsdk.InitializeRequest{
		ProtocolVersion: acpsdk.ProtocolVersionNumber,
		ClientCapabilities: acpsdk.ClientCapabilities{
			Fs: acpsdk.FileSystemCapabilities{ReadTextFile: true, WriteTextFile: true},
		},
	}); err != nil {
		return nil, fmt.Errorf("initialize: %s", net.redactProxySecret(err.Error()))
	}
	// No MCP servers, but the list itself is required: an agent asked for a
	// session with none at all rejects a missing field outright.
	sess, err := conn.NewSession(ctx, acpsdk.NewSessionRequest{
		Cwd:        cwd,
		McpServers: []acpsdk.McpServer{},
		// The same hand-over a real session makes, so an argument the CLI does
		// not know is reported while the agent is being edited rather than on
		// the first card it takes.
		Meta: sessionMeta(a),
	})
	if err != nil {
		return nil, fmt.Errorf("session/new: %s", net.redactProxySecret(err.Error()))
	}
	return agentOptions(sess.ConfigOptions), nil
}

// agentOptions converts what the agent declared into what the dialog draws. An
// option in a shape we have no control for is dropped rather than shown as a
// setting that cannot be set.
func agentOptions(options []acpsdk.SessionConfigOption) []AgentOption {
	out := make([]AgentOption, 0, len(options))
	for _, opt := range options {
		switch {
		case opt.Select != nil:
			sel := opt.Select
			values := make([]AgentOptionValue, 0, 4)
			for _, v := range configSelectOptions(sel.Options) {
				values = append(values, AgentOptionValue{
					Value:       string(v.Value),
					Name:        v.Name,
					Description: derefString(v.Description),
				})
			}
			out = append(out, AgentOption{
				ID:          string(sel.Id),
				Name:        sel.Name,
				Description: derefString(sel.Description),
				Type:        agentOptionSelect,
				Category:    categoryName(sel.Category),
				Current:     string(sel.CurrentValue),
				Values:      values,
			})
		case opt.Boolean != nil:
			b := opt.Boolean
			out = append(out, AgentOption{
				ID:          string(b.Id),
				Name:        b.Name,
				Description: derefString(b.Description),
				Type:        agentOptionBoolean,
				Category:    categoryName(b.Category),
				Current:     strconv.FormatBool(b.CurrentValue),
			})
		}
	}
	return out
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func categoryName(c *acpsdk.SessionConfigOptionCategory) string {
	if c == nil {
		return ""
	}
	return string(*c)
}

// applyAgentOptions sets whatever the agent's registry entry asks for, among
// the settings the agent declared in this session.
//
// It runs after the kind's own mode and model, so an option the user chose by
// hand wins over the one the table would have set — asking for codex's
// read-only mode here means read-only, not "agent".
//
// Advisory throughout, like the model: a setting the agent no longer offers is
// logged and skipped, because the turn works without it and failing a card over
// a switch would be worse than running with the agent's own default.
func (m *Manager) applyAgentOptions(ctx context.Context, s *Session, conn *acpsdk.ClientSideConnection, sess acpsdk.NewSessionResponse) {
	if len(s.Agent.Options) == 0 {
		return
	}
	// Sorted, so a config with several settings applies in the same order every
	// time and the debug log reads the same twice.
	ids := make([]string, 0, len(s.Agent.Options))
	for id := range s.Agent.Options {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	for _, id := range ids {
		want := strings.TrimSpace(s.Agent.Options[id])
		if want == "" {
			continue // "leave it as the agent has it"
		}
		req, err := configRequest(sess, id, want)
		if err != nil {
			m.log.Warn("acp: agent setting not applied", "session", s.ID,
				"option", id, "value", want, "err", err)
			continue
		}
		if _, err := conn.SetSessionConfigOption(ctx, *req); err != nil {
			m.log.Warn("acp: agent refused a setting", "session", s.ID,
				"option", id, "value", want, "err", err)
		}
	}
}

// configRequest builds the call that sets option id to want, or reports why it
// cannot.
//
// It never skips a call because the agent "already has that value": the only
// account of the current value is the one session/new gave, and the mode and
// the model have been set since — so a setting that reads as current may be the
// one we just changed. Setting a value the agent already holds costs one
// message; not setting one it no longer holds is the bug this had.
func configRequest(sess acpsdk.NewSessionResponse, id, want string) (*acpsdk.SetSessionConfigOptionRequest, error) {
	for _, opt := range sess.ConfigOptions {
		switch {
		case opt.Select != nil && string(opt.Select.Id) == id:
			sel := opt.Select
			value, ok := matchConfigValue(sel.Options, want)
			if !ok {
				return nil, fmt.Errorf("агент не предлагает значения %q (есть: %s)", want, configValueIDs(sel.Options))
			}
			return &acpsdk.SetSessionConfigOptionRequest{
				ValueId: &acpsdk.SetSessionConfigOptionValueId{
					SessionId: sess.SessionId,
					ConfigId:  sel.Id,
					Value:     acpsdk.SessionConfigValueId(value),
				},
			}, nil

		case opt.Boolean != nil && string(opt.Boolean.Id) == id:
			b := opt.Boolean
			value, err := parseOptionBool(want)
			if err != nil {
				return nil, err
			}
			return &acpsdk.SetSessionConfigOptionRequest{
				Boolean: &acpsdk.SetSessionConfigOptionBoolean{
					SessionId: sess.SessionId,
					ConfigId:  b.Id,
					Value:     value,
				},
			}, nil
		}
	}
	return nil, fmt.Errorf("агент не предлагает такой настройки")
}

// parseOptionBool reads a toggle written any of the ways it is stored: the
// config file is edited by hand as well as by the dialog, and the same switch
// is spelled "on" by one agent and true by another.
func parseOptionBool(value string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "on", "yes", "1", "enabled":
		return true, nil
	case "false", "off", "no", "0", "disabled":
		return false, nil
	}
	return false, fmt.Errorf("значение %q не похоже на переключатель (on/off)", value)
}

// probeClient answers the agent during a capability probe. Nothing is prompted,
// so nothing should reach any of these — an agent that asks anyway is told the
// client cannot help rather than left waiting.
type probeClient struct{}

var _ acpsdk.Client = probeClient{}

func (probeClient) SessionUpdate(ctx context.Context, params acpsdk.SessionNotification) error {
	return nil
}

func (probeClient) RequestPermission(ctx context.Context, params acpsdk.RequestPermissionRequest) (acpsdk.RequestPermissionResponse, error) {
	return acpsdk.RequestPermissionResponse{
		Outcome: acpsdk.RequestPermissionOutcome{Cancelled: &acpsdk.RequestPermissionOutcomeCancelled{}},
	}, nil
}

func (probeClient) ReadTextFile(ctx context.Context, params acpsdk.ReadTextFileRequest) (acpsdk.ReadTextFileResponse, error) {
	return acpsdk.ReadTextFileResponse{}, fmt.Errorf("filesystem not available while probing the agent")
}

func (probeClient) WriteTextFile(ctx context.Context, params acpsdk.WriteTextFileRequest) (acpsdk.WriteTextFileResponse, error) {
	return acpsdk.WriteTextFileResponse{}, fmt.Errorf("filesystem not available while probing the agent")
}

func (probeClient) CreateTerminal(ctx context.Context, params acpsdk.CreateTerminalRequest) (acpsdk.CreateTerminalResponse, error) {
	return acpsdk.CreateTerminalResponse{}, fmt.Errorf("terminal not supported")
}

func (probeClient) KillTerminal(ctx context.Context, params acpsdk.KillTerminalRequest) (acpsdk.KillTerminalResponse, error) {
	return acpsdk.KillTerminalResponse{}, fmt.Errorf("terminal not supported")
}

func (probeClient) TerminalOutput(ctx context.Context, params acpsdk.TerminalOutputRequest) (acpsdk.TerminalOutputResponse, error) {
	return acpsdk.TerminalOutputResponse{}, fmt.Errorf("terminal not supported")
}

func (probeClient) ReleaseTerminal(ctx context.Context, params acpsdk.ReleaseTerminalRequest) (acpsdk.ReleaseTerminalResponse, error) {
	return acpsdk.ReleaseTerminalResponse{}, fmt.Errorf("terminal not supported")
}

func (probeClient) WaitForTerminalExit(ctx context.Context, params acpsdk.WaitForTerminalExitRequest) (acpsdk.WaitForTerminalExitResponse, error) {
	return acpsdk.WaitForTerminalExitResponse{}, fmt.Errorf("terminal not supported")
}
