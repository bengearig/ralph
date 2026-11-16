package github

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bengearig/ralph/internal/hosting"
)

func routeServer(t *testing.T, routes map[string]string) *Provider {
	t.Helper()
	return newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		body, ok := routes[r.URL.Path]
		if !ok {
			body = "[]"
		}
		_, _ = w.Write([]byte(body))
	})
}

func prServer(t *testing.T, body string, status int) *Provider {
	return newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/o/r/pulls/7" {
			t.Errorf("path = %s", r.URL.Path)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	})
}

func TestGitHubGetOpen(t *testing.T) {
	p := prServer(t, `{"number":7,"state":"open","html_url":"u","title":"T","head":{"ref":"h"},"base":{"ref":"main"}}`, 200)
	pr, err := p.GetPullRequest(context.Background(), 7)
	if err != nil || pr.State != hosting.StateOpen || pr.Number != 7 || pr.Head != "h" || pr.MergeCommit != "" {
		t.Errorf("pr = %+v, err = %v", pr, err)
	}
}

func TestGitHubGetMergedHasCommit(t *testing.T) {
	p := prServer(t, `{"number":7,"state":"closed","merged_at":"2026-01-02T03:04:05Z","merge_commit_sha":"abc"}`, 200)
	pr, err := p.GetPullRequest(context.Background(), 7)
	if err != nil || pr.State != hosting.StateMerged || pr.MergeCommit != "abc" {
		t.Errorf("pr = %+v, err = %v", pr, err)
	}
}

func TestGitHubGetClosedNotMerged(t *testing.T) {
	p := prServer(t, `{"number":7,"state":"closed","merged_at":null,"merge_commit_sha":"abc"}`, 200)
	pr, err := p.GetPullRequest(context.Background(), 7)
	if err != nil || pr.State != hosting.StateClosed || pr.MergeCommit != "" {
		t.Errorf("pr = %+v, err = %v", pr, err)
	}
}

func TestGitHubGetNotFound(t *testing.T) {
	p := prServer(t, `{"message":"Not Found"}`, 404)
	if _, err := p.GetPullRequest(context.Background(), 7); !errors.Is(err, hosting.ErrNotFound) {
		t.Errorf("err = %v", err)
	}
}

const (
	reviewsPath  = "/repos/o/r/pulls/7/reviews"
	inlinePath   = "/repos/o/r/pulls/7/comments"
	commentsPath = "/repos/o/r/issues/7/comments"
)

