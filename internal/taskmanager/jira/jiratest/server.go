// Package jiratest is an in-memory server that speaks the Jira Cloud REST API
// v3 requests the jira client sends, with the JSON shapes and error bodies
// Atlassian documents, and has controls that change the state behind it.
//
// It is written from the published API description, not from a real site.
package jiratest

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bengearig/ralph/internal/adf"
	"github.com/bengearig/ralph/internal/taskmanager"
)

// Options set the project, the credential and the paging the server uses. Zero
// values select the defaults.
type Options struct {
	Project string
	// Types are the issue type names of the project.
	Types []string
	// Initial is the status a new ticket starts in.
	Initial string
	User    string
	Token   string
	// PageSize caps how many issues a search returns in one page.
	PageSize int
}

// Issue is a ticket to seed. A zero Key takes the next key of the project and
// a zero Status the initial one. Description is a raw ADF document and wins
// over Body, which is Markdown.
type Issue struct {
	Key         string
	Type        string
	Summary     string
	Body        string
	Description json.RawMessage
	Status      string
	Parent      string
	Labels      []string
}

// Request is a request the server received.
type Request struct {
	Method string
	Path   string
	Query  url.Values
	Header http.Header
	// Body is the decoded JSON body, nil when there was none.
	Body any
}

type stored struct {
	id          string
	key         string
	typ         string
	summary     string
	description json.RawMessage
	status      string
	parent      string
	labels      []string
	updated     time.Time
}

// Server is a running fake Jira site.
type Server struct {
	t    testing.TB
	srv  *httptest.Server
	opts Options

	mu       sync.Mutex
	issues   map[string]*stored
	aliases  map[string]string
	next     int
	nextID   int
	clock    time.Time
	requests []Request
	rejected bool
	limit    time.Duration
	failure  *failure

	workflow map[string][]transitionDef
	required map[[2]string]bool
	nextTr   int
	boards   []*boardDef
	sprints  []*sprintDef
}

type transitionDef struct{ id, name, to string }

type boardDef struct {
	id      int
	name    string
	project string
}

type sprintDef struct {
	id, board int
	name      string
	state     string
	issues    []string
}

type failure struct {
	status int
	body   string
}

// NewServer starts a server that is closed when the test ends.
func NewServer(t testing.TB, opts Options) *Server {
	t.Helper()
	if opts.Project == "" {
		opts.Project = "RAL"
	}
	if len(opts.Types) == 0 {
		opts.Types = []string{"Task", "Feature", "Bug"}
	}
	if opts.Initial == "" {
		opts.Initial = "To Do"
	}
	if opts.User == "" {
		opts.User = "ralph@example.com"
	}
	if opts.Token == "" {
		opts.Token = "jiratest-api-token"
	}
	if opts.PageSize <= 0 {
		opts.PageSize = 100
	}
	s := &Server{
		t:       t,
		opts:    opts,
		issues:  map[string]*stored{},
		aliases: map[string]string{},
		nextID:  10000,
		clock:   time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),

		required: map[[2]string]bool{},
	}
	s.setWorkflow(map[string][]string{
		"To Do":       {"In Progress"},
		"In Progress": {"To Do", "Done"},
		"Done":        {"In Progress"},
	})
	s.srv = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.srv.Close)
	return s
}

// URL is the site address to give the client.
func (s *Server) URL() string { return s.srv.URL }

// Project is the key of the project the server serves.
func (s *Server) Project() string { return s.opts.Project }

// User and Token are the credential the server accepts.
func (s *Server) User() string  { return s.opts.User }
func (s *Server) Token() string { return s.opts.Token }

// Close stops the server.
func (s *Server) Close() { s.srv.Close() }

// Seed adds a ticket and returns its key.
func (s *Server) Seed(in Issue) string {
	s.t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	st := &stored{
		typ:     in.Type,
		summary: in.Summary,
		status:  in.Status,
		parent:  in.Parent,
		labels:  slices.Clone(in.Labels),
	}
	if st.typ == "" {
		st.typ = s.opts.Types[0]
	}
	if st.status == "" {
		st.status = s.opts.Initial
	}
	if in.Description != nil {
		st.description = in.Description
	} else {
		st.description = adfOf(in.Body)
	}
	s.add(st, in.Key)
	return st.key
}

