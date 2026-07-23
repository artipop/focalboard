package acp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	acpsdk "github.com/coder/acp-go-sdk"
)

// sessionClient implements acpsdk.Client for one session: it receives the
// agent's stream, persists it, forwards it to the UI and answers permission
// requests according to policy.
type sessionClient struct {
	m *Manager
	s *Session
}

var _ acpsdk.Client = (*sessionClient)(nil)

// RequestPermission applies the auto-allow list and the session's accumulated
// "always allow" set. Anything else is rejected in Phase 1 (no modal yet).
func (c *sessionClient) RequestPermission(ctx context.Context, params acpsdk.RequestPermissionRequest) (acpsdk.RequestPermissionResponse, error) {
	toolName := permissionToolName(params)
	title := ""
	if params.ToolCall.Title != nil {
		title = *params.ToolCall.Title
	}

	decision := "reject"
	if c.m.cfg.ToolAllowed(toolName) || c.s.toolAllowed(toolName) {
		decision = "allow"
	}
	c.s.appendEvent(c.m, "permission", map[string]any{
		"tool":     toolName,
		"title":    title,
		"decision": decision,
	})
	c.m.ui.Emit(EventPermission, map[string]any{
		"sessionId": c.s.ID,
		"cardId":    c.s.CardID,
		"tool":      toolName,
		"title":     title,
		"decision":  decision, // Phase 1: already decided, informational only
	})

	if decision == "allow" {
		return selectOption(params, acpsdk.PermissionOptionKindAllowOnce)
	}
	c.m.log.Info("acp: permission rejected by policy", "session", c.s.ID, "tool", toolName)
	return selectOption(params, acpsdk.PermissionOptionKindRejectOnce)
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
			c.m.ui.Emit(EventChunk, map[string]any{
				"sessionId": c.s.ID, "cardId": c.s.CardID, "text": t.Text,
			})
		}
	case u.AgentThoughtChunk != nil:
		if t := u.AgentThoughtChunk.Content.Text; t != nil {
			c.s.appendEvent(c.m, "thought", map[string]any{"text": t.Text})
			if c.m.cfg.ShowThoughts {
				c.m.ui.Emit(EventChunk, map[string]any{
					"sessionId": c.s.ID, "cardId": c.s.CardID, "text": t.Text, "thought": true,
				})
			}
		}
	case u.ToolCall != nil:
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
