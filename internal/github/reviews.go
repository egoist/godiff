package github

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"time"
)

// Side is the side of a diff a review comment is on: LEFT for the old
// file's lines, RIGHT for the new file's.
type Side string

const (
	Left  Side = "LEFT"
	Right Side = "RIGHT"
)

// Thread is a review comment and its replies, on a line or a range of
// lines of a file.
type Thread struct {
	RootID int64
	// NodeID identifies the thread in GraphQL, to resolve it; "" when not
	// known, without a token.
	NodeID       string
	Path         string
	Side         Side
	Line         int // the last line; 0 once outdated
	StartLine    int // the first line of a range, else 0
	OriginalLine int
	// Outdated is set once the lines it was on changed.
	Outdated bool
	Resolved bool
	// Pending is a draft of the viewer's review, which only they see.
	Pending  bool
	Comments []*Comment
}

// Comment is a comment of a thread.
type Comment struct {
	ID        int64
	Author    User
	Body      string
	CreatedAt time.Time
	URL       string
	Pending   bool
}

// ReviewSummary is a submitted review with a verdict or a body.
type ReviewSummary struct {
	ID          int64
	Author      User
	State       string // approved, changes_requested, commented or dismissed
	Body        string
	SubmittedAt time.Time
	URL         string
}

// PendingReview is the viewer's review not yet submitted.
type PendingReview struct {
	ID     int64
	NodeID string
	Body   string
}

// Reviews are the review threads and the reviews of a pull request.
type Reviews struct {
	Threads   []*Thread
	Summaries []ReviewSummary
	Pending   *PendingReview
}

// Comments counts the comments of the threads.
func (r *Reviews) Comments() int {
	n := 0
	for _, t := range r.Threads {
		n += len(t.Comments)
	}
	return n
}

// PendingComments counts the drafts of the viewer's pending review.
func (r *Reviews) PendingComments() int {
	n := 0
	for _, t := range r.Threads {
		if t.Pending {
			n += len(t.Comments)
		}
	}
	return n
}

type reviewCommentJSON struct {
	ID                  int64     `json:"id"`
	InReplyToID         int64     `json:"in_reply_to_id"`
	PullRequestReviewID int64     `json:"pull_request_review_id"`
	Path                string    `json:"path"`
	Side                Side      `json:"side"`
	Line                *int      `json:"line"`
	StartLine           *int      `json:"start_line"`
	OriginalLine        *int      `json:"original_line"`
	User                *User     `json:"user"`
	Body                string    `json:"body"`
	CreatedAt           time.Time `json:"created_at"`
	HTMLURL             string    `json:"html_url"`
}

type reviewJSON struct {
	ID          int64     `json:"id"`
	NodeID      string    `json:"node_id"`
	User        *User     `json:"user"`
	State       string    `json:"state"`
	Body        string    `json:"body"`
	SubmittedAt time.Time `json:"submitted_at"`
	HTMLURL     string    `json:"html_url"`
}

// resolution is a thread's state in GraphQL, which REST does not tell.
type resolution struct {
	nodeID   string
	resolved bool
}

// Reviews returns the review threads, the reviews and the viewer's pending
// review of a pull request.
func (c *Client) Reviews(ctx context.Context, repo Repo, number int) (*Reviews, error) {
	base := fmt.Sprintf("/repos/%s/pulls/%d", repo, number)
	comments, err := paginate[reviewCommentJSON](ctx, c, base+"/comments")
	if err != nil {
		return nil, err
	}
	reviews, err := paginate[reviewJSON](ctx, c, base+"/reviews")
	if err != nil {
		return nil, err
	}
	var pending []reviewCommentJSON
	for _, r := range reviews {
		if r.State == "PENDING" {
			if pending, err = paginate[reviewCommentJSON](ctx, c, fmt.Sprintf("%s/reviews/%d/comments", base, r.ID)); err != nil {
				return nil, err
			}
		}
	}
	var res map[int64]resolution
	if c.Token != "" {
		// Resolution is GraphQL's alone: unknown without it.
		res, _ = c.resolutions(ctx, repo, number)
	}
	return normalize(comments, reviews, pending, res), nil
}

