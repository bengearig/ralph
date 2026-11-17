// Package githubtest is an in-memory GitHub REST API server. It answers the
// requests the github provider sends with GitHub's JSON shapes and status
// codes, and has controls that change the state behind it.
package githubtest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Config sets the repository and credential the server expects. Zero values
// select the defaults.
type Config struct {
	Owner string
	Repo  string
	// Token is the bearer token the server accepts.
	Token string
	// PageSize caps how many items a list returns, whatever per_page asks for.
	PageSize int
}

// Review is a pull request review; State is APPROVED, CHANGES_REQUESTED or COMMENTED.
type Review struct {
	Author string
	State  string
	Body   string
	At     time.Time
}

// Comment is an inline comment (with Path and Line) or a conversation comment.
type Comment struct {
	Author string
	Body   string
	Path   string
	Line   int
	At     time.Time
}

type pull struct {
	number   int
	title    string
	body     string
	head     string
	headSHA  string
	base     string
	draft    bool
	merged   bool
	closed   bool
	created  time.Time
	mergedAt time.Time
	author   string
}

type review struct {
	id int
	Review
}

type comment struct {
	id int
	Comment
}

// Server is a running fake GitHub API.
type Server struct {
	t   testing.TB
	srv *httptest.Server
	cfg Config

	mu        sync.Mutex
	pulls     []*pull
	reviews   map[int][]review
	inline    map[int][]comment
	comments  map[int][]comment
	nextID    int
	clock     time.Time
	rejected  bool
	rateReset time.Time
	down      bool
}

// New starts a server that is closed when the test ends.
func New(t testing.TB, cfg Config) *Server {
	t.Helper()
	if cfg.Owner == "" {
		cfg.Owner = "octo-org"
	}
	if cfg.Repo == "" {
		cfg.Repo = "hello-world"
	}
	if cfg.Token == "" {
		cfg.Token = "ghp_githubtest0000000000000000000000000000"
	}
	if cfg.PageSize <= 0 {
		cfg.PageSize = 100
	}
	s := &Server{
		t:        t,
		cfg:      cfg,
		reviews:  map[int][]review{},
		inline:   map[int][]comment{},
		comments: map[int][]comment{},
		nextID:   1000,
		clock:    time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	mux := http.NewServeMux()
	base := "/repos/" + cfg.Owner + "/" + cfg.Repo
	mux.HandleFunc("POST "+base+"/pulls", s.guard(s.create))
	mux.HandleFunc("GET "+base+"/pulls", s.guard(s.list))
	mux.HandleFunc("GET "+base+"/pulls/{number}", s.guard(s.get))
	mux.HandleFunc("GET "+base+"/pulls/{number}/reviews", s.guard(s.listReviews))
	mux.HandleFunc("GET "+base+"/pulls/{number}/comments", s.guard(s.listInline))
	mux.HandleFunc("GET "+base+"/issues/{number}/comments", s.guard(s.listComments))
	mux.HandleFunc("/", s.guard(func(w http.ResponseWriter, _ *http.Request) { notFound(w) }))
	s.srv = httptest.NewServer(mux)
	t.Cleanup(s.srv.Close)
	return s
}

// URL is the API base address to give the provider.
func (s *Server) URL() string { return s.srv.URL }

// Owner and Repo name the repository the server serves.
func (s *Server) Owner() string { return s.cfg.Owner }
func (s *Server) Repo() string  { return s.cfg.Repo }

// Token is the credential the server accepts.
func (s *Server) Token() string { return s.cfg.Token }

// Merge merges the pull request.
func (s *Server) Merge(number int) {
	s.t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.must(number)
	p.closed, p.merged = true, true
	p.mergedAt = s.tick(time.Time{})
}

// SetHead sets the commit the pull request's head branch is reported at. The
// default is a hash derived from the head branch name.
func (s *Server) SetHead(number int, sha string) {
	s.t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.must(number).headSHA = sha
}

// Decline closes the pull request without merging it.
func (s *Server) Decline(number int) {
	s.t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.must(number).closed = true
}

// AddReview adds a review and returns its identifier.
func (s *Server) AddReview(number int, r Review) int {
	s.t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.must(number)
	r.At = s.tick(r.At)
	id := s.id()
	s.reviews[number] = append(s.reviews[number], review{id, r})
	return id
}

// AddInline adds an inline comment and returns its identifier.
func (s *Server) AddInline(number int, c Comment) int {
	s.t.Helper()
	return s.addComment(number, c, s.inline)
}

// AddComment adds a conversation comment and returns its identifier.
func (s *Server) AddComment(number int, c Comment) int {
	s.t.Helper()
	return s.addComment(number, c, s.comments)
}

func (s *Server) addComment(number int, c Comment, into map[int][]comment) int {
	s.t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.must(number)
	c.At = s.tick(c.At)
	id := s.id()
	into[number] = append(into[number], comment{id, c})
	return id
}

// RejectCredentials makes every request answer 401.
func (s *Server) RejectCredentials() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rejected = true
}

