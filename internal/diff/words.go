package diff

import (
	"unicode"
	"unicode/utf8"
)

// Range is a range of bytes of a line.
type Range struct{ Start, End int }

// Pair is a deleted line and the added line that replaces it, by their
// indices in a hunk's lines.
type Pair struct{ Del, Add int }

// Pairs pairs the deleted lines of each run of changes with the added
// lines that follow them, in order, as a split view shows them side by
// side.
func Pairs(lines []Line) []Pair {
	var pairs []Pair
	for i := 0; i < len(lines); {
		if lines[i].Kind != Del {
			i++
			continue
		}
		start := i
		for i < len(lines) && lines[i].Kind == Del {
			i++
		}
		dels := i - start
		addStart := i
		for i < len(lines) && lines[i].Kind == Add {
			i++
		}
		adds := i - addStart
		for k := range min(dels, adds) {
			pairs = append(pairs, Pair{Del: start + k, Add: addStart + k})
		}
	}
	return pairs
}

// maxWordDiffLen bounds the lines compared word by word.
const maxWordDiffLen = 1000

// WordDiff returns the ranges of a and b that differ, word by word, or
// nothing when the lines have too little in common for the ranges to help.
func WordDiff(a, b string) (ra, rb []Range) {
	if a == b || len(a) > maxWordDiffLen || len(b) > maxWordDiffLen {
		return nil, nil
	}
	ta, tb := tokens(a), tokens(b)
	// Trim the common prefix and suffix, then find the longest common
	// subsequence of what is left.
	pre := 0
	for pre < len(ta) && pre < len(tb) && a[ta[pre].Start:ta[pre].End] == b[tb[pre].Start:tb[pre].End] {
		pre++
	}
	suf := 0
	for suf < len(ta)-pre && suf < len(tb)-pre &&
		a[ta[len(ta)-1-suf].Start:ta[len(ta)-1-suf].End] == b[tb[len(tb)-1-suf].Start:tb[len(tb)-1-suf].End] {
		suf++
	}
	ma, mb := ta[pre:len(ta)-suf], tb[pre:len(tb)-suf]
	keepA := make([]bool, len(ma))
	keepB := make([]bool, len(mb))
	if len(ma)*len(mb) <= 250_000 {
		lcs(a, b, ma, mb, keepA, keepB)
	}
	ra = changed(ma, keepA)
	rb = changed(mb, keepB)
	// Lines that share little are rewritten rather than edited: marking
	// most of them would only add noise.
	same := 0
	for i, k := range keepA {
		if k {
			same += ma[i].End - ma[i].Start
		}
	}
	for i := range pre {
		same += ta[i].End - ta[i].Start
	}
	for i := range suf {
		t := ta[len(ta)-1-i]
		same += t.End - t.Start
	}
	if total := max(len(trimSpace(a)), len(trimSpace(b))); total > 0 && same*10 < total*3 {
		return nil, nil
	}
	return ra, rb
}

func trimSpace(s string) string {
	i, j := 0, len(s)
	for i < j && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	for j > i && (s[j-1] == ' ' || s[j-1] == '\t') {
		j--
	}
	return s[i:j]
}

// changed merges the tokens not kept into ranges.
func changed(toks []Range, keep []bool) []Range {
	var out []Range
	for i, t := range toks {
		if keep[i] {
			continue
		}
		if n := len(out); n > 0 && out[n-1].End == t.Start {
			out[n-1].End = t.End
			continue
		}
		out = append(out, t)
	}
	return out
}

// lcs marks the tokens of the longest common subsequence of ta and tb.
func lcs(a, b string, ta, tb []Range, keepA, keepB []bool) {
	n, m := len(ta), len(tb)
	if n == 0 || m == 0 {
		return
	}
	dp := make([]int32, (n+1)*(m+1))
	at := func(i, j int) *int32 { return &dp[i*(m+1)+j] }
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[ta[i].Start:ta[i].End] == b[tb[j].Start:tb[j].End] {
				*at(i, j) = *at(i+1, j+1) + 1
			} else {
				*at(i, j) = max(*at(i+1, j), *at(i, j+1))
			}
		}
	}
	for i, j := 0, 0; i < n && j < m; {
		switch {
		case a[ta[i].Start:ta[i].End] == b[tb[j].Start:tb[j].End]:
			keepA[i], keepB[j] = true, true
			i++
			j++
		case *at(i+1, j) >= *at(i, j+1):
			i++
		default:
			j++
		}
	}
}

// tokens splits a line into words, runs of spaces, and single other
// characters.
func tokens(s string) []Range {
	var out []Range
	i := 0
	for i < len(s) {
		r, size := rune(s[i]), 1
		if r >= 0x80 {
			r, size = decodeRune(s[i:])
		}
		j := i + size
		switch {
		case isWord(r):
			for j < len(s) {
				r2, s2 := rune(s[j]), 1
				if r2 >= 0x80 {
					r2, s2 = decodeRune(s[j:])
				}
				if !isWord(r2) {
					break
				}
				j += s2
			}
		case r == ' ' || r == '\t':
			for j < len(s) && (s[j] == ' ' || s[j] == '\t') {
				j++
			}
		}
		out = append(out, Range{i, j})
		i = j
	}
	return out
}

func isWord(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

func decodeRune(s string) (rune, int) { return utf8.DecodeRuneInString(s) }
