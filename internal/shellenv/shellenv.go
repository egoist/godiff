// Package shellenv finds the programs of the user's shell: an app opened
// from Finder or the Dock has the system's PATH alone, without the
// directories of Homebrew, mise or npm where gh and the coding agents are.
package shellenv

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/egoist/godiff/internal/proc"
)

var (
	once sync.Once
	path string
)

// Path returns the app's own PATH, the user's shell's, and the usual
// directories of package managers after them. With GODIFF_NO_SHELL_PATH,
// as in tests, it is the app's own alone.
func Path() string {
	if os.Getenv("GODIFF_NO_SHELL_PATH") != "" {
		return os.Getenv("PATH")
	}
	once.Do(func() { path = resolve() })
	return path
}

func resolve() string {
	dirs := filepath.SplitList(os.Getenv("PATH"))
	dirs = append(dirs, filepath.SplitList(shellPath())...)
	if home, err := os.UserHomeDir(); err == nil && runtime.GOOS != "windows" {
		for _, d := range []string{".local/bin", ".local/share/mise/shims", ".bun/bin", ".opencode/bin", ".npm-global/bin", ".volta/bin", ".cargo/bin", "go/bin"} {
			dirs = append(dirs, filepath.Join(home, d))
		}
		dirs = append(dirs, "/opt/homebrew/bin", "/usr/local/bin", "/usr/bin", "/bin")
	}
	seen := map[string]bool{}
	var out []string
	for _, d := range dirs {
		if d == "" || seen[d] {
			continue
		}
		seen[d] = true
		out = append(out, d)
	}
	return strings.Join(out, string(os.PathListSeparator))
}

// shellPath asks the user's shell for its PATH, as an interactive login
// shell sets it.
func shellPath() string {
	if runtime.GOOS == "windows" {
		return ""
	}
	sh := os.Getenv("SHELL")
	if sh == "" {
		sh = "/bin/sh"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Markers keep out what the shell's configuration prints.
	cmd := exec.CommandContext(ctx, sh, "-i", "-l", "-c", `printf "__GODIFF_PATH__%s__END__" "$PATH"`)
	proc.HideConsole(cmd)
	cmd.Stdin = nil
	out, err := cmd.Output()
	if err != nil && len(out) == 0 {
		return ""
	}
	s := string(out)
	start := strings.Index(s, "__GODIFF_PATH__")
	end := strings.LastIndex(s, "__END__")
	if start < 0 || end < start {
		return ""
	}
	return s[start+len("__GODIFF_PATH__") : end]
}

// LookPath finds a program in Path.
func LookPath(name string) (string, error) {
	for _, dir := range filepath.SplitList(Path()) {
		p := filepath.Join(dir, name)
		if runtime.GOOS == "windows" {
			for _, ext := range []string{".exe", ".cmd", ".bat"} {
				if fi, err := os.Stat(p + ext); err == nil && !fi.IsDir() {
					return p + ext, nil
				}
			}
			continue
		}
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0 {
			return p, nil
		}
	}
	return "", &exec.Error{Name: name, Err: exec.ErrNotFound}
}

// Environ returns the app's environment with Path as its PATH, for the
// programs it runs, which run others in turn.
func Environ() []string {
	env := os.Environ()
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		if !strings.HasPrefix(kv, "PATH=") {
			out = append(out, kv)
		}
	}
	return append(out, "PATH="+Path())
}
