package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egoist/godiff/internal/diff"
	"github.com/egoist/godiff/internal/git"
	"github.com/egoist/godiff/internal/highlight"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// sourceKind is what a window reviews.
type sourceKind uint8

const (
	sourceWorkingTree sourceKind = iota // uncommitted changes
	sourceCommit                        // a commit, against its first parent
	sourceBranch                        // the work tree since it branched from a branch
)

type source struct {
	kind sourceKind
	ref  string // the commit, or the branch
}

// historyPage is how many commits the History tab loads at a time.
const historyPage = 30

// session is the review of one source, kept while the window shows
// another.
type session struct {
	comments []*comment
	viewed   map[string]string
}

// window is a window reviewing a repository.
type window struct {
	win      *mygo.Window
	repo     *git.Repo
	launch   source // what the window was opened on
	source   source
	settings Settings

	branch     string
	files      []*fileState
	commit     *git.Commit // the commit reviewed, for sourceCommit
	base       string      // the merge base, for sourceBranch
	loading    bool
	loadedOnce bool
	loadErr    error
	gen        int // counts loads, to drop the results of older ones
	genA       atomic.Int64
	viewed     map[string]string

	// The sidebar.
	sidebarShown bool
	sidebarWidth float32
	tab          int // 0 files, 1 history
	filter       string
	filterFocus  bool
	treeList     ui.ListState
	treeEl       *ui.Element
	treeSel      string // the key of the row chosen
	treeRows     []treeRow
	closedDirs   map[string]bool
	treeItems    map[string]*treeNode
	treeRoots    []string
	history      []git.Commit
	historyLimit int
	historyMore  bool
	historyList  ui.ListState
	historyEl    *ui.Element
	// historyFilter filters the History tab, apart from the files'.
	historyFilter  string
	historyLoading bool
	// reloaded are the files that changed in the last refresh.
	reloaded  map[string]bool
	dragWidth float32

	// The diff surface.
	rows       []row
	rowsDirty  bool
	list       ui.ListState
	current    int // the file shown at the top of the surface
	hscroll    map[string]float32
	diffListEl *ui.Element
	revealedAt time.Time
	now        time.Time
	charW      float32
	charKey    string
	// The hunk chosen with j and k.
	selFile, selHunk int
	focusList        bool
	focusedOnce      bool
	// typing is set while a field has the focus, whose keys are its own.
	typing bool

	// Find in diffs.
	finding     bool
	query       string
	matches     []match
	match       int
	matchesFor  string
	fileMatches map[int]bool

	// The reviews of the other sources.
	sessions map[source]*session
	// Review comments, the one asked about discarding, and the git user.
	comments   []*comment
	discarding *comment
	user       string
	copied     string
	copiedAt   time.Time

	// Committing.
	commitOpen     bool
	subject        string
	body           string
	commitPaths    map[string]bool
	commitBusy     bool
	commitErr      string
	commitOutput   string
	commitDone     string // the hash committed
	committedFiles int
	commitFocus    bool

	// The command bar.
	paletteOpen  bool
	paletteQuery string
	paletteRow   int
	paletteList  ui.ListState
	// palettePointer is where the pointer was over the list.
	palettePointer [2]float32

	// The dialog opening a commit or a branch.
	dialog      dialogKind
	dialogOpen  bool
	dialogValue string
	dialogErr   string

	// changed is set when the work tree changed since the last load.
	changed   bool
	signature string
	help      bool
}

var (
	windowsMu sync.Mutex
	windows   []*window
)

