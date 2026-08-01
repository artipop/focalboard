package dokku

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// connect wires the server to an in-memory client, so the tools are exercised
// through the real protocol without spawning anything.
func connect(t *testing.T, cl *Client) *mcp.ClientSession {
	return connectWithArtifacts(t, cl, "")
}

// connectWithArtifacts is connect for a server that records its deploy outcome.
func connectWithArtifacts(t *testing.T, cl *Client, artifacts string) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	ss, err := NewServer(cl, artifacts).Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil).Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() {
		_ = cs.Close()
		_ = ss.Wait()
	})
	return cs
}

func callText(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) (string, bool) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("call %s: %v", name, err)
	}
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String(), res.IsError
}

func TestToolsList(t *testing.T) {
	cl, _ := New(testTarget(), "/repo", "main")
	cl.Run = (&fakeRunner{}).run
	cs := connect(t, cl)

	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}
	sort.Strings(names)
	want := []string{"app_logs", "deploy_branch", "deployment_status", "destroy_deployment", "list_deployments"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("tools = %v, want %v", names, want)
	}
}

func TestDeployToolReportsURL(t *testing.T) {
	f := &fakeRunner{replies: map[string]reply{
		"apps:exists": {err: &ExitError{Code: 1}},
		"git push":    {out: "remote: deployed\n"},
	}}
	cl, _ := New(testTarget(), "/repo", "feat/login")
	cl.Run = f.run

	text, isErr := callText(t, connect(t, cl), "deploy_branch", nil)
	if isErr {
		t.Fatalf("deploy reported an error: %s", text)
	}
	for _, want := range []string{"http://api-feat-login.example.com", "api-feat-login", "remote: deployed"} {
		if !strings.Contains(text, want) {
			t.Errorf("deploy output %q missing %q", text, want)
		}
	}
}

func TestDeployToolFailureCarriesTheBuildLog(t *testing.T) {
	f := &fakeRunner{replies: map[string]reply{
		"git push": {out: "remote: ERROR: no Procfile found\n", err: &ExitError{Code: 1}},
	}}
	cl, _ := New(testTarget(), "/repo", "main")
	cl.Run = f.run

	text, isErr := callText(t, connect(t, cl), "deploy_branch", nil)
	if !isErr {
		t.Fatal("a failed push must come back as a tool error")
	}
	// The agent can only diagnose what it is shown.
	if !strings.Contains(text, "no Procfile found") {
		t.Errorf("failure output %q lost the build log", text)
	}
}

func TestDeployToolAcceptsAnExplicitBranch(t *testing.T) {
	f := &fakeRunner{}
	cl, _ := New(testTarget(), "/repo", "main")
	cl.Run = f.run

	text, isErr := callText(t, connect(t, cl), "deploy_branch", map[string]any{"branch": "fix/typo"})
	if isErr {
		t.Fatalf("deploy reported an error: %s", text)
	}
	if !strings.Contains(text, "api-fix-typo") {
		t.Errorf("explicit branch ignored: %s", text)
	}
}

func TestDestroyNeedsConfirmation(t *testing.T) {
	f := &fakeRunner{}
	cl, _ := New(testTarget(), "/repo", "main")
	cl.Run = f.run
	cs := connect(t, cl)

	text, isErr := callText(t, cs, "destroy_deployment", nil)
	if !isErr || !strings.Contains(text, "confirm") {
		t.Fatalf("unconfirmed destroy must refuse, got %q (isError=%v)", text, isErr)
	}
	if len(f.calls) != 0 {
		t.Fatalf("unconfirmed destroy still ran %v", f.calls)
	}

	if _, isErr := callText(t, cs, "destroy_deployment", map[string]any{"confirm": true}); isErr {
		t.Fatal("confirmed destroy failed")
	}
	if len(f.calls) != 1 || !strings.Contains(f.line(0), "apps:destroy api-main --force") {
		t.Fatalf("destroy call: %v", f.calls)
	}
}

func TestLogsToolUsesRequestedLineCount(t *testing.T) {
	f := &fakeRunner{replies: map[string]reply{"logs": {out: "line one"}}}
	cl, _ := New(testTarget(), "/repo", "main")
	cl.Run = f.run

	text, isErr := callText(t, connect(t, cl), "app_logs", map[string]any{"lines": 50})
	if isErr {
		t.Fatalf("logs reported an error: %s", text)
	}
	if !strings.Contains(f.line(0), "logs api-main -n 50") {
		t.Errorf("logs call: %s", f.line(0))
	}
}
