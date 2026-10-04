package main

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/egoist/godiff/internal/diff"
	"github.com/egoist/godiff/internal/highlight"
	"github.com/egoist/mygo/ui"
)

// cardRadius rounds the corners of the files' cards.
const cardRadius = 10

// codeFont is the family of the code.
func (w *window) codeFont() string {
	if w.settings.CodeFontFamily != "" {
		return w.settings.CodeFontFamily + ", monospace"
	}
	return "SF Mono, Menlo, monospace"
}

func (w *window) codeSize() float32 { return float32(w.settings.CodeFontSize) }

// lineHeight is the height of a line of code: 20 at 13.
func (w *window) lineHeight() float32 {
	return float32(math.Round(float64(w.codeSize()) * 20 / 13))
}

// charWidth is the width of a column of code.
func (w *window) charWidth(c *ui.Context) float32 {
	key := fmt.Sprint(w.codeFont(), w.codeSize())
	if w.charKey != key {
		width, _ := c.MeasureText(0, ui.Span{Text: strings.Repeat("0", 20), Font: w.codeFont(), Size: w.codeSize()})
		w.charW, w.charKey = width/20, key
	}
	return w.charW
}

// fileMetrics are the widths a file's rows share: of its line numbers,
// and how far its lines go.
type fileMetrics struct {
	digits  int
	maxCols int
}

func (f *fileState) metrics() fileMetrics {
	if f.metricsDone {
		return f.metric
	}
	maxNum, maxCols := 0, 0
	for _, h := range f.Hunks {
		maxNum = max(maxNum, h.OldStart+h.OldLines, h.NewStart+h.NewLines)
		for _, l := range h.Lines {
			maxCols = max(maxCols, columns(l.Text))
		}
	}
	maxNum = max(maxNum, len(f.oldLines), len(f.newLines))
	if f.canExpand() {
		for _, l := range f.newLines {
			maxCols = max(maxCols, columns(l))
		}
		for _, l := range f.oldLines {
			maxCols = max(maxCols, columns(l))
		}
	}
	f.metric = fileMetrics{digits: max(len(fmt.Sprint(maxNum)), 2), maxCols: maxCols}
	f.metricsDone = f.loaded
	return f.metric
}

// diffList shows the files' cards, building the rows in view only.
func (w *window) diffList(c *ui.Context) {
	t := c.Theme()
	pal := paletteFor(t)
	if w.rowsDirty {
		w.updateMatches()
		w.buildRows()
	}
	list := ui.List(c, &w.list, len(w.rows), func(i int) { w.diffRow(c, pal, i) }).
		Grow(1).Padding(0, 12, 24).Background(pal.appBg).Label("Changes")
	w.diffListEl = list

	// The file at the top follows the scrolling, and the tree with it.
	if first, _ := w.list.Visible(); first >= 0 && first < len(w.rows) {
		file := int(w.rows[first].file)
		if file != w.current && time.Since(w.revealedAt) > 1200*time.Millisecond {
			w.current = file
			w.selectTreeFile(file)
		}
	}
}

// revealFile scrolls the surface to a file's card.
func (w *window) revealFile(i int) {
	if w.rowsDirty {
		w.buildRows()
	}
	for r := range w.rows {
		if w.rows[r].kind == rowHeader && int(w.rows[r].file) == i {
			w.list.ScrollTo(r, ui.Start)
			break
		}
	}
	w.current = i
	w.revealedAt = time.Now()
}

func (w *window) diffRow(c *ui.Context, pal *palette, i int) {
	r := &w.rows[i]
	f := w.files[r.file]
	switch r.kind {
	case rowHeader:
		w.fileHeader(c, pal, int(r.file), f)
	case rowNote:
		w.card(c, pal).Padding(10, 16).Background(pal.gapBg).Children(func() {
			ui.Text(c, w.fileNote(f)).FontSize(12).TextColor(pal.gapText)
		})
	case rowGap:
		w.gapRow(c, pal, f, r)
	case rowImage:
		w.imageRow(c, pal, f)
	case rowLine:
		w.lineRow(c, pal, f, r)
	case rowComment:
		w.commentRow(c, pal, f, r.comment)
	case rowEnd:
		if f.collapsed && !w.forceOpen(int(r.file)) {
			ui.Box(c).Height(0)
			return
		}
		ui.Box(c).Height(4).Background(pal.codeBg).
			BorderWidth(0, 1, 1, 1).BorderColor(pal.cardBorder).Radius(0, 0, cardRadius, cardRadius)
	}
}

