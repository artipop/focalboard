package acp

import (
	"context"
	"time"

	"github.com/mattermost/focalboard/desktop/internal/vcs"
)

// The second input of the flow engine: what happens in the repository. The app
// sits on a laptop, so there is nowhere for a webhook to arrive — everything is
// polled, and only for the branches a parked card actually waits on. With no
// card waiting, no request is made at all.

// vcsPollTimeout bounds one watcher call, so a hung network cannot stall the loop.
const vcsPollTimeout = 60 * time.Second

// SetWatchers replaces the repository watchers (tests use it to inject a fake).
func (m *Manager) SetWatchers(w ...vcs.Watcher) { m.watchers = w }

// defaultWatchers are the ones built from the config: the local repository
// always, and GitHub — which only spends a request when a route actually waits
// for a pull request. Its token is optional: public repositories answer without
// one, at a rate limit low enough that the watcher paces itself.
func defaultWatchers(cfg Config) []vcs.Watcher {
	return []vcs.Watcher{
		&vcs.Git{Remote: cfg.GitRemote},
		&vcs.GitHub{Remote: cfg.GitRemote, Token: cfg.GithubTokenValue()},
	}
}

// vcsLoop polls on a schedule until the app shuts down.
func (m *Manager) vcsLoop() {
	defer m.wg.Done()
	ticker := time.NewTicker(m.cfg.VCSPoll())
	defer ticker.Stop()
	for {
		select {
		case <-m.rootCtx.Done():
			return
		case <-ticker.C:
			m.PollVCS()
		}
	}
}

// PollVCS asks every watcher about every branch somebody is waiting on, and
// delivers what is new to the flow engine.
func (m *Manager) PollVCS() {
	targets := m.FlowTargets()
	if len(targets) == 0 || len(m.watchers) == 0 {
		return
	}
	remote := m.gitRemote()
	for _, ft := range targets {
		target := vcs.Target{
			RepoPath: ft.RepoPath,
			Branch:   ft.Branch,
			Remote:   remote,
			Triggers: ft.Triggers,
		}
		for _, w := range m.watchers {
			ctx, cancel := context.WithTimeout(m.rootCtx, vcsPollTimeout)
			events, err := w.Poll(ctx, target)
			cancel()
			if err != nil {
				m.log.Warn("acp: repository poll failed", "watcher", w.Name(),
					"repo", ft.RepoPath, "branch", ft.Branch, "err", err)
			}
			for _, e := range events {
				m.deliverVCSEvent(e)
			}
		}
	}
}

// deliverVCSEvent drops an event the engine has already acted on. A watcher
// reports the state it sees, not a change: a merged branch stays merged, and
// without this the card would move on every poll.
func (m *Manager) deliverVCSEvent(e vcs.Event) {
	if m.store != nil {
		fresh, err := m.store.ClaimVCSEvent(e.RepoPath, e.Branch, e.Kind, e.Marker)
		if err != nil {
			m.log.Error("acp: vcs dedup failed", "err", err)
			return
		}
		if !fresh {
			return
		}
	}
	m.log.Info("acp: repository event", "kind", e.Kind, "repo", e.RepoPath, "branch", e.Branch)
	m.OnVCSEvent(VCSEvent{Kind: e.Kind, RepoPath: e.RepoPath, Branch: e.Branch, Detail: e.Detail})
}

func (m *Manager) gitRemote() string {
	m.cfgMu.RLock()
	defer m.cfgMu.RUnlock()
	return m.cfg.GitRemote
}
