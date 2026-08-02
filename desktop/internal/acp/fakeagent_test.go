package acp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"
)

// Every agent is now reached the same way — an ACP process on stdio — so the
// tests need one fake agent rather than a fake of each CLI's own protocol. It
// is this test binary, re-executed: TestMain notices the scenario in its
// environment and speaks ACP instead of running tests, which keeps the fake
// honest (it is driven by the real SDK) and needs no toolchain beyond Go.
//
// A scenario is named rather than scripted, and the names are the ones the
// tests already used, so a test still reads "run this manager against an agent
// that hangs".

const (
	fakeAgentEnv    = "FOCALBOARD_FAKE_ACP"     // scenario name; set by writeFakeAgent
	fakeAgentDirEnv = "FOCALBOARD_FAKE_ACP_DIR" // where the agent records what it saw
)

// The scenarios. Their old names are kept: the tests read the same either way,
// and the change of protocol is not what any of them is about.
const (
	fakeClaudeHappy          = "happy"
	fakeClaudeHang           = "hang"
	fakeClaudeCrash          = "crash"
	fakeClaudeEcho           = "echo"
	fakeClaudeMultiTurn      = "multiturn"
	fakeClaudeSlowTurn       = "slowturn"
	fakeClaudeAsksPermission = "permission"
	fakeClaudeAsksForm       = "form"
	fakeClaudeSlowPermission = "slowpermission"
	fakeClaudeRecordingArgs  = "record"
	fakeCodexEnv             = "env-codexhome"
	fakeCodexProxy           = "env-proxy"
)

// TestMain turns this binary into the fake agent when it is launched as one.
func TestMain(m *testing.M) {
	if scenario := os.Getenv(fakeAgentEnv); scenario != "" {
		runFakeAgent(scenario)
		return
	}
	os.Exit(m.Run())
}

// writeFakeAgent installs a launcher for one scenario and returns its path. It
// is a shell script rather than the binary itself so that the scenario travels
// in the environment of the child alone, and so a test can still put a wrapper
// in front of it the way a user puts proxychains in front of a CLI.
func writeFakeAgent(t *testing.T, scenario string) string {
	t.Helper()
	dir := t.TempDir()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "fake-agent")
	script := fmt.Sprintf("#!/bin/sh\n%s=%q %s=%q exec %q \"$@\"\n",
		fakeAgentEnv, scenario, fakeAgentDirEnv, dir, self)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// fakeAgentDir is where the agent launched from path records what it saw.
func fakeAgentDir(path string) string { return filepath.Dir(path) }

// runFakeAgent serves one ACP connection on stdio and exits when the client
// disconnects. Called from TestMain in the re-executed binary.
func runFakeAgent(scenario string) {
	if scenario == fakeClaudeCrash {
		os.Exit(1)
	}
	agent := &fakeAgent{scenario: scenario, dir: os.Getenv(fakeAgentDirEnv)}
	agent.record("args.txt", strings.Join(os.Args[1:], "\n"))
	conn := acpsdk.NewAgentSideConnection(agent, os.Stdout, os.Stdin)
	agent.conn = conn
	<-conn.Done()
}

type fakeAgent struct {
	scenario string
	dir      string
	conn     *acpsdk.AgentSideConnection
	turns    int
}

// record writes what the agent was given, for tests that assert on it.
func (f *fakeAgent) record(name, content string) {
	if f.dir == "" {
		return
	}
	_ = os.WriteFile(filepath.Join(f.dir, name), []byte(content), 0o600)
}

func (f *fakeAgent) Initialize(ctx context.Context, params acpsdk.InitializeRequest) (acpsdk.InitializeResponse, error) {
	// An agent decides what it may ask for from what the client says it can
	// draw, so the capabilities are worth recording.
	if caps, err := json.Marshal(params.ClientCapabilities); err == nil {
		f.record("capabilities.json", string(caps))
	}
	return acpsdk.InitializeResponse{ProtocolVersion: acpsdk.ProtocolVersionNumber}, nil
}

