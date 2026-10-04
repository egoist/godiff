package main

import (
	"fmt"
	"slices"
	"strings"

	"github.com/egoist/godiff/internal/diff"
)

// side is the side of a diff a comment is on.
type side uint8

const (
	sideNew side = iota
	sideOld
)

// comment is a review comment on a line, or a range of lines of one side,
// anchored on its last line.
type comment struct {
	id         int
	path       string
	side       side
	start      int // the first line of the range
	line       int // the last line, where it shows
	text       string
	focus      bool // takes the focus as it is built
	wasFocused bool
}

func (c *comment) pending() bool { return strings.TrimSpace(c.text) != "" }

// label is "New line 12", "Old lines 3-5".
func (c *comment) label() string {
	s := "New"
	if c.side == sideOld {
		s = "Old"
	}
	if c.start != 0 && c.start != c.line {
		return fmt.Sprintf("%s lines %d-%d", s, min(c.start, c.line), max(c.start, c.line))
	}
	return fmt.Sprintf("%s line %d", s, c.line)
}

var commentIDs int

// addComment starts a comment on lines start…line of a side of a file,
// or focuses the empty draft already there.
func (w *window) addComment(path string, sd side, start, line int) {
	for _, c := range w.comments {
		if c.path == path && c.side == sd && c.line == line && !c.pending() {
			c.focus = true
			return
		}
	}
	// An empty draft elsewhere moves here rather than stay behind.
	w.comments = slices.DeleteFunc(w.comments, func(c *comment) bool { return !c.pending() })
	commentIDs++
	w.comments = append(w.comments, &comment{id: commentIDs, path: path, side: sd, start: start, line: line, focus: true})
	w.rowsDirty = true
}

func (w *window) deleteComment(c *comment) {
	w.comments = slices.DeleteFunc(w.comments, func(o *comment) bool { return o == c })
	w.rowsDirty = true
}

// pendingComments counts the comments with text.
func (w *window) pendingComments() int {
	n := 0
	for _, c := range w.comments {
		if c.pending() {
			n++
		}
	}
	return n
}

// pruneComments drops the empty drafts of files gone from the review.
func (w *window) pruneComments() {
	paths := map[string]bool{}
	for _, f := range w.files {
		paths[f.Path] = true
	}
	w.comments = slices.DeleteFunc(w.comments, func(c *comment) bool { return !paths[c.path] && !c.pending() })
}

// commentsMarkdown writes the pending comments as Markdown, each with the
// lines around it, for an agent or a colleague to address.
func (w *window) commentsMarkdown() string {
	index := map[string]int{}
	byPath := map[string]*fileState{}
	for i, f := range w.files {
		index[f.Path] = i
		byPath[f.Path] = f
	}
	var list []*comment
	for _, c := range w.comments {
		if c.pending() {
			list = append(list, c)
		}
	}
	if len(list) == 0 {
		return ""
	}
	slices.SortStableFunc(list, func(a, b *comment) int {
		ia, ok := index[a.path]
		if !ok {
			ia = len(w.files)
		}
		ib, ok := index[b.path]
		if !ok {
			ib = len(w.files)
		}
		if ia != ib {
			return ia - ib
		}
		if a.line != b.line {
			return a.line - b.line
		}
		return a.id - b.id
	})
	var items []string
	for i, c := range list {
		context := "No patch context available."
		if f := byPath[c.path]; f != nil {
			if ctx := commentContext(f, c); ctx != "" {
				context = ctx
			} else if f.Note != "" {
				context = f.Note
			}
		}
		fence := "```"
		for strings.Contains(context, fence) {
			fence += "`"
		}
		item := fmt.Sprintf("%d. **%s** (%s)\n\n%s\n\n%s", i+1, c.path, c.label(),
			indent3(fence+"diff\n"+context+"\n"+fence), indent3(strings.TrimSpace(c.text)))
		items = append(items, item)
	}
	out := strings.Join(items, "\n\n")
	if prefix := w.settings.ReviewCommentsPrefix; prefix != "" {
		out = prefix + "\n\n" + out
	}
	return out
}

func indent3(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = "   " + l
	}
	return strings.Join(lines, "\n")
}

// commentContext returns the hunk header and the lines around a comment,
// three on either side, as a diff.
func commentContext(f *fileState, c *comment) string {
	type ctxRow struct {
		prefix   byte
		old, new int
		text     string
	}
	for _, h := range f.Hunks {
		// Deletions before additions within each change, as diffs read.
		var rows []ctxRow
		for i := 0; i < len(h.Lines); {
			l := h.Lines[i]
			if l.Kind == diff.Context {
				rows = append(rows, ctxRow{' ', l.Old, l.New, l.Text})
				i++
				continue
			}
			j := i
			for j < len(h.Lines) && h.Lines[j].Kind != diff.Context {
				j++
			}
			for _, l := range h.Lines[i:j] {
				if l.Kind == diff.Del {
					rows = append(rows, ctxRow{'-', l.Old, 0, l.Text})
				}
			}
			for _, l := range h.Lines[i:j] {
				if l.Kind == diff.Add {
					rows = append(rows, ctxRow{'+', 0, l.New, l.Text})
				}
			}
			i = j
		}
		find := func(n int) int {
			for i, r := range rows {
				if c.side == sideOld && r.prefix != '+' && r.old == n {
					return i
				}
				if c.side == sideNew && r.prefix != '-' && r.new == n {
					return i
				}
			}
			return -1
		}
		end := find(c.line)
		if end < 0 {
			continue
		}
		start := end
		if c.start != 0 {
			if s := find(c.start); s >= 0 {
				start = s
			}
		}
		lo := max(min(start, end)-3, 0)
		hi := min(max(start, end)+4, len(rows))
		var b strings.Builder
		fmt.Fprintf(&b, "@@ -%d,%d +%d,%d @@", h.OldStart, h.OldLines, h.NewStart, h.NewLines)
		for _, r := range rows[lo:hi] {
			var num string
			switch r.prefix {
			case '+':
				num = fmt.Sprint(r.new)
			case '-':
				num = fmt.Sprint(r.old)
			default:
				num = fmt.Sprintf("%d/%d", r.old, r.new)
			}
			fmt.Fprintf(&b, "\n%c%4s | %s", r.prefix, num, strings.TrimRight(r.text, "\r\n"))
		}
		return b.String()
	}
	return gapContext(f, c)
}

// gapContext returns the lines around a comment on an unchanged line
// outside the hunks, one the user expanded, from the file's contents.
func gapContext(f *fileState, c *comment) string {
	if !f.canExpand() {
		return ""
	}
	for i := 0; i <= len(f.Hunks); i++ {
		g := f.gapBefore(i)
		start := g.newStart
		if c.side == sideOld {
			start = g.oldStart
		}
		k := c.line - start
		if k < 0 || k >= g.count {
			continue
		}
		lo, hi := max(k-3, 0), min(k+4, g.count)
		var b strings.Builder
		fmt.Fprintf(&b, "@@ -%d,%d +%d,%d @@", g.oldStart+lo, hi-lo, g.newStart+lo, hi-lo)
		for j := lo; j < hi; j++ {
			old, new := g.oldStart+j, g.newStart+j
			fmt.Fprintf(&b, "\n %4s | %s", fmt.Sprintf("%d/%d", old, new), f.contextText(int32(old), int32(new)))
		}
		return b.String()
	}
	return ""
}
