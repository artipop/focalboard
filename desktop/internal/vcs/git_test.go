package vcs

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The git watcher is tested against real repositories: its whole job is knowing
// what git says, and a fake runner would only test our idea of that.

func git(t *testing.T, repo string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
	cmd.Env = append(cmd.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// twoRepos builds a bare "remote" with a main branch and a clone of it.
func twoRepos(t *testing.T) (clone, remote string) {
	t.Helper()
	root := t.TempDir()
	remote = filepath.Join(root, "remote.git")
	seed := filepath.Join(root, "seed")

	git(t, root, "init", "--bare", "--initial-branch=main", remote)
	git(t, root, "init", "--initial-branch=main", seed)
	if err := writeFile(filepath.Join(seed, "README.md"), "hello\n"); err != nil {
		t.Fatal(err)
	}
	git(t, seed, "add", ".")
	git(t, seed, "commit", "-m", "init")
	git(t, seed, "remote", "add", "origin", remote)
	git(t, seed, "push", "-u", "origin", "main")

	clone = filepath.Join(root, "clone")
	git(t, root, "clone", remote, clone)
	return clone, remote
}

func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}

func TestGitReportsAPushedBranch(t *testing.T) {
	clone, _ := twoRepos(t)
	w := &Git{}
	target := Target{RepoPath: clone, Branch: "feat/x", Triggers: []string{KindBranchPushed}}

	// A branch that exists only locally is not pushed.
	git(t, clone, "checkout", "-q", "-b", "feat/x")
	if err := writeFile(filepath.Join(clone, "a.txt"), "a\n"); err != nil {
		t.Fatal(err)
	}
	git(t, clone, "add", ".")
	git(t, clone, "commit", "-m", "work")

	events, err := w.Poll(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("a local-only branch is not pushed: %+v", events)
	}

	git(t, clone, "push", "-u", "origin", "feat/x")
	events, err = w.Poll(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Kind != KindBranchPushed {
		t.Fatalf("events: %+v", events)
	}
	if events[0].Marker == "" || events[0].Branch != "feat/x" || events[0].RepoPath != clone {
		t.Fatalf("event: %+v", events[0])
	}
	// The marker follows the commit, so pushing again is a new occurrence.
	first := events[0].Marker
	if err := writeFile(filepath.Join(clone, "b.txt"), "b\n"); err != nil {
		t.Fatal(err)
	}
	git(t, clone, "add", ".")
	git(t, clone, "commit", "-m", "more")
	git(t, clone, "push")
	events, _ = w.Poll(context.Background(), target)
	if len(events) != 1 || events[0].Marker == first {
		t.Fatalf("marker did not follow the branch: %+v", events)
	}
}

func TestGitReportsAMergedBranch(t *testing.T) {
	clone, _ := twoRepos(t)
	w := &Git{}
	target := Target{RepoPath: clone, Branch: "feat/y", Triggers: []string{KindBranchMerged}}

	git(t, clone, "checkout", "-q", "-b", "feat/y")
	if err := writeFile(filepath.Join(clone, "c.txt"), "c\n"); err != nil {
		t.Fatal(err)
	}
	git(t, clone, "add", ".")
	git(t, clone, "commit", "-m", "work")
	git(t, clone, "push", "-u", "origin", "feat/y")

	events, err := w.Poll(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("an unmerged branch must produce nothing: %+v", events)
	}

	// Merge it the way a forge would: onto main, pushed to the remote.
	git(t, clone, "checkout", "-q", "main")
	git(t, clone, "merge", "--no-ff", "-m", "merge feat/y", "feat/y")
	git(t, clone, "push")

	events, err = w.Poll(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Kind != KindBranchMerged {
		t.Fatalf("events: %+v", events)
	}
	if !strings.Contains(events[0].Detail, "feat/y") {
		t.Fatalf("detail should name the branch: %q", events[0].Detail)
	}

	// It stays merged after the branch is deleted — that is the usual state
	// after a pull request lands, and it must still be recognised.
	git(t, clone, "branch", "-D", "feat/y")
	git(t, clone, "push", "origin", "--delete", "feat/y")
	events, err = w.Poll(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("a merged and deleted branch: %+v", events)
	}
}

func TestGitDoesNotMergeTheDefaultBranchIntoItself(t *testing.T) {
	clone, _ := twoRepos(t)
	w := &Git{}
	events, err := w.Poll(context.Background(), Target{
		RepoPath: clone, Branch: "main", Triggers: []string{KindBranchMerged, KindBranchPushed},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range events {
		if e.Kind == KindBranchMerged {
			t.Fatalf("main is always its own ancestor; that is not a merge: %+v", e)
		}
	}
}

func TestGitAsksForNothingItIsNotWaitedOn(t *testing.T) {
	clone, _ := twoRepos(t)
	var ran []string
	w := &Git{Run: func(ctx context.Context, repo string, args ...string) (string, error) {
		ran = append(ran, strings.Join(args, " "))
		return Exec(ctx, repo, args...)
	}}

	// Only GitHub triggers: the git watcher must not even fetch.
	if _, err := w.Poll(context.Background(), Target{
		RepoPath: clone, Branch: "feat/x", Triggers: []string{KindPRMerged},
	}); err != nil {
		t.Fatal(err)
	}
	if len(ran) != 0 {
		t.Fatalf("nothing should have run: %v", ran)
	}
}

func TestGitFindsTheDefaultBranchWhateverItIsCalled(t *testing.T) {
	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	seed := filepath.Join(root, "seed")
	git(t, root, "init", "--bare", "--initial-branch=trunk", remote)
	git(t, root, "init", "--initial-branch=trunk", seed)
	if err := writeFile(filepath.Join(seed, "f.txt"), "x\n"); err != nil {
		t.Fatal(err)
	}
	git(t, seed, "add", ".")
	git(t, seed, "commit", "-m", "init")
	git(t, seed, "remote", "add", "origin", remote)
	git(t, seed, "push", "-u", "origin", "trunk")
	clone := filepath.Join(root, "clone")
	git(t, root, "clone", remote, clone)

	w := &Git{}
	base, err := w.defaultBranch(context.Background(), clone, "origin")
	if err != nil || base != "trunk" {
		t.Fatalf("default branch: %q, %v", base, err)
	}
}