func (f *fakeAgent) NewSession(ctx context.Context, params acpsdk.NewSessionRequest) (acpsdk.NewSessionResponse, error) {
	// The MCP servers a session was configured with arrive here now, which is
	// what the deploy tests assert on.
	if servers, err := json.Marshal(params.McpServers); err == nil {
		f.record("mcp.json", string(servers))
	}
	// What a client hands the adapter for the CLI behind it (Remote Control and
	// friends) travels in _meta, which is what the hand-over tests read back.
	if meta, err := json.Marshal(params.Meta); err == nil {
		f.record("meta.json", string(meta))
	}
	f.record("env.txt", strings.Join(os.Environ(), "\n"))
	// The modes and the model option are spelled the way the codex adapter
	// spells them: it starts read-only, calls the working mode "agent", and
	// takes the model as a session config option rather than a flag.
	models := acpsdk.SessionConfigSelectOptionsUngrouped{
		{Value: "gpt-5.4", Name: "GPT-5.4"},
		{Value: "gpt-5.6-sol", Name: "GPT-5.6-Sol"},
	}
	// Alongside them, the two shapes a per-agent setting takes: a select the
	// way claude offers its effort level, and a boolean toggle the way an agent
	// that has Fast mode offers it. An agent that has neither simply lists
	// neither, which is what the dialog then shows.
	effort := acpsdk.SessionConfigSelectOptionsUngrouped{
		{Value: "default", Name: "Default"},
		{Value: "high", Name: "High"},
	}
	return acpsdk.NewSessionResponse{
		SessionId: acpsdk.SessionId("fake-session"),
		Modes: &acpsdk.SessionModeState{
			CurrentModeId: "read-only",
			AvailableModes: []acpsdk.SessionMode{
				{Id: "read-only", Name: "Read Only"},
				{Id: "agent", Name: "Agent"},
				{Id: "agent-full-access", Name: "Agent (full access)"},
			},
		},
		ConfigOptions: []acpsdk.SessionConfigOption{{
			Select: &acpsdk.SessionConfigOptionSelect{
				Id:           "model",
				Name:         "Model",
				Type:         "select",
				CurrentValue: "gpt-5.6-sol",
				Options:      acpsdk.SessionConfigSelectOptions{Ungrouped: &models},
			},
		}, {
			Select: &acpsdk.SessionConfigOptionSelect{
				Id:           "effort",
				Name:         "Effort",
				Type:         "select",
				CurrentValue: "default",
				Options:      acpsdk.SessionConfigSelectOptions{Ungrouped: &effort},
			},
		}, {
			Boolean: &acpsdk.SessionConfigOptionBoolean{
				Id:           "fast",
				Name:         "Fast mode",
				Type:         "boolean",
				CurrentValue: false,
			},
		}},
	}, nil
}

func (f *fakeAgent) SetSessionMode(ctx context.Context, params acpsdk.SetSessionModeRequest) (acpsdk.SetSessionModeResponse, error) {
	f.record("mode.txt", string(params.ModeId))
	return acpsdk.SetSessionModeResponse{}, nil
}

