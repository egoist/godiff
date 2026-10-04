package main

import (
	"runtime"

	"github.com/egoist/mygo"
)

// focused returns the review window with the focus, nil for none.
func focused(win *mygo.Window) *window {
	windowsMu.Lock()
	defer windowsMu.Unlock()
	for _, w := range windows {
		if w.win == win {
			return w
		}
	}
	return nil
}

// inWindow returns a menu item's action on the focused review window.
func inWindow(fn func(w *window)) func(*mygo.MenuItem, *mygo.Window) {
	return func(_ *mygo.MenuItem, win *mygo.Window) {
		if w := focused(win); w != nil {
			w.win.Update(func() { fn(w) })
		}
	}
}

var appMenu *mygo.Menu

// buildMenu makes the menu bar.
func buildMenu() *mygo.Menu {
	s := cfg.Get()
	setTheme := func(theme string) func(*mygo.MenuItem, *mygo.Window) {
		return func(*mygo.MenuItem, *mygo.Window) { cfg.Update(func(s *Settings) { s.Theme = theme }) }
	}
	appMenu = mygo.NewMenu([]*mygo.MenuItem{
		{Role: mygo.RoleAppMenu, Submenu: []*mygo.MenuItem{
			{Role: mygo.RoleAbout},
			mygo.Separator(),
			{Label: "Open Config File…", Accelerator: "CmdOrCtrl+,", Click: func(*mygo.MenuItem, *mygo.Window) { openConfig() }},
			{Label: "Install Command Line Tool…", Hidden: runtime.GOOS == "windows", Click: func(*mygo.MenuItem, *mygo.Window) { installCLI() }},
			mygo.Separator(),
			{Role: mygo.RoleServices},
			mygo.Separator(),
			{Role: mygo.RoleHide},
			{Role: mygo.RoleHideOthers},
			{Role: mygo.RoleUnhide},
			mygo.Separator(),
			{Role: mygo.RoleQuit},
		}},
		{Label: "File", Submenu: []*mygo.MenuItem{
			{Label: "Open Folder…", Accelerator: "CmdOrCtrl+O", Click: func(*mygo.MenuItem, *mygo.Window) { openFolder() }},
			{Label: "Open Commit…", Accelerator: "CmdOrCtrl+Shift+C", Click: inWindow(func(w *window) { w.openDialog(dialogCommit) })},
			{Label: "Open Branch…", Click: inWindow(func(w *window) { w.openDialog(dialogBranch) })},
			mygo.Separator(),
			{Label: "Commit…", Accelerator: "CmdOrCtrl+Shift+Enter", Click: inWindow(func(w *window) {
				if w.source.kind == sourceWorkingTree && !w.commitOpen && len(w.files) > 0 {
					w.toggleCommit()
				}
			})},
			mygo.Separator(),
			{Label: "Open File in Editor", Accelerator: "CmdOrCtrl+Shift+O", Click: inWindow(func(w *window) { w.openCurrent() })},
			mygo.Separator(),
			{Role: mygo.RoleClose},
		}},
		{Label: "Edit", Submenu: []*mygo.MenuItem{
			{Role: mygo.RoleUndo},
			{Role: mygo.RoleRedo},
			mygo.Separator(),
			{Role: mygo.RoleCut},
			{Role: mygo.RoleCopy},
			{Role: mygo.RolePaste},
			{Role: mygo.RoleSelectAll},
			mygo.Separator(),
			{Label: "Find in Diffs", Accelerator: "CmdOrCtrl+F", Click: inWindow(func(w *window) { w.finding = true })},
			{Label: "Filter Files", Accelerator: "CmdOrCtrl+P", Click: inWindow(func(w *window) { w.focusFilter() })},
			mygo.Separator(),
			{Label: "Copy Review Comments", Accelerator: "CmdOrCtrl+Shift+M", Click: inWindow(func(w *window) { w.copyComments() })},
		}},
		{Label: "View", Submenu: []*mygo.MenuItem{
			{Label: "Command Bar…", Accelerator: "CmdOrCtrl+K", Click: inWindow(func(w *window) { w.paletteOpen = !w.paletteOpen })},
			{Label: "Toggle Sidebar", Accelerator: "CmdOrCtrl+Shift+B", Click: inWindow(func(w *window) { w.toggleSidebar() })},
			{Label: "Files", Accelerator: "CmdOrCtrl+1", Click: inWindow(func(w *window) { w.tab, w.sidebarShown = 0, true })},
			{Label: "History", Accelerator: "CmdOrCtrl+2", Click: inWindow(func(w *window) { w.tab, w.sidebarShown = 1, true })},
			mygo.Separator(),
			{Label: "Diff", Submenu: []*mygo.MenuItem{
				{ID: "split", Label: "Split", Type: mygo.MenuItemRadio, Checked: s.DiffStyle == "split", Click: func(*mygo.MenuItem, *mygo.Window) {
					cfg.Update(func(s *Settings) { s.DiffStyle = "split" })
				}},
				{ID: "unified", Label: "Unified", Type: mygo.MenuItemRadio, Checked: s.DiffStyle == "unified", Click: func(*mygo.MenuItem, *mygo.Window) {
					cfg.Update(func(s *Settings) { s.DiffStyle = "unified" })
				}},
				mygo.Separator(),
				{ID: "wrap", Label: "Word Wrap", Type: mygo.MenuItemCheckbox, Checked: s.WordWrap, Click: func(*mygo.MenuItem, *mygo.Window) { toggleWrap() }},
				{ID: "whitespace", Label: "Show Whitespace", Type: mygo.MenuItemCheckbox, Checked: s.ShowWhitespace, Click: func(*mygo.MenuItem, *mygo.Window) {
					cfg.Update(func(s *Settings) { s.ShowWhitespace = !s.ShowWhitespace })
				}},
				mygo.Separator(),
				{Label: "Collapse All Files", Click: inWindow(func(w *window) { w.setAllCollapsed(true) })},
				{Label: "Expand All Files", Click: inWindow(func(w *window) { w.setAllCollapsed(false) })},
			}},
			{Label: "Font Size", Submenu: []*mygo.MenuItem{
				{Label: "Bigger", Accelerator: "CmdOrCtrl+=", Click: func(*mygo.MenuItem, *mygo.Window) { changeFontSize(1) }},
				{Label: "Smaller", Accelerator: "CmdOrCtrl+-", Click: func(*mygo.MenuItem, *mygo.Window) { changeFontSize(-1) }},
				{Label: "Actual Size", Accelerator: "CmdOrCtrl+0", Click: func(*mygo.MenuItem, *mygo.Window) { changeFontSize(0) }},
			}},
			{Label: "Comments", Submenu: []*mygo.MenuItem{
				{ID: "copyOnClose", Label: "Copy Comments on Close", Type: mygo.MenuItemCheckbox, Checked: s.CopyCommentsOnClose, Click: func(*mygo.MenuItem, *mygo.Window) {
					cfg.Update(func(s *Settings) { s.CopyCommentsOnClose = !s.CopyCommentsOnClose })
				}},
			}},
			{Label: "Theme", Submenu: []*mygo.MenuItem{
				{ID: "theme-system", Label: "Match System", Type: mygo.MenuItemRadio, Checked: s.Theme == "system", Click: setTheme("system")},
				{ID: "theme-light", Label: "Light", Type: mygo.MenuItemRadio, Checked: s.Theme == "light", Click: setTheme("light")},
				{ID: "theme-dark", Label: "Dark", Type: mygo.MenuItemRadio, Checked: s.Theme == "dark", Click: setTheme("dark")},
			}},
			mygo.Separator(),
			{Label: "Refresh Changes", Accelerator: "CmdOrCtrl+R", Click: inWindow(func(w *window) { w.refresh() })},
			mygo.Separator(),
			{Role: mygo.RoleToggleFullScreen},
		}},
		{Role: mygo.RoleWindowMenu},
		{Role: mygo.RoleHelp, Submenu: []*mygo.MenuItem{
			{Label: "Keyboard Shortcuts", Click: inWindow(func(w *window) { w.help = true })},
		}},
	})
	return appMenu
}

