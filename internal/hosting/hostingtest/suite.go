package hostingtest

import (
	"context"
	"errors"
	"net/url"
	"testing"
	"time"

	"github.com/bengearig/ralph/internal/hosting"
)

var ctx = context.Background()

// Run runs every conformance check as a subtest, each with a fresh Subject.
func Run(t *testing.T, newSubject func(t *testing.T) Subject) {
	t.Helper()
	run(stdT{t}, func(t runT) Subject { return newSubject(t.(stdT).T) })
}

type check struct {
	name string
	// needs names the Subject controls the check uses.
	needs []string
	run   func(t runT, s Subject)
}

const (
	ctlMerge      = "Merge"
	ctlDecline    = "Decline"
	ctlReview     = "AddReview"
	ctlInline     = "AddInline"
	ctlComment    = "AddComment"
	ctlReject     = "RejectCredentials"
	ctlRateLimit  = "RateLimit"
	ctlBringDown  = "BringDown"
	ctlOrderPause = "Pause"
)

var checks = []check{
	{"CreateThenGet", nil, createThenGet},
	{"DraftRoundTrip", nil, draftRoundTrip},
	{"DuplicateCreateConflicts", nil, duplicateCreateConflicts},
	{"FindNone", nil, findNone},
	{"FindPrefersOpen", []string{ctlMerge, ctlDecline}, findPrefersOpen},
	{"FindNewestWhenNoneOpen", []string{ctlMerge, ctlDecline}, findNewestWhenNoneOpen},
	{"FindIgnoresOtherBranches", nil, findIgnoresOtherBranches},
	{"GetUnknownNumber", nil, getUnknownNumber},
	{"MergedState", []string{ctlMerge}, mergedState},
	{"DeclinedState", []string{ctlDecline}, declinedState},
	{"FeedbackInline", []string{ctlInline}, feedbackInline},
	{"FeedbackComment", []string{ctlComment}, feedbackComment},
	{"FeedbackReviewChangesRequested", []string{ctlReview}, feedbackReviewChangesRequested},
	{"FeedbackReviewApproved", []string{ctlReview}, feedbackReviewApproved},
	{"FeedbackIdentifiersUnique", []string{ctlReview, ctlInline, ctlComment}, feedbackIdentifiersUnique},
	{"FeedbackOrdered", []string{ctlReview, ctlInline, ctlComment, ctlOrderPause}, feedbackOrdered},
	{"FeedbackSince", []string{ctlReview, ctlInline, ctlComment, ctlOrderPause}, feedbackSince},
	{"FeedbackEmpty", nil, feedbackEmpty},
	{"RejectedCredentials", []string{ctlReject}, rejectedCredentials},
	{"RateLimited", []string{ctlRateLimit}, rateLimited},
	{"Unavailable", []string{ctlBringDown}, unavailable},
}

func run(t runT, newSubject func(t runT) Subject) {
	for _, c := range checks {
		t.Run(c.name, func(t runT) {
			s := newSubject(t)
			if s.Provider == nil {
				t.Fatal("Subject.Provider is nil")
			}
			for _, need := range c.needs {
				if missing(s, need) {
					t.Skipf("subject control %s is not set", need)
				}
			}
			c.run(t, s)
		})
	}
}

// missing reports whether the named control is unavailable. The pause is
// unavailable when the subtest would have to wait but -short is set.
func missing(s Subject, control string) bool {
	switch control {
	case ctlMerge:
		return s.Merge == nil
	case ctlDecline:
		return s.Decline == nil
	case ctlReview:
		return s.AddReview == nil
	case ctlInline:
		return s.AddInline == nil
	case ctlComment:
		return s.AddComment == nil
	case ctlReject:
		return s.RejectCredentials == nil
	case ctlRateLimit:
		return s.RateLimit == nil
	case ctlBringDown:
		return s.BringDown == nil
	case ctlOrderPause:
		return s.Pause > 0 && testing.Short()
	}
	return false
}

func create(t testing.TB, s Subject, head string, draft bool) hosting.PullRequest {
	t.Helper()
	pr, err := s.Provider.CreatePullRequest(ctx, hosting.NewPullRequest{
		Head: head, Base: "main", Title: "Conformance " + head, Body: "body", Draft: draft,
	})
	if err != nil {
		t.Fatalf("create %q: %v", head, err)
	}
	return pr
}

func get(t testing.TB, s Subject, number int) hosting.PullRequest {
	t.Helper()
	pr, err := s.Provider.GetPullRequest(ctx, number)
	if err != nil {
		t.Fatalf("get #%d: %v", number, err)
	}
	return pr
}

