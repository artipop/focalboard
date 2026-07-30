package acp

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func registryManager(t *testing.T, cfgPath string, repos ...RepoEntry) *Manager {
	t.Helper()
	cfg := DefaultConfig(t.TempDir())
	cfg.Repos = repos
	return NewManager(cfg, cfgPath, nil, newFakeWriter(), &fakeEmitter{}, nil)
}

func TestAddRemoveRepoPersists(t *testing.T) {
	repo := initTestRepo(t)
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	m := registryManager(t, cfgPath)

	entry, err := m.AddRepo("", repo)
	if err != nil {
		t.Fatal(err)
	}
	if entry.Name != filepath.Base(repo) {
		t.Errorf("default name should be basename, got %q", entry.Name)
	}

	// Duplicates by name and by path are rejected.
	if _, err := m.AddRepo(entry.Name, t.TempDir()); err == nil {
		t.Error("duplicate name accepted")
	}
	if _, err := m.AddRepo("other", repo); err == nil {
		t.Error("duplicate path accepted")
	}
	// Non-git directories are rejected.
	if _, err := m.AddRepo("notgit", t.TempDir()); err == nil {
		t.Error("non-git dir accepted")
	}

	// Persisted and reloadable.
	loaded, err := LoadConfig(cfgPath, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Repos) != 1 || loaded.Repos[0].Path != repo {
		t.Fatalf("registry not persisted: %+v", loaded.Repos)
	}

	if err := m.RemoveRepo(entry.Name); err != nil {
		t.Fatal(err)
	}
	if err := m.RemoveRepo(entry.Name); err == nil {
		t.Error("removing missing entry should fail")
	}
	loaded, _ = LoadConfig(cfgPath, t.TempDir())
	if len(loaded.Repos) != 0 {
		t.Fatalf("removal not persisted: %+v", loaded.Repos)
	}
}

func TestResolveRepoByTag(t *testing.T) {
	repo := initTestRepo(t)
	m := registryManager(t, "", RepoEntry{Name: "MyRepo", Path: repo})

	ev := CardMoved{OptionNames: []string{"urgent", "myrepo"}, Props: map[string]string{}}
	got, err := m.resolveRepo(ev)
	if err != nil || got != repo {
		t.Fatalf("tag match failed: got=%q err=%v", got, err)
	}

	// No matching tag → error naming the registry entries.
	_, err = m.resolveRepo(CardMoved{OptionNames: []string{"urgent"}, Props: map[string]string{}})
	if err == nil || !strings.Contains(err.Error(), "MyRepo") {
		t.Errorf("expected mismatch error listing repos, got %v", err)
	}

	// A card dragged out of a column named after the repo matches too
	// (repo-lane boards: the trigger move erases the tag from the card).
	got, err = m.resolveRepo(CardMoved{
		Props:      map[string]string{},
		FromColumn: Column{PropertyName: "Status", Name: "myrepo"},
		ToColumn:   Column{PropertyName: "Status", Name: DefaultTriggerColumn},
	})
	if err != nil || got != repo {
		t.Fatalf("from-column match failed: got=%q err=%v", got, err)
	}

	// Registered repo with a dead path → clear error.
	m2 := registryManager(t, "", RepoEntry{Name: "gone", Path: "/no/such/dir"})
	if _, err := m2.resolveRepo(CardMoved{OptionNames: []string{"gone"}}); err == nil {
		t.Error("dead registry path should error")
	}
}

func TestResolveRepoExplicitOverride(t *testing.T) {
	repo := initTestRepo(t)
	other := initTestRepo(t)
	m := registryManager(t, "", RepoEntry{Name: "tagged", Path: other})

	// Explicit repo_path wins over tags, and registered paths are allowed
	// without being whitelisted.
	ev := CardMoved{
		Props:       map[string]string{"repo_path": repo},
		OptionNames: []string{"tagged"},
	}
	if _, err := m.resolveRepo(ev); err == nil {
		t.Fatal("unregistered repo_path should be rejected (not whitelisted)")
	}

	ev.Props["repo_path"] = other
	got, err := m.resolveRepo(ev)
	if err != nil || got != other {
		t.Fatalf("registered repo_path should be allowed: got=%q err=%v", got, err)
	}
}

func TestTriggerSessionViaTag(t *testing.T) {
	m, writer, events, repo := testManager(t, fakeClaudeHappy, nil)
	if _, err := m.AddRepo("boardrepo", repo); err != nil {
		t.Fatal(err)
	}

	ev := moveEvent("card10", "", "opt-backlog", "opt-agent")
	delete(ev.Props, "repo_path")
	ev.Props["repo_path"] = ""             // no explicit path
	ev.OptionNames = []string{"BoardRepo"} // tag, case-insensitive
	events.ch <- ev

	waitFor(t, 15*time.Second, "tag-mapped session done", func() bool {
		sessions, _, err := m.store.SessionsForCard("card10")
		return err == nil && len(sessions) == 1 && sessions[0].Status == StatusDone
	})
	// The tag picked the repository; the session runs in that repository's
	// worktree, which is named after it.
	sessions, _, _ := m.store.SessionsForCard("card10")
	if wt := sessions[0].WorktreePath; !strings.Contains(filepath.Base(wt), filepath.Base(repo)) {
		t.Errorf("expected a worktree of %q, got %q", repo, wt)
	}
	if got := writer.cardComments("card10"); len(got) < 2 {
		t.Errorf("expected comments, got %v", got)
	}
}
