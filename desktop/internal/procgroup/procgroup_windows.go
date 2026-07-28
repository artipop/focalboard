//go:build windows

package procgroup

import (
	"os/exec"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// stillActive is the exit code GetExitCodeProcess reports for a running
// process (STILL_ACTIVE).
const stillActive = 259

// processGroup holds the job object the child is assigned to. Windows has no
// signalable process groups, so a kill-on-close job object is what makes the
// whole tree (the agent CLI and anything it spawns) killable as a unit. The
// handle is swapped to 0 by the first KillGroup, which may run concurrently
// with another one.
type processGroup struct {
	job atomic.Uintptr
}

func setGroupAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		// A group of its own keeps console Ctrl+C aimed at us (wails dev) away
		// from the agent; no window keeps a console from flashing up for the
		// agent CLI, whose stdio we pipe anyway.
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.CREATE_NO_WINDOW,
	}
}

// adoptGroup puts the freshly started child into a kill-on-close job object,
// which every process it spawns inherits. Best effort: if the job cannot be
// created or assigned, KillGroup falls back to killing the direct child.
func (p *Process) adoptGroup() {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		_ = windows.CloseHandle(job)
		return
	}
	proc, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(p.Cmd.Process.Pid))
	if err != nil {
		_ = windows.CloseHandle(job)
		return
	}
	defer func() { _ = windows.CloseHandle(proc) }()
	if err := windows.AssignProcessToJobObject(job, proc); err != nil {
		_ = windows.CloseHandle(job)
		return
	}
	p.group.job.Store(uintptr(job))
}

// KillGroup terminates the whole job object: closing stdin first, then killing
// the tree once grace runs out. Closing stdin is the graceful step here —
// Windows has no SIGTERM, and the agent CLIs exit on EOF. It polls the child's
// exit code instead of waiting, so the session goroutine stays the sole owner
// of Cmd.Wait. Safe to call more than once.
func (p *Process) KillGroup(grace time.Duration) {
	if p == nil || p.Cmd == nil || p.Cmd.Process == nil {
		return
	}
	if p.Stdin != nil {
		_ = p.Stdin.Close()
	}
	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) {
		if !p.running() {
			break // the child is gone; still terminate the job for its children
		}
		time.Sleep(50 * time.Millisecond)
	}
	if job := windows.Handle(p.group.job.Swap(0)); job != 0 {
		_ = windows.TerminateJobObject(job, 1)
		// Closing the last handle to a kill-on-job-close job is itself a kill,
		// and releases the handle a long-lived app would otherwise leak.
		_ = windows.CloseHandle(job)
		return
	}
	_ = p.Cmd.Process.Kill()
}

// running reports whether the child process is still alive.
func (p *Process) running() bool {
	proc, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(p.Cmd.Process.Pid))
	if err != nil {
		return false
	}
	defer func() { _ = windows.CloseHandle(proc) }()
	var code uint32
	if err := windows.GetExitCodeProcess(proc, &code); err != nil {
		return false
	}
	return code == stillActive
}
