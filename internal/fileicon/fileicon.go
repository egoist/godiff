// Package fileicon gives files the icons of their kinds, as the file tree of
// codiff, @pierre/trees, draws them: a Go file the gopher's, a TypeScript
// file TypeScript's, each in a color of its own.
package fileicon

import (
	"strings"
	"sync"

	"github.com/egoist/mygo/ui"
)

// Icon is the icon of a kind of file.
type Icon struct {
	// Token names the kind, as go or typescript; default for the files of
	// no kind known.
	Token       string
	SVG         *ui.SVG
	Light, Dark ui.Color
}

// Color is the icon's color in the light or the dark.
func (i *Icon) Color(dark bool) ui.Color {
	if dark {
		return i.Dark
	}
	return i.Light
}

var (
	once  sync.Once
	icons map[string]*Icon
)

func load() {
	icons = make(map[string]*Icon, len(symbols))
	for token, svg := range symbols {
		icons[token] = &Icon{Token: token, SVG: ui.MustParseSVG([]byte(svg)), Light: ui.Hex(lightColors[token]), Dark: ui.Hex(darkColors[token])}
	}
}

// For returns the icon of a file, by its path: by its whole name, as
// Dockerfile, else by its extensions, the longest first, as .d.ts before
// .ts.
func For(path string) *Icon {
	once.Do(load)
	return icons[Token(path)]
}

// Token returns the kind of a file, by its path.
func Token(path string) string {
	name := strings.ToLower(path[strings.LastIndexByte(path, '/')+1:])
	if t, ok := byName[name]; ok {
		return t
	}
	for i := strings.IndexByte(name, '.'); i >= 0; {
		ext := name[i+1:]
		if t, ok := overrides[ext]; ok {
			return t
		}
		if t, ok := byExtension[ext]; ok {
			return t
		}
		j := strings.IndexByte(ext, '.')
		if j < 0 {
			break
		}
		i += j + 1
	}
	return "default"
}
