package main

import (
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/godiff/internal/git"
	"github.com/egoist/mygo/ui"
)

func (w *window) view(c *ui.Context) {
	if debugFrames {
		start := time.Now()
		defer func() {
			d := time.Since(start)
			if since := start.Sub(w.switchedAt); since < 3*time.Second {
				log.Printf("frame %v after the switch: view %v (files %d, rows %d, loading %v)", since, d, len(w.files), len(w.rows), w.loading)
			} else if d > 4*time.Millisecond {
				log.Printf("slow view: %v (rows %d, comments %d)", d, len(w.rows), len(w.comments))
			}
		}()
	}
	t := c.Theme()
	pal := paletteFor(t)
	w.now = c.Now()
	// On macOS, the window shows the sidebar's material where nothing is
	// drawn. Elsewhere it would show black, as under the translucent edge
	// of the sidebar, so the sidebar's color is drawn there instead.
	c.Root().Background(w.sidebarBg(t))
	w.shortcuts(c)

	ui.Row(c).Fill().AlignItems(ui.Stretch).Children(func() {
		right := w.settings.SidebarPosition == "right"
		if w.sidebarShown && !right {
			w.sidebar(c)
			w.sidebarResizer(c, pal)
		}
		ui.Column(c).Grow(1).MinWidth(0).Background(pal.appBg).Children(func() {
			w.toolbar(c, pal)
			w.mainArea(c, pal)
		})
		if w.sidebarShown && right {
			w.sidebarResizer(c, pal)
			w.sidebar(c)
		}
	})
	w.palette(c)
	w.sourceDialog(c)
	w.shortcutsHelp(c)
	if w.discarding != nil {
		open := true
		if choice := ui.AlertDialog(c, &open, "Discard this review comment?", "Its text will be lost.", "Cancel", "Discard"); choice == 1 {
			w.deleteComment(w.discarding)
			w.discarding = nil
		} else if !open {
			w.discarding = nil
		}
	}
	if !w.focusedOnce && w.diffListEl != nil && len(w.rows) > 0 {
		// The review takes the keys as the window opens.
		w.focusedOnce = true
		w.focusList = true
	}
	if w.focusList && w.diffListEl != nil {
		w.diffListEl.Focus()
		w.focusList = false
	}
}

// debugFrames logs the views that take long to build.
var debugFrames = os.Getenv("GODIFF_DEBUG") != ""

// toolbar is the bar along the top of the review, which drags the window.
func (w *window) toolbar(c *ui.Context, pal *palette) {
	t := c.Theme()
	bar := c.TitleBar()
	sidebarRight := w.sidebarShown && w.settings.SidebarPosition == "right"
	left := float32(14)
	if !w.sidebarShown || sidebarRight {
		left = bar.Left + 8
	}
	right := float32(12)
	if !sidebarRight {
		right += bar.Right
	}
	ui.Row(c).Height(titleBarHeight).Padding(0, right, 0, left).Gap(8).Shrink(0).DragWindow().
		BorderWidth(0, 0, 1, 0).BorderColor(pal.cardBorder).Background(pal.headerBg.Alpha(0.6)).Children(func() {
		if !w.sidebarShown {
			w.sidebarToggle(c)
		}
		// The repository: its name, and where it is.
		ui.Column(c).Gap(1).Shrink(1).MinWidth(0).Tooltip(w.repo.Root).Children(func() {
			ui.Text(c, filepath.Base(w.repo.Root)).FontSize(13).Bold().SingleLine()
			ui.Text(c, filepath.Dir(abbreviateHome(w.repo.Root))).FontSize(11).TextColor(t.TextMuted).SingleLine()
		})
		if w.branch != "" {
			chip(c, pal, iconBranch, w.branch, t.TextMuted).Tooltip("Branch " + w.branch)
		}
		switch w.source.kind {
		case sourceCommit:
			label := shortHash(w.source.ref)
			chip(c, pal, iconCommit, label, pal.ref).Font(w.codeFont()).Tooltip(w.source.ref)
		case sourceBranch:
			chip(c, pal, iconBranch, "Local + branch vs "+w.source.ref, pal.ref)
		}
		if w.loading && len(w.files) > 0 {
			ui.Spinner(c).Label("Loading").Size(14, 14)
		}
		ui.Spacer(c)
		if !w.commitOpen {
			// Find, the comments, and the layout.
			if iconButton(c, iconSearch, "Find in diffs (⌘F)").Clicked() {
				w.finding = !w.finding
			}
			n := w.pendingComments()
			copied := time.Since(w.copiedAt) < 2*time.Second
			cb := ui.ButtonBase(c).Height(28).Padding(0, 8).Gap(5).Radius(7).TextColor(t.TextMuted).
				Label("Copy review comments").Tooltip("Copy review comments as Markdown").Disabled(n == 0)
			if n == 0 {
				cb.Opacity(0.5)
			}
			if cb.Hovered() {
				cb.Background(ui.RGBA(127, 127, 127, 0.13))
			}
			if cb.Clicked() {
				w.copyComments()
				c.After(2 * time.Second)
			}
			cb.Children(func() {
				if copied {
					ui.Icon(c, iconCheck).FontSize(15).TextColor(pal.viewed)
				} else {
					ui.Icon(c, iconComment).FontSize(15)
				}
				ui.Textf(c, "%d", n).Font(w.codeFont()).FontSize(11).FontWeight(700)
			})
			w.layoutControl(c, pal)
		}
	})
}

