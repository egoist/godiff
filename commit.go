package main

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/egoist/godiff/internal/diff"
	"github.com/egoist/mygo/ui"
)

// toggleCommit shows the commit view, or the review again.
func (w *window) toggleCommit() {
	w.commitOpen = !w.commitOpen
	if w.commitOpen {
		w.commitDone = ""
		w.commitErr = ""
		w.commitFocus = true
		w.reconcileCommitPaths()
	}
}

// reconcileCommitPaths chooses the files new to the list, and forgets
// those gone from it.
func (w *window) reconcileCommitPaths() {
	if w.commitPaths == nil {
		w.commitPaths = map[string]bool{}
	}
	present := map[string]bool{}
	for _, f := range w.files {
		if f.Directory && strings.HasPrefix(f.Path, "Untracked files not shown") {
			continue
		}
		present[f.Path] = true
		if _, ok := w.commitPaths[f.Path]; !ok {
			w.commitPaths[f.Path] = true
		}
	}
	for p := range w.commitPaths {
		if !present[p] {
			delete(w.commitPaths, p)
		}
	}
}

// commitFiles are the files the commit view lists.
func (w *window) commitFiles() []*fileState {
	var out []*fileState
	for _, f := range w.files {
		if _, ok := w.commitPaths[f.Path]; ok {
			out = append(out, f)
		}
	}
	return out
}

