package acp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"
	"github.com/google/uuid"
)

// chunkFlushDelay batches streamed text before it reaches the UI: agents emit
// a token at a time, and one UI event per token would swamp the event bus.
const chunkFlushDelay = 60 * time.Millisecond

// sessionClient implements acpsdk.Client for one session: it receives the
// agent's stream, persists it, forwards it to the UI and answers permission
// requests according to policy.
type sessionClient struct {
	m *Manager
	s *Session

	chunkMu      sync.Mutex
	chunkBuf     strings.Builder
	chunkThought bool
	chunkTimer   *time.Timer
}

// emitChunk queues streamed text for the UI, flushing on a short timer or as
// soon as the kind of text changes (agent output vs. thinking).
func (c *sessionClient) emitChunk(text string, thought bool) {
	c.chunkMu.Lock()
	if c.chunkBuf.Len() > 0 && c.chunkThought != thought {
		c.flushLocked()
	}
	c.chunkThought = thought
	c.chunkBuf.WriteString(text)
	if c.chunkTimer == nil {
		c.chunkTimer = time.AfterFunc(chunkFlushDelay, c.flush)
	}
	c.chunkMu.Unlock()
}

func (c *sessionClient) flush() {
	c.chunkMu.Lock()
	c.flushLocked()
	c.chunkMu.Unlock()
}

// flushLocked emits whatever is buffered. Callers hold chunkMu.
func (c *sessionClient) flushLocked() {
	if c.chunkTimer != nil {
		c.chunkTimer.Stop()
		c.chunkTimer = nil
	}
	if c.chunkBuf.Len() == 0 {
		return
	}
	payload := map[string]any{
		"sessionId": c.s.ID, "cardId": c.s.CardID, "text": c.chunkBuf.String(),
	}
	if c.chunkThought {
		payload["thought"] = true
	}
	c.chunkBuf.Reset()
	c.m.ui.Emit(EventChunk, payload)
}

var _ acpsdk.Client = (*sessionClient)(nil)

// RequestPermission applies the auto-allow list and the session's accumulated
// "always allow" set. Anything else goes to the user when a console is
// watching, and is rejected outright when nobody is there to answer.
//
// Blocking here is safe: the SDK dispatches every inbound request on its own
// goroutine, so the agent's session/update stream keeps flowing while the
// prompt is on screen.
func (c *sessionClient) RequestPermission(ctx context.Context, params acpsdk.RequestPermissionRequest) (acpsdk.RequestPermissionResponse, error) {
	toolName := permissionToolName(params)
	title := ""
	if params.ToolCall.Title != nil {
		title = *params.ToolCall.Title
	}

	if c.s.autoAllowed(toolName, c.m.cfg) || c.s.toolAllowed(toolName) {
		c.recordDecision(toolName, title, "allow", true)
		return selectOption(params, acpsdk.PermissionOptionKindAllowOnce)
	}
	if !c.s.hasConsole() {
		c.recordDecision(toolName, title, "reject", true)
		// Spell out why: the tool is simply not on the allow list, and there is
		// no console open on the card to ask. Opening the card attaches one.
		c.m.log.Info("acp: permission rejected — no console attached to ask",
			"session", c.s.ID, "card", c.s.CardID, "tool", toolName,
			"hint", "open the card to answer prompts, or add the tool to autoAllowTools")
		return selectOption(params, acpsdk.PermissionOptionKindRejectOnce)
	}
	return c.askUser(ctx, params, toolName, title)
}

