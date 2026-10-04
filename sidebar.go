package main

import (
	"fmt"
	"log"
	"runtime"
	"strings"
	"time"

	"github.com/egoist/godiff/internal/diff"
	"github.com/egoist/godiff/internal/git"
	"github.com/egoist/mygo/ui"
)

// treeNode is a row of the file tree: a file, or a directory with the
// chain of directories holding only it compacted into one row, as
// src/app/components.
type treeNode struct {
	name     string
	dir      bool
	file     int // the file's index in window.files
	children []string
}

// The sidebar's width: its default, its bounds, and the width below which
// dragging hides it.
const (
	sidebarDefault  = 292
	sidebarMin      = 220
	sidebarMax      = 640
	sidebarCollapse = 80
)

// fuzzyMatch reports whether the runes of query appear in s in order.
func fuzzyMatch(s, query string) bool {
	if query == "" {
		return true
	}
	s, query = strings.ToLower(s), strings.ToLower(query)
	i := 0
	for _, r := range s {
		if i < len(query) && r == rune(query[i]) {
			i++
		}
	}
	return i >= len(query)
}

// fileVisible reports whether a file shows: it passes the filter and,
// while finding, holds a match.
func (w *window) fileVisible(i int) bool {
	f := w.files[i]
	if !w.hasContent(f) {
		return false
	}
	if !fuzzyMatch(f.Path, strings.TrimSpace(w.filter)) {
		return false
	}
	if w.finding && strings.TrimSpace(w.query) != "" {
		return w.fileMatches[i]
	}
	return true
}

// hasContent reports whether a file has something to show: files whose
// only changes were whitespace, hidden, have nothing.
func (w *window) hasContent(f *fileState) bool {
	return len(f.Hunks) > 0 || f.Binary || f.Directory || f.TooLarge || f.Note != "" ||
		f.Status != diff.Modified || f.OldPath != f.Path || f.ModeChange != ""
}

// fileNote returns why a file's lines do not show, "" when they do.
func (w *window) fileNote(f *fileState) string {
	switch {
	case f.Note != "":
		return f.Note
	case f.Binary:
		return "Binary file changed."
	case f.TooLarge:
		return "File is too large, so Godiff skipped rendering it."
	case len(f.Hunks) == 0 && f.OldPath != f.Path:
		return "File renamed without changes."
	case len(f.Hunks) == 0 && f.ModeChange != "":
		return "File mode changed: " + f.ModeChange + "."
	case len(f.Hunks) == 0 && f.Status == diff.Added, len(f.Hunks) == 0 && f.Status == diff.Untracked:
		return "Empty file added."
	case len(f.Hunks) == 0 && f.Status == diff.Deleted:
		return "Empty file deleted."
	case len(f.Hunks) == 0:
		return "No changes to show."
	}
	return ""
}

// buildTree lays out the tree of the files that show.
func (w *window) buildTree() {
	type dirNode struct {
		dirs  map[string]*dirNode
		order []string // names of dirs and files, in the files' order
		files map[string]int
	}
	newDir := func() *dirNode { return &dirNode{dirs: map[string]*dirNode{}, files: map[string]int{}} }
	root := newDir()
	for i, f := range w.files {
		if !w.fileVisible(i) {
			continue
		}
		parts := strings.Split(f.Path, "/")
		d := root
		for _, p := range parts[:len(parts)-1] {
			next := d.dirs[p]
			if next == nil {
				next = newDir()
				d.dirs[p] = next
				d.order = append(d.order, "d"+p)
			}
			d = next
		}
		name := parts[len(parts)-1]
		d.files[name] = i
		d.order = append(d.order, "f"+name)
	}
	items := map[string]*treeNode{}
	var walk func(d *dirNode, prefix string) []string
	walk = func(d *dirNode, prefix string) []string {
		var keys []string
		for _, o := range d.order {
			name := o[1:]
			if o[0] == 'f' {
				i := d.files[name]
				key := "f:" + w.files[i].Path
				items[key] = &treeNode{name: name, file: i}
				keys = append(keys, key)
				continue
			}
			sub := d.dirs[name]
			label := name
			path := prefix + name
			// Directories holding one directory alone join it.
			for len(sub.order) == 1 && sub.order[0][0] == 'd' {
				child := sub.order[0][1:]
				label += "/" + child
				path += "/" + child
				sub = sub.dirs[child]
			}
			key := "d:" + path
			items[key] = &treeNode{name: label, dir: true, file: -1, children: walk(sub, path+"/")}
			keys = append(keys, key)
		}
		return keys
	}
	w.treeRoots = walk(root, "")
	w.treeItems = items
}