func find(t testing.TB, s Subject, head string) (hosting.PullRequest, bool) {
	t.Helper()
	pr, ok, err := s.Provider.FindPullRequest(ctx, head)
	if err != nil {
		t.Fatalf("find %q: %v", head, err)
	}
	return pr, ok
}

func feedback(t testing.TB, s Subject, number int, opts hosting.FeedbackOptions) []hosting.Feedback {
	t.Helper()
	got, err := s.Provider.ReviewFeedback(ctx, number, opts)
	if err != nil {
		t.Fatalf("feedback #%d: %v", number, err)
	}
	return got
}

func pause(s Subject) {
	if s.Pause > 0 {
		time.Sleep(s.Pause)
	}
}

func createThenGet(t runT, s Subject) {
	created := create(t, s, "conformance/create", false)
	if created.Number < 1 {
		t.Fatalf("created number = %d", created.Number)
	}
	got := get(t, s, created.Number)
	if got.Number != created.Number {
		t.Errorf("number = %d, want %d", got.Number, created.Number)
	}
	if got.Title != "Conformance conformance/create" {
		t.Errorf("title = %q", got.Title)
	}
	if got.Head != "conformance/create" || got.Base != "main" {
		t.Errorf("head, base = %q, %q", got.Head, got.Base)
	}
	if got.Draft {
		t.Error("draft = true, want false")
	}
	if got.URL == "" {
		t.Error("URL is empty")
	}
	if got.State != hosting.StateOpen {
		t.Errorf("state = %q, want open", got.State)
	}
}

func draftRoundTrip(t runT, s Subject) {
	for _, draft := range []bool{true, false} {
		head := "conformance/draft-off"
		if draft {
			head = "conformance/draft-on"
		}
		created := create(t, s, head, draft)
		if created.Draft != draft {
			t.Errorf("created draft = %v, want %v", created.Draft, draft)
		}
		if got := get(t, s, created.Number); got.Draft != draft {
			t.Errorf("got draft = %v, want %v", got.Draft, draft)
		}
	}
}

func duplicateCreateConflicts(t runT, s Subject) {
	create(t, s, "conformance/dup", false)
	_, err := s.Provider.CreatePullRequest(ctx, hosting.NewPullRequest{Head: "conformance/dup", Base: "main", Title: "again"})
	if !errors.Is(err, hosting.ErrConflict) {
		t.Fatalf("second create err = %v, want ErrConflict", err)
	}
}

func findNone(t runT, s Subject) {
	pr, ok, err := s.Provider.FindPullRequest(ctx, "conformance/none")
	if err != nil || ok {
		t.Fatalf("find = %+v, %v, %v; want not found and no error", pr, ok, err)
	}
}

// An open pull request is always the newest for its head, because a second one
// cannot be opened while one is open; the closed ones it must be preferred
// over are older.
func findPrefersOpen(t runT, s Subject) {
	const head = "conformance/prefer-open"
	first := create(t, s, head, false)
	s.Merge(t, first.Number)
	second := create(t, s, head, false)
	s.Decline(t, second.Number)
	open := create(t, s, head, false)
	got, ok := find(t, s, head)
	if !ok || got.Number != open.Number {
		t.Fatalf("find = #%d, found %v; want the open #%d", got.Number, ok, open.Number)
	}
	if got.State != hosting.StateOpen {
		t.Errorf("state = %q, want open", got.State)
	}
}

func findNewestWhenNoneOpen(t runT, s Subject) {
	const head = "conformance/newest"
	first := create(t, s, head, false)
	s.Decline(t, first.Number)
	second := create(t, s, head, false)
	s.Merge(t, second.Number)
	third := create(t, s, head, false)
	s.Decline(t, third.Number)
	got, ok := find(t, s, head)
	if !ok || got.Number != third.Number {
		t.Fatalf("find = #%d, found %v; want the newest #%d", got.Number, ok, third.Number)
	}
}

func findIgnoresOtherBranches(t runT, s Subject) {
	a := create(t, s, "conformance/branch-a", false)
	b := create(t, s, "conformance/branch-b", false)
	for head, want := range map[string]int{"conformance/branch-a": a.Number, "conformance/branch-b": b.Number} {
		got, ok := find(t, s, head)
		if !ok || got.Number != want {
			t.Errorf("find %q = #%d, found %v; want #%d", head, got.Number, ok, want)
		}
	}
	if pr, ok := find(t, s, "conformance/branch-c"); ok {
		t.Errorf("find of a branch without a pull request = #%d", pr.Number)
	}
}

func getUnknownNumber(t runT, s Subject) {
	create(t, s, "conformance/known", false)
	_, err := s.Provider.GetPullRequest(ctx, 987654)
	if !errors.Is(err, hosting.ErrNotFound) {
		t.Fatalf("get err = %v, want ErrNotFound", err)
	}
}