// Delete removes a ticket.
func (s *Server) Delete(key string) {
	s.t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.lookup(key)
	if st == nil {
		s.t.Fatalf("jiratest: no ticket %s", key)
	}
	delete(s.issues, st.key)
	for old, k := range s.aliases {
		if k == st.key {
			delete(s.aliases, old)
		}
	}
}

// Edit changes the title and the body of a ticket, as a person in Jira would.
func (s *Server) Edit(key string, f taskmanager.Fields) {
	s.t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.lookup(key)
	if st == nil {
		s.t.Fatalf("jiratest: no ticket %s", key)
	}
	st.summary = f.Title
	st.description = adfOf(f.Body)
	st.updated = s.tick()
}

// MoveKey gives a ticket a new key; the old key still reaches it by a direct
// read but no longer matches a search, as in Jira.
func (s *Server) MoveKey(oldKey, newKey string) {
	s.t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.lookup(oldKey)
	if st == nil {
		s.t.Fatalf("jiratest: no ticket %s", oldKey)
	}
	delete(s.issues, st.key)
	s.aliases[st.key] = newKey
	st.key = newKey
	s.issues[newKey] = st
}

// RejectCredentials makes every request answer 401.
func (s *Server) RejectCredentials() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rejected = true
}

// RateLimit makes every request answer 429 with a Retry-After of retryAfter.
func (s *Server) RateLimit(retryAfter time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.limit = retryAfter
}

// FailWith makes every request answer status with body.
func (s *Server) FailWith(status int, body string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failure = &failure{status, body}
}

// Heal undoes RejectCredentials, RateLimit and FailWith.
func (s *Server) Heal() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rejected, s.limit, s.failure = false, 0, nil
}

// Requests returns the requests received so far.
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.requests)
}

// ResetRequests forgets the requests received so far.
func (s *Server) ResetRequests() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = nil
}

// SetWorkflow replaces the workflow: transitions maps a status name to the
// names of the statuses a ticket in it can move to. The server invents the
// transition ids and names.
func (s *Server) SetWorkflow(initial string, transitions map[string][]string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if initial != "" {
		s.opts.Initial = initial
	}
	s.setWorkflow(transitions)
}

func (s *Server) setWorkflow(transitions map[string][]string) {
	s.workflow = map[string][]transitionDef{}
	s.required = map[[2]string]bool{}
	froms := make([]string, 0, len(transitions))
	for from := range transitions {
		froms = append(froms, from)
	}
	sort.Strings(froms)
	for _, from := range froms {
		for _, to := range transitions[from] {
			s.nextTr++
			s.workflow[from] = append(s.workflow[from], transitionDef{
				id: strconv.Itoa(10 * s.nextTr), name: "Transition " + strconv.Itoa(s.nextTr), to: to,
			})
		}
	}
}

// RequireFieldsFor makes the transition from one status to another answer 400
// because a field is missing.
func (s *Server) RequireFieldsFor(from, to string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.required[[2]string{from, to}] = true
}

// AddBoard adds a scrum board of a project.
func (s *Server) AddBoard(id int, name, projectKey string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.boards = append(s.boards, &boardDef{id: id, name: name, project: projectKey})
}

// AddSprint adds a sprint to a board; state is active, future or closed.
func (s *Server) AddSprint(boardID, id int, name, state string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sprints = append(s.sprints, &sprintDef{id: id, board: boardID, name: name, state: state})
}

// SetSprintState changes the state of a sprint, as closing it in Jira would.
func (s *Server) SetSprintState(id int, state string) {
	s.t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, sp := range s.sprints {
		if sp.id == id {
			sp.state = state
			return
		}
	}
	s.t.Fatalf("jiratest: no sprint %d", id)
}

// SprintIssues returns the keys of the tickets put in a sprint.
func (s *Server) SprintIssues(id int) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, sp := range s.sprints {
		if sp.id == id {
			return slices.Clone(sp.issues)
		}
	}
	return nil
}

// Status returns the status a ticket is in.
func (s *Server) Status(key string) string {
	s.t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.lookup(key)
	if st == nil {
		s.t.Fatalf("jiratest: no ticket %s", key)
	}
	return st.status
}