func (f *fakeAgent) Prompt(ctx context.Context, params acpsdk.PromptRequest) (acpsdk.PromptResponse, error) {
	f.turns++
	prompt := promptText(params)
	f.record(fmt.Sprintf("prompt-%d.txt", f.turns), prompt)

	switch f.scenario {
	case fakeClaudeHang:
		<-ctx.Done()
		return acpsdk.PromptResponse{StopReason: acpsdk.StopReasonCancelled}, nil

	case fakeClaudeSlowTurn:
		select {
		case <-time.After(3 * time.Second):
		case <-ctx.Done():
			return acpsdk.PromptResponse{StopReason: acpsdk.StopReasonCancelled}, nil
		}
		return f.say(ctx, params.SessionId, "slow work done")

	case fakeClaudeEcho:
		// Planning asks for a title on the first line and a description under
		// it, and the tests read both back, so the echo answers in that shape.
		if strings.Contains(prompt, "заголовок задачи") {
			return f.say(ctx, params.SessionId, "Кэшировать ответы\n\nСделать кэш и проверить тестом.")
		}
		return f.say(ctx, params.SessionId, "echo: "+prompt)

	case fakeClaudeMultiTurn:
		return f.say(ctx, params.SessionId, fmt.Sprintf("turn %d done", f.turns))

	case fakeCodexEnv:
		return f.say(ctx, params.SessionId, "codex home is "+os.Getenv("CODEX_HOME"))

	case fakeCodexProxy:
		return f.say(ctx, params.SessionId,
			fmt.Sprintf("proxy=%s ca=%s", os.Getenv("HTTPS_PROXY"), os.Getenv("NODE_EXTRA_CA_CERTS")))

	case fakeClaudeAsksPermission, fakeClaudeSlowPermission:
		if f.scenario == fakeClaudeSlowPermission {
			// The slow variant asks a moment later, which is the window a test
			// needs to open a console on a session that started unattended.
			select {
			case <-time.After(700 * time.Millisecond):
			case <-ctx.Done():
				return acpsdk.PromptResponse{StopReason: acpsdk.StopReasonCancelled}, nil
			}
		}
		f.askPermission(ctx, params.SessionId)
		return f.say(ctx, params.SessionId, "fake work done")

	case fakeClaudeAsksForm:
		// What the claude adapter sends for its own AskUserQuestion: a question
		// as the message, the options as a `oneOf` of consts, and a free-text
		// field marked as the custom answer for that question.
		answer, err := f.conn.UnstableCreateElicitation(ctx, acpsdk.UnstableCreateElicitationRequest{
			Form: &acpsdk.UnstableCreateElicitationForm{
				Mode:    "form",
				Message: "Which database?",
				RequestedSchema: acpsdk.UnstableElicitationSchema{
					Type: "object",
					Properties: map[string]any{
						"question_0": map[string]any{
							"type":  "string",
							"title": "Database",
							"oneOf": []any{
								map[string]any{"const": "sqlite", "title": "SQLite", "description": "one file"},
								map[string]any{"const": "postgres", "title": "Postgres"},
							},
						},
						"question_0_custom": map[string]any{
							"type":  "string",
							"title": "Other",
							"_meta": map[string]any{
								"_askUserQuestionCustomAnswer": map[string]any{"questionId": "question_0"},
							},
						},
					},
				},
			},
		})
		if err != nil {
			return f.say(ctx, params.SessionId, "elicitation failed: "+err.Error())
		}
		switch {
		case answer.Accept != nil:
			raw, _ := json.Marshal(answer.Accept.Content)
			f.record("elicitation.json", string(raw))
			return f.say(ctx, params.SessionId, "answered: "+string(raw))
		case answer.Decline != nil:
			f.record("elicitation.json", "declined")
			return f.say(ctx, params.SessionId, "declined")
		default:
			f.record("elicitation.json", "cancelled")
			return f.say(ctx, params.SessionId, "cancelled")
		}

	case fakeClaudeRecordingArgs:
		return f.say(ctx, params.SessionId, "deployed")

	default: // fakeClaudeHappy
		return f.say(ctx, params.SessionId, "fake work done")
	}
}

// say streams one message and ends the turn, which is the shape of every
// scenario that simply answers.
func (f *fakeAgent) say(ctx context.Context, id acpsdk.SessionId, text string) (acpsdk.PromptResponse, error) {
	_ = f.conn.SessionUpdate(ctx, acpsdk.SessionNotification{
		SessionId: id,
		Update: acpsdk.SessionUpdate{AgentMessageChunk: &acpsdk.SessionUpdateAgentMessageChunk{
			Content: acpsdk.ContentBlock{Text: &acpsdk.ContentBlockText{Text: text}},
		}},
	})
	return acpsdk.PromptResponse{StopReason: acpsdk.StopReasonEndTurn}, nil
}

