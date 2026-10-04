// Package diff holds the model of a set of changed files and parses the
// patches git prints into it.
package diff

import (
	"bufio"
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"strconv"
	"strings"
)

// Status is how a file changed, as git's status letters.
type Status byte

const (
	Modified  Status = 'M'
	Added     Status = 'A'
	Deleted   Status = 'D'
	Renamed   Status = 'R'
	Copied    Status = 'C'
	TypeChg   Status = 'T'
	Untracked Status = '?'
	Conflict  Status = 'U'
)

// Label is the status as a word, as file headers show it.
func (s Status) Label() string {
	switch s {
	case Added:
		return "Added"
	case Deleted:
		return "Deleted"
	case Renamed:
		return "Renamed"
	case Copied:
		return "Copied"
	case TypeChg:
		return "Type changed"
	case Untracked:
		return "Untracked"
	case Conflict:
		return "Conflict"
	}
	return "Modified"
}

// LineKind is what a line of a hunk is.
type LineKind uint8

const (
	Context LineKind = iota
	Add
	Del
)

// Line is a line of a hunk. Old and New are its numbers in the old and the
// new file, 0 on the side it is not in.
type Line struct {
	Kind      LineKind
	Old, New  int
	Text      string
	NoNewline bool
}

// Hunk is a run of changed lines with the context around them.
type Hunk struct {
	OldStart, OldLines int
	NewStart, NewLines int
	// Section is the function or heading git shows after the @@.
	Section string
	Lines   []Line
}

// File is a changed file.
type File struct {
	Path    string
	OldPath string // the path before a rename or a copy, else Path
	Status  Status
	Binary  bool
	// Mode change, as "100644 → 100755", if any.
	ModeChange string
	Hunks      []Hunk
	Additions  int
	Deletions  int
	// TooLarge is set when the patch was dropped for its size.
	TooLarge bool
	// Directory is an untracked directory shown collapsed, as
	// node_modules, whose files are not listed.
	Directory bool
	// Generated is a file a tool wrote, as a lockfile: it starts
	// collapsed.
	Generated bool
	// Note explains why the lines are not shown, if they are not.
	Note string
	// Fingerprint identifies the content of the change: viewed files are
	// remembered by it, and show again once it changes.
	Fingerprint string
}

// Name is the base name of the file.
func (f *File) Name() string {
	if i := strings.LastIndexByte(f.Path, '/'); i >= 0 {
		return f.Path[i+1:]
	}
	return f.Path
}

// Dir is the directory of the file, with a trailing slash, or "".
func (f *File) Dir() string {
	if i := strings.LastIndexByte(f.Path, '/'); i >= 0 {
		return f.Path[:i+1]
	}
	return ""
}

// Count sets Additions and Deletions from the hunks.
func (f *File) Count() {
	f.Additions, f.Deletions = 0, 0
	for _, h := range f.Hunks {
		for _, l := range h.Lines {
			switch l.Kind {
			case Add:
				f.Additions++
			case Del:
				f.Deletions++
			}
		}
	}
}