// openWindow opens a window on the repository holding dir.
func openWindow(dir string, src source) error {
	repo, err := git.Open(dir)
	if err != nil {
		return err
	}
	if src.kind == sourceCommit {
		hash, err := repo.Resolve(src.ref)
		if err != nil {
			return err
		}
		src.ref = hash
	}
	// A repository already open comes to the front.
	windowsMu.Lock()
	for _, w := range windows {
		if w.repo.Root == repo.Root && w.launch == src {
			windowsMu.Unlock()
			w.win.Show()
			w.win.Focus()
			return nil
		}
	}
	windowsMu.Unlock()

	w := newWindow(repo, src)
	w.win = mygo.NewWindow(mygo.WindowOptions{
		Title:          windowTitle(repo.Root, src),
		Width:          1280,
		Height:         860,
		MinWidth:       720,
		MinHeight:      420,
		StateKey:       "main",
		TitleBarStyle:  mygo.TitleBarHiddenInset,
		TitleBarHeight: 52,
		Vibrancy:       mygo.VibrancySidebar,
		Content:        ui.View(w.view),
	})
	windowsMu.Lock()
	windows = append(windows, w)
	windowsMu.Unlock()
	stop := make(chan struct{})
	w.win.OnClose(func(*mygo.CloseEvent) {
		if w.settings.CopyCommentsOnClose && len(w.comments) > 0 {
			mygo.Clipboard.WriteText(w.commentsMarkdown())
		}
	})
	offSettings := cfg.OnChange(func(s Settings) { w.win.Update(func() { w.applySettings(s) }) })
	w.win.OnClosed(func() {
		close(stop)
		offSettings()
		windowsMu.Lock()
		for i, o := range windows {
			if o == w {
				windows = append(windows[:i], windows[i+1:]...)
				break
			}
		}
		windowsMu.Unlock()
	})
	w.win.OnFocus(func() { go w.checkChanges() })
	go state.setLastRepository(repo.Root)
	w.load()
	w.loadHistory()
	go w.watch(stop)
	w.captureIfAsked()
	return nil
}

// newWindow makes the state of a window reviewing a source of a
// repository.
func newWindow(repo *git.Repo, src source) *window {
	width, shown := state.layout()
	w := &window{
		repo:         repo,
		launch:       src,
		source:       src,
		settings:     cfg.Get(),
		sidebarShown: shown,
		sidebarWidth: width,
		historyLimit: historyPage,
		viewed:       state.viewed(repo.Root),
		hscroll:      map[string]float32{},
		closedDirs:   map[string]bool{},
		reloaded:     map[string]bool{},
		fileMatches:  map[int]bool{},
		selFile:      -1,
		selHunk:      -1,
	}
	w.dragWidth = w.sidebarWidth
	w.list.Key = func(i int) any { return w.key(&w.rows[i]) }
	w.list.Header = func(i int) bool { return w.rows[i].kind == rowHeader }
	return w
}

// windowTitle is "<repository>[/<source>] · Godiff".
func windowTitle(root string, src source) string {
	name := filepath.Base(root)
	switch src.kind {
	case sourceCommit:
		name += "/" + shortHash(src.ref)
	case sourceBranch:
		name += "/" + src.ref
	}
	return name + " · Godiff"
}

func shortHash(h string) string {
	if len(h) > 7 {
		return h[:7]
	}
	return h
}

// applySettings takes settings changed in the file or the menus.
func (w *window) applySettings(s Settings) {
	old := w.settings
	w.settings = s
	if old.ShowWhitespace != s.ShowWhitespace {
		w.load()
	}
	if old.DiffStyle != s.DiffStyle || old.WordWrap != s.WordWrap || old.CodeFontSize != s.CodeFontSize {
		w.rowsDirty = true
	}
}

// background runs fn on a goroutine of its own, or at once in tests,
// which have no window.
func (w *window) background(fn func()) {
	if w.win == nil {
		fn()
		return
	}
	go fn()
}

// update runs fn on the main thread, then draws a frame.
func (w *window) update(fn func()) {
	if w.win == nil {
		fn()
		return
	}
	w.win.Update(fn)
}

func (w *window) invalidate() {
	if w.win != nil {
		w.win.Invalidate()
	}
}

func (w *window) split() bool { return w.settings.DiffStyle != "unified" }

// splitFile reports whether a file shows side by side: files that are
// all new or all gone show in one column.
func (w *window) splitFile(f *fileState) bool {
	if !w.split() {
		return false
	}
	return !f.oneSided()
}

