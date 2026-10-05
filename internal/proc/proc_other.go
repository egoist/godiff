//go:build !windows

// Package proc prepares the commands the app runs.
package proc

import "os/exec"

// HideConsole keeps a console program from opening a console window,
// which only Windows opens.
func HideConsole(cmd *exec.Cmd) {}
