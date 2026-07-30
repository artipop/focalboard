package acp

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/mattermost/focalboard/desktop/internal/dokku"
)

// WorktreeInfo describes a session's dedicated git worktree.
type WorktreeInfo struct {
	Path    string
	Branch  string
	BaseRef string
}

// worktreeLocks serializes worktree creation per repository to avoid
// concurrent git index locks.
var worktreeLocks sync.Map // repo path → *sync.Mutex

func repoLock(repo string) *sync.Mutex {
	mu, _ := worktreeLocks.LoadOrStore(repo, &sync.Mutex{})
	return mu.(*sync.Mutex)
}

func gitCmd(ctx context.Context, repo string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", repo}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// CreateWorktree adds a new worktree for the session off baseBranch (or HEAD
// when empty), on a fresh branch named after the card (see WorktreeBranch).
func CreateWorktree(ctx context.Context, repo, baseBranch, title, cardID, sessionID, worktreeRoot string) (WorktreeInfo, error) {
	mu := repoLock(repo)
	mu.Lock()
	defer mu.Unlock()

	if _, err := gitCmd(ctx, repo, "rev-parse", "--git-dir"); err != nil {
		return WorktreeInfo{}, fmt.Errorf("%s is not a git repository: %w", repo, err)
	}

	baseRef := strings.TrimSpace(baseBranch)
	if baseRef == "" {
		baseRef = "HEAD"
	} else if _, err := gitCmd(ctx, repo, "rev-parse", "--verify", baseRef); err != nil {
		return WorktreeInfo{}, fmt.Errorf("base branch %q not found in %s", baseRef, repo)
	}

	branch := WorktreeBranch(title, cardID, sessionID)
	path := filepath.Join(worktreeRoot, fmt.Sprintf("%s-%s", filepath.Base(repo), shortID(sessionID)))
	if err := os.MkdirAll(worktreeRoot, 0o755); err != nil {
		return WorktreeInfo{}, err
	}
	if _, err := gitCmd(ctx, repo, "worktree", "add", "-b", branch, path, baseRef); err != nil {
		return WorktreeInfo{}, err
	}
	return WorktreeInfo{Path: path, Branch: branch, BaseRef: baseRef}, nil
}

// RemoveWorktreeIfClean removes the worktree (and its branch) only when it has
// no uncommitted changes and no commits ahead of its base ref.
func RemoveWorktreeIfClean(ctx context.Context, repo string, w WorktreeInfo) (bool, error) {
	mu := repoLock(repo)
	mu.Lock()
	defer mu.Unlock()

	status, err := gitCmd(ctx, w.Path, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	if status != "" {
		return false, nil
	}
	ahead, err := gitCmd(ctx, w.Path, "rev-list", "--count", w.BaseRef+"..HEAD")
	if err == nil && ahead != "0" {
		return false, nil
	}
	if _, err := gitCmd(ctx, repo, "worktree", "remove", "--force", w.Path); err != nil {
		return false, err
	}
	_, _ = gitCmd(ctx, repo, "branch", "-D", w.Branch)
	return true, nil
}

// PruneStale runs `git worktree prune` on every known repo, cleaning up
// records of worktrees whose directories are gone.
func PruneStale(ctx context.Context, repos []string) {
	for _, repo := range repos {
		_, _ = gitCmd(ctx, repo, "worktree", "prune")
	}
}

// WorktreeBranch names a session's branch after the card it works on:
// "Login via SSO" → acp/login-via-sso-1a2b. The branch is what the card shows
// and what a deploy publishes, so it ends up in a preview hostname — hence the
// same folding a Dokku app name gets (dokku.AppSlug: lowercase, [a-z0-9-], cut
// with a hash), and a blank title falls back to the card id. A title with no
// ASCII in it keeps only what folding leaves, which for a fully non-Latin title
// is AppSlug's hash: unreadable, but valid and unique.
//
// The tail is the session id's own short form, because two sessions on one card
// — or two cards sharing a title — would otherwise collide on
// `git worktree add -b`, and a shorter tail collides for real (four sessions
// named sess{a,b,c,d} all begin with "sess").
func WorktreeBranch(title, cardID, sessionID string) string {
	slug := dokku.AppSlug(title)
	if strings.TrimSpace(title) == "" {
		slug = shortID(cardID)
	}
	return fmt.Sprintf("acp/%s-%s", slug, shortID(sessionID))
}

func shortID(id string) string {
	id = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		}
		return -1
	}, id)
	if len(id) > 8 {
		return id[:8]
	}
	if id == "" {
		return "x"
	}
	return id
}
