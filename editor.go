package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"github.com/egoist/godiff/internal/proc"
	"github.com/egoist/mygo"
)

var editorArgs = regexp.MustCompile(`"[^"]+"|'[^']+'|\S+`)

// openEditor opens a file at a line in the user's editor: the command of
// $GODIFF_EDITOR or the settings, where {file}, {line} and {repo} stand
// for the file, the line and the repository, else VS Code, else the app
// the system opens the file with.
func openEditor(command, repo, file string, line int) error {
	lineText := ""
	if line > 0 {
		lineText = strconv.Itoa(line)
	}
	if repo == "" {
		repo = filepath.Dir(file)
	}
	var candidates [][]string
	if env := os.Getenv("GODIFF_EDITOR"); env != "" {
		command = env
	}
	if command = strings.TrimSpace(command); command != "" {
		var args []string
		for _, a := range editorArgs.FindAllString(command, -1) {
			if len(a) >= 2 && (a[0] == '"' || a[0] == '\'') && a[len(a)-1] == a[0] {
				a = a[1 : len(a)-1]
			}
			args = append(args, a)
		}
		hasFile := false
		for i, a := range args[1:] {
			if strings.Contains(a, "{file}") {
				hasFile = true
			}
			a = strings.ReplaceAll(a, "{file}", file)
			a = strings.ReplaceAll(a, "{line}", lineText)
			a = strings.ReplaceAll(a, "{repo}", repo)
			args[i+1] = a
		}
		if !hasFile {
			args = append(args, file)
		}
		candidates = append(candidates, args)
	}
	// The user's command may be a console editor, which needs its window.
	custom := len(candidates)
	target := file
	if lineText != "" {
		target += ":" + lineText
	}
	for _, code := range []string{"/opt/homebrew/bin/code", "/usr/local/bin/code", "code"} {
		candidates = append(candidates, []string{code, "-g", target})
	}
	if runtime.GOOS == "darwin" {
		candidates = append(candidates, []string{"open", "-a", "Visual Studio Code", file}, []string{"open", "-t", file})
	}
	for i, args := range candidates {
		cmd := exec.Command(args[0], args[1:]...)
		if i >= custom {
			proc.HideConsole(cmd)
		}
		if err := cmd.Run(); err == nil {
			return nil
		}
	}
	if mygo.Shell.OpenPath(file) == nil {
		return nil
	}
	return errors.New("no editor could open " + file)
}