// chip is a label in a pill, as the branch.
func chip(c *ui.Context, pal *palette, svg *ui.SVG, label string, color ui.Color) *ui.Element {
	return ui.Row(c).Gap(5).Padding(3, 8).Radius(12).Background(ui.RGBA(127, 127, 127, 0.08)).
		Border(1, ui.RGBA(127, 127, 127, 0.12)).TextColor(color).Shrink(1).MinWidth(0).MaxWidth(240).Children(func() {
		ui.Icon(c, svg).FontSize(12)
		ui.Text(c, label).FontSize(12).FontWeight(600).SingleLine()
	})
}

// layoutControl switches between split and unified diffs.
func (w *window) layoutControl(c *ui.Context, pal *palette) {
	t := c.Theme()
	choice := 0
	if !w.split() {
		choice = 1
	}
	seg := ui.SegmentedBase(c, &choice, 2)
	seg.Track.Padding(2).Gap(2).Radius(8).Background(ui.RGBA(127, 127, 127, 0.1)).Label("Diff layout").Children(func() {
		for i, it := range []struct {
			icon *ui.SVG
			name string
		}{{iconSplit, "Split"}, {iconUnified, "Unified"}} {
			s := seg.Segment(i).Size(30, 24).Radius(6).Center().Label(it.name).Tooltip(it.name).TextColor(t.TextMuted)
			if i == choice {
				s.Background(pal.headerBg).Shadow(0, 1, 2, 0, ui.RGBA(0, 0, 0, 0.12)).TextColor(t.Text)
			}
			s.Children(func() { ui.Icon(c, it.icon).FontSize(15) })
		}
	})
	if (choice == 0) != w.split() {
		toggleLayout()
		w.settings.DiffStyle = map[bool]string{true: "split", false: "unified"}[choice == 0]
		w.rowsDirty = true
	}
}

