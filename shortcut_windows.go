package main

import (
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// MyGo's Windows native surface does not dispatch menu accelerators.
// Register them in the view until native menu dispatch handles them.
func menuShortcuts(c *ui.Context, w *window, native func() *mygo.Window, open func()) {
	c.OnShortcut(ui.Cmd, ui.KeyO, open)
	c.OnShortcut(ui.Cmd, ui.KeyComma, openConfig)
	c.OnShortcut(ui.Cmd, ui.KeyW, func() {
		if win := native(); win != nil {
			win.Close()
		}
	})
	c.OnShortcut(ui.Cmd, ui.KeyM, func() {
		if win := native(); win != nil {
			win.Minimize()
		}
	})
	c.OnShortcut(0, ui.KeyF11, func() {
		if win := native(); win != nil {
			win.ToggleFullScreen()
		}
	})
	c.OnShortcut(ui.Cmd, ui.KeyEqual, func() { changeFontSize(1) })
	c.OnShortcut(ui.Cmd|ui.Shift, ui.KeyEqual, func() { changeFontSize(1) })
	c.OnShortcut(ui.Cmd, ui.KeyMinus, func() { changeFontSize(-1) })
	c.OnShortcut(ui.Cmd, ui.Key0, func() { changeFontSize(0) })
	if w == nil {
		return
	}
	c.OnShortcut(ui.Cmd|ui.Shift, ui.KeyC, func() { w.openDialog(dialogCommit) })
	c.OnShortcut(ui.Cmd|ui.Shift, ui.KeyEnter, func() {
		if w.source.kind == sourceWorkingTree && !w.commitOpen && len(w.files) > 0 {
			w.toggleCommit()
		}
	})
	c.OnShortcut(ui.Cmd|ui.Shift, ui.KeyO, w.openCurrent)
	c.OnShortcut(ui.Cmd, ui.KeyF, func() { w.finding = true })
	c.OnShortcut(ui.Cmd, ui.KeyP, w.focusFilter)
	c.OnShortcut(ui.Cmd|ui.Shift, ui.KeyM, w.copyComments)
	c.OnShortcut(ui.Cmd, ui.KeyK, func() { w.paletteOpen = !w.paletteOpen })
	c.OnShortcut(ui.Cmd|ui.Shift, ui.KeyB, w.toggleSidebar)
	c.OnShortcut(ui.Cmd, ui.Key1, func() { w.tab, w.sidebarShown = 0, true })
	c.OnShortcut(ui.Cmd, ui.Key2, func() { w.tab, w.sidebarShown = 1, true })
	c.OnShortcut(ui.Cmd, ui.Key3, w.showPulls)
	c.OnShortcut(ui.Cmd|ui.Shift, ui.KeyI, w.reviewWithAI)
	c.OnShortcut(ui.Cmd, ui.KeyR, w.refresh)
}