func feedbackOf(t *testing.T, p *Provider, opts hosting.FeedbackOptions) []hosting.Feedback {
	t.Helper()
	got, err := p.ReviewFeedback(context.Background(), 7, opts)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestGitHubFeedbackCombinesSources(t *testing.T) {
	p := routeServer(t, map[string]string{
		reviewsPath:  `[{"id":1,"user":{"login":"rev"},"body":"fix","state":"CHANGES_REQUESTED","html_url":"ru","submitted_at":"2026-01-01T00:00:01Z"}]`,
		inlinePath:   `[{"id":2,"user":{"login":"ann"},"body":"here","path":"a.go","line":12,"html_url":"iu","created_at":"2026-01-01T00:00:02Z"}]`,
		commentsPath: `[{"id":3,"user":{"login":"bob"},"body":"hi","html_url":"cu","created_at":"2026-01-01T00:00:03Z"}]`,
	})
	got := feedbackOf(t, p, hosting.FeedbackOptions{})
	if len(got) != 3 {
		t.Fatalf("got %+v", got)
	}
	want := []hosting.Feedback{
		{ID: "review-1", Kind: hosting.KindReview, Author: "rev", Body: "fix", State: hosting.ReviewChangesRequested, URL: "ru"},
		{ID: "inline-2", Kind: hosting.KindInline, Author: "ann", Body: "here", Path: "a.go", Line: 12, URL: "iu"},
		{ID: "comment-3", Kind: hosting.KindComment, Author: "bob", Body: "hi", URL: "cu"},
	}
	for i, w := range want {
		g := got[i]
		g.CreatedAt = time.Time{}
		if g != w {
			t.Errorf("item %d = %+v, want %+v", i, g, w)
		}
		if got[i].CreatedAt.IsZero() {
			t.Errorf("item %d has no time", i)
		}
	}
}

func TestGitHubFeedbackOrdering(t *testing.T) {
	p := routeServer(t, map[string]string{
		reviewsPath:  `[{"id":9,"user":{"login":"a"},"body":"x","state":"APPROVED","submitted_at":"2026-01-01T00:00:05Z"}]`,
		inlinePath:   `[{"id":1,"user":{"login":"a"},"body":"x","created_at":"2026-01-01T00:00:05Z"},{"id":2,"user":{"login":"a"},"body":"x","created_at":"2026-01-01T00:00:01Z"}]`,
		commentsPath: `[{"id":1,"user":{"login":"a"},"body":"x","created_at":"2026-01-01T00:00:05Z"}]`,
	})
	var ids []string
	for _, f := range feedbackOf(t, p, hosting.FeedbackOptions{}) {
		ids = append(ids, f.ID)
	}
	if got := strings.Join(ids, ","); got != "inline-2,comment-1,inline-1,review-9" {
		t.Errorf("order = %s", got)
	}
}

func TestGitHubFeedbackSkipsPendingAndEmptyCommentedReviews(t *testing.T) {
	p := routeServer(t, map[string]string{reviewsPath: `[
		{"id":1,"body":"draft","state":"PENDING","submitted_at":"2026-01-01T00:00:01Z"},
		{"id":2,"body":"","state":"COMMENTED","submitted_at":"2026-01-01T00:00:02Z"},
		{"id":3,"body":"note","state":"COMMENTED","submitted_at":"2026-01-01T00:00:03Z"},
		{"id":4,"body":"","state":"DISMISSED","submitted_at":"2026-01-01T00:00:04Z"}]`})
	got := feedbackOf(t, p, hosting.FeedbackOptions{})
	if len(got) != 1 || got[0].ID != "review-3" || got[0].State != hosting.ReviewCommented {
		t.Errorf("got %+v", got)
	}
}

func TestGitHubFeedbackKeepsEmptyChangesRequested(t *testing.T) {
	p := routeServer(t, map[string]string{reviewsPath: `[
		{"id":1,"body":"","state":"CHANGES_REQUESTED","submitted_at":"2026-01-01T00:00:01Z"},
		{"id":2,"body":"","state":"APPROVED","submitted_at":"2026-01-01T00:00:02Z"}]`})
	got := feedbackOf(t, p, hosting.FeedbackOptions{})
	if len(got) != 2 || got[0].State != hosting.ReviewChangesRequested || got[1].State != hosting.ReviewApproved {
		t.Errorf("got %+v", got)
	}
}

func TestGitHubFeedbackSince(t *testing.T) {
	p := routeServer(t, map[string]string{commentsPath: `[
		{"id":1,"body":"a","created_at":"2026-01-01T00:00:01Z"},
		{"id":2,"body":"b","created_at":"2026-01-01T00:00:02Z"},
		{"id":3,"body":"c","created_at":"2026-01-01T00:00:03Z"}]`})
	since := time.Date(2026, 1, 1, 0, 0, 2, 0, time.UTC)
	got := feedbackOf(t, p, hosting.FeedbackOptions{Since: since})
	if len(got) != 1 || got[0].ID != "comment-3" {
		t.Errorf("got %+v", got)
	}
}

func TestGitHubFeedbackPaginates(t *testing.T) {
	var base string
	p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != commentsPath {
			_, _ = w.Write([]byte("[]"))
			return
		}
		if r.URL.Query().Get("per_page") != "100" {
			t.Errorf("per_page = %q", r.URL.RawQuery)
		}
		page := r.URL.Query().Get("page")
		if page == "" {
			page = "1"
		}
		var n int
		_, _ = fmt.Sscan(page, &n)
		if n < 3 {
			w.Header().Set("Link", fmt.Sprintf(`<%s%s?per_page=100&page=%d>; rel="next", <%s%s?page=3>; rel="last"`, base, commentsPath, n+1, base, commentsPath))
		}
		_, _ = fmt.Fprintf(w, `[{"id":%d,"body":"x","created_at":"2026-01-01T00:00:0%dZ"}]`, n, n)
	})
	base = p.apiURL
	got := feedbackOf(t, p, hosting.FeedbackOptions{})
	if len(got) != 3 || got[2].ID != "comment-3" {
		t.Errorf("got %+v", got)
	}
}

