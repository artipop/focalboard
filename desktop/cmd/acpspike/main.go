// Command acpspike is the Phase-0 spike for the ACP integration (TZ_ACP_wails_v0.2.md).
//
// Modes:
//
//	-mode lib  (default): drive Claude Code through an IN-PROCESS ACP bridge
//	           (beyond5959/acp-adapter pkg/claudeacp over io.Pipe) using the
//	           coder/acp-go-sdk client side. No Node.js, no subprocess adapter;
//	           the bridge itself spawns the `claude` binary.
//	-mode raw: probe the `claude` binary's native stream-json stdio protocol
//	           directly, to verify whether permission control requests
//	           (can_use_tool) can be proxied — needed for the Phase-2 modal.
//
// Exit criterion (spec §9 Phase 0): stream a hardcoded prompt to the console,
// dump the agent's capabilities and protocol version.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	acp "github.com/coder/acp-go-sdk"

	"github.com/beyond5959/acp-adapter/pkg/claudeacp"

	"github.com/mattermost/focalboard/desktop/internal/acp/claudebridge"
)

func main() {
	mode := flag.String("mode", "lib", "lib | raw | bridge")
	cwd := flag.String("cwd", "", "working directory for the session (default: temp dir)")
	prompt := flag.String("prompt", "Create a file named hello.txt containing exactly 'hello from acp spike', then briefly summarize what you did.", "prompt to send")
	timeout := flag.Duration("timeout", 5*time.Minute, "overall timeout")
	flag.Parse()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	ctx, cancelTimeout := context.WithTimeout(ctx, *timeout)
	defer cancelTimeout()

	if *cwd == "" {
		dir, err := os.MkdirTemp("", "acpspike-*")
		if err != nil {
			log.Fatalf("mktemp: %v", err)
		}
		*cwd = dir
	}
	abs, err := filepath.Abs(*cwd)
	if err != nil {
		log.Fatalf("abs cwd: %v", err)
	}
	fmt.Printf("== acpspike mode=%s cwd=%s ==\n", *mode, abs)

	switch *mode {
	case "lib":
		if err := runLib(ctx, abs, *prompt); err != nil {
			log.Fatalf("lib mode: %v", err)
		}
	case "raw":
		if err := runRaw(ctx, abs, *prompt); err != nil {
			log.Fatalf("raw mode: %v", err)
		}
	case "bridge":
		if err := runBridge(ctx, abs, *prompt); err != nil {
			log.Fatalf("bridge mode: %v", err)
		}
	default:
		log.Fatalf("unknown mode %q", *mode)
	}
}

// ---- mode=lib: in-process ACP bridge (claudeacp) + coder SDK client ----

// spikeClient implements acp.Client, printing everything it receives.
type spikeClient struct{}

var _ acp.Client = (*spikeClient)(nil)

func (c *spikeClient) RequestPermission(ctx context.Context, params acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	title := ""
	if params.ToolCall.Title != nil {
		title = *params.ToolCall.Title
	}
	fmt.Printf("\n🔐 RequestPermission: tool=%q options=%d\n", title, len(params.Options))
	for _, opt := range params.Options {
		fmt.Printf("   option id=%s name=%q kind=%s\n", opt.OptionId, opt.Name, opt.Kind)
	}
	// Auto-select the first allow-ish option so the spike never blocks.
	for _, opt := range params.Options {
		if opt.Kind == acp.PermissionOptionKindAllowOnce || opt.Kind == acp.PermissionOptionKindAllowAlways {
			fmt.Printf("   → auto-allow via %s\n", opt.OptionId)
			return acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{
				Selected: &acp.RequestPermissionOutcomeSelected{OptionId: opt.OptionId},
			}}, nil
		}
	}
	return acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{
		Cancelled: &acp.RequestPermissionOutcomeCancelled{},
	}}, nil
}

func (c *spikeClient) SessionUpdate(ctx context.Context, params acp.SessionNotification) error {
	u := params.Update
	switch {
	case u.AgentMessageChunk != nil:
		if t := u.AgentMessageChunk.Content.Text; t != nil {
			fmt.Print(t.Text)
		}
	case u.AgentThoughtChunk != nil:
		if t := u.AgentThoughtChunk.Content.Text; t != nil {
			fmt.Printf("\n💭 %s\n", t.Text)
		}
	case u.ToolCall != nil:
		fmt.Printf("\n🔧 tool_call %s title=%q status=%s\n", u.ToolCall.ToolCallId, u.ToolCall.Title, u.ToolCall.Status)
	case u.ToolCallUpdate != nil:
		status := ""
		if u.ToolCallUpdate.Status != nil {
			status = string(*u.ToolCallUpdate.Status)
		}
		fmt.Printf("\n🔧 tool_call_update %s status=%s\n", u.ToolCallUpdate.ToolCallId, status)
	case u.Plan != nil:
		fmt.Printf("\n🗺  plan (%d entries)\n", len(u.Plan.Entries))
	default:
		raw, _ := json.Marshal(u)
		fmt.Printf("\n[update] %s\n", raw)
	}
	return nil
}

