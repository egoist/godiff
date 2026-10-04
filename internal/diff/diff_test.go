package diff

import (
	"testing"
)

const samplePatch = `diff --git a/main.go b/main.go
index 83db48f..bf2a3c1 100644
--- a/main.go
+++ b/main.go
@@ -1,5 +1,6 @@ package main
 package main

-import "fmt"
+import (
+	"fmt"
+)

 func main() {
@@ -10,3 +11,3 @@ func main() {
 	a := 1
-	fmt.Println(a)
+	fmt.Println(a + 1)
 }
\ No newline at end of file
diff --git a/old name.txt b/new name.txt
similarity index 90%
rename from old name.txt
rename to new name.txt
index 1111111..2222222 100644
--- a/old name.txt
+++ b/new name.txt
@@ -1 +1 @@
-hello
+hello world
diff --git a/img.png b/img.png
new file mode 100644
index 0000000..3333333
Binary files /dev/null and b/img.png differ
diff --git a/gone.txt b/gone.txt
deleted file mode 100644
index 4444444..0000000
--- a/gone.txt
+++ /dev/null
@@ -1,2 +0,0 @@
-a
-b
`

func TestParse(t *testing.T) {
	files := Parse([]byte(samplePatch))
	if len(files) != 4 {
		t.Fatalf("got %d files", len(files))
	}
	m := files[0]
	if m.Path != "main.go" || m.Status != Modified || len(m.Hunks) != 2 {
		t.Fatalf("main.go: %+v", m)
	}
	if m.Additions != 4 || m.Deletions != 2 {
		t.Errorf("main.go counts +%d -%d", m.Additions, m.Deletions)
	}
	h := m.Hunks[1]
	if h.Lines[0].Old != 10 || h.Lines[0].New != 11 || !h.Lines[3].NoNewline {
		t.Errorf("second hunk lines: %+v", h.Lines)
	}
	if h.Section != "func main() {" {
		t.Errorf("section %q", h.Section)
	}
	r := files[1]
	if r.Status != Renamed || r.OldPath != "old name.txt" || r.Path != "new name.txt" {
		t.Errorf("rename: %+v", r)
	}
	b := files[2]
	if !b.Binary || b.Status != Added || b.Path != "img.png" {
		t.Errorf("binary: %+v", b)
	}
	d := files[3]
	if d.Status != Deleted || d.Path != "gone.txt" || d.Deletions != 2 {
		t.Errorf("deleted: %+v", d)
	}
}

func TestWordDiff(t *testing.T) {
	ra, rb := WordDiff(`	fmt.Println(a)`, `	fmt.Println(a + 1)`)
	if len(ra) != 0 || len(rb) != 1 {
		t.Fatalf("ranges %v %v", ra, rb)
	}
	if got := `	fmt.Println(a + 1)`[rb[0].Start:rb[0].End]; got != " + 1" {
		t.Errorf("changed %q", got)
	}
	if ra, rb := WordDiff("completely different", "nothing alike here at all"); ra != nil || rb != nil {
		t.Errorf("rewritten lines marked: %v %v", ra, rb)
	}
}

func TestPairs(t *testing.T) {
	lines := []Line{{Kind: Context}, {Kind: Del}, {Kind: Del}, {Kind: Add}, {Kind: Context}, {Kind: Add}}
	p := Pairs(lines)
	if len(p) != 1 || p[0] != (Pair{1, 3}) {
		t.Errorf("pairs %v", p)
	}
}