// syncMenu shows the settings in the menu's check marks.
func syncMenu(s Settings) {
	if appMenu == nil {
		return
	}
	set := func(id string, on bool) {
		if it := appMenu.ItemByID(id); it != nil {
			it.SetChecked(on)
		}
	}
	set("split", s.DiffStyle == "split")
	set("unified", s.DiffStyle == "unified")
	set("wrap", s.WordWrap)
	set("whitespace", s.ShowWhitespace)
	set("copyOnClose", s.CopyCommentsOnClose)
	set("theme-system", s.Theme == "system")
	set("theme-light", s.Theme == "light")
	set("theme-dark", s.Theme == "dark")
}

// applyTheme follows the theme of the settings.
func applyTheme(s Settings) {
	switch s.Theme {
	case "light":
		mygo.Theme.SetSource(mygo.ThemeLight)
	case "dark":
		mygo.Theme.SetSource(mygo.ThemeDark)
	default:
		mygo.Theme.SetSource(mygo.ThemeSystem)
	}
}

// openFolder asks for a repository and opens it.
func openFolder() {
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Title: "Open a Git Repository", Directory: true})
		if err != nil || len(paths) == 0 {
			return
		}
		if err := openWindow(paths[0], source{kind: sourceWorkingTree}); err != nil {
			mygo.Dialog.Error("Could not open the folder", errorText(err))
			return
		}
		closeWelcome()
	}()
}
