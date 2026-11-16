package bitbucket

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bengearig/ralph/internal/hosting"
)

const (
	commentsPath = "/repositories/ws/rp/pullrequests/7/comments"
	prPath       = "/repositories/ws/rp/pullrequests/7"
	prURL        = "https://bitbucket.org/ws/rp/pull-requests/7"
)

type fakeFeedback struct {
	pages        [][]map[string]any
	participants []map[string]any
}

func (f fakeFeedback) handler(base func() string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case commentsPath:
			idx := 0
			if r.URL.Query().Get("page") != "" {
				idx = int(r.URL.Query().Get("page")[0] - '0')
			}
			body := map[string]any{"values": f.pages[idx]}
			if idx+1 < len(f.pages) {
				body["next"] = base() + commentsPath + "?page=" + string(rune('0'+idx+1))
			}
			writeJSON(w, 200, body)
		case prPath:
			writeJSON(w, 200, map[string]any{
				"id": 7, "participants": f.participants,
				"links": map[string]any{"html": map[string]any{"href": prURL}},
			})
		default:
			http.NotFound(w, r)
		}
	}
}

func feedbackProvider(t *testing.T, f fakeFeedback) *Provider {
	t.Helper()
	var url string
	return newTestProvider(t, f.handler(func() string { return url }), func(c *Config) { url = c.APIURL })
}

func comment(id int, created, raw string, extra map[string]any) map[string]any {
	c := map[string]any{
		"id": id, "created_on": created, "content": map[string]any{"raw": raw},
		"user":  map[string]any{"display_name": "Ann"},
		"links": map[string]any{"html": map[string]any{"href": "https://bitbucket.org/c/" + raw}},
	}
	for k, v := range extra {
		c[k] = v
	}
	return c
}

func sameFeedback(a, b hosting.Feedback) bool {
	if !a.CreatedAt.Equal(b.CreatedAt) {
		return false
	}
	a.CreatedAt, b.CreatedAt = time.Time{}, time.Time{}
	return a == b
}

func read(t *testing.T, p *Provider, opts hosting.FeedbackOptions) []hosting.Feedback {
	t.Helper()
	got, err := p.ReviewFeedback(context.Background(), 7, opts)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestBitbucketFeedbackCommentsAndInline(t *testing.T) {
	p := feedbackProvider(t, fakeFeedback{pages: [][]map[string]any{{
		comment(1, "2026-01-02T03:04:05.123456+00:00", "general", nil),
		comment(2, "2026-01-02T03:04:06Z", "inline", map[string]any{"inline": map[string]any{"path": "a/b.go", "from": 3, "to": 9}}),
	}}})
	got := read(t, p, hosting.FeedbackOptions{})
	if len(got) != 2 {
		t.Fatalf("got %+v", got)
	}
	want0 := hosting.Feedback{ID: "comment-1", Kind: hosting.KindComment, Author: "Ann", Body: "general",
		URL: "https://bitbucket.org/c/general", CreatedAt: time.Date(2026, 1, 2, 3, 4, 5, 123456000, time.UTC)}
	if !sameFeedback(got[0], want0) {
		t.Fatalf("got %+v want %+v", got[0], want0)
	}
	want1 := hosting.Feedback{ID: "comment-2", Kind: hosting.KindInline, Author: "Ann", Body: "inline", Path: "a/b.go", Line: 9,
		URL: "https://bitbucket.org/c/inline", CreatedAt: time.Date(2026, 1, 2, 3, 4, 6, 0, time.UTC)}
	if !sameFeedback(got[1], want1) {
		t.Fatalf("got %+v want %+v", got[1], want1)
	}
}

func TestBitbucketFeedbackInlineLineFallsBackToFrom(t *testing.T) {
	p := feedbackProvider(t, fakeFeedback{pages: [][]map[string]any{{
		comment(1, "2026-01-01T00:00:01Z", "a", map[string]any{"inline": map[string]any{"path": "x", "from": 4, "to": nil}}),
		comment(2, "2026-01-01T00:00:02Z", "b", map[string]any{"inline": map[string]any{"path": "x"}}),
	}}})
	got := read(t, p, hosting.FeedbackOptions{})
	if got[0].Line != 4 || got[1].Line != 0 || got[1].Kind != hosting.KindInline {
		t.Fatalf("got %+v", got)
	}
}

func TestBitbucketFeedbackSkipsDeletedAndPending(t *testing.T) {
	p := feedbackProvider(t, fakeFeedback{pages: [][]map[string]any{{
		comment(1, "2026-01-01T00:00:01Z", "a", map[string]any{"deleted": true}),
		comment(2, "2026-01-01T00:00:02Z", "b", map[string]any{"pending": true}),
		comment(3, "2026-01-01T00:00:03Z", "c", nil),
	}}})
	got := read(t, p, hosting.FeedbackOptions{})
	if len(got) != 1 || got[0].ID != "comment-3" {
		t.Fatalf("got %+v", got)
	}
}

func TestBitbucketFeedbackIncludesRepliesAndResolved(t *testing.T) {
	p := feedbackProvider(t, fakeFeedback{pages: [][]map[string]any{{
		comment(1, "2026-01-01T00:00:01Z", "a", map[string]any{"resolution": map[string]any{"type": "comment_resolution"}}),
		comment(2, "2026-01-01T00:00:02Z", "b", map[string]any{"parent": map[string]any{"id": 1}}),
	}}})
	if got := read(t, p, hosting.FeedbackOptions{}); len(got) != 2 {
		t.Fatalf("got %+v", got)
	}
}

func participant(id, state string, approved bool, on any) map[string]any {
	return map[string]any{
		"user":  map[string]any{"display_name": "User " + id, "account_id": id},
		"state": state, "approved": approved, "participated_on": on,
	}
}

func TestBitbucketFeedbackVerdictsFromParticipants(t *testing.T) {
	p := feedbackProvider(t, fakeFeedback{pages: [][]map[string]any{{}}, participants: []map[string]any{
		participant("a1", "changes_requested", false, "2026-02-01T00:00:00Z"),
		participant("a2", "approved", true, "2026-02-02T00:00:00.5Z"),
		participant("a3", "", true, "2026-02-03T00:00:00Z"),
		participant("a4", "", false, "2026-02-04T00:00:00Z"),
	}})
	got := read(t, p, hosting.FeedbackOptions{})
	if len(got) != 3 {
		t.Fatalf("got %+v", got)
	}
	want := []hosting.Feedback{
		{ID: "review-a1", Kind: hosting.KindReview, Author: "User a1", State: hosting.ReviewChangesRequested, URL: prURL, CreatedAt: time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)},
		{ID: "review-a2", Kind: hosting.KindReview, Author: "User a2", State: hosting.ReviewApproved, URL: prURL, CreatedAt: time.Date(2026, 2, 2, 0, 0, 0, 500000000, time.UTC)},
		{ID: "review-a3", Kind: hosting.KindReview, Author: "User a3", State: hosting.ReviewApproved, URL: prURL, CreatedAt: time.Date(2026, 2, 3, 0, 0, 0, 0, time.UTC)},
	}
	for i := range want {
		if !sameFeedback(got[i], want[i]) {
			t.Fatalf("[%d] got %+v want %+v", i, got[i], want[i])
		}
	}
}

