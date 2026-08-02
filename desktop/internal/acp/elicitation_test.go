package acp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"
)

// The claude adapter offers its AskUserQuestion tool only to a client that says
// it can draw a form, so what we advertise decides whether an agent can ask the
// user anything at all mid-task.
func TestWeTellAgentsWeCanDrawAForm(t *testing.T) {
	caps := clientCapabilities()
	if caps.Elicitation == nil || caps.Elicitation.Form == nil {
		t.Fatalf("form elicitation is not advertised: %+v", caps)
	}

	script := writeFakeAgent(t, fakeClaudeHappy)
	m, _, events, repo := testManager(t, fakeClaudeHappy, func(c *Config) {
		c.Agents = []AgentEntry{{Name: "clyde", Kind: "claude", BinPath: script}}
	})
	ev := moveEvent("cardCaps", repo, "opt-backlog", "opt-agent")
	ev.OptionNames = []string{"clyde"}
	events.ch <- ev
	waitFor(t, 15*time.Second, "session done", func() bool {
		sessions, _, err := m.store.SessionsForCard("cardCaps")
		return err == nil && len(sessions) == 1 && sessions[0].Status == StatusDone
	})

	raw, err := os.ReadFile(filepath.Join(fakeAgentDir(script), "capabilities.json"))
	if err != nil {
		t.Fatalf("the agent recorded no capabilities: %v", err)
	}
	if !strings.Contains(string(raw), "elicitation") {
		t.Errorf("the agent was not told we can draw a form: %s", raw)
	}
}

// A console watching the session gets the form, and what the user filled in
// reaches the agent under the schema's own field names.
func TestFormIsAnsweredFromTheConsole(t *testing.T) {
	script := writeFakeAgent(t, fakeClaudeAsksForm)
	m, _, events, repo, emitter := testManagerWithEmitter(t, fakeClaudeAsksForm, func(c *Config) {
		c.Agents = []AgentEntry{{Name: "clyde", Kind: "claude", BinPath: script}}
	})

	ev := moveEvent("cardForm", repo, "opt-backlog", "opt-agent")
	ev.OptionNames = []string{"clyde"}
	events.ch <- ev

	var s *Session
	waitFor(t, 10*time.Second, "session to start", func() bool {
		m.mu.Lock()
		defer m.mu.Unlock()
		s = m.byCard["cardForm"]
		return s != nil
	})
	if !m.AttachSession(s.ID) {
		t.Fatal("AttachSession refused a live session")
	}

	var payload map[string]any
	waitFor(t, 10*time.Second, "the form to reach the console", func() bool {
		payload = emitter.pendingElicitation()
		return payload != nil
	})

	// The console is handed a form it has nothing left to interpret: the
	// question, one select with its options, and the free-text field paired to
	// that select.
	if payload["message"] != "Which database?" {
		t.Errorf("message = %v, want the agent's question", payload["message"])
	}
	fields, _ := payload["fields"].([]ElicitationField)
	if len(fields) != 2 {
		t.Fatalf("fields = %+v, want the select and its custom answer", fields)
	}
	if fields[0].Key != "question_0" || fields[0].Type != fieldSelect || len(fields[0].Options) != 2 {
		t.Errorf("first field = %+v, want a select with two options", fields[0])
	}
	if fields[0].Options[0].Value != "sqlite" || fields[0].Options[0].Description != "one file" {
		t.Errorf("option = %+v, want the agent's own value and description", fields[0].Options[0])
	}
	if fields[1].CustomFor != "question_0" {
		t.Errorf("second field = %+v, want it paired with question_0", fields[1])
	}

	requestID, _ := payload["requestId"].(string)
	if err := m.AnswerElicitation(s.ID, requestID, `{"question_0":"postgres"}`); err != nil {
		t.Fatalf("answer: %v", err)
	}

	waitFor(t, 10*time.Second, "the agent to receive the answer", func() bool {
		_, err := os.ReadFile(filepath.Join(fakeAgentDir(script), "elicitation.json"))
		return err == nil
	})
	raw, _ := os.ReadFile(filepath.Join(fakeAgentDir(script), "elicitation.json"))
	var content map[string]any
	if err := json.Unmarshal(raw, &content); err != nil {
		t.Fatalf("agent got %q: %v", raw, err)
	}
	if content["question_0"] != "postgres" {
		t.Errorf("agent received %v, want the answer keyed by the schema's own name", content)
	}
}

