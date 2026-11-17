// Package bitbuckettest is an in-memory Bitbucket Cloud REST API server. It
// answers the requests the bitbucket provider sends with Bitbucket's JSON
// shapes and status codes, and has controls that change the state behind it.
package bitbuckettest

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
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
	Workspace string
	Repo      string
	// Token is the credential the server accepts.
	Token string
	// User, when set, makes the server accept only Basic authentication of
	// User and Token; when empty it accepts only Bearer authentication of Token.
	User string
	// PageSize caps how many values a list returns, whatever pagelen asks for.
	PageSize int
}

// Review is a reviewer's verdict; State is "approved" or "changes_requested".
// Bitbucket has no review text, so a body is not part of it.
type Review struct {
	Author string
	State  string
	At     time.Time
}

// Comment is a general comment, or an inline one when Path is set.
type Comment struct {
	Author string
	Body   string
	Path   string
	Line   int
	At     time.Time
}

type pull struct {
	id          int
	title       string
	description string
	source      string
	sourceHash  string
	destination string
	draft       bool
	state       string
	created     time.Time
	updated     time.Time
	participant []*participant
	comments    []comment
}

type participant struct {
	Review
}

type comment struct {
	id int
	Comment
}

// Server is a running fake Bitbucket Cloud API.
type Server struct {
	t   testing.TB
	srv *httptest.Server
	cfg Config

	mu        sync.Mutex
	pulls     []*pull
	nextID    int
	clock     time.Time
	rejected  bool
	rateReset time.Time
	down      bool
}

// New starts a server that is closed when the test ends.
func New(t testing.TB, cfg Config) *Server {
	t.Helper()
	if cfg.Workspace == "" {
		cfg.Workspace = "acme"
	}
	if cfg.Repo == "" {
		cfg.Repo = "widgets"
	}
	if cfg.Token == "" {
		cfg.Token = "bbtoken-test-0000000000000000"
	}
	if cfg.PageSize <= 0 {
		cfg.PageSize = 50
	}
	s := &Server{t: t, cfg: cfg, nextID: 100, clock: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	mux := http.NewServeMux()
	base := "/repositories/" + cfg.Workspace + "/" + cfg.Repo + "/pullrequests"
	mux.HandleFunc("POST "+base, s.guard(s.create))
	mux.HandleFunc("GET "+base, s.guard(s.list))
	mux.HandleFunc("GET "+base+"/{id}", s.guard(s.get))
	mux.HandleFunc("GET "+base+"/{id}/comments", s.guard(s.listComments))
	mux.HandleFunc("/", s.guard(func(w http.ResponseWriter, _ *http.Request) { notFound(w) }))
	s.srv = httptest.NewServer(mux)
	t.Cleanup(s.srv.Close)
	return s
}

// URL is the API base address to give the provider.
func (s *Server) URL() string { return s.srv.URL }

// Workspace and Repo name the repository the server serves.
func (s *Server) Workspace() string { return s.cfg.Workspace }
func (s *Server) Repo() string      { return s.cfg.Repo }

// Token and User are the credential the server accepts.
func (s *Server) Token() string { return s.cfg.Token }
func (s *Server) User() string  { return s.cfg.User }

// Merge merges the pull request.
func (s *Server) Merge(number int) { s.setState(number, "MERGED") }

// SetHead sets the commit the pull request's source branch is reported at. The
// default is a hash derived from the source branch name.
func (s *Server) SetHead(number int, sha string) {
	s.t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.must(number).sourceHash = sha
}

// Decline declines the pull request.
func (s *Server) Decline(number int) { s.setState(number, "DECLINED") }

func (s *Server) setState(number int, state string) {
	s.t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.must(number)
	p.state = state
	p.updated = s.tick(time.Time{})
}

// AddReview records a reviewer's verdict, replacing that reviewer's earlier one.
func (s *Server) AddReview(number int, r Review) {
	s.t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.must(number)
	r.At = s.tick(r.At)
	for _, part := range p.participant {
		if part.Author == r.Author {
			part.Review = r
			return
		}
	}
	p.participant = append(p.participant, &participant{r})
}

// AddInline adds a comment attached to a line of a file and returns its identifier.
func (s *Server) AddInline(number int, c Comment) int {
	s.t.Helper()
	if c.Path == "" {
		s.t.Fatal("bitbuckettest: an inline comment needs a path")
	}
	return s.AddComment(number, c)
}

// AddComment adds a comment and returns its identifier; it is inline when it has a path.
func (s *Server) AddComment(number int, c Comment) int {
	s.t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.must(number)
	c.At = s.tick(c.At)
	s.nextID++
	p.comments = append(p.comments, comment{s.nextID, c})
	return s.nextID
}

// RejectCredentials makes every request answer 401.
func (s *Server) RejectCredentials() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rejected = true
}

