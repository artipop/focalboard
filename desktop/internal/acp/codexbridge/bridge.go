// Package codexbridge is an in-process ACP agent backed by the OpenAI `codex`
// binary's `codex exec --json` event stream. Like claudebridge it lets the rest
// of the app talk pure ACP (coder/acp-go-sdk) with the codex CLI as the only
// external dependency — no Node.js.
//
// Codex has no ACP mode, so this drives `codex exec` (one non-interactive turn
// per prompt) and translates its NDJSON "thread/turn/item" events into ACP
// session updates. Approval is governed by the sandbox/approval flags passed in
// Options.ExtraArgs, not by interactive per-tool permission round-trips.
//
// Wire-protocol facts (verified against the installed codex CLI, see NOTES.md):
//   - spawn: codex exec --json --skip-git-repo-check -C <cwd> [-m model] [args…] <prompt>
//   - events (top-level "type"): thread.started, turn.started, item.started,
//     item.updated, item.completed, turn.completed, turn.failed, error.
//   - item kinds ("item.type"): agent_message {text}, reasoning {text},
//     command_execution {command, aggregated_output, exit_code, status}, and
//     other tool-like items (file_change, mcp_tool_call, …).
package codexbridge

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	acp "github.com/coder/acp-go-sdk"

	"github.com/mattermost/focalboard/desktop/internal/procgroup"
)

// Options configures the bridge.
type Options struct {
	// Launch is the base argv of the codex CLI: the resolved binary, or a
	// wrapper command ending in it (e.g. `proxychains4 -f myproxy.conf codex`).
	// The bridge appends `exec --json …` after it.
	Launch []string
	// Model is passed as -m when non-empty.
	Model string
	// ExtraArgs are inserted before the prompt (e.g. --sandbox, --ask-for-approval).
	ExtraArgs []string
	// Env are "KEY=value" pairs injected into each codex process; DropEnv names
	// are removed from the inherited environment first so Env overrides them
	// (this is how per-agent CODEX_HOME/OPENAI_API_KEY isolate accounts).
	Env     []string
	DropEnv []string
	Logger  *slog.Logger
}

// Bridge implements acp.Agent by driving one `codex exec` subprocess per turn.
type Bridge struct {
	opts Options
	conn *acp.AgentSideConnection

	mu       sync.Mutex
	sessions map[acp.SessionId]*session
}

type session struct {
	id  acp.SessionId
	cwd string

	mu        sync.Mutex
	proc      *procgroup.Process
	cancelled bool
	threadID  string          // codex thread_id, set from thread.started; resumed on later turns
	turn      int             // 1-based turn counter, namespaces per-turn item ids
	started   map[string]bool // item id → StartToolCall already emitted (this turn)
}

// toolCallID namespaces a codex item id by turn: codex restarts item numbering
// at item_0 on every turn, so the raw id would collide across turns.
func (s *session) toolCallID(itemID string) acp.ToolCallId {
	return acp.ToolCallId(fmt.Sprintf("t%d-%s", s.turn, itemID))
}

// New creates a bridge. SetConn must be called before the first Prompt.
func New(opts Options) *Bridge {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	return &Bridge{opts: opts, sessions: make(map[acp.SessionId]*session)}
}

// SetConn wires the agent-side connection used to push session updates.
func (b *Bridge) SetConn(conn *acp.AgentSideConnection) { b.conn = conn }

// KillAll terminates every live codex subprocess (app shutdown path).
func (b *Bridge) KillAll(grace time.Duration) {
	b.mu.Lock()
	sessions := make([]*session, 0, len(b.sessions))
	for _, s := range b.sessions {
		sessions = append(sessions, s)
	}
	b.mu.Unlock()
	for _, s := range sessions {
		s.mu.Lock()
		proc := s.proc
		s.mu.Unlock()
		if proc != nil {
			proc.KillGroup(grace)
		}
	}
}

// ---- acp.Agent ----

func (b *Bridge) Initialize(ctx context.Context, params acp.InitializeRequest) (acp.InitializeResponse, error) {
	return acp.InitializeResponse{
		ProtocolVersion: acp.ProtocolVersionNumber,
		AgentInfo:       &acp.Implementation{Name: "focalboard-codex-bridge", Version: "0.1"},
	}, nil
}

func (b *Bridge) NewSession(ctx context.Context, params acp.NewSessionRequest) (acp.NewSessionResponse, error) {
	s := &session{id: acp.SessionId(uuid.NewString()), cwd: params.Cwd, started: map[string]bool{}}
	b.mu.Lock()
	b.sessions[s.id] = s
	b.mu.Unlock()
	return acp.NewSessionResponse{SessionId: s.id}, nil
}