func adfOf(md string) json.RawMessage {
	if strings.TrimSpace(md) == "" {
		return nil
	}
	b, err := json.Marshal(adf.FromMarkdown(md))
	if err != nil {
		panic(err)
	}
	return b
}

func (s *Server) tick() time.Time {
	s.clock = s.clock.Add(time.Second)
	return s.clock
}

func (s *Server) add(st *stored, key string) {
	if key == "" {
		s.next++
		key = fmt.Sprintf("%s-%d", s.opts.Project, s.next)
	} else if p, n, ok := strings.Cut(key, "-"); ok && p == s.opts.Project {
		if v, err := strconv.Atoi(n); err == nil && v > s.next {
			s.next = v
		}
	}
	s.nextID++
	st.key = key
	st.id = strconv.Itoa(s.nextID)
	st.updated = s.tick()
	s.issues[key] = st
}

// lookup finds a ticket by its key or by a key it had before it was moved.
func (s *Server) lookup(key string) *stored {
	if st, ok := s.issues[key]; ok {
		return st
	}
	if k, ok := s.aliases[key]; ok {
		return s.issues[k]
	}
	return nil
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var decoded any
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &decoded)
	}
	s.mu.Lock()
	s.requests = append(s.requests, Request{Method: r.Method, Path: r.URL.Path, Query: r.URL.Query(), Header: r.Header.Clone(), Body: decoded})
	rejected, limit, fail := s.rejected, s.limit, s.failure
	s.mu.Unlock()

	want := "Basic " + base64.StdEncoding.EncodeToString([]byte(s.opts.User+":"+s.opts.Token))
	switch {
	case rejected || r.Header.Get("Authorization") != want:
		writeJSON(w, http.StatusUnauthorized, errorBody("Client must be authenticated to access this resource."))
	case limit > 0:
		w.Header().Set("Retry-After", strconv.Itoa(int((limit+time.Second-1)/time.Second)))
		writeJSON(w, http.StatusTooManyRequests, errorBody("Rate limit exceeded."))
	case fail != nil:
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(fail.status)
		_, _ = io.WriteString(w, fail.body)
	default:
		s.route(w, r, decoded)
	}
}

func (s *Server) route(w http.ResponseWriter, r *http.Request, body any) {
	const api = "/rest/api/3/"
	if agile, ok := strings.CutPrefix(r.URL.Path, "/rest/agile/1.0/"); ok {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.agile(w, r, agile, body)
		return
	}
	path, ok := strings.CutPrefix(r.URL.Path, api)
	if !ok {
		notFound(w, "")
		return
	}
	parts := strings.Split(path, "/")
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case r.Method == "GET" && path == "myself":
		writeJSON(w, http.StatusOK, map[string]any{
			"accountId": "5b10ac8d82e05b22cc7d4ef5", "accountType": "atlassian", "active": true,
			"displayName": "Ralph Tester", "emailAddress": s.opts.User,
		})
	case r.Method == "GET" && len(parts) == 2 && parts[0] == "project":
		s.project(w, parts[1])
	case r.Method == "POST" && path == "issue":
		s.create(w, body)
	case len(parts) == 2 && parts[0] == "issue" && r.Method == "GET":
		s.get(w, r, parts[1])
	case len(parts) == 3 && parts[0] == "issue" && parts[2] == "transitions" && r.Method == "GET":
		s.listTransitions(w, parts[1])
	case len(parts) == 3 && parts[0] == "issue" && parts[2] == "transitions" && r.Method == "POST":
		s.transition(w, parts[1], body)
	case len(parts) == 2 && parts[0] == "issue" && r.Method == "PUT":
		s.update(w, parts[1], body)
	case r.Method == "POST" && path == "search/jql":
		s.search(w, body)
	default:
		notFound(w, "")
	}
}

func errorBody(msgs ...string) map[string]any {
	return map[string]any{"errorMessages": msgs, "errors": map[string]string{}}
}

func fieldError(field, msg string) map[string]any {
	return map[string]any{"errorMessages": []string{}, "errors": map[string]string{field: msg}}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json;charset=UTF-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func notFound(w http.ResponseWriter, msg string) {
	if msg == "" {
		msg = "No project could be found with key or id or the project is not visible."
	}
	writeJSON(w, http.StatusNotFound, errorBody(msg))
}

func (s *Server) project(w http.ResponseWriter, key string) {
	if key != s.opts.Project {
		notFound(w, "No project could be found with key '"+key+"'.")
		return
	}
	types := make([]map[string]any, len(s.opts.Types))
	for i, n := range s.opts.Types {
		types[i] = map[string]any{"id": strconv.Itoa(10000 + i), "name": n, "subtask": false}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id": "10000", "key": s.opts.Project, "name": s.opts.Project + " project", "issueTypes": types,
	})
}

