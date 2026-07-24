package acp

import (
	"path/filepath"
	"strings"
	"testing"
)

func agentManager(t *testing.T, cfgPath string, agents ...AgentEntry) *Manager {
	t.Helper()
	cfg := DefaultConfig(t.TempDir())
	cfg.Agents = agents
	return NewManager(cfg, cfgPath, nil, newFakeWriter(), &fakeEmitter{}, nil)
}

func TestAddUpdateRemoveAgentPersists(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	m := agentManager(t, cfgPath)

	if _, err := m.AddAgent(AgentEntry{Name: "codex-a", Kind: "codex", Env: map[string]string{"CODEX_HOME": "/tmp/a"}}); err != nil {
		t.Fatal(err)
	}

	// Empty name and unknown kind are rejected.
	if _, err := m.AddAgent(AgentEntry{Name: "", Kind: "claude"}); err == nil {
		t.Error("empty name accepted")
	}
	if _, err := m.AddAgent(AgentEntry{Name: "bad", Kind: "gemini"}); err == nil {
		t.Error("unknown kind accepted")
	}
	// Duplicate name (case-insensitive) rejected.
	if _, err := m.AddAgent(AgentEntry{Name: "CODEX-A", Kind: "codex"}); err == nil {
		t.Error("duplicate name accepted")
	}

	// Persisted and reloadable.
	loaded, err := LoadConfig(cfgPath, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Agents) != 1 || loaded.Agents[0].Env["CODEX_HOME"] != "/tmp/a" {
		t.Fatalf("agent not persisted: %+v", loaded.Agents)
	}

	// Update replaces fields for the matching name.
	if _, err := m.UpdateAgent(AgentEntry{Name: "codex-a", Kind: "codex", Model: "gpt-5"}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.UpdateAgent(AgentEntry{Name: "missing", Kind: "codex"}); err == nil {
		t.Error("updating missing agent should fail")
	}
	loaded, _ = LoadConfig(cfgPath, t.TempDir())
	if loaded.Agents[0].Model != "gpt-5" {
		t.Fatalf("update not persisted: %+v", loaded.Agents)
	}

	if err := m.RemoveAgent("codex-a"); err != nil {
		t.Fatal(err)
	}
	if err := m.RemoveAgent("codex-a"); err == nil {
		t.Error("removing missing agent should fail")
	}
	loaded, _ = LoadConfig(cfgPath, t.TempDir())
	if len(loaded.Agents) != 0 {
		t.Fatalf("removal not persisted: %+v", loaded.Agents)
	}
}

func TestResolveAgentByOption(t *testing.T) {
	m := agentManager(t, "",
		AgentEntry{Name: "claude", Kind: "claude"},
		AgentEntry{Name: "codex-acct1", Kind: "codex"},
	)

	got, err := m.resolveAgent(CardMoved{OptionNames: []string{"urgent", "codex-acct1"}, Props: map[string]string{}})
	if err != nil || got.Name != "codex-acct1" {
		t.Fatalf("option match failed: got=%+v err=%v", got, err)
	}

	// Explicit `agent` property wins.
	got, err = m.resolveAgent(CardMoved{OptionNames: []string{"codex-acct1"}, Props: map[string]string{"agent": "claude"}})
	if err != nil || got.Name != "claude" {
		t.Fatalf("explicit agent property failed: got=%+v err=%v", got, err)
	}

	// Unknown explicit agent errors, listing the registry.
	_, err = m.resolveAgent(CardMoved{Props: map[string]string{"agent": "nope"}})
	if err == nil || !strings.Contains(err.Error(), "codex-acct1") {
		t.Errorf("expected error listing agents, got %v", err)
	}

	// Ambiguous (multiple agents, no selection) errors.
	if _, err := m.resolveAgent(CardMoved{Props: map[string]string{}}); err == nil {
		t.Error("ambiguous agent selection should error")
	}
}

func TestResolveAgentSingleAndFallback(t *testing.T) {
	// Exactly one agent → used without a card selection.
	m := agentManager(t, "", AgentEntry{Name: "only", Kind: "codex"})
	got, err := m.resolveAgent(CardMoved{Props: map[string]string{}})
	if err != nil || got.Name != "only" {
		t.Fatalf("single-agent resolution failed: got=%+v err=%v", got, err)
	}

	// Empty registry → synthesized from AgentMode (default claude).
	m2 := agentManager(t, "")
	got, err = m2.resolveAgent(CardMoved{Props: map[string]string{}})
	if err != nil || got.Kind != AgentKindClaude {
		t.Fatalf("empty-registry fallback failed: got=%+v err=%v", got, err)
	}

	// Empty registry with acp-command mode → external kind.
	m3 := agentManager(t, "")
	m3.cfg.AgentMode = "acp-command"
	got, _ = m3.resolveAgent(CardMoved{Props: map[string]string{}})
	if got.Kind != "acp-command" {
		t.Fatalf("expected acp-command fallback, got %+v", got)
	}
}