// RateLimit makes every request answer as an exhausted rate limit that resets at resetAt.
func (s *Server) RateLimit(resetAt time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rateReset = resetAt
}

// BringDown makes every request answer 500.
func (s *Server) BringDown() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.down = true
}

func (s *Server) must(number int) *pull {
	s.t.Helper()
	if p := s.find(number); p != nil {
		return p
	}
	s.t.Fatalf("githubtest: no pull request #%d", number)
	return nil
}

func (s *Server) find(number int) *pull {
	for _, p := range s.pulls {
		if p.number == number {
			return p
		}
	}
	return nil
}

func (s *Server) id() int {
	s.nextID++
	return s.nextID
}

// tick returns at, or when it is zero a time a second after the latest one used.
func (s *Server) tick(at time.Time) time.Time {
	if at.IsZero() {
		s.clock = s.clock.Add(time.Second)
		return s.clock
	}
	if at.After(s.clock) {
		s.clock = at
	}
	return at
}

func (s *Server) guard(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		down, reset, rejected := s.down, s.rateReset, s.rejected
		s.mu.Unlock()
		switch {
		case down:
			writeJSON(w, http.StatusInternalServerError, map[string]any{"message": "Server Error"})
		case !reset.IsZero():
			w.Header().Set("X-RateLimit-Limit", "5000")
			w.Header().Set("X-RateLimit-Remaining", "0")
			w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(reset.Unix(), 10))
			w.Header().Set("X-RateLimit-Resource", "core")
			writeJSON(w, http.StatusForbidden, map[string]any{
				"message":           "API rate limit exceeded for user ID 1001.",
				"documentation_url": "https://docs.github.com/rest/overview/resources-in-the-rest-api#rate-limiting",
			})
		case rejected || r.Header.Get("Authorization") != "Bearer "+s.cfg.Token:
			writeJSON(w, http.StatusUnauthorized, map[string]any{
				"message":           "Bad credentials",
				"documentation_url": "https://docs.github.com/rest",
			})
		default:
			h(w, r)
		}
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func notFound(w http.ResponseWriter) {
	writeJSON(w, http.StatusNotFound, map[string]any{
		"message":           "Not Found",
		"documentation_url": "https://docs.github.com/rest",
	})
}