// card is a row inside a file's card, between its sides.
func (w *window) card(c *ui.Context, pal *palette) *ui.Element {
	return ui.Row(c).Background(pal.codeBg).BorderWidth(0, 1, 0, 1).BorderColor(pal.cardBorder)
}

// forceOpen reports whether a collapsed file shows its lines anyway, as
// the files holding matches of the find bar do.
func (w *window) forceOpen(i int) bool {
	return w.searching() && w.fileMatches[i]
}

func (w *window) fileHeader(c *ui.Context, pal *palette, idx int, f *fileState) {
	t := c.Theme()
	collapsed := f.collapsed && !w.forceOpen(idx)
	viewed := w.isViewed(f)
	// The band above the header spaces the cards, and hides the lines
	// scrolling under it while it is pinned.
	band := ui.Column(c).Padding(12, 0, 0, 0).Background(pal.appBg)
	var h *ui.Element
	band.Children(func() {
		h = ui.Row(c).Height(46).Padding(0, 8, 0, 6).Gap(8).Background(pal.headerBg).Border(1, pal.cardBorder)
	})
	if collapsed {
		h.Radius(cardRadius)
	} else {
		h.Radius(cardRadius, cardRadius, 0, 0)
	}
	if idx == w.current && w.diffListEl != nil && w.diffListEl.FocusWithin() {
		h.Border(1, t.Accent.Alpha(0.6))
	}
	h.ContextMenu(func(m *ui.Menu) {
		if m.Item("Open in Editor").Disabled(f.Status == diff.Deleted).Chosen() {
			w.openInEditor(f.Path, firstLine(f))
		}
		if m.Item("Copy Path").Chosen() {
			c.WriteClipboard(f.Path)
		}
		if m.Item("Viewed").Checked(viewed).Chosen() {
			w.setViewed(f, !viewed)
		}
		m.Separator()
		if m.Item("Collapse All").Chosen() {
			w.setAllCollapsed(true)
		}
		if m.Item("Expand All").Chosen() {
			w.setAllCollapsed(false)
		}
	})
	h.Children(func() {
		toggle := ui.ButtonBase(c).Gap(8).Grow(1).Shrink(1).MinWidth(0).Height(46).Label(f.Path).Tooltip(f.Path)
		if toggle.Clicked() {
			f.collapsed = !collapsed
			if !f.collapsed && w.searching() {
				w.fileMatches[idx] = true
			}
			w.rowsDirty = true
		}
		toggle.FocusRing(false).Children(func() {
			chevron := ui.Box(c).Size(26, 26).Radius(13).Center().Shrink(0)
			if toggle.Hovered() {
				chevron.Background(pal.hover)
			}
			chevron.Children(func() {
				target := float32(0)
				if collapsed {
					target = -90
				}
				ic := ui.Icon(c, iconChevronDown).FontSize(15).TextColor(t.TextMuted)
				ic.Rotate(ic.Animate("rot", target, 160*time.Millisecond))
			})
			pathColor := t.Text
			if collapsed || viewed {
				pathColor = t.TextMuted
			}
			ui.Column(c).Grow(1).Shrink(1).MinWidth(0).Gap(1).Children(func() {
				ui.RichText(c,
					ui.Span{Text: f.Dir(), Color: t.TextMuted},
					ui.Span{Text: f.Name(), Color: pathColor, Weight: 600},
				).Font(w.codeFont()).FontSize(13).SingleLine()
				if f.OldPath != f.Path {
					ui.Text(c, "from "+f.OldPath).Font(w.codeFont()).FontSize(11).TextColor(t.TextMuted).SingleLine()
				}
			})
		})
		// The copy button shows while the pointer is over the header; it
		// is always built, so that the elements after it keep their state.
		cp := iconButton(c, iconCopy, "Copy path")
		if !h.Hovered() && !cp.FocusVisible() {
			cp.Opacity(0)
		}
		if cp.Clicked() {
			c.WriteClipboard(f.Path)
			c.Toast("Copied " + f.Name())
		}
		open := iconButton(c, iconOpen, "Open file in editor").Disabled(f.Status == diff.Deleted || f.Directory)
		if f.Status == diff.Deleted {
			open.Tooltip("Deleted files cannot be opened")
		}
		if open.Clicked() {
			w.openInEditor(f.Path, firstLine(f))
		}
		if countable(f) && (f.Additions > 0 || f.Deletions > 0) {
			ui.Row(c).Gap(8).Padding(4, 9).Radius(14).Background(pal.pill).Shrink(0).
				Tooltip(lines(f.Additions, "added") + ", " + lines(f.Deletions, "removed")).Children(func() {
				ui.Text(c, "+"+thousands(f.Additions)).Font(w.codeFont()).FontSize(12).FontWeight(600).TextColor(pal.addText)
				ui.Text(c, "-"+thousands(f.Deletions)).Font(w.codeFont()).FontSize(12).FontWeight(600).TextColor(pal.delText)
			})
		}
		if f.Generated {
			ui.Text(c, "Generated").FontSize(11).FontWeight(600).Padding(4, 9).Radius(14).
				Background(pal.ref.Alpha(0.15)).TextColor(pal.ref).Shrink(0)
		}
		w.viewedButton(c, pal, f, viewed)
	})
}

