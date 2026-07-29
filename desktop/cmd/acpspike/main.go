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
//	-mode stream: measure how the agent's answer actually reaches the console —
//	           chunk count, first-chunk latency and the largest gap between
//	           chunks — through the production claudebridge. A single chunk
//	           arriving at the end means streaming is broken somewhere.
//	-mode multiturn: probe conversation continuity across two turns, which the
//	           interactive session console needs (both bridges currently respawn
//	           the CLI per turn, so turn 2 would start with an empty context).
//	           Selected with -agent claude|codex and -strategy live|resume|bridge.
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
	"strings"
	"sync"
	"syscall"
	"time"

	acp "github.com/coder/acp-go-sdk"
	"github.com/google/uuid"

	"github.com/beyond5959/acp-adapter/pkg/claudeacp"

	"github.com/mattermost/focalboard/desktop/internal/acp/claudebridge"
)

func main() {
	mode := flag.String("mode", "lib", "lib | raw | bridge | multiturn | stream")
	cwd := flag.String("cwd", "", "working directory for the session (default: temp dir)")
	prompt := flag.String("prompt", "Create a file named hello.txt containing exactly 'hello from acp spike', then briefly summarize what you did.", "prompt to send")
	timeout := flag.Duration("timeout", 5*time.Minute, "overall timeout")
	agent := flag.String("agent", "claude", "multiturn: claude | codex")
	strategy := flag.String("strategy", "live", "multiturn (claude only): live (one process, two stdin messages) | resume (respawn with --resume) | bridge (two ACP turns through claudebridge)")
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
	case "stream":
		if err := runStream(ctx, abs, *prompt); err != nil {
			log.Fatalf("stream mode: %v", err)
		}
	case "ask":
		if err := runAsk(ctx, abs, *strategy); err != nil {
			log.Fatalf("ask mode: %v", err)
		}
	case "multiturn":
		if err := runMultiturn(ctx, abs, *agent, *strategy); err != nil {
			log.Fatalf("multiturn mode: %v", err)
		}
	default:
		log.Fatalf("unknown mode %q", *mode)
	}
}

// ---- mode=lib: in-process ACP bridge (claudeacp) + coder SDK client ----

// spikeClient implements acp.Client, printing everything it receives and
// accumulating the agent's text so a mode can assert on it.
type spikeClient struct {
	mu   sync.Mutex
	seen strings.Builder
}

var _ acp.Client = (*spikeClient)(nil)

func (c *spikeClient) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seen.Reset()
}

func (c *spikeClient) text() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.seen.String()
}

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
			c.mu.Lock()
			c.seen.WriteString(t.Text)
			c.mu.Unlock()
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

// ---- mode=multiturn: does a second turn keep the first turn's context? ----

// The memory probe: turn 1 plants a token, turn 2 asks for it back. A CLI that
// silently starts a fresh conversation answers turn 2 with an apology instead.
const (
	memoryToken  = "4271"
	memoryPrompt = "Remember this number for later: " + memoryToken + ". Reply with just OK, nothing else."
	recallPrompt = "What number did I ask you to remember? Reply with just the number, nothing else."
)

func runMultiturn(ctx context.Context, cwd, agent, strategy string) error {
	switch agent {
	case "claude":
		switch strategy {
		case "live":
			return multiturnClaudeLive(ctx, cwd)
		case "resume":
			return multiturnClaudeResume(ctx, cwd)
		case "bridge":
			return multiturnClaudeBridge(ctx, cwd)
		default:
			return fmt.Errorf("unknown -strategy %q (live | resume | bridge)", strategy)
		}
	case "codex":
		return multiturnCodex(ctx, cwd)
	default:
		return fmt.Errorf("unknown -agent %q (claude | codex)", agent)
	}
}

// verdict reports whether the recall answer actually contains the planted token.
func verdict(answer string) error {
	if strings.Contains(answer, memoryToken) {
		fmt.Printf("\n✅ CONTEXT RETAINED — turn 2 recalled %s\n", memoryToken)
		return nil
	}
	fmt.Printf("\n❌ CONTEXT LOST — turn 2 did not recall %s\n", memoryToken)
	return fmt.Errorf("context not retained across turns")
}

// ---- claude ----

// claudeProc is one `claude` stream-json subprocess with a persistent scanner,
// so several turns can be read off the same stdout.
type claudeProc struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	scanner *bufio.Scanner
}

