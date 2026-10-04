package git

import (
	"bytes"
	"context"
	"strings"

	"github.com/egoist/godiff/internal/diff"
)

// generatedDirs are directories of build output and dependencies, whose
// untracked files show as one collapsed row.
var generatedDirs = []string{".cache", ".next", ".parcel-cache", ".pnpm-store", ".turbo", ".yarn", "build", "coverage", "dist", "node_modules", "out", "target", "vendor"}

var (
	generatedSegments = map[string]bool{
		"__generated__": true, "__snapshots__": true, ".generated": true, "codegen": true, "gen": true,
		"generated": true, "generated-sources": true, "generated-src": true,
	}
	generatedNames = map[string]bool{
		"bun.lock": true, "bun.lockb": true, "cargo.lock": true, "gemfile.lock": true, "npm-shrinkwrap.json": true,
		"package-lock.json": true, "pnpm-lock.yaml": true, "poetry.lock": true, "pubspec.lock": true, "uv.lock": true,
		"yarn.lock": true, "go.sum": true,
	}
	generatedSuffixes = []string{
		".d.ts.map", ".g.dart", ".generated.cjs", ".generated.css", ".generated.js", ".generated.jsx", ".generated.mjs",
		".generated.ts", ".generated.tsx", ".pb.go", ".pb.gw.go", ".snap", ".snapshot",
		"-generated.js", "-generated.ts", "-generated.tsx", ".min.cjs", ".min.css", ".min.js", ".min.mjs", ".pb.cc",
		".pb.h", ".pb.rb", "_generated.go", "_generated.rs", "_pb2.py", "_pb2_grpc.py",
	}
)

// LooksGenerated reports whether a path is of a file tools usually write,
// by its name.
func LooksGenerated(path string) bool {
	p := strings.ToLower(strings.ReplaceAll(path, `\`, "/"))
	parts := strings.Split(p, "/")
	for _, part := range parts {
		if generatedSegments[part] {
			return true
		}
	}
	base := parts[len(parts)-1]
	if generatedNames[base] {
		return true
	}
	for _, s := range generatedSuffixes {
		if strings.HasSuffix(base, s) {
			return true
		}
	}
	return strings.HasSuffix(base, ".map") && !strings.HasSuffix(base, ".importmap")
}

// markGenerated marks the files git attributes (linguist-generated,
// gitlab-generated) or their names say are generated. source is the
// commit whose attributes apply, "" for the work tree's.
func (r *Repo) markGenerated(files []*diff.File, source string) {
	if len(files) == 0 {
		return
	}
	var stdin bytes.Buffer
	for _, f := range files {
		stdin.WriteString(f.Path)
		stdin.WriteByte(0)
	}
	args := []string{"check-attr"}
	if source != "" {
		args = append(args, "--source", source)
	}
	args = append(args, "-z", "--stdin", "linguist-generated", "gitlab-generated")
	decided := map[string]bool{}
	if out, err := run(context.Background(), r.Root, &stdin, args...); err == nil {
		fields := bytes.Split(out, []byte{0})
		for i := 0; i+2 < len(fields); i += 3 {
			path, value := string(fields[i]), string(fields[i+2])
			switch value {
			case "unspecified":
			case "unset", "false":
				if _, ok := decided[path]; !ok {
					decided[path] = false
				}
			default:
				decided[path] = true
			}
		}
	}
	for _, f := range files {
		if g, ok := decided[f.Path]; ok {
			f.Generated = g
		} else {
			f.Generated = LooksGenerated(f.Path)
		}
	}
}