// viewedButton marks a file viewed, which collapses it.
func (w *window) viewedButton(c *ui.Context, pal *palette, f *fileState, viewed bool) {
	t := c.Theme()
	on := viewed
	b := ui.CheckboxBase(c, &on).Gap(7).Height(30).Padding(0, 11).Radius(14).Shrink(0).
		Border(1, ui.RGBA(127, 127, 127, 0.22)).Label("Viewed")
	if b.Changed() {
		w.setViewed(f, on)
	}
	if b.Hovered() {
		b.Background(pal.hover)
	}
	if viewed {
		b.Border(1, pal.viewed.Alpha(0.44)).TextColor(pal.viewed)
	}
	b.Children(func() {
		box := ui.Box(c).Size(15, 15).Radius(4).Center()
		if viewed {
			box.Background(pal.viewed).Children(func() {
				ui.Icon(c, iconCheck).FontSize(11).TextColor(pal.codeBg)
			})
		} else {
			box.Border(1.5, ui.RGBA(127, 127, 127, 0.45))
		}
		ui.Text(c, "Viewed").FontSize(12).FontWeight(600).TextColor(map[bool]ui.Color{true: pal.viewed, false: t.Text}[viewed])
	})
}

// setAllCollapsed collapses or expands every file.
func (w *window) setAllCollapsed(collapsed bool) {
	for _, f := range w.files {
		f.collapsed = collapsed
	}
	w.rowsDirty = true
}

// firstLine is the first changed line of a file, to open it there.
func firstLine(f *fileState) int {
	for _, h := range f.Hunks {
		for _, l := range h.Lines {
			if l.Kind == diff.Add {
				return l.New
			}
		}
		if h.NewStart > 0 {
			return h.NewStart
		}
	}
	return 0
}

// gutterWidth is the width of a column of line numbers.
func (w *window) gutterWidth(c *ui.Context, f *fileState) float32 {
	return float32(f.metrics().digits)*w.charWidth(c) + 18
}

