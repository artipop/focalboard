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
