package main

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/egoist/godiff/internal/diff"
	"github.com/egoist/godiff/internal/git"
	"github.com/egoist/godiff/internal/github"
	"github.com/egoist/mygo"
)

// ghRemote is the remote of a repository on GitHub.
type ghRemote struct {
	repo   github.Repo
	remote string // the remote's name, as origin
}

func (g ghRemote) ok() bool { return !g.repo.IsZero() }

// findGitHubRemote returns the remote of want on GitHub, or, for a zero
// want, the remote whose pull requests the repository's are: upstream, as
// forks name the repository they come from, else origin, else the first.
func findGitHubRemote(repo *git.Repo, want github.Repo) (ghRemote, bool) {
	var found []ghRemote
	for _, r := range repo.Remotes() {
		gr, ok := github.ParseRemote(r.URL)
		if !ok {
			continue
		}
		if !want.IsZero() {
			if strings.EqualFold(gr.String(), want.String()) {
				return ghRemote{repo: gr, remote: r.Name}, true
			}
			continue
		}
		found = append(found, ghRemote{repo: gr, remote: r.Name})
	}
	for _, name := range []string{"upstream", "origin"} {
		for _, f := range found {
			if f.remote == name {
				return f, true
			}
		}
	}
	if len(found) > 0 {
		return found[0], true
	}
	return ghRemote{}, false
}

// pullSource is the source of a pull request.
func pullSource(repo github.Repo, n int) source {
	return source{kind: sourcePull, ref: fmt.Sprintf("%s#%d", repo, n)}
}

// pull returns the repository and the number of a pull request's source.
func (s source) pull() (github.Repo, int) {
	repo, n, _ := github.ParsePull(s.ref)
	return repo, n
}

// resolvePull names a pull request of a repository fully, as
// owner/repo#123: one named #123 alone is of the repository's own remote
// on GitHub. The repository must have a remote of the pull request's.
func resolvePull(repo *git.Repo, ref string) (string, error) {
	gh, n, ok := github.ParsePull(ref)
	if !ok {
		if v, err := strconv.Atoi(strings.TrimPrefix(ref, "#")); err == nil && v > 0 {
			n, ok = v, true
		}
	}
	if !ok {
		return "", fmt.Errorf("%q names no pull request: use 123, owner/repo#123 or its URL", ref)
	}
	r, found := findGitHubRemote(repo, gh)
	if !found {
		if gh.IsZero() {
			return "", fmt.Errorf("%s has no remote on GitHub", repo.Root)
		}
		return "", fmt.Errorf("%s has no remote of %s: open a clone of it to review its pull requests", repo.Root, gh)
	}
	return pullSource(r.repo, n).ref, nil
}

// viewedKey is what the files viewed of a source are remembered by: the
// repository's for its work tree, a pull request's own, none for commits,
// which do not change.
func viewedKey(root string, src source) string {
	switch src.kind {
	case sourceCommit:
		return ""
	case sourcePull:
		return root + "#" + src.ref
	}
	return root
}

// pullState is the review of a pull request.
type pullState struct {
	repo   github.Repo
	number int
	meta   *github.PullRequest
	head   string // the head commit reviewed

	reviews        *github.Reviews
	reviewsLoading bool
	reviewsErr     error
	reviewsAt      time.Time
	// viewer is the login of the token, "" without one.
	viewer string
	// readOnly says why nothing can be written, "" when it can.
	readOnly string
	// checkedAt is when GitHub was last asked for new commits.
	checkedAt time.Time

	descOpen bool
	// Drafts of replies, the comment edited, and threads shown open,
	// resolved or outdated as they are.
	replies  map[int64]*string
	editing  int64
	editText string
	opened   map[int64]bool
	// busy counts the writes in flight; writeErr is the last one's error.
	busy     int
	writeErr string

	// The review dialog.
	submitOpen  bool
	submitBody  string
	submitEvent string
	submitBusy  bool
	submitErr   string
}

// pullFor returns the review of a pull request's source, made the first
// time.
func (w *window) pullFor(src source) *pullState {
	if p := w.prs[src]; p != nil {
		return p
	}
	repo, n := src.pull()
	p := &pullState{repo: repo, number: n, replies: map[int64]*string{}, opened: map[int64]bool{}}
	if w.prs == nil {
		w.prs = map[source]*pullState{}
	}
	w.prs[src] = p
	return p
}