// fingerprint hashes what identifies the change.
func fingerprint(parts ...string) string {
	h := sha1.New()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// Parse parses the output of git diff (or git show) with patches.
func Parse(patch []byte) []*File {
	var (
		files []*File
		cur   *File
		hunk  *Hunk
		raw   bytes.Buffer // the current file's patch, for its fingerprint
		oldN  int
		newN  int
		// The lines of the hunk still to come, on each side.
		remOld, remNew int
	)
	finish := func() {
		if cur == nil {
			return
		}
		if hunk != nil {
			cur.Hunks = append(cur.Hunks, *hunk)
			hunk = nil
		}
		cur.Count()
		cur.Fingerprint = fingerprint(cur.Path, raw.String())
		files = append(files, cur)
		cur = nil
		raw.Reset()
	}
	sc := bufio.NewScanner(bytes.NewReader(patch))
	sc.Buffer(make([]byte, 64*1024), 64*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "diff --git ") || strings.HasPrefix(line, "diff --cc ") || strings.HasPrefix(line, "diff --combined ") {
			finish()
			cur = &File{Status: Modified}
			cur.OldPath, cur.Path = splitGitHeader(line)
			raw.WriteString(line)
			raw.WriteByte('\n')
			continue
		}
		if cur == nil {
			continue
		}
		raw.WriteString(line)
		raw.WriteByte('\n')
		// Lines of files with CRLF endings show without the CR.
		line = strings.TrimSuffix(line, "\r")
		if hunk != nil && (remOld > 0 || remNew > 0) {
			switch {
			case strings.HasPrefix(line, "+"):
				newN++
				remNew--
				hunk.Lines = append(hunk.Lines, Line{Kind: Add, New: newN, Text: line[1:]})
				continue
			case strings.HasPrefix(line, "-"):
				oldN++
				remOld--
				hunk.Lines = append(hunk.Lines, Line{Kind: Del, Old: oldN, Text: line[1:]})
				continue
			case strings.HasPrefix(line, " ") || line == "":
				oldN++
				newN++
				remOld--
				remNew--
				text := line
				if text != "" {
					text = text[1:]
				}
				hunk.Lines = append(hunk.Lines, Line{Kind: Context, Old: oldN, New: newN, Text: text})
				continue
			}
		}
		if strings.HasPrefix(line, `\`) {
			// "\ No newline at end of file" after the line it is about.
			if hunk != nil {
				if n := len(hunk.Lines); n > 0 {
					hunk.Lines[n-1].NoNewline = true
				}
			}
			continue
		}
		switch {
		case strings.HasPrefix(line, "@@"):
			if hunk != nil {
				cur.Hunks = append(cur.Hunks, *hunk)
			}
			hunk = parseHunkHeader(line)
			if hunk == nil {
				continue
			}
			oldN, newN = hunk.OldStart-1, hunk.NewStart-1
			if hunk.OldLines == 0 {
				oldN = hunk.OldStart
			}
			if hunk.NewLines == 0 {
				newN = hunk.NewStart
			}
			remOld, remNew = hunk.OldLines, hunk.NewLines
		case strings.HasPrefix(line, "new file mode"):
			cur.Status = Added
		case strings.HasPrefix(line, "deleted file mode"):
			cur.Status = Deleted
		case strings.HasPrefix(line, "old mode "):
			cur.ModeChange = strings.TrimPrefix(line, "old mode ")
		case strings.HasPrefix(line, "new mode "):
			cur.ModeChange += " → " + strings.TrimPrefix(line, "new mode ")
		case strings.HasPrefix(line, "rename from "):
			cur.Status = Renamed
			cur.OldPath = unquote(strings.TrimPrefix(line, "rename from "))
		case strings.HasPrefix(line, "rename to "):
			cur.Status = Renamed
			cur.Path = unquote(strings.TrimPrefix(line, "rename to "))
		case strings.HasPrefix(line, "copy from "):
			cur.Status = Copied
			cur.OldPath = unquote(strings.TrimPrefix(line, "copy from "))
		case strings.HasPrefix(line, "copy to "):
			cur.Status = Copied
			cur.Path = unquote(strings.TrimPrefix(line, "copy to "))
		case strings.HasPrefix(line, "--- "):
			if p := stripPrefix(unquote(strings.TrimPrefix(line, "--- "))); p != "" {
				cur.OldPath = p
			}
		case strings.HasPrefix(line, "+++ "):
			if p := stripPrefix(unquote(strings.TrimPrefix(line, "+++ "))); p != "" {
				cur.Path = p
			}
		case strings.HasPrefix(line, "Binary files ") || strings.HasPrefix(line, "GIT binary patch"):
			cur.Binary = true
		}
	}
	finish()
	for _, f := range files {
		if f.Path == "" {
			f.Path = f.OldPath
		}
		if f.OldPath == "" {
			f.OldPath = f.Path
		}
	}
	return files
}

// stripPrefix takes the a/ or b/ off a path of ---/+++ lines, "" for
// /dev/null.
func stripPrefix(p string) string {
	p = strings.TrimSuffix(p, "\t")
	if p == "/dev/null" {
		return ""
	}
	if len(p) > 2 && (p[0] == 'a' || p[0] == 'b' || p[0] == 'i' || p[0] == 'w' || p[0] == 'c' || p[0] == 'o') && p[1] == '/' {
		return p[2:]
	}
	return p
}

// splitGitHeader returns the paths of "diff --git a/x b/y".
func splitGitHeader(line string) (string, string) {
	rest := line
	for _, p := range []string{"diff --git ", "diff --cc ", "diff --combined "} {
		rest = strings.TrimPrefix(rest, p)
	}
	if strings.HasPrefix(rest, `"`) {
		// Quoted: "a/x" "b/y", or "a/x" b/y.
		end := closingQuote(rest)
		if end > 0 {
			a := unquote(rest[:end+1])
			b := unquote(strings.TrimSpace(rest[end+1:]))
			return stripPrefix(a), stripPrefix(b)
		}
	}
	if !strings.Contains(rest, " ") {
		// diff --cc path
		return rest, rest
	}
	// a/x b/x: both halves are the same length when the path is unchanged.
	if n := len(rest); n%2 == 1 {
		a, b := rest[:n/2], rest[n/2+1:]
		if stripPrefix(a) == stripPrefix(b) {
			return stripPrefix(a), stripPrefix(b)
		}
	}
	if i := strings.Index(rest, " b/"); i >= 0 {
		return stripPrefix(rest[:i]), stripPrefix(rest[i+1:])
	}
	return rest, rest
}

func closingQuote(s string) int {
	for i := 1; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case '"':
			return i
		}
	}
	return -1
}