func (c *spikeClient) ReadTextFile(ctx context.Context, params acp.ReadTextFileRequest) (acp.ReadTextFileResponse, error) {
	fmt.Printf("\n📖 fs/read %s\n", params.Path)
	b, err := os.ReadFile(params.Path)
	if err != nil {
		return acp.ReadTextFileResponse{}, err
	}
	return acp.ReadTextFileResponse{Content: string(b)}, nil
}

func (c *spikeClient) WriteTextFile(ctx context.Context, params acp.WriteTextFileRequest) (acp.WriteTextFileResponse, error) {
	fmt.Printf("\n✍️  fs/write %s (%d bytes)\n", params.Path, len(params.Content))
	if err := os.WriteFile(params.Path, []byte(params.Content), 0o644); err != nil {
		return acp.WriteTextFileResponse{}, err
	}
	return acp.WriteTextFileResponse{}, nil
}

// Terminal capability is not advertised; stubs satisfy the interface.
func (c *spikeClient) CreateTerminal(ctx context.Context, params acp.CreateTerminalRequest) (acp.CreateTerminalResponse, error) {
	return acp.CreateTerminalResponse{}, fmt.Errorf("terminal not supported")
}
func (c *spikeClient) KillTerminal(ctx context.Context, params acp.KillTerminalRequest) (acp.KillTerminalResponse, error) {
	return acp.KillTerminalResponse{}, fmt.Errorf("terminal not supported")
}
func (c *spikeClient) TerminalOutput(ctx context.Context, params acp.TerminalOutputRequest) (acp.TerminalOutputResponse, error) {
	return acp.TerminalOutputResponse{}, fmt.Errorf("terminal not supported")
}
func (c *spikeClient) ReleaseTerminal(ctx context.Context, params acp.ReleaseTerminalRequest) (acp.ReleaseTerminalResponse, error) {
	return acp.ReleaseTerminalResponse{}, fmt.Errorf("terminal not supported")
}
func (c *spikeClient) WaitForTerminalExit(ctx context.Context, params acp.WaitForTerminalExitRequest) (acp.WaitForTerminalExitResponse, error) {
	return acp.WaitForTerminalExitResponse{}, fmt.Errorf("terminal not supported")
}

func runLib(ctx context.Context, cwd, prompt string) error {
	claudeBin, err := exec.LookPath("claude")
	if err != nil {
		return fmt.Errorf("claude binary not found in PATH: %w", err)
	}

	// Two pipes wire the in-process agent to the in-process client.
	clientIn, agentOut := io.Pipe() // agent writes → client reads
	agentIn, clientOut := io.Pipe() // client writes → agent reads

	cfg := claudeacp.DefaultRuntimeConfig()
	cfg.ClaudeBin = claudeBin
	// The bridge cannot proxy interactive permissions (it shells out per turn);
	// for the spike run with skip-permissions, as the library defaults to.
	cfg.SkipPerms = true
	cfg.LogLevel = "warn"
	cfg.TraceJSONFile = filepath.Join(cwd, "acp-trace.jsonl")

	bridgeErr := make(chan error, 1)
	go func() {
		bridgeErr <- claudeacp.RunStdio(ctx, cfg, agentIn, agentOut, os.Stderr)
	}()

	conn := acp.NewClientSideConnection(&spikeClient{}, clientOut, clientIn)

	initResp, err := conn.Initialize(ctx, acp.InitializeRequest{
		ProtocolVersion: acp.ProtocolVersionNumber,
		ClientCapabilities: acp.ClientCapabilities{
			Fs: acp.FileSystemCapabilities{ReadTextFile: true, WriteTextFile: true},
		},
	})
	if err != nil {
		return fmt.Errorf("initialize: %w", err)
	}
	caps, _ := json.MarshalIndent(initResp, "", "  ")
	fmt.Printf("✅ initialize ok, protocol v%v\nagent response dump:\n%s\n", initResp.ProtocolVersion, caps)

	sess, err := conn.NewSession(ctx, acp.NewSessionRequest{Cwd: cwd, McpServers: []acp.McpServer{}})
	if err != nil {
		return fmt.Errorf("session/new: %w", err)
	}
	fmt.Printf("📝 session %s\n💬 prompt: %s\n\n", sess.SessionId, prompt)

	resp, err := conn.Prompt(ctx, acp.PromptRequest{
		SessionId: sess.SessionId,
		Prompt:    []acp.ContentBlock{acp.TextBlock(prompt)},
	})
	if err != nil {
		return fmt.Errorf("session/prompt: %w", err)
	}
	fmt.Printf("\n\n✅ prompt done, stopReason=%s\n", resp.StopReason)

	if _, err := os.Stat(filepath.Join(cwd, "hello.txt")); err == nil {
		fmt.Println("✅ hello.txt exists in cwd")
	}
	return nil
}

