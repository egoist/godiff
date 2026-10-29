package main

import (
	"testing"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func TestOpenFolderShortcut(t *testing.T) {
	var calls int
	var input string
	tt := ui.NewTester(func(c *ui.Context) {
		menuShortcuts(c, nil, func() *mygo.Window { return nil }, func() { calls++ })
		ui.TextInput(c, &input).AutoFocus()
	}, 400, 200)
	tt.Frame()

	// It works while a text field owns focus, without inserting the key.
	tt.Key(ui.Ctrl, ui.KeyO)
	if calls != 1 || input != "" {
		t.Fatalf("Ctrl+O: calls = %d, input = %q", calls, input)
	}
	tt.Frame()
	if calls != 1 {
		t.Fatalf("shortcut repeated after a frame: %d calls", calls)
	}
	tt.Key(ui.Ctrl|ui.Shift, ui.KeyO)
	if calls != 1 {
		t.Fatalf("Ctrl+Shift+O opened a folder: %d calls", calls)
	}
}

func TestWindowMenuShortcuts(t *testing.T) {
	dir := testRepo(t)
	for _, tc := range []struct {
		name  string
		mods  ui.Modifiers
		key   ui.Key
		check func(*window) bool
	}{
		{"find", ui.Ctrl, ui.KeyF, func(w *window) bool { return w.finding }},
		{"filter", ui.Ctrl, ui.KeyP, func(w *window) bool { return w.sidebarShown && w.tab == 0 }},
		{"command bar", ui.Ctrl, ui.KeyK, func(w *window) bool { return w.paletteOpen }},
		{"sidebar", ui.Ctrl | ui.Shift, ui.KeyB, func(w *window) bool { return !w.sidebarShown }},
		{"history", ui.Ctrl, ui.Key2, func(w *window) bool { return w.tab == 1 && w.sidebarShown }},
		{"open commit", ui.Ctrl | ui.Shift, ui.KeyC, func(w *window) bool { return w.dialogOpen && w.dialog == dialogCommit }},
		{"commit", ui.Ctrl | ui.Shift, ui.KeyEnter, func(w *window) bool { return w.commitOpen }},
		{"help", ui.Shift, ui.KeySlash, func(w *window) bool { return w.help }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, tt := newTestWindow(t, dir)
			if tc.name == "filter" {
				w.sidebarShown = false
				w.tab = 1
				tt.Frame()
			}
			tt.Key(tc.mods, tc.key)
			if !tc.check(w) {
				t.Fatal("shortcut did not run its menu action")
			}
		})
	}
}