// mainArea shows the review, the commit view, or why there is nothing.
func (w *window) mainArea(c *ui.Context, pal *palette) {
	t := c.Theme()
	if w.commitOpen && w.source.kind == sourceWorkingTree {
		w.commitView(c)
		return
	}
	if w.changed {
		ui.Row(c).Justify(ui.Center).Padding(8, 12, 0).Children(func() {
			ui.Row(c).Gap(6).Padding(5, 6, 5, 12).Radius(16).Background(pal.viewed.Alpha(0.1).Over(pal.codeBg)).
				Border(1, pal.viewed.Alpha(0.2)).Children(func() {
				ui.Text(c, "Local changes detected,").FontSize(13).FontWeight(600).TextColor(pal.viewed.Mix(t.Text, 0.6))
				ref := ui.ButtonBase(c).FocusRing(true).Children(func() {
					ui.Text(c, "refresh to see them.").FontSize(13).FontWeight(600).Underline().TextColor(pal.viewed.Mix(t.Text, 0.6))
				})
				if ref.Clicked() {
					w.refresh()
				}
				if iconButton(c, iconClose, "Dismiss").Size(22, 22).Clicked() {
					w.changed = false
				}
			})
		})
	}
	find := ui.FindBar(c, &w.finding, &w.query, len(w.matches), &w.match).Label("Find in diffs")
	if find.FocusWithin() {
		w.typing = true
	}
	if find.Changed() {
		w.showMatch()
	}
	if w.query != w.matchesFor && (w.finding || w.matchesFor != "") {
		w.rowsDirty = true
	}
	if !w.finding && w.matchesFor != "" {
		w.query = ""
		w.rowsDirty = true
	}

	// A load shows "Thinking…" once it takes long: until then the window
	// keeps what it showed, so that quick loads change it once.
	waiting := w.loading && len(w.files) == 0
	slow := false
	if waiting {
		if left := thinkingDelay - time.Since(w.loadStart); left > 0 {
			c.After(left)
		} else {
			slow = true
		}
	}
	// The commit's message shows at once, from the history, until it
	// shows atop the changes.
	if w.source.kind == sourceCommit && w.commit != nil && w.loadErr == nil && len(w.files) == 0 {
		w.commitMessage(c, pal).Margin(11, 12, 0)
	}
	switch {
	case w.loadErr != nil:
		emptyPanel(c, pal, "Unable to read repository", errorText(w.loadErr), nil)
	case slow:
		thinking(c)
	case waiting && len(w.files) == 0:
		ui.Box(c).Grow(1)
	case len(w.files) == 0:
		title, detail := "No local changes", abbreviateHome(w.repo.Root)
		switch w.source.kind {
		case sourceCommit:
			title, detail = "No changes in commit", shortHash(w.source.ref)
		case sourceBranch:
			title, detail = "No changes", w.source.ref
		}
		emptyPanel(c, pal, title, detail, func() {
			if w.source.kind == sourceWorkingTree && len(w.history) > 0 {
				if ui.Button(c, "Show History").Clicked() {
					w.tab, w.sidebarShown = 1, true
				}
			}
		})
	default:
		if w.rowsDirty {
			w.updateMatches()
			w.buildRows()
		}
		if len(w.rows) == 0 {
			title, detail := "No matching files", strings.TrimSpace(w.filter)
			if w.searching() {
				title, detail = "No matches in diffs", strings.TrimSpace(w.query)
			}
			if detail == "" {
				detail = "Whitespace-only changes hidden"
			}
			emptyPanel(c, pal, title, detail, nil)
			return
		}
		w.diffList(c)
	}
}

// commitMessage shows the message of the commit reviewed.
func (w *window) commitMessage(c *ui.Context, pal *palette) *ui.Element {
	t := c.Theme()
	cm := w.commit
	return ui.Column(c).Padding(12, 16).Gap(6).Radius(cardRadius).Background(pal.headerBg).Border(1, pal.cardBorder).Children(func() {
		ui.Row(c).Gap(10).Children(func() {
			ui.Avatar(c, cm.Author, nil).Size(26, 26)
			ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
				ui.Text(c, cm.Subject).FontSize(14).Bold().SingleLine().Selectable()
				ui.Textf(c, "%s committed %s", cm.Author, relativeTime(w.now, cm.Time)).FontSize(11).TextColor(t.TextMuted).
					Tooltip(cm.Email + " · " + cm.Time.Format("Mon Jan 2 15:04:05 2006"))
			})
			ui.Text(c, shortHash(cm.Hash)).Font(w.codeFont()).FontSize(12).TextColor(pal.ref).Selectable()
		})
		if cm.Body != "" {
			ui.Text(c, cm.Body).FontSize(13).Selectable().Padding(4, 0, 0, 36).TextColor(t.Text.Alpha(0.85))
		}
	})
}