// normalize threads the comments under their first by in_reply_to_id, and
// splits the reviews into the submitted and the viewer's pending one.
func normalize(comments []reviewCommentJSON, reviews []reviewJSON, pending []reviewCommentJSON, res map[int64]resolution) *Reviews {
	out := &Reviews{}
	threads := map[int64]*Thread{}
	seen := map[int64]bool{}
	add := func(c reviewCommentJSON, isPending bool) {
		if seen[c.ID] {
			return
		}
		seen[c.ID] = true
		root := c.ID
		if c.InReplyToID != 0 {
			root = c.InReplyToID
		}
		t := threads[root]
		if t == nil {
			t = &Thread{RootID: root, Path: c.Path, Side: c.Side, Pending: isPending}
			if t.Side == "" {
				t.Side = Right
			}
			if c.Line != nil {
				t.Line = *c.Line
			} else {
				t.Outdated = true
			}
			if c.StartLine != nil {
				t.StartLine = *c.StartLine
			}
			if c.OriginalLine != nil {
				t.OriginalLine = *c.OriginalLine
			}
			if r, ok := res[root]; ok {
				t.NodeID, t.Resolved = r.nodeID, r.resolved
			}
			threads[root] = t
			out.Threads = append(out.Threads, t)
		}
		cm := &Comment{ID: c.ID, Body: c.Body, CreatedAt: c.CreatedAt, URL: c.HTMLURL, Pending: isPending}
		if c.User != nil {
			cm.Author = *c.User
		}
		t.Comments = append(t.Comments, cm)
	}
	// Roots come before their replies, as GitHub lists them by id.
	sort.SliceStable(comments, func(i, j int) bool { return comments[i].ID < comments[j].ID })
	for _, c := range comments {
		add(c, false)
	}
	for _, c := range pending {
		add(c, true)
	}
	for _, r := range reviews {
		if r.State == "PENDING" {
			out.Pending = &PendingReview{ID: r.ID, NodeID: r.NodeID, Body: r.Body}
			continue
		}
		// A comment review without a body wraps comments posted alone.
		if r.State == "COMMENTED" && r.Body == "" {
			continue
		}
		state := map[string]string{"APPROVED": "approved", "CHANGES_REQUESTED": "changes_requested", "COMMENTED": "commented", "DISMISSED": "dismissed"}[r.State]
		if state == "" {
			continue
		}
		s := ReviewSummary{ID: r.ID, State: state, Body: r.Body, SubmittedAt: r.SubmittedAt, URL: r.HTMLURL}
		if r.User != nil {
			s.Author = *r.User
		}
		out.Summaries = append(out.Summaries, s)
	}
	return out
}

const reviewThreadsQuery = `
query ReviewThreads($owner: String!, $repo: String!, $number: Int!, $cursor: String) {
  repository(owner: $owner, name: $repo) {
    pullRequest(number: $number) {
      reviewThreads(first: 100, after: $cursor) {
        pageInfo { hasNextPage endCursor }
        nodes { id isResolved comments(first: 1) { nodes { fullDatabaseId } } }
      }
    }
  }
}`

// resolutions returns whether each thread is resolved, by the id of its
// first comment.
func (c *Client) resolutions(ctx context.Context, repo Repo, number int) (map[int64]resolution, error) {
	out := map[int64]resolution{}
	var cursor any
	for {
		var data struct {
			Repository *struct {
				PullRequest *struct {
					ReviewThreads struct {
						PageInfo struct {
							HasNextPage bool   `json:"hasNextPage"`
							EndCursor   string `json:"endCursor"`
						} `json:"pageInfo"`
						Nodes []struct {
							ID         string `json:"id"`
							IsResolved bool   `json:"isResolved"`
							Comments   struct {
								Nodes []struct {
									FullDatabaseID string `json:"fullDatabaseId"`
								} `json:"nodes"`
							} `json:"comments"`
						} `json:"nodes"`
					} `json:"reviewThreads"`
				} `json:"pullRequest"`
			} `json:"repository"`
		}
		vars := map[string]any{"owner": repo.Owner, "repo": repo.Name, "number": number, "cursor": cursor}
		if err := c.graphql(ctx, reviewThreadsQuery, vars, &data); err != nil {
			return nil, err
		}
		if data.Repository == nil || data.Repository.PullRequest == nil {
			return out, nil
		}
		rt := data.Repository.PullRequest.ReviewThreads
		for _, n := range rt.Nodes {
			if len(n.Comments.Nodes) == 0 {
				continue
			}
			var id int64
			fmt.Sscan(n.Comments.Nodes[0].FullDatabaseID, &id)
			if id != 0 {
				out[id] = resolution{nodeID: n.ID, resolved: n.IsResolved}
			}
		}
		if !rt.PageInfo.HasNextPage {
			return out, nil
		}
		cursor = rt.PageInfo.EndCursor
	}
}

// NewComment is a review comment to post on a line, or on the lines
// StartLine…Line of a side.
type NewComment struct {
	Body      string
	CommitID  string // the head commit the lines are of
	Path      string
	Side      Side
	Line      int
	StartLine int
}

