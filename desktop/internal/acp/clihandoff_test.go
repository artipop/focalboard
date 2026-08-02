package acp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Claude's Remote Control is a flag of the CLI and nothing in ACP, so no probe
// can find it: the only way is the door the adapter documents — session/new's
// _meta, where extraArgs reach the CLI it spawns.
func TestCLIArgsReachTheAgentThroughSessionMeta(t *testing.T) {
	script := writeFakeAgent(t, fakeClaudeHappy)
	m, _, events, repo := testManager(t, fakeClaudeHappy, func(c *Config) {
		c.Agents = []AgentEntry{{
			Name:    "remote",
			Kind:    "claude",
			BinPath: script,
			CLIArgs: []string{"--remote-control", "--remote-control-session-name-prefix", "board"},
		}}
	})

	ev := moveEvent("cardRemote", repo, "opt-backlog", "opt-agent")
	ev.OptionNames = []string{"remote"}
	events.ch <- ev

	waitFor(t, 15*time.Second, "session done", func() bool {
		sessions, _, err := m.store.SessionsForCard("cardRemote")
		return err == nil && len(sessions) == 1 && sessions[0].Status == StatusDone
	})

	raw, err := os.ReadFile(filepath.Join(fakeAgentDir(script), "meta.json"))
	if err != nil {
		t.Fatalf("the agent recorded no _meta: %v", err)
	}
	var meta struct {
		ClaudeCode struct {
			Options struct {
				ExtraArgs map[string]string `json:"extraArgs"`
			} `json:"options"`
		} `json:"claudeCode"`
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		t.Fatalf("_meta %s: %v", raw, err)
	}
	want := map[string]string{"remote-control": "", "remote-control-session-name-prefix": "board"}
	if !reflect.DeepEqual(meta.ClaudeCode.Options.ExtraArgs, want) {
		t.Errorf("extraArgs = %#v, want %#v (raw _meta: %s)", meta.ClaudeCode.Options.ExtraArgs, want, raw)
	}
}

// An agent with nothing to hand over sends no _meta at all: the field exists
// for the CLI behind an adapter, not as something every session carries.
func TestNoCLIArgsMeansNoSessionMeta(t *testing.T) {
	if meta := sessionMeta(AgentEntry{Name: "plain", Kind: "claude"}); meta != nil {
		t.Errorf("sessionMeta = %#v, want nothing", meta)
	}
}

// A kind whose adapter has no such channel is refused when it is saved, rather
// than accepting a setting that would quietly do nothing.
func TestCLIArgsAreRefusedForAKindWithoutTheChannel(t *testing.T) {
	_, err := validateAgent(AgentEntry{Name: "cx", Kind: "codex", CLIArgs: []string{"--remote-control"}})
	if err == nil {
		t.Fatalf("a codex agent accepted CLI arguments it has nowhere to put")
	}
	if !strings.Contains(err.Error(), "codex") {
		t.Errorf("the refusal does not say which agent it is about: %v", err)
	}

	// And a value that is not a flag is a typo, not an argument.
	if _, err := validateAgent(AgentEntry{Name: "cl", Kind: "claude", CLIArgs: []string{"remote-control"}}); err == nil {
		t.Errorf("an argument without a dash was accepted")
	}

	// The normal case still saves, trimmed.
	entry, err := validateAgent(AgentEntry{Name: "cl", Kind: "claude", CLIArgs: []string{" --remote-control ", "  "}})
	if err != nil {
		t.Fatalf("validateAgent: %v", err)
	}
	if !reflect.DeepEqual(entry.CLIArgs, []string{"--remote-control"}) {
		t.Errorf("cliArgs = %#v, want just --remote-control", entry.CLIArgs)
	}
}

// The three ways a flag is written, in the shape the SDK takes them.
func TestExtraArgsSpellings(t *testing.T) {
	got := extraArgs([]string{
		"--remote-control",
		"--remote-control-session-name-prefix", "my board",
		"--fallback-model=sonnet",
		"--verbose",
	})
	want := map[string]any{
		"remote-control":                     "",
		"remote-control-session-name-prefix": "my board",
		"fallback-model":                     "sonnet",
		"verbose":                            "",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("extraArgs = %#v, want %#v", got, want)
	}
}