func (s *Server) create(w http.ResponseWriter, body any) {
	in, _ := body.(map[string]any)
	fields, _ := in["fields"].(map[string]any)
	if fields == nil {
		writeJSON(w, http.StatusBadRequest, errorBody("Request body must hold fields."))
		return
	}
	name := func(field, attr string) string {
		m, _ := fields[field].(map[string]any)
		v, _ := m[attr].(string)
		return v
	}
	summary, _ := fields["summary"].(string)
	st := &stored{summary: summary, typ: name("issuetype", "name"), status: s.opts.Initial}
	switch {
	case name("project", "key") != s.opts.Project:
		writeJSON(w, http.StatusBadRequest, fieldError("project", "valid project is required"))
		return
	case !slices.Contains(s.opts.Types, st.typ):
		writeJSON(w, http.StatusBadRequest, fieldError("issuetype", "specified issue type is not valid"))
		return
	case summary == "":
		writeJSON(w, http.StatusBadRequest, fieldError("summary", "You must specify a summary of the issue."))
		return
	case len([]rune(summary)) > 255:
		writeJSON(w, http.StatusBadRequest, fieldError("summary", "Summary must be less than 255 characters."))
		return
	}
	if p := name("parent", "key"); p != "" {
		if s.lookup(p) == nil {
			writeJSON(w, http.StatusBadRequest, fieldError("parent", "Given parent work item does not exist"))
			return
		}
		st.parent = p
	}
	if d, ok := fields["description"]; ok && d != nil {
		raw, _ := json.Marshal(d)
		if _, err := adf.ParseJSON(raw); err != nil {
			writeJSON(w, http.StatusBadRequest, fieldError("description", "Operation value must be an Atlassian Document"))
			return
		}
		st.description = raw
	}
	if ls, ok := fields["labels"].([]any); ok {
		for _, l := range ls {
			if v, ok := l.(string); ok {
				st.labels = append(st.labels, v)
			}
		}
	}
	s.add(st, "")
	writeJSON(w, http.StatusCreated, map[string]any{
		"id": st.id, "key": st.key, "self": s.srv.URL + "/rest/api/3/issue/" + st.id,
	})
}

func (s *Server) update(w http.ResponseWriter, key string, body any) {
	st := s.lookup(key)
	if st == nil {
		writeJSON(w, http.StatusNotFound, errorBody("Issue does not exist or you do not have permission to see it."))
		return
	}
	in, _ := body.(map[string]any)
	fields, _ := in["fields"].(map[string]any)
	if v, ok := fields["summary"]; ok {
		sum, _ := v.(string)
		if sum == "" {
			writeJSON(w, http.StatusBadRequest, fieldError("summary", "You must specify a summary of the issue."))
			return
		}
		if len([]rune(sum)) > 255 {
			writeJSON(w, http.StatusBadRequest, fieldError("summary", "Summary must be less than 255 characters."))
			return
		}
		st.summary = sum
	}
	if d, ok := fields["description"]; ok {
		if d == nil {
			st.description = nil
		} else {
			raw, _ := json.Marshal(d)
			if _, err := adf.ParseJSON(raw); err != nil {
				writeJSON(w, http.StatusBadRequest, fieldError("description", "Operation value must be an Atlassian Document"))
				return
			}
			st.description = raw
		}
	}
	st.updated = s.tick()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) render(st *stored, wanted []string) map[string]any {
	all := map[string]any{
		"summary":   st.summary,
		"status":    map[string]any{"name": st.status},
		"labels":    append([]string{}, st.labels...),
		"issuetype": map[string]any{"name": st.typ},
		"updated":   st.updated.Format("2006-01-02T15:04:05.000-0700"),
	}
	if st.description != nil {
		all["description"] = st.description
	} else {
		all["description"] = nil
	}
	if st.parent != "" {
		all["parent"] = map[string]any{"key": st.parent}
	}
	fields := map[string]any{}
	for k, v := range all {
		if len(wanted) == 0 || slices.Contains(wanted, k) {
			fields[k] = v
		}
	}
	return map[string]any{"id": st.id, "key": st.key, "self": s.srv.URL + "/rest/api/3/issue/" + st.id, "fields": fields}
}