// commitView composes and makes a commit of the files chosen.
func (w *window) commitView(c *ui.Context) {
	t := c.Theme()
	pal := paletteFor(t)
	w.reconcileCommitPaths()
	files := w.commitFiles()
	chosen, adds, dels := 0, 0, 0
	for _, f := range files {
		if w.commitPaths[f.Path] {
			chosen++
		}
		adds += f.Additions
		dels += f.Deletions
	}
	subject := strings.TrimSpace(w.subject)
	can := !w.commitBusy && w.commitDone == "" && chosen > 0 && subject != ""
	submit := func() {
		if can {
			w.makeCommit()
		}
	}
	ui.Column(c).Grow(1).MinHeight(0).Background(pal.appBg).Children(func() {
		ui.Row(c).Padding(12, 20).Gap(12).BorderWidth(0, 0, 1, 0).BorderColor(pal.cardBorder).Children(func() {
			ui.Text(c, "Commit").FontSize(22).Bold()
			if w.branch != "" {
				ui.Row(c).Gap(5).Padding(3, 8).Radius(12).Background(pal.ref.Alpha(0.13)).TextColor(pal.ref).Children(func() {
					ui.Icon(c, iconBranch).FontSize(12)
					ui.Text(c, w.branch).Font(w.codeFont()).FontSize(11).FontWeight(600)
				})
			}
			ui.RichText(c,
				ui.Span{Text: plural(len(files), "file") + " · ", Color: t.TextMuted},
				ui.Span{Text: "+" + thousands(adds), Color: pal.addText, Weight: 700},
				ui.Span{Text: " −" + thousands(dels), Color: pal.delText, Weight: 700},
			).FontSize(12).FontWeight(600)
		})
		ui.Scroll(c).Grow(1).Children(func() {
			ui.Column(c).MaxWidth(760).FillWidth().Margin(0, ui.Auto).Padding(24, 24, 32).Gap(10).Children(func() {
				if w.commitDone != "" {
					ui.Row(c).Gap(10).Padding(12, 14).Radius(12).Background(pal.viewed.Alpha(0.1)).Border(1, pal.viewed.Alpha(0.25)).Children(func() {
						ui.Icon(c, iconCheckCircle).FontSize(20).TextColor(pal.viewed)
						ui.Column(c).Gap(2).Children(func() {
							ui.Text(c, "Committed "+plural(w.committedFiles, "file")).Bold()
							ui.Textf(c, "%s · onto %s", w.commitDone, w.branch).Font(w.codeFont()).FontSize(12).TextColor(t.TextMuted)
						})
					})
				}
				subj := ui.TextInputBase(c, &w.subject).Placeholder("Summarize the change in one line…").Label("Subject").
					Font(w.codeFont()).FontSize(17).FontWeight(600).Padding(14, 16).Radius(14).Background(pal.codeBg).Border(1, pal.cardBorder)
				if w.commitFocus {
					subj.Focus()
					w.commitFocus = false
				}
				if subj.Focused() {
					subj.Border(1, t.Accent.Alpha(0.6)).Shadow(0, 0, 0, 3, t.Accent.Alpha(0.2))
				}
				if subj.Submitted() {
					submit()
				}
				n := utf8.RuneCountInString(w.subject)
				counter := ui.Textf(c, "%d/50", n).Font(w.codeFont()).FontSize(11).FontWeight(700).AlignSelf(ui.End).TextColor(t.TextMuted)
				if n > 50 {
					counter.TextColor(pal.ref)
				}
				ui.Text(c, "Summary").FontSize(13).Bold().Margin(6, 0, 0)
				body := ui.TextAreaBase(c, &w.body).Placeholder("Describe the change in a paragraph or two…").Label("Summary").
					FontSize(13).MinHeight(120).Padding(12, 14).Radius(14).Background(pal.codeBg).Border(1, pal.cardBorder)
				if body.Focused() {
					body.Border(1, t.Accent.Alpha(0.6)).Shadow(0, 0, 0, 3, t.Accent.Alpha(0.2))
				}
				ui.Row(c).Margin(14, 0, 0).Children(func() {
					ui.Text(c, "Files in this commit").FontSize(13).Bold()
					ui.Spacer(c)
					ui.Textf(c, "%d of %d selected", chosen, len(files)).Font(w.codeFont()).FontSize(11).TextColor(t.TextMuted)
				})
				ui.Column(c).Radius(14).Background(pal.codeBg).Border(1, pal.cardBorder).Padding(6).Gap(2).Children(func() {
					all := chosen == len(files) && len(files) > 0
					group := ui.ButtonBase(c).Gap(10).Padding(6, 8).Radius(8).Label("Changed files")
					if group.Hovered() {
						group.Background(pal.hover)
					}
					if group.Clicked() {
						for _, f := range files {
							w.commitPaths[f.Path] = !all
						}
					}
					group.Children(func() {
						checkMark(c, pal, all, chosen > 0 && !all)
						ui.Text(c, "Changed files").FontSize(13).FontWeight(600).Grow(1)
						ui.Textf(c, "%d/%d", chosen, len(files)).Font(w.codeFont()).FontSize(11).TextColor(t.TextMuted)
					})
					for _, f := range files {
						on := w.commitPaths[f.Path]
						row := ui.ButtonBase(c).Key(f.Path).Gap(10).Padding(5, 8, 5, 26).Radius(8).Label(f.Path)
						if row.Hovered() {
							row.Background(pal.hover)
						}
						if row.Clicked() {
							w.commitPaths[f.Path] = !on
						}
						if !on {
							row.Opacity(0.55)
						}
						row.Children(func() {
							checkMark(c, pal, on, false)
							fileIcon(c, f, 14)
							ui.RichText(c,
								ui.Span{Text: f.Dir(), Color: t.TextMuted},
								ui.Span{Text: f.Name()},
							).Font(w.codeFont()).FontSize(12).SingleLine().Grow(1).Shrink(1).MinWidth(0)
							ui.Text(c, statusLetter(f)).Font(w.codeFont()).FontSize(11).FontWeight(700).TextColor(statusColor(f.Status, pal, t))
							if countable(f) {
								ui.RichText(c,
									ui.Span{Text: "+" + thousands(f.Additions), Color: pal.addText},
									ui.Span{Text: " −" + thousands(f.Deletions), Color: pal.delText},
								).Font(w.codeFont()).FontSize(11).FontWeight(600).Shrink(0)
							}
						})
					}
				})
				if w.commitOutput != "" {
					ui.Column(c).Margin(10, 0, 0).Radius(12).Border(1, pal.cardBorder).Background(pal.codeBg).Clip().Children(func() {
						ui.Row(c).Padding(6, 6, 6, 12).BorderWidth(0, 0, 1, 0).BorderColor(pal.cardBorder).Children(func() {
							ui.Text(c, "Commit output").FontSize(12).Bold().Grow(1)
							if iconButton(c, iconClose, "Dismiss").Size(24, 24).Clicked() {
								w.commitOutput = ""
							}
						})
						ui.Text(c, w.commitOutput).Font(w.codeFont()).FontSize(12).Padding(10, 12).Selectable()
					})
				}
			})
		})
		ui.Row(c).Padding(10, 20).Gap(12).BorderWidth(1, 0, 0, 0).BorderColor(pal.cardBorder).Children(func() {
			if w.commitErr != "" {
				ui.Text(c, w.commitErr).FontSize(12).TextColor(t.Danger).Grow(1).Shrink(1).MaxLines(2)
			} else {
				ui.Text(c, "⌘↩ commits").FontSize(12).TextColor(t.TextMuted).Grow(1)
			}
			label := "Commit"
			switch {
			case w.commitBusy:
				label = "Committing…"
			case w.commitDone != "":
				label = "Committed"
			}
			b := ui.PrimaryButton(c, "").Disabled(!can).Children(func() {
				ui.Icon(c, iconCommit).FontSize(15)
				ui.Text(c, label).SingleLine()
				if chosen < len(files) && chosen > 0 {
					ui.Text(c, plural(chosen, "file")).FontSize(11).Padding(1, 6).Radius(8).Background(ui.RGBA(255, 255, 255, 0.22))
				}
			})
			if b.Clicked() {
				submit()
			}
		})
	})
	if c.Shortcut(ui.Cmd, ui.KeyEnter) {
		submit()
	}
}