// startClaude spawns the CLI with exactly the production flags of claudebridge
// (see internal/acp/claudebridge/bridge.go:runTurn) plus extraArgs.
func startClaude(ctx context.Context, cwd string, extraArgs ...string) (*claudeProc, error) {
	bin, err := exec.LookPath("claude")
	if err != nil {
		return nil, fmt.Errorf("claude binary not found in PATH: %w", err)
	}
	argv := append([]string{
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--verbose",
		"--include-partial-messages",
		"--permission-prompt-tool", "stdio",
		"-p",
	}, extraArgs...)
	fmt.Printf("$ claude %s\n", strings.Join(argv, " "))

	cmd := exec.CommandContext(ctx, bin, argv...)
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), "CLAUDECODE=")
	cmd.Stderr = os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 1024*1024), 32*1024*1024)
	return &claudeProc{cmd: cmd, stdin: stdin, scanner: scanner}, nil
}

func (p *claudeProc) send(prompt string) error {
	msg := map[string]any{
		"type": "user",
		"message": map[string]any{
			"role":    "user",
			"content": []map[string]any{{"type": "text", "text": prompt}},
		},
	}
	b, _ := json.Marshal(msg)
	fmt.Printf("→ user: %s\n", prompt)
	_, err := p.stdin.Write(append(b, '\n'))
	return err
}

// awaitResult scans until the turn's terminal "result" line, returning the
// result text and the session_id advertised by system/init (empty if none seen).
func (p *claudeProc) awaitResult() (result, sessionID string, err error) {
	for p.scanner.Scan() {
		line := p.scanner.Bytes()
		var msg struct {
			Type      string `json:"type"`
			Subtype   string `json:"subtype"`
			SessionID string `json:"session_id"`
			IsError   bool   `json:"is_error"`
			Result    string `json:"result"`
		}
		if err := json.Unmarshal(line, &msg); err != nil {
			continue
		}
		if msg.SessionID != "" && sessionID == "" {
			sessionID = msg.SessionID
		}
		switch msg.Type {
		case "system":
			fmt.Printf("← system/%s session_id=%s\n", msg.Subtype, msg.SessionID)
		case "result":
			if msg.IsError {
				return msg.Result, sessionID, fmt.Errorf("claude reported an error: %s", msg.Result)
			}
			fmt.Printf("← result: %s\n", strings.TrimSpace(msg.Result))
			return msg.Result, sessionID, nil
		}
	}
	if err := p.scanner.Err(); err != nil {
		return "", sessionID, fmt.Errorf("read claude stream: %w", err)
	}
	return "", sessionID, fmt.Errorf("claude exited without a result message")
}

func (p *claudeProc) kill() {
	if p.cmd.Process != nil {
		_ = syscall.Kill(-p.cmd.Process.Pid, syscall.SIGKILL)
		_, _ = p.cmd.Process.Wait()
	}
}

// multiturnClaudeLive tests the cheap path: keep one process alive and write a
// second user message into the same stdin after the first turn's result.
func multiturnClaudeLive(ctx context.Context, cwd string) error {
	fmt.Println("== claude / strategy=live (one process, two stdin messages) ==")
	proc, err := startClaude(ctx, cwd)
	if err != nil {
		return err
	}
	defer proc.kill()

	if err := proc.send(memoryPrompt); err != nil {
		return err
	}
	if _, sessionID, err := proc.awaitResult(); err != nil {
		return fmt.Errorf("turn 1: %w", err)
	} else {
		fmt.Printf("   (turn 1 session_id=%s)\n", sessionID)
	}

	fmt.Println("\n-- turn 2 on the SAME process --")
	if err := proc.send(recallPrompt); err != nil {
		return fmt.Errorf("turn 2 send (process likely exited after turn 1): %w", err)
	}
	answer, _, err := proc.awaitResult()
	if err != nil {
		return fmt.Errorf("turn 2: %w", err)
	}
	return verdict(answer)
}

// multiturnClaudeResume tests the fallback path: dictate the session id up front
// with --session-id, then respawn with --resume for the second turn.
func multiturnClaudeResume(ctx context.Context, cwd string) error {
	fmt.Println("== claude / strategy=resume (respawn with --resume) ==")
	sessionID := uuid.NewString()

	proc, err := startClaude(ctx, cwd, "--session-id", sessionID)
	if err != nil {
		return err
	}
	if err := proc.send(memoryPrompt); err != nil {
		proc.kill()
		return err
	}
	_, seen, err := proc.awaitResult()
	proc.kill()
	if err != nil {
		return fmt.Errorf("turn 1: %w", err)
	}
	if seen != sessionID {
		fmt.Printf("⚠️  session_id mismatch: asked for %s, CLI reported %s\n", sessionID, seen)
	}

	fmt.Println("\n-- turn 2 in a FRESH process with --resume --")
	proc2, err := startClaude(ctx, cwd, "--resume", sessionID)
	if err != nil {
		return err
	}
	defer proc2.kill()
	if err := proc2.send(recallPrompt); err != nil {
		return err
	}
	answer, _, err := proc2.awaitResult()
	if err != nil {
		return fmt.Errorf("turn 2: %w", err)
	}
	return verdict(answer)
}

