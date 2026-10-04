package main

import (
	"github.com/egoist/godiff/internal/diff"
	"github.com/egoist/godiff/internal/highlight"
	"github.com/egoist/mygo/ui"
)

// fileState is a changed file as the review shows it: its change, the
// contents of both sides once loaded, and what the user did to it.
type fileState struct {
	*diff.File
	// The lines of the old and the new file, and their tokens, once loaded;
	// loaded is set then, even for files without contents.
	oldLines, newLines []string
	oldHL, newHL       [][]highlight.Seg
	loaded             bool
	// collapsed hides the file's lines.
	collapsed bool
	// expanded is how many lines of each gap of unchanged lines show, from
	// its top and its bottom.
	expanded map[int]gapShown
	// words are the changed ranges of lines replaced by others, by the
	// index of the hunk and of the line in it.
	words map[[2]int][]diff.Range
	// The old and the new image of a changed picture, and the sizes of
	// their files.
	oldImage, newImage *ui.Bitmap
	oldSize, newSize   int
	// metric is computed once the contents are loaded.
	metric      fileMetrics
	metricsDone bool
}

type gapShown struct{ top, bottom int }

// rowKind is what a row of the diff surface shows.
type rowKind uint8

const (
	rowHeader  rowKind = iota // a file's header, pinned while its lines scroll
	rowNote                   // why the lines do not show
	rowImage                  // a picture, before and after
	rowGap                    // unchanged lines not shown
	rowLine                   // a line, or a pair of lines side by side
	rowComment                // a comment on the line above
	rowEnd                    // the bottom of a file's card
)

// row is a row of the diff surface.
type row struct {
	kind rowKind
	file int32
	// For lines of hunks: the hunk, and the indices of its lines on the
	// left (old) and the right (new) side, -1 for none. Unified rows use a
	// alone. Lines of context the user expanded have hunk -1 and their
	// numbers in old and new.
	hunk     int32
	a, b     int32
	old, new int32
	// For gaps: which gap, its first lines on each side, and how many
	// lines it hides.
	gap   int32
	count int32
	// For comments: the comment.
	comment *comment
}

// rowKey identifies a row across rebuilds, so that the list keeps its
// place as rows come and go above it.
type rowKey struct {
	kind     rowKind
	file     string
	old, new int32
	gap      int32
	comment  *comment
}

// oneSided reports whether the file is all new or all gone, with lines on
// one side only.
func (f *fileState) oneSided() bool {
	switch f.Status {
	case diff.Added, diff.Untracked, diff.Deleted:
		return true
	}
	return false
}

// lineAt returns the line of a hunk, nil for -1.
func (f *fileState) lineAt(hunk, i int32) *diff.Line {
	if hunk < 0 || i < 0 {
		return nil
	}
	return &f.Hunks[hunk].Lines[i]
}

// gap is a run of unchanged lines between hunks, or before the first or
// after the last.
type gap struct {
	index              int
	oldStart, newStart int // the numbers of its first lines
	count              int
}

// lastLines returns the numbers of the last lines of a hunk on each side.
func lastLines(h *diff.Hunk) (int, int) {
	o := h.OldStart + h.OldLines - 1
	if h.OldLines == 0 {
		o = h.OldStart
	}
	n := h.NewStart + h.NewLines - 1
	if h.NewLines == 0 {
		n = h.NewStart
	}
	return o, n
}

// gapBefore returns the gap before hunk i; i == len(Hunks) is the gap
// after the last, known once the contents are loaded.
func (f *fileState) gapBefore(i int) gap {
	prevOld, prevNew := 0, 0
	if i > 0 {
		prevOld, prevNew = lastLines(&f.Hunks[i-1])
	}
	g := gap{index: i, oldStart: prevOld + 1, newStart: prevNew + 1}
	if i < len(f.Hunks) {
		h := &f.Hunks[i]
		first := h.OldStart
		if h.OldLines == 0 {
			first = h.OldStart + 1
		}
		g.count = first - g.oldStart
		return g
	}
	switch {
	case f.newLines != nil:
		g.count = len(f.newLines) - prevNew
	case f.oldLines != nil:
		g.count = len(f.oldLines) - prevOld
	}
	return g
}

// contextText returns the text of an unchanged line, by its numbers.
func (f *fileState) contextText(old, new int32) string {
	if new > 0 && int(new) <= len(f.newLines) {
		return f.newLines[new-1]
	}
	if old > 0 && int(old) <= len(f.oldLines) {
		return f.oldLines[old-1]
	}
	return ""
}

// canExpand reports whether gaps can show their lines.
func (f *fileState) canExpand() bool {
	return f.newLines != nil || f.oldLines != nil
}

// computeWords finds the changed words of the lines a hunk replaces.
func (f *fileState) computeWords() {
	if f.words != nil {
		return
	}
	f.words = map[[2]int][]diff.Range{}
	for hi := range f.Hunks {
		lines := f.Hunks[hi].Lines
		for _, p := range diff.Pairs(lines) {
			ra, rb := diff.WordDiff(lines[p.Del].Text, lines[p.Add].Text)
			if ra != nil || rb != nil {
				f.words[[2]int{hi, p.Del}] = ra
				f.words[[2]int{hi, p.Add}] = rb
			}
		}
	}
}

