// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"context"
	"encoding/json"
	"errors"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/mattermost/focalboard/desktop/internal/acp"
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

// ListAgentAdapters reports, per agent kind, whether it can be started on this
// machine — the adapter is installed, npx would fetch it, or Node.js is missing
// — so the dialog can say it instead of a card failing later.
func (a *App) ListAgentAdapters() (string, error) {
	if a.mgr == nil {
		return "[]", nil
	}
	out, err := json.Marshal(a.mgr.AdapterStatuses())
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// InstallAgentAdapter installs a kind's adapter with npm and returns npm's own
// output, so a failure is readable.
func (a *App) InstallAgentAdapter(kind string) (string, error) {
	if a.mgr == nil {
		return "", errACPDisabled
	}
	return a.mgr.InstallAdapter(kind)
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

// AgentOptions asks the agent itself which settings it has — Fast mode, an
// effort level, a permission mode — and returns them as JSON:
// [{"id","name","type","current","values":[…]}, …]. The agent is started the
// way a session would start it and asked nothing, so the dialog offers exactly
// what this agent supports and no toggle for what it does not. refresh skips
// the cached answer.
func (a *App) AgentOptions(entryJSON string, refresh bool) (string, error) {
	if a.mgr == nil {
		return "[]", nil
	}
	var entry acp.AgentEntry
	if err := json.Unmarshal([]byte(entryJSON), &entry); err != nil {
		return "", err
	}
	options, err := a.mgr.AgentOptions(entry, refresh)
	if err != nil {
		return "", err
	}
	out, err := json.Marshal(options)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// RemoveAgent deletes an agent registry entry by name.
func (a *App) RemoveAgent(name string) error {
	if a.mgr == nil {
		return errACPDisabled
	}
	return a.mgr.RemoveAgent(name)
}

// SyncAgentUsers gives every registered agent a board account and adds it to
// the board's members, so cards can be assigned to an agent in a person
// property. Returns the accounts as JSON: [{"name","username","userId",
// "created"}, …].
func (a *App) SyncAgentUsers(boardID string) (string, error) {
	if a.mgr == nil {
		return "", errACPDisabled
	}
	users, err := a.mgr.SyncAgentUsers(context.Background(), boardID)
	if err != nil {
		return "", err
	}
	out, _ := json.Marshal(users)
	return string(out), nil
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

// ListDeployTargets returns the deploy registry as JSON:
// [{"name","sshHost","baseDomain",…}, …].
func (a *App) ListDeployTargets() (string, error) {
	if a.mgr == nil {
		return "[]", nil
	}
	out, err := json.Marshal(a.mgr.Deploys())
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// AddDeployTarget registers a Dokku destination from a JSON-encoded DeployEntry
// and returns the created entry as JSON.
func (a *App) AddDeployTarget(entryJSON string) (string, error) {
	if a.mgr == nil {
		return "", errACPDisabled
	}
	var entry acp.DeployEntry
	if err := json.Unmarshal([]byte(entryJSON), &entry); err != nil {
		return "", err
	}
	saved, err := a.mgr.AddDeploy(entry)
	if err != nil {
		return "", err
	}
	out, _ := json.Marshal(saved)
	return string(out), nil
}

// UpdateDeployTarget replaces an existing destination (matched by name) from a
// JSON-encoded DeployEntry and returns the saved entry as JSON.
func (a *App) UpdateDeployTarget(entryJSON string) (string, error) {
	if a.mgr == nil {
		return "", errACPDisabled
	}
	var entry acp.DeployEntry
	if err := json.Unmarshal([]byte(entryJSON), &entry); err != nil {
		return "", err
	}
	saved, err := a.mgr.UpdateDeploy(entry)
	if err != nil {
		return "", err
	}
	out, _ := json.Marshal(saved)
	return string(out), nil
}

// RemoveDeployTarget deletes a Dokku destination by name.
func (a *App) RemoveDeployTarget(name string) error {
	if a.mgr == nil {
		return errACPDisabled
	}
	return a.mgr.RemoveDeploy(name)
}

// ListFlows returns the routes a board may use as JSON — its own, plus any tied
// to no board in particular — each a graph of nodes (a column) and edges (an
// event and where it leads).
func (a *App) ListFlows(boardID string) (string, error) {
	if a.mgr == nil {
		return "[]", nil
	}
	out, err := json.Marshal(a.mgr.BoardFlows(boardID))
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// ListFlowTriggers returns the closed set of edge triggers the engine
// implements, so the editor can only offer transitions that actually work.
func (a *App) ListFlowTriggers() (string, error) {
	out, err := json.Marshal(acp.FlowTriggers)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// ListFlowTemplates returns the routes a fresh install is seeded with, rebuilt
// from the current column names. An install whose registry predates them (or
// whose routes were deleted) can add the ones it is missing from the editor
// instead of retyping a graph.
func (a *App) ListFlowTemplates() (string, error) {
	if a.mgr == nil {
		return "[]", nil
	}
	out, err := json.Marshal(a.mgr.FlowTemplates())
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// AddFlow registers a route from a JSON-encoded FlowEntry and returns it.
func (a *App) AddFlow(entryJSON string) (string, error) {
	if a.mgr == nil {
		return "", errACPDisabled
	}
	var entry acp.FlowEntry
	if err := json.Unmarshal([]byte(entryJSON), &entry); err != nil {
		return "", err
	}
	saved, err := a.mgr.AddFlow(entry)
	if err != nil {
		return "", err
	}
	out, _ := json.Marshal(saved)
	return string(out), nil
}

// UpdateFlow replaces an existing route (matched by name) and returns it.
func (a *App) UpdateFlow(entryJSON string) (string, error) {
	if a.mgr == nil {
		return "", errACPDisabled
	}
	var entry acp.FlowEntry
	if err := json.Unmarshal([]byte(entryJSON), &entry); err != nil {
		return "", err
	}
	saved, err := a.mgr.UpdateFlow(entry)
	if err != nil {
		return "", err
	}
	out, _ := json.Marshal(saved)
	return string(out), nil
}

// RemoveFlow deletes a route by name. Cards standing on it simply stop moving
// by themselves.
func (a *App) RemoveFlow(name string) error {
	if a.mgr == nil {
		return errACPDisabled
	}
	return a.mgr.RemoveFlow(name)
}

// ListBoardColumns returns what each configured column of a board does: the
// action, the crew that works it and how many of them at once.
func (a *App) ListBoardColumns(boardID string) (string, error) {
	if a.mgr == nil {
		return "[]", nil
	}
	out, err := json.Marshal(a.mgr.BoardColumns(boardID))
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// SaveBoardColumn stores the settings of one column from a JSON-encoded
// ColumnSpec and returns what was saved.
func (a *App) SaveBoardColumn(specJSON string) (string, error) {
	if a.mgr == nil {
		return "", errACPDisabled
	}
	var spec acp.ColumnSpec
	if err := json.Unmarshal([]byte(specJSON), &spec); err != nil {
		return "", err
	}
	saved, err := a.mgr.SaveColumn(spec)
	if err != nil {
		return "", err
	}
	out, _ := json.Marshal(saved)
	return string(out), nil
}

// RemoveBoardColumn forgets a column's settings. The column stays on the board.
func (a *App) RemoveBoardColumn(boardID, optionID, column string) error {
	if a.mgr == nil {
		return errACPDisabled
	}
	return a.mgr.RemoveColumn(boardID, optionID, column)
}

// GetCardFlow describes where a card stands on its route: the stages, the one
// it is on, what that stage waits for. Returns "null" for a card with no route.
func (a *App) GetCardFlow(cardID string) (string, error) {
	if a.mgr == nil {
		return "null", nil
	}
	flow, err := a.mgr.CardFlowFor(cardID)
	if err != nil {
		return "", err
	}
	out, err := json.Marshal(flow)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// SeedBoardAutomation takes the columns and routes a board carries of its own
// into the registry now, rather than waiting for the first card to be moved.
// The setup wizard calls it, so what the board can do is visible as soon as it
// is configured. Idempotent: anything already registered is left alone.
func (a *App) SeedBoardAutomation(boardID string) error {
	if a.mgr == nil {
		return errACPDisabled
	}
	a.mgr.SeedBoard(boardID)
	return nil
}

// GetBoardFlowOverview returns where the board's cards stand on each route:
// per stage, how many are there, how many are working and how many wait.
func (a *App) GetBoardFlowOverview(boardID string) (string, error) {
	if a.mgr == nil {
		return "[]", nil
	}
	overview, err := a.mgr.BoardFlowOverview(boardID)
	if err != nil {
		return "", err
	}
	out, err := json.Marshal(overview)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// GetWorktreeMode reports where sessions run ("always" or "never"). The column
// editor asks, because a crew of several agents in one repository only works
// when each session gets its own worktree.
func (a *App) GetWorktreeMode() (string, error) {
	if a.mgr == nil {
		return "", nil
	}
	return a.mgr.WorktreeMode(), nil
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

// StartCardSession opens an interactive agent session on a card (or attaches to
// the one already running) and returns its session id.
// repoName may be empty, in which case the repository is taken from the card;
// the console supplies one when the card carries no repository tag.
func (a *App) StartCardSession(cardID, repoName string) (string, error) {
	if a.mgr == nil {
		return "", errACPDisabled
	}
	s, err := a.mgr.StartSessionForCard(cardID, repoName)
	if err != nil {
		return "", err
	}
	return s.ID, nil
}

// StartCardDeploy publishes a card's branch to its Dokku target without moving
// the card into the deploy column, and returns the deploy session's id. branch
// is the one the card is working on (its session's worktree branch); empty lets
// the card property or the checked-out branch decide.
func (a *App) StartCardDeploy(cardID, branch string) (string, error) {
	if a.mgr == nil {
		return "", errACPDisabled
	}
	s, err := a.mgr.StartDeployForCard(cardID, branch)
	if err != nil {
		return "", err
	}
	return s.ID, nil
}

// StartPlanningSession opens a card-less session for talking a task through
// before it exists, and returns its session id. repoName/agentName may be empty
// when the corresponding registry holds exactly one entry.
func (a *App) StartPlanningSession(repoName, agentName string) (string, error) {
	if a.mgr == nil {
		return "", errACPDisabled
	}
	s, err := a.mgr.StartPlanningSession(repoName, agentName)
	if err != nil {
		return "", err
	}
	return s.ID, nil
}

// ComposeTask asks the agent to boil the conversation down to a task and
// returns its answer: the first line is the title, the rest the description.
func (a *App) ComposeTask(sessionID string) (string, error) {
	if a.mgr == nil {
		return "", errACPDisabled
	}
	return a.mgr.ComposeTask(sessionID)
}

// PromptSession sends a follow-up message to a live session.
func (a *App) PromptSession(sessionID, text string) error {
	if a.mgr == nil {
		return errACPDisabled
	}
	return a.mgr.PromptSession(sessionID, text)
}

// AnswerPermission delivers the user's choice for a pending permission prompt.
func (a *App) AnswerPermission(sessionID, requestID, optionID string) error {
	if a.mgr == nil {
		return errACPDisabled
	}
	return a.mgr.AnswerPermission(sessionID, requestID, optionID)
}

// AnswerElicitation delivers what the user filled into a form the agent asked
// for. contentJSON is an object keyed by the field keys the form carried.
func (a *App) AnswerElicitation(sessionID, requestID, contentJSON string) error {
	if a.mgr == nil {
		return errACPDisabled
	}
	return a.mgr.AnswerElicitation(sessionID, requestID, contentJSON)
}

// AttachSession marks the console as watching a session, keeping it alive
// between turns. Returns false when the session is no longer live.
func (a *App) AttachSession(sessionID string) bool {
	if a.mgr == nil {
		return false
	}
	return a.mgr.AttachSession(sessionID)
}

// DetachSession drops the console; an unattended idle session then closes.
func (a *App) DetachSession(sessionID string) {
	if a.mgr == nil {
		return
	}
	a.mgr.DetachSession(sessionID)
}

// CloseSession ends a session after its current turn.
func (a *App) CloseSession(sessionID string) error {
	if a.mgr == nil {
		return errACPDisabled
	}
	return a.mgr.CloseSession(sessionID)
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