func (b *Bridge) Cancel(ctx context.Context, params acp.CancelNotification) error {
	b.mu.Lock()
	s := b.sessions[params.SessionId]
	b.mu.Unlock()
	if s == nil {
		return nil
	}
	s.mu.Lock()
	s.cancelled = true
	proc := s.proc
	s.mu.Unlock()
	if proc != nil {
		go proc.KillGroup(2 * time.Second)
	}
	return nil
}

func (b *Bridge) Prompt(ctx context.Context, params acp.PromptRequest) (acp.PromptResponse, error) {
	b.mu.Lock()
	s := b.sessions[params.SessionId]
	b.mu.Unlock()
	if s == nil {
		return acp.PromptResponse{}, fmt.Errorf("unknown session %s", params.SessionId)
	}
	var text strings.Builder
	for _, block := range params.Prompt {
		if block.Text != nil {
			text.WriteString(block.Text.Text)
		}
	}
	return b.runTurn(ctx, s, text.String())
}

// Unused parts of the Agent surface.
func (b *Bridge) Authenticate(ctx context.Context, params acp.AuthenticateRequest) (acp.AuthenticateResponse, error) {
	return acp.AuthenticateResponse{}, nil
}
func (b *Bridge) Logout(ctx context.Context, params acp.LogoutRequest) (acp.LogoutResponse, error) {
	return acp.LogoutResponse{}, nil
}
func (b *Bridge) CloseSession(ctx context.Context, params acp.CloseSessionRequest) (acp.CloseSessionResponse, error) {
	return acp.CloseSessionResponse{}, acp.NewMethodNotFound(acp.AgentMethodSessionClose)
}
func (b *Bridge) ListSessions(ctx context.Context, params acp.ListSessionsRequest) (acp.ListSessionsResponse, error) {
	return acp.ListSessionsResponse{}, acp.NewMethodNotFound(acp.AgentMethodSessionList)
}
func (b *Bridge) ResumeSession(ctx context.Context, params acp.ResumeSessionRequest) (acp.ResumeSessionResponse, error) {
	return acp.ResumeSessionResponse{}, acp.NewMethodNotFound(acp.AgentMethodSessionResume)
}
func (b *Bridge) SetSessionConfigOption(ctx context.Context, params acp.SetSessionConfigOptionRequest) (acp.SetSessionConfigOptionResponse, error) {
	return acp.SetSessionConfigOptionResponse{}, acp.NewMethodNotFound(acp.AgentMethodSessionSetConfigOption)
}
func (b *Bridge) SetSessionMode(ctx context.Context, params acp.SetSessionModeRequest) (acp.SetSessionModeResponse, error) {
	return acp.SetSessionModeResponse{}, acp.NewMethodNotFound(acp.AgentMethodSessionSetMode)
}

// ---- codex exec turn ----

// runTurn spawns `codex exec --json`, feeds it the prompt as the final argument
// and translates the NDJSON stream into ACP updates until turn.completed.
// `codex exec` is one turn per process, so every turn after the first resumes
// the thread captured from thread.started.
func (b *Bridge) runTurn(ctx context.Context, s *session, prompt string) (acp.PromptResponse, error) {
	// Cancellation and item ids are scoped to a turn.
	s.mu.Lock()
	s.cancelled = false
	s.turn++
	s.started = map[string]bool{}
	threadID := s.threadID
	s.mu.Unlock()

	argv := append([]string(nil), b.opts.Launch...)
	if threadID != "" {
		// `exec resume` takes no -C; the cwd comes from the process itself.
		argv = append(argv, "exec", "resume", threadID, "--json", "--skip-git-repo-check")
	} else {
		argv = append(argv, "exec", "--json", "--skip-git-repo-check", "-C", s.cwd)
	}
	if b.opts.Model != "" {
		argv = append(argv, "-m", b.opts.Model)
	}
	argv = append(argv, b.opts.ExtraArgs...)
	argv = append(argv, prompt)

	proc, err := procgroup.Spawn(ctx, argv, s.cwd, b.opts.Env, b.opts.DropEnv...)
	if err != nil {
		return acp.PromptResponse{}, fmt.Errorf("spawn codex: %w", err)
	}
	// codex exec reads the prompt from argv; close stdin so it never blocks.
	_ = proc.Stdin.Close()
	s.mu.Lock()
	s.proc = proc
	s.mu.Unlock()
	defer func() {
		go proc.KillGroup(2 * time.Second)
		s.mu.Lock()
		s.proc = nil
		s.mu.Unlock()
	}()

	type turnResult struct {
		resp acp.PromptResponse
		err  error
	}
	resultCh := make(chan turnResult, 1)
	go func() {
		resp, err := b.consumeStream(ctx, s, proc.Stdout)
		resultCh <- turnResult{resp, err}
	}()

	select {
	case r := <-resultCh:
		if s.isCancelled() {
			return acp.PromptResponse{StopReason: acp.StopReasonCancelled}, nil
		}
		return r.resp, r.err
	case <-ctx.Done():
		proc.KillGroup(2 * time.Second)
		<-resultCh
		return acp.PromptResponse{StopReason: acp.StopReasonCancelled}, nil
	}
}

