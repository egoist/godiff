package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/godiff/internal/agent"
	"github.com/egoist/godiff/internal/diff"
	"github.com/egoist/godiff/internal/git"
	"github.com/egoist/godiff/internal/github"
	"github.com/egoist/mygo/ui"
)

func TestKindOf(t *testing.T) {
	for path, want := range map[string]string{
		"README.md":                  "docs",
		"docs/guide/setup.ts":        "docs",
		".github/CONTRIBUTING.md":    "docs",
		"src/app.go":                 "code",
		"src/app_test.go":            "tests",
		"web/button.spec.tsx":        "tests",
		"tests/fixtures/a.json":      "tests",
		"vite.config.ts":             "config",
		".eslintrc.json":             "config",
		".github/workflows/ci.yml":   "config",
		"packages/a/tsconfig.json":   "config",
		"Dockerfile":                 "config",
		"go.mod":                     "deps",
		"packages/a/package.json":    "deps",
		"pnpm-lock.yaml":             "generated",
		"dist/app.js":                "generated",
		"api/types.generated.ts":     "generated",
		"packages/web/src/vite.ts":   "code",
		"packages/web/config.ts":     "code",
		"packages/web/src/.eslintrc": "code",
	} {
		f := &diff.File{Path: path, Generated: git.LooksGenerated(path)}
		if got := kinds[kindOf(f)].key; got != want {
			t.Errorf("%s: %s, want %s", path, got, want)
		}
	}
	if got := kinds[kindOf(&diff.File{Path: "logo.png", Binary: true})].key; got != "other" {
		t.Errorf("binary: %s", got)
	}
}

// fakeGitHub answers as GitHub's API does for acme/app#1, and records
// the writes.
type fakeGitHub struct {
	mu       sync.Mutex
	base     string
	head     string
	comments string
	writes   []string
}