// multiturnClaudeBridge drives the production claudebridge through two ACP
// turns on one session, checking that the bridge really reuses its subprocess
// and keeps the conversation.
func multiturnClaudeBridge(ctx context.Context, cwd string) error {
	fmt.Println("== claude / strategy=bridge (production claudebridge, two ACP turns) ==")
	claudeBin, err := exec.LookPath("claude")
	if err != nil {
		return fmt.Errorf("claude binary not found in PATH: %w", err)
	}
	bridge := claudebridge.New(claudebridge.Options{Launch: []string{claudeBin}})

	clientIn, agentOut := io.Pipe()
	agentIn, clientOut := io.Pipe()
	agentConn := acp.NewAgentSideConnection(bridge, agentOut, agentIn)
	bridge.SetConn(agentConn)
	client := &spikeClient{}
	conn := acp.NewClientSideConnection(client, clientOut, clientIn)
	defer bridge.KillAll(2 * time.Second)

	if _, err := conn.Initialize(ctx, acp.InitializeRequest{
		ProtocolVersion: acp.ProtocolVersionNumber,
		ClientCapabilities: acp.ClientCapabilities{
			Fs: acp.FileSystemCapabilities{ReadTextFile: true, WriteTextFile: true},
		},
	}); err != nil {
		return fmt.Errorf("initialize: %w", err)
	}
	sess, err := conn.NewSession(ctx, acp.NewSessionRequest{Cwd: cwd, McpServers: []acp.McpServer{}})
	if err != nil {
		return fmt.Errorf("session/new: %w", err)
	}
	fmt.Printf("📝 session %s\n", sess.SessionId)

	turn := func(n int, prompt string) error {
		fmt.Printf("\n-- ACP turn %d --\n→ %s\n", n, prompt)
		client.reset()
		resp, err := conn.Prompt(ctx, acp.PromptRequest{
			SessionId: sess.SessionId,
			Prompt:    []acp.ContentBlock{acp.TextBlock(prompt)},
		})
		if err != nil {
			return err
		}
		fmt.Printf("\n(stopReason=%s)\n", resp.StopReason)
		return nil
	}

	if err := turn(1, memoryPrompt); err != nil {
		return fmt.Errorf("turn 1: %w", err)
	}
	if err := turn(2, recallPrompt); err != nil {
		return fmt.Errorf("turn 2: %w", err)
	}
	return verdict(client.text())
}

// ---- codex ----

// multiturnCodex captures thread_id from `thread.started` and replays the second
// turn through `codex exec resume <id>`.
func multiturnCodex(ctx context.Context, cwd string) error {
	fmt.Println("== codex / exec resume ==")
	bin, err := exec.LookPath("codex")
	if err != nil {
		return fmt.Errorf("codex binary not found in PATH: %w", err)
	}

	_, threadID, err := runCodex(ctx, bin, cwd,
		"exec", "--json", "--skip-git-repo-check", "-C", cwd, memoryPrompt)
	if err != nil {
		return fmt.Errorf("turn 1: %w", err)
	}
	if threadID == "" {
		return fmt.Errorf("turn 1 produced no thread_id")
	}
	fmt.Printf("   (turn 1 thread_id=%s)\n", threadID)

	// `exec resume` takes no -C; the working directory comes from cmd.Dir.
	fmt.Println("\n-- turn 2 via `codex exec resume` --")
	answer, _, err := runCodex(ctx, bin, cwd,
		"exec", "resume", threadID, "--json", "--skip-git-repo-check", recallPrompt)
	if err != nil {
		return fmt.Errorf("turn 2: %w", err)
	}
	return verdict(answer)
}