// setSource shows another source in the window.
func (w *window) setSource(src source) {
	if w.source == src {
		return
	}
	// Each source keeps its review: the comments, and what was viewed of a
	// commit.
	if w.sessions == nil {
		w.sessions = map[source]*session{}
	}
	w.sessions[w.source] = &session{comments: w.comments, viewed: w.viewed}
	w.comments, w.viewed = nil, nil
	if s := w.sessions[src]; s != nil {
		w.comments, w.viewed = s.comments, s.viewed
	}
	if w.viewed == nil {
		w.viewed = map[string]string{}
		if src.kind != sourceCommit {
			w.viewed = state.viewed(w.repo.Root)
		}
	}
	w.source = src
	w.files, w.rows, w.commit = nil, nil, nil
	w.list = ui.ListState{Key: w.list.Key, Header: w.list.Header}
	w.selFile, w.selHunk = -1, -1
	w.finding, w.query = false, ""
	w.hscroll = map[string]float32{}
	w.commitOpen = false
	if w.win != nil {
		w.win.SetTitle(windowTitle(w.repo.Root, src))
	}
	w.load()
}

// load reads the changes of the source, then the contents of their files.
func (w *window) load() {
	w.gen++
	gen := w.gen
	src := w.source
	opts := git.Options{ShowWhitespace: w.settings.ShowWhitespace}
	w.loading = true
	w.loadErr = nil
	w.changed = false
	w.genA.Store(int64(gen))
	w.background(func() {
		var (
			files  []*diff.File
			commit *git.Commit
			base   string
			err    error
		)
		branch := w.repo.Branch()
		sig := ""
		switch src.kind {
		case sourceWorkingTree:
			sig = w.repo.StatusSignature()
			files, err = w.repo.WorkingTree(opts)
		case sourceCommit:
			var c git.Commit
			c, err = w.repo.CommitInfo(src.ref)
			if err == nil {
				commit = &c
				files, err = w.repo.CommitDiff(c, opts)
			}
		case sourceBranch:
			sig = w.repo.StatusSignature()
			files, base, err = w.repo.Compare(src.ref, opts)
		}
		// The revisions of the old and the new side; "" is the work tree.
		var oldRev, newRev string
		switch src.kind {
		case sourceWorkingTree:
			if w.repo.HasHead() {
				oldRev = "HEAD"
			}
		case sourceCommit:
			if commit != nil && len(commit.Parents) > 0 {
				oldRev = commit.Parents[0]
			}
			newRev = src.ref
		case sourceBranch:
			oldRev = base
		}
		w.update(func() {
			if gen != w.gen {
				return
			}
			w.loading = false
			w.loadErr = err
			w.branch = branch
			w.commit = commit
			w.base = base
			if sig != "" {
				w.signature = sig
			}
			w.setFiles(files)
			if !w.loadedOnce && len(files) == 0 && src.kind != sourceCommit {
				// Nothing to review: the history shows instead.
				w.tab = 1
			}
			w.loadedOnce = true
			if err == nil {
				// Files unchanged since the last load keep their contents.
				var files []*fileState
				for _, f := range w.files {
					if !f.loaded {
						files = append(files, f)
					}
				}
				w.background(func() { w.loadContents(gen, oldRev, newRev, files) })
			}
		})
	})
}

// setFiles shows newly read files, keeping what the user did to those
// that did not change.
func (w *window) setFiles(files []*diff.File) {
	prev := map[string]*fileState{}
	for _, f := range w.files {
		prev[f.Path] = f
	}
	// A refresh marks the files that changed since, and opens them.
	refresh := len(prev) > 0
	w.reloaded = map[string]bool{}
	next := make([]*fileState, 0, len(files))
	for _, f := range files {
		fs := &fileState{File: f}
		switch p := prev[f.Path]; {
		case p != nil && p.Fingerprint == f.Fingerprint:
			fs.collapsed = p.collapsed
			fs.expanded = p.expanded
			// The contents stay until they load again.
			fs.oldLines, fs.newLines, fs.oldHL, fs.newHL = p.oldLines, p.newLines, p.oldHL, p.newHL
			fs.oldImage, fs.newImage, fs.oldSize, fs.newSize = p.oldImage, p.newImage, p.oldSize, p.newSize
			fs.loaded = p.loaded
		case refresh:
			w.reloaded[f.Path] = true
			fs.collapsed = f.Generated || f.Directory
		default:
			fs.collapsed = w.isViewed(fs) || f.Generated || f.Directory
		}
		next = append(next, fs)
	}
	w.files = next
	w.matchesFor = "\x00" // find again
	w.buildTree()
	w.rowsDirty = true
	w.pruneComments()
}

