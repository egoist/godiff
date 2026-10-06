//go:build !windows

// Package proc prepares the commands the app runs.
package proc

import (
	"os/exec"
	"syscall"
)

// HideConsole keeps a console program from opening a console window,
// which only Windows opens.
func HideConsole(cmd *exec.Cmd) {}

// KillGroup starts a command in a process group of its own, which
// cancelling its context kills whole: agents run tools in processes of
// their own.
func KillGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