// runCodex runs one `codex exec` invocation, returning the concatenated
// agent_message text and the thread id from thread.started.
func runCodex(ctx context.Context, bin, cwd string, argv ...string) (text, threadID string, err error) {
	fmt.Printf("$ codex %s\n", strings.Join(argv, " "))
	cmd := exec.CommandContext(ctx, bin, argv...)
	cmd.Dir = cwd
	cmd.Stderr = os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", "", err
	}
	if err := cmd.Start(); err != nil {
		return "", "", err
	}
	defer func() {
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
	}()

	var out strings.Builder
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 1024*1024), 32*1024*1024)
	for scanner.Scan() {
		var msg struct {
			Type     string `json:"type"`
			ThreadID string `json:"thread_id"`
			Item     *struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"item"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			continue
		}
		switch msg.Type {
		case "thread.started":
			threadID = msg.ThreadID
			fmt.Printf("← thread.started %s\n", msg.ThreadID)
		case "item.completed":
			if msg.Item != nil && msg.Item.Type == "agent_message" {
				fmt.Printf("← agent_message: %s\n", strings.TrimSpace(msg.Item.Text))
				out.WriteString(msg.Item.Text)
			}
		case "turn.failed", "error":
			if msg.Error != nil {
				return out.String(), threadID, fmt.Errorf("codex error: %s", msg.Error.Message)
			}
			return out.String(), threadID, fmt.Errorf("codex reported %s", msg.Type)
		}
	}
	if err := scanner.Err(); err != nil {
		return out.String(), threadID, err
	}
	return out.String(), threadID, cmd.Wait()
}

// ---- mode=stream: is the answer actually streamed? ----

// streamPrompt asks for something long enough that arriving in one piece would
// be obvious, and cheap enough not to need the repository.
const streamPrompt = "Перечисли по пунктам 10 признаков хорошего кода, каждый с одним предложением пояснения. Не используй инструменты."

// chunkClock records when each piece of the agent's answer arrives.
type chunkClock struct {
	mu       sync.Mutex
	start    time.Time
	arrivals []time.Duration
	thoughts int
	bytes    int
}

func (c *chunkClock) text(n int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.arrivals = append(c.arrivals, time.Since(c.start))
	c.bytes += n
}

func (c *chunkClock) thought() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.thoughts++
}

func (c *chunkClock) report() {
	c.mu.Lock()
	defer c.mu.Unlock()
	fmt.Printf("\n\n== streaming ==\n")
	fmt.Printf("text chunks : %d (%d bytes)\n", len(c.arrivals), c.bytes)
	fmt.Printf("thought chunks: %d\n", c.thoughts)
	if len(c.arrivals) == 0 {
		fmt.Println("❌ nothing arrived")
		return
	}
	var maxGap time.Duration
	prev := time.Duration(0)
	for _, at := range c.arrivals {
		if gap := at - prev; gap > maxGap {
			maxGap = gap
		}
		prev = at
	}
	fmt.Printf("first chunk : %s\n", c.arrivals[0].Round(time.Millisecond))
	fmt.Printf("last chunk  : %s\n", c.arrivals[len(c.arrivals)-1].Round(time.Millisecond))
	fmt.Printf("largest gap : %s\n", maxGap.Round(time.Millisecond))
	switch {
	case len(c.arrivals) == 1:
		fmt.Println("❌ the whole answer arrived as ONE chunk — not streamed")
	case c.arrivals[0] > 10*time.Second:
		fmt.Println("⚠️  streamed, but the first chunk took a long time (thinking is invisible unless showThoughts is on)")
	default:
		fmt.Println("✅ streamed incrementally")
	}
}

// streamClient counts chunks instead of printing a transcript.
type streamClient struct {
	spikeClient
	clock *chunkClock
}

func (c *streamClient) SessionUpdate(ctx context.Context, params acp.SessionNotification) error {
	u := params.Update
	switch {
	case u.AgentMessageChunk != nil:
		if t := u.AgentMessageChunk.Content.Text; t != nil {
			c.clock.text(len(t.Text))
			fmt.Print(".")
		}
	case u.AgentThoughtChunk != nil:
		c.clock.thought()
		fmt.Print("~")
	case u.ToolCall != nil:
		fmt.Print("T")
	}
	return nil
}