func mergedState(t runT, s Subject) {
	pr := create(t, s, "conformance/merged", false)
	s.Merge(t, pr.Number)
	got := get(t, s, pr.Number)
	if got.State != hosting.StateMerged {
		t.Errorf("state = %q, want merged", got.State)
	}
	if got.MergeCommit == "" {
		t.Error("merge commit is empty")
	}
}

func declinedState(t runT, s Subject) {
	pr := create(t, s, "conformance/declined", false)
	s.Decline(t, pr.Number)
	got := get(t, s, pr.Number)
	if got.State != hosting.StateClosed {
		t.Errorf("state = %q, want closed", got.State)
	}
	if got.MergeCommit != "" {
		t.Errorf("merge commit = %q, want empty", got.MergeCommit)
	}
}

// onlyFeedback returns the single item feedback holds and checks the fields
// every item must have.
func onlyFeedback(t testing.TB, s Subject, number int) hosting.Feedback {
	t.Helper()
	got := feedback(t, s, number, hosting.FeedbackOptions{})
	if len(got) != 1 {
		t.Fatalf("feedback = %+v, want one item", got)
	}
	checkCommon(t, got[0])
	return got[0]
}

func checkCommon(t testing.TB, f hosting.Feedback) {
	t.Helper()
	if f.ID == "" {
		t.Errorf("feedback %+v has no ID", f)
	}
	if f.Author == "" {
		t.Errorf("feedback %q has no author", f.ID)
	}
	if f.CreatedAt.IsZero() {
		t.Errorf("feedback %q has no time", f.ID)
	}
	if f.URL != "" {
		if u, err := url.Parse(f.URL); err != nil || u.Scheme == "" || u.Host == "" {
			t.Errorf("feedback %q has URL %q, want empty or absolute", f.ID, f.URL)
		}
	}
}

func feedbackInline(t runT, s Subject) {
	pr := create(t, s, "conformance/inline", false)
	s.AddInline(t, pr.Number, hosting.Feedback{Path: "pkg/file.go", Line: 12, Body: "inline body", Author: "alice"})
	f := onlyFeedback(t, s, pr.Number)
	if f.Kind != hosting.KindInline || f.Path != "pkg/file.go" || f.Line != 12 {
		t.Errorf("feedback = %+v, want inline at pkg/file.go:12", f)
	}
	if f.Author != "alice" || f.Body != "inline body" {
		t.Errorf("author, body = %q, %q", f.Author, f.Body)
	}
}

func feedbackComment(t runT, s Subject) {
	pr := create(t, s, "conformance/comment", false)
	s.AddComment(t, pr.Number, hosting.Feedback{Body: "general body", Author: "bob"})
	f := onlyFeedback(t, s, pr.Number)
	if f.Kind != hosting.KindComment {
		t.Errorf("kind = %q, want comment", f.Kind)
	}
	if f.Author != "bob" || f.Body != "general body" {
		t.Errorf("author, body = %q, %q", f.Author, f.Body)
	}
}

func feedbackReview(t runT, s Subject, head string, state hosting.ReviewState) {
	pr := create(t, s, head, false)
	s.AddReview(t, pr.Number, hosting.Feedback{Kind: hosting.KindReview, State: state, Body: "review body", Author: "carol"})
	f := onlyFeedback(t, s, pr.Number)
	if f.Kind != hosting.KindReview || f.State != state {
		t.Errorf("feedback = %+v, want a %s review", f, state)
	}
}

func feedbackReviewChangesRequested(t runT, s Subject) {
	feedbackReview(t, s, "conformance/review-changes", hosting.ReviewChangesRequested)
}

func feedbackReviewApproved(t runT, s Subject) {
	feedbackReview(t, s, "conformance/review-approved", hosting.ReviewApproved)
}

// addMixed adds feedback of every kind, pausing between additions, and
// returns the pull request number.
func addMixed(t testing.TB, s Subject, head string) int {
	t.Helper()
	pr := create(t, s, head, false)
	s.AddComment(t, pr.Number, hosting.Feedback{Body: "first", Author: "alice"})
	pause(s)
	s.AddInline(t, pr.Number, hosting.Feedback{Path: "a.go", Line: 3, Body: "second", Author: "bob"})
	pause(s)
	s.AddReview(t, pr.Number, hosting.Feedback{Kind: hosting.KindReview, State: hosting.ReviewChangesRequested, Author: "carol"})
	pause(s)
	s.AddComment(t, pr.Number, hosting.Feedback{Body: "fourth", Author: "alice"})
	return pr.Number
}

