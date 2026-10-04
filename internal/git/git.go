// Package git reads the changes of a repository and commits them, through
// the git command line.
package git

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/egoist/godiff/internal/diff"
)

// EmptyTree is the hash of git's empty tree, the base of a repository
// without commits.
const EmptyTree = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

// Repo is a git repository's work tree.
type Repo struct {
	Root string
}

// ErrNotRepository is returned by Open for a directory outside any work
// tree.
var ErrNotRepository = errors.New("not a git repository")

// Open finds the repository holding dir.
func Open(dir string) (*Repo, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if fi, err := os.Stat(abs); err != nil {
		return nil, err
	} else if !fi.IsDir() {
		abs = filepath.Dir(abs)
	}
	out, err := run(context.Background(), abs, nil, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", abs, ErrNotRepository)
	}
	return &Repo{Root: strings.TrimSpace(string(out))}, nil
}

// Error is a git command that failed, with what it printed.
type Error struct {
	Args   []string
	Stderr string
	Err    error
}

func (e *Error) Error() string {
	msg := strings.TrimSpace(e.Stderr)
	if msg == "" {
		msg = e.Err.Error()
	}
	return "git " + strings.Join(e.Args, " ") + ": " + msg
}

func (e *Error) Unwrap() error { return e.Err }

func run(ctx context.Context, dir string, stdin io.Reader, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Stdin = stdin
	// Reading must not take the index lock from the user's own git
	// commands, and must print paths and messages the same everywhere.
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "LC_ALL=C", "GIT_PAGER=cat", "GIT_TERMINAL_PROMPT=0")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return stdout.Bytes(), &Error{Args: args, Stderr: stderr.String(), Err: err}
	}
	return stdout.Bytes(), nil
}

// Git runs git in the repository and returns what it printed.
func (r *Repo) Git(args ...string) ([]byte, error) {
	return run(context.Background(), r.Root, nil, args...)
}

