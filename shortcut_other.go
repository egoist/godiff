//go:build !windows

package main

import (
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// Other platforms dispatch the menu accelerator themselves.
func menuShortcuts(_ *ui.Context, _ *window, _ func() *mygo.Window, _ func()) {}