// statusColor is the color of a file's status letter.
func statusColor(s diff.Status, pal *palette, t *ui.Theme) ui.Color {
	switch s {
	case diff.Added, diff.Untracked:
		return pal.addBar
	case diff.Deleted:
		return pal.delBar
	case diff.Renamed, diff.Copied:
		return t.Accent
	case diff.Conflict:
		return t.Danger
	}
	return ui.Hex("#d4a72c")
}

func statusLetter(f *fileState) string {
	switch f.Status {
	case diff.Untracked:
		return "U"
	case diff.Conflict:
		return "!"
	}
	return string(f.Status)
}

// compact writes a count briefly: 999, 1.2k, 15k, 1.2m.
func compact(n int) string {
	switch {
	case n < 1000:
		return fmt.Sprint(n)
	case n < 10000:
		return strings.TrimSuffix(fmt.Sprintf("%.1f", float64(n)/1000), ".0") + "k"
	case n < 1000000:
		return fmt.Sprintf("%dk", (n+500)/1000)
	}
	return strings.TrimSuffix(fmt.Sprintf("%.1f", float64(n)/1000000), ".0") + "m"
}

// thousands writes a count with separators: 1,234.
func thousands(n int) string {
	s := fmt.Sprint(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

func lines(n int, what string) string {
	if n == 1 {
		return "1 " + what + " line"
	}
	return thousands(n) + " " + what + " lines"
}

// countable reports whether a file's lines were counted.
func countable(f *fileState) bool {
	return !f.Binary && !f.Directory && !f.TooLarge
}

func (w *window) sidebar(c *ui.Context) {
	t := c.Theme()
	pal := paletteFor(t)
	bar := c.TitleBar()
	sidebarRight := w.settings.SidebarPosition == "right"
	left, right := bar.Left+6, float32(10)
	if sidebarRight {
		left, right = 10, bar.Right+6
	}
	ui.Column(c).Width(w.sidebarWidth).Shrink(0).Background(w.sidebarBg(t)).Children(func() {
		// The title bar, under the window controls: the views of the
		// sidebar and its toggle, centered on the window controls.
		ui.Row(c).Height(titleBarHeight).Padding(0, right, 0, left).Gap(6).DragWindow().Children(func() {
			ui.Spacer(c)
			if w.tabControl(c) {
				w.commitOpen = false
			}
			w.sidebarToggle(c)
		})
		ui.Column(c).Padding(2, 10, 8).Children(func() {
			if w.tab == 0 {
				if searchInput(c, &w.filter, "Filter files", &w.filterFocus, &w.typing) {
					w.matchesFor = "\x00"
					w.buildTree()
					w.rowsDirty = true
				}
			} else {
				searchInput(c, &w.historyFilter, "Filter history", &w.filterFocus, &w.typing)
			}
		})
		if w.tab == 0 {
			w.fileTree(c)
		} else {
			w.historyView(c)
		}
		w.sidebarFooter(c, pal)
	})
}

// sidebarBg is the sidebar's background: none on macOS, where the
// window's material shows through, else the theme's surface.
func (w *window) sidebarBg(t *ui.Theme) ui.Color {
	if runtime.GOOS == "darwin" && w.win != nil {
		return ui.Transparent
	}
	if t.Dark {
		return ui.Hex("#262628")
	}
	return ui.Hex("#ebebed")
}

// titleBarHeight is the height of the bars along the top, which centers
// their controls on the traffic lights of a window with an inset title bar.
const titleBarHeight = 52

// tabControl switches the sidebar between the files and the history, and
// reports a switch.
func (w *window) tabControl(c *ui.Context) bool {
	t := c.Theme()
	pal := paletteFor(t)
	tab := w.tab
	seg := ui.SegmentedBase(c, &tab, 2)
	seg.Track.Padding(2).Gap(2).Radius(8).Background(ui.RGBA(127, 127, 127, 0.12)).Label("Sidebar").Children(func() {
		for i, it := range []struct {
			icon *ui.SVG
			name string
		}{{iconTree, "Files (⌘1)"}, {iconHistory, "History (⌘2)"}} {
			s := seg.Segment(i).Size(30, 24).Radius(6).Center().Label(it.name).Tooltip(it.name).TextColor(t.TextMuted)
			if i == tab {
				s.Background(pal.headerBg).Shadow(0, 1, 2, 0, ui.RGBA(0, 0, 0, 0.12)).TextColor(t.Text)
			}
			s.Children(func() { ui.Icon(c, it.icon).FontSize(15) })
		}
	})
	if tab != w.tab {
		w.tab = tab
		return true
	}
	return false
}

// sidebarToggle shows and hides the sidebar.
func (w *window) sidebarToggle(c *ui.Context) {
	tip := "Hide sidebar (⌘⇧B)"
	if !w.sidebarShown {
		tip = "Show sidebar (⌘⇧B)"
	}
	if iconButton(c, iconSidebar, tip).Clicked() {
		w.toggleSidebar()
	}
}

func (w *window) toggleSidebar() {
	w.sidebarShown = !w.sidebarShown
	if w.sidebarShown && w.sidebarWidth < sidebarMin {
		w.sidebarWidth = sidebarDefault
	}
	go state.setLayout(w.sidebarWidth, w.sidebarShown)
}

// searchInput is a field filtering a list, as the search fields of
// macOS: a magnifying glass, the text, and a button clearing it, as Escape
// does. It reports a change.
func searchInput(c *ui.Context, query *string, placeholder string, focus *bool, typing *bool) bool {
	t := c.Theme()
	changed := false
	box := ui.Row(c).Height(28).Padding(0, 6, 0, 8).Gap(6).Radius(7).Background(ui.RGBA(127, 127, 127, 0.12))
	box.Children(func() {
		ui.Icon(c, iconSearch).FontSize(13).TextColor(t.TextMuted)
		in := ui.TextInputBase(c, query).Placeholder(placeholder).Label(placeholder).FontSize(13).Grow(1).MinWidth(0)
		if *focus {
			in.Focus()
			*focus = false
		}
		if in.Changed() {
			changed = true
		}
		if in.Focused() {
			box.Shadow(0, 0, 0, 3, t.Focus.Alpha(0.45))
			*typing = true
		}
		if *query != "" {
			clear := ui.ButtonBase(c).Size(16, 16).Radius(8).Center().Background(t.TextMuted.Alpha(0.45)).Label("Clear").FocusRing(false)
			clear.Children(func() { ui.Icon(c, iconClose).FontSize(10).TextColor(t.Background) })
			if clear.Clicked() || in.Shortcut(0, ui.KeyEscape) {
				*query = ""
				changed = true
			}
		}
	})
	return changed
}

// iconButton is a button showing an icon alone, as in a toolbar.
func iconButton(c *ui.Context, svg *ui.SVG, tip string) *ui.Element {
	t := c.Theme()
	b := ui.ButtonBase(c).Size(28, 28).Radius(7).Center().Label(tip).Tooltip(tip).TextColor(t.TextMuted)
	if b.Pressed() {
		b.Background(ui.RGBA(127, 127, 127, 0.22))
	} else if b.Hovered() {
		b.Background(ui.RGBA(127, 127, 127, 0.13))
	}
	b.Children(func() { ui.Icon(c, svg).FontSize(16) })
	return b
}

// treeRow is a row of the file tree as it shows: an item and its depth.
type treeRow struct {
	key   string
	depth int
}

// visibleTree lists the rows of the tree, inside the directories open.
func (w *window) visibleTree() []treeRow {
	var rows []treeRow
	var walk func(keys []string, depth int)
	walk = func(keys []string, depth int) {
		for _, k := range keys {
			rows = append(rows, treeRow{k, depth})
			if n := w.treeItems[k]; n != nil && n.dir && !w.closedDirs[k] {
				walk(n.children, depth+1)
			}
		}
	}
	walk(w.treeRoots, 0)
	return rows
}

func (w *window) fileTree(c *ui.Context) {
	t := c.Theme()
	pal := paletteFor(t)
	if len(w.treeRoots) == 0 {
		ui.Column(c).Grow(1).Center().Padding(16).Children(func() {
			msg := "No changed files"
			switch {
			case w.loading && len(w.files) == 0:
				msg = "Loading…"
			case strings.TrimSpace(w.filter) != "":
				msg = "No matching files"
			}
			ui.Text(c, msg).FontSize(12).TextColor(t.TextMuted)
		})
		return
	}
	rows := w.visibleTree()
	w.treeRows = rows
	focused := w.treeEl != nil && w.treeEl.FocusWithin()
	choose := func(key string) {
		w.treeSel = key
		if n := w.treeItems[key]; n != nil && !n.dir {
			w.commitOpen = false
			w.revealFile(n.file)
		}
	}
	w.treeList.Key = func(i int) any { return rows[i].key }
	w.treeList.Label = func(i int) string {
		if n := w.treeItems[rows[i].key]; n != nil {
			return n.name
		}
		return ""
	}
	list := ui.List(c, &w.treeList, len(rows), func(i int) {
		key := rows[i].key
		n := w.treeItems[key]
		if n == nil {
			return
		}
		selected := key == w.treeSel
		row := ui.Row(c).Height(28).Padding(0, 8, 0, 6+float32(rows[i].depth)*14).Gap(5).Radius(6).MinWidth(0)
		textColor := t.Text
		switch {
		case selected && focused:
			row.Background(t.Accent)
			textColor = t.AccentText
		case selected:
			row.Background(ui.RGBA(127, 127, 127, 0.2))
		case row.Hovered():
			row.Background(ui.RGBA(127, 127, 127, 0.08))
		}
		if row.Clicked() {
			if n.dir {
				w.closedDirs[key] = !w.closedDirs[key]
			}
			choose(key)
		}
		muted := t.TextMuted
		if selected && focused {
			muted = t.AccentText.Alpha(0.8)
		}
		row.TextColor(textColor).Children(func() {
			arrow := ui.Box(c).Size(14, 14).Center().Shrink(0)
			if n.dir {
				arrow.Children(func() {
					ic := ui.Icon(c, iconChevronDown).FontSize(12).TextColor(muted)
					target := float32(0)
					if w.closedDirs[key] {
						target = -90
					}
					ic.Rotate(ic.Animate("rot", target, 150*time.Millisecond))
				})
				ui.Icon(c, iconFolder).FontSize(14).TextColor(muted)
				ui.Text(c, n.name).FontSize(13).SingleLine().Grow(1).Shrink(1).MinWidth(0)
				return
			}
			f := w.files[n.file]
			viewed := w.isViewed(f)
			ui.Icon(c, iconFile).FontSize(14).TextColor(muted)
			name := ui.Text(c, n.name).FontSize(13).SingleLine().Grow(1).Shrink(1).MinWidth(0)
			if viewed && !(selected && focused) {
				name.TextColor(t.TextMuted)
			}
			if countable(f) && (f.Additions > 0 || f.Deletions > 0) {
				ui.Textf(c, "+%s -%s", compact(f.Additions), compact(f.Deletions)).
					Font(w.codeFont()).FontSize(10).FontWeight(600).TextColor(muted).Shrink(0).
					Tooltip(lines(f.Additions, "added") + ", " + lines(f.Deletions, "removed"))
			}
			letter := statusColor(f.Status, pal, t)
			switch {
			case selected && focused:
				letter = t.AccentText
			case viewed:
				letter = t.TextMuted
			case w.reloaded[f.Path]:
				letter = pal.ref
			}
			ui.Text(c, statusLetter(f)).Font(w.codeFont()).FontSize(11).FontWeight(700).TextColor(letter).
				Width(12).TextAlign(ui.Center).Shrink(0).Tooltip(f.Status.Label())
		})
	}).Grow(1).Padding(2, 8).Gap(1).Focusable().FocusRing(false).Label("Changed files")
	w.treeEl = list

	// The keys move the choice, open and close directories.
	at := -1
	for i, r := range rows {
		if r.key == w.treeSel {
			at = i
		}
	}
	move := func(i int) {
		if i >= 0 && i < len(rows) {
			choose(rows[i].key)
			w.treeList.ScrollIntoView(i)
		}
	}
	if list.Shortcut(0, ui.KeyDown) {
		move(at + 1)
	}
	if list.Shortcut(0, ui.KeyUp) {
		move(max(at-1, 0))
	}
	if list.Shortcut(0, ui.KeyHome) {
		move(0)
	}
	if list.Shortcut(0, ui.KeyEnd) {
		move(len(rows) - 1)
	}
	if at >= 0 {
		n := w.treeItems[rows[at].key]
		if list.Shortcut(0, ui.KeyRight) && n != nil && n.dir {
			if w.closedDirs[rows[at].key] {
				w.closedDirs[rows[at].key] = false
			} else {
				move(at + 1)
			}
		}
		if list.Shortcut(0, ui.KeyLeft) {
			if n != nil && n.dir && !w.closedDirs[rows[at].key] {
				w.closedDirs[rows[at].key] = true
			} else {
				// To the directory holding it.
				for j := at - 1; j >= 0; j-- {
					if rows[j].depth < rows[at].depth {
						move(j)
						break
					}
				}
			}
		}
	}
}

// selectTreeFile chooses a file's row in the tree, as the surface
// scrolls to it.
func (w *window) selectTreeFile(i int) {
	key := "f:" + w.files[i].Path
	if w.treeSel == key {
		return
	}
	w.treeSel = key
	// The directories holding it open.
	for k, n := range w.treeItems {
		if n.dir && w.closedDirs[k] && strings.HasPrefix(w.files[i].Path, strings.TrimPrefix(k, "d:")+"/") {
			w.closedDirs[k] = false
		}
	}
	for r, row := range w.visibleTree() {
		if row.key == key {
			w.treeList.ScrollIntoView(r)
			return
		}
	}
}

// historyView lists the commits of the History tab.
func (w *window) historyView(c *ui.Context) {
	t := c.Theme()
	pal := paletteFor(t)
	q := strings.ToLower(strings.TrimSpace(w.historyFilter))
	type entry struct {
		local  bool
		commit *git.Commit
	}
	var entries []entry
	if q == "" {
		entries = append(entries, entry{local: true})
	}
	for i := range w.history {
		cm := &w.history[i]
		if q == "" || strings.Contains(strings.ToLower(cm.Subject), q) || strings.HasPrefix(cm.Hash, q) || strings.Contains(strings.ToLower(cm.Author), q) {
			entries = append(entries, entry{commit: cm})
		}
	}
	target := w.source
	current := -1
	for i, e := range entries {
		if (e.local && target.kind != sourceCommit) || (e.commit != nil && target.kind == sourceCommit && target.ref == e.commit.Hash) {
			current = i
		}
	}
	open := func(i int) {
		if i < 0 || i >= len(entries) {
			return
		}
		w.commitOpen = false
		w.historyList.ScrollIntoView(i)
		if entries[i].local {
			w.setSource(w.launchWorkTree())
		} else {
			w.setSource(source{kind: sourceCommit, ref: entries[i].commit.Hash})
		}
	}
	w.historyList.Key = func(i int) any {
		if entries[i].local {
			return "local"
		}
		return entries[i].commit.Hash
	}
	w.historyList.Label = func(i int) string {
		if entries[i].local {
			return "Uncommitted changes"
		}
		return entries[i].commit.Subject
	}
	focused := w.focusHistory || w.historyEl != nil && w.historyEl.FocusWithin()
	now := time.Now()
	list := ui.List(c, &w.historyList, len(entries), func(i int) {
		e := entries[i]
		row := ui.Row(c).Gap(8).Padding(5, 8).Radius(6).AlignItems(ui.Start).Role(ui.RoleButton)
		if e.local {
			row.Label("Uncommitted changes")
		} else {
			row.Label(e.commit.Subject)
		}
		muted, ref := t.TextMuted, pal.ref
		switch {
		case i == current && focused:
			row.Background(t.Accent).TextColor(t.AccentText)
			muted, ref = t.AccentText.Alpha(0.75), t.AccentText
		case i == current:
			row.Background(ui.RGBA(127, 127, 127, 0.2))
		case row.Hovered():
			row.Background(ui.RGBA(127, 127, 127, 0.08))
		}
		if row.Clicked() {
			if debugFrames {
				log.Printf("history row %d clicked", i)
			}
			open(i)
		}
		row.Children(func() {
			if e.local {
				ui.Text(c, "local").Font(w.codeFont()).FontSize(12).TextColor(ref).Width(56).Shrink(0)
				ui.Text(c, "Uncommitted changes").FontSize(12).SingleLine().Grow(1)
				return
			}
			cm := e.commit
			when := w.commitTimes[cm.Hash]
			if when.at != now.Truncate(time.Minute) {
				// Formatted once a minute, rather than every frame.
				when = commitTime{at: now.Truncate(time.Minute), ago: relativeTime(now, cm.Time), full: cm.Time.Format("Mon Jan 2 15:04:05 2006")}
				w.commitTimes[cm.Hash] = when
			}
			ui.Text(c, cm.Short).Font(w.codeFont()).FontSize(12).TextColor(ref).Width(56).Shrink(0)
			ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
				ui.Text(c, cm.Subject).FontSize(12).SingleLine().Tooltip(cm.Subject)
				ui.Row(c).Gap(6).Children(func() {
					ui.Text(c, cm.Author).FontSize(10).SingleLine().TextColor(muted).Grow(1).MinWidth(0)
					ui.Text(c, when.ago).FontSize(10).TextColor(muted).Shrink(0).Tooltip(when.full)
				})
			})
		})
	}).Grow(1).Padding(2, 8).Gap(1).Focusable().FocusRing(false).Label("History")
	w.historyEl = list
	if w.focusHistory {
		list.Focus()
		w.focusHistory = false
	}
	list.Children(func() {
		if len(entries) == 0 {
			ui.Text(c, "No matching commits").FontSize(12).TextColor(t.TextMuted).Padding(12)
		}
	})
	if list.Shortcut(0, ui.KeyDown) {
		open(current + 1)
	}
	if list.Shortcut(0, ui.KeyUp) {
		open(max(current-1, 0))
	}
	// More commits load well before the end comes into view, so that
	// scrolling does not stop there.
	if q == "" && w.historyMore && !w.historyLoading {
		if _, last := w.historyList.Visible(); last >= len(entries)-historyPage/2 {
			w.historyLimit += historyPage
			w.loadHistory()
		}
	}
}

