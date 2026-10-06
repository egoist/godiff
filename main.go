// Godiff is a native, minimal, local diff viewer for reviewing Git changes
// and committing them, after codiff, drawn by MyGo.
//
//	godiff                 the uncommitted changes of the repository here
//	godiff <path>          those of another repository
//	godiff <commit>        a commit, as HEAD~1 or a1b2c3d
//	godiff <branch>        the work tree's changes since it branched off
//	godiff --pr <number>   a pull request on GitHub, as owner/repo#123 or
//	                       its URL too
package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"runtime/pprof"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egoist/godiff/internal/git"
	"github.com/egoist/godiff/internal/github"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

const usage = `Usage: godiff [<commit> | <branch> | <pull request>] [<path>]

Review the uncommitted changes of the Git repository at <path> (default:
the current directory), a commit, the work tree's changes since it
branched off <branch>, or a pull request on GitHub, named as
owner/repo#123 or by its URL.

Options:
  --commit <ref>   review a commit
  --branch <ref>   compare the work tree with a branch
  --pr <number>    review a pull request of the repository's GitHub remote
  --cwd <dir>      the directory relative paths start from
  -h, --help       show this help
`

// request is what a command line asks to open.
type request struct {
	dir string
	src source
}

var commitLike = regexp.MustCompile(`^([0-9a-fA-F]{4,64}|(HEAD|@)([~^][0-9]*)*|.*[~^].*|.*@\{.*\})$`)

// parseArgs reads a command line, relative to dir.
func parseArgs(args []string, dir string) (request, error) {
	req := request{dir: dir}
	var refs []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-h" || a == "--help":
			return req, errHelp
		case a == "--pr":
			if i+1 >= len(args) {
				return req, fmt.Errorf("--pr needs a pull request")
			}
			i++
			req.src = source{kind: sourcePull, ref: args[i]}
		case isPullArg(a):
			req.src = source{kind: sourcePull, ref: a}
		case a == "--commit" || a == "--branch":
			if i+1 >= len(args) {
				return req, fmt.Errorf("%s needs a revision", a)
			}
			i++
			kind := sourceCommit
			if a == "--branch" {
				kind = sourceBranch
			}
			req.src = source{kind: kind, ref: args[i]}
		case strings.HasPrefix(a, "-psn_"), a == "":
			// What macOS passes to apps opened from Finder.
		case strings.HasPrefix(a, "-"):
			return req, fmt.Errorf("unknown option %s", a)
		case strings.HasPrefix(a, "/") || strings.HasPrefix(a, "./") || strings.HasPrefix(a, "../") || strings.HasPrefix(a, "~"):
			req.dir = resolve(dir, a)
		default:
			refs = append(refs, a)
		}
	}
	for _, a := range refs {
		if fi, err := os.Stat(resolve(dir, a)); err == nil && fi.IsDir() && !commitLike.MatchString(a) {
			req.dir = resolve(dir, a)
			continue
		}
		if req.src.kind != sourceWorkingTree {
			return req, fmt.Errorf("unexpected argument %s", a)
		}
		req.src = source{kind: sourceBranch, ref: a}
	}
	// A revision is a commit unless it names a branch.
	if req.src.kind == sourcePull {
		if repo, err := git.Open(req.dir); err == nil {
			ref, err := resolvePull(repo, req.src.ref)
			if err != nil {
				return req, err
			}
			req.src.ref = ref
		}
	}
	if req.src.kind == sourceBranch && req.src.ref != "" {
		if repo, err := git.Open(req.dir); err == nil {
			_, local := repo.Git("show-ref", "--verify", "--quiet", "refs/heads/"+req.src.ref)
			_, remote := repo.Git("show-ref", "--verify", "--quiet", "refs/remotes/"+req.src.ref)
			if local != nil && remote != nil {
				if _, err := repo.Resolve(req.src.ref); err == nil {
					req.src.kind = sourceCommit
				} else {
					return req, fmt.Errorf("%q is neither a branch nor a commit of this repository", req.src.ref)
				}
			}
		}
	}
	return req, nil
}

var errHelp = fmt.Errorf("help")

// isPullArg reports whether an argument names a pull request: its URL,
// owner/repo#123 or #123.
func isPullArg(a string) bool {
	_, _, ok := github.ParsePull(a)
	return ok
}

func resolve(dir, p string) string {
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(home, p[2:])
		}
	}
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Join(dir, p)
}

var (
	welcomeMu  sync.Mutex
	welcomeWin *mygo.Window
	// launched is set once the windows of the launch are open.
	launched atomic.Bool
)

