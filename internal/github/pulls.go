package github

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// Branch is a side of a pull request: the branch, and its commit.
type Branch struct {
	Ref   string `json:"ref"`
	SHA   string `json:"sha"`
	Label string `json:"label"` // owner:branch
}

// PullRequest is a pull request, as its page shows it.
type PullRequest struct {
	Number       int        `json:"number"`
	Title        string     `json:"title"`
	Body         string     `json:"body"`
	State        string     `json:"state"` // open or closed
	Draft        bool       `json:"draft"`
	Merged       bool       `json:"merged"`
	User         User       `json:"user"`
	HTMLURL      string     `json:"html_url"`
	Base         Branch     `json:"base"`
	Head         Branch     `json:"head"`
	Additions    int        `json:"additions"`
	Deletions    int        `json:"deletions"`
	ChangedFiles int        `json:"changed_files"`
	Commits      int        `json:"commits"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	MergedAt     *time.Time `json:"merged_at"`
}

// Status is the pull request's state as its badge shows it: Open, Draft,
// Merged or Closed.
func (p *PullRequest) Status() string {
	switch {
	case p.Merged || p.MergedAt != nil:
		return "Merged"
	case p.State == "closed":
		return "Closed"
	case p.Draft:
		return "Draft"
	}
	return "Open"
}

// Pull returns a pull request.
func (c *Client) Pull(ctx context.Context, repo Repo, number int) (*PullRequest, error) {
	var p PullRequest
	if _, err := c.do(ctx, http.MethodGet, fmt.Sprintf("/repos/%s/pulls/%d", repo, number), nil, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// PullCommit is a commit of a pull request.
type PullCommit struct {
	SHA    string `json:"sha"`
	Commit struct {
		Message string `json:"message"`
	} `json:"commit"`
}

// PullCommits returns the commits of a pull request, oldest first.
func (c *Client) PullCommits(ctx context.Context, repo Repo, number int) ([]PullCommit, error) {
	return paginate[PullCommit](ctx, c, fmt.Sprintf("/repos/%s/pulls/%d/commits", repo, number))
}

// Summary is a pull request as a list shows it. Without a token, the
// review decision, the checks and the counts are unknown.
type Summary struct {
	Number         int
	Title          string
	URL            string
	Draft          bool
	Author         User
	HeadRef        string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	Comments       int
	ReviewDecision string // approved, changes_requested, review_required, or ""
	Checks         string // success, failure, pending, or ""
	Additions      int
	Deletions      int
	ChangedFiles   int
	Labels         []Label
}

// Label is a label of a pull request, with its color as hex.
type Label struct {
	Name  string
	Color string
}

// OpenPulls returns the open pull requests of a repository, the recently
// updated first: the first hundred.
func (c *Client) OpenPulls(ctx context.Context, repo Repo) ([]Summary, error) {
	if c.Token == "" {
		return c.openPullsREST(ctx, repo)
	}
	return c.openPullsGraphQL(ctx, repo)
}

func (c *Client) openPullsREST(ctx context.Context, repo Repo) ([]Summary, error) {
	var items []struct {
		Number    int       `json:"number"`
		Title     string    `json:"title"`
		HTMLURL   string    `json:"html_url"`
		Draft     bool      `json:"draft"`
		User      User      `json:"user"`
		Head      Branch    `json:"head"`
		CreatedAt time.Time `json:"created_at"`
		UpdatedAt time.Time `json:"updated_at"`
		Labels    []struct {
			Name  string `json:"name"`
			Color string `json:"color"`
		} `json:"labels"`
	}
	path := fmt.Sprintf("/repos/%s/pulls?state=open&sort=updated&direction=desc&per_page=100", repo)
	if _, err := c.do(ctx, http.MethodGet, path, nil, &items); err != nil {
		return nil, err
	}
	out := make([]Summary, 0, len(items))
	for _, it := range items {
		s := Summary{Number: it.Number, Title: it.Title, URL: it.HTMLURL, Draft: it.Draft, Author: it.User,
			HeadRef: it.Head.Ref, CreatedAt: it.CreatedAt, UpdatedAt: it.UpdatedAt}
		for _, l := range it.Labels {
			s.Labels = append(s.Labels, Label{Name: l.Name, Color: l.Color})
		}
		out = append(out, s)
	}
	return out, nil
}

const openPullsQuery = `
query OpenPullRequests($query: String!) {
  search(query: $query, type: ISSUE, first: 100) {
    nodes {
      ... on PullRequest {
        number title url isDraft createdAt updatedAt headRefName
        author { login avatarUrl }
        labels(first: 10) { nodes { name color } }
        comments { totalCount }
        reviewDecision
        commits(last: 1) { nodes { commit { statusCheckRollup { state } } } }
        additions deletions changedFiles
      }
    }
  }
}`

func (c *Client) openPullsGraphQL(ctx context.Context, repo Repo) ([]Summary, error) {
	var data struct {
		Search struct {
			Nodes []struct {
				Number      int       `json:"number"`
				Title       string    `json:"title"`
				URL         string    `json:"url"`
				IsDraft     bool      `json:"isDraft"`
				CreatedAt   time.Time `json:"createdAt"`
				UpdatedAt   time.Time `json:"updatedAt"`
				HeadRefName string    `json:"headRefName"`
				Author      *struct {
					Login     string `json:"login"`
					AvatarURL string `json:"avatarUrl"`
				} `json:"author"`
				Labels *struct {
					Nodes []struct {
						Name  string `json:"name"`
						Color string `json:"color"`
					} `json:"nodes"`
				} `json:"labels"`
				Comments struct {
					TotalCount int `json:"totalCount"`
				} `json:"comments"`
				ReviewDecision string `json:"reviewDecision"`
				Commits        struct {
					Nodes []struct {
						Commit struct {
							StatusCheckRollup *struct {
								State string `json:"state"`
							} `json:"statusCheckRollup"`
						} `json:"commit"`
					} `json:"nodes"`
				} `json:"commits"`
				Additions    int `json:"additions"`
				Deletions    int `json:"deletions"`
				ChangedFiles int `json:"changedFiles"`
			} `json:"nodes"`
		} `json:"search"`
	}
	q := fmt.Sprintf("repo:%s is:pr is:open sort:updated-desc", repo)
	if err := c.graphql(ctx, openPullsQuery, map[string]any{"query": q}, &data); err != nil {
		return nil, err
	}
	var out []Summary
	for _, n := range data.Search.Nodes {
		if n.Number == 0 {
			continue // not a pull request
		}
		s := Summary{Number: n.Number, Title: n.Title, URL: n.URL, Draft: n.IsDraft, HeadRef: n.HeadRefName,
			CreatedAt: n.CreatedAt, UpdatedAt: n.UpdatedAt, Comments: n.Comments.TotalCount,
			Additions: n.Additions, Deletions: n.Deletions, ChangedFiles: n.ChangedFiles}
		if n.Author != nil {
			s.Author = User{Login: n.Author.Login, AvatarURL: n.Author.AvatarURL}
		}
		if n.Labels != nil {
			for _, l := range n.Labels.Nodes {
				s.Labels = append(s.Labels, Label{Name: l.Name, Color: l.Color})
			}
		}
		switch n.ReviewDecision {
		case "APPROVED":
			s.ReviewDecision = "approved"
		case "CHANGES_REQUESTED":
			s.ReviewDecision = "changes_requested"
		case "REVIEW_REQUIRED":
			s.ReviewDecision = "review_required"
		}
		if len(n.Commits.Nodes) > 0 && n.Commits.Nodes[0].Commit.StatusCheckRollup != nil {
			switch n.Commits.Nodes[0].Commit.StatusCheckRollup.State {
			case "SUCCESS":
				s.Checks = "success"
			case "FAILURE", "ERROR":
				s.Checks = "failure"
			case "PENDING", "EXPECTED":
				s.Checks = "pending"
			}
		}
		out = append(out, s)
	}
	return out, nil
}