// emptyPanel says why there is nothing to show.
func emptyPanel(c *ui.Context, pal *palette, title, detail string, actions func()) {
	t := c.Theme()
	ui.Column(c).Grow(1).Center().Padding(24).Children(func() {
		ui.Column(c).MaxWidth(520).Padding(28).Gap(10).Radius(16).Background(pal.headerBg).Border(1, pal.cardBorder).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, iconFileDiff).FontSize(28).TextColor(t.TextMuted)
			ui.Text(c, title).FontSize(15).Bold().TextAlign(ui.Center)
			if detail != "" {
				ui.Text(c, detail).FontSize(13).Font("SF Mono, Menlo, monospace").TextColor(t.TextMuted).TextAlign(ui.Center).Selectable()
			}
			if actions != nil {
				ui.Row(c).Gap(8).Margin(6, 0, 0).Children(actions)
			}
		})
	})
}

// thinkingDelay is how long a load goes before the window says so.
const thinkingDelay = 200 * time.Millisecond

// thinking shows that the changes are loading.
func thinking(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Grow(1).Center().Children(func() {
		label := ui.Text(c, "Thinking…").Italic().FontSize(13).Font("SF Mono, Menlo, monospace").TextColor(t.TextMuted)
		label.Opacity(0.5 + 0.5*label.Loop("pulse", 1600*time.Millisecond, ui.Bounce(ui.EaseInOut)))
	})
}

// shortcuts handles the window's keys that the menus do not.
func (w *window) shortcuts(c *ui.Context) {
	if c.Shortcut(ui.Cmd|ui.Shift, ui.KeyP) {
		w.paletteOpen = !w.paletteOpen
		w.paletteRow = 0
	}
	// Keys that type text belong to the fields: these work while no field
	// had the focus in the last frame.
	typing := w.typing
	w.typing = false
	if w.commitOpen || w.paletteOpen || w.dialogOpen || w.help || typing || w.diffListEl == nil {
		return
	}
	if c.Shortcut(0, ui.KeyJ) || c.Shortcut(ui.Ctrl, ui.KeyDown) {
		w.nextHunk(1)
	}
	if c.Shortcut(0, ui.KeyK) || c.Shortcut(ui.Ctrl, ui.KeyUp) {
		w.nextHunk(-1)
	}
	if w.diffListEl.Shortcut(0, ui.KeyEnter) || (w.selHunk >= 0 && c.Shortcut(0, ui.KeyEnter)) {
		w.commentOnSelection()
	}
	if w.selHunk >= 0 && c.Shortcut(0, ui.KeyEscape) {
		w.selFile, w.selHunk = -1, -1
	}
	if c.Shortcut(ui.Alt, ui.KeyZ) {
		toggleWrap()
	}
	if c.Shortcut(ui.Shift, ui.KeySlash) {
		w.help = true
	}
}

// welcome is the window shown without a repository.
type welcome struct {
	err error
}

func (v *welcome) view(c *ui.Context) {
	t := c.Theme()
	pal := paletteFor(t)
	c.Root().Background(pal.appBg)
	bar := c.TitleBar()
	ui.Box(c).Height(max(bar.Height, 40)).DragWindow().FillWidth()
	title := "Open a Git repository"
	detail := "Run godiff from a Git repository in Terminal, or choose File → Open Folder… to open one."
	if v.err != nil && !errors.Is(v.err, git.ErrNotRepository) {
		title, detail = "Unable to read repository", errorText(v.err)
	} else if v.err != nil {
		title = "No Git repository found"
		detail = "Godiff was opened outside a Git repository. Run godiff from inside a repository, or choose File → Open Folder… to open one."
	}
	emptyPanel(c, pal, title, detail, func() {
		if ui.PrimaryButton(c, "Open Folder…").Clicked() {
			openFolder()
		}
	})
}