func feedbackIdentifiersUnique(t runT, s Subject) {
	number := addMixed(t, s, "conformance/unique")
	got := feedback(t, s, number, hosting.FeedbackOptions{})
	if len(got) < 4 {
		t.Fatalf("feedback = %+v, want at least four items", got)
	}
	seen := map[string]bool{}
	for _, f := range got {
		checkCommon(t, f)
		if seen[f.ID] {
			t.Errorf("identifier %q appears more than once", f.ID)
		}
		seen[f.ID] = true
	}
}

func feedbackOrdered(t runT, s Subject) {
	number := addMixed(t, s, "conformance/ordered")
	got := feedback(t, s, number, hosting.FeedbackOptions{})
	if len(got) < 4 {
		t.Fatalf("feedback = %+v, want at least four items", got)
	}
	for i := 1; i < len(got); i++ {
		prev, cur := got[i-1], got[i]
		switch {
		case cur.CreatedAt.Before(prev.CreatedAt):
			t.Errorf("%q (%v) comes after %q (%v)", cur.ID, cur.CreatedAt, prev.ID, prev.CreatedAt)
		case cur.CreatedAt.Equal(prev.CreatedAt) && cur.ID <= prev.ID:
			t.Errorf("%q comes after %q at the same time", cur.ID, prev.ID)
		}
	}
}

func feedbackSince(t runT, s Subject) {
	number := addMixed(t, s, "conformance/since")
	all := feedback(t, s, number, hosting.FeedbackOptions{})
	if len(all) < 4 {
		t.Fatalf("feedback = %+v, want at least four items", all)
	}
	for i := 1; i < len(all); i++ {
		if !all[i].CreatedAt.After(all[i-1].CreatedAt) {
			t.Fatalf("items %q and %q have the same time; set Subject.Pause so the service stamps them apart", all[i-1].ID, all[i].ID)
		}
	}
	for cut := 0; cut < len(all); cut++ {
		got := feedback(t, s, number, hosting.FeedbackOptions{Since: all[cut].CreatedAt})
		want := all[cut+1:]
		if len(got) != len(want) {
			t.Errorf("Since item %d: got %d items, want %d", cut, len(got), len(want))
			continue
		}
		for i := range want {
			if got[i].ID != want[i].ID {
				t.Errorf("Since item %d: item %d is %q, want %q", cut, i, got[i].ID, want[i].ID)
			}
		}
	}
}

func feedbackEmpty(t runT, s Subject) {
	pr := create(t, s, "conformance/empty", false)
	got := feedback(t, s, pr.Number, hosting.FeedbackOptions{})
	if got == nil || len(got) != 0 {
		t.Fatalf("feedback = %#v, want an empty non-nil slice", got)
	}
	got = feedback(t, s, pr.Number, hosting.FeedbackOptions{Since: time.Now().Add(time.Hour)})
	if got == nil || len(got) != 0 {
		t.Fatalf("feedback since the future = %#v, want an empty non-nil slice", got)
	}
}

// everyOperation calls each provider operation on a pull request created
// before the service was changed, and passes each error to check.
func everyOperation(t testing.TB, s Subject, number int, head string, check func(op string, err error)) {
	t.Helper()
	_, err := s.Provider.CreatePullRequest(ctx, hosting.NewPullRequest{Head: head + "-new", Base: "main", Title: "t"})
	check("create", err)
	_, _, err = s.Provider.FindPullRequest(ctx, head)
	check("find", err)
	_, err = s.Provider.GetPullRequest(ctx, number)
	check("get", err)
	_, err = s.Provider.ReviewFeedback(ctx, number, hosting.FeedbackOptions{})
	check("feedback", err)
}

func rejectedCredentials(t runT, s Subject) {
	pr := create(t, s, "conformance/unauthorized", false)
	s.RejectCredentials(t)
	everyOperation(t, s, pr.Number, pr.Head, func(op string, err error) {
		if !errors.Is(err, hosting.ErrUnauthorized) {
			t.Errorf("%s err = %v, want ErrUnauthorized", op, err)
		}
	})
}

func rateLimited(t runT, s Subject) {
	pr := create(t, s, "conformance/rate-limited", false)
	s.RateLimit(t, time.Now().Add(time.Minute))
	everyOperation(t, s, pr.Number, pr.Head, func(op string, err error) {
		var rl *hosting.RateLimitError
		if !errors.Is(err, hosting.ErrRateLimited) || !errors.As(err, &rl) {
			t.Errorf("%s err = %v, want ErrRateLimited as a *RateLimitError", op, err)
		}
	})
}

func unavailable(t runT, s Subject) {
	pr := create(t, s, "conformance/unavailable", false)
	s.BringDown(t)
	everyOperation(t, s, pr.Number, pr.Head, func(op string, err error) {
		if !errors.Is(err, hosting.ErrUnavailable) {
			t.Errorf("%s err = %v, want ErrUnavailable", op, err)
		}
	})
}