// ---- mode=bridge: exercise the production claudebridge in-process ----

// runBridge runs the same ACP stack the app uses: claudebridge (agent side)
// over io.Pipe against the coder SDK client side, spawning the real claude.
func runBridge(ctx context.Context, cwd, prompt string) error {
	claudeBin, err := exec.LookPath("claude")
	if err != nil {
		return fmt.Errorf("claude binary not found in PATH: %w", err)
	}
	bridge := claudebridge.New(claudebridge.Options{Launch: []string{claudeBin}})

	clientIn, agentOut := io.Pipe()
	agentIn, clientOut := io.Pipe()
	agentConn := acp.NewAgentSideConnection(bridge, agentOut, agentIn)
	bridge.SetConn(agentConn)
	conn := acp.NewClientSideConnection(&spikeClient{}, clientOut, clientIn)
	defer bridge.KillAll(2 * time.Second)

	initResp, err := conn.Initialize(ctx, acp.InitializeRequest{
		ProtocolVersion: acp.ProtocolVersionNumber,
		ClientCapabilities: acp.ClientCapabilities{
			Fs: acp.FileSystemCapabilities{ReadTextFile: true, WriteTextFile: true},
		},
	})
	if err != nil {
		return fmt.Errorf("initialize: %w", err)
	}
	fmt.Printf("✅ initialize ok, protocol v%v\n", initResp.ProtocolVersion)

	sess, err := conn.NewSession(ctx, acp.NewSessionRequest{Cwd: cwd, McpServers: []acp.McpServer{}})
	if err != nil {
		return fmt.Errorf("session/new: %w", err)
	}
	fmt.Printf("📝 session %s\n💬 prompt: %s\n\n", sess.SessionId, prompt)

	resp, err := conn.Prompt(ctx, acp.PromptRequest{
		SessionId: sess.SessionId,
		Prompt:    []acp.ContentBlock{acp.TextBlock(prompt)},
	})
	if err != nil {
		return fmt.Errorf("session/prompt: %w", err)
	}
	fmt.Printf("\n\n✅ prompt done, stopReason=%s\n", resp.StopReason)
	if _, err := os.Stat(filepath.Join(cwd, "hello.txt")); err == nil {
		fmt.Println("✅ hello.txt exists in cwd")
	}
	return nil
}

// ---- mode=raw: probe claude's native stream-json protocol ----

// runRaw talks to `claude --input-format stream-json` directly and prints every
// line, to record the wire protocol and check for control_request/can_use_tool.
func runRaw(ctx context.Context, cwd, prompt string) error {
	claudeBin, err := exec.LookPath("claude")
	if err != nil {
		return fmt.Errorf("claude binary not found in PATH: %w", err)
	}
	cmd := exec.CommandContext(ctx, claudeBin,
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--verbose",
		"--include-partial-messages",
		"--permission-prompt-tool", "stdio", // ask the CLI to route permissions to us
		"-p",
	)
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), "CLAUDECODE=")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	defer func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		_, _ = cmd.Process.Wait()
	}()

	// Reader goroutine: print every NDJSON line; answer can_use_tool control requests.
	done := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 0, 1024*1024), 16*1024*1024)
		for scanner.Scan() {
			line := scanner.Bytes()
			fmt.Printf("← %s\n", line)
			var msg struct {
				Type    string `json:"type"`
				Request struct {
					Subtype string          `json:"subtype"`
					Input   json.RawMessage `json:"input"`
				} `json:"request"`
				RequestID any `json:"request_id"`
			}
			if err := json.Unmarshal(line, &msg); err != nil {
				continue
			}
			if msg.Type == "control_request" && msg.Request.Subtype == "can_use_tool" {
				// Envelope per cli-protocol.md: request_id nested inside "response".
				resp := map[string]any{
					"type": "control_response",
					"response": map[string]any{
						"subtype":    "success",
						"request_id": msg.RequestID,
						"response": map[string]any{
							"behavior":     "allow",
							"updatedInput": msg.Request.Input,
						},
					},
				}
				b, _ := json.Marshal(resp)
				fmt.Printf("→ %s\n", b)
				_, _ = stdin.Write(append(b, '\n'))
			}
			if msg.Type == "result" {
				done <- nil
				return
			}
		}
		done <- scanner.Err()
	}()

	user := map[string]any{
		"type": "user",
		"message": map[string]any{
			"role":    "user",
			"content": []map[string]any{{"type": "text", "text": prompt}},
		},
	}
	b, _ := json.Marshal(user)
	fmt.Printf("→ %s\n", b)
	if _, err := stdin.Write(append(b, '\n')); err != nil {
		return err
	}

	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}