// runStream drives the production bridge and measures the arrival of the answer.
// Each "." is one text chunk as the console would receive it, "~" a thought.
func runStream(ctx context.Context, cwd, prompt string) error {
	if prompt == "" {
		prompt = streamPrompt
	}
	claudeBin, err := exec.LookPath("claude")
	if err != nil {
		return fmt.Errorf("claude binary not found in PATH: %w", err)
	}
	bridge := claudebridge.New(claudebridge.Options{Launch: []string{claudeBin}})

	clientIn, agentOut := io.Pipe()
	agentIn, clientOut := io.Pipe()
	agentConn := acp.NewAgentSideConnection(bridge, agentOut, agentIn)
	bridge.SetConn(agentConn)
	clock := &chunkClock{start: time.Now()}
	conn := acp.NewClientSideConnection(&streamClient{clock: clock}, clientOut, clientIn)
	defer bridge.KillAll(2 * time.Second)

	if _, err := conn.Initialize(ctx, acp.InitializeRequest{
		ProtocolVersion:    acp.ProtocolVersionNumber,
		ClientCapabilities: acp.ClientCapabilities{Fs: acp.FileSystemCapabilities{ReadTextFile: true, WriteTextFile: true}},
	}); err != nil {
		return fmt.Errorf("initialize: %w", err)
	}
	sess, err := conn.NewSession(ctx, acp.NewSessionRequest{Cwd: cwd, McpServers: []acp.McpServer{}})
	if err != nil {
		return fmt.Errorf("session/new: %w", err)
	}

	// Two turns: the report is about the second, which is where it looked wrong.
	for turn, text := range []string{"Привет. Ответь одним словом: готов?", prompt} {
		fmt.Printf("\n-- turn %d --\n", turn+1)
		clock.mu.Lock()
		clock.start = time.Now()
		clock.arrivals = nil
		clock.thoughts = 0
		clock.bytes = 0
		clock.mu.Unlock()

		if _, err := conn.Prompt(ctx, acp.PromptRequest{
			SessionId: sess.SessionId,
			Prompt:    []acp.ContentBlock{acp.TextBlock(text)},
		}); err != nil {
			return fmt.Errorf("turn %d: %w", turn+1, err)
		}
		clock.report()
	}
	return nil
}

// ---- mode=ask: can AskUserQuestion answers be fed back? ----

// The can_use_tool channel only carries allow/deny, so the question is whether
// either can smuggle the user's answers to the model: "allow" runs the tool
// (which has no TTY in this mode), "deny" hands back a message.
func runAsk(ctx context.Context, cwd, behavior string) error {
	claudeBin, err := exec.LookPath("claude")
	if err != nil {
		return fmt.Errorf("claude binary not found in PATH: %w", err)
	}
	cmd := exec.CommandContext(ctx, claudeBin,
		"--input-format", "stream-json", "--output-format", "stream-json",
		"--verbose", "--include-partial-messages",
		"--permission-prompt-tool", "stdio", "-p",
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

	// What a user would have picked in the native picker.
	const answers = "Пользователь ответил на вопросы:\n" +
		"1. Цель — «Тестируемость»: код трудно покрыть тестами из-за жёстких зависимостей.\n" +
		"2. Подход — «Сначала тесты, потом рефакторинг»."

	done := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 0, 1024*1024), 32*1024*1024)
		for scanner.Scan() {
			var msg struct {
				Type      string `json:"type"`
				RequestID any    `json:"request_id"`
				Result    string `json:"result"`
				Request   struct {
					Subtype  string          `json:"subtype"`
					ToolName string          `json:"tool_name"`
					Input    json.RawMessage `json:"input"`
				} `json:"request"`
				Event *struct {
					Delta *struct {
						Text string `json:"text"`
					} `json:"delta"`
				} `json:"event"`
			}
			if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
				continue
			}
			switch msg.Type {
			case "stream_event":
				if msg.Event != nil && msg.Event.Delta != nil {
					fmt.Print(msg.Event.Delta.Text)
				}
			case "control_request":
				if msg.Request.Subtype != "can_use_tool" {
					continue
				}
				fmt.Printf("\n\n🔐 %s → отвечаем %q\n", msg.Request.ToolName, behavior)
				var payload map[string]any
				if behavior == "allow" {
					// Try to smuggle the answers in as updated input.
					var input map[string]any
					_ = json.Unmarshal(msg.Request.Input, &input)
					input["answers"] = answers
					payload = map[string]any{"behavior": "allow", "updatedInput": input}
				} else {
					payload = map[string]any{"behavior": "deny", "message": answers}
				}
				out, _ := json.Marshal(map[string]any{
					"type": "control_response",
					"response": map[string]any{
						"subtype": "success", "request_id": msg.RequestID, "response": payload,
					},
				})
				_, _ = stdin.Write(append(out, '\n'))
			case "result":
				fmt.Printf("\n\n== result ==\n%s\n", msg.Result)
				done <- nil
				return
			}
		}
		done <- scanner.Err()
	}()

	user := map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": []map[string]any{
		{"type": "text", "text": "Спланируй рефакторинг. Сначала задай мне 2 уточняющих вопроса через AskUserQuestion, потом коротко предложи план с учётом ответов."},
	}}}
	b, _ := json.Marshal(user)
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
