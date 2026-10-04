package main

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// clickPlus clicks the button commenting on the line whose code starts at
// x, y: it shows while the pointer is over the line, left of the code.
func clickPlus(tt *ui.Tester, x, y float32) {
	tt.Move(x+30, y)
	tt.Frame()
	tt.ClickAt(x-14, y)
	tt.Frame()
}

func countRows(w *window, k rowKind) int {
	n := 0
	for _, r := range w.rows {
		if r.kind == k {
			n++
		}
	}
	return n
}

func TestPlusOnUnchangedLines(t *testing.T) {
	for _, layout := range []string{"split", "unified"} {
		w, tt := newTestWindow(t, testRepo(t))
		w.settings.DiffStyle = layout
		w.rowsDirty = true
		tt.Frame()
		box, ok := tt.Find(`import "fmt"`)
		if !ok {
			t.Fatal("no unchanged line")
		}
		clickPlus(tt, box.X, box.Y+box.H/2)
		if len(w.comments) != 1 || countRows(w, rowComment) != 1 {
			t.Errorf("%s, first side: %d comments, %d shown", layout, len(w.comments), countRows(w, rowComment))
		}
		if layout != "split" {
			continue
		}
		// The same line on the other side: the first, still empty, moves
		// there.
		right := box.X + float32(w.diffListEl.Bounds().W)/2
		clickPlus(tt, right, box.Y+box.H/2)
		if len(w.comments) != 1 || w.comments[0].side != sideNew || countRows(w, rowComment) != 1 {
			t.Errorf("other side: %d comments, %d shown", len(w.comments), countRows(w, rowComment))
		}
	}
}

func TestCommentOnExpandedLine(t *testing.T) {
	w, _ := newTestWindow(t, testRepo(t))
	var long *fileState
	for _, f := range w.files {
		if f.Path == "docs/long.txt" {
			long = f
		}
	}
	// Line 30 is in the gap after the hunk, on both sides.
	w.addComment("docs/long.txt", sideOld, 30, 30)
	w.comments[0].text = "Why?"
	md := w.commentsMarkdown()
	if !strings.Contains(md, "(Old line 30)") || !strings.Contains(md, "   @@ -27,7 +27,7 @@") || !strings.Contains(md, "    30/30 | "+long.oldLines[29]) {
		t.Errorf("markdown:\n%s", md)
	}
}

func TestSwitchSourceOnFilesTab(t *testing.T) {
	w, tt := newTestWindow(t, testRepo(t))
	w.tab = 0
	tt.Frame()
	hash, err := w.repo.Resolve("HEAD")
	if err != nil {
		t.Fatal(err)
	}
	// Frames run while the commit's files load: the tree must not show
	// the files of the work tree, gone.
	w.hold = true
	w.setSource(source{kind: sourceCommit, ref: hash})
	tt.Frame()
	// The commit shows at once, its message from the history, before its
	// changes load.
	if w.source.kind != sourceCommit || tt.HasText("new.go") || !tt.HasText("First commit") {
		t.Errorf("source %+v, texts %q", w.source, tt.Texts())
	}
	w.hold = false
	for len(w.held) > 0 {
		fn := w.held[0]
		w.held = w.held[1:]
		fn()
	}
	tt.Frame()
	if len(w.files) != 3 || !tt.HasText("main.go") || tt.HasText("new.go") {
		t.Errorf("files %d", len(w.files))
	}
	// Its first files came colored, with their contents.
	for _, f := range w.files {
		if !f.loaded {
			t.Errorf("%s not loaded", f.Path)
		}
	}
}