// unquote undoes git's C-style quoting of paths.
func unquote(s string) string {
	if len(s) < 2 || s[0] != '"' || s[len(s)-1] != '"' {
		return s
	}
	if u, err := strconv.Unquote(s); err == nil {
		return u
	}
	// Octal escapes of UTF-8 bytes, which strconv takes, are the usual
	// case; fall back to stripping the quotes.
	return s[1 : len(s)-1]
}

// parseHunkHeader parses "@@ -a,b +c,d @@ section".
func parseHunkHeader(line string) *Hunk {
	end := strings.Index(line[2:], "@@")
	if end < 0 {
		return nil
	}
	ranges := strings.Fields(line[2 : end+2])
	h := &Hunk{Section: strings.TrimSpace(line[end+4:])}
	for _, r := range ranges {
		if len(r) < 2 {
			continue
		}
		start, count := parseRange(r[1:])
		switch r[0] {
		case '-':
			h.OldStart, h.OldLines = start, count
		case '+':
			h.NewStart, h.NewLines = start, count
		}
	}
	return h
}

func parseRange(s string) (int, int) {
	start, count, ok := strings.Cut(s, ",")
	a, _ := strconv.Atoi(start)
	if !ok {
		return a, 1
	}
	b, _ := strconv.Atoi(count)
	return a, b
}

// NewFileFromContent makes the change of a file that is all new, as an
// untracked file, from its content.
func NewFileFromContent(path string, content []byte, status Status) *File {
	f := &File{Path: path, OldPath: path, Status: status}
	if IsBinary(content) {
		f.Binary = true
		f.Fingerprint = fingerprint(path, string(content))
		return f
	}
	lines := SplitLines(string(content))
	if len(lines) > 0 {
		h := Hunk{OldStart: 0, OldLines: 0, NewStart: 1, NewLines: len(lines)}
		for i, l := range lines {
			h.Lines = append(h.Lines, Line{Kind: Add, New: i + 1, Text: l})
		}
		if !bytes.HasSuffix(content, []byte("\n")) {
			h.Lines[len(h.Lines)-1].NoNewline = true
		}
		f.Hunks = []Hunk{h}
	}
	f.Count()
	f.Fingerprint = fingerprint(path, string(content))
	return f
}

// IsBinary reports whether content looks binary, as git decides: a NUL
// byte in its first 8000 bytes.
func IsBinary(content []byte) bool {
	n := min(len(content), 8000)
	return bytes.IndexByte(content[:n], 0) >= 0
}

// SplitLines splits text into lines without their line endings; a final
// newline does not start another line.
func SplitLines(s string) []string {
	if s == "" {
		return nil
	}
	s = strings.TrimSuffix(s, "\n")
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSuffix(l, "\r")
	}
	return lines
}