func (w *window) gapRow(c *ui.Context, pal *palette, f *fileState, r *row) {
	t := c.Theme()
	gutter := w.gutterWidth(c, f)
	if !w.splitFile(f) && !f.oneSided() {
		gutter = 2*gutter - 4
	}
	first, last := r.gap == 0, int(r.gap) == len(f.Hunks)
	n := int(r.count)
	expand := func(top, bottom int) {
		if f.expanded == nil {
			f.expanded = map[int]gapShown{}
		}
		s := f.expanded[int(r.gap)]
		s.top += top
		s.bottom += bottom
		f.expanded[int(r.gap)] = s
		f.metricsDone = false
		w.rowsDirty = true
	}
	all := func() { expand(n, 0) }
	can := f.canExpand()
	w.card(c, pal).Height(30).Background(pal.gapBg).Children(func() {
		ui.Row(c).Width(gutter + 4).Height(30).Shrink(0).Children(func() {
			button := func(svg *ui.SVG, tip string, action func()) {
				b := ui.ButtonBase(c).Grow(1).Height(30).Center().Label(tip).Tooltip(tip).TextColor(pal.gapText).Disabled(!can)
				b.BorderWidth(0, 2, 0, 0).BorderColor(pal.codeBg)
				if b.Hovered() {
					b.TextColor(t.Text).Background(pal.hover)
				}
				b.Children(func() { ui.Icon(c, svg).FontSize(14) })
				if b.Clicked() {
					action()
				}
			}
			switch {
			case n <= expandStep && first:
				button(iconArrowUp, "Show the lines above", all)
			case n <= expandStep && last:
				button(iconArrowDown, "Show the lines below", all)
			case n <= expandStep:
				button(iconExpand, "Show the lines", all)
			default:
				if !first {
					button(iconArrowDown, fmt.Sprintf("Show %d more lines", expandStep), func() { expand(expandStep, 0) })
				}
				if !last {
					button(iconArrowUp, fmt.Sprintf("Show %d more lines", expandStep), func() { expand(0, expandStep) })
				}
			}
		})
		label := ui.ButtonBase(c).Padding(0, 12).Height(30).FocusRing(false).Disabled(!can).Children(func() {
			text := fmt.Sprintf("%d unmodified lines", n)
			if n == 1 {
				text = "1 unmodified line"
			}
			ui.Text(c, text).FontSize(12).TextColor(pal.gapText)
		})
		if label.Hovered() && can {
			label.Underline()
		}
		if label.Clicked() {
			switch {
			case n <= expandStep:
				all()
			case first:
				expand(0, expandStep)
			case last:
				expand(expandStep, 0)
			default:
				expand(expandStep, expandStep)
			}
		}
		if n > expandStep {
			b := ui.ButtonBase(c).Padding(3, 8).Radius(6).Disabled(!can).Children(func() {
				ui.Text(c, "Expand all").FontSize(12).FontWeight(600).TextColor(pal.gapText)
			})
			if b.Hovered() {
				b.Background(pal.hover)
			}
			if b.Clicked() {
				all()
			}
		}
		if h := r.gap; int(h) < len(f.Hunks) && f.Hunks[h].Section != "" {
			ui.Text(c, f.Hunks[h].Section).Font(w.codeFont()).FontSize(12).TextColor(pal.gapText).SingleLine().Grow(1).Shrink(1).MinWidth(0)
		}
	})
}

// lineSide is what one side of a line row shows.
type lineSide struct {
	present bool
	kind    diff.LineKind
	num     int
	text    string
	segs    []highlight.Seg
	words   []diff.Range
	hunk    int32
	index   int32 // the line's index in its hunk, -1 for expanded context
}

// sideOf returns a line of a row for the old or the new side.
func (w *window) sideOf(f *fileState, r *row, s side) lineSide {
	if r.hunk < 0 {
		// Expanded context.
		ls := lineSide{present: true, kind: diff.Context, hunk: -1, index: -1}
		if s == sideOld {
			ls.num = int(r.old)
			ls.text = f.contextText(r.old, r.new)
			if int(r.old) <= len(f.oldHL) {
				ls.segs = f.oldHL[r.old-1]
			} else if int(r.new) <= len(f.newHL) {
				ls.segs = f.newHL[r.new-1]
			}
		} else {
			ls.num = int(r.new)
			ls.text = f.contextText(r.old, r.new)
			if int(r.new) <= len(f.newHL) {
				ls.segs = f.newHL[r.new-1]
			}
		}
		return ls
	}
	idx := r.a
	if s == sideNew && w.splitFile(f) {
		idx = r.b
	}
	l := f.lineAt(r.hunk, idx)
	if l == nil {
		return lineSide{}
	}
	ls := lineSide{present: true, kind: l.Kind, text: l.Text, hunk: r.hunk, index: idx}
	if l.Kind == diff.Del {
		ls.num = l.Old
		if l.Old <= len(f.oldHL) {
			ls.segs = f.oldHL[l.Old-1]
		}
	} else {
		ls.num = l.New
		if s == sideOld && l.Kind == diff.Context {
			ls.num = l.Old
		}
		if l.New > 0 && l.New <= len(f.newHL) {
			ls.segs = f.newHL[l.New-1]
		}
	}
	ls.words = f.words[[2]int{int(r.hunk), int(idx)}]
	return ls
}

