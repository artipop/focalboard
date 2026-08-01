// Package vcs watches repositories for the events a card's route waits on:
// the branch was pushed, the branch was merged, a pull request was opened or
// merged, checks passed. It knows nothing about boards or flows — it is handed
// a target and reports what it sees, and the caller decides what that means.
//
// Everything here is polled rather than pushed: the app runs on a laptop behind
// NAT, where a webhook has nowhere to arrive.
package vcs

import (
	"context"
	"strings"
	"time"
)

// Event kinds. They are the same strings the flow engine uses as edge triggers;
// keeping them literal here avoids a dependency between the two packages.
const (
	KindBranchPushed = "branch.pushed"
	KindBranchMerged = "branch.merged"

	KindPROpened       = "pr.opened"
	KindPRMerged       = "pr.merged"
	KindPRClosed       = "pr.closed"
	KindReviewApproved = "review.approved"
	KindChecksPassed   = "checks.passed"
	KindChecksFailed   = "checks.failed"
)

// Target is one branch somebody is waiting on.
type Target struct {
	RepoPath string   // local git repository
	Branch   string   // branch the card is about
	Remote   string   // remote to consult; empty means "origin"
	Triggers []string // event kinds the caller cares about
}

// RemoteOr returns the remote to use.
func (t Target) RemoteOr(fallback string) string {
	if r := strings.TrimSpace(t.Remote); r != "" {
		return r
	}
	if fallback != "" {
		return fallback
	}
	return "origin"
}

// Wants reports whether the target waits for this kind of event. A watcher must
// not spend requests on anything else.
func (t Target) Wants(kind string) bool {
	for _, k := range t.Triggers {
		if k == kind {
			return true
		}
	}
	return false
}

// Event is something that happened to a branch.
type Event struct {
	Kind     string
	RepoPath string
	Branch   string
	Detail   string // human phrasing for the card comment
	URL      string // pull request URL, when there is one
	Number   int    // pull request number, when there is one

	// Marker distinguishes one occurrence from the next: the same kind fires
	// again only when the marker changes (a new commit, another pull request).
	// It is what the caller stores to keep an event from repeating.
	Marker string
	At     time.Time
}

// Watcher observes one source of events. Poll is called on a schedule with the
// targets somebody is currently waiting on.
type Watcher interface {
	// Name identifies the watcher in logs.
	Name() string
	// Poll reports what it can see for one target. An error is transient by
	// nature (network, a repository being rewritten) and only gets logged.
	Poll(ctx context.Context, t Target) ([]Event, error)
}