// isViewed reports whether the user marked the file viewed, as it is now.
func (w *window) isViewed(f *fileState) bool {
	return f.Fingerprint != "" && w.viewed[f.Path] == f.Fingerprint
}

func (w *window) setViewed(f *fileState, viewed bool) {
	fp := ""
	if viewed {
		fp = f.Fingerprint
		w.viewed[f.Path] = fp
	} else {
		delete(w.viewed, f.Path)
	}
	// Only the work tree's files are remembered: commits do not change.
	if w.source.kind != sourceCommit {
		go state.setViewed(w.repo.Root, f.Path, fp)
	}
	f.collapsed = viewed
	w.rowsDirty = true
}

// maxContent is the size of the files whose contents are loaded.
const maxContent = 2 << 20

// loaded is a file's contents, read and tokenized, or decoded for
// pictures.
type loaded struct {
	file               *fileState
	oldLines, newLines []string
	oldHL, newHL       [][]highlight.Seg
	oldData, newData   []byte // of pictures
	oldImage, newImage *ui.Bitmap
}

// maxImage is the size of the pictures shown.
const maxImage = 32 << 20

// isImage reports whether a path is of a picture the app shows.
func isImage(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".bmp", ".webp", ".ico":
		return true
	}
	return false
}

// loadContents reads both sides of the files, for their colors and their
// unchanged lines, and shows them as they come.
func (w *window) loadContents(gen int, oldRev, newRev string, files []*fileState) {
	contents, err := w.repo.NewContents()
	if err != nil {
		return
	}
	defer contents.Close()

	jobs := make(chan loaded)
	results := make(chan loaded)
	var wg sync.WaitGroup
	for range max(runtime.NumCPU()-1, 1) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobs {
				job.oldHL = highlight.Lines(job.file.OldPath, job.oldLines)
				job.newHL = highlight.Lines(job.file.Path, job.newLines)
				if job.oldData != nil {
					job.oldImage, _ = ui.DecodeBitmap(job.oldData)
				}
				if job.newData != nil {
					job.newImage, _ = ui.DecodeBitmap(job.newData)
				}
				results <- job
			}
		}()
	}
	go func() {
		defer close(jobs)
		read := func(rev, path string) []string {
			var data []byte
			if rev == "" {
				data = w.repo.ReadWorkTree(path)
			} else {
				data, _ = contents.Read(rev, path)
			}
			if data == nil || len(data) > maxContent || diff.IsBinary(data) {
				return nil
			}
			lines := diff.SplitLines(string(data))
			if lines == nil {
				lines = []string{}
			}
			return lines
		}
		readRaw := func(rev, path string) []byte {
			var data []byte
			if rev == "" {
				data = w.repo.ReadWorkTree(path)
			} else {
				data, _ = contents.Read(rev, path)
			}
			if len(data) > maxImage {
				return nil
			}
			return data
		}
		for _, f := range files {
			if w.stale(gen) {
				return
			}
			job := loaded{file: f}
			if f.Binary && isImage(f.Path) {
				if oldRev != "" && f.Status != diff.Added && f.Status != diff.Untracked {
					job.oldData = readRaw(oldRev, f.OldPath)
				}
				if f.Status != diff.Deleted {
					job.newData = readRaw(newRev, f.Path)
				}
				jobs <- job
				continue
			}
			if !f.Binary && !f.Directory && !f.TooLarge {
				if oldRev != "" && f.Status != diff.Added && f.Status != diff.Untracked {
					job.oldLines = read(oldRev, f.OldPath)
				}
				if f.Status != diff.Deleted {
					job.newLines = read(newRev, f.Path)
				}
			}
			jobs <- job
		}
	}()
	go func() {
		wg.Wait()
		close(results)
	}()

	var batch []loaded
	flush := func() {
		if len(batch) == 0 {
			return
		}
		done := batch
		batch = nil
		w.update(func() {
			if gen != w.gen {
				return
			}
			for _, l := range done {
				f := l.file
				f.oldLines, f.newLines = l.oldLines, l.newLines
				f.oldHL, f.newHL = l.oldHL, l.newHL
				f.oldImage, f.newImage = l.oldImage, l.newImage
				f.oldSize, f.newSize = len(l.oldData), len(l.newData)
				f.loaded = true
			}
			w.rowsDirty = true
		})
	}
	tick := time.NewTicker(60 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case l, ok := <-results:
			if !ok {
				flush()
				return
			}
			batch = append(batch, l)
		case <-tick.C:
			flush()
		}
	}
}

