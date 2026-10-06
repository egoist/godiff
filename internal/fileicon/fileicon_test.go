package fileicon

import "testing"

func TestToken(t *testing.T) {
	for path, want := range map[string]string{
		"main.go":                   "go",
		"web/src/App.tsx":           "react",
		"web/src/app.ts":            "typescript",
		"types/global.d.ts":         "typescript",
		"lib/index.mjs":             "javascript",
		"Dockerfile":                "docker",
		"deploy/docker-compose.yml": "docker",
		".gitignore":                "git",
		"README.md":                 "markdown",
		"pnpm-lock.yaml":            "yml",
		"bun.lock":                  "bun",
		"vite.config.ts":            "vite",
		"styles/site.scss":          "sass",
		"logo.PNG":                  "image",
		"LICENSE":                   "default",
		"Makefile.unknown-kind":     "default",
	} {
		if got := Token(path); got != want {
			t.Errorf("%s: %s, want %s", path, got, want)
		}
	}
}

func TestIcons(t *testing.T) {
	for token := range symbols {
		i := For("x." + token)
		if i == nil {
			t.Fatalf("no icon for %s", token)
		}
		ic := icons[token]
		if ic.SVG == nil || ic.Light.A == 0 || ic.Dark.A == 0 {
			t.Errorf("%s: %+v", token, ic)
		}
	}
	if i := For("main.go"); i.Token != "go" || i.Color(true) == i.Color(false) {
		t.Errorf("go icon %+v", i)
	}
}
