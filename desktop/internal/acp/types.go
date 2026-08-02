// Package acp implements the board-to-coding-agent integration described in
// TZ_ACP_wails_v0.2.md: moving a card into a trigger column starts an ACP
// session in a dedicated git worktree and reports progress back to the card.
//
// The package deliberately knows nothing about the Focalboard server. It talks
// to the board only through the BoardEvents/BoardWriter interfaces below, whose
// implementations live in internal/boardadapter.
package acp

import (
	"context"
	"time"
)

// Column identifies one option of a select property ("column" on a kanban board).
type Column struct {
	PropertyID   string // select property id on the board
	PropertyName string // e.g. "Status"
	OptionID     string // option id stored in the card's properties map
	Name         string // option display value, e.g. "To Agent"
}

// CardMoved is the normalized "card changed column" event.
type CardMoved struct {
	EventID     string
	CardID      string
	BoardID     string
	Title       string
	Body        string            // card description text, if any
	Props       map[string]string // lowercased property name → display value
	OptionNames []string          // display names of every selected select/multiSelect option (tags included)
	// PersonNames are the usernames behind every person/multiPerson value on the
	// card — the "Assignee" route to an agent, which works because a registered
	// agent is provisioned as a board user (see BoardUsers).
	PersonNames []string
	FromColumn  Column
	ToColumn    Column
	At          time.Time
}

// BoardEvents delivers normalized card-move events from the board.
type BoardEvents interface {
	Subscribe(ctx context.Context) (<-chan CardMoved, error)
}

// BoardWriter performs the mutations the integration needs.
type BoardWriter interface {
	AddComment(ctx context.Context, cardID, text string) error
	MoveCard(ctx context.Context, cardID, optionID string) error
	// MoveCardByOptionName moves a card to a column the config names rather than
	// identifies: "Tested", not an option id.
	MoveCardByOptionName(ctx context.Context, cardID, propertyName, optionName string) error
	// AttachFile adds a file to the card's content — how a test run's
	// screenshots reach the person reading the result.
	AttachFile(ctx context.Context, cardID, filename, mime string, data []byte) error
}

// BoardReader reads a card on demand, so a session can be opened from the UI
// without waiting for the card to be moved into the trigger column. The
// returned event carries no columns — nothing was moved.
type BoardReader interface {
	CardByID(ctx context.Context, cardID string) (CardMoved, error)
}

// AgentUser is a registry entry seen as a board account: the user an agent is
// assigned as. Username is derived from Name (see AgentUsername); UserID and
// Created are filled in by BoardUsers.
type AgentUser struct {
	Name     string `json:"name"`
	Username string `json:"username"`
	UserID   string `json:"userId,omitempty"`
	Created  bool   `json:"created,omitempty"`
}

// BoardUsers keeps the board-side accounts in step with the agent registry, so
// an agent can be picked in a person property ("Assignee") like any other
// member — and stops being offered once it is unregistered. Optional: without
// it the "Agent" select field remains the only routing mechanism.
type BoardUsers interface {
	EnsureAgentUsers(ctx context.Context, boardID string, agents []AgentUser) ([]AgentUser, error)
	// RetireAgentUser drops the account's board memberships and reports how
	// many were removed. The account itself stays: cards may still name it, and
	// re-registering the agent should give it its identity back.
	RetireAgentUser(ctx context.Context, agent AgentUser) (int, error)
}

// UIEmitter pushes events to the desktop UI. Implementations must be safe to
// call before the UI is ready (drop and log).
type UIEmitter interface {
	Emit(event string, payload any)
}

// SessionStatus is the lifecycle state of an agent session.
type SessionStatus string

const (
	StatusQueued  SessionStatus = "queued"
	StatusRunning SessionStatus = "running"
	// StatusIdle is a live interactive session between turns: the agent
	// connection is up and waiting for the next user message.
	StatusIdle              SessionStatus = "idle"
	StatusWaitingPermission SessionStatus = "waiting_permission"
	StatusDone              SessionStatus = "done"
	StatusFailed            SessionStatus = "failed"
	StatusCancelled         SessionStatus = "cancelled"
)

// Terminal reports whether the status is final.
func (s SessionStatus) Terminal() bool {
	return s == StatusDone || s == StatusFailed || s == StatusCancelled
}

// UI event names emitted through UIEmitter.
const (
	EventSession    = "acp:session"
	EventChunk      = "acp:chunk"
	EventTool       = "acp:tool"
	EventPermission = "acp:permission"
	// EventElicitation is a form the agent asked the user to fill in — its own
	// question with options, or an MCP server's. See elicitation.go.
	EventElicitation = "acp:elicitation"
	// EventPrompt echoes a user turn back to every attached console, so a
	// second console shows what was typed into the first one.
	EventPrompt = "acp:prompt"
)
