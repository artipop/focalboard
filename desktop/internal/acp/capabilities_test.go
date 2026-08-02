package acp

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// What an agent can be configured with is the agent's own answer, not a table
// of ours: the probe starts it, opens a throwaway session and reads what it
// declared.
func TestAgentOptionsComeFromTheAgentItself(t *testing.T) {
	script := writeFakeAgent(t, fakeClaudeHappy)
	m, _, _, _ := testManager(t, fakeClaudeHappy, nil)

	options, err := m.AgentOptions(AgentEntry{Name: "probed", Kind: "codex", BinPath: script}, false)
	if err != nil {
		t.Fatalf("AgentOptions: %v", err)
	}

	byID := map[string]AgentOption{}
	for _, opt := range options {
		byID[opt.ID] = opt
	}
	fast, ok := byID["fast"]
	if !ok {
		t.Fatalf("the toggle the agent declared is missing; got %+v", options)
	}
	if fast.Type != agentOptionBoolean || fast.Name != "Fast mode" || fast.Current != "false" {
		t.Errorf("fast option = %+v, want a boolean named \"Fast mode\", currently off", fast)
	}
	effort, ok := byID["effort"]
	if !ok {
		t.Fatalf("the select the agent declared is missing; got %+v", options)
	}
	if effort.Type != agentOptionSelect || len(effort.Values) != 2 || effort.Values[1].Value != "high" {
		t.Errorf("effort option = %+v, want a select offering default/high", effort)
	}
	if _, offered := byID["remote-control"]; offered {
		t.Errorf("an option the agent never mentioned was offered: %+v", options)
	}

	// The form asks before the entry has a name: an agent is chosen first and
	// named after, and the name has nothing to do with what it can do.
	if _, err := m.AgentOptions(AgentEntry{Kind: "codex", BinPath: script}, true); err != nil {
		t.Errorf("an unnamed entry could not be asked: %v", err)
	}
}

// The second ask is answered from the cache, so opening the dialog does not
// start an agent every time. A refresh asks again.
func TestAgentOptionsAreCachedPerLaunch(t *testing.T) {
	script := writeFakeAgent(t, fakeClaudeHappy)
	m, _, _, _ := testManager(t, fakeClaudeHappy, nil)
	entry := AgentEntry{Name: "probed", Kind: "codex", BinPath: script}

	if _, err := m.AgentOptions(entry, false); err != nil {
		t.Fatalf("AgentOptions: %v", err)
	}
	// Make the agent unstartable: a cached answer needs no process.
	if err := os.Remove(script); err != nil {
		t.Fatal(err)
	}
	if _, err := m.AgentOptions(entry, false); err != nil {
		t.Fatalf("cached AgentOptions: %v", err)
	}
	if _, err := m.AgentOptions(entry, true); err == nil {
		t.Errorf("a refresh answered from the cache instead of starting the agent again")
	}
	// A different launch is a different question, cache or not.
	if _, err := m.AgentOptions(AgentEntry{Name: "probed", Kind: "codex", BinPath: script + "-gone"}, false); err == nil {
		t.Errorf("another binary answered from the first one's cache")
	}
}

// A setting chosen on the agent is applied to its session, in both shapes ACP
// gives it: a select value and a boolean toggle.
func TestAgentOptionsAreAppliedToTheSession(t *testing.T) {
	script := writeFakeAgent(t, fakeClaudeHappy)
	m, _, events, repo := testManager(t, fakeClaudeHappy, func(c *Config) {
		c.Agents = []AgentEntry{{
			Name:    "tuned",
			Kind:    "codex",
			BinPath: script,
			Options: map[string]string{
				"effort": "high",
				"fast":   "on",
				// Something this agent does not have: skipped, never fatal.
				"remote-control": "on",
			},
		}}
	})

	ev := moveEvent("cardOptions", repo, "opt-backlog", "opt-agent")
	ev.OptionNames = []string{"tuned"}
	events.ch <- ev

	waitFor(t, 15*time.Second, "session done", func() bool {
		sessions, _, err := m.store.SessionsForCard("cardOptions")
		return err == nil && len(sessions) == 1 && sessions[0].Status == StatusDone
	})

	dir := fakeAgentDir(script)
	if effort, err := os.ReadFile(filepath.Join(dir, "effort.txt")); err != nil || string(effort) != "high" {
		t.Errorf("effort = %q (err %v), want high", effort, err)
	}
	if fast, err := os.ReadFile(filepath.Join(dir, "fast.txt")); err != nil || string(fast) != "true" {
		t.Errorf("fast = %q (err %v), want true — \"on\" is how the dialog writes a toggle", fast, err)
	}
}

// The user's own choice is applied after the kind's table, so asking for codex's
// read-only mode by hand means read-only rather than the "agent" mode the table
// switches every codex session into.
func TestAgentOptionsOutrankTheKindDefaults(t *testing.T) {
	script := writeFakeAgent(t, fakeClaudeHappy)
	m, _, events, repo := testManager(t, fakeClaudeHappy, func(c *Config) {
		c.Agents = []AgentEntry{{
			Name:    "careful",
			Kind:    "codex",
			BinPath: script,
			Model:   "gpt-5.4",
			Options: map[string]string{"model": "gpt-5.6-sol"},
		}}
	})

	ev := moveEvent("cardOverride", repo, "opt-backlog", "opt-agent")
	ev.OptionNames = []string{"careful"}
	events.ch <- ev

	waitFor(t, 15*time.Second, "session done", func() bool {
		sessions, _, err := m.store.SessionsForCard("cardOverride")
		return err == nil && len(sessions) == 1 && sessions[0].Status == StatusDone
	})

	model, err := os.ReadFile(filepath.Join(fakeAgentDir(script), "model.txt"))
	if err != nil || string(model) != "gpt-5.6-sol" {
		t.Errorf("model = %q (err %v), want gpt-5.6-sol — the option is set last and wins", model, err)
	}
}

func TestConfigRequestReadsToggleSpellings(t *testing.T) {
	for _, value := range []string{"on", "true", "YES", "1", "enabled"} {
		if got, err := parseOptionBool(value); err != nil || !got {
			t.Errorf("parseOptionBool(%q) = %v, %v; want true", value, got, err)
		}
	}
	for _, value := range []string{"off", "false", "No", "0", "disabled"} {
		if got, err := parseOptionBool(value); err != nil || got {
			t.Errorf("parseOptionBool(%q) = %v, %v; want false", value, got, err)
		}
	}
	if _, err := parseOptionBool("maybe"); err == nil {
		t.Errorf("parseOptionBool(\"maybe\") was accepted")
	}
}

// An option left blank is not stored: it says the same as leaving it out, and
// only makes the config file harder to read.
func TestEmptyAgentOptionsAreDropped(t *testing.T) {
	entry, err := validateAgent(AgentEntry{
		Name:    "a",
		Kind:    "claude",
		Options: map[string]string{" fast ": " on ", "effort": "", "": "high"},
	})
	if err != nil {
		t.Fatalf("validateAgent: %v", err)
	}
	if len(entry.Options) != 1 || entry.Options["fast"] != "on" {
		t.Errorf("options = %#v, want just fast=on", entry.Options)
	}
}