func (s *session) isCancelled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cancelled
}

// codexEvent is the subset of codex exec --json output the bridge cares about.
type codexEvent struct {
	Type     string     `json:"type"`
	ThreadID string     `json:"thread_id"` // thread.started
	Item     *codexItem `json:"item"`
	Error    *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// codexItem is one thread item (assistant message, reasoning, tool call, …).
type codexItem struct {
	ID            string `json:"id"`
	Type          string `json:"type"`
	Text          string `json:"text"`              // agent_message / reasoning
	Command       string `json:"command"`           // command_execution
	AggregatedOut string `json:"aggregated_output"` // command_execution
	ExitCode      *int   `json:"exit_code"`         // command_execution
	Status        string `json:"status"`            // in_progress / completed / failed
}

func (b *Bridge) consumeStream(ctx context.Context, s *session, stdout io.Reader) (acp.PromptResponse, error) {
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 1024*1024), 32*1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var ev codexEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			b.opts.Logger.Warn("codexbridge: unparsable line", "err", err)
			continue
		}
		switch ev.Type {
		case "thread.started":
			// Recorded on the first turn and replayed as `exec resume <id>`; a
			// resumed turn re-announces the same id.
			if ev.ThreadID != "" {
				s.mu.Lock()
				s.threadID = ev.ThreadID
				s.mu.Unlock()
			}
		case "item.started", "item.updated", "item.completed":
			b.handleItem(ctx, s, ev.Type, ev.Item)
		case "turn.completed":
			return acp.PromptResponse{StopReason: acp.StopReasonEndTurn}, nil
		case "turn.failed", "error":
			msg := "codex reported an error"
			if ev.Error != nil && ev.Error.Message != "" {
				msg = ev.Error.Message
			}
			return acp.PromptResponse{}, fmt.Errorf("%s", truncate(msg, 2000))
		}
	}
	if err := scanner.Err(); err != nil {
		return acp.PromptResponse{}, fmt.Errorf("read codex stream: %w", err)
	}
	if s.isCancelled() || ctx.Err() != nil {
		return acp.PromptResponse{StopReason: acp.StopReasonCancelled}, nil
	}
	// Some codex versions exit without an explicit turn.completed; treat a clean
	// EOF as end of turn (the final text was already streamed via agent_message).
	return acp.PromptResponse{StopReason: acp.StopReasonEndTurn}, nil
}

// handleItem translates a thread item into ACP session updates. agent_message
// and reasoning become text/thought; everything else is surfaced as a tool call.
func (b *Bridge) handleItem(ctx context.Context, s *session, phase string, item *codexItem) {
	if item == nil {
		return
	}
	switch item.Type {
	case "agent_message":
		if phase == "item.completed" && item.Text != "" {
			_ = b.conn.SessionUpdate(ctx, acp.SessionNotification{
				SessionId: s.id,
				Update:    acp.UpdateAgentMessageText(item.Text),
			})
		}
	case "reasoning":
		if phase == "item.completed" && item.Text != "" {
			_ = b.conn.SessionUpdate(ctx, acp.SessionNotification{
				SessionId: s.id,
				Update:    acp.UpdateAgentThoughtText(item.Text),
			})
		}
	default:
		b.handleToolItem(ctx, s, phase, item)
	}
}

func (b *Bridge) handleToolItem(ctx context.Context, s *session, phase string, item *codexItem) {
	if item.ID == "" {
		return
	}
	title := item.Type
	if item.Command != "" {
		title = item.Command
	}

	s.mu.Lock()
	started := s.started[item.ID]
	if !started {
		s.started[item.ID] = true
	}
	toolCallID := s.toolCallID(item.ID)
	s.mu.Unlock()

	if !started {
		_ = b.conn.SessionUpdate(ctx, acp.SessionNotification{
			SessionId: s.id,
			Update: acp.StartToolCall(
				toolCallID,
				title,
				acp.WithStartStatus(acp.ToolCallStatusInProgress),
			),
		})
	}

	if phase == "item.completed" {
		status := acp.ToolCallStatusCompleted
		if item.Status == "failed" || (item.ExitCode != nil && *item.ExitCode != 0) {
			status = acp.ToolCallStatusFailed
		}
		_ = b.conn.SessionUpdate(ctx, acp.SessionNotification{
			SessionId: s.id,
			Update: acp.UpdateToolCall(
				toolCallID,
				acp.WithUpdateStatus(status),
			),
		})
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
