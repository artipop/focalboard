package acp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	acpsdk "github.com/coder/acp-go-sdk"
)

// The policy is written in tool names and a permission request carries none, so
// this is what stands between an unattended card and an agent that is refused
// every tool it asks for.
func TestPermissionToolNameIsRecovered(t *testing.T) {
	c := &sessionClient{}

	kind := func(k acpsdk.ToolKind) *acpsdk.ToolKind { return &k }
	str := func(s string) *string { return &s }

	// 1. The name the agent gave, namespaced the way the vendor adapters do it.
	got := c.permissionToolName(acpsdk.RequestPermissionRequest{
		ToolCall: acpsdk.ToolCallUpdate{
			Meta: map[string]any{"claudeCode": map[string]any{"toolName": "WebFetch"}},
		},
	})
	if got != "WebFetch" {
		t.Errorf("meta name = %q", got)
	}

	// 2. Routing a built-in tool back through our file system does not make it
	// a different tool.
	got = c.permissionToolName(acpsdk.RequestPermissionRequest{
		ToolCall: acpsdk.ToolCallUpdate{Meta: map[string]any{"toolName": "mcp__acp__Read"}},
	})
	if got != "Read" {
		t.Errorf("prefixed name = %q", got)
	}

	// 3. The call was announced before permission was asked for it; only the
	// announcement carries the name.
	c.noteToolCall("call-7", map[string]any{"claudeCode": map[string]any{"toolName": "Bash"}}, "execute", nil)
	got = c.permissionToolName(acpsdk.RequestPermissionRequest{
		ToolCall: acpsdk.ToolCallUpdate{ToolCallId: "call-7", Title: str("`git push`")},
	})
	if got != "Bash" {
		t.Errorf("remembered name = %q", got)
	}

	// 4. Nobody named it: the kind and the input say what it plainly is. This
	// is the codex case, where the tools have their own names entirely.
	for _, tc := range []struct {
		name  string
		kind  acpsdk.ToolKind
		input map[string]any
		want  string
	}{
		{"shell", acpsdk.ToolKindExecute, map[string]any{"command": []any{"/bin/zsh", "-lc", "git status"}}, "Bash"},
		{"read", acpsdk.ToolKindRead, map[string]any{"path": "/tmp/x"}, "Read"},
		{"write", acpsdk.ToolKindEdit, map[string]any{"file_path": "/tmp/x", "content": "hi"}, "Write"},
		{"edit", acpsdk.ToolKindEdit, map[string]any{"file_path": "/tmp/x", "old_string": "a", "new_string": "b"}, "Edit"},
		{"grep", acpsdk.ToolKindSearch, map[string]any{"pattern": "TODO"}, "Grep"},
	} {
		got = c.permissionToolName(acpsdk.RequestPermissionRequest{
			ToolCall: acpsdk.ToolCallUpdate{Kind: kind(tc.kind), RawInput: tc.input},
		})
		if got != tc.want {
			t.Errorf("%s inferred as %q, want %q", tc.name, got, tc.want)
		}
	}

	// 5. Nothing to go on but the title, which is all some agents ever send.
	got = c.permissionToolName(acpsdk.RequestPermissionRequest{
		ToolCall: acpsdk.ToolCallUpdate{Title: str("Allow running MCP server?")},
	})
	if got != "Allow running MCP server?" {
		t.Errorf("title fallback = %q", got)
	}
}

// An argument pattern has to keep matching whatever shape the agent sends its
// command in, or a policy silently stops allowing what it says it allows.
func TestPolicyMatchesCommandsWhateverTheirShape(t *testing.T) {
	policy := ToolPolicy{"Bash(git log*)", "Read"}

	for _, tc := range []struct {
		name  string
		input any
		want  bool
	}{
		{"string command", map[string]any{"command": "git log --oneline"}, true},
		{"argv through a shell", map[string]any{"command": []any{"/bin/zsh", "-lc", "git log --oneline"}}, true},
		{"argv, wrong command", map[string]any{"command": []any{"/bin/zsh", "-lc", "rm -rf /"}}, false},
		{"no command at all", map[string]any{}, false},
	} {
		if got := policy.Allows("Bash", tc.input); got != tc.want {
			t.Errorf("%s: Allows = %v, want %v", tc.name, got, tc.want)
		}
	}

	// A bare name still needs no input to match.
	if !policy.Allows("Read", nil) {
		t.Error("a bare policy entry should match without input")
	}
}

// A kind that cannot be started should say so where the agent is configured,
// not on a card an hour later.
func TestAdapterStatusesExplainWhatIsMissing(t *testing.T) {
	statuses := (&Manager{}).AdapterStatuses()
	if len(statuses) != len(acpNative) {
		t.Fatalf("expected one status per known kind, got %d", len(statuses))
	}
	byKind := map[string]AdapterStatus{}
	for _, st := range statuses {
		byKind[st.Kind] = st
	}
	for _, kind := range []string{AgentKindClaude, AgentKindCodex} {
		st, ok := byKind[kind]
		if !ok {
			t.Fatalf("%s is missing from the statuses", kind)
		}
		if st.Package == "" {
			t.Errorf("%s should name the package that provides it", kind)
		}
		if !st.Ready && st.Detail == "" {
			t.Errorf("%s is not ready and does not say why", kind)
		}
		if st.Ready && st.ViaNPX && !strings.Contains(st.Detail, "npx") {
			t.Errorf("%s runs through npx and should say so: %q", kind, st.Detail)
		}
	}
	// The generic acp kind carries its own command, so there is nothing to
	// report about it.
	if _, ok := byKind[AgentKindACP]; ok {
		t.Error("the acp kind has no adapter to check")
	}
}

// The agent may spell the worktree differently than we do — macOS reaches the
// temp tree through a symlink — and still mean the same directory. Refusing it
// there would deny the agent its own working copy.
func TestFileJailFollowsSymlinks(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "wt")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	c := &sessionClient{
		m: NewManager(DefaultConfig(t.TempDir()), "", nil, newFakeWriter(), &fakeEmitter{}, nil),
		s: &Session{ID: "s1", Worktree: WorktreeInfo{Path: link}},
	}

	// Both spellings of the same file are inside.
	for _, path := range []string{
		filepath.Join(link, "hello.txt"),
		filepath.Join(real, "hello.txt"),
	} {
		if _, err := c.jail(path); err != nil {
			t.Errorf("jail refused %s: %v", path, err)
		}
	}

	// Somewhere else still is somewhere else.
	if _, err := c.jail(filepath.Join(filepath.Dir(real), "elsewhere.txt")); err == nil {
		t.Error("a path outside the worktree should be refused")
	}
}
