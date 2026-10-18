package main

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
	"weak"

	"github.com/egoist/godiff/internal/diff"
	"github.com/egoist/mygo/ui"
)

func TestDiffSpanCacheFollowsViewport(t *testing.T) {
	dir := testRepo(t)
	var code strings.Builder
	for i := range 3000 {
		fmt.Fprintf(&code, "var value%d = fmt.Sprintf(\"item %%d\", %d) // unique value\n", i, i)
	}
	writeFile(t, dir, "long.go", code.String())
	w, tt := newTestWindow(t, dir)
	var f *fileState
	for i, file := range w.files {
		if file.Path == "long.go" {
			f = file
			w.revealFile(i)
			break
		}
	}
	if f == nil {
		t.Fatal("long.go was not loaded")
	}
	tt.Frame()
	first := lineSide{present: true, kind: diff.Add, hunk: 0, index: 0, num: 1, text: f.Hunks[0].Lines[0].Text, segs: f.newHL[0]}
	spans := w.lineSpans(f, first, sideNew, &lightPalette, lightPalette.addWord)
	old := weak.Make(&spans[0])
	tt.Frame()
	if got := w.lineSpans(f, first, sideNew, &lightPalette, lightPalette.addWord); &got[0] != &spans[0] {
		t.Fatal("a new frame rebuilt the styled code of an unchanged visible line")
	}
	spans = nil
	for i := 0; i < len(w.rows); i += 60 {
		w.list.ScrollTo(i, ui.Start)
		tt.Frame()
		entries := 0
		for _, file := range w.files {
			entries += len(file.spans) + len(file.previousSpans)
		}
		if entries > 256 {
			t.Fatalf("scrolling to row %d retained %d styled lines", i, entries)
		}
	}
	tt.Frame()
	runtime.GC()
	if old.Value() != nil {
		t.Fatal("scrolling offscreen retained the first line's styled code")
	}
	runtime.KeepAlive(w)
	runtime.KeepAlive(tt)
}

func TestLoadedContentsInvalidateSpanCaches(t *testing.T) {
	f := &fileState{File: &diff.File{Path: "file.txt"}}
	w := &window{}
	s := lineSide{hunk: 0, index: 0, num: 1, text: "before"}
	w.lineSpans(f, s, sideNew, &lightPalette, ui.Transparent)
	f.previousSpans, f.spans = f.spans, nil
	s.num = 2
	w.lineSpans(f, s, sideNew, &lightPalette, ui.Transparent)
	l := loaded{file: f, newLines: []string{"after"}}
	l.apply()
	s.num, s.text = 1, "after"
	if got := w.lineSpans(f, s, sideNew, &lightPalette, ui.Transparent); len(got) != 1 || got[0].Text != "after" {
		t.Fatalf("reloaded contents show stale spans: %+v", got)
	}
}
