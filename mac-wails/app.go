// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"context"
	"encoding/json"
	"errors"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/mattermost/focalboard/mac-wails/internal/acp"
)

// errACPDisabled is returned by bindings when the integration is off.
var errACPDisabled = errors.New("интеграция агента выключена (см. конфиг acp)")

// App holds Wails lifecycle state and exposes methods bound into the frontend
// (reachable from JS as window.go.main.App.*). It contains no logic of its
// own; ACP calls delegate to the manager.
type App struct {
	ctx     context.Context
	emitter *wailsEmitter
	mgr     *acp.Manager // nil when the ACP integration is disabled
}

func NewApp(emitter *wailsEmitter) *App {
	return &App{emitter: emitter}
}

// startup captures the Wails runtime context once the app is ready.
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	if a.emitter != nil {
		a.emitter.SetContext(ctx)
	}
}

// OpenInBrowser opens the given URL in the user's default system browser.
// Bound to JS and invoked by the target=_blank click handler injected in
// bootstrapScript.
func (a *App) OpenInBrowser(url string) {
	if a.ctx == nil || url == "" {
		return
	}
	wruntime.BrowserOpenURL(a.ctx, url)
}

// CancelSession cancels the live agent session of a card, if any.
func (a *App) CancelSession(cardID string) bool {
	if a.mgr == nil {
		return false
	}
	return a.mgr.CancelSessionForCard(cardID, "отменено пользователем")
}

// ListAgentRepos returns the repo registry as JSON: [{"name","path"}, …].
func (a *App) ListAgentRepos() (string, error) {
	if a.mgr == nil {
		return "[]", nil
	}
	out, err := json.Marshal(a.mgr.Repos())
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// PickDirectory opens the native folder picker and returns the chosen
// absolute path ("" when the user cancels).
func (a *App) PickDirectory(title string) (string, error) {
	if a.ctx == nil {
		return "", nil
	}
	return wruntime.OpenDirectoryDialog(a.ctx, wruntime.OpenDialogOptions{Title: title})
}

// AddAgentRepo registers a local repository (name defaults to the directory
// basename when empty) and returns the created entry as JSON.
func (a *App) AddAgentRepo(name, path string) (string, error) {
	if a.mgr == nil {
		return "", errACPDisabled
	}
	entry, err := a.mgr.AddRepo(name, path)
	if err != nil {
		return "", err
	}
	out, _ := json.Marshal(entry)
	return string(out), nil
}

// RemoveAgentRepo deletes a repo registry entry by name.
func (a *App) RemoveAgentRepo(name string) error {
	if a.mgr == nil {
		return errACPDisabled
	}
	return a.mgr.RemoveRepo(name)
}

// ListAgents returns the agent registry as JSON: [{"name","kind",…}, …].
func (a *App) ListAgents() (string, error) {
	if a.mgr == nil {
		return "[]", nil
	}
	out, err := json.Marshal(a.mgr.Agents())
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// AddAgent registers a new agent from a JSON-encoded AgentEntry and returns the
// created entry as JSON.
func (a *App) AddAgent(entryJSON string) (string, error) {
	if a.mgr == nil {
		return "", errACPDisabled
	}
	var entry acp.AgentEntry
	if err := json.Unmarshal([]byte(entryJSON), &entry); err != nil {
		return "", err
	}
	saved, err := a.mgr.AddAgent(entry)
	if err != nil {
		return "", err
	}
	out, _ := json.Marshal(saved)
	return string(out), nil
}

// UpdateAgent replaces an existing agent (matched by name) from a JSON-encoded
// AgentEntry and returns the saved entry as JSON.
func (a *App) UpdateAgent(entryJSON string) (string, error) {
	if a.mgr == nil {
		return "", errACPDisabled
	}
	var entry acp.AgentEntry
	if err := json.Unmarshal([]byte(entryJSON), &entry); err != nil {
		return "", err
	}
	saved, err := a.mgr.UpdateAgent(entry)
	if err != nil {
		return "", err
	}
	out, _ := json.Marshal(saved)
	return string(out), nil
}

// RemoveAgent deletes an agent registry entry by name.
func (a *App) RemoveAgent(name string) error {
	if a.mgr == nil {
		return errACPDisabled
	}
	return a.mgr.RemoveAgent(name)
}

// ListProxies returns the proxy registry as JSON: [{"name","proxy",…}, …].
func (a *App) ListProxies() (string, error) {
	if a.mgr == nil {
		return "[]", nil
	}
	out, err := json.Marshal(a.mgr.Proxies())
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// AddProxy registers a network configuration from a JSON-encoded ProxyEntry and
// returns the created entry as JSON.
func (a *App) AddProxy(entryJSON string) (string, error) {
	if a.mgr == nil {
		return "", errACPDisabled
	}
	var entry acp.ProxyEntry
	if err := json.Unmarshal([]byte(entryJSON), &entry); err != nil {
		return "", err
	}
	saved, err := a.mgr.AddProxy(entry)
	if err != nil {
		return "", err
	}
	out, _ := json.Marshal(saved)
	return string(out), nil
}

// UpdateProxy replaces an existing network configuration (matched by name) from
// a JSON-encoded ProxyEntry and returns the saved entry as JSON.
func (a *App) UpdateProxy(entryJSON string) (string, error) {
	if a.mgr == nil {
		return "", errACPDisabled
	}
	var entry acp.ProxyEntry
	if err := json.Unmarshal([]byte(entryJSON), &entry); err != nil {
		return "", err
	}
	saved, err := a.mgr.UpdateProxy(entry)
	if err != nil {
		return "", err
	}
	out, _ := json.Marshal(saved)
	return string(out), nil
}

// RemoveProxy deletes a network configuration by name.
func (a *App) RemoveProxy(name string) error {
	if a.mgr == nil {
		return errACPDisabled
	}
	return a.mgr.RemoveProxy(name)
}

// GetAgentSystemPrompt returns the board/column-level system prompt.
func (a *App) GetAgentSystemPrompt() (string, error) {
	if a.mgr == nil {
		return "", nil
	}
	return a.mgr.SystemPrompt(), nil
}

// SetAgentSystemPrompt stores the board/column-level system prompt.
func (a *App) SetAgentSystemPrompt(text string) error {
	if a.mgr == nil {
		return errACPDisabled
	}
	return a.mgr.SetSystemPrompt(text)
}

// GetCardSessions returns the card's persisted agent sessions and their event
// logs as JSON: {"sessions": [...], "events": [...]}.
func (a *App) GetCardSessions(cardID string) (string, error) {
	if a.mgr == nil {
		return `{"sessions":[],"events":[]}`, nil
	}
	sessions, events, err := a.mgr.CardSessions(cardID)
	if err != nil {
		return "", err
	}
	out, err := json.Marshal(map[string]any{"sessions": sessions, "events": events})
	if err != nil {
		return "", err
	}
	return string(out), nil
}