func (w *window) lineRow(c *ui.Context, pal *palette, f *fileState, r *row) {
	gutter := w.gutterWidth(c, f)
	lh := w.lineHeight()
	e := w.card(c, pal).AlignItems(ui.Stretch).MinHeight(lh)
	hs := w.hscroll[f.Path]
	if !w.settings.WordWrap {
		e.HandleInput(func(ev ui.InputEvent) bool {
			if ev.Kind != ui.InputScroll || math.Abs(float64(ev.DX)) <= math.Abs(float64(ev.DY)) {
				return false
			}
			b := e.Bounds()
			code := b.W - 2*gutter - 40
			if w.splitFile(f) {
				code = b.W/2 - gutter - 30
			}
			limit := max(float32(f.metrics().maxCols)*w.charWidth(c)-code, 0)
			w.hscroll[f.Path] = min(max(w.hscroll[f.Path]+ev.DX, 0), limit)
			w.invalidate()
			return true
		})
	}
	if !w.splitFile(f) {
		s := w.sideOf(f, r, sideNew)
		sd := sideNew
		if s.kind == diff.Del {
			sd = sideOld
		}
		e.Children(func() {
			w.lineCell(c, pal, f, r, s, sd, gutter, hs, !f.oneSided())
		})
		return
	}
	e.Children(func() {
		left := w.sideOf(f, r, sideOld)
		right := w.sideOf(f, r, sideNew)
		ui.Row(c).Grow(1).Basis(0).MinWidth(0).AlignItems(ui.Stretch).Children(func() {
			w.lineCell(c, pal, f, r, left, sideOld, gutter, hs, false)
		})
		ui.Box(c).Width(1).Shrink(0).Background(pal.cardBorder)
		ui.Row(c).Grow(1).Basis(0).MinWidth(0).AlignItems(ui.Stretch).Children(func() {
			w.lineCell(c, pal, f, r, right, sideNew, gutter, hs, false)
		})
	})
}

