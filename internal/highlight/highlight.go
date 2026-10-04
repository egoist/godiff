// Package highlight colors source code by its tokens, with Chroma's lexers
// and the palettes of codiff's Licht and Dunkel themes.
package highlight

import (
	"path/filepath"
	"strings"
	"sync"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
)

// Class is the kind of a token, which picks its color.
type Class uint8

const (
	Plain Class = iota
	Comment
	Preproc
	Keyword
	Type
	LangConst
	Function
	ClassName
	Exception
	Number
	String
	Escape
	Regexp
	Tag
	Attribute
	Property
	Heading
	Inserted
	Deleted
	NumClasses
)

// Seg is a run of a line's bytes of one class.
type Seg struct {
	Start, End int32
	Class      Class
}

// MaxBytes bounds the files worth highlighting.
const MaxBytes = 1 << 20

var (
	lexerMu    sync.Mutex
	lexerCache = map[string]chroma.Lexer{}
)

// Lexer returns the lexer for a file name, nil for plain text. Lexers are
// kept by the file's extension, or its name without one: finding one
// tries the patterns of every lexer.
func Lexer(name string) chroma.Lexer {
	base := name
	if i := strings.LastIndexByte(base, '/'); i >= 0 {
		base = base[i+1:]
	}
	key := base
	if i := strings.LastIndexByte(base, '.'); i > 0 && !specialName(base) {
		key = "*" + strings.ToLower(base[i:])
	}
	lexerMu.Lock()
	l, ok := lexerCache[key]
	lexerMu.Unlock()
	if ok {
		return l
	}
	l = findLexer(name)
	lexerMu.Lock()
	lexerCache[key] = l
	lexerMu.Unlock()
	return l
}

var (
	specialOnce     sync.Once
	specialPatterns []string
)

// specialName reports whether a lexer matches a file name by more than
// its extension, as CMakeLists.txt or *.d.ts, or the name ends as backups
// do: its lexer is found by its whole name.
func specialName(base string) bool {
	specialOnce.Do(func() {
		for _, l := range lexers.GlobalLexerRegistry.Lexers {
			for _, glob := range l.Config().Filenames {
				ext, ok := strings.CutPrefix(glob, "*.")
				if !ok || strings.ContainsAny(ext, ".*?[") {
					specialPatterns = append(specialPatterns, glob)
				}
			}
		}
	})
	for _, suffix := range []string{"~", ".bak", ".old", ".orig", ".dpkg-dist", ".dpkg-old", ".ucf-dist", ".ucf-new", ".ucf-old", ".rpmnew", ".rpmorig", ".rpmsave"} {
		if strings.HasSuffix(base, suffix) {
			return true
		}
	}
	for _, glob := range specialPatterns {
		if ok, _ := filepath.Match(glob, base); ok {
			return true
		}
	}
	return false
}

func findLexer(name string) chroma.Lexer {
	l := lexers.Match(name)
	if l == nil {
		base := name
		if i := strings.LastIndexByte(base, '/'); i >= 0 {
			base = base[i+1:]
		}
		switch base {
		case "Makefile", "makefile", "GNUmakefile":
			l = lexers.Get("make")
		case "Dockerfile":
			l = lexers.Get("docker")
		}
	}
	if l == nil {
		return nil
	}
	return chroma.Coalesce(l)
}

// Lines tokenizes lines of the file named name, and returns the segments of
// each line, nil for plain text.
func Lines(name string, lines []string) [][]Seg {
	lexer := Lexer(name)
	if lexer == nil || len(lines) == 0 {
		return nil
	}
	size := 0
	for _, l := range lines {
		size += len(l) + 1
	}
	if size > MaxBytes {
		return nil
	}
	text := strings.Join(lines, "\n") + "\n"
	it, err := lexer.Tokenise(nil, text)
	if err != nil {
		return nil
	}
	out := make([][]Seg, len(lines))
	row, col := 0, 0
	for tok := it(); tok != chroma.EOF; tok = it() {
		class := classOf(tok.Type)
		v := tok.Value
		for v != "" && row < len(lines) {
			nl := strings.IndexByte(v, '\n')
			part := v
			if nl >= 0 {
				part = v[:nl]
			}
			if part != "" {
				end := min(col+len(part), len(lines[row]))
				if class != Plain && end > col {
					segs := out[row]
					if n := len(segs); n > 0 && segs[n-1].Class == class && int(segs[n-1].End) == col {
						segs[n-1].End = int32(end)
					} else {
						segs = append(segs, Seg{Start: int32(col), End: int32(end), Class: class})
					}
					out[row] = segs
				}
				col = end
			}
			if nl < 0 {
				break
			}
			v = v[nl+1:]
			row++
			col = 0
		}
	}
	return out
}

func classOf(t chroma.TokenType) Class {
	switch t {
	case chroma.KeywordType:
		return Type
	case chroma.KeywordConstant, chroma.NameBuiltinPseudo, chroma.NameVariableMagic:
		return LangConst
	case chroma.NameFunction, chroma.NameFunctionMagic, chroma.NameBuiltin, chroma.NameDecorator:
		return Function
	case chroma.NameClass:
		return ClassName
	case chroma.NameException:
		return Exception
	case chroma.NameConstant, chroma.LiteralStringSymbol:
		return Number
	case chroma.NameTag, chroma.NameEntity:
		return Tag
	case chroma.NameAttribute:
		return Attribute
	case chroma.NameProperty, chroma.NameLabel:
		return Property
	case chroma.LiteralStringEscape:
		return Escape
	case chroma.LiteralStringRegex:
		return Regexp
	case chroma.CommentPreproc, chroma.CommentPreprocFile:
		return Preproc
	case chroma.GenericHeading, chroma.GenericSubheading:
		return Heading
	case chroma.GenericInserted:
		return Inserted
	case chroma.GenericDeleted:
		return Deleted
	}
	switch t.Category() {
	case chroma.Keyword, chroma.Operator:
		return Keyword
	case chroma.Comment:
		return Comment
	}
	switch t.SubCategory() {
	case chroma.LiteralString:
		return String
	case chroma.LiteralNumber:
		return Number
	}
	return Plain
}