// askUser puts the prompt on the console and waits for a decision, falling back
// to a rejection on timeout or cancellation.
func (c *sessionClient) askUser(ctx context.Context, params acpsdk.RequestPermissionRequest, toolName, title string) (acpsdk.RequestPermissionResponse, error) {
	c.flush() // the prompt must land after the text that explains it
	requestID := uuid.NewString()
	answer := c.m.registerPermission(requestID, c.s.ID)
	defer c.m.forgetPermission(requestID)

	options := make([]map[string]any, 0, len(params.Options))
	for _, opt := range params.Options {
		options = append(options, map[string]any{
			"optionId": string(opt.OptionId),
			"name":     opt.Name,
			"kind":     string(opt.Kind),
		})
	}
	c.s.appendEvent(c.m, "permission", map[string]any{
		"requestId": requestID,
		"tool":      toolName,
		"title":     title,
		"options":   options,
		"pending":   true,
	})
	c.m.setStatus(c.s, StatusWaitingPermission)
	c.m.ui.Emit(EventPermission, map[string]any{
		"sessionId": c.s.ID,
		"cardId":    c.s.CardID,
		"requestId": requestID,
		"tool":      toolName,
		"title":     title,
		"options":   options,
		"pending":   true,
	})
	// The turn is still running whatever the user decides.
	defer c.m.setStatus(c.s, StatusRunning)

	timeout := time.NewTimer(c.m.cfg.PermissionTimeout())
	defer timeout.Stop()

	var chosen string
	select {
	case chosen = <-answer:
	case <-timeout.C:
		c.recordDecision(toolName, title, "reject", false)
		c.m.log.Info("acp: permission prompt timed out", "session", c.s.ID, "tool", toolName)
		return selectOption(params, acpsdk.PermissionOptionKindRejectOnce)
	case <-ctx.Done():
		return acpsdk.RequestPermissionResponse{Outcome: acpsdk.RequestPermissionOutcome{
			Cancelled: &acpsdk.RequestPermissionOutcomeCancelled{},
		}}, nil
	}

	for _, opt := range params.Options {
		if string(opt.OptionId) != chosen {
			continue
		}
		if opt.Kind == acpsdk.PermissionOptionKindAllowAlways {
			c.s.allowToolAlways(toolName)
		}
		c.recordDecision(toolName, title, string(opt.Kind), false)
		return acpsdk.RequestPermissionResponse{Outcome: acpsdk.RequestPermissionOutcome{
			Selected: &acpsdk.RequestPermissionOutcomeSelected{OptionId: opt.OptionId},
		}}, nil
	}
	c.recordDecision(toolName, title, "reject", false)
	return selectOption(params, acpsdk.PermissionOptionKindRejectOnce)
}

// recordDecision persists and broadcasts how a permission ended up. byPolicy
// marks decisions the user was never asked about.
func (c *sessionClient) recordDecision(toolName, title, decision string, byPolicy bool) {
	c.s.appendEvent(c.m, "permission", map[string]any{
		"tool":     toolName,
		"title":    title,
		"decision": decision,
		"byPolicy": byPolicy,
	})
	c.m.ui.Emit(EventPermission, map[string]any{
		"sessionId": c.s.ID,
		"cardId":    c.s.CardID,
		"tool":      toolName,
		"title":     title,
		"decision":  decision,
		"byPolicy":  byPolicy,
	})
}

// selectOption picks the agent-offered option of the wanted kind, falling back
// to cancellation when the agent offered nothing suitable.
func selectOption(params acpsdk.RequestPermissionRequest, kind acpsdk.PermissionOptionKind) (acpsdk.RequestPermissionResponse, error) {
	for _, opt := range params.Options {
		if opt.Kind == kind {
			return acpsdk.RequestPermissionResponse{Outcome: acpsdk.RequestPermissionOutcome{
				Selected: &acpsdk.RequestPermissionOutcomeSelected{OptionId: opt.OptionId},
			}}, nil
		}
	}
	return acpsdk.RequestPermissionResponse{Outcome: acpsdk.RequestPermissionOutcome{
		Cancelled: &acpsdk.RequestPermissionOutcomeCancelled{},
	}}, nil
}

// permissionToolName extracts the tool name the bridge put into the meta.
func permissionToolName(params acpsdk.RequestPermissionRequest) string {
	if params.ToolCall.Meta != nil {
		if name, ok := params.ToolCall.Meta["toolName"].(string); ok && name != "" {
			return name
		}
	}
	if params.ToolCall.Title != nil {
		if name, _, found := strings.Cut(*params.ToolCall.Title, ":"); found {
			return strings.TrimSpace(name)
		}
		return *params.ToolCall.Title
	}
	return ""
}

