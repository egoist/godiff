package github

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestParseRemote(t *testing.T) {
	for _, tc := range []struct {
		url  string
		want string
	}{
		{"git@github.com:egoist/godiff.git", "egoist/godiff"},
		{"https://github.com/egoist/godiff", "egoist/godiff"},
		{"https://github.com/egoist/godiff.git", "egoist/godiff"},
		{"https://token@github.com/a-b/c.d.git", "a-b/c.d"},
		{"ssh://git@github.com/egoist/godiff.git", "egoist/godiff"},
		{"git@gitlab.com:egoist/godiff.git", ""},
		{"/srv/repos/app.git", ""},
	} {
		r, ok := ParseRemote(tc.url)
		got := ""
		if ok {
			got = r.String()
		}
		if got != tc.want {
			t.Errorf("%s: %q, want %q", tc.url, got, tc.want)
		}
	}
}

func TestParsePull(t *testing.T) {
	for _, tc := range []struct {
		in   string
		repo string
		n    int
	}{
		{"https://github.com/egoist/godiff/pull/12", "egoist/godiff", 12},
		{"https://github.com/egoist/godiff/pull/12/files", "egoist/godiff", 12},
		{"github.com/egoist/godiff/pull/3", "egoist/godiff", 3},
		{"egoist/godiff#7", "egoist/godiff", 7},
		{"#42", "/", 42},
	} {
		repo, n, ok := ParsePull(tc.in)
		if !ok || repo.String() != tc.repo || n != tc.n {
			t.Errorf("%s: %v %d %v", tc.in, repo, n, ok)
		}
	}
	for _, bad := range []string{"main", "HEAD~1", "12", "#0", "https://github.com/a/b/issues/1"} {
		if _, _, ok := ParsePull(bad); ok {
			t.Errorf("%s parsed", bad)
		}
	}
}

func intp(n int) *int { return &n }

func TestNormalize(t *testing.T) {
	ada, bo := &User{Login: "ada"}, &User{Login: "bo"}
	comments := []reviewCommentJSON{
		{ID: 3, InReplyToID: 1, Path: "a.go", Side: Right, Line: intp(4), User: bo, Body: "Agreed"},
		{ID: 1, Path: "a.go", Side: Right, Line: intp(4), StartLine: intp(2), User: ada, Body: "Rename this"},
		{ID: 2, Path: "b.go", Side: Left, Line: nil, OriginalLine: intp(9), User: ada, Body: "Gone"},
	}
	reviews := []reviewJSON{
		{ID: 10, State: "APPROVED", User: bo, Body: "LGTM"},
		{ID: 11, State: "COMMENTED", User: ada, Body: ""},
		{ID: 12, State: "PENDING", NodeID: "PRR_x", User: ada},
	}
	pending := []reviewCommentJSON{{ID: 20, Path: "a.go", Side: Right, Line: intp(8), User: ada, Body: "Draft"}}
	res := map[int64]resolution{1: {nodeID: "T_1", resolved: true}}
	r := normalize(comments, reviews, pending, res)
	if len(r.Threads) != 3 {
		t.Fatalf("threads %+v", r.Threads)
	}
	first := r.Threads[0]
	if first.RootID != 1 || len(first.Comments) != 2 || first.Comments[1].Body != "Agreed" || first.StartLine != 2 || !first.Resolved || first.NodeID != "T_1" {
		t.Errorf("first thread %+v", first)
	}
	if !r.Threads[1].Outdated || r.Threads[1].OriginalLine != 9 || r.Threads[1].Side != Left {
		t.Errorf("outdated thread %+v", r.Threads[1])
	}
	if !r.Threads[2].Pending || r.PendingComments() != 1 {
		t.Errorf("pending thread %+v", r.Threads[2])
	}
	if r.Pending == nil || r.Pending.ID != 12 || r.Pending.NodeID != "PRR_x" {
		t.Errorf("pending review %+v", r.Pending)
	}
	if len(r.Summaries) != 1 || r.Summaries[0].State != "approved" || r.Summaries[0].Author.Login != "bo" {
		t.Errorf("summaries %+v", r.Summaries)
	}
}

// fakeGitHub answers as GitHub's API does, and records the writes.
type fakeGitHub struct {
	mu     sync.Mutex
	writes []string
}

