package acp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCodexAgentRunsWithIsolatedEnv(t *testing.T) {
	codexScript := writeFakeAgent(t, fakeCodexEnv)
	m, writer, events, repo := testManager(t, fakeClaudeHappy, func(c *Config) {
		c.Agents = []AgentEntry{{
			Name:    "codexagent",
			Kind:    "codex",
			BinPath: codexScript,
			Env:     map[string]string{"CODEX_HOME": "/custom/codexhome"},
		}}
	})

	ev := moveEvent("cardCodex", repo, "opt-backlog", "opt-agent")
	ev.OptionNames = []string{"codexagent"} // routes to the codex agent
	events.ch <- ev

	waitFor(t, 15*time.Second, "codex session done", func() bool {
		sessions, _, err := m.store.SessionsForCard("cardCodex")
		return err == nil && len(sessions) == 1 && sessions[0].Status == StatusDone
	})

	sessions, _, _ := m.store.SessionsForCard("cardCodex")
	if sessions[0].AgentKind != "codex" {
		t.Errorf("expected agent kind codex, got %q", sessions[0].AgentKind)
	}
	comments := writer.cardComments("cardCodex")
	last := comments[len(comments)-1]
	if !strings.Contains(last, "/custom/codexhome") {
		t.Errorf("per-agent CODEX_HOME did not reach the codex process; final comment: %q", last)
	}

	// The codex adapter starts read-only, so a session that was asked to do
	// work has to be switched over — otherwise the turn is spent explaining
	// that nothing can be edited.
	mode, err := os.ReadFile(filepath.Join(fakeAgentDir(codexScript), "mode.txt"))
	if err != nil || string(mode) != "agent" {
		t.Errorf("session mode = %q (err %v), want agent", mode, err)
	}
}

// The codex adapter stopped taking the model on the command line: it offers it
// as a session config option, which is the protocol's own way of asking and the
// only one that still works.
func TestCodexModelIsChosenOverTheProtocol(t *testing.T) {
	script := writeFakeAgent(t, fakeCodexEnv)
	m, _, events, repo := testManager(t, fakeClaudeHappy, func(c *Config) {
		c.Agents = []AgentEntry{{
			Name:    "codexagent",
			Kind:    "codex",
			BinPath: script,
			Model:   "gpt-5.4",
		}}
	})

	ev := moveEvent("cardModel", repo, "opt-backlog", "opt-agent")
	ev.OptionNames = []string{"codexagent"}
	events.ch <- ev

	waitFor(t, 15*time.Second, "codex session done", func() bool {
		sessions, _, err := m.store.SessionsForCard("cardModel")
		return err == nil && len(sessions) == 1 && sessions[0].Status == StatusDone
	})

	model, err := os.ReadFile(filepath.Join(fakeAgentDir(script), "model.txt"))
	if err != nil || string(model) != "gpt-5.4" {
		t.Errorf("session model = %q (err %v), want gpt-5.4", model, err)
	}
}

// A wrapper command in front of the CLI (proxychains and friends) plus per-agent
// proxy settings: both must survive all the way to the spawned process.
func TestCodexAgentWrapperCommandAndProxy(t *testing.T) {
	script := writeFakeAgent(t, fakeCodexProxy)
	m, writer, events, repo := testManager(t, fakeClaudeHappy, func(c *Config) {
		c.Proxies = []ProxyEntry{{
			Name: "office",
			NetworkSettings: NetworkSettings{
				Proxy:  "http://proxy.example.com:8080",
				CACert: "/etc/my-ca.pem",
			},
		}}
		c.Agents = []AgentEntry{{
			Name:      "proxiedcodex",
			Kind:      "codex",
			Command:   []string{"/bin/sh", script}, // stands in for `proxychains4 -f … codex`
			ProxyName: "office",
		}}
	})

	ev := moveEvent("cardProxy", repo, "opt-backlog", "opt-agent")
	ev.OptionNames = []string{"proxiedcodex"}
	events.ch <- ev

	waitFor(t, 15*time.Second, "wrapped codex session done", func() bool {
		sessions, _, err := m.store.SessionsForCard("cardProxy")
		return err == nil && len(sessions) == 1 && sessions[0].Status == StatusDone
	})

	comments := writer.cardComments("cardProxy")
	last := comments[len(comments)-1]
	if !strings.Contains(last, "proxy=http://proxy.example.com:8080") {
		t.Errorf("per-agent proxy did not reach the process; final comment: %q", last)
	}
	if !strings.Contains(last, "ca=/etc/my-ca.pem") {
		t.Errorf("per-agent CA bundle did not reach the process; final comment: %q", last)
	}
}
