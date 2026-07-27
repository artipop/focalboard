// Package claudebridge is an in-process ACP agent backed by the `claude`
// binary's native stream-json stdio protocol. It lets the rest of the app talk
// pure ACP (coder/acp-go-sdk) while the only external dependency is the claude
// CLI itself — no Node.js adapter.
//
// Wire-protocol facts (verified against claude 2.1.218/2.1.220, see
// cmd/acpspike/NOTES.md):
//   - spawn: claude --input-format stream-json --output-format stream-json
//     --verbose --include-partial-messages --permission-prompt-tool stdio -p
//   - permission requests arrive as {"type":"control_request","request":
//     {"subtype":"can_use_tool",...}}; the response envelope must nest
//     request_id INSIDE "response", otherwise the CLI hangs silently.
//   - the CLI does not exit after a turn's terminal "result" line: writing
//     another user message into the same stdin starts the next turn with the
//     conversation intact, so one subprocess serves the whole session.
package claudebridge

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
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
	// Launch is the base argv of the claude CLI: the resolved binary, or a
	// wrapper command ending in it (e.g. `proxychains4 -f myproxy.conf claude`).
	// The bridge appends its own stream-json flags after it.
	Launch []string
	// ExtraArgs are appended to the claude invocation (e.g. --model).
	ExtraArgs []string
	// Env are "KEY=value" pairs injected into each claude process; DropEnv names
	// are removed from the inherited environment first so Env overrides them
	// (per-agent CLAUDE_CONFIG_DIR/ANTHROPIC_API_KEY isolate accounts).
	Env     []string
	DropEnv []string
	Logger  *slog.Logger
}

// Bridge implements acp.Agent by driving one claude subprocess per session.
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
	h         *procHandle
	started   bool // a subprocess has already run for this session id
	cancelled bool
}

// procHandle is one live claude subprocess together with the reader and writer
// state bound to it. Every turn of a session shares the same handle: the
// scanner must be created once per subprocess, since it buffers ahead of the
// lines it has returned and a fresh scanner would drop what it already read.
type procHandle struct {
	proc     *procgroup.Process
	scanner  *bufio.Scanner
	stdinMu  sync.Mutex
	stopOnce sync.Once
}

// stop tears the subprocess down exactly once. Cancellation and the turn loop
// can both reach a dying handle, and Cmd.Wait must not be called twice.
func (h *procHandle) stop(grace time.Duration) {
	h.stopOnce.Do(func() {
		h.proc.KillGroup(grace)
		// Reap: nobody else owns Wait, and an interactive session may respawn
		// several times over its life.
		go func() { _ = h.proc.Wait() }()
	})
}