func (r *Repo) gitString(args ...string) string {
	out, err := r.Git(args...)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// Branch returns the name of the branch checked out, or the abbreviated
// commit of a detached HEAD.
func (r *Repo) Branch() string {
	if b := r.gitString("symbolic-ref", "--short", "-q", "HEAD"); b != "" {
		return b
	}
	return r.gitString("rev-parse", "--short", "HEAD")
}

// ConfigValue returns a value of git's configuration, "" when unset.
func (r *Repo) ConfigValue(key string) string {
	return r.gitString("config", "--get", key)
}

// HasHead reports whether the repository has a commit.
func (r *Repo) HasHead() bool {
	_, err := r.Git("rev-parse", "--verify", "-q", "HEAD")
	return err == nil
}

// Resolve returns the full hash of a revision.
func (r *Repo) Resolve(rev string) (string, error) {
	out, err := r.Git("rev-parse", "--verify", "-q", rev+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("unknown revision %q", rev)
	}
	return strings.TrimSpace(string(out)), nil
}

// diffArgs are the options of every diff: no colors, external tools or
// prefixes of the user's configuration, and renames detected.
func diffArgs(opts Options) []string {
	args := []string{"-c", "core.quotePath=false", "diff", "--no-color", "--no-ext-diff", "--no-textconv", "--src-prefix=a/", "--dst-prefix=b/", "-M", "-U3", "--submodule=short"}
	if !opts.ShowWhitespace {
		args = append(args, "--ignore-space-at-eol", "-w")
	}
	return args
}

// Options change how changes are read.
type Options struct {
	// ShowWhitespace keeps changes of whitespace alone.
	ShowWhitespace bool
}

// maxPatch is the size of patch beyond which a file's lines are not shown.
const maxPatch = 4 << 20

// WorkingTree returns the changes of the work tree and the index against
// HEAD, untracked files included.
func (r *Repo) WorkingTree(opts Options) ([]*diff.File, error) {
	base := "HEAD"
	if !r.HasHead() {
		base = EmptyTree
	}
	args := append(diffArgs(opts), base, "--")
	out, err := r.Git(args...)
	if err != nil {
		return nil, err
	}
	files := diff.Parse(out)
	untracked, err := r.untrackedFiles()
	if err != nil {
		return nil, err
	}
	files = append(files, untracked...)
	r.markConflicts(files)
	r.markGenerated(files, "")
	SortFiles(files)
	return files, nil
}

// maxUntracked is the number of untracked files listed one by one.
const maxUntracked = 1000

// untrackedFiles returns the changes of the untracked files, with
// directories of build output and dependencies as one row each.
func (r *Repo) untrackedFiles() ([]*diff.File, error) {
	paths, err := r.Untracked()
	if err != nil {
		return nil, err
	}
	var files []*diff.File
	for i, path := range paths {
		if i == maxUntracked {
			more := len(paths) - maxUntracked
			files = append(files, &diff.File{
				Path: fmt.Sprintf("Untracked files not shown (%d more)", more), Status: diff.Untracked, Directory: true,
				Note: fmt.Sprintf("%d untracked files are not shown.", more), Fingerprint: fmt.Sprint("more:", more),
			})
			break
		}
		files = append(files, r.untrackedFile(path))
	}
	args := []string{"-c", "core.quotePath=false", "ls-files", "--others", "--exclude-standard", "--directory", "-z", "--"}
	for _, g := range generatedDirs {
		args = append(args, g, ":(glob)**/"+g+"/")
	}
	if out, err := r.Git(args...); err == nil {
		seen := map[string]bool{}
		for _, p := range bytes.Split(out, []byte{0}) {
			dir := strings.TrimSuffix(string(p), "/")
			if dir == "" || seen[dir] {
				continue
			}
			seen[dir] = true
			files = append(files, &diff.File{
				Path: dir, OldPath: dir, Status: diff.Untracked, Directory: true,
				Note: "Untracked directory is collapsed by default.", Fingerprint: "dir:" + dir,
			})
		}
	}
	for _, f := range files {
		f.OldPath = f.Path
	}
	return files, nil
}

// untrackedFile makes the change of a new file from its content.
func (r *Repo) untrackedFile(path string) *diff.File {
	full := filepath.Join(r.Root, filepath.FromSlash(path))
	fi, err := os.Lstat(full)
	if err != nil {
		return &diff.File{Path: path, OldPath: path, Status: diff.Untracked}
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		target, _ := os.Readlink(full)
		return diff.NewFileFromContent(path, []byte(target), diff.Untracked)
	}
	if fi.Size() > maxPatch {
		f := &diff.File{Path: path, OldPath: path, Status: diff.Untracked, TooLarge: true}
		f.Fingerprint = fmt.Sprintf("%s:%d:%d", path, fi.Size(), fi.ModTime().UnixNano())
		return f
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return &diff.File{Path: path, OldPath: path, Status: diff.Untracked}
	}
	return diff.NewFileFromContent(path, data, diff.Untracked)
}

// Untracked returns the paths of the untracked files that are not
// ignored, outside directories of build output and dependencies.
func (r *Repo) Untracked() ([]string, error) {
	args := []string{"-c", "core.quotePath=false", "ls-files", "--others", "--exclude-standard", "-z", "--", "."}
	for _, g := range generatedDirs {
		args = append(args, ":(exclude)"+g+"/**", ":(exclude)**/"+g+"/**")
	}
	out, err := r.Git(args...)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, p := range bytes.Split(out, []byte{0}) {
		if len(p) > 0 {
			paths = append(paths, string(p))
		}
	}
	slices.Sort(paths)
	return paths, nil
}

// markConflicts marks the files with merge conflicts.
func (r *Repo) markConflicts(files []*diff.File) {
	out, err := r.Git("diff", "--name-only", "--diff-filter=U", "-z")
	if err != nil || len(out) == 0 {
		return
	}
	conflicted := map[string]bool{}
	for _, p := range bytes.Split(out, []byte{0}) {
		conflicted[string(p)] = true
	}
	for _, f := range files {
		if conflicted[f.Path] {
			f.Status = diff.Conflict
		}
	}
}

// StatusSignature returns a value that changes when the work tree's
// changes do, cheaply, to notice edits made outside the app.
func (r *Repo) StatusSignature() string {
	out, err := r.Git("-c", "core.quotePath=false", "status", "--porcelain=v2", "--branch", "-z", "-uall")
	if err != nil {
		return "error:" + err.Error()
	}
	var b strings.Builder
	b.Write(out)
	// A file changed twice has the same status: its size and time tell.
	fields := bytes.Split(out, []byte{0})
	for i := 0; i < len(fields); i++ {
		rec := string(fields[i])
		var path string
		switch {
		case strings.HasPrefix(rec, "? "):
			path = rec[2:]
		case strings.HasPrefix(rec, "1 "):
			path = nthField(rec, 8)
		case strings.HasPrefix(rec, "2 "):
			path = nthField(rec, 9)
			i++ // the original path
		case strings.HasPrefix(rec, "u "):
			path = nthField(rec, 10)
		}
		if path == "" {
			continue
		}
		if fi, err := os.Lstat(filepath.Join(r.Root, filepath.FromSlash(path))); err == nil {
			fmt.Fprintf(&b, "|%s:%d:%d:%d", path, fi.Mode(), fi.Size(), fi.ModTime().UnixNano())
		} else {
			fmt.Fprintf(&b, "|%s:missing", path)
		}
	}
	return b.String()
}

// nthField returns what follows the nth space of a record.
func nthField(rec string, n int) string {
	for range n {
		i := strings.IndexByte(rec, ' ')
		if i < 0 {
			return ""
		}
		rec = rec[i+1:]
	}
	return rec
}

// Commit is a commit of the history.
type Commit struct {
	Hash      string
	Short     string
	Parents   []string
	Author    string
	Email     string
	Time      time.Time
	Subject   string
	Body      string
	Refs      string
	Additions int
	Deletions int
	Files     int
}

const logFormat = "%H%x1f%h%x1f%P%x1f%an%x1f%ae%x1f%at%x1f%D%x1f%s%x1f%b%x1e"

// Log returns n commits of the history of HEAD, after skipping skip.
func (r *Repo) Log(skip, n int) ([]Commit, error) {
	if !r.HasHead() {
		return nil, nil
	}
	out, err := r.Git("log", "--no-color", "--format="+logFormat, "--skip="+strconv.Itoa(skip), "-n", strconv.Itoa(n), "HEAD", "--")
	if err != nil {
		return nil, err
	}
	return parseLog(out), nil
}

// CommitInfo returns a commit.
func (r *Repo) CommitInfo(rev string) (Commit, error) {
	out, err := r.Git("log", "--no-color", "--format="+logFormat, "-n", "1", rev, "--")
	if err != nil {
		return Commit{}, err
	}
	commits := parseLog(out)
	if len(commits) == 0 {
		return Commit{}, fmt.Errorf("unknown revision %q", rev)
	}
	return commits[0], nil
}

func parseLog(out []byte) []Commit {
	var commits []Commit
	for _, rec := range bytes.Split(out, []byte{0x1e}) {
		rec = bytes.TrimLeft(rec, "\n")
		if len(rec) == 0 {
			continue
		}
		f := strings.SplitN(string(rec), "\x1f", 9)
		if len(f) < 9 {
			continue
		}
		sec, _ := strconv.ParseInt(f[5], 10, 64)
		c := Commit{
			Hash: f[0], Short: f[1], Author: f[3], Email: f[4],
			Time: time.Unix(sec, 0), Refs: f[6], Subject: f[7], Body: strings.TrimSpace(f[8]),
		}
		if f[2] != "" {
			c.Parents = strings.Fields(f[2])
		}
		commits = append(commits, c)
	}
	return commits
}

// CommitDiff returns the changes of a commit against its first parent.
func (r *Repo) CommitDiff(c Commit, opts Options) ([]*diff.File, error) {
	base := EmptyTree
	if len(c.Parents) > 0 {
		base = c.Parents[0]
	}
	args := append(diffArgs(opts), base, c.Hash, "--")
	out, err := r.Git(args...)
	if err != nil {
		return nil, err
	}
	files := diff.Parse(out)
	r.markGenerated(files, c.Hash)
	SortFiles(files)
	return files, nil
}

// MergeBase returns the merge base of two revisions.
func (r *Repo) MergeBase(a, b string) (string, error) {
	out, err := r.Git("merge-base", a, b)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// Compare returns the changes of the work tree, committed or not, since it
// branched from base.
func (r *Repo) Compare(base string, opts Options) ([]*diff.File, string, error) {
	mb, err := r.MergeBase(base, "HEAD")
	if err != nil {
		return nil, "", fmt.Errorf("no common ancestor of %s and HEAD", base)
	}
	args := append(diffArgs(opts), mb, "--")
	out, err := r.Git(args...)
	if err != nil {
		return nil, "", err
	}
	files := diff.Parse(out)
	untracked, err := r.untrackedFiles()
	if err != nil {
		return nil, "", err
	}
	files = append(files, untracked...)
	r.markGenerated(files, "")
	SortFiles(files)
	return files, mb, nil
}

// CommitChanges commits the files at paths, staged from the work tree as
// they are, with a message, and returns the new commit's short hash. Other
// changes staged in the index stay staged and out of the commit.
func (r *Repo) CommitChanges(message string, paths []string) (string, error) {
	if strings.TrimSpace(message) == "" {
		return "", errors.New("the commit message is empty")
	}
	if len(paths) == 0 {
		return "", errors.New("no files to commit")
	}
	pathspecs := func() io.Reader {
		var b bytes.Buffer
		for _, p := range paths {
			b.WriteString(p)
			b.WriteByte(0)
		}
		return &b
	}
	// Stage the files chosen, deleted ones included.
	if _, err := run(context.Background(), r.Root, pathspecs(), "--literal-pathspecs", "add", "-A", "--pathspec-from-file=-", "--pathspec-file-nul"); err != nil {
		return "", err
	}
	msg, err := os.CreateTemp("", "godiff-message-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(msg.Name())
	if _, err := msg.WriteString(message); err != nil {
		msg.Close()
		return "", err
	}
	msg.Close()
	if _, err := run(context.Background(), r.Root, pathspecs(), "--literal-pathspecs", "commit", "-F", msg.Name(), "--pathspec-from-file=-", "--pathspec-file-nul"); err != nil {
		return "", err
	}
	return r.gitString("rev-parse", "--short", "HEAD"), nil
}

// Contents reads files of revisions in one git process: each spec is
// "rev:path", or ":path" for the index. Missing files are nil.
type Contents struct {
	mu     sync.Mutex
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
}

// NewContents starts git cat-file for the repository.
func (r *Repo) NewContents() (*Contents, error) {
	cmd := exec.Command("git", "cat-file", "--batch")
	cmd.Dir = r.Root
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &Contents{cmd: cmd, stdin: stdin, stdout: bufio.NewReaderSize(stdout, 64*1024)}, nil
}

// Read returns the content of rev:path, nil when there is none.
func (c *Contents) Read(rev, path string) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if strings.ContainsAny(path, "\n") {
		return nil, nil
	}
	if _, err := fmt.Fprintf(c.stdin, "%s:%s\n", rev, path); err != nil {
		return nil, err
	}
	header, err := c.stdout.ReadString('\n')
	if err != nil {
		return nil, err
	}
	fields := strings.Fields(header)
	if len(fields) < 3 || fields[len(fields)-1] == "missing" {
		return nil, nil
	}
	size, err := strconv.Atoi(fields[2])
	if err != nil {
		return nil, fmt.Errorf("cat-file: %q", header)
	}
	data := make([]byte, size+1) // and the newline after it
	if _, err := io.ReadFull(c.stdout, data); err != nil {
		return nil, err
	}
	return data[:size], nil
}

// Close stops git cat-file.
func (c *Contents) Close() error {
	c.stdin.Close()
	return c.cmd.Wait()
}

// ReadWorkTree reads a file of the work tree, nil when there is none.
func (r *Repo) ReadWorkTree(path string) []byte {
	data, err := os.ReadFile(filepath.Join(r.Root, filepath.FromSlash(path)))
	if err != nil {
		return nil
	}
	return data
}
