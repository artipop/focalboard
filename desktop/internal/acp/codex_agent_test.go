package acp

import (
	"strings"
	"testing"
	"time"
)

// fakeCodexEnv emits codex exec --json events and echoes CODEX_HOME back in the
// agent message, so the test can assert the per-agent env reached the process.
const fakeCodexEnv = `#!/bin/sh
printf '%s\n' '{"type":"thread.started"}'
printf '{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"codex home is %s"}}\n' "$CODEX_HOME"
printf '%s\n' '{"type":"turn.completed"}'
`

func TestCodexAgentRunsWithIsolatedEnv(t *testing.T) {
	codexScript := writeFakeClaude(t, fakeCodexEnv) // generic: writes an executable script
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
}

// fakeCodexProxy echoes the proxy env back, so the test can assert the agent's
// network settings reached the process.
const fakeCodexProxy = `#!/bin/sh
printf '%s\n' '{"type":"thread.started"}'
printf '{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"proxy=%s ca=%s"}}\n' "$HTTPS_PROXY" "$NODE_EXTRA_CA_CERTS"
printf '%s\n' '{"type":"turn.completed"}'
`

// A wrapper command in front of the CLI (proxychains and friends) plus per-agent
// proxy settings: both must survive all the way to the spawned process.
func TestCodexAgentWrapperCommandAndProxy(t *testing.T) {
	script := writeFakeClaude(t, fakeCodexProxy)
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
