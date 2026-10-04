package main

import (
	"slices"
	"strings"

	"github.com/egoist/godiff/internal/diff"
	"github.com/egoist/godiff/internal/highlight"
	"github.com/egoist/mygo/ui"
)

// tabWidth is the columns a tab takes.
const tabWidth = 4

// mark is a range of a line drawn with a background: a changed word or a
// match of the find bar.
type mark struct {
	diff.Range
	color ui.Color
}

// codeSpans styles a line of code: the colors of its tokens, and the
// backgrounds of its marks, later marks over earlier ones. Tabs become
// spaces up to the next tab stop.
func codeSpans(text string, segs []highlight.Seg, marks []mark, pal *palette) []ui.Span {
	if text == "" {
		return []ui.Span{{Text: " "}}
	}
	// The places where the style may change.
	cuts := []int{0, len(text)}
	for _, s := range segs {
		cuts = append(cuts, int(s.Start), int(s.End))
	}
	for _, m := range marks {
		cuts = append(cuts, m.Start, m.End)
	}
	slices.Sort(cuts)
	cuts = slices.Compact(cuts)

	var spans []ui.Span
	col := 0
	si := 0
	for i := 0; i+1 < len(cuts); i++ {
		start, end := cuts[i], cuts[i+1]
		if start >= len(text) || start < 0 {
			break
		}
		end = min(end, len(text))
		if end <= start {
			continue
		}
		for si < len(segs) && int(segs[si].End) <= start {
			si++
		}
		color := pal.code
		if si < len(segs) && int(segs[si].Start) <= start {
			color = pal.syntax[segs[si].Class]
		}
		var bg ui.Color
		for _, m := range marks {
			if m.Start <= start && end <= m.End {
				bg = m.color
			}
		}
		part := text[start:end]
		if strings.IndexByte(part, '\t') >= 0 {
			part, col = expandTabs(part, col)
		} else {
			col += len(part)
		}
		if n := len(spans); n > 0 && spans[n-1].Color == color && spans[n-1].Background == bg {
			spans[n-1].Text += part
			continue
		}
		spans = append(spans, ui.Span{Text: part, Color: color, Background: bg})
	}
	return spans
}

// expandTabs replaces the tabs of s, which starts at column col, and
// returns the column after it.
func expandTabs(s string, col int) (string, int) {
	var b strings.Builder
	for _, r := range s {
		if r == '\t' {
			n := tabWidth - col%tabWidth
			b.WriteString(strings.Repeat(" ", n))
			col += n
			continue
		}
		b.WriteRune(r)
		col++
	}
	return b.String(), col
}

// columns returns how many columns a line takes, tabs expanded.
func columns(s string) int {
	col := 0
	for _, r := range s {
		if r == '\t' {
			col += tabWidth - col%tabWidth
		} else {
			col++
		}
	}
	return col
}