func (f *fakeGitHub) serve(t *testing.T) *httptest.Server {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		// Writes, which GraphQL's queries are not.
		if r.Method != http.MethodGet && (r.URL.Path != "/graphql" || strings.Contains(string(body), "mutation")) {
			f.mu.Lock()
			f.writes = append(f.writes, r.Method+" "+r.URL.Path+" "+string(body))
			f.mu.Unlock()
		}
		switch {
		case r.URL.Path == "/repos/acme/app/pulls/1":
			fmt.Fprint(w, `{"number":1,"title":"Greet","state":"open","user":{"login":"ada"},"base":{"ref":"main","sha":"b"},"head":{"ref":"greet","sha":"h"},"changed_files":2}`)
		case r.URL.Path == "/repos/acme/app/pulls/1/comments" && r.URL.Query().Get("page") == "":
			w.Header().Set("Link", fmt.Sprintf(`<%s/repos/acme/app/pulls/1/comments?page=2&per_page=100>; rel="next"`, "http://"+r.Host))
			fmt.Fprint(w, `[{"id":1,"path":"a.go","side":"RIGHT","line":3,"user":{"login":"bo"},"body":"Why?"}]`)
		case r.URL.Path == "/repos/acme/app/pulls/1/comments":
			fmt.Fprint(w, `[{"id":2,"in_reply_to_id":1,"path":"a.go","side":"RIGHT","line":3,"user":{"login":"ada"},"body":"Because."}]`)
		case r.URL.Path == "/repos/acme/app/pulls/1/reviews" && r.Method == http.MethodGet:
			fmt.Fprint(w, `[{"id":7,"node_id":"PRR_7","state":"PENDING","user":{"login":"ada"}}]`)
		case r.URL.Path == "/repos/acme/app/pulls/1/reviews/7/comments":
			fmt.Fprint(w, `[{"id":5,"path":"b.go","side":"RIGHT","line":1,"user":{"login":"ada"},"body":"Draft"}]`)
		case r.URL.Path == "/graphql":
			if strings.Contains(string(body), "reviewThreads") {
				fmt.Fprint(w, `{"data":{"repository":{"pullRequest":{"reviewThreads":{"pageInfo":{"hasNextPage":false},"nodes":[{"id":"T1","isResolved":false,"comments":{"nodes":[{"fullDatabaseId":"1"}]}}]}}}}}`)
				return
			}
			if strings.Contains(string(body), "search(") {
				fmt.Fprint(w, `{"data":{"search":{"nodes":[{"number":4,"title":"Add","isDraft":true,"author":{"login":"cy"},"reviewDecision":"APPROVED","commits":{"nodes":[{"commit":{"statusCheckRollup":{"state":"FAILURE"}}}]},"additions":3},{}]}}}`)
				return
			}
			fmt.Fprint(w, `{"data":{}}`)
		case r.URL.Path == "/repos/acme/app/pulls" && r.Method == http.MethodGet:
			fmt.Fprint(w, `[{"number":4,"title":"Add","draft":false,"user":{"login":"cy"},"head":{"ref":"add"}}]`)
		case r.Method != http.MethodGet:
			fmt.Fprint(w, `{}`)
		default:
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"message":"Not Found"}`)
		}
	}))
	t.Cleanup(s.Close)
	return s
}

func withAPI(t *testing.T, url string) {
	old := API
	API = url
	t.Cleanup(func() { API = old })
}

func TestClient(t *testing.T) {
	f := &fakeGitHub{}
	withAPI(t, f.serve(t).URL)
	ctx := context.Background()
	repo := Repo{"acme", "app"}
	c := &Client{Token: "t"}

	p, err := c.Pull(ctx, repo, 1)
	if err != nil || p.Title != "Greet" || p.Head.SHA != "h" || p.Status() != "Open" {
		t.Fatalf("pull %+v %v", p, err)
	}
	r, err := c.Reviews(ctx, repo, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Threads) != 2 || len(r.Threads[0].Comments) != 2 || r.Threads[0].NodeID != "T1" || !r.Threads[1].Pending || r.Pending == nil {
		t.Fatalf("reviews %+v", r)
	}

	pulls, err := c.OpenPulls(ctx, repo)
	if err != nil || len(pulls) != 1 || !pulls[0].Draft || pulls[0].ReviewDecision != "approved" || pulls[0].Checks != "failure" {
		t.Fatalf("graphql pulls %+v %v", pulls, err)
	}
	pulls, err = (&Client{}).OpenPulls(ctx, repo)
	if err != nil || len(pulls) != 1 || pulls[0].HeadRef != "add" {
		t.Fatalf("rest pulls %+v %v", pulls, err)
	}

	nc := NewComment{Body: "Hm", CommitID: "h", Path: "a.go", Side: Right, Line: 5, StartLine: 3}
	if err := c.AddToReview(ctx, repo, 1, nil, nc); err != nil {
		t.Fatal(err)
	}
	if err := c.AddToReview(ctx, repo, 1, r.Pending, nc); err != nil {
		t.Fatal(err)
	}
	if err := c.SubmitReview(ctx, repo, 1, r.Pending, VerdictApprove, "Ship it"); err != nil {
		t.Fatal(err)
	}
	if err := c.Reply(ctx, repo, 1, 1, "Sure"); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	writes := f.writes
	f.mu.Unlock()
	if len(writes) != 4 {
		t.Fatalf("writes %q", writes)
	}
	var started struct {
		CommitID string           `json:"commit_id"`
		Comments []map[string]any `json:"comments"`
	}
	json.Unmarshal([]byte(strings.SplitN(writes[0], " ", 3)[2]), &started)
	if !strings.HasPrefix(writes[0], "POST /repos/acme/app/pulls/1/reviews ") || started.CommitID != "h" || started.Comments[0]["start_line"] != float64(3) {
		t.Errorf("start review: %s", writes[0])
	}
	if !strings.HasPrefix(writes[1], "POST /graphql ") || !strings.Contains(writes[1], `"reviewId":"PRR_7"`) {
		t.Errorf("add to review: %s", writes[1])
	}
	if !strings.HasPrefix(writes[2], "POST /repos/acme/app/pulls/1/reviews/7/events ") || !strings.Contains(writes[2], `"event":"APPROVE"`) {
		t.Errorf("submit: %s", writes[2])
	}
	if !strings.HasPrefix(writes[3], "POST /repos/acme/app/pulls/1/comments/1/replies ") {
		t.Errorf("reply: %s", writes[3])
	}

	_, err = c.Pull(ctx, repo, 404)
	if err == nil || !strings.Contains(err.Error(), "Not found") {
		t.Errorf("missing pull: %v", err)
	}
}
