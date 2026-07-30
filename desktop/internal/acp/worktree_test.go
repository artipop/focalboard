package acp

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func initTestRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init", "-b", "main")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-m", "init")
	return repo
}

func TestWorktreeBranchNamesTheCard(t *testing.T) {
	// The readable case: the branch (and so the preview address) says what the
	// card is about.
	got := WorktreeBranch("Login via SSO", "card-1234abcd", "sess-5678efgh")
	if !strings.HasPrefix(got, "acp/login-via-sso-") {
		t.Errorf("branch %q, want it named after the title", got)
	}

	// Two cards with the same title must not share a branch.
	if WorktreeBranch("Same", "card-a", "sess-a") == WorktreeBranch("Same", "card-b", "sess-b") {
		t.Error("two cards with one title collided on a branch")
	}
	// Nor two sessions of one card, which is what the tail is for.
	if WorktreeBranch("Same", "card-a", "sess-1") == WorktreeBranch("Same", "card-a", "sess-2") {
		t.Error("two sessions of one card collided on a branch")
	}

	// A blank title falls back to the card, a non-Latin one to whatever folding
	// leaves; both must still be a valid ref of the same shape.
	for _, name := range []string{WorktreeBranch("", "card-1234abcd", "sess-1"), WorktreeBranch("Задача", "card-1234abcd", "sess-1")} {
		if !strings.HasPrefix(name, "acp/") || strings.ContainsAny(name, " ~^:?*[\\") {
			t.Errorf("branch %q is not a usable ref", name)
		}
	}
}

func TestCreateWorktreeNamingAndBase(t *testing.T) {
	repo := initTestRepo(t)
	root := t.TempDir()
	ctx := context.Background()

	wt, err := CreateWorktree(ctx, repo, "", "Логин через SSO", "card-1234abcd", "sess-5678efgh", root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(wt.Branch, "acp/") {
		t.Errorf("branch %q lacks acp/ prefix", wt.Branch)
	}
	if !strings.HasPrefix(wt.Path, root) {
		t.Errorf("worktree path %q outside root %q", wt.Path, root)
	}
	if _, err := os.Stat(filepath.Join(wt.Path, "README.md")); err != nil {
		t.Errorf("worktree missing checked-out file: %v", err)
	}
}

func TestCreateWorktreeBadBase(t *testing.T) {
	repo := initTestRepo(t)
	if _, err := CreateWorktree(context.Background(), repo, "no-such-branch", "title", "c", "s", t.TempDir()); err == nil {
		t.Fatal("expected error for missing base branch")
	}
}

func TestRemoveWorktreeCleanVsDirty(t *testing.T) {
	repo := initTestRepo(t)
	ctx := context.Background()

	clean, err := CreateWorktree(ctx, repo, "", "clean card", "card1", "sessclean", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	removed, err := RemoveWorktreeIfClean(ctx, repo, clean)
	if err != nil || !removed {
		t.Fatalf("clean worktree should be removed: removed=%v err=%v", removed, err)
	}

	dirty, err := CreateWorktree(ctx, repo, "", "dirty card", "card2", "sessdirty", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dirty.Path, "new.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	removed, err = RemoveWorktreeIfClean(ctx, repo, dirty)
	if err != nil {
		t.Fatal(err)
	}
	if removed {
		t.Fatal("dirty worktree must be kept")
	}
	if _, err := os.Stat(dirty.Path); err != nil {
		t.Fatalf("dirty worktree directory vanished: %v", err)
	}
}

func TestConcurrentWorktreeCreation(t *testing.T) {
	repo := initTestRepo(t)
	root := t.TempDir()
	var wg sync.WaitGroup
	errs := make([]error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = CreateWorktree(context.Background(), repo, "", "card", "card", "sess"+string(rune('a'+i)), root)
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("concurrent worktree %d: %v", i, err)
		}
	}
}