// pullRemote returns the remote a pull request's commits are fetched
// from.
func (w *window) pullRemote(src source) string {
	if src.kind != sourcePull {
		return ""
	}
	repo, _ := src.pull()
	if w.gh.ok() && strings.EqualFold(w.gh.repo.String(), repo.String()) {
		return w.gh.remote
	}
	if r, ok := findGitHubRemote(w.repo, repo); ok {
		return r.remote
	}
	return repo.URL() + ".git"
}

// pulledRequest is a pull request read from GitHub, its commits fetched.
type pulledRequest struct {
	meta      *github.PullRequest
	head      string
	mergeBase string
	files     []*diff.File
	commits   []string
}

// fetchPull reads a pull request, fetches its commits when the repository
// lacks them, and reads its changes against where it branched, as GitHub
// shows them. It runs off the main thread.
func (w *window) fetchPull(gen int, src source, remote string, opts git.Options) (pulledRequest, error) {
	var p pulledRequest
	repo, n := src.pull()
	status := func(s string) {
		w.update(func() {
			if gen == w.gen {
				w.loadStatus = s
			}
		})
	}
	ctx := context.Background()
	status(fmt.Sprintf("Reading #%d from GitHub…", n))
	meta, err := ghClient().Pull(ctx, repo, n)
	if err != nil {
		return p, err
	}
	p.meta, p.head = meta, meta.Head.SHA
	base := meta.Base.SHA
	var refspecs []string
	if !w.repo.HasCommit(p.head) {
		refspecs = append(refspecs, fmt.Sprintf("refs/pull/%d/head", n))
	}
	if !w.repo.HasCommit(base) {
		refspecs = append(refspecs, "refs/heads/"+meta.Base.Ref)
	}
	if len(refspecs) > 0 && !w.stale(gen) {
		status(fmt.Sprintf("Fetching #%d from %s…", n, remote))
		ferr := w.repo.Fetch(ctx, remote, refspecs...)
		if ferr != nil && remote != repo.URL()+".git" {
			// SSH may want a passphrase: GitHub serves public
			// repositories over HTTPS without one.
			if w.repo.Fetch(ctx, repo.URL()+".git", refspecs...) == nil {
				ferr = nil
			}
		}
		if !w.repo.HasCommit(p.head) {
			if ferr == nil {
				ferr = errors.New("GitHub did not send them")
			}
			return p, fmt.Errorf("could not fetch the commits of #%d from %s: %s", n, remote, errorText(ferr))
		}
		if !w.repo.HasCommit(base) {
			// The base branch moved on, its old head gone with a force
			// push: diff against where it is now.
			if r, err := w.repo.Resolve("FETCH_HEAD"); err == nil {
				base = r
			}
		}
	}
	status(fmt.Sprintf("Reading the changes of #%d…", n))
	p.files, p.mergeBase, err = w.repo.Range(base, p.head, opts)
	if err != nil {
		return p, err
	}
	p.commits = w.repo.Subjects(p.mergeBase, p.head, 250)
	return p, nil
}

// apply takes a pull request read from GitHub.
func (p *pullState) apply(r pulledRequest) {
	p.meta = r.meta
	p.head = r.head
	p.checkedAt = time.Now()
}

// loadReviews reads the review threads of the pull request shown, and who
// the token is.
func (w *window) loadReviews() {
	p := w.pr
	if p == nil || p.reviewsLoading {
		return
	}
	p.reviewsLoading = true
	needViewer := p.viewer == "" && p.readOnly == ""
	w.background(func() {
		client := ghClient()
		ctx := context.Background()
		reviews, err := client.Reviews(ctx, p.repo, p.number)
		viewer, readOnly := "", ""
		if needViewer {
			if client.Token == "" {
				readOnly = "Sign in with the GitHub CLI (gh auth login), or set githubToken in the config file, to comment and review."
			} else if u, err := client.Viewer(ctx); err == nil {
				viewer = u.Login
			}
		}
		w.update(func() {
			p.reviewsLoading = false
			p.reviewsAt = time.Now()
			p.reviewsErr = err
			if err == nil {
				p.reviews = reviews
				if p.submitBody == "" && reviews.Pending != nil {
					p.submitBody = reviews.Pending.Body
				}
			}
			if viewer != "" {
				p.viewer = viewer
			}
			if readOnly != "" {
				p.readOnly = readOnly
			}
			if w.pr == p {
				w.rowsDirty = true
			}
		})
	})
}

// canWrite reports whether the pull request can be commented on: once
// GitHub said whose the token is, and refused none of its writes.
func (p *pullState) canWrite() bool {
	return p != nil && p.readOnly == "" && p.viewer != "" && p.meta != nil
}