func (w *window) stale(gen int) bool { return w.genA.Load() != int64(gen) }

// loadHistory reads the commits of the History tab.
func (w *window) loadHistory() {
	limit := w.historyLimit
	w.historyLoading = true
	w.background(func() {
		commits, err := w.repo.Log(0, limit)
		if err != nil {
			commits = nil
		}
		w.update(func() {
			w.history = commits
			w.historyMore = len(commits) >= limit
			w.historyLoading = false
		})
	})
}

// watch notices changes of the work tree, polling as codiff does: often
// while the window has the focus, rarely while it is in the background.
func (w *window) watch(stop chan struct{}) {
	for {
		delay := 10 * time.Second
		if w.win.IsFocused() {
			delay = 2500 * time.Millisecond
		} else if !w.win.IsVisible() || w.win.IsMinimized() {
			delay = 30 * time.Second
		}
		select {
		case <-stop:
			return
		case <-time.After(delay):
		}
		w.checkChanges()
	}
}

// checkChanges compares the work tree with what the window shows.
func (w *window) checkChanges() {
	var src source
	var sig string
	var busy bool
	mygo.RunOnMain(func() { src, sig, busy = w.source, w.signature, w.loading || w.changed })
	if busy || sig == "" || src.kind == sourceCommit {
		return
	}
	if now := w.repo.StatusSignature(); now != sig {
		w.win.Update(func() {
			if w.source == src && !w.loading {
				w.changed = true
			}
		})
	}
}

// refresh loads the source again, and the history.
func (w *window) refresh() {
	w.load()
	w.loadHistory()
}

// openInEditor opens a file of the repository in the user's editor, at a
// line.
func (w *window) openInEditor(path string, line int) {
	abs := filepath.Join(w.repo.Root, filepath.FromSlash(path))
	if _, err := os.Stat(abs); err != nil {
		abs = w.repo.Root
		line = 0
	}
	go func() {
		if err := openEditor(w.settings.EditorCommand, w.repo.Root, abs, line); err != nil {
			mygo.Dialog.Error("Could not open the file", err.Error())
		}
	}()
}

// abbreviateHome writes the home directory as ~.
func abbreviateHome(p string) string {
	home, err := os.UserHomeDir()
	if err == nil && (p == home || strings.HasPrefix(p, home+string(filepath.Separator))) {
		return "~" + p[len(home):]
	}
	return p
}

// shortPath abbreviates the directories of a path to their first letter,
// as fish does: ~/P/fate.
func shortPath(p string) string {
	p = abbreviateHome(p)
	parts := strings.Split(p, "/")
	for i := 1; i < len(parts)-1; i++ {
		part := parts[i]
		if part == "" || part == "~" {
			continue
		}
		n := 1
		if strings.HasPrefix(part, ".") && len(part) > 1 {
			n = 2
		}
		r := []rune(part)
		parts[i] = string(r[:min(n, len(r))])
	}
	return strings.Join(parts, "/")
}

// errorText is the text of an error for the window.
func errorText(err error) string {
	var ge *git.Error
	if errors.As(err, &ge) && strings.TrimSpace(ge.Stderr) != "" {
		return strings.TrimSpace(ge.Stderr)
	}
	return fmt.Sprint(err)
}
