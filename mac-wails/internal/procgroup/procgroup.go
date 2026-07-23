package procgroup

import (
	"context"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// Process is a spawned agent subprocess placed in its own process group,
// so the whole tree (e.g. claude and anything it spawns) can be killed at once.
type Process struct {
	Cmd    *exec.Cmd
	Stdin  io.WriteCloser
	Stdout io.ReadCloser
	pgid   int
}

// Spawn starts argv in cwd with stdio pipes and its own process group.
// Stderr goes to the parent's stderr (captured in app logs). dropEnv names
// environment variables removed from the child's environment.
func Spawn(ctx context.Context, argv []string, cwd string, extraEnv []string, dropEnv ...string) (*Process, error) {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = cwd
	env := os.Environ()
	if len(dropEnv) > 0 {
		kept := env[:0]
		for _, kv := range env {
			drop := false
			for _, name := range dropEnv {
				if strings.HasPrefix(kv, name+"=") {
					drop = true
					break
				}
			}
			if !drop {
				kept = append(kept, kv)
			}
		}
		env = kept
	}
	cmd.Env = append(env, extraEnv...)
	cmd.Stderr = os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// CommandContext's default Kill would only hit the direct child; the
	// manager kills the whole group via KillGroup instead.
	cmd.Cancel = func() error { return nil }

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
	return &Process{Cmd: cmd, Stdin: stdin, Stdout: stdout, pgid: cmd.Process.Pid}, nil
}

// KillGroup terminates the whole process group: SIGTERM, then SIGKILL after
// grace. It polls with signal 0 instead of waiting, so the session goroutine
// stays the sole owner of Cmd.Wait. Safe to call more than once.
func (p *Process) KillGroup(grace time.Duration) {
	if p == nil || p.Cmd == nil || p.Cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-p.pgid, syscall.SIGTERM)
	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(-p.pgid, 0); err != nil {
			return // group is gone
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = syscall.Kill(-p.pgid, syscall.SIGKILL)
}

// Wait blocks until the process exits.
func (p *Process) Wait() error {
	return p.Cmd.Wait()
}