func (s *Server) get(w http.ResponseWriter, r *http.Request, key string) {
	st := s.lookup(key)
	if st == nil {
		writeJSON(w, http.StatusNotFound, errorBody("Issue does not exist or you do not have permission to see it."))
		return
	}
	var wanted []string
	if f := r.URL.Query().Get("fields"); f != "" {
		wanted = strings.Split(f, ",")
	}
	writeJSON(w, http.StatusOK, s.render(st, wanted))
}

var (
	labelJQL = regexp.MustCompile(`^project = ("(?:[^"\\]|\\.)*") AND labels = ("(?:[^"\\]|\\.)*")$`)
	keyJQL   = regexp.MustCompile(`^key in \((.*)\)$`)
	quoted   = regexp.MustCompile(`"(?:[^"\\]|\\.)*"`)
)

func unquote(q string) string {
	var out strings.Builder
	rs := []rune(q[1 : len(q)-1])
	for i := 0; i < len(rs); i++ {
		if rs[i] == '\\' && i+1 < len(rs) {
			i++
		}
		out.WriteRune(rs[i])
	}
	return out.String()
}

func (s *Server) search(w http.ResponseWriter, body any) {
	in, _ := body.(map[string]any)
	jql, _ := in["jql"].(string)
	var wanted []string
	if fs, ok := in["fields"].([]any); ok {
		for _, f := range fs {
			if v, ok := f.(string); ok {
				wanted = append(wanted, v)
			}
		}
	}
	var match []*stored
	switch {
	case labelJQL.MatchString(jql):
		m := labelJQL.FindStringSubmatch(jql)
		project, label := unquote(m[1]), unquote(m[2])
		for _, st := range s.issues {
			if project == s.opts.Project && slices.Contains(st.labels, label) {
				match = append(match, st)
			}
		}
	case keyJQL.MatchString(jql):
		for _, q := range quoted.FindAllString(keyJQL.FindStringSubmatch(jql)[1], -1) {
			if st, ok := s.issues[unquote(q)]; ok {
				match = append(match, st)
			}
		}
	default:
		writeJSON(w, http.StatusBadRequest, errorBody("Error in the JQL Query: the stub understands only label and key searches."))
		return
	}
	sort.Slice(match, func(i, j int) bool { return number(match[i].key) < number(match[j].key) })

	offset := 0
	if tok, _ := in["nextPageToken"].(string); tok != "" {
		n, err := strconv.Atoi(strings.TrimPrefix(tok, "page-"))
		if err != nil || n < 0 {
			writeJSON(w, http.StatusBadRequest, errorBody("The next page token is not valid."))
			return
		}
		offset = n
	}
	size := s.opts.PageSize
	if m, ok := in["maxResults"].(float64); ok && int(m) > 0 {
		size = min(size, int(m))
	}
	end := min(offset+size, len(match))
	if offset > len(match) {
		offset = len(match)
	}
	issues := make([]map[string]any, 0, end-offset)
	for _, st := range match[offset:end] {
		issues = append(issues, s.render(st, wanted))
	}
	res := map[string]any{"issues": issues}
	if end < len(match) {
		res["nextPageToken"] = "page-" + strconv.Itoa(end)
	} else {
		res["isLast"] = true
	}
	writeJSON(w, http.StatusOK, res)
}

func number(key string) int {
	n, _ := strconv.Atoi(key[strings.LastIndex(key, "-")+1:])
	return n
}

