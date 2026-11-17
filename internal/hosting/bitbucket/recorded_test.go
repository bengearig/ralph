package bitbucket

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bengearig/ralph/internal/hosting"
)

const recordedRepoPath = "/repositories/acme/widgets"

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// recordedRoute serves a fixture for a request with the method and path and,
// when query is set, those query values. The first matching route wins.
type recordedRoute struct {
	method  string
	path    string
	query   url.Values
	status  int
	fixture string
	header  map[string]string
}

func (r recordedRoute) matches(req *http.Request) bool {
	if r.method != req.Method || recordedRepoPath+r.path != req.URL.Path {
		return false
	}
	got := req.URL.Query()
	for k, want := range r.query {
		if strings.Join(got[k], ",") != strings.Join(want, ",") {
			return false
		}
	}
	return true
}

// recordedProvider serves fixtures and returns the provider as a hosting.Provider.
// {{BASE}} in a fixture is replaced by the server's address.
func recordedProvider(t *testing.T, routes ...recordedRoute) hosting.Provider {
	t.Helper()
	var base string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer "+testToken {
			t.Errorf("Authorization = %q", got)
		}
		for _, route := range routes {
			if !route.matches(r) {
				continue
			}
			for k, v := range route.header {
				w.Header().Set(k, v)
			}
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			status := route.status
			if status == 0 {
				status = http.StatusOK
			}
			w.WriteHeader(status)
			_, _ = w.Write(bytes.ReplaceAll(fixture(t, route.fixture), []byte("{{BASE}}"), []byte(base)))
			return
		}
		t.Errorf("unexpected request %s %s", r.Method, r.URL.RequestURI())
		http.Error(w, `{"type":"error","error":{"message":"unexpected"}}`, http.StatusTeapot)
	}))
	t.Cleanup(srv.Close)
	base = srv.URL
	p, err := New(Config{APIURL: srv.URL, Workspace: "acme", Repo: "widgets", Token: tokenFn, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return ts
}

func wantPR(t *testing.T, got, want hosting.PullRequest) {
	t.Helper()
	if got != want {
		t.Errorf("pull request = %+v, want %+v", got, want)
	}
}

func get(path, fixture string) recordedRoute {
	return recordedRoute{method: "GET", path: path, fixture: fixture}
}

func TestRecordedBitbucketCreate(t *testing.T) {
	p := recordedProvider(t, recordedRoute{method: "POST", path: "/pullrequests", status: http.StatusCreated, fixture: "pr_created.json"})
	got, err := p.CreatePullRequest(context.Background(), hosting.NewPullRequest{Head: "feature-x", Base: "main", Title: "Add widget caching", Draft: true})
	if err != nil {
		t.Fatal(err)
	}
	wantPR(t, got, hosting.PullRequest{
		Number: 7, URL: "https://bitbucket.org/acme/widgets/pull-requests/7",
		Title: "Add widget caching", Body: "Caches widget lookups for a minute.\n\nCloses nothing yet.",
		Head: "feature-x", Base: "main", Draft: true, State: hosting.StateOpen, HeadCommit: "6f1e2d3c4b5a69788796a5b4c3d2e1f001122334",
	})
}

func TestRecordedBitbucketFindOpenAndClosed(t *testing.T) {
	p := recordedProvider(t,
		recordedRoute{method: "GET", path: "/pullrequests", query: url.Values{"state": {"OPEN"}}, fixture: "pr_list.json"},
	)
	got, found, err := p.FindPullRequest(context.Background(), "feature-x")
	if err != nil || !found {
		t.Fatalf("found = %v, err = %v", found, err)
	}
	if got.Number != 7 || got.State != hosting.StateOpen || got.Head != "feature-x" {
		t.Errorf("an open pull request must win over a declined one, got %+v", got)
	}

	p = recordedProvider(t,
		recordedRoute{method: "GET", path: "/pullrequests", query: url.Values{"state": {"OPEN"}}, fixture: "pr_list_empty.json"},
		recordedRoute{method: "GET", path: "/pullrequests", query: url.Values{"sort": {"-created_on"}}, fixture: "pr_list_closed.json"},
	)
	got, found, err = p.FindPullRequest(context.Background(), "feature-x")
	if err != nil || !found {
		t.Fatalf("found = %v, err = %v", found, err)
	}
	if got.Number != 6 || got.State != hosting.StateClosed || got.MergeCommit != "" {
		t.Errorf("closed-only list = %+v", got)
	}

	p = recordedProvider(t, recordedRoute{method: "GET", path: "/pullrequests", fixture: "pr_list_empty.json"})
	if got, found, err = p.FindPullRequest(context.Background(), "nobody"); err != nil || found {
		t.Errorf("empty list = %+v, %v, %v", got, found, err)
	}
}

func TestRecordedBitbucketGetStates(t *testing.T) {
	p := recordedProvider(t,
		get("/pullrequests/7", "pr_open.json"),
		get("/pullrequests/5", "pr_merged.json"),
		get("/pullrequests/6", "pr_declined.json"),
		get("/pullrequests/4", "pr_superseded.json"),
	)
	ctx := context.Background()
	cases := []struct {
		number int
		want   hosting.PullRequest
	}{
		{7, hosting.PullRequest{
			Number: 7, URL: "https://bitbucket.org/acme/widgets/pull-requests/7", Title: "Add widget caching",
			Body: "Caches widget lookups for a minute.\n\nCloses nothing yet.", Head: "feature-x", Base: "main", State: hosting.StateOpen, HeadCommit: "6f1e2d3c4b5a69788796a5b4c3d2e1f001122334",
		}},
		{5, hosting.PullRequest{
			Number: 5, URL: "https://bitbucket.org/acme/widgets/pull-requests/5", Title: "Fix widget label encoding",
			Body: "Labels were double encoded.", Head: "fix-labels", Base: "main", State: hosting.StateMerged,
			MergeCommit: "9e8d7c6b5a4f30211203f4e5d6c7b8a990817263", HeadCommit: "6f1e2d3c4b5a69788796a5b4c3d2e1f001122334",
		}},
		{6, hosting.PullRequest{
			Number: 6, URL: "https://bitbucket.org/acme/widgets/pull-requests/6", Title: "Try a different cache",
			Body: "An experiment.", Head: "feature-x", Base: "main", State: hosting.StateClosed, HeadCommit: "6f1e2d3c4b5a69788796a5b4c3d2e1f001122334",
		}},
		{4, hosting.PullRequest{
			Number: 4, URL: "https://bitbucket.org/acme/widgets/pull-requests/4", Title: "Old widget cache",
			Body: "Replaced by a rebased pull request.", Head: "feature-old", Base: "main", State: hosting.StateClosed, HeadCommit: "6f1e2d3c4b5a69788796a5b4c3d2e1f001122334",
		}},
	}
	for _, c := range cases {
		got, err := p.GetPullRequest(ctx, c.number)
		if err != nil {
			t.Fatalf("get #%d: %v", c.number, err)
		}
		wantPR(t, got, c.want)
	}
}

func TestRecordedBitbucketHeadCommit(t *testing.T) {
	p := recordedProvider(t, get("/pullrequests/5", "pr_merged.json"))
	got, err := p.GetPullRequest(context.Background(), 5)
	if err != nil {
		t.Fatal(err)
	}
	if got.HeadCommit != "6f1e2d3c4b5a69788796a5b4c3d2e1f001122334" {
		t.Errorf("head commit = %q", got.HeadCommit)
	}
	if got.HeadCommit == got.MergeCommit {
		t.Error("head commit must not be the merge commit")
	}
}

func recordedFeedbackRoutes() []recordedRoute {
	return []recordedRoute{
		{method: "GET", path: "/pullrequests/7/comments", query: url.Values{"page": {"2"}}, fixture: "comments_page2.json"},
		get("/pullrequests/7/comments", "comments_page1.json"),
		get("/pullrequests/7", "pr_open.json"),
	}
}

func TestRecordedBitbucketFeedback(t *testing.T) {
	p := recordedProvider(t, recordedFeedbackRoutes()...)
	got, err := p.ReviewFeedback(context.Background(), 7, hosting.FeedbackOptions{})
	if err != nil {
		t.Fatal(err)
	}
	const (
		bob  = "review-712020:22222222-aaaa-4bbb-8ccc-000000000002"
		dave = "review-712020:44444444-aaaa-4bbb-8ccc-000000000004"
		pull = "https://bitbucket.org/acme/widgets/pull-requests/7"
		diff = pull + "/_/diff#comment-"
	)
	want := []hosting.Feedback{
		{ID: "comment-1001", Kind: hosting.KindComment, Author: "Dave Reviewer", Body: "Why a minute and not configurable?",
			CreatedAt: mustTime(t, "2026-02-03T10:15:30.123456Z"), URL: pull + "#comment-1001"},
		{ID: "comment-1002", Kind: hosting.KindInline, Author: "Dave Reviewer", Body: "This map is never evicted.",
			Path: "src/cache.go", Line: 42, CreatedAt: mustTime(t, "2026-02-03T10:20:00Z"), URL: diff + "1002"},
		{ID: "comment-1003", Kind: hosting.KindInline, Author: "Dave Reviewer", Body: "This line was removed on purpose?",
			Path: "src/lookup.go", Line: 17, CreatedAt: mustTime(t, "2026-02-03T10:21:00Z"), URL: diff + "1003"},
		{ID: "comment-1004", Kind: hosting.KindComment, Author: "Alice Author", Body: "It will be configurable in a follow-up.",
			CreatedAt: mustTime(t, "2026-02-03T11:00:00Z"), URL: pull + "#comment-1004"},
		{ID: "comment-1007", Kind: hosting.KindInline, Author: "Carol Checker", Body: "Nit: name this ttl.",
			Path: "src/cache.go", Line: 5, CreatedAt: mustTime(t, "2026-02-03T12:00:00Z"), URL: diff + "1007"},
		{ID: "comment-1008", Kind: hosting.KindComment, Author: "Erin Example", Body: "Looks better. One more thing about the tests.",
			CreatedAt: mustTime(t, "2026-02-04T08:00:00Z"), URL: pull + "#comment-1008"},
		{ID: "comment-1009", Kind: hosting.KindInline, Author: "Erin Example", Body: "Both sides of the diff changed here.",
			Path: "src/cache_test.go", Line: 33, CreatedAt: mustTime(t, "2026-02-04T09:30:00Z"), URL: diff + "1009"},
		{ID: bob, Kind: hosting.KindReview, Author: "Bob Builder", State: hosting.ReviewChangesRequested,
			CreatedAt: mustTime(t, "2026-02-04T16:45:10.5Z"), URL: pull},
		{ID: dave, Kind: hosting.KindReview, Author: "Dave Reviewer", State: hosting.ReviewApproved,
			CreatedAt: mustTime(t, "2026-02-05T09:00:00Z"), URL: pull},
	}
	if len(got) != len(want) {
		t.Fatalf("feedback has %d items, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if !sameFeedback(got[i], want[i]) {
			t.Errorf("item %d = %+v\nwant     %+v", i, got[i], want[i])
		}
	}

	since, err := p.ReviewFeedback(context.Background(), 7, hosting.FeedbackOptions{Since: mustTime(t, "2026-02-04T09:30:00Z")})
	if err != nil || len(since) != 2 || since[0].ID != bob || since[1].ID != dave {
		t.Errorf("since = %+v, %v", since, err)
	}
}

func TestRecordedBitbucketUnknownFieldsIgnored(t *testing.T) {
	for _, name := range []string{"pr_created.json", "pr_open.json", "pr_merged.json", "pr_declined.json", "pr_superseded.json", "pr_list.json", "comments_page1.json"} {
		if n := len(fixture(t, name)); n < 2000 {
			t.Errorf("%s is only %d bytes; fixtures must be full-size", name, n)
		}
	}
	for name, extra := range map[string][]string{
		"pr_open.json":        {"rendered", "summary", "author", "reviewers", "closed_by", "reason", "queued", "close_source_branch", "task_count"},
		"comments_page1.json": {"pullrequest", "updated_on", "parent", "resolution"},
	} {
		raw := string(fixture(t, name))
		for _, field := range extra {
			if !strings.Contains(raw, `"`+field+`"`) {
				t.Errorf("%s does not carry %q", name, field)
			}
		}
	}

	var pj pullRequestJSON
	if err := json.Unmarshal(fixture(t, "pr_merged.json"), &pj); err != nil {
		t.Fatal(err)
	}
	if pj.ID != 5 || pj.Source.Branch.Name != "fix-labels" || pj.MergeCommit == nil || pj.Source.Repository.FullName != "acme/widgets" {
		t.Errorf("decoded = %+v", pj)
	}
	var pg page[commentJSON]
	if err := json.Unmarshal(fixture(t, "comments_page1.json"), &pg); err != nil {
		t.Fatal(err)
	}
	if len(pg.Values) != 7 || !pg.Values[4].Deleted || !pg.Values[5].Pending ||
		pg.Values[1].Inline == nil || pg.Values[1].Inline.From != nil || *pg.Values[1].Inline.To != 42 ||
		pg.Values[2].Inline.To != nil || *pg.Values[2].Inline.From != 17 {
		t.Errorf("comments = %+v", pg.Values)
	}
	var participants participantsJSON
	if err := json.Unmarshal(fixture(t, "pr_open.json"), &participants); err != nil || len(participants.Participants) != 4 {
		t.Errorf("participants = %+v, %v", participants, err)
	}
}

func TestRecordedBitbucketErrors(t *testing.T) {
	cases := []struct {
		name     string
		method   string
		path     string
		status   int
		fixture  string
		header   map[string]string
		want     error
		contains string
	}{
		{"401", "GET", "/pullrequests/1", http.StatusUnauthorized, "error_401.json", nil, hosting.ErrUnauthorized, "bitbucket.token"},
		{"403", "GET", "/pullrequests/1", http.StatusForbidden, "error_403.json", nil, hosting.ErrUnauthorized, "privilege scopes"},
		{"404", "GET", "/pullrequests/1", http.StatusNotFound, "error_404.json", nil, hosting.ErrNotFound, "Resource not found"},
		{"400 already exists", "POST", "/pullrequests", http.StatusBadRequest, "error_400_exists.json", nil, hosting.ErrConflict, "already exists"},
		{"400 fields", "POST", "/pullrequests", http.StatusBadRequest, "error_400_fields.json", nil, hosting.ErrInvalid, "source: You must specify a source branch."},
		{"429", "GET", "/pullrequests/1", http.StatusTooManyRequests, "error_429.json", map[string]string{"Retry-After": "30"}, hosting.ErrRateLimited, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := recordedProvider(t, recordedRoute{method: c.method, path: c.path, status: c.status, fixture: c.fixture, header: c.header})
			var err error
			if c.method == "POST" {
				_, err = p.CreatePullRequest(context.Background(), newPR())
			} else {
				_, err = p.GetPullRequest(context.Background(), 1)
			}
			if !errors.Is(err, c.want) {
				t.Fatalf("err = %v, want %v", err, c.want)
			}
			if c.contains != "" && !strings.Contains(err.Error(), c.contains) {
				t.Errorf("err = %q, want it to contain %q", err, c.contains)
			}
			if c.want == hosting.ErrRateLimited {
				var rl *hosting.RateLimitError
				if !errors.As(err, &rl) || time.Until(rl.ResetAt) < 20*time.Second || time.Until(rl.ResetAt) > 31*time.Second {
					t.Errorf("rate limit error = %#v", err)
				}
			}
		})
	}
}

func TestRecordedBitbucketUsesProviderInterface(t *testing.T) {
	routes := append(recordedFeedbackRoutes(),
		recordedRoute{method: "POST", path: "/pullrequests", status: http.StatusCreated, fixture: "pr_created.json"},
		recordedRoute{method: "GET", path: "/pullrequests", query: url.Values{"state": {"OPEN"}}, fixture: "pr_list.json"},
	)
	p := recordedProvider(t, routes...)
	ctx := context.Background()

	created, err := p.CreatePullRequest(ctx, newPR())
	if err != nil || created.Number != 7 {
		t.Fatalf("create = %+v, %v", created, err)
	}
	found, ok, err := p.FindPullRequest(ctx, "feature-x")
	if err != nil || !ok || found.Number != created.Number {
		t.Fatalf("find = %+v, %v, %v", found, ok, err)
	}
	got, err := p.GetPullRequest(ctx, found.Number)
	if err != nil || got.State != hosting.StateOpen {
		t.Fatalf("get = %+v, %v", got, err)
	}
	fb, err := p.ReviewFeedback(ctx, got.Number, hosting.FeedbackOptions{})
	if err != nil || len(fb) != 9 {
		t.Fatalf("feedback = %d items, %v", len(fb), err)
	}
}