// writeLine sends one NDJSON message to the CLI. Turns and the permission
// answering goroutine both write, hence the mutex.
func (h *procHandle) writeLine(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	h.stdinMu.Lock()
	defer h.stdinMu.Unlock()
	_, err = h.proc.Stdin.Write(append(b, '\n'))
	return err
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

// KillAll terminates every live claude subprocess (app shutdown path).
func (b *Bridge) KillAll(grace time.Duration) {
	b.mu.Lock()
	sessions := make([]*session, 0, len(b.sessions))
	for _, s := range b.sessions {
		sessions = append(sessions, s)
	}
	b.mu.Unlock()
	for _, s := range sessions {
		if h := s.takeHandle(); h != nil {
			h.stop(grace)
		}
	}
}

// takeHandle detaches the session's subprocess handle, returning it to the
// caller for teardown. Returns nil when no subprocess is live.
func (s *session) takeHandle() *procHandle {
	s.mu.Lock()
	defer s.mu.Unlock()
	h := s.h
	s.h = nil
	return h
}

// dropHandle tears down h and detaches it, but only if it is still the
// session's current subprocess — a later turn may already have respawned one.
// The next turn resumes the conversation instead of starting a fresh one.
func (s *session) dropHandle(h *procHandle) {
	s.mu.Lock()
	if s.h == h {
		s.h = nil
	}
	s.mu.Unlock()
	h.stop(2 * time.Second)
}

// ---- acp.Agent ----

func (b *Bridge) Initialize(ctx context.Context, params acp.InitializeRequest) (acp.InitializeResponse, error) {
	return acp.InitializeResponse{
		ProtocolVersion: acp.ProtocolVersionNumber,
		AgentInfo:       &acp.Implementation{Name: "focalboard-claude-bridge", Version: "0.1"},
	}, nil
}

func (b *Bridge) NewSession(ctx context.Context, params acp.NewSessionRequest) (acp.NewSessionResponse, error) {
	s := &session{id: acp.SessionId(uuid.NewString()), cwd: params.Cwd}
	b.mu.Lock()
	b.sessions[s.id] = s
	b.mu.Unlock()
	return acp.NewSessionResponse{SessionId: s.id}, nil
}

// Cancel is invoked by the SDK when the client sends session/cancel. The
// prompt context is cancelled by the SDK as well; killing the process here
// guarantees the 2-second stop. Killing is the only way to interrupt the CLI
// mid-turn, so the conversation continues in a resumed subprocess next turn.
func (b *Bridge) Cancel(ctx context.Context, params acp.CancelNotification) error {
	b.mu.Lock()
	s := b.sessions[params.SessionId]
	b.mu.Unlock()
	if s == nil {
		return nil
	}
	s.mu.Lock()
	s.cancelled = true
	h := s.h
	s.mu.Unlock()
	if h != nil {
		go s.dropHandle(h)
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

// ---- claude stream-json turn ----

// ensureProc returns the session's live subprocess, spawning one if needed.
// The ACP session id is a UUID the bridge itself minted in NewSession, so the
// CLI can be told to adopt it up front (--session-id) and to resume it after a
// respawn (--resume) — a cancelled or crashed turn therefore does not cost the
// conversation history.
func (b *Bridge) ensureProc(ctx context.Context, s *session) (*procHandle, error) {
	s.mu.Lock()
	if h := s.h; h != nil {
		s.mu.Unlock()
		return h, nil
	}
	resume := s.started
	s.mu.Unlock()

	argv := append(append([]string(nil), b.opts.Launch...),
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--verbose",
		"--include-partial-messages",
		"--permission-prompt-tool", "stdio",
		"-p",
	)
	if resume {
		argv = append(argv, "--resume", string(s.id))
	} else {
		argv = append(argv, "--session-id", string(s.id))
	}
	argv = append(argv, b.opts.ExtraArgs...)

	// CLAUDECODE in the environment triggers the CLI's nested-session guard.
	dropEnv := append([]string{"CLAUDECODE"}, b.opts.DropEnv...)
	// The subprocess outlives the turn that spawned it; teardown goes through
	// dropHandle/KillAll, never through the turn context.
	proc, err := procgroup.Spawn(context.WithoutCancel(ctx), argv, s.cwd, b.opts.Env, dropEnv...)
	if err != nil {
		return nil, fmt.Errorf("spawn claude: %w", err)
	}
	scanner := bufio.NewScanner(proc.Stdout)
	scanner.Buffer(make([]byte, 0, 1024*1024), 32*1024*1024)
	h := &procHandle{proc: proc, scanner: scanner}

	s.mu.Lock()
	s.h = h
	s.started = true
	s.mu.Unlock()
	return h, nil
}

// runTurn feeds the prompt to the session's claude subprocess and translates
// the NDJSON stream into ACP session updates until the terminal "result"
// message. The subprocess is left running for the next turn.
func (b *Bridge) runTurn(ctx context.Context, s *session, prompt string) (acp.PromptResponse, error) {
	// Cancellation is scoped to a turn: a session cancelled earlier must not
	// report every later turn as cancelled too.
	s.mu.Lock()
	s.cancelled = false
	s.mu.Unlock()

	h, err := b.ensureProc(ctx, s)
	if err != nil {
		return acp.PromptResponse{}, err
	}

	userMsg := map[string]any{
		"type": "user",
		"message": map[string]any{
			"role":    "user",
			"content": []map[string]any{{"type": "text", "text": prompt}},
		},
	}
	if err := h.writeLine(userMsg); err != nil {
		s.dropHandle(h)
		return acp.PromptResponse{}, fmt.Errorf("send prompt: %w", err)
	}

	type turnResult struct {
		resp acp.PromptResponse
		err  error
	}
	resultCh := make(chan turnResult, 1)
	go func() {
		resp, err := b.consumeStream(ctx, s, h)
		resultCh <- turnResult{resp, err}
	}()

	select {
	case r := <-resultCh:
		if r.err != nil {
			// The stream ended without a result: the CLI died or closed stdout,
			// so the handle is useless. The next turn respawns with --resume.
			s.dropHandle(h)
		}
		if s.isCancelled() {
			return acp.PromptResponse{StopReason: acp.StopReasonCancelled}, nil
		}
		return r.resp, r.err
	case <-ctx.Done():
		// session/cancel or turn timeout: killing is the only way to interrupt
		// the CLI mid-turn.
		s.dropHandle(h)
		<-resultCh
		return acp.PromptResponse{StopReason: acp.StopReasonCancelled}, nil
	}
}

func (s *session) isCancelled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cancelled
}

// streamLine is the subset of claude's NDJSON output the bridge cares about.
type streamLine struct {
	Type    string `json:"type"`
	Subtype string `json:"subtype"`
	IsError bool   `json:"is_error"`
	Result  string `json:"result"`
	Event   *struct {
		Type         string `json:"type"`
		Index        int    `json:"index"`
		ContentBlock *struct {
			Type string `json:"type"`
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"content_block"`
		Delta *struct {
			Type     string `json:"type"`
			Text     string `json:"text"`
			Thinking string `json:"thinking"`
		} `json:"delta"`
	} `json:"event"`
	Message *struct {
		Role    string            `json:"role"`
		Content []json.RawMessage `json:"content"`
	} `json:"message"`
	RequestID any          `json:"request_id"`
	Request   *permRequest `json:"request"`
}

// permRequest is the payload of a can_use_tool control request.
type permRequest struct {
	Subtype     string          `json:"subtype"`
	ToolName    string          `json:"tool_name"`
	DisplayName string          `json:"display_name"`
	Description string          `json:"description"`
	Input       json.RawMessage `json:"input"`
	ToolUseID   string          `json:"tool_use_id"`
}

// consumeStream reads one turn off the session's subprocess. The scanner lives
// on the handle, so leftovers buffered past this turn's "result" line stay
// available to the next turn.
func (b *Bridge) consumeStream(ctx context.Context, s *session, h *procHandle) (acp.PromptResponse, error) {
	scanner := h.scanner

	// index → tool call id for in-flight tool_use content blocks.
	toolByIndex := map[int]string{}

	for scanner.Scan() {
		line := scanner.Bytes()
		var msg streamLine
		if err := json.Unmarshal(line, &msg); err != nil {
			b.opts.Logger.Warn("claudebridge: unparsable line", "err", err)
			continue
		}
		switch msg.Type {
		case "stream_event":
			b.handleStreamEvent(ctx, s, &msg, toolByIndex)
		case "assistant":
			b.handleAssistant(ctx, s, &msg)
		case "user":
			b.handleToolResults(ctx, s, &msg)
		case "control_request":
			if msg.Request != nil && msg.Request.Subtype == "can_use_tool" {
				// Answer asynchronously: the client may take minutes to decide
				// and the CLI keeps streaming independently.
				req := *msg.Request
				reqID := msg.RequestID
				go b.answerPermission(ctx, s, reqID, req, h.writeLine)
			}
		case "result":
			if msg.IsError {
				return acp.PromptResponse{}, fmt.Errorf("claude reported an error: %s", truncate(msg.Result, 2000))
			}
			return acp.PromptResponse{StopReason: acp.StopReasonEndTurn}, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return acp.PromptResponse{}, fmt.Errorf("read claude stream: %w", err)
	}
	if s.isCancelled() || ctx.Err() != nil {
		return acp.PromptResponse{StopReason: acp.StopReasonCancelled}, nil
	}
	return acp.PromptResponse{}, fmt.Errorf("claude exited without a result message")
}

func (b *Bridge) handleStreamEvent(ctx context.Context, s *session, msg *streamLine, toolByIndex map[int]string) {
	ev := msg.Event
	if ev == nil {
		return
	}
	switch ev.Type {
	case "content_block_start":
		if ev.ContentBlock != nil && ev.ContentBlock.Type == "tool_use" {
			toolByIndex[ev.Index] = ev.ContentBlock.ID
			_ = b.conn.SessionUpdate(ctx, acp.SessionNotification{
				SessionId: s.id,
				Update: acp.StartToolCall(
					acp.ToolCallId(ev.ContentBlock.ID),
					ev.ContentBlock.Name,
					acp.WithStartStatus(acp.ToolCallStatusPending),
				),
			})
		}
	case "content_block_delta":
		if ev.Delta == nil {
			return
		}
		switch ev.Delta.Type {
		case "text_delta":
			if ev.Delta.Text != "" {
				_ = b.conn.SessionUpdate(ctx, acp.SessionNotification{
					SessionId: s.id,
					Update:    acp.UpdateAgentMessageText(ev.Delta.Text),
				})
			}
		case "thinking_delta":
			if ev.Delta.Thinking != "" {
				_ = b.conn.SessionUpdate(ctx, acp.SessionNotification{
					SessionId: s.id,
					Update:    acp.UpdateAgentThoughtText(ev.Delta.Thinking),
				})
			}
		}
	}
}

// assistantContent is one aggregated content block of an assistant message.
type assistantContent struct {
	Type  string          `json:"type"`
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// handleAssistant upgrades pending tool calls with their full input once the
// aggregated assistant message arrives.
func (b *Bridge) handleAssistant(ctx context.Context, s *session, msg *streamLine) {
	if msg.Message == nil {
		return
	}
	for _, raw := range msg.Message.Content {
		var c assistantContent
		if err := json.Unmarshal(raw, &c); err != nil || c.Type != "tool_use" {
			continue
		}
		var input any
		_ = json.Unmarshal(c.Input, &input)
		_ = b.conn.SessionUpdate(ctx, acp.SessionNotification{
			SessionId: s.id,
			Update: acp.UpdateToolCall(
				acp.ToolCallId(c.ID),
				acp.WithUpdateStatus(acp.ToolCallStatusInProgress),
				acp.WithUpdateRawInput(input),
			),
		})
	}
}

// toolResultContent is a tool_result block inside a user message.
type toolResultContent struct {
	Type      string `json:"type"`
	ToolUseID string `json:"tool_use_id"`
	IsError   bool   `json:"is_error"`
}

func (b *Bridge) handleToolResults(ctx context.Context, s *session, msg *streamLine) {
	if msg.Message == nil {
		return
	}
	for _, raw := range msg.Message.Content {
		var c toolResultContent
		if err := json.Unmarshal(raw, &c); err != nil || c.Type != "tool_result" {
			continue
		}
		status := acp.ToolCallStatusCompleted
		if c.IsError {
			status = acp.ToolCallStatusFailed
		}
		_ = b.conn.SessionUpdate(ctx, acp.SessionNotification{
			SessionId: s.id,
			Update: acp.UpdateToolCall(
				acp.ToolCallId(c.ToolUseID),
				acp.WithUpdateStatus(status),
			),
		})
	}
}

// Permission option ids the bridge understands.
const (
	optAllow       = "allow"
	optAllowAlways = "allow_always"
	optReject      = "reject"
)

func (b *Bridge) answerPermission(ctx context.Context, s *session, reqID any, req permRequest, writeLine func(any) error) {
	var input any
	_ = json.Unmarshal(req.Input, &input)
	title := req.ToolName
	if req.Description != "" {
		title = fmt.Sprintf("%s: %s", req.ToolName, req.Description)
	}
	resp, err := b.conn.RequestPermission(ctx, acp.RequestPermissionRequest{
		SessionId: s.id,
		ToolCall: acp.ToolCallUpdate{
			ToolCallId: acp.ToolCallId(req.ToolUseID),
			Title:      acp.Ptr(title),
			Status:     acp.Ptr(acp.ToolCallStatusPending),
			RawInput:   input,
			Meta:       map[string]any{"toolName": req.ToolName},
		},
		Options: []acp.PermissionOption{
			{Kind: acp.PermissionOptionKindAllowOnce, Name: "Allow once", OptionId: acp.PermissionOptionId(optAllow)},
			{Kind: acp.PermissionOptionKindAllowAlways, Name: "Allow for this session", OptionId: acp.PermissionOptionId(optAllowAlways)},
			{Kind: acp.PermissionOptionKindRejectOnce, Name: "Reject", OptionId: acp.PermissionOptionId(optReject)},
		},
	})

	allow := false
	if err == nil && resp.Outcome.Selected != nil {
		id := string(resp.Outcome.Selected.OptionId)
		allow = id == optAllow || id == optAllowAlways
	}

	var payload map[string]any
	if allow {
		payload = map[string]any{"behavior": "allow", "updatedInput": json.RawMessage(req.Input)}
	} else {
		payload = map[string]any{"behavior": "deny", "message": "Permission denied by Focalboard agent policy"}
	}
	// request_id must be nested inside "response" (see package doc).
	out := map[string]any{
		"type": "control_response",
		"response": map[string]any{
			"subtype":    "success",
			"request_id": reqID,
			"response":   payload,
		},
	}
	if err := writeLine(out); err != nil {
		b.opts.Logger.Warn("claudebridge: failed to answer permission", "err", err)
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