func (c *sessionClient) SessionUpdate(ctx context.Context, params acpsdk.SessionNotification) error {
	u := params.Update
	switch {
	case u.AgentMessageChunk != nil:
		if t := u.AgentMessageChunk.Content.Text; t != nil {
			c.s.finalMu.Lock()
			c.s.finalBuf.WriteString(t.Text)
			c.s.finalMu.Unlock()
			c.s.appendEvent(c.m, "chunk", map[string]any{"text": t.Text})
			c.emitChunk(t.Text, false)
		}
	case u.AgentThoughtChunk != nil:
		if t := u.AgentThoughtChunk.Content.Text; t != nil {
			c.s.appendEvent(c.m, "thought", map[string]any{"text": t.Text})
			if c.m.cfg.ShowThoughts {
				c.emitChunk(t.Text, true)
			}
		}
	case u.ToolCall != nil:
		c.flush() // keep the console in the order the agent produced it
		c.s.appendEvent(c.m, "tool_call", map[string]any{
			"toolCallId": string(u.ToolCall.ToolCallId),
			"title":      u.ToolCall.Title,
			"status":     string(u.ToolCall.Status),
		})
		c.m.ui.Emit(EventTool, map[string]any{
			"sessionId": c.s.ID, "cardId": c.s.CardID,
			"toolCallId": string(u.ToolCall.ToolCallId),
			"title":      u.ToolCall.Title,
			"status":     string(u.ToolCall.Status),
		})
	case u.ToolCallUpdate != nil:
		status := ""
		if u.ToolCallUpdate.Status != nil {
			status = string(*u.ToolCallUpdate.Status)
		}
		c.s.appendEvent(c.m, "tool_update", map[string]any{
			"toolCallId": string(u.ToolCallUpdate.ToolCallId),
			"status":     status,
		})
		c.m.ui.Emit(EventTool, map[string]any{
			"sessionId": c.s.ID, "cardId": c.s.CardID,
			"toolCallId": string(u.ToolCallUpdate.ToolCallId),
			"status":     status,
		})
	}
	return nil
}

// File-system proxying is jailed to the session worktree. The claude bridge
// never calls these (the CLI does its own I/O); external ACP agents might.
func (c *sessionClient) ReadTextFile(ctx context.Context, params acpsdk.ReadTextFileRequest) (acpsdk.ReadTextFileResponse, error) {
	path, err := c.jail(params.Path)
	if err != nil {
		return acpsdk.ReadTextFileResponse{}, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return acpsdk.ReadTextFileResponse{}, err
	}
	return acpsdk.ReadTextFileResponse{Content: string(b)}, nil
}

func (c *sessionClient) WriteTextFile(ctx context.Context, params acpsdk.WriteTextFileRequest) (acpsdk.WriteTextFileResponse, error) {
	path, err := c.jail(params.Path)
	if err != nil {
		return acpsdk.WriteTextFileResponse{}, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return acpsdk.WriteTextFileResponse{}, err
	}
	if err := os.WriteFile(path, []byte(params.Content), 0o644); err != nil {
		return acpsdk.WriteTextFileResponse{}, err
	}
	return acpsdk.WriteTextFileResponse{}, nil
}

func (c *sessionClient) jail(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("path must be absolute: %s", path)
	}
	clean := filepath.Clean(path)
	root := c.s.Worktree.Path
	if root == "" {
		return "", fmt.Errorf("session has no worktree")
	}
	if clean != root && !strings.HasPrefix(clean, root+string(filepath.Separator)) {
		c.m.log.Warn("acp: fs access outside worktree denied", "session", c.s.ID, "path", clean)
		return "", fmt.Errorf("path %s is outside the session worktree", clean)
	}
	return clean, nil
}

// Terminal capability is not advertised.
func (c *sessionClient) CreateTerminal(ctx context.Context, params acpsdk.CreateTerminalRequest) (acpsdk.CreateTerminalResponse, error) {
	return acpsdk.CreateTerminalResponse{}, fmt.Errorf("terminal not supported")
}
func (c *sessionClient) KillTerminal(ctx context.Context, params acpsdk.KillTerminalRequest) (acpsdk.KillTerminalResponse, error) {
	return acpsdk.KillTerminalResponse{}, fmt.Errorf("terminal not supported")
}
func (c *sessionClient) TerminalOutput(ctx context.Context, params acpsdk.TerminalOutputRequest) (acpsdk.TerminalOutputResponse, error) {
	return acpsdk.TerminalOutputResponse{}, fmt.Errorf("terminal not supported")
}
func (c *sessionClient) ReleaseTerminal(ctx context.Context, params acpsdk.ReleaseTerminalRequest) (acpsdk.ReleaseTerminalResponse, error) {
	return acpsdk.ReleaseTerminalResponse{}, fmt.Errorf("terminal not supported")
}
func (c *sessionClient) WaitForTerminalExit(ctx context.Context, params acpsdk.WaitForTerminalExitRequest) (acpsdk.WaitForTerminalExitResponse, error) {
	return acpsdk.WaitForTerminalExitResponse{}, fmt.Errorf("terminal not supported")
}
