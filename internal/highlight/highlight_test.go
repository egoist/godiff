package highlight

import "testing"

func TestLines(t *testing.T) {
	lines := []string{"package main", "", "// hi", `func main() { println("x", 42) }`}
	segs := Lines("main.go", lines)
	if len(segs) != len(lines) {
		t.Fatalf("got %d lines", len(segs))
	}
	has := func(row int, text string, class Class) bool {
		for _, s := range segs[row] {
			if lines[row][s.Start:s.End] == text && s.Class == class {
				return true
			}
		}
		return false
	}
	if !has(0, "package", Keyword) {
		t.Errorf("line 0: %+v", segs[0])
	}
	if !has(2, "// hi", Comment) {
		t.Errorf("line 2: %+v", segs[2])
	}
	if !has(3, `"x"`, String) || !has(3, "42", Number) {
		t.Errorf("line 3: %+v", segs[3])
	}
	if Lines("notes.unknownext", lines) != nil {
		t.Error("plain text highlighted")
	}
}