func (s *Server) create(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Title string `json:"title"`
		Head  string `json:"head"`
		Base  string `json:"base"`
		Body  string `json:"body"`
		Draft bool   `json:"draft"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Head == "" || in.Base == "" || in.Title == "" {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"message": "Validation Failed",
			"errors":  []map[string]any{{"resource": "PullRequest", "code": "missing_field", "message": "title, head and base are required"}},
		})
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.pulls {
		if p.head == in.Head && !p.closed {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
				"message": "Validation Failed",
				"errors": []map[string]any{{
					"resource": "PullRequest", "code": "custom",
					"message": "A pull request already exists for " + s.cfg.Owner + ":" + in.Head + ".",
				}},
				"documentation_url": "https://docs.github.com/rest/pulls/pulls#create-a-pull-request",
			})
			return
		}
	}
	p := &pull{
		number: len(s.pulls) + 1, title: in.Title, body: in.Body, head: in.Head, base: in.Base,
		draft: in.Draft, created: s.tick(time.Time{}), author: "alice",
	}
	s.pulls = append(s.pulls, p)
	writeJSON(w, http.StatusCreated, s.pullJSON(p, r))
}

func (s *Server) get(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.pullFromPath(r)
	if p == nil {
		notFound(w)
		return
	}
	writeJSON(w, http.StatusOK, s.pullJSON(p, r))
}

func (s *Server) pullFromPath(r *http.Request) *pull {
	n, err := strconv.Atoi(r.PathValue("number"))
	if err != nil {
		return nil
	}
	return s.find(n)
}

func (s *Server) list(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	s.mu.Lock()
	defer s.mu.Unlock()

	state := q.Get("state")
	if state == "" {
		state = "open"
	}
	var matched []*pull
	for _, p := range s.pulls {
		if h := q.Get("head"); h != "" {
			owner, branch, ok := strings.Cut(h, ":")
			if !ok || owner != s.cfg.Owner || branch != p.head {
				continue
			}
		}
		switch {
		case state == "open" && p.closed, state == "closed" && !p.closed:
			continue
		}
		matched = append(matched, p)
	}
	desc := q.Get("direction") != "asc"
	sort.SliceStable(matched, func(i, j int) bool {
		if desc {
			return matched[i].number > matched[j].number
		}
		return matched[i].number < matched[j].number
	})
	out := make([]any, 0, len(matched))
	for _, p := range matched {
		out = append(out, s.pullJSON(p, r))
	}
	s.writePage(w, r, out)
}

func (s *Server) listReviews(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.pullFromPath(r)
	if p == nil {
		notFound(w)
		return
	}
	out := make([]any, 0)
	for _, rv := range s.reviews[p.number] {
		out = append(out, map[string]any{
			"id":                 rv.id,
			"node_id":            fmt.Sprintf("PRR_%d", rv.id),
			"user":               user(rv.Author),
			"body":               rv.Body,
			"state":              rv.State,
			"html_url":           s.html(fmt.Sprintf("/pull/%d#pullrequestreview-%d", p.number, rv.id)),
			"pull_request_url":   s.api(fmt.Sprintf("/pulls/%d", p.number)),
			"submitted_at":       rv.At.UTC().Format(time.RFC3339),
			"commit_id":          commitID(p.number),
			"author_association": "MEMBER",
		})
	}
	s.writePage(w, r, out)
}

func (s *Server) listInline(w http.ResponseWriter, r *http.Request) {
	s.listComments0(w, r, s.inline, true)
}

func (s *Server) listComments(w http.ResponseWriter, r *http.Request) {
	s.listComments0(w, r, s.comments, false)
}

func (s *Server) listComments0(w http.ResponseWriter, r *http.Request, from map[int][]comment, inline bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.pullFromPath(r)
	if p == nil {
		notFound(w)
		return
	}
	out := make([]any, 0)
	for _, c := range from[p.number] {
		item := map[string]any{
			"id":         c.id,
			"node_id":    fmt.Sprintf("C_%d", c.id),
			"user":       user(c.Author),
			"body":       c.Body,
			"created_at": c.At.UTC().Format(time.RFC3339),
			"updated_at": c.At.UTC().Format(time.RFC3339),
		}
		if inline {
			item["html_url"] = s.html(fmt.Sprintf("/pull/%d#discussion_r%d", p.number, c.id))
			item["path"] = c.Path
			item["line"] = c.Line
			item["original_line"] = c.Line
			item["commit_id"] = commitID(p.number)
			item["side"] = "RIGHT"
			item["diff_hunk"] = "@@ -1,3 +1,4 @@"
		} else {
			item["html_url"] = s.html(fmt.Sprintf("/pull/%d#issuecomment-%d", p.number, c.id))
			item["issue_url"] = s.api(fmt.Sprintf("/issues/%d", p.number))
		}
		out = append(out, item)
	}
	s.writePage(w, r, out)
}

// writePage writes one page of items and a Link header pointing to the next.
func (s *Server) writePage(w http.ResponseWriter, r *http.Request, items []any) {
	q := r.URL.Query()
	per := s.cfg.PageSize
	if n, err := strconv.Atoi(q.Get("per_page")); err == nil && n > 0 && n < per {
		per = n
	}
	page := 1
	if n, err := strconv.Atoi(q.Get("page")); err == nil && n > 1 {
		page = n
	}
	start := min((page-1)*per, len(items))
	end := min(start+per, len(items))
	if end < len(items) {
		link := func(n int, rel string) string {
			next := url.Values{}
			for k, v := range q {
				next[k] = v
			}
			next.Set("page", strconv.Itoa(n))
			return fmt.Sprintf("<%s%s?%s>; rel=%q", s.srv.URL, r.URL.Path, next.Encode(), rel)
		}
		last := (len(items) + per - 1) / per
		w.Header().Add("Link", link(page+1, "next")+", "+link(last, "last"))
	}
	writeJSON(w, http.StatusOK, items[start:end])
}

func user(login string) map[string]any {
	return map[string]any{
		"login": login, "id": 1001, "node_id": "MDQ6VXNlcjE=", "type": "User", "site_admin": false,
		"html_url": "https://github.com/" + login, "url": "https://api.github.com/users/" + login,
		"avatar_url": "https://avatars.githubusercontent.com/u/1001?v=4",
	}
}

func branchCommit(branch string) string {
	sum := sha256.Sum256([]byte(branch))
	return hex.EncodeToString(sum[:])[:40]
}

func (p *pull) headCommit() string {
	if p.headSHA != "" {
		return p.headSHA
	}
	return branchCommit(p.head)
}

func commitID(number int) string { return fmt.Sprintf("%040x", 0xabc000+number) }

func (s *Server) html(path string) string {
	return s.srv.URL + "/" + s.cfg.Owner + "/" + s.cfg.Repo + path
}

func (s *Server) api(path string) string {
	return s.srv.URL + "/repos/" + s.cfg.Owner + "/" + s.cfg.Repo + path
}

func (s *Server) pullJSON(p *pull, _ *http.Request) map[string]any {
	state := "open"
	if p.closed {
		state = "closed"
	}
	var mergedAt any
	if p.merged {
		mergedAt = p.mergedAt.UTC().Format(time.RFC3339)
	}
	repo := map[string]any{
		"id": 2001, "name": s.cfg.Repo, "full_name": s.cfg.Owner + "/" + s.cfg.Repo,
		"private": false, "owner": user(s.cfg.Owner),
	}
	return map[string]any{
		"url":        s.api(fmt.Sprintf("/pulls/%d", p.number)),
		"id":         1000000 + p.number,
		"node_id":    fmt.Sprintf("PR_%d", p.number),
		"html_url":   s.html(fmt.Sprintf("/pull/%d", p.number)),
		"number":     p.number,
		"state":      state,
		"locked":     false,
		"title":      p.title,
		"user":       user(p.author),
		"body":       p.body,
		"created_at": p.created.UTC().Format(time.RFC3339),
		"updated_at": p.created.UTC().Format(time.RFC3339),
		"closed_at": func() any {
			if p.closed {
				return p.created.UTC().Format(time.RFC3339)
			}
			return nil
		}(),
		"merged_at":          mergedAt,
		"merge_commit_sha":   commitID(p.number),
		"draft":              p.draft,
		"head":               map[string]any{"label": s.cfg.Owner + ":" + p.head, "ref": p.head, "sha": p.headCommit(), "user": user(s.cfg.Owner), "repo": repo},
		"base":               map[string]any{"label": s.cfg.Owner + ":" + p.base, "ref": p.base, "sha": commitID(0), "user": user(s.cfg.Owner), "repo": repo},
		"author_association": "MEMBER",
		"merged":             p.merged,
		"comments":           len(s.comments[p.number]),
		"review_comments":    len(s.inline[p.number]),
	}
}
