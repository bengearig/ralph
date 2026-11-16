package fake

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bengearig/ralph/internal/hosting"
)

var _ hosting.Provider = (*Provider)(nil)

var ctx = context.Background()

func TestFakeImplementsProvider(t *testing.T) {
	var _ hosting.Provider = New()
}

func create(t *testing.T, p *Provider, head string) hosting.PullRequest {
	t.Helper()
	pr, err := p.CreatePullRequest(ctx, hosting.NewPullRequest{Head: head, Base: "main", Title: "t", Body: "b", Draft: true})
	if err != nil {
		t.Fatal(err)
	}
	return pr
}

func TestFakeCreateAndGet(t *testing.T) {
	p := New()
	pr := create(t, p, "a")
	if pr.Number != 1 || pr.State != hosting.StateOpen || !pr.Draft || pr.Head != "a" || pr.Base != "main" {
		t.Fatalf("pr = %+v", pr)
	}
	if create(t, p, "b").Number != 2 {
		t.Fatal("second number should be 2")
	}
	got, err := p.GetPullRequest(ctx, 1)
	if err != nil || got != pr {
		t.Fatalf("get = %+v, %v", got, err)
	}
	if len(p.Calls()) != 3 {
		t.Fatalf("calls = %+v", p.Calls())
	}
}

func TestFakeCreateConflict(t *testing.T) {
	p := New()
	create(t, p, "a")
	_, err := p.CreatePullRequest(ctx, hosting.NewPullRequest{Head: "a"})
	if !errors.Is(err, hosting.ErrConflict) {
		t.Fatalf("err = %v", err)
	}
	p.Close(1)
	if _, err := p.CreatePullRequest(ctx, hosting.NewPullRequest{Head: "a"}); err != nil {
		t.Fatalf("after close: %v", err)
	}
}

func TestFakeFindPrefersOpen(t *testing.T) {
	p := New()
	create(t, p, "a")
	p.Close(1)
	create(t, p, "a")
	p.Merge(2)
	open := create(t, p, "a")
	create(t, p, "b")
	got, ok, err := p.FindPullRequest(ctx, "a")
	if err != nil || !ok || got.Number != open.Number {
		t.Fatalf("find = %+v, %v, %v", got, ok, err)
	}
	p.Close(open.Number)
	got, ok, _ = p.FindPullRequest(ctx, "a")
	if !ok || got.Number != open.Number {
		t.Fatalf("newest closed = %+v", got)
	}
}

func TestFakeFindNone(t *testing.T) {
	_, ok, err := New().FindPullRequest(ctx, "x")
	if ok || err != nil {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
}

func TestFakeNotFound(t *testing.T) {
	p := New()
	if _, err := p.GetPullRequest(ctx, 7); !errors.Is(err, hosting.ErrNotFound) {
		t.Fatalf("get err = %v", err)
	}
	if _, err := p.ReviewFeedback(ctx, 0, hosting.FeedbackOptions{}); !errors.Is(err, hosting.ErrNotFound) {
		t.Fatalf("feedback err = %v", err)
	}
}

func TestFakeMergeAndClose(t *testing.T) {
	p := New()
	create(t, p, "a")
	create(t, p, "b")
	p.Merge(1)
	p.Close(2)
	m, _ := p.GetPullRequest(ctx, 1)
	c, _ := p.GetPullRequest(ctx, 2)
	if m.State != hosting.StateMerged || m.MergeCommit == "" || c.State != hosting.StateClosed || c.MergeCommit != "" {
		t.Fatalf("merged=%+v closed=%+v", m, c)
	}
}

func TestFakeFeedbackSinceAndOrder(t *testing.T) {
	p := New()
	create(t, p, "a")
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	p.AddFeedback(1, hosting.Feedback{ID: "c", CreatedAt: t0.Add(2 * time.Hour)})
	p.AddFeedback(1, hosting.Feedback{ID: "b", CreatedAt: t0.Add(time.Hour)})
	p.AddFeedback(1, hosting.Feedback{ID: "a", CreatedAt: t0.Add(time.Hour)})
	p.AddFeedback(1, hosting.Feedback{ID: "z", CreatedAt: t0})
	ids := func(fs []hosting.Feedback) string {
		s := ""
		for _, f := range fs {
			s += f.ID
		}
		return s
	}
	all, err := p.ReviewFeedback(ctx, 1, hosting.FeedbackOptions{})
	if err != nil || ids(all) != "zabc" {
		t.Fatalf("all = %q, %v", ids(all), err)
	}
	since, _ := p.ReviewFeedback(ctx, 1, hosting.FeedbackOptions{Since: t0.Add(time.Hour)})
	if ids(since) != "c" {
		t.Fatalf("since = %q", ids(since))
	}
}

func TestFakeRejectCredentials(t *testing.T) {
	p := New()
	create(t, p, "a")
	p.AddFeedback(1, hosting.Feedback{ID: "x"})
	p.RejectCredentials()
	checkAll(t, p, hosting.ErrUnauthorized)
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.prs) != 1 || len(p.feedback[1]) != 1 {
		t.Fatalf("state changed: %+v %+v", p.prs, p.feedback)
	}
}

func TestFakeRateLimit(t *testing.T) {
	p := New()
	create(t, p, "a")
	reset := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	p.RateLimit(reset)
	checkAll(t, p, hosting.ErrRateLimited)
	_, err := p.GetPullRequest(ctx, 1)
	var rl *hosting.RateLimitError
	if !errors.As(err, &rl) || !rl.ResetAt.Equal(reset) {
		t.Fatalf("err = %v", err)
	}
	p.Merge(1)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.prs[0].State != hosting.StateMerged {
		t.Fatal("controls must still change state")
	}
}

func TestFakeBringDown(t *testing.T) {
	p := New()
	create(t, p, "a")
	p.BringDown()
	checkAll(t, p, hosting.ErrUnavailable)
	if got := len(p.Calls()); got != 5 {
		t.Fatalf("calls = %d, want failed calls recorded", got)
	}
}

func checkAll(t *testing.T, p *Provider, want error) {
	t.Helper()
	_, err := p.CreatePullRequest(ctx, hosting.NewPullRequest{Head: "b"})
	checks := map[string]error{"create": err}
	_, _, checks["find"] = p.FindPullRequest(ctx, "a")
	_, checks["get"] = p.GetPullRequest(ctx, 1)
	_, checks["feedback"] = p.ReviewFeedback(ctx, 1, hosting.FeedbackOptions{})
	for op, err := range checks {
		if !errors.Is(err, want) {
			t.Errorf("%s err = %v, want %v", op, err, want)
		}
	}
}