// askPermission asks for something the policy does not allow on its own, the
// way an adapter does it: a tool call announced with its name in _meta, then a
// permission request carrying only the id, the title and the raw input.
func (f *fakeAgent) askPermission(ctx context.Context, id acpsdk.SessionId) {
	const callID = "call-1"
	kind := acpsdk.ToolKindFetch
	title := "Fetch https://example.com"
	input := map[string]any{"url": "https://example.com"}
	_ = f.conn.SessionUpdate(ctx, acpsdk.SessionNotification{
		SessionId: id,
		Update: acpsdk.SessionUpdate{ToolCall: &acpsdk.SessionUpdateToolCall{
			ToolCallId: acpsdk.ToolCallId(callID),
			Title:      title,
			Kind:       kind,
			RawInput:   input,
			Meta:       map[string]any{"claudeCode": map[string]any{"toolName": "WebFetch"}},
		}},
	})
	_, _ = f.conn.RequestPermission(ctx, acpsdk.RequestPermissionRequest{
		SessionId: id,
		ToolCall: acpsdk.ToolCallUpdate{
			ToolCallId: acpsdk.ToolCallId(callID),
			Title:      &title,
			Kind:       &kind,
			RawInput:   input,
		},
		Options: []acpsdk.PermissionOption{
			{OptionId: "allow", Name: "Allow", Kind: acpsdk.PermissionOptionKindAllowOnce},
			{OptionId: "always", Name: "Always Allow", Kind: acpsdk.PermissionOptionKindAllowAlways},
			{OptionId: "reject", Name: "Reject", Kind: acpsdk.PermissionOptionKindRejectOnce},
		},
	})
}

func promptText(params acpsdk.PromptRequest) string {
	var b strings.Builder
	for _, block := range params.Prompt {
		if block.Text != nil {
			b.WriteString(block.Text.Text)
		}
	}
	return b.String()
}

// The rest of the agent surface, which no scenario exercises.

func (f *fakeAgent) Authenticate(ctx context.Context, params acpsdk.AuthenticateRequest) (acpsdk.AuthenticateResponse, error) {
	return acpsdk.AuthenticateResponse{}, nil
}
func (f *fakeAgent) Logout(ctx context.Context, params acpsdk.LogoutRequest) (acpsdk.LogoutResponse, error) {
	return acpsdk.LogoutResponse{}, nil
}
func (f *fakeAgent) Cancel(ctx context.Context, params acpsdk.CancelNotification) error { return nil }
func (f *fakeAgent) CloseSession(ctx context.Context, params acpsdk.CloseSessionRequest) (acpsdk.CloseSessionResponse, error) {
	return acpsdk.CloseSessionResponse{}, acpsdk.NewMethodNotFound(acpsdk.AgentMethodSessionClose)
}
func (f *fakeAgent) ListSessions(ctx context.Context, params acpsdk.ListSessionsRequest) (acpsdk.ListSessionsResponse, error) {
	return acpsdk.ListSessionsResponse{}, acpsdk.NewMethodNotFound(acpsdk.AgentMethodSessionList)
}
func (f *fakeAgent) LoadSession(ctx context.Context, params acpsdk.LoadSessionRequest) (acpsdk.LoadSessionResponse, error) {
	return acpsdk.LoadSessionResponse{}, acpsdk.NewMethodNotFound(acpsdk.AgentMethodSessionLoad)
}
func (f *fakeAgent) ResumeSession(ctx context.Context, params acpsdk.ResumeSessionRequest) (acpsdk.ResumeSessionResponse, error) {
	return acpsdk.ResumeSessionResponse{}, acpsdk.NewMethodNotFound(acpsdk.AgentMethodSessionResume)
}
func (f *fakeAgent) SetSessionConfigOption(ctx context.Context, params acpsdk.SetSessionConfigOptionRequest) (acpsdk.SetSessionConfigOptionResponse, error) {
	switch {
	case params.ValueId != nil:
		f.record(string(params.ValueId.ConfigId)+".txt", string(params.ValueId.Value))
	case params.Boolean != nil:
		f.record(string(params.Boolean.ConfigId)+".txt", strconv.FormatBool(params.Boolean.Value))
	default:
		return acpsdk.SetSessionConfigOptionResponse{}, acpsdk.NewMethodNotFound(acpsdk.AgentMethodSessionSetConfigOption)
	}
	return acpsdk.SetSessionConfigOptionResponse{}, nil
}