// checkMark draws a check box: checked, mixed or empty.
func checkMark(c *ui.Context, pal *palette, on, mixed bool) {
	t := c.Theme()
	box := ui.Box(c).Size(16, 16).Radius(4).Center().Shrink(0)
	switch {
	case on:
		box.Background(t.Accent).Children(func() { ui.Icon(c, iconCheck).FontSize(11).TextColor(t.AccentText) })
	case mixed:
		box.Background(t.Accent).Children(func() { ui.Box(c).Size(8, 2).Radius(1).Background(t.AccentText) })
	default:
		box.Border(1.5, ui.RGBA(127, 127, 127, 0.45)).Background(pal.codeBg)
	}
}

func plural(n int, what string) string {
	if n == 1 {
		return "1 " + what
	}
	return fmt.Sprintf("%s %ss", thousands(n), what)
}

// makeCommit commits the files chosen.
func (w *window) makeCommit() {
	var paths []string
	count := 0
	for _, f := range w.commitFiles() {
		if !w.commitPaths[f.Path] {
			continue
		}
		count++
		paths = append(paths, f.Path)
		if f.OldPath != f.Path && f.Status == diff.Renamed {
			paths = append(paths, f.OldPath)
		}
	}
	msg := strings.TrimSpace(w.subject) + "\n"
	if body := strings.TrimSpace(w.body); body != "" {
		msg = strings.TrimSpace(w.subject) + "\n\n" + body + "\n"
	}
	w.commitBusy = true
	w.commitErr = ""
	w.commitOutput = ""
	w.background(func() {
		hash, err := w.repo.CommitChanges(msg, paths)
		w.update(func() {
			w.commitBusy = false
			if err != nil {
				w.commitErr = "The commit failed — see the output above."
				w.commitOutput = errorText(err)
				return
			}
			w.commitDone = hash
			w.committedFiles = count
			w.subject, w.body = "", ""
			w.loadHistory()
			if w.win != nil {
				go w.checkChanges()
			} else {
				w.changed = true
			}
		})
	})
}