func TestBitbucketFeedbackAuthorFallbacks(t *testing.T) {
	users := []map[string]any{{"display_name": "Disp", "nickname": "nick"}, {"nickname": "nick"}, {}}
	var cs []map[string]any
	for i, u := range users {
		cs = append(cs, comment(i+1, "2026-01-01T00:00:0"+string(rune('1'+i))+"Z", "x", map[string]any{"user": u}))
	}
	got := read(t, feedbackProvider(t, fakeFeedback{pages: [][]map[string]any{cs}}), hosting.FeedbackOptions{})
	for i, want := range []string{"Disp", "nick", "unknown"} {
		if got[i].Author != want {
			t.Fatalf("[%d] author %q want %q", i, got[i].Author, want)
		}
	}
}

func TestBitbucketFeedbackOrdering(t *testing.T) {
	p := feedbackProvider(t, fakeFeedback{
		pages: [][]map[string]any{{
			comment(2, "2026-01-01T00:00:05Z", "a", nil),
			comment(1, "2026-01-01T00:00:05Z", "b", nil),
			comment(3, "2026-01-01T00:00:01Z", "c", nil),
		}},
		participants: []map[string]any{participant("z", "approved", true, "2026-01-01T00:00:03Z")},
	})
	var ids []string
	for _, f := range read(t, p, hosting.FeedbackOptions{}) {
		ids = append(ids, f.ID)
	}
	if strings.Join(ids, ",") != "comment-3,review-z,comment-1,comment-2" {
		t.Fatalf("order %v", ids)
	}
}

func TestBitbucketFeedbackSince(t *testing.T) {
	p := feedbackProvider(t, fakeFeedback{
		pages: [][]map[string]any{{
			comment(1, "2026-01-01T00:00:01Z", "a", nil),
			comment(2, "2026-01-01T00:00:02Z", "b", nil),
			comment(3, "2026-01-01T00:00:03Z", "c", nil),
		}},
		participants: []map[string]any{participant("z", "approved", true, nil)},
	})
	got := read(t, p, hosting.FeedbackOptions{Since: time.Date(2026, 1, 1, 0, 0, 2, 0, time.UTC)})
	if len(got) != 1 || got[0].ID != "comment-3" {
		t.Fatalf("got %+v", got)
	}
}

func TestBitbucketFeedbackPaginatesComments(t *testing.T) {
	p := feedbackProvider(t, fakeFeedback{pages: [][]map[string]any{
		{comment(1, "2026-01-01T00:00:01Z", "a", nil)},
		{comment(2, "2026-01-01T00:00:02Z", "b", nil)},
		{comment(3, "2026-01-01T00:00:03Z", "c", nil)},
	}})
	if got := read(t, p, hosting.FeedbackOptions{}); len(got) != 3 {
		t.Fatalf("got %+v", got)
	}
}