// expandStep is how many lines a click on a gap's arrows shows, and
// inlineGap the most unchanged lines between hunks shown without asking.
const (
	expandStep = 100
	inlineGap  = 12
)

// buildRows lays out the rows of the files.
func (w *window) buildRows() {
	rows := w.rows[:0]
	for fi, f := range w.files {
		if !w.fileVisible(fi) {
			continue
		}
		idx := int32(fi)
		rows = append(rows, row{kind: rowHeader, file: idx})
		if f.collapsed && !w.forceOpen(fi) {
			rows = append(rows, row{kind: rowEnd, file: idx})
			continue
		}
		if f.oldImage != nil || f.newImage != nil {
			rows = append(rows, row{kind: rowImage, file: idx})
			rows = append(rows, row{kind: rowEnd, file: idx})
			continue
		}
		if note := w.fileNote(f); note != "" {
			rows = append(rows, row{kind: rowNote, file: idx})
			rows = append(rows, row{kind: rowEnd, file: idx})
			continue
		}
		f.computeWords()
		split := w.splitFile(f)
		addGap := func(g gap) {
			if g.count <= 0 {
				return
			}
			shown := f.expanded[g.index]
			if !f.canExpand() {
				shown = gapShown{}
			} else if g.count <= inlineGap {
				shown = gapShown{top: g.count}
			}
			hidden := g.count - shown.top - shown.bottom
			if hidden <= 0 {
				shown = gapShown{top: g.count}
				hidden = 0
			}
			for k := range shown.top {
				rows = w.appendContext(rows, idx, f, int32(g.oldStart+k), int32(g.newStart+k))
			}
			if hidden > 0 {
				rows = append(rows, row{kind: rowGap, file: idx, gap: int32(g.index), old: int32(g.oldStart + shown.top), new: int32(g.newStart + shown.top), count: int32(hidden)})
			}
			for k := g.count - shown.bottom; k < g.count; k++ {
				rows = w.appendContext(rows, idx, f, int32(g.oldStart+k), int32(g.newStart+k))
			}
		}
		for hi := range f.Hunks {
			addGap(f.gapBefore(hi))
			lines := f.Hunks[hi].Lines
			h := int32(hi)
			if !split {
				for li := range lines {
					rows = append(rows, row{kind: rowLine, file: idx, hunk: h, a: int32(li), b: -1})
					rows = w.appendComments(rows, idx, f, &lines[li])
				}
				continue
			}
			for li := 0; li < len(lines); {
				if lines[li].Kind == diff.Context {
					rows = append(rows, row{kind: rowLine, file: idx, hunk: h, a: int32(li), b: int32(li)})
					rows = w.appendComments(rows, idx, f, &lines[li])
					li++
					continue
				}
				start := li
				for li < len(lines) && lines[li].Kind == diff.Del {
					li++
				}
				dels := li - start
				addStart := li
				for li < len(lines) && lines[li].Kind == diff.Add {
					li++
				}
				adds := li - addStart
				for k := range max(dels, adds) {
					a, b := int32(-1), int32(-1)
					if k < dels {
						a = int32(start + k)
					}
					if k < adds {
						b = int32(addStart + k)
					}
					rows = append(rows, row{kind: rowLine, file: idx, hunk: h, a: a, b: b})
					if a >= 0 {
						rows = w.appendComments(rows, idx, f, &lines[a])
					}
					if b >= 0 {
						rows = w.appendComments(rows, idx, f, &lines[b])
					}
				}
			}
		}
		if len(f.Hunks) > 0 {
			addGap(f.gapBefore(len(f.Hunks)))
		}
		rows = append(rows, row{kind: rowEnd, file: idx})
	}
	w.rows = rows
	w.rowsDirty = false
}

// appendContext adds a line of context the user expanded.
func (w *window) appendContext(rows []row, idx int32, f *fileState, old, new int32) []row {
	rows = append(rows, row{kind: rowLine, file: idx, hunk: -1, a: -1, b: -1, old: old, new: new})
	line := diff.Line{Kind: diff.Context, Old: int(old), New: int(new)}
	return w.appendComments(rows, idx, f, &line)
}

// appendComments adds the comments on a line.
func (w *window) appendComments(rows []row, idx int32, f *fileState, l *diff.Line) []row {
	for _, cm := range w.comments {
		if cm.path != f.Path {
			continue
		}
		if (cm.side == sideOld && l.Kind == diff.Del && cm.line == l.Old) ||
			(cm.side == sideNew && l.Kind != diff.Del && cm.line == l.New) {
			rows = append(rows, row{kind: rowComment, file: idx, comment: cm})
		}
	}
	return rows
}

// key returns the identity of a row.
func (w *window) key(r *row) rowKey {
	f := w.files[r.file]
	k := rowKey{kind: r.kind, file: f.Path, gap: r.gap, comment: r.comment}
	if r.kind == rowLine {
		if r.hunk < 0 {
			k.old, k.new = r.old, r.new
		} else {
			if l := f.lineAt(r.hunk, r.a); l != nil {
				k.old, k.new = int32(l.Old), int32(l.New)
			}
			if l := f.lineAt(r.hunk, r.b); l != nil {
				if k.old == 0 {
					k.old = int32(l.Old)
				}
				k.new = int32(l.New)
			}
			if k.old == 0 && k.new == 0 {
				k.gap = r.hunk<<16 | r.a
			}
		}
	}
	return k
}