// noWindows reports whether no window is open.
func noWindows() bool {
	windowsMu.Lock()
	n := len(windows)
	windowsMu.Unlock()
	welcomeMu.Lock()
	defer welcomeMu.Unlock()
	return n == 0 && (welcomeWin == nil || welcomeWin.IsDestroyed())
}

// showWelcome shows the window that opens a repository, with why none
// was.
func showWelcome(err error) {
	welcomeMu.Lock()
	defer welcomeMu.Unlock()
	if welcomeWin != nil && !welcomeWin.IsDestroyed() {
		welcomeWin.Show()
		welcomeWin.Focus()
		return
	}
	v := &welcome{err: err}
	welcomeWin = mygo.NewWindow(mygo.WindowOptions{
		Title:         "Godiff",
		Width:         640,
		Height:        420,
		MinWidth:      480,
		MinHeight:     320,
		TitleBarStyle: mygo.TitleBarHiddenInset,
		Content:       ui.View(v.view),
	})
}

func closeWelcome() {
	welcomeMu.Lock()
	defer welcomeMu.Unlock()
	if welcomeWin != nil && !welcomeWin.IsDestroyed() {
		welcomeWin.Close()
	}
	welcomeWin = nil
}

// open opens a window for a command line, or the welcome window when it
// names no repository.
func open(req request, fromUser bool) {
	err := openWindow(req.dir, req.src)
	if err == nil {
		return
	}
	if !fromUser {
		// Opened from Finder: the repository of last time.
		if last := state.lastRepository(); last != "" {
			if openWindow(last, source{}) == nil {
				return
			}
		}
	}
	showWelcome(err)
}

func main() {
	wd, _ := os.Getwd()
	args, wd := takeCwd(os.Args[1:], wd)
	req, err := parseArgs(args, wd)
	if err == errHelp {
		fmt.Print(usage)
		return
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "godiff:", err)
		os.Exit(2)
	}
	fromUser := len(args) > 0 || (wd != "/" && wd != "")

	name := "Godiff"
	if n := os.Getenv("GODIFF_NAME"); n != "" {
		// Another name runs apart from the installed app, for testing.
		name = n
	}
	mygo.App.SetName(name)
	if !mygo.App.RequestSingleInstanceLock() {
		return // the running instance opens the window
	}
	mygo.App.OnSecondInstance(func(args []string, workingDir string) {
		if len(args) > 0 {
			args = args[1:]
		}
		args, workingDir = takeCwd(args, workingDir)
		req, err := parseArgs(args, workingDir)
		if err != nil {
			go mygo.Dialog.Error("Godiff", err.Error())
			return
		}
		go open(req, true)
	})
	mygo.App.OnOpenFile(func(path string) {
		go open(request{dir: path}, true)
	})
	applyTheme(cfg.Get())
	mygo.App.SetMenu(buildMenu())
	cfg.OnChange(func(s Settings) {
		mygo.RunOnMain(func() {
			syncMenu(s)
			applyTheme(s)
		})
	})
	mygo.App.OnWindowAllClosed(func() {
		if runtime.GOOS != "darwin" {
			mygo.App.Quit()
		}
	})
	mygo.App.OnActivate(func(hasVisibleWindows bool) {
		// A click on the Dock icon with no window open opens the last
		// repository; the activation of the launch itself does not.
		if !hasVisibleWindows && launched.Load() && noWindows() {
			go open(request{dir: state.lastRepository()}, false)
		}
	})
	if path := os.Getenv("GODIFF_CPUPROFILE"); path != "" {
		// The whole session, written as the app quits.
		if f, err := os.Create(path); err == nil {
			pprof.StartCPUProfile(f)
			mygo.App.OnQuit(func() {
				pprof.StopCPUProfile()
				f.Close()
			})
		}
	}
	if path := os.Getenv("GODIFF_STARTUP_PROFILE"); path != "" {
		// The first seconds, which draw the window for the first time.
		if f, err := os.Create(path); err == nil {
			pprof.StartCPUProfile(f)
			go func() {
				time.Sleep(3 * time.Second)
				pprof.StopCPUProfile()
				f.Close()
			}()
		}
	}
	fetchAvatars, detectAgents = true, true
	mygo.App.WhenReady(func() {
		if debugFrames {
			go watchMainThread()
		}
		state.open()
		cfg.watch()
		go func() {
			open(req, fromUser)
			launched.Store(true)
		}()
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