func TestBitbucketFeedbackPageLimit(t *testing.T) {
	var calls atomic.Int32
	var base string
	p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		writeJSON(w, 200, map[string]any{"values": []any{}, "next": base + commentsPath + "?again=1"})
	}, func(c *Config) { base = c.APIURL })
	_, err := p.ReviewFeedback(context.Background(), 7, hosting.FeedbackOptions{})
	if err == nil || calls.Load() != 50 {
		t.Fatalf("err = %v calls = %d", err, calls.Load())
	}
}

func TestBitbucketFeedbackNextOtherHostRefused(t *testing.T) {
	var other atomic.Int32
	o := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { other.Add(1) }))
	t.Cleanup(o.Close)
	p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"values": []any{}, "next": o.URL + "/x"})
	})
	_, err := p.ReviewFeedback(context.Background(), 7, hosting.FeedbackOptions{})
	if err == nil || other.Load() != 0 {
		t.Fatalf("err = %v other = %d", err, other.Load())
	}
}

func TestBitbucketFeedbackEmptyIsNotNil(t *testing.T) {
	got := read(t, feedbackProvider(t, fakeFeedback{pages: [][]map[string]any{{}}}), hosting.FeedbackOptions{})
	if got == nil || len(got) != 0 {
		t.Fatalf("got %#v", got)
	}
}

func TestBitbucketFeedbackIDsUniqueAcrossSources(t *testing.T) {
	p := feedbackProvider(t, fakeFeedback{
		pages:        [][]map[string]any{{comment(5, "2026-01-01T00:00:01Z", "a", nil)}},
		participants: []map[string]any{participant("5", "approved", true, "2026-01-01T00:00:01Z")},
	})
	got := read(t, p, hosting.FeedbackOptions{})
	if len(got) != 2 || got[0].ID == got[1].ID {
		t.Fatalf("got %+v", got)
	}
}

func TestBitbucketFeedbackBadTimestampNamesField(t *testing.T) {
	_, err := feedbackProvider(t, fakeFeedback{pages: [][]map[string]any{{comment(9, "yesterday", "a", nil)}}}).
		ReviewFeedback(context.Background(), 7, hosting.FeedbackOptions{})
	if err == nil || !strings.Contains(err.Error(), "created_on") || !strings.Contains(err.Error(), "comment-9") {
		t.Fatalf("err = %v", err)
	}
	_, err = feedbackProvider(t, fakeFeedback{pages: [][]map[string]any{{}}, participants: []map[string]any{participant("q", "approved", true, "nope")}}).
		ReviewFeedback(context.Background(), 7, hosting.FeedbackOptions{})
	if err == nil || !strings.Contains(err.Error(), "participated_on") || !strings.Contains(err.Error(), "review-q") {
		t.Fatalf("err = %v", err)
	}
}

func TestBitbucketFeedbackErrors(t *testing.T) {
	cases := []struct {
		name   string
		path   string
		status int
		check  func(error) bool
	}{
		{"comments 404", commentsPath, 404, func(e error) bool { return errors.Is(e, hosting.ErrNotFound) }},
		{"pull request 404", prPath, 404, func(e error) bool { return errors.Is(e, hosting.ErrNotFound) }},
		{"comments 429", commentsPath, 429, func(e error) bool { var r *hosting.RateLimitError; return errors.As(e, &r) }},
		{"pull request 429", prPath, 429, func(e error) bool { var r *hosting.RateLimitError; return errors.As(e, &r) }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == c.path {
					writeJSON(w, c.status, errBody("nope", nil))
					return
				}
				writeJSON(w, 200, map[string]any{"values": []any{}})
			})
			_, err := p.ReviewFeedback(context.Background(), 7, hosting.FeedbackOptions{})
			if err == nil || !c.check(err) {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestBitbucketFeedbackUsesBearerOrBasicLikeOtherCalls(t *testing.T) {
	for _, user := range []string{"", testEmail} {
		var create, feedback []string
		p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost {
				create = append(create, r.Header.Get("Authorization"))
				writeJSON(w, 200, prBody(7, "OPEN", "feat", "ws/rp"))
				return
			}
			feedback = append(feedback, r.Header.Get("Authorization"))
			writeJSON(w, 200, map[string]any{"values": []any{}})
		}, func(c *Config) { c.User = user })
		if _, err := p.CreatePullRequest(context.Background(), newPR()); err != nil {
			t.Fatal(err)
		}
		read(t, p, hosting.FeedbackOptions{})
		if len(create) != 1 || len(feedback) != 2 || feedback[0] != create[0] || feedback[1] != create[0] {
			t.Fatalf("user %q: create %v feedback %v", user, create, feedback)
		}
	}
}