// write runs a change on GitHub, then reads the threads again.
func (w *window) write(p *pullState, fn func(ctx context.Context, c *github.Client) error, done func(err error)) {
	p.busy++
	p.writeErr = ""
	w.background(func() {
		err := fn(context.Background(), ghClient())
		w.update(func() {
			p.busy--
			if err != nil {
				p.writeErr = errorText(err)
				if github.Forbidden(err) {
					p.readOnly = "GitHub refused to let this token write to " + p.repo.String() + "."
				}
			}
			if done != nil {
				done(err)
			}
			if err == nil {
				p.reviewsLoading = false
				w.loadReviews()
			}
		})
	})
}

// postComment posts a review comment of the pull request shown: alone, or
// into the viewer's pending review.
func (w *window) postComment(cm *comment, review bool) {
	p := w.pr
	if p == nil || !p.canWrite() || !cm.pending() || cm.posting {
		return
	}
	nc := github.NewComment{Body: strings.TrimSpace(cm.text), CommitID: p.head, Path: cm.path, Side: github.Right, Line: cm.line}
	if cm.side == sideOld {
		nc.Side = github.Left
	}
	if cm.start != 0 && cm.start != cm.line {
		nc.StartLine, nc.Line = min(cm.start, cm.line), max(cm.start, cm.line)
	}
	var pending *github.PendingReview
	if p.reviews != nil {
		pending = p.reviews.Pending
	}
	cm.posting = true
	cm.postErr = ""
	w.write(p, func(ctx context.Context, c *github.Client) error {
		if review {
			return c.AddToReview(ctx, p.repo, p.number, pending, nc)
		}
		return c.Comment(ctx, p.repo, p.number, nc)
	}, func(err error) {
		cm.posting = false
		if err != nil {
			cm.postErr = errorText(err)
			return
		}
		w.deleteComment(cm)
	})
}

// checkPull asks GitHub whether the pull request shown has new commits,
// now and then: it runs off the main thread.
func (w *window) checkPull(src source) {
	var p *pullState
	var due bool
	mygo.RunOnMain(func() {
		p = w.pr
		if p == nil || w.source != src || p.meta == nil {
			return
		}
		// Without a token, GitHub allows 60 requests an hour.
		every := time.Minute
		if githubTokenKnown() == "" {
			every = 5 * time.Minute
		}
		due = time.Since(p.checkedAt) > every
		if due {
			p.checkedAt = time.Now()
		}
	})
	if !due {
		return
	}
	meta, err := ghClient().Pull(context.Background(), p.repo, p.number)
	if err != nil {
		return
	}
	w.update(func() {
		if w.pr != p || w.source != src {
			return
		}
		if meta.Head.SHA != p.head && !w.loading {
			w.changed = true
		}
		p.meta.Title, p.meta.Body, p.meta.State, p.meta.Draft, p.meta.Merged = meta.Title, meta.Body, meta.State, meta.Draft, meta.Merged
		if time.Since(p.reviewsAt) > 30*time.Second {
			w.loadReviews()
		}
	})
}

// loadPulls lists the open pull requests of the repository's remote on
// GitHub.
func (w *window) loadPulls() {
	if !w.gh.ok() || w.pullsLoading {
		return
	}
	w.pullsLoading = true
	repo := w.gh.repo
	w.background(func() {
		pulls, err := ghClient().OpenPulls(context.Background(), repo)
		w.update(func() {
			w.pullsLoading = false
			w.pullsLoaded = true
			w.pullsErr = err
			if err == nil {
				w.pulls = pulls
			}
		})
	})
}

var token struct {
	sync.Mutex
	value string
	at    time.Time
}

// githubToken returns the token for GitHub: the settings', else the
// environment's or the GitHub CLI's, found again every few minutes, as the
// user may sign in meanwhile. It runs gh: off the main thread.
func githubToken() string {
	if t := strings.TrimSpace(cfg.Get().GithubToken); t != "" {
		return t
	}
	token.Lock()
	defer token.Unlock()
	if token.at.IsZero() || time.Since(token.at) > 5*time.Minute {
		token.value, token.at = github.FindToken(), time.Now()
	}
	return token.value
}

// githubTokenKnown is the token as last found, without looking for it.
func githubTokenKnown() string {
	if t := strings.TrimSpace(cfg.Get().GithubToken); t != "" {
		return t
	}
	token.Lock()
	defer token.Unlock()
	return token.value
}

func ghClient() *github.Client { return &github.Client{Token: githubToken()} }