// lineCell shows a line: its numbers and its code.
func (w *window) lineCell(c *ui.Context, pal *palette, f *fileState, r *row, s lineSide, sd side, gutter, hs float32, unified bool) {
	lh := w.lineHeight()
	if !s.present {
		ui.Box(c).Grow(1).Background(pal.emptySide).MinHeight(lh)
		return
	}
	bg, gutterBg, bar, numColor := pal.codeBg, pal.codeBg, ui.Transparent, pal.lineNumber
	var wordColor ui.Color
	switch s.kind {
	case diff.Add:
		bg, gutterBg, bar, numColor, wordColor = pal.addBg, pal.addGutter, pal.addBar, pal.addBar, pal.addWord
	case diff.Del:
		bg, gutterBg, bar, numColor, wordColor = pal.delBg, pal.delGutter, pal.delBar, pal.delBar, pal.delWord
	}
	cell := ui.Row(c).Grow(1).MinWidth(0).AlignItems(ui.Stretch).Background(bg)
	if w.isSelectedLine(f, s) {
		cell.Background(blend(bg, pal.selected))
		gutterBg = blend(gutterBg, pal.selected)
	} else if cell.Hovered() {
		cell.Background(blend(bg, pal.hover.Alpha(0.5)))
		gutterBg = blend(gutterBg, pal.hover.Alpha(0.5))
	}
	if cell.Clicked() {
		w.addComment(f.Path, sd, s.num, s.num)
	}
	cell.ContextMenu(func(m *ui.Menu) {
		if m.Item(fmt.Sprintf("Comment on Line %d", s.num)).Chosen() {
			w.addComment(f.Path, sd, s.num, s.num)
		}
		if m.Item("Copy Line").Chosen() {
			c.WriteClipboard(s.text)
		}
		m.Separator()
		line := s.num
		if sd == sideOld {
			line = 0
		}
		if m.Item("Open in Editor").Disabled(f.Status == diff.Deleted).Chosen() {
			w.openInEditor(f.Path, line)
		}
		if m.Item("Copy Path").Chosen() {
			c.WriteClipboard(f.Path)
		}
	})
	cell.Children(func() {
		num := func(n int, width float32) {
			txt := ""
			if n > 0 {
				txt = fmt.Sprint(n)
			}
			ui.Text(c, txt).Font(w.codeFont()).FontSize(w.codeSize()-1).FixedLineHeight(lh).TextColor(numColor).
				Width(width).TextAlign(ui.End).Padding(0, 8, 0, 0).Shrink(0)
		}
		hovered := cell.Hovered()
		ui.Row(c).Shrink(0).AlignItems(ui.Start).Background(gutterBg).Children(func() {
			ui.Box(c).Width(4).AlignSelf(ui.Stretch).Background(bar)
			if unified {
				old, new := 0, 0
				if r.hunk < 0 {
					old, new = int(r.old), int(r.new)
				} else if l := f.lineAt(r.hunk, s.index); l != nil {
					old, new = l.Old, l.New
				}
				num(old, gutter-4)
				num(new, gutter-4)
			} else {
				num(s.num, gutter-4)
			}
		})
		var marks []mark
		for _, rg := range s.words {
			marks = append(marks, mark{Range: rg, color: wordColor})
		}
		if w.searching() {
			active := w.activeMatch(f, s)
			for _, rg := range findRanges(s.text, w.query) {
				color := pal.match
				if active {
					color = pal.matchNow
				}
				marks = append(marks, mark{Range: rg, color: color})
			}
		}
		// The button commenting on the line, at the edge of the numbers
		// while the pointer is over the line; always built, so that the
		// elements after it keep their state.
		numbers := gutter
		if unified {
			numbers = 2*gutter - 4
		}
		plus := ui.ButtonBase(c).Absolute().Top((lh-18)/2).Left(numbers-9).Size(18, 18).Radius(5).Center().
			Background(c.Theme().Accent).TextColor(c.Theme().AccentText).Label("Comment").Tooltip("Comment on this line").FocusRing(false)
		if !hovered {
			plus.Opacity(0)
		}
		plus.Children(func() { ui.Icon(c, iconPlus).FontSize(13) })
		if plus.Clicked() {
			w.addComment(f.Path, sd, s.num, s.num)
		}
		// The margin keeps the clipped code clear of the line numbers.
		code := ui.Box(c).Grow(1).Basis(0).MinWidth(0).ClipX().Margin(0, 10)
		code.Children(func() {
			text := ui.RichText(c, codeSpans(s.text, s.segs, marks, pal)...).Font(w.codeFont()).
				FontSize(w.codeSize()).FixedLineHeight(lh).TextColor(pal.code)
			if !w.settings.WordWrap {
				text.NoWrap().AlignSelf(ui.Start).Left(-hs)
			}
		})
	})
}

// blend lays a translucent color over another.
func blend(base, over ui.Color) ui.Color { return over.Over(base) }

