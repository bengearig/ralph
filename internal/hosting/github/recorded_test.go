package github

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/bengearig/ralph/internal/hosting"
)

const recordedRepoPath = "/repos/o/r"

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

type recordedRoute struct {
	status  int
	fixture string
	header  map[string]string
}

// recordedProvider serves fixtures by "METHOD /path" and returns the provider as a hosting.Provider.
func recordedProvider(t *testing.T, routes map[string]recordedRoute) hosting.Provider {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		route, ok := routes[r.Method+" "+r.URL.Path]
		if !ok {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.Error(w, `{"message":"unexpected"}`, http.StatusTeapot)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+testToken {
			t.Errorf("Authorization = %q", got)
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
		_, _ = w.Write(fixture(t, route.fixture))
	}))
	t.Cleanup(srv.Close)
	p, err := New(Config{APIURL: srv.URL, Owner: "o", Repo: "r", Token: staticToken(""), Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func wantPR(t *testing.T, got, want hosting.PullRequest) {
	t.Helper()
	if got != want {
		t.Errorf("pull request = %+v, want %+v", got, want)
	}
}

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return ts
}

func TestRecordedCreate(t *testing.T) {
	p := recordedProvider(t, map[string]recordedRoute{
		"POST " + recordedRepoPath + "/pulls": {status: http.StatusCreated, fixture: "pull_created.json"},
	})
	got, err := p.CreatePullRequest(context.Background(), hosting.NewPullRequest{Head: "feature-x", Base: "main", Title: "Amazing new feature", Draft: true})
	if err != nil {
		t.Fatal(err)
	}
	wantPR(t, got, hosting.PullRequest{
		Number: 1347, URL: "https://github.com/octo-org/hello-world/pull/1347",
		Title: "Amazing new feature", Body: "Please pull these awesome changes in!",
		Head: "feature-x", Base: "main", Draft: true, State: hosting.StateOpen,
	})
}

func TestRecordedFindOpenAndClosed(t *testing.T) {
	p := recordedProvider(t, map[string]recordedRoute{"GET " + recordedRepoPath + "/pulls": {fixture: "pulls_list.json"}})
	got, found, err := p.FindPullRequest(context.Background(), "feature-x")
	if err != nil || !found {
		t.Fatalf("found = %v, err = %v", found, err)
	}
	if got.Number != 1347 || got.State != hosting.StateOpen {
		t.Errorf("an open pull request must win over a newer closed one, got %+v", got)
	}

	p = recordedProvider(t, map[string]recordedRoute{"GET " + recordedRepoPath + "/pulls": {fixture: "pulls_list_closed_only.json"}})
	got, found, err = p.FindPullRequest(context.Background(), "feature-x")
	if err != nil || !found {
		t.Fatalf("found = %v, err = %v", found, err)
	}
	if got.Number != 1350 || got.State != hosting.StateClosed || got.MergeCommit != "" {
		t.Errorf("closed-only list = %+v", got)
	}
}

func TestRecordedGetOpenMergedClosed(t *testing.T) {
	p := recordedProvider(t, map[string]recordedRoute{
		"GET " + recordedRepoPath + "/pulls/1347": {fixture: "pull_open.json"},
		"GET " + recordedRepoPath + "/pulls/1349": {fixture: "pull_merged.json"},
		"GET " + recordedRepoPath + "/pulls/1351": {fixture: "pull_closed.json"},
	})
	cases := []struct {
		number int
		want   hosting.PullRequest
	}{
		{1347, hosting.PullRequest{Number: 1347, URL: "https://github.com/octo-org/hello-world/pull/1347", Title: "Amazing new feature", Body: "Please pull these awesome changes in!", Head: "feature-x", Base: "main", State: hosting.StateOpen}},
		{1349, hosting.PullRequest{Number: 1349, URL: "https://github.com/octo-org/hello-world/pull/1349", Title: "Fix the thing", Body: "Fixes #12.", Head: "feature-y", Base: "main", State: hosting.StateMerged, MergeCommit: "a1b2c3d4e5f60718293a4b5c6d7e8f9012345678"}},
		{1351, hosting.PullRequest{Number: 1351, URL: "https://github.com/octo-org/hello-world/pull/1351", Title: "Abandoned idea", Body: "Never mind.", Head: "feature-z", Base: "main", State: hosting.StateClosed}},
	}
	for _, c := range cases {
		t.Run(strconv.Itoa(c.number), func(t *testing.T) {
			got, err := p.GetPullRequest(context.Background(), c.number)
			if err != nil {
				t.Fatal(err)
			}
			wantPR(t, got, c.want)
		})
	}
}

func feedbackRoutes() map[string]recordedRoute {
	return map[string]recordedRoute{
		"GET " + recordedRepoPath + "/pulls/1347/reviews":   {fixture: "reviews.json"},
		"GET " + recordedRepoPath + "/pulls/1347/comments":  {fixture: "pull_comments.json"},
		"GET " + recordedRepoPath + "/issues/1347/comments": {fixture: "issue_comments.json"},
	}
}

func TestRecordedFeedbackAllSources(t *testing.T) {
	p := recordedProvider(t, feedbackRoutes())
	got, err := p.ReviewFeedback(context.Background(), 1347, hosting.FeedbackOptions{})
	if err != nil {
		t.Fatal(err)
	}
	const pr = "https://github.com/octo-org/hello-world/pull/1347#"
	want := []hosting.Feedback{
		{ID: "comment-302", Kind: hosting.KindComment, Author: "bob", Body: "CI is green now.", CreatedAt: mustTime(t, "2026-11-13T08:00:00Z"), URL: pr + "issuecomment-302"},
		{ID: "review-80", Kind: hosting.KindReview, Author: "bob", Body: "Looks good to me.", State: hosting.ReviewApproved, CreatedAt: mustTime(t, "2026-11-13T09:00:00Z"), URL: pr + "pullrequestreview-80"},
		{ID: "inline-201", Kind: hosting.KindInline, Author: "bob", Body: "Rename this variable.", Path: "internal/a.go", Line: 10, CreatedAt: mustTime(t, "2026-11-13T09:30:00Z"), URL: pr + "discussion_r201"},
		{ID: "inline-202", Kind: hosting.KindInline, Author: "alice", Body: "This block is outdated but the comment stands.", Path: "internal/b.go", Line: 8, CreatedAt: mustTime(t, "2026-11-13T10:30:00Z"), URL: pr + "discussion_r202"},
		{ID: "review-81", Kind: hosting.KindReview, Author: "alice", Body: "Please add a test for the empty case.", State: hosting.ReviewChangesRequested, CreatedAt: mustTime(t, "2026-11-13T11:00:00Z"), URL: pr + "pullrequestreview-81"},
		{ID: "inline-203", Kind: hosting.KindInline, Author: "bob", Body: "Moved line wins over the original.", Path: "internal/c.go", Line: 12, CreatedAt: mustTime(t, "2026-11-13T13:30:00Z"), URL: pr + "discussion_r203"},
		{ID: "inline-204", Kind: hosting.KindInline, Author: "octo-org", Body: "File-level comment with no line.", Path: "internal/d.go", Line: 0, CreatedAt: mustTime(t, "2026-11-13T14:30:00Z"), URL: pr + "discussion_r204"},
		{ID: "comment-301", Kind: hosting.KindComment, Author: "alice", Body: "Is this ready for another look?", CreatedAt: mustTime(t, "2026-11-13T15:00:00Z"), URL: pr + "issuecomment-301"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d items, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("item %d = %+v, want %+v", i, got[i], want[i])
		}
	}

	since, err := p.ReviewFeedback(context.Background(), 1347, hosting.FeedbackOptions{Since: mustTime(t, "2026-11-13T11:00:00Z")})
	if err != nil {
		t.Fatal(err)
	}
	if len(since) != 3 || since[0].ID != "inline-203" {
		t.Errorf("since filter = %+v", since)
	}
}

func TestRecordedUnknownFieldsIgnored(t *testing.T) {
	for _, name := range []string{"pull_created.json", "pull_open.json", "pull_merged.json", "pull_closed.json", "pulls_list.json", "reviews.json", "pull_comments.json", "issue_comments.json"} {
		if n := len(fixture(t, name)); n < 2000 {
			t.Errorf("%s is only %d bytes; fixtures must be full-size", name, n)
		}
	}
	var pj pullRequestJSON
	if err := json.Unmarshal(fixture(t, "pull_merged.json"), &pj); err != nil {
		t.Fatal(err)
	}
	if pj.Number != 1349 || pj.Head.Ref != "feature-y" || pj.MergedAt == nil {
		t.Errorf("decoded = %+v", pj)
	}
	var comments []commentJSON
	if err := json.Unmarshal(fixture(t, "pull_comments.json"), &comments); err != nil {
		t.Fatal(err)
	}
	if len(comments) != 4 || comments[1].Line != nil || comments[1].OriginalLine == nil || *comments[1].OriginalLine != 8 {
		t.Errorf("comments = %+v", comments)
	}
}

func TestRecordedErrors(t *testing.T) {
	cases := []struct {
		name    string
		method  string
		path    string
		status  int
		fixture string
		header  map[string]string
		want    error
	}{
		{"401", "GET", "/pulls/1", http.StatusUnauthorized, "error_401.json", nil, hosting.ErrUnauthorized},
		{"403 rate limit", "GET", "/pulls/1", http.StatusForbidden, "error_403_rate_limit.json", map[string]string{"X-RateLimit-Remaining": "0", "X-RateLimit-Reset": "1800000000"}, hosting.ErrRateLimited},
		{"404", "GET", "/pulls/1", http.StatusNotFound, "error_404.json", nil, hosting.ErrNotFound},
		{"422 already exists", "POST", "/pulls", http.StatusUnprocessableEntity, "error_422_exists.json", nil, hosting.ErrConflict},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := recordedProvider(t, map[string]recordedRoute{c.method + " " + recordedRepoPath + c.path: {status: c.status, fixture: c.fixture, header: c.header}})
			var err error
			if c.method == "POST" {
				_, err = p.CreatePullRequest(context.Background(), newPR())
			} else {
				_, err = p.GetPullRequest(context.Background(), 1)
			}
			if !errors.Is(err, c.want) {
				t.Fatalf("err = %v, want %v", err, c.want)
			}
			if c.want == hosting.ErrRateLimited {
				var rl *hosting.RateLimitError
				if !errors.As(err, &rl) || !rl.ResetAt.Equal(time.Unix(1800000000, 0)) {
					t.Errorf("rate limit error = %#v", err)
				}
			}
		})
	}
}

func TestRecordedUsesProviderInterface(t *testing.T) {
	routes := feedbackRoutes()
	routes["POST "+recordedRepoPath+"/pulls"] = recordedRoute{status: http.StatusCreated, fixture: "pull_created.json"}
	routes["GET "+recordedRepoPath+"/pulls"] = recordedRoute{fixture: "pulls_list.json"}
	routes["GET "+recordedRepoPath+"/pulls/1347"] = recordedRoute{fixture: "pull_open.json"}
	p := recordedProvider(t, routes)
	ctx := context.Background()

	created, err := p.CreatePullRequest(ctx, newPR())
	if err != nil || created.Number != 1347 {
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
	if err != nil || len(fb) != 8 {
		t.Fatalf("feedback = %d items, %v", len(fb), err)
	}
}