func (n NewComment) fields() map[string]any {
	m := map[string]any{"path": n.Path, "body": n.Body, "side": n.Side, "line": n.Line}
	if n.StartLine != 0 && n.StartLine != n.Line {
		m["start_line"] = n.StartLine
		m["start_side"] = n.Side
	}
	return m
}

// Comment posts a review comment at once, outside a review.
func (c *Client) Comment(ctx context.Context, repo Repo, number int, n NewComment) error {
	body := n.fields()
	body["commit_id"] = n.CommitID
	_, err := c.do(ctx, http.MethodPost, fmt.Sprintf("/repos/%s/pulls/%d/comments", repo, number), body, nil)
	return err
}

const addThreadMutation = `
mutation AddPendingThread($reviewId: ID!, $path: String!, $body: String!, $line: Int!, $side: DiffSide!, $startLine: Int, $startSide: DiffSide) {
  addPullRequestReviewThread(input: {pullRequestReviewId: $reviewId, path: $path, body: $body, line: $line, side: $side, startLine: $startLine, startSide: $startSide}) {
    thread { id }
  }
}`

// AddToReview adds a comment to the viewer's pending review, which it
// starts when there is none.
func (c *Client) AddToReview(ctx context.Context, repo Repo, number int, pending *PendingReview, n NewComment) error {
	if pending == nil {
		// REST attaches comments to a review as it starts only.
		body := map[string]any{"commit_id": n.CommitID, "comments": []any{n.fields()}}
		_, err := c.do(ctx, http.MethodPost, fmt.Sprintf("/repos/%s/pulls/%d/reviews", repo, number), body, nil)
		return err
	}
	vars := map[string]any{"reviewId": pending.NodeID, "path": n.Path, "body": n.Body, "line": n.Line, "side": n.Side, "startLine": nil, "startSide": nil}
	if n.StartLine != 0 && n.StartLine != n.Line {
		vars["startLine"], vars["startSide"] = n.StartLine, n.Side
	}
	return c.graphql(ctx, addThreadMutation, vars, nil)
}

// Reply answers a thread, at once.
func (c *Client) Reply(ctx context.Context, repo Repo, number int, root int64, body string) error {
	_, err := c.do(ctx, http.MethodPost, fmt.Sprintf("/repos/%s/pulls/%d/comments/%d/replies", repo, number, root), map[string]any{"body": body}, nil)
	return err
}

// EditComment changes the text of a review comment.
func (c *Client) EditComment(ctx context.Context, repo Repo, id int64, body string) error {
	_, err := c.do(ctx, http.MethodPatch, fmt.Sprintf("/repos/%s/pulls/comments/%d", repo, id), map[string]any{"body": body}, nil)
	return err
}

// DeleteComment deletes a review comment.
func (c *Client) DeleteComment(ctx context.Context, repo Repo, id int64) error {
	_, err := c.do(ctx, http.MethodDelete, fmt.Sprintf("/repos/%s/pulls/comments/%d", repo, id), nil, nil)
	return err
}

const resolveMutation = `mutation Resolve($id: ID!) { resolveReviewThread(input: {threadId: $id}) { thread { id } } }`
const unresolveMutation = `mutation Unresolve($id: ID!) { unresolveReviewThread(input: {threadId: $id}) { thread { id } } }`

// Resolve resolves a thread, or unresolves it.
func (c *Client) Resolve(ctx context.Context, nodeID string, resolved bool) error {
	q := resolveMutation
	if !resolved {
		q = unresolveMutation
	}
	return c.graphql(ctx, q, map[string]any{"id": nodeID}, nil)
}

// Verdicts of a review.
const (
	VerdictComment        = "COMMENT"
	VerdictApprove        = "APPROVE"
	VerdictRequestChanges = "REQUEST_CHANGES"
)

// SubmitReview submits the viewer's pending review with a verdict, or a
// review without comments when there is none pending.
func (c *Client) SubmitReview(ctx context.Context, repo Repo, number int, pending *PendingReview, event, body string) error {
	base := fmt.Sprintf("/repos/%s/pulls/%d/reviews", repo, number)
	if pending != nil {
		_, err := c.do(ctx, http.MethodPost, fmt.Sprintf("%s/%d/events", base, pending.ID), map[string]any{"event": event, "body": body}, nil)
		return err
	}
	_, err := c.do(ctx, http.MethodPost, base, map[string]any{"event": event, "body": body}, nil)
	return err
}

// DiscardReview deletes the viewer's pending review and its drafts.
func (c *Client) DiscardReview(ctx context.Context, repo Repo, number int, pending *PendingReview) error {
	_, err := c.do(ctx, http.MethodDelete, fmt.Sprintf("/repos/%s/pulls/%d/reviews/%d", repo, number, pending.ID), nil, nil)
	return err
}