// RateLimit makes every request answer 429 with a Retry-After that runs until resetAt.
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
	s.t.Fatalf("bitbuckettest: no pull request #%d", number)
	return nil
}

func (s *Server) find(number int) *pull {
	for _, p := range s.pulls {
		if p.id == number {
			return p
		}
	}
	return nil
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

func (s *Server) authorized(r *http.Request) bool {
	got := r.Header.Get("Authorization")
	if s.cfg.User != "" {
		return got == "Basic "+base64.StdEncoding.EncodeToString([]byte(s.cfg.User+":"+s.cfg.Token))
	}
	return got == "Bearer "+s.cfg.Token
}

func (s *Server) guard(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		down, reset, rejected := s.down, s.rateReset, s.rejected
		s.mu.Unlock()
		switch {
		case down:
			writeError(w, http.StatusInternalServerError, "Internal server error", nil)
		case !reset.IsZero():
			secs := max(1, int64(math.Ceil(time.Until(reset).Seconds())))
			w.Header().Set("Retry-After", strconv.FormatInt(secs, 10))
			writeError(w, http.StatusTooManyRequests, "Rate limit for this resource has been exceeded", nil)
		case rejected || !s.authorized(r):
			writeError(w, http.StatusUnauthorized, "Invalid credentials", nil)
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

func writeError(w http.ResponseWriter, status int, message string, fields map[string]any) {
	e := map[string]any{"message": message}
	if fields != nil {
		e["fields"] = fields
	}
	writeJSON(w, status, map[string]any{"type": "error", "error": e})
}

func notFound(w http.ResponseWriter) {
	writeJSON(w, http.StatusNotFound, map[string]any{
		"type": "error",
		"error": map[string]any{
			"message": "Resource not found",
			"detail":  "There is no API hosted at this URL.",
		},
	})
}

func (s *Server) create(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Title       string `json:"title"`
		Description string `json:"description"`
		Draft       bool   `json:"draft"`
		Source      struct {
			Branch struct {
				Name string `json:"name"`
			} `json:"branch"`
		} `json:"source"`
		Destination struct {
			Branch struct {
				Name string `json:"name"`
			} `json:"branch"`
		} `json:"destination"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "Bad request", nil)
		return
	}
	fields := map[string]any{}
	if in.Title == "" {
		fields["title"] = []string{"This field is required."}
	}
	if in.Source.Branch.Name == "" {
		fields["source"] = []string{"This field is required."}
	}
	if len(fields) > 0 {
		writeError(w, http.StatusBadRequest, "Bad request", fields)
		return
	}
	dest := in.Destination.Branch.Name
	if dest == "" {
		dest = "main"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.pulls {
		if p.source == in.Source.Branch.Name && p.state == "OPEN" {
			writeError(w, http.StatusBadRequest, "Bad request: a pull request already exists for this source branch", nil)
			return
		}
	}
	now := s.tick(time.Time{})
	p := &pull{
		id: len(s.pulls) + 1, title: in.Title, description: in.Description, source: in.Source.Branch.Name,
		destination: dest, draft: in.Draft, state: "OPEN", created: now, updated: now,
	}
	s.pulls = append(s.pulls, p)
	writeJSON(w, http.StatusCreated, s.pullJSON(p))
}

func (s *Server) get(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.pullFromPath(r)
	if p == nil {
		notFound(w)
		return
	}
	writeJSON(w, http.StatusOK, s.pullJSON(p))
}

func (s *Server) pullFromPath(r *http.Request) *pull {
	n, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		return nil
	}
	return s.find(n)
}

func (s *Server) list(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	source, filtered, ok := parseSourceFilter(q.Get("q"))
	if !ok {
		writeError(w, http.StatusBadRequest, "Bad request: unsupported filter", map[string]any{"q": []string{"Only source.branch.name is supported."}})
		return
	}
	states := q["state"]
	if len(states) == 0 {
		states = []string{"OPEN"}
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	var matched []*pull
	for _, p := range s.pulls {
		if filtered && p.source != source {
			continue
		}
		if !contains(states, p.state) {
			continue
		}
		matched = append(matched, p)
	}
	newestFirst := false
	switch q.Get("sort") {
	case "-created_on", "-id":
		newestFirst = true
	}
	sort.SliceStable(matched, func(i, j int) bool {
		if newestFirst {
			return matched[i].id > matched[j].id
		}
		return matched[i].id < matched[j].id
	})
	values := make([]any, 0, len(matched))
	for _, p := range matched {
		values = append(values, s.pullJSON(p))
	}
	s.writePage(w, r, values)
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

// parseSourceFilter reads q=source.branch.name="x"; an empty q matches everything.
func parseSourceFilter(q string) (name string, filtered, ok bool) {
	if q == "" {
		return "", false, true
	}
	const prefix = `source.branch.name="`
	if !strings.HasPrefix(q, prefix) || !strings.HasSuffix(q, `"`) || len(q) < len(prefix)+1 {
		return "", false, false
	}
	var b strings.Builder
	rest := q[len(prefix) : len(q)-1]
	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case '\\':
			i++
			if i >= len(rest) {
				return "", false, false
			}
		case '"':
			return "", false, false
		}
		b.WriteByte(rest[i])
	}
	return b.String(), true, true
}

func (s *Server) listComments(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.pullFromPath(r)
	if p == nil {
		notFound(w)
		return
	}
	cs := append([]comment(nil), p.comments...)
	sort.SliceStable(cs, func(i, j int) bool { return cs[i].At.Before(cs[j].At) })
	values := make([]any, 0, len(cs))
	for _, c := range cs {
		values = append(values, s.commentJSON(p, c))
	}
	s.writePage(w, r, values)
}

// writePage writes one page of values with Bitbucket's pagination envelope.
func (s *Server) writePage(w http.ResponseWriter, r *http.Request, values []any) {
	q := r.URL.Query()
	per := s.cfg.PageSize
	if n, err := strconv.Atoi(q.Get("pagelen")); err == nil && n > 0 && n < per {
		per = n
	}
	page := 1
	if n, err := strconv.Atoi(q.Get("page")); err == nil && n > 1 {
		page = n
	}
	start := min((page-1)*per, len(values))
	end := min(start+per, len(values))
	out := map[string]any{"pagelen": per, "page": page, "size": len(values), "values": values[start:end]}
	if end < len(values) {
		next := url.Values{}
		for k, v := range q {
			next[k] = v
		}
		next.Set("page", strconv.Itoa(page+1))
		out["next"] = s.srv.URL + r.URL.Path + "?" + next.Encode()
	}
	if page > 1 {
		prev := url.Values{}
		for k, v := range q {
			prev[k] = v
		}
		prev.Set("page", strconv.Itoa(page-1))
		out["previous"] = s.srv.URL + r.URL.Path + "?" + prev.Encode()
	}
	writeJSON(w, http.StatusOK, out)
}

func stamp(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000000-07:00") }

func account(name string) map[string]any {
	id := "acct-" + name
	return map[string]any{
		"type": "user", "display_name": name, "nickname": name, "account_id": id,
		"uuid": "{" + id + "}",
		"links": map[string]any{
			"self":   map[string]any{"href": "https://api.bitbucket.org/2.0/users/" + id},
			"html":   map[string]any{"href": "https://bitbucket.org/" + id + "/"},
			"avatar": map[string]any{"href": "https://secure.gravatar.com/avatar/0?d=identicon"},
		},
	}
}

func (s *Server) repoJSON() map[string]any {
	return map[string]any{
		"type": "repository", "full_name": s.cfg.Workspace + "/" + s.cfg.Repo, "name": s.cfg.Repo,
		"uuid": "{repo-" + s.cfg.Repo + "}",
	}
}

func (p *pull) headHash() string {
	if p.sourceHash != "" {
		return p.sourceHash
	}
	sum := sha256.Sum256([]byte(p.source))
	return hex.EncodeToString(sum[:])[:40]
}

func hash(n int) string { return fmt.Sprintf("%040x", 0xdef000+n) }

func (s *Server) html(path string) string {
	return s.srv.URL + "/" + s.cfg.Workspace + "/" + s.cfg.Repo + path
}

func (s *Server) api(path string) string {
	return s.srv.URL + "/repositories/" + s.cfg.Workspace + "/" + s.cfg.Repo + path
}

func (s *Server) pullJSON(p *pull) map[string]any {
	participants := make([]any, 0, len(p.participant))
	reviewers := make([]any, 0, len(p.participant))
	for _, part := range p.participant {
		participants = append(participants, map[string]any{
			"type": "participant", "user": account(part.Author), "role": "REVIEWER",
			"approved": part.State == "approved", "state": part.State,
			"participated_on": stamp(part.At),
		})
		reviewers = append(reviewers, account(part.Author))
	}
	var mergeCommit any
	if p.state == "MERGED" {
		mergeCommit = map[string]any{"type": "commit", "hash": hash(p.id)}
	}
	n := strconv.Itoa(p.id)
	return map[string]any{
		"type": "pullrequest", "id": p.id, "title": p.title, "description": p.description,
		"state": p.state, "draft": p.draft, "author": account("alice"),
		"rendered": map[string]any{
			"title":       map[string]any{"raw": p.title, "markup": "markdown", "html": "<p>" + p.title + "</p>"},
			"description": map[string]any{"raw": p.description, "markup": "markdown", "html": "<p>" + p.description + "</p>"},
		},
		"summary": map[string]any{"raw": p.description, "markup": "markdown", "html": "<p>" + p.description + "</p>"},
		"source": map[string]any{
			"branch": map[string]any{"name": p.source}, "commit": map[string]any{"type": "commit", "hash": p.headHash()},
			"repository": s.repoJSON(),
		},
		"destination": map[string]any{
			"branch": map[string]any{"name": p.destination}, "commit": map[string]any{"type": "commit", "hash": hash(0)},
			"repository": s.repoJSON(),
		},
		"merge_commit":        mergeCommit,
		"close_source_branch": false,
		"closed_by":           nil,
		"reason":              "",
		"created_on":          stamp(p.created),
		"updated_on":          stamp(p.updated),
		"comment_count":       len(p.comments),
		"task_count":          0,
		"participants":        participants,
		"reviewers":           reviewers,
		"queued":              false,
		"links": map[string]any{
			"self":     map[string]any{"href": s.api("/pullrequests/" + n)},
			"html":     map[string]any{"href": s.html("/pull-requests/" + n)},
			"comments": map[string]any{"href": s.api("/pullrequests/" + n + "/comments")},
			"commits":  map[string]any{"href": s.api("/pullrequests/" + n + "/commits")},
			"diff":     map[string]any{"href": s.api("/diff/" + s.cfg.Workspace + "/" + s.cfg.Repo + ":" + hash(p.id))},
		},
	}
}

func (s *Server) commentJSON(p *pull, c comment) map[string]any {
	n := strconv.Itoa(p.id)
	id := strconv.Itoa(c.id)
	out := map[string]any{
		"type": "pullrequest_comment", "id": c.id, "created_on": stamp(c.At), "updated_on": stamp(c.At),
		"content": map[string]any{
			"type": "rendered", "raw": c.Body, "markup": "markdown", "html": "<p>" + c.Body + "</p>",
		},
		"user":    account(c.Author),
		"deleted": false, "pending": false,
		"pullrequest": map[string]any{
			"type": "pullrequest", "id": p.id, "title": p.title,
			"links": map[string]any{"html": map[string]any{"href": s.html("/pull-requests/" + n)}},
		},
		"links": map[string]any{
			"self": map[string]any{"href": s.api("/pullrequests/" + n + "/comments/" + id)},
			"html": map[string]any{"href": s.html("/pull-requests/" + n + "/_/diff#comment-" + id)},
		},
	}
	if c.Path != "" {
		out["inline"] = map[string]any{"path": c.Path, "from": nil, "to": c.Line}
	}
	return out
}
