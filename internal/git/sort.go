package git

import (
	"slices"
	"strings"

	"github.com/egoist/godiff/internal/diff"
)

// SortFiles sorts files as a file tree lists them: at each level,
// directories before files, each by name.
func SortFiles(files []*diff.File) {
	slices.SortStableFunc(files, func(a, b *diff.File) int {
		return ComparePaths(a.Path, b.Path)
	})
}

// ComparePaths orders two paths as a file tree lists them.
func ComparePaths(a, b string) int {
	as, bs := strings.Split(a, "/"), strings.Split(b, "/")
	for i := 0; i < len(as) && i < len(bs); i++ {
		aDir, bDir := i < len(as)-1, i < len(bs)-1
		if aDir != bDir {
			if aDir {
				return -1
			}
			return 1
		}
		if c := compareNames(as[i], bs[i]); c != 0 {
			return c
		}
	}
	return len(as) - len(bs)
}

func compareNames(a, b string) int {
	if c := strings.Compare(strings.ToLower(a), strings.ToLower(b)); c != 0 {
		return c
	}
	return strings.Compare(a, b)
}