func TestGitHubFeedbackPageLimit(t *testing.T) {
	var calls atomic.Int32
	p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != reviewsPath {
			_, _ = w.Write([]byte("[]"))
			return
		}
		n := calls.Add(1)
		w.Header().Set("Link", fmt.Sprintf(`<http://x%s?page=%d>; rel="next"`, reviewsPath, n+1))
		_, _ = w.Write([]byte("[]"))
	})
	_, err := p.ReviewFeedback(context.Background(), 7, hosting.FeedbackOptions{})
	if err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("err = %v", err)
	}
	if calls.Load() != 50 {
		t.Errorf("calls = %d", calls.Load())
	}
}

func TestGitHubFeedbackEmptyIsNotNil(t *testing.T) {
	got := feedbackOf(t, routeServer(t, nil), hosting.FeedbackOptions{})
	if got == nil || len(got) != 0 {
		t.Errorf("got %#v", got)
	}
}

func TestGitHubFeedbackIDsUniqueAcrossSources(t *testing.T) {
	p := routeServer(t, map[string]string{
		reviewsPath:  `[{"id":5,"body":"x","state":"APPROVED","submitted_at":"2026-01-01T00:00:01Z"}]`,
		inlinePath:   `[{"id":5,"body":"x","created_at":"2026-01-01T00:00:01Z"}]`,
		commentsPath: `[{"id":5,"body":"x","created_at":"2026-01-01T00:00:01Z"}]`,
	})
	seen := map[string]bool{}
	for _, f := range feedbackOf(t, p, hosting.FeedbackOptions{}) {
		seen[f.ID] = true
	}
	if len(seen) != 3 {
		t.Errorf("ids = %v", seen)
	}
}

func TestGitHubFeedbackInlineLineFallback(t *testing.T) {
	p := routeServer(t, map[string]string{inlinePath: `[
		{"id":1,"body":"a","line":null,"original_line":8,"created_at":"2026-01-01T00:00:01Z"},
		{"id":2,"body":"b","created_at":"2026-01-01T00:00:02Z"},
		{"id":3,"body":"c","line":4,"original_line":9,"created_at":"2026-01-01T00:00:03Z"}]`})
	got := feedbackOf(t, p, hosting.FeedbackOptions{})
	if got[0].Line != 8 || got[1].Line != 0 || got[2].Line != 4 {
		t.Errorf("lines = %d %d %d", got[0].Line, got[1].Line, got[2].Line)
	}
}

func TestGitHubFeedbackErrors(t *testing.T) {
	for _, failing := range []string{reviewsPath, inlinePath, commentsPath} {
		for status, want := range map[int]error{404: hosting.ErrNotFound, 429: nil} {
			p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == failing {
					w.WriteHeader(status)
					_, _ = w.Write([]byte(`{"message":"no"}`))
					return
				}
				_, _ = w.Write([]byte("[]"))
			})
			_, err := p.ReviewFeedback(context.Background(), 7, hosting.FeedbackOptions{})
			if status == 404 && !errors.Is(err, want) {
				t.Errorf("%s: 404 err = %v", failing, err)
			}
			var rl *hosting.RateLimitError
			if status == 429 && (!errors.As(err, &rl) || !errors.Is(err, hosting.ErrRateLimited)) {
				t.Errorf("%s: 429 err = %v", failing, err)
			}
		}
	}
}