// commentRow shows a comment, under the line it is on.
func (w *window) commentRow(c *ui.Context, pal *palette, f *fileState, cm *comment) {
	t := c.Theme()
	w.card(c, pal).Padding(8, 16).Gap(10).AlignItems(ui.Start).Children(func() {
		ui.Avatar(c, w.userName(), nil).Size(28, 28).Shrink(0)
		box := ui.Column(c).Grow(1).MinWidth(0).Radius(12).Border(1, pal.cardBorder).Background(pal.headerBg).Clip()
		box.Children(func() {
			ui.Row(c).Height(32).Padding(0, 4, 0, 10).Gap(8).BorderWidth(0, 0, 1, 0).BorderColor(pal.cardBorder).Children(func() {
				ui.Text(c, w.userName()).FontSize(12).Bold()
				ui.Text(c, cm.label()).FontSize(11).TextColor(t.TextMuted)
				ui.Spacer(c)
				if iconButton(c, iconClose, "Delete comment").Size(24, 24).Clicked() {
					w.deleteComment(cm)
				}
			})
			area := ui.TextAreaBase(c, &cm.text).Placeholder("Write a review comment…").MinHeight(64).Padding(8, 10).
				FontSize(13).Label("Review comment")
			if cm.focus {
				area.Focus()
				cm.focus = false
			}
			if area.Shortcut(ui.Cmd, ui.KeyEnter) {
				w.focusList = true
			}
			if area.Shortcut(0, ui.KeyEscape) {
				if cm.pending() {
					w.discarding = cm
				} else {
					w.deleteComment(cm)
					w.focusList = true
				}
			}
			focused := area.Focused()
			if focused {
				w.typing = true
			}
			if cm.wasFocused && !focused && !cm.pending() {
				// An empty comment the user left goes.
				w.deleteComment(cm)
			}
			cm.wasFocused = focused
			if focused {
				box.Border(1, t.Accent.Alpha(0.6))
			}
		})
	})
}

// userName is the name of the git user, for comments.
func (w *window) userName() string {
	if w.user == "" {
		w.user = strings.TrimSpace(w.repo.ConfigValue("user.name"))
		if w.user == "" {
			w.user = strings.TrimSpace(w.repo.ConfigValue("user.email"))
		}
		if w.user == "" {
			w.user = "Git user"
		}
	}
	return w.user
}

// imageRow shows a changed picture: the old one and the new one, side by
// side, on a checkerboard for their transparency.
func (w *window) imageRow(c *ui.Context, pal *palette, f *fileState) {
	t := c.Theme()
	pane := func(label string, img *ui.Bitmap, size int, bar ui.Color) {
		ui.Column(c).Grow(1).Basis(0).MinWidth(0).Padding(14).Gap(8).AlignItems(ui.Center).Children(func() {
			if img == nil {
				ui.Text(c, "No "+strings.ToLower(label)+" image").FontSize(12).TextColor(t.TextMuted)
				return
			}
			iw, ih := img.Size()
			scale := min(float32(1), 320/float32(max(ih, 1)))
			ui.Box(c).Size(float32(iw)*scale, float32(ih)*scale).MaxWidthPercent(100).Radius(4).Border(1, pal.cardBorder).Clip().
				Stripes(ui.RGBA(127, 127, 127, 0.12), 6, 6, 45).Children(func() {
				ui.Image(c, img).Fill()
			})
			ui.Row(c).Gap(6).Children(func() {
				ui.Box(c).Size(8, 8).Radius(4).Background(bar)
				ui.Textf(c, "%s · %d×%d · %s", label, iw, ih, formatBytes(size)).FontSize(11).TextColor(t.TextMuted)
			})
		})
	}
	w.card(c, pal).AlignItems(ui.Stretch).Children(func() {
		if f.Status != diff.Added && f.Status != diff.Untracked {
			pane("Old", f.oldImage, f.oldSize, pal.delBar)
		}
		if f.Status != diff.Deleted && f.Status != diff.Added && f.Status != diff.Untracked {
			ui.Box(c).Width(1).Background(pal.cardBorder)
		}
		if f.Status != diff.Deleted {
			pane("New", f.newImage, f.newSize, pal.addBar)
		}
	})
}

// formatBytes writes a size: 512 B, 1.5 KiB, 12 MiB.
func formatBytes(n int) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	v, unit := float64(n)/1024, "KiB"
	for _, u := range []string{"MiB", "GiB"} {
		if v < 1024 {
			break
		}
		v, unit = v/1024, u
	}
	if v < 10 {
		return fmt.Sprintf("%.1f %s", v, unit)
	}
	return fmt.Sprintf("%.0f %s", v, unit)
}