func (s *Server) listTransitions(w http.ResponseWriter, key string) {
	st := s.lookup(key)
	if st == nil {
		writeJSON(w, http.StatusNotFound, errorBody("Issue does not exist or you do not have permission to see it."))
		return
	}
	list := []map[string]any{}
	for _, t := range s.workflow[st.status] {
		list = append(list, map[string]any{
			"id": t.id, "name": t.name, "hasScreen": false, "isAvailable": true,
			"to": map[string]any{"id": "3" + t.id, "name": t.to},
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"expand": "transitions", "transitions": list})
}

func (s *Server) transition(w http.ResponseWriter, key string, body any) {
	st := s.lookup(key)
	if st == nil {
		writeJSON(w, http.StatusNotFound, errorBody("Issue does not exist or you do not have permission to see it."))
		return
	}
	in, _ := body.(map[string]any)
	tr, _ := in["transition"].(map[string]any)
	id, _ := tr["id"].(string)
	for _, t := range s.workflow[st.status] {
		if t.id != id {
			continue
		}
		if s.required[[2]string{st.status, t.to}] {
			writeJSON(w, http.StatusBadRequest, fieldError("resolution", "Field 'resolution' is required"))
			return
		}
		st.status = t.to
		st.updated = s.tick()
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(w, http.StatusBadRequest, errorBody("Transition id '"+id+"' is not valid for this issue."))
}

func (s *Server) agile(w http.ResponseWriter, r *http.Request, path string, body any) {
	parts := strings.Split(path, "/")
	q := r.URL.Query()
	switch {
	case r.Method == "GET" && path == "board":
		var match []map[string]any
		for _, b := range s.boards {
			if b.project == q.Get("projectKeyOrId") && (q.Get("type") == "" || q.Get("type") == "scrum") {
				match = append(match, map[string]any{"id": b.id, "name": b.name, "type": "scrum"})
			}
		}
		s.page(w, q, match)
	case r.Method == "GET" && len(parts) == 3 && parts[0] == "board" && parts[2] == "sprint":
		id, _ := strconv.Atoi(parts[1])
		if !slices.ContainsFunc(s.boards, func(b *boardDef) bool { return b.id == id }) {
			writeJSON(w, http.StatusNotFound, errorBody("Board does not exist or you do not have permission to see it."))
			return
		}
		states := strings.Split(q.Get("state"), ",")
		var match []map[string]any
		for _, sp := range s.sprints {
			if sp.board == id && (q.Get("state") == "" || slices.Contains(states, sp.state)) {
				match = append(match, map[string]any{"id": sp.id, "name": sp.name, "state": sp.state, "originBoardId": id})
			}
		}
		s.page(w, q, match)
	case r.Method == "POST" && len(parts) == 3 && parts[0] == "sprint" && parts[2] == "issue":
		s.addToSprint(w, parts[1], body)
	default:
		writeJSON(w, http.StatusNotFound, errorBody("The requested resource does not exist."))
	}
}

func (s *Server) page(w http.ResponseWriter, q url.Values, all []map[string]any) {
	start, _ := strconv.Atoi(q.Get("startAt"))
	size := s.opts.PageSize
	if m, err := strconv.Atoi(q.Get("maxResults")); err == nil && m > 0 {
		size = min(size, m)
	}
	start = min(max(start, 0), len(all))
	end := min(start+size, len(all))
	values := all[start:end]
	if values == nil {
		values = []map[string]any{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"maxResults": size, "startAt": start, "isLast": end == len(all), "values": values})
}

func (s *Server) addToSprint(w http.ResponseWriter, rawID string, body any) {
	id, _ := strconv.Atoi(rawID)
	var sp *sprintDef
	for _, c := range s.sprints {
		if c.id == id {
			sp = c
		}
	}
	if sp == nil {
		writeJSON(w, http.StatusNotFound, errorBody("Sprint does not exist or you do not have permission to view it."))
		return
	}
	in, _ := body.(map[string]any)
	list, _ := in["issues"].([]any)
	if len(list) == 0 {
		writeJSON(w, http.StatusBadRequest, errorBody("issues must not be empty"))
		return
	}
	if sp.state == "closed" {
		writeJSON(w, http.StatusBadRequest, errorBody("Cannot add issues to a closed sprint."))
		return
	}
	var keys []string
	for _, v := range list {
		k, _ := v.(string)
		st := s.lookup(k)
		if st == nil {
			writeJSON(w, http.StatusNotFound, errorBody("Issue does not exist or you do not have permission to see it."))
			return
		}
		keys = append(keys, st.key)
	}
	for _, k := range keys {
		if !slices.Contains(sp.issues, k) {
			sp.issues = append(sp.issues, k)
		}
	}
	w.WriteHeader(http.StatusNoContent)
}