// Nobody is watching, so there is nobody to fill the form in: decline at once
// rather than hold the turn until the prompt times out.
func TestFormIsDeclinedWithNoConsole(t *testing.T) {
	script := writeFakeAgent(t, fakeClaudeAsksForm)
	m, _, events, repo := testManager(t, fakeClaudeAsksForm, func(c *Config) {
		c.PermissionTimeoutMinutes = 30 // a wait here would fail the test, not pass it
		c.Agents = []AgentEntry{{Name: "clyde", Kind: "claude", BinPath: script}}
	})

	ev := moveEvent("cardNoConsole", repo, "opt-backlog", "opt-agent")
	ev.OptionNames = []string{"clyde"}
	events.ch <- ev

	waitFor(t, 20*time.Second, "session done", func() bool {
		sessions, _, err := m.store.SessionsForCard("cardNoConsole")
		return err == nil && len(sessions) == 1 && sessions[0].Status == StatusDone
	})
	raw, err := os.ReadFile(filepath.Join(fakeAgentDir(script), "elicitation.json"))
	if err != nil || string(raw) != "declined" {
		t.Errorf("agent saw %q (err %v), want declined", raw, err)
	}
}

// The form is read out of the schema, not out of any knowledge of the tool that
// sent it — which is what makes it work for an MCP server's elicitation too.
func TestFormIsReadOutOfTheSchema(t *testing.T) {
	form := elicitationForm(acpsdk.UnstableCreateElicitationForm{
		Message: " Please answer ",
		RequestedSchema: acpsdk.UnstableElicitationSchema{
			Required: []string{"name"},
			Properties: map[string]any{
				"name":    map[string]any{"type": "string", "title": "Name"},
				"age":     map[string]any{"type": "integer"},
				"agree":   map[string]any{"type": "boolean"},
				"colours": map[string]any{"type": "array", "items": map[string]any{"anyOf": []any{map[string]any{"const": "red"}, map[string]any{"const": "blue"}}}},
				"size":    map[string]any{"type": "string", "enum": []any{"S", "M"}},
			},
		},
	})
	if form.Message != "Please answer" {
		t.Errorf("message = %q, want it trimmed", form.Message)
	}
	got := map[string]ElicitationField{}
	for _, f := range form.Fields {
		got[f.Key] = f
	}
	if got["name"].Type != fieldText || !got["name"].Required {
		t.Errorf("name = %+v, want a required text field", got["name"])
	}
	if got["age"].Type != fieldNumber {
		t.Errorf("age = %+v, want a number field", got["age"])
	}
	if got["agree"].Type != fieldBoolean {
		t.Errorf("agree = %+v, want a boolean field", got["agree"])
	}
	if got["colours"].Type != fieldMultiSelect || len(got["colours"].Options) != 2 {
		t.Errorf("colours = %+v, want a multi-select with two options", got["colours"])
	}
	if got["size"].Type != fieldSelect || got["size"].Options[1].Value != "M" {
		t.Errorf("size = %+v, want a select built from enum", got["size"])
	}

	// Fields come out in a stable order; a JSON object has none, and the person
	// answering should see the same form twice.
	keys := make([]string, 0, len(form.Fields))
	for _, f := range form.Fields {
		keys = append(keys, f.Key)
	}
	want := []string{"age", "agree", "colours", "name", "size"}
	if strings.Join(keys, ",") != strings.Join(want, ",") {
		t.Errorf("field order = %v, want %v", keys, want)
	}
}

// An answer for a form that is no longer waiting is the common shape of "I
// answered and nothing happened", so it says which one it was.
func TestAnswerForAnUnknownFormIsRejected(t *testing.T) {
	m, _, _, _ := testManager(t, fakeClaudeHappy, nil)
	if err := m.AnswerElicitation("session", "nope", `{}`); err == nil {
		t.Errorf("an answer to a form nobody is waiting for was accepted")
	}
	if err := m.AnswerElicitation("session", "nope", `not json`); err == nil {
		t.Errorf("a malformed answer was accepted")
	}
}
