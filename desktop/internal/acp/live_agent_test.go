package acp

import (
	"os"
	"strings"
	"testing"
	"time"
)

// A fake agent proves our side of the protocol; it cannot prove that a vendor
// adapter answers the way we read it. This runs one real card through a real
// adapter — a process spawn, a session, a turn, a comment on the card — and is
// skipped unless asked for, because it needs the agent installed, logged in,
// and costs a model call.
//
//	FOCALBOARD_ACP_LIVE=codex go test ./internal/acp -run TestLiveAgentRunsACard -v
//
// The value is the agent kind. FOCALBOARD_ACP_LIVE_BIN overrides the adapter
// binary, which is how an adapter installed somewhere other than PATH is used.
func TestLiveAgentRunsACard(t *testing.T) {
	kind := os.Getenv("FOCALBOARD_ACP_LIVE")
	if kind == "" {
		t.Skip("set FOCALBOARD_ACP_LIVE=<kind> to run against a real agent")
	}

	m, writer, events, repo := testManager(t, fakeClaudeHappy, func(c *Config) {
		c.Agents = []AgentEntry{{
			Name:    "live",
			Kind:    kind,
			BinPath: os.Getenv("FOCALBOARD_ACP_LIVE_BIN"),
		}}
		// A live turn is a model call, not a script.
		c.SessionTimeoutMinutes = 5
	})

	ev := moveEvent("cardLive", repo, "opt-backlog", "opt-agent")
	ev.OptionNames = []string{"live"}
	ev.Title = "Say DONE"
	ev.Body = "Reply with the single word DONE. Do not use any tools and do not change any files."
	// The interesting run is the one that actually works: an unattended card
	// with tools it has to be allowed by policy, since nobody is there to ask.
	//
	//	FOCALBOARD_ACP_LIVE_TASK='create hello.txt containing HI, then say DONE'
	if task := os.Getenv("FOCALBOARD_ACP_LIVE_TASK"); task != "" {
		ev.Body = task
	}
	events.ch <- ev

	waitFor(t, 5*time.Minute, "the live session to finish", func() bool {
		sessions, _, err := m.store.SessionsForCard("cardLive")
		return err == nil && len(sessions) == 1 && sessions[0].Status.Terminal()
	})

	sessions, _, _ := m.store.SessionsForCard("cardLive")
	comments := strings.Join(writer.cardComments("cardLive"), "\n")
	if sessions[0].Status != StatusDone {
		t.Fatalf("live session ended %s: %s\n%s", sessions[0].Status, sessions[0].ErrorText, comments)
	}
	if !strings.Contains(strings.ToUpper(comments), "DONE") {
		t.Errorf("the agent's answer did not reach the card:\n%s", comments)
	}
	t.Logf("live %s session finished:\n%s", kind, comments)
}

// The bridges existed largely to keep a conversation going across turns and to
// stop one mid-flight. Both are the adapter's job now, so both are worth
// seeing once against a real one. Same env switch as above.
func TestLiveAgentKeepsTheConversationAndCancels(t *testing.T) {
	kind := os.Getenv("FOCALBOARD_ACP_LIVE")
	if kind == "" {
		t.Skip("set FOCALBOARD_ACP_LIVE=<kind> to run against a real agent")
	}

	m, _, _, _ := testManager(t, fakeClaudeHappy, func(c *Config) {
		c.Agents = []AgentEntry{{
			Name:    "live",
			Kind:    kind,
			BinPath: os.Getenv("FOCALBOARD_ACP_LIVE_BIN"),
		}}
		c.SessionTimeoutMinutes = 5
	})

	s := liveSession(t, m, "cardLiveTalk")
	waitStatus(t, s, StatusIdle)

	// The second turn has to remember the first, or an interactive console is
	// just a series of strangers.
	if err := m.PromptSession(s.ID, "Remember the number 4173. Reply with just OK."); err != nil {
		t.Fatalf("prompt: %v", err)
	}
	waitStatus(t, s, StatusIdle)
	if err := m.PromptSession(s.ID, "What number did I ask you to remember? Reply with the number only."); err != nil {
		t.Fatalf("prompt: %v", err)
	}
	waitStatus(t, s, StatusIdle)
	if got := lastAgentText(t, m, s); !strings.Contains(got, "4173") {
		t.Errorf("the agent did not keep the conversation across turns, last answer: %q", got)
	}

	// And a turn in flight can still be stopped.
	if err := m.PromptSession(s.ID, "Count slowly from 1 to 500, one line each."); err != nil {
		t.Fatalf("prompt: %v", err)
	}
	waitFor(t, 30*time.Second, "the long turn to start", func() bool { return s.Status() == StatusRunning })
	m.CancelSessionForCard("cardLiveTalk", "тест")
	waitFor(t, 60*time.Second, "the cancelled turn to end", func() bool {
		return s.Status() == StatusIdle || s.Status().Terminal()
	})
	t.Logf("after cancel: %s", s.Status())
}
