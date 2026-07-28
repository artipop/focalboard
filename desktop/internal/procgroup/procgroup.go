package procgroup

import (
	"context"
	"io"
	"os"
	"os/exec"
	"strings"
)

// Process is a spawned agent subprocess placed in its own process group (a job
// object on Windows), so the whole tree (e.g. claude and anything it spawns)
// can be killed at once.
type Process struct {
	Cmd    *exec.Cmd
	Stdin  io.WriteCloser
	Stdout io.ReadCloser
	group  processGroup
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
	setGroupAttr(cmd)
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
	p := &Process{Cmd: cmd, Stdin: stdin, Stdout: stdout}
	p.adoptGroup()
	return p, nil
}

// Wait blocks until the process exits.
func (p *Process) Wait() error {
	return p.Cmd.Wait()
}
