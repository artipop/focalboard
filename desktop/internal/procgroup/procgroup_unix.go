//go:build !windows

package procgroup

import (
	"os/exec"
	"syscall"
	"time"
)

// processGroup is the child's process group id, which Setpgid makes equal to
// the child's pid.
type processGroup struct {
	pgid int
}

func setGroupAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func (p *Process) adoptGroup() {
	p.group.pgid = p.Cmd.Process.Pid
}

// KillGroup terminates the whole process group: SIGTERM, then SIGKILL after
// grace. It polls with signal 0 instead of waiting, so the session goroutine
// stays the sole owner of Cmd.Wait. Safe to call more than once.
func (p *Process) KillGroup(grace time.Duration) {
	if p == nil || p.Cmd == nil || p.Cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-p.group.pgid, syscall.SIGTERM)
	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(-p.group.pgid, 0); err != nil {
			return // group is gone
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = syscall.Kill(-p.group.pgid, syscall.SIGKILL)
}
