package main

import (
	"strings"

	"github.com/egoist/godiff/internal/diff"
	"github.com/egoist/mygo/ui"
)

// match is a line holding the query of the find bar, or a file whose path
// holds it (line -1).
type match struct {
	file int
	hunk int32
	line int32
}

// searching reports whether the find bar is open with a query.
func (w *window) searching() bool {
	return w.finding && strings.TrimSpace(w.query) != ""
}

// findRanges returns where query is in text, ignoring case.
func findRanges(text, query string) []diff.Range {
	query = strings.TrimSpace(query)
	if query == "" || len(query) > len(text) {
		return nil
	}
	lower, q := strings.ToLower(text), strings.ToLower(query)
	if len(lower) != len(text) {
		// Lowercasing changed the length: match the text as it is.
		lower, q = text, query
	}
	var out []diff.Range
	for start := 0; ; {
		i := strings.Index(lower[start:], q)
		if i < 0 {
			return out
		}
		out = append(out, diff.Range{Start: start + i, End: start + i + len(q)})
		start += i + len(q)
	}
}

// updateMatches finds the query in the files' paths and lines.
func (w *window) updateMatches() {
	key := w.query
	if !w.finding {
		key = ""
	}
	if key == w.matchesFor {
		return
	}
	w.matchesFor = key
	w.matches = w.matches[:0]
	w.fileMatches = map[int]bool{}
	q := strings.ToLower(strings.TrimSpace(key))
	if q == "" {
		w.match = 0
		return
	}
	filter := strings.TrimSpace(w.filter)
	for fi, f := range w.files {
		if !w.hasContent(f) || !fuzzyMatch(f.Path, filter) {
			continue
		}
		found := false
		if strings.Contains(strings.ToLower(f.Path), q) || strings.Contains(strings.ToLower(f.OldPath), q) {
			w.matches = append(w.matches, match{file: fi, hunk: -1, line: -1})
			found = true
		}
		for hi, h := range f.Hunks {
			for li, l := range h.Lines {
				if strings.Contains(strings.ToLower(l.Text), q) {
					w.matches = append(w.matches, match{file: fi, hunk: int32(hi), line: int32(li)})
					found = true
				}
			}
		}
		if !found && strings.Contains(strings.ToLower(w.fileNote(f)), q) {
			w.matches = append(w.matches, match{file: fi, hunk: -1, line: -1})
			found = true
		}
		if found {
			w.fileMatches[fi] = true
		}
	}
	if w.match >= len(w.matches) {
		w.match = 0
	}
	w.buildTree()
	w.rowsDirty = true
}

// activeMatch reports whether a line is the current match.
func (w *window) activeMatch(f *fileState, s lineSide) bool {
	if w.match < 0 || w.match >= len(w.matches) || s.index < 0 {
		return false
	}
	m := w.matches[w.match]
	return w.files[m.file] == f && m.hunk == s.hunk && m.line == s.index
}

// showMatch scrolls the current match into the middle of the surface.
func (w *window) showMatch() {
	if w.match < 0 || w.match >= len(w.matches) {
		return
	}
	m := w.matches[w.match]
	if w.rowsDirty {
		w.buildRows()
	}
	for i := range w.rows {
		r := &w.rows[i]
		if int(r.file) != m.file {
			continue
		}
		if m.hunk < 0 && r.kind == rowHeader {
			w.list.ScrollTo(i, ui.Start)
			break
		}
		if r.kind == rowLine && r.hunk == m.hunk && (r.a == m.line || r.b == m.line) {
			w.list.ScrollTo(i, ui.Center)
			break
		}
	}
	w.current = m.file
	w.selectTreeFile(m.file)
}

// anchor is where j and k stop: a hunk of a file shown open.
type anchor struct {
	file int
	hunk int
	row  int
}

// anchors lists the hunks in the order they show.
func (w *window) anchors() []anchor {
	if w.rowsDirty {
		w.buildRows()
	}
	var out []anchor
	seen := map[[2]int]bool{}
	for i := range w.rows {
		r := &w.rows[i]
		switch r.kind {
		case rowHeader:
			f := w.files[r.file]
			if f.collapsed && !w.forceOpen(int(r.file)) {
				out = append(out, anchor{file: int(r.file), hunk: -1, row: i})
			}
		case rowLine:
			if r.hunk < 0 {
				continue
			}
			k := [2]int{int(r.file), int(r.hunk)}
			if seen[k] {
				continue
			}
			f := w.files[r.file]
			l1, l2 := f.lineAt(r.hunk, r.a), f.lineAt(r.hunk, r.b)
			if (l1 != nil && l1.Kind != diff.Context) || (l2 != nil && l2.Kind != diff.Context) {
				seen[k] = true
				out = append(out, anchor{file: int(r.file), hunk: int(r.hunk), row: i})
			}
		}
	}
	return out
}

// nextHunk moves the choice to the next (dir 1) or the previous (dir -1)
// hunk, and selects its changed lines.
func (w *window) nextHunk(dir int) {
	anchors := w.anchors()
	if len(anchors) == 0 {
		return
	}
	at := -1
	for i, a := range anchors {
		if a.file == w.selFile && a.hunk == w.selHunk {
			at = i
		}
	}
	if at >= 0 {
		at = min(max(at+dir, 0), len(anchors)-1)
	} else {
		first, _ := w.list.Visible()
		at = 0
		if dir > 0 {
			for i, a := range anchors {
				if a.row > first {
					at = i
					break
				}
			}
		} else {
			at = 0
			for i, a := range anchors {
				if a.row < first {
					at = i
				}
			}
		}
	}
	a := anchors[at]
	w.selFile, w.selHunk = a.file, a.hunk
	if a.hunk < 0 {
		w.list.ScrollTo(a.row, ui.Start)
	} else {
		w.list.ScrollTo(a.row, ui.Center)
	}
	w.current = a.file
	w.revealedAt = w.now
	w.selectTreeFile(a.file)
}

// isSelectedLine reports whether a line is a changed line of the hunk
// chosen with j and k.
func (w *window) isSelectedLine(f *fileState, s lineSide) bool {
	if s.hunk < 0 || int(s.hunk) != w.selHunk || s.kind == diff.Context || w.selFile < 0 || w.selFile >= len(w.files) {
		return false
	}
	return w.files[w.selFile] == f
}

// commentOnSelection starts a comment on the changed lines of the hunk
// chosen with j and k.
func (w *window) commentOnSelection() bool {
	if w.selFile < 0 || w.selFile >= len(w.files) || w.selHunk < 0 {
		return false
	}
	f := w.files[w.selFile]
	if w.selHunk >= len(f.Hunks) {
		return false
	}
	h := f.Hunks[w.selHunk]
	start, end, sd := 0, 0, sideNew
	for _, l := range h.Lines {
		if l.Kind == diff.Add {
			if start == 0 {
				start = l.New
			}
			end = l.New
		}
	}
	if start == 0 {
		sd = sideOld
		for _, l := range h.Lines {
			if l.Kind == diff.Del {
				if start == 0 {
					start = l.Old
				}
				end = l.Old
			}
		}
	}
	if start == 0 {
		return false
	}
	w.addComment(f.Path, sd, start, end)
	return true
}