// commitTime is how a commit's time shows, as of a minute.
type commitTime struct {
	at        time.Time
	ago, full string
}

// launchWorkTree is the source of uncommitted changes: the work tree, or
// its comparison with a branch when the window was opened on one.
func (w *window) launchWorkTree() source {
	if w.launch.kind == sourceBranch {
		return w.launch
	}
	return source{kind: sourceWorkingTree}
}

// relativeTime writes how long ago t was: just now, 5m ago, 3d ago.
func relativeTime(now, t time.Time) string {
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d/time.Minute))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d/time.Hour))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d/(24*time.Hour)))
	case d < 365*24*time.Hour:
		return fmt.Sprintf("%dmo ago", int(d/(30*24*time.Hour)))
	}
	return fmt.Sprintf("%dy ago", int(d/(365*24*time.Hour)))
}

// sidebarFooter shows the total of the changes, and the Commit button.
func (w *window) sidebarFooter(c *ui.Context, pal *palette) {
	t := c.Theme()
	adds, dels, counted := 0, 0, false
	for i, f := range w.files {
		if w.fileVisible(i) && countable(f) {
			adds += f.Additions
			dels += f.Deletions
			counted = true
		}
	}
	canCommit := w.tab == 0 && w.source.kind == sourceWorkingTree && len(w.files) > 0
	if !(counted && w.tab == 0) && !canCommit {
		return
	}
	ui.Row(c).MinHeight(40).Padding(6, 10).Gap(8).BorderWidth(1, 0, 0, 0).BorderColor(pal.cardBorder).Children(func() {
		if counted && w.tab == 0 {
			ui.Row(c).Gap(6).Tooltip("Total change: " + lines(adds, "added") + ", " + lines(dels, "removed")).Children(func() {
				ui.Text(c, "Total:").FontSize(11).FontWeight(600).TextColor(t.TextMuted)
				ui.Text(c, "+"+thousands(adds)).Font(w.codeFont()).FontSize(11).FontWeight(600).TextColor(pal.addText)
				ui.Text(c, "-"+thousands(dels)).Font(w.codeFont()).FontSize(11).FontWeight(600).TextColor(pal.delText)
			})
		}
		ui.Spacer(c)
		if canCommit {
			label := "Commit"
			if w.commitOpen {
				label = "Review"
			}
			b := ui.Button(c, "").Children(func() {
				ui.Icon(c, iconCommit).FontSize(14)
				ui.Text(c, label).SingleLine()
			})
			if b.Clicked() {
				w.toggleCommit()
			}
		}
	})
}

// sidebarResizer is the edge of the sidebar the user drags.
func (w *window) sidebarResizer(c *ui.Context, pal *palette) {
	ui.Box(c).Width(1).Shrink(0).Background(pal.cardBorder).Children(func() {
		handle := ui.Box(c).Absolute().Top(0).Bottom(0).Left(-4).Width(9).Cursor(ui.CursorResizeEW)
		if dx, _, held := handle.Dragged(); held {
			w.dragWidth += dx
			switch {
			case w.dragWidth < sidebarCollapse:
				w.sidebarShown = false
			default:
				w.sidebarShown = true
				w.sidebarWidth = min(max(w.dragWidth, sidebarMin), sidebarMax)
			}
		} else {
			if w.dragWidth != 0 && w.dragWidth != w.sidebarWidth {
				go state.setLayout(w.sidebarWidth, w.sidebarShown)
			}
			w.dragWidth = w.sidebarWidth
		}
	})
}