func (f *fakeGitHub) serve(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.Method != http.MethodGet && (r.URL.Path != "/graphql" || strings.Contains(string(body), "mutation")) {
			f.writes = append(f.writes, r.Method+" "+r.URL.Path+" "+string(body))
			fmt.Fprint(w, `{}`)
			return
		}
		switch r.URL.Path {
		case "/repos/acme/app/pulls/1":
			fmt.Fprintf(w, `{"number":1,"title":"Greet louder","body":"<!-- template -->\nMakes the greeting shorter.","state":"open","user":{"login":"bo"},
				"html_url":"https://github.com/acme/app/pull/1","base":{"ref":"main","sha":%q,"label":"acme:main"},
				"head":{"ref":"greet","sha":%q,"label":"bo:greet"},"additions":3,"deletions":1,"changed_files":2,"commits":1}`, f.base, f.head)
		case "/repos/acme/app/pulls":
			fmt.Fprint(w, `[{"number":1,"title":"Greet louder","user":{"login":"bo"},"head":{"ref":"greet"}}]`)
		case "/repos/acme/app/pulls/1/comments":
			fmt.Fprint(w, f.comments)
		case "/repos/acme/app/pulls/1/reviews":
			fmt.Fprint(w, `[{"id":9,"state":"CHANGES_REQUESTED","user":{"login":"cy"},"body":"Not yet."}]`)
		case "/user":
			fmt.Fprint(w, `{"login":"ada"}`)
		case "/graphql":
			if strings.Contains(string(body), "search(") {
				fmt.Fprint(w, `{"data":{"search":{"nodes":[{"number":1,"title":"Greet louder","author":{"login":"bo"},"headRefName":"greet","reviewDecision":"CHANGES_REQUESTED"}]}}}`)
				return
			}
			fmt.Fprint(w, `{"data":{"repository":{"pullRequest":{"reviewThreads":{"pageInfo":{"hasNextPage":false},"nodes":[{"id":"T1","isResolved":false,"comments":{"nodes":[{"fullDatabaseId":"1"}]}}]}}}}}`)
		default:
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"message":"Not Found"}`)
		}
	}))
	t.Cleanup(s.Close)
	old := github.API
	github.API = s.URL
	t.Cleanup(func() { github.API = old })
}

func (f *fakeGitHub) takeWrites() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	w := f.writes
	f.writes = nil
	return w
}

// pullRepo makes the repository of a pull request on GitHub, with the
// pull request's head as GitHub keeps it, and a clone of its main branch
// alone, which lacks it.
func pullRepo(t *testing.T) (clone, base, head string) {
	server := t.TempDir()
	gitIn(t, server, "init", "-q", "-b", "main")
	writeFile(t, server, "main.go", mainGo)
	writeFile(t, server, "README.md", "# App\n")
	gitIn(t, server, "add", ".")
	gitIn(t, server, "commit", "-q", "-m", "First commit")
	base = strings.TrimSpace(gitIn(t, server, "rev-parse", "HEAD"))
	gitIn(t, server, "checkout", "-q", "-b", "greet")
	writeFile(t, server, "main.go", strings.Replace(mainGo, `"Hello, " + name`, `"Hi, " + name + "!"`, 1))
	writeFile(t, server, "main_test.go", "package main\n\nimport \"testing\"\n\nfunc TestGreet(t *testing.T) {}\n")
	gitIn(t, server, "add", ".")
	gitIn(t, server, "commit", "-q", "-m", "Greet louder")
	head = strings.TrimSpace(gitIn(t, server, "rev-parse", "HEAD"))
	gitIn(t, server, "update-ref", "refs/pull/1/head", head)
	gitIn(t, server, "checkout", "-q", "main")
	gitIn(t, server, "branch", "-D", "greet")

	clone = filepath.Join(t.TempDir(), "app")
	// Through git's transport, which sends what main holds alone.
	gitIn(t, filepath.Dir(clone), "clone", "-q", "--single-branch", "--branch", "main", "file://"+server, clone)
	gitIn(t, clone, "remote", "set-url", "origin", server)
	return clone, base, head
}

// withSettings changes the settings for a test.
func withSettings(t *testing.T, fn func(s *Settings)) {
	old := cfg.Get()
	cfg.Update(fn)
	t.Cleanup(func() { cfg.Update(func(s *Settings) { *s = old }) })
}

func TestPullRequest(t *testing.T) {
	clone, base, head := pullRepo(t)
	f := &fakeGitHub{base: base, head: head, comments: `[
		{"id":1,"path":"main.go","side":"RIGHT","line":6,"user":{"login":"cy"},"body":"Why the exclamation mark?","created_at":"2026-10-01T10:00:00Z"},
		{"id":2,"in_reply_to_id":1,"path":"main.go","side":"RIGHT","line":6,"user":{"login":"bo"},"body":"To greet louder.","created_at":"2026-10-01T11:00:00Z"},
		{"id":3,"path":"main.go","side":"RIGHT","line":null,"original_line":2,"user":{"login":"cy"},"body":"Old remark","created_at":"2026-09-01T11:00:00Z"}]`}
	f.serve(t)
	withSettings(t, func(s *Settings) { s.GithubToken = "t" })

	repo, err := git.Open(clone)
	if err != nil {
		t.Fatal(err)
	}
	if repo.HasCommit(head) {
		t.Fatal("the clone has the pull request's commit already")
	}
	w := newWindow(repo, source{})
	w.settings = cfg.Get()
	w.gh = ghRemote{repo: github.Repo{Owner: "acme", Name: "app"}, remote: "origin"}
	w.sidebarShown, w.sidebarWidth = true, sidebarDefault
	w.load()
	tt := ui.NewTester(w.view, 1280, 1400)
	tt.Frame()

	// The tab lists the pull requests.
	w.showPulls()
	tt.Frame()
	if !tt.HasText("Greet louder") {
		t.Fatalf("no pull request in %q", tt.Texts())
	}
	if err := tt.Click("Greet louder"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Frame()
	if w.source != pullSource(github.Repo{Owner: "acme", Name: "app"}, 1) || w.loadErr != nil {
		t.Fatalf("source %+v, error %v", w.source, w.loadErr)
	}
	if !repo.HasCommit(head) {
		t.Fatal("the pull request's commit was not fetched")
	}
	var paths []string
	for _, fs := range w.files {
		paths = append(paths, fs.Path)
	}
	// Grouped by kind: the code, then the tests.
	if !slices.Equal(paths, []string{"main.go", "main_test.go"}) || len(w.groups) != 2 || w.groups[0].label != "Code" || w.groups[1].label != "Tests" {
		t.Fatalf("files %v, groups %+v", paths, w.groups)
	}
	tt.Frame()
	snapshot(t, tt, "pull")
	for _, want := range []string{"Greet louder", "Makes the greeting shorter.", "Why the exclamation mark?", "To greet louder.", "cy requested changes", "Open", "bo:greet"} {
		if !tt.HasText(want) {
			t.Errorf("no %q in %q", want, tt.Texts())
		}
	}
	if tt.HasText("template") {
		t.Error("the description's HTML comment shows")
	}
	// The outdated thread shows folded at the end of the file.
	if !tt.HasText("Outdated") || tt.HasText("Old remark") && !tt.HasText("cy ") {
		t.Errorf("outdated thread: %q", tt.Texts())
	}

	// A reply posts at once.
	reply := w.pr.replies[1]
	if reply == nil {
		t.Fatal("no reply box")
	}
	*reply = "Fair enough."
	tt.Frame()
	if err := tt.Click("Reply"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	writes := f.takeWrites()
	if len(writes) != 1 || !strings.HasPrefix(writes[0], "POST /repos/acme/app/pulls/1/comments/1/replies ") || !strings.Contains(writes[0], "Fair enough.") {
		t.Fatalf("writes %q", writes)
	}

	// A comment on a line starts a review.
	w.addComment("main.go", sideNew, 6, 6)
	w.comments[len(w.comments)-1].text = "Use a constant."
	w.rowsDirty = true
	tt.Frame()
	if err := tt.Click("Start a Review"); err != nil {
		t.Fatalf("%v in %q", err, tt.Texts())
	}
	tt.Frame()
	writes = f.takeWrites()
	if len(writes) != 1 || !strings.HasPrefix(writes[0], "POST /repos/acme/app/pulls/1/reviews ") || !strings.Contains(writes[0], `"line":6`) || !strings.Contains(writes[0], head) {
		t.Fatalf("writes %q", writes)
	}
	if w.pendingComments() != 0 {
		t.Error("the comment posted stays")
	}

	// The review submits with a verdict.
	w.openSubmit()
	tt.Frame()
	if err := tt.Click("Approve"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	snapshot(t, tt, "pull-review")
	if err := tt.Click("Submit Review"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	writes = f.takeWrites()
	if len(writes) != 1 || !strings.HasPrefix(writes[0], "POST /repos/acme/app/pulls/1/reviews ") || !strings.Contains(writes[0], `"event":"APPROVE"`) {
		t.Fatalf("writes %q", writes)
	}
	if w.pr.submitOpen {
		t.Error("the review dialog stays open")
	}

	// Its files' viewed marks are the pull request's own.
	if key := viewedKey(w.repo.Root, w.source); key != w.repo.Root+"#acme/app#1" {
		t.Errorf("viewed key %q", key)
	}
}

func TestPullRequestReadOnly(t *testing.T) {
	clone, base, head := pullRepo(t)
	f := &fakeGitHub{base: base, head: head, comments: `[]`}
	f.serve(t)
	withSettings(t, func(s *Settings) { s.GithubToken = "" })
	t.Setenv("GODIFF_NO_SHELL_PATH", "1")
	t.Setenv("PATH", t.TempDir()) // no gh
	for _, k := range []string{"GODIFF_GITHUB_TOKEN", "GH_TOKEN", "GITHUB_TOKEN"} {
		t.Setenv(k, "")
	}
	token.Lock()
	token.at = time.Time{}
	token.Unlock()
	t.Cleanup(func() {
		token.Lock()
		token.at = time.Time{}
		token.Unlock()
	})

	repo, _ := git.Open(clone)
	w := newWindow(repo, pullSource(github.Repo{Owner: "acme", Name: "app"}, 1))
	w.settings = cfg.Get()
	w.gh = ghRemote{repo: github.Repo{Owner: "acme", Name: "app"}, remote: "origin"}
	w.load()
	tt := ui.NewTester(w.view, 1280, 860)
	tt.Frame()
	if w.loadErr != nil || len(w.files) != 2 {
		t.Fatalf("error %v, %d files", w.loadErr, len(w.files))
	}
	if w.pr.canWrite() || !tt.HasText(w.pr.readOnly) {
		t.Errorf("read only %q: %q", w.pr.readOnly, tt.Texts())
	}
}

func TestAIReview(t *testing.T) {
	w, tt := newTestWindow(t, testRepo(t))
	old := runAgent
	t.Cleanup(func() { runAgent = old })
	var asked agent.Request
	runAgent = func(ctx context.Context, a agent.Agent, req agent.Request, progress func(string)) (*agent.Analysis, error) {
		asked = req
		progress("Reading main.go")
		return &agent.Analysis{
			OverallSummary: "Greets louder and tidies the docs.",
			Agent:          a.Name,
			Model:          "test-model",
			GeneratedAt:    time.Now(),
			Groups: []agent.Group{
				{Key: "greeting", Label: "Greeting", Summary: "A shorter greeting.", Critical: true, FilePaths: []string{"main.go", "src/new.go"},
					LineNotes: []agent.LineNote{{Path: "main.go", Side: "additions", Line: 6, Text: "Callers may match on Hello."}}},
				{Key: "docs", Label: "Docs", Summary: "Wording.", FilePaths: []string{"docs/long.txt", "old.txt"},
					FileNotes: []agent.FileNote{{Path: "old.txt", Text: "Nothing reads it anymore."}}},
			},
		}, nil
	}
	withSettings(t, func(s *Settings) { s.AIAgent = "claude" })
	w.settings = cfg.Get()
	if w.grouping() != groupNone {
		t.Fatalf("grouping %d before the review", w.grouping())
	}
	// Choosing to group by a review there is not yet offers one, and runs
	// no agent.
	w.chooseGrouping(groupAI)
	tt.Frame()
	if asked.Dir != "" || w.analyses[w.source].running {
		t.Fatal("the agent ran unasked")
	}
	if !tt.HasText("Review with AI") {
		t.Fatalf("no offer in %q", tt.Texts())
	}
	if err := tt.Click("Review with AI"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !asked.Checkout || len(asked.Files) != 4 || asked.Dir != w.repo.Root {
		t.Errorf("request %+v", asked)
	}
	if w.grouping() != groupAI || len(w.groups) != 2 || w.groups[0].label != "Greeting" || !w.groups[0].critical {
		t.Fatalf("grouping %d, groups %+v", w.grouping(), w.groups)
	}
	// The group's files first, as the tree lists them.
	if w.files[0].Path != "src/new.go" || w.files[1].Path != "main.go" || w.files[0].group != 0 || w.files[2].group != 1 {
		t.Errorf("order %s, %s", w.files[0].Path, w.files[1].Path)
	}
	for _, want := range []string{"AI Review", "Greets louder and tidies the docs.", "Greeting", "A shorter greeting.", "Callers may match on Hello.", "1 to take care with"} {
		if !tt.HasText(want) {
			t.Errorf("no %q in %q", want, tt.Texts())
		}
	}
	snapshot(t, tt, "ai-review")
	w.revealFile(slices.IndexFunc(w.files, func(f *fileState) bool { return f.Path == "old.txt" }))
	tt.Frame()
	if !tt.HasText("Nothing reads it anymore.") {
		t.Errorf("no file note in %q", tt.Texts())
	}

	// By kind, and back.
	w.chooseGrouping(groupKind)
	tt.Frame()
	if w.grouping() != groupKind || w.groups[0].label != "Docs" {
		t.Errorf("by kind: %+v", w.groups)
	}
	w.chooseGrouping(groupNone)
	tt.Frame()
	if len(w.groups) != 0 || w.files[0].Path != "docs/long.txt" {
		t.Errorf("ungrouped: %d groups, first %s", len(w.groups), w.files[0].Path)
	}
	// A file that changes since loses its notes.
	w.chooseGrouping(groupAI)
	st := w.analyses[w.source]
	st.fingerprints["main.go"] = "changed"
	w.regroup()
	if len(w.notes["main.go"]) != 0 || len(w.notes["old.txt"]) != 1 {
		t.Errorf("notes %v", w.notes)
	}
}

func TestParsePullArgs(t *testing.T) {
	dir := testRepo(t)
	gitIn(t, dir, "remote", "add", "origin", "git@github.com:acme/app.git")
	gitIn(t, dir, "remote", "add", "upstream", "https://github.com/up/app.git")
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"acme/app#5"}, "acme/app#5"},
		{[]string{"https://github.com/acme/app/pull/7/files"}, "acme/app#7"},
		{[]string{"--pr", "12"}, "up/app#12"},
		{[]string{"#3"}, "up/app#3"},
	} {
		req, err := parseArgs(tc.args, dir)
		if err != nil {
			t.Errorf("%v: %v", tc.args, err)
			continue
		}
		if req.src != (source{kind: sourcePull, ref: tc.want}) {
			t.Errorf("%v: %+v", tc.args, req.src)
		}
	}
	if _, err := parseArgs([]string{"other/thing#1"}, dir); err == nil || !strings.Contains(err.Error(), "no remote of other/thing") {
		t.Errorf("another repository: %v", err)
	}
}

func TestAIModelMenu(t *testing.T) {
	w, tt := newTestWindow(t, testRepo(t))
	// The models OpenCode lists, as if it had listed them.
	modelsFound.Lock()
	modelsFound.byAgent = map[string]*agentModels{"opencode": {done: true, fetchedAt: time.Now(), def: "deepseek/deepseek-flash", models: []agent.Model{
		{ID: "deepseek/deepseek-flash", Label: "deepseek-flash", Provider: "deepseek"},
		{ID: "vercel/anthropic/claude-sonnet-4.6", Label: "anthropic/claude-sonnet-4.6", Provider: "vercel"},
		{ID: "vercel/bfl/flux-3-image", Label: "bfl/flux-3-image", Provider: "vercel"},
	}}}
	modelsFound.Unlock()
	t.Cleanup(func() {
		modelsFound.Lock()
		modelsFound.byAgent = nil
		modelsFound.Unlock()
	})
	withSettings(t, func(s *Settings) { s.AIAgent = "opencode" })
	w.settings = cfg.Get()
	w.chooseGrouping(groupAI)
	tt.Frame()
	if err := tt.Click("Default Model"); err != nil {
		t.Fatalf("%v in %q", err, tt.Texts())
	}
	if got, want := tt.Menu(), []string{"Default (deepseek/deepseek-flash)", "-", "deepseek", "vercel"}; !slices.Equal(got, want) {
		t.Fatalf("menu %q, want %q", got, want)
	}
	if err := tt.ChooseMenuItem("vercel", "anthropic", "claude-sonnet-4.6"); err != nil {
		t.Fatal(err)
	}
	if got := cfg.Get().AIModel; got != "vercel/anthropic/claude-sonnet-4.6" {
		t.Fatalf("model %q", got)
	}
	w.settings = cfg.Get()
	tt.Frame()
	if !tt.HasText("claude-sonnet-4.6") {
		t.Errorf("no model on the button in %q", tt.Texts())
	}
	// Choosing another agent forgets the model of the one before.
	if err := tt.Click("OpenCode"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("Pi"); err != nil {
		t.Fatal(err)
	}
	if got := cfg.Get().AIModel; got != "" {
		t.Errorf("model %q after choosing another agent", got)
	}
}
