// Package github reads the pull requests of a repository on GitHub and
// reviews them, through GitHub's REST and GraphQL APIs.
package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Repo is a repository on GitHub.
type Repo struct {
	Owner, Name string
}

func (r Repo) String() string { return r.Owner + "/" + r.Name }

// IsZero reports whether the repository is unknown.
func (r Repo) IsZero() bool { return r.Owner == "" || r.Name == "" }

// URL is the repository's page.
func (r Repo) URL() string { return "https://github.com/" + r.String() }

var (
	remoteURL = regexp.MustCompile(`^(?:https?://(?:[^@/]+@)?|ssh://(?:[^@/]+@)?|git://|(?:[^@/]+@))github\.com[:/]([\w.-]+)/([\w.-]+?)(?:\.git)?/?$`)
	pullURL   = regexp.MustCompile(`^(?:https?://)?(?:www\.)?github\.com/([\w.-]+)/([\w.-]+)/pull/(\d+)(?:[/?#].*)?$`)
	pullRef   = regexp.MustCompile(`^(?:([\w.-]+)/([\w.-]+))?#(\d+)$`)
)

// ParseRemote reads the repository of a GitHub remote's URL, as
// git@github.com:owner/repo.git or https://github.com/owner/repo.
func ParseRemote(url string) (Repo, bool) {
	m := remoteURL.FindStringSubmatch(strings.TrimSpace(url))
	if m == nil {
		return Repo{}, false
	}
	return Repo{Owner: m[1], Name: m[2]}, true
}

// ParsePull reads a pull request named as owner/repo#123, #123 or its URL.
// The repository is zero for #123.
func ParsePull(s string) (Repo, int, bool) {
	s = strings.TrimSpace(s)
	if m := pullURL.FindStringSubmatch(s); m != nil {
		n, _ := strconv.Atoi(m[3])
		return Repo{Owner: m[1], Name: m[2]}, n, n > 0
	}
	if m := pullRef.FindStringSubmatch(s); m != nil {
		n, _ := strconv.Atoi(m[3])
		return Repo{Owner: m[1], Name: m[2]}, n, n > 0
	}
	return Repo{}, 0, false
}

// API is where the client sends its requests, GitHub's own unless tests
// set another.
var API = "https://api.github.com"

// Client talks to GitHub's API, with a token or without one: without, only
// public repositories can be read, and nothing written.
type Client struct {
	Token string
	HTTP  *http.Client
}

// Error is a request GitHub refused.
type Error struct {
	Status  int
	Message string
}

func (e *Error) Error() string {
	switch {
	case e.Status == http.StatusUnauthorized:
		return "GitHub refused the token: " + e.Message
	case e.Status == http.StatusForbidden && strings.Contains(strings.ToLower(e.Message), "rate limit"):
		return "GitHub's rate limit was reached. Sign in with the GitHub CLI (gh auth login) to raise it."
	case e.Status == http.StatusNotFound:
		return "Not found on GitHub. A private repository needs a token: sign in with gh auth login."
	}
	return fmt.Sprintf("GitHub: %s (%d)", e.Message, e.Status)
}

// Forbidden reports whether err is GitHub refusing a write the token may
// not make.
func Forbidden(err error) bool {
	var e *Error
	return errors.As(err, &e) && (e.Status == http.StatusForbidden || e.Status == http.StatusUnauthorized)
}

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

// do sends a request to the REST API and decodes its answer into out, and
// returns the answer's headers.
func (c *Client) do(ctx context.Context, method, path string, body, out any) (http.Header, error) {
	var r io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		r = bytes.NewReader(data)
	}
	url := path
	if !strings.HasPrefix(url, "http") {
		url = API + path
	}
	req, err := http.NewRequestWithContext(ctx, method, url, r)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "godiff")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	res, err := c.http().Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	data, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	if res.StatusCode >= 300 {
		return res.Header, apiError(res.StatusCode, data)
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return res.Header, fmt.Errorf("GitHub: %w", err)
		}
	}
	return res.Header, nil
}

// apiError reads GitHub's message, and the details of a validation error.
func apiError(status int, data []byte) error {
	var body struct {
		Message string `json:"message"`
		Errors  []any  `json:"errors"`
	}
	json.Unmarshal(data, &body)
	msg := body.Message
	if msg == "" {
		msg = http.StatusText(status)
	}
	var details []string
	for _, e := range body.Errors {
		switch e := e.(type) {
		case string:
			details = append(details, e)
		case map[string]any:
			if m, ok := e["message"].(string); ok && m != "" {
				details = append(details, m)
			}
		}
	}
	if len(details) > 0 {
		msg += ": " + strings.Join(details, "; ")
	}
	return &Error{Status: status, Message: msg}
}

var nextLink = regexp.MustCompile(`<([^>]+)>;\s*rel="next"`)

// paginate reads every page of a list.
func paginate[T any](ctx context.Context, c *Client, path string) ([]T, error) {
	var all []T
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	url := path + sep + "per_page=100"
	for url != "" {
		var page []T
		h, err := c.do(ctx, http.MethodGet, url, nil, &page)
		if err != nil {
			return nil, err
		}
		all = append(all, page...)
		url = ""
		if m := nextLink.FindStringSubmatch(h.Get("Link")); m != nil {
			url = m[1]
		}
	}
	return all, nil
}

// graphql runs a query of GitHub's GraphQL API, which needs a token.
func (c *Client) graphql(ctx context.Context, query string, vars map[string]any, out any) error {
	if c.Token == "" {
		return &Error{Status: http.StatusUnauthorized, Message: "GitHub's GraphQL API needs a token"}
	}
	var res struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if _, err := c.do(ctx, http.MethodPost, "/graphql", map[string]any{"query": query, "variables": vars}, &res); err != nil {
		return err
	}
	if len(res.Errors) > 0 {
		var msgs []string
		for _, e := range res.Errors {
			msgs = append(msgs, e.Message)
		}
		return &Error{Status: http.StatusUnprocessableEntity, Message: strings.Join(msgs, "; ")}
	}
	if out != nil {
		return json.Unmarshal(res.Data, out)
	}
	return nil
}

// User is an account on GitHub.
type User struct {
	Login     string `json:"login"`
	AvatarURL string `json:"avatar_url"`
}

// Viewer returns the account of the token.
func (c *Client) Viewer(ctx context.Context) (User, error) {
	var u User
	_, err := c.do(ctx, http.MethodGet, "/user", nil, &u)
	return u, err
}
