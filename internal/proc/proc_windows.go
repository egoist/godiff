// Package proc prepares the commands the app runs.
package proc

import (
	"os/exec"
	"syscall"
)

const createNoWindow = 0x08000000

// HideConsole keeps a console program from opening a console window. The
// app is built without a console of its own, so Windows would give each
// one it starts a window of its own.
func HideConsole(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= createNoWindow
	cmd.SysProcAttr.HideWindow = true
}
