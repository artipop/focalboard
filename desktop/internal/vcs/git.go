package vcs

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Git watches the repository itself: no tokens, no API, works with any hosting.
// It answers the two questions that do not need a forge — is the branch on the
// remote, and has it landed on the default branch.
type Git struct {
	// Remote is consulted when a target does not name one.
	Remote string
	// Run is the command seam; nil means the real git.
	Run Runner
}

// Runner executes a git command in a repository and returns its output.
type Runner func(ctx context.Context, repo string, args ...string) (string, error)

// Exec is the real Runner.
func Exec(ctx context.Context, repo string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", repo}, args...)...)
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil {
		return text, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, text)
	}
	return text, nil
}

func (g *Git) Name() string { return "git" }

func (g *Git) run(ctx context.Context, repo string, args ...string) (string, error) {
	if g.Run != nil {
		return g.Run(ctx, repo, args...)
	}
	return Exec(ctx, repo, args...)
}

// Poll fetches the remote once and answers whatever the target waits for.
func (g *Git) Poll(ctx context.Context, t Target) ([]Event, error) {
	wantsPushed := t.Wants(KindBranchPushed)
	wantsMerged := t.Wants(KindBranchMerged)
	if !wantsPushed && !wantsMerged {
		return nil, nil
	}
	if t.RepoPath == "" || t.Branch == "" {
		return nil, nil
	}
	remote := t.RemoteOr(g.Remote)

	// One fetch answers both questions. --prune keeps a deleted branch from
	// looking alive forever.
	if _, err := g.run(ctx, t.RepoPath, "fetch", "--quiet", "--prune", remote); err != nil {
		return nil, fmt.Errorf("не удалось обновить %s: %w", remote, err)
	}

	var events []Event
	remoteSHA, onRemote := g.remoteSHA(ctx, t, remote)
	if onRemote {
		g.rememberTip(ctx, t, remoteSHA)
	}
	if wantsPushed && onRemote {
		events = append(events, Event{
			Kind: KindBranchPushed, RepoPath: t.RepoPath, Branch: t.Branch,
			Detail: fmt.Sprintf("ветка `%s` запушена в %s", t.Branch, remote),
			Marker: remoteSHA, At: time.Now(),
		})
	}
	if wantsMerged {
		merged, base, err := g.merged(ctx, t, remote)
		if err != nil {
			return events, err
		}
		if merged {
			events = append(events, Event{
				Kind: KindBranchMerged, RepoPath: t.RepoPath, Branch: t.Branch,
				Detail: fmt.Sprintf("ветка `%s` влита в `%s`", t.Branch, base),
				Marker: base + ":" + t.Branch, At: time.Now(),
			})
		}
	}
	return events, nil
}

// remoteSHA is the commit the branch points at on the remote, and whether it is
// there at all.
func (g *Git) remoteSHA(ctx context.Context, t Target, remote string) (string, bool) {
	out, err := g.run(ctx, t.RepoPath, "ls-remote", "--heads", remote, "refs/heads/"+t.Branch)
	if err != nil || strings.TrimSpace(out) == "" {
		return "", false
	}
	fields := strings.Fields(out)
	if len(fields) == 0 {
		return "", false
	}
	return fields[0], true
}

// merged reports whether the branch has landed on the remote's default branch.
func (g *Git) merged(ctx context.Context, t Target, remote string) (bool, string, error) {
	base, err := g.defaultBranch(ctx, t.RepoPath, remote)
	if err != nil {
		return false, "", err
	}
	// A branch cannot be merged into itself, and the default branch is always
	// its own ancestor — which would fire the event for every card on trunk.
	if strings.EqualFold(base, t.Branch) {
		return false, base, nil
	}
	tip, err := g.branchTip(ctx, t, remote)
	if err != nil || tip == "" {
		return false, base, err
	}
	_, err = g.run(ctx, t.RepoPath, "merge-base", "--is-ancestor", tip, remote+"/"+base)
	if err != nil {
		// A non-zero exit is the answer "not merged", not a failure. Anything
		// else (a missing ref) surfaces as an error only when the tip is gone,
		// which branchTip already handled.
		return false, base, nil
	}
	return true, base, nil
}

// seenRef is where the watcher remembers a branch's tip. Landing a pull request
// usually deletes the branch, and then neither the local nor the remote ref is
// left to test for ancestry — but the commit itself is still in the default
// branch's history, so remembering the tip while the branch existed is what
// makes "merged" answerable afterwards. It lives under refs/focalboard/, so it
// is invisible to `git branch`, never pushed, and keeps the object alive.
func seenRef(branch string) string { return "refs/focalboard/seen/" + branch }

// rememberTip records where the branch stood, so a later poll can still answer
// "was it merged" once the branch itself is gone.
func (g *Git) rememberTip(ctx context.Context, t Target, sha string) {
	if sha == "" {
		return
	}
	// Failing to remember only costs us the post-deletion answer; it must not
	// fail the poll.
	_, _ = g.run(ctx, t.RepoPath, "update-ref", seenRef(t.Branch), sha)
}

// branchTip is the commit to test for ancestry: the local branch, what the
// remote has, or the tip we saw last time.
func (g *Git) branchTip(ctx context.Context, t Target, remote string) (string, error) {
	refs := []string{
		"refs/heads/" + t.Branch,
		"refs/remotes/" + remote + "/" + t.Branch,
		seenRef(t.Branch),
	}
	for _, ref := range refs {
		if out, err := g.run(ctx, t.RepoPath, "rev-parse", "--verify", "--quiet", ref); err == nil {
			if sha := strings.TrimSpace(out); sha != "" {
				return sha, nil
			}
		}
	}
	// The branch is gone and was never seen: there is nothing to test, and
	// saying so is not an error.
	return "", nil
}

// defaultBranch is the remote's HEAD — main, master or whatever the project
// uses. The symbolic ref is only present after a clone or an explicit
// set-head, so a guess follows.
func (g *Git) defaultBranch(ctx context.Context, repo, remote string) (string, error) {
	if out, err := g.run(ctx, repo, "symbolic-ref", "--quiet", "--short", "refs/remotes/"+remote+"/HEAD"); err == nil {
		if name := strings.TrimPrefix(strings.TrimSpace(out), remote+"/"); name != "" {
			return name, nil
		}
	}
	for _, guess := range []string{"main", "master"} {
		if _, err := g.run(ctx, repo, "rev-parse", "--verify", "--quiet", "refs/remotes/"+remote+"/"+guess); err == nil {
			return guess, nil
		}
	}
	return "", fmt.Errorf("не удалось определить основную ветку %s", remote)
}
