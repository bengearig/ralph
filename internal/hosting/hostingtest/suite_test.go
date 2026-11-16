package hostingtest

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"testing"
	"time"

	"github.com/bengearig/ralph/internal/hosting"
	"github.com/bengearig/ralph/internal/hosting/fake"
)

// recorder is a runT that records what the suite does instead of failing the
// real test.
type recorder struct {
	testing.TB
	results map[string]string
	name    string
	status  string
}

func newRecorder(t testing.TB) *recorder {
	return &recorder{TB: t, results: map[string]string{}, status: "pass"}
}

func (r *recorder) Run(name string, f func(runT)) bool {
	child := &recorder{TB: r.TB, results: r.results, name: name, status: "pass"}
	done := make(chan struct{})
	go func() {
		defer close(done)
		f(child)
	}()
	<-done
	r.results[name] = child.status
	return child.status != "fail"
}

func (r *recorder) Name() string          { return r.name }
func (r *recorder) Fail()                 { r.status = "fail" }
func (r *recorder) FailNow()              { r.status = "fail"; runtime.Goexit() }
func (r *recorder) Failed() bool          { return r.status == "fail" }
func (r *recorder) Error(...any)          { r.status = "fail" }
func (r *recorder) Errorf(string, ...any) { r.status = "fail" }
func (r *recorder) Fatal(...any)          { r.FailNow() }
func (r *recorder) Fatalf(string, ...any) { r.FailNow() }
func (r *recorder) Skip(...any)           { r.SkipNow() }
func (r *recorder) Skipf(string, ...any)  { r.SkipNow() }
func (r *recorder) Skipped() bool         { return r.status == "skip" }
func (r *recorder) Log(...any)            {}
func (r *recorder) Logf(string, ...any)   {}
func (r *recorder) Helper()               {}
func (r *recorder) SkipNow() {
	if r.status != "fail" {
		r.status = "skip"
	}
	runtime.Goexit()
}

func fakeSubject(p hosting.Provider, f *fake.Provider) Subject {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	n := 0
	add := func(prefix string, kind hosting.FeedbackKind) func(testing.TB, int, hosting.Feedback) {
		return func(_ testing.TB, number int, fb hosting.Feedback) {
			n++
			fb.ID = fmt.Sprintf("%s-%d", prefix, n)
			fb.Kind = kind
			fb.CreatedAt = base.Add(time.Duration(n) * time.Minute)
			f.AddFeedback(number, fb)
		}
	}
	return Subject{
		Provider:          p,
		Merge:             func(_ testing.TB, number int) { f.Merge(number) },
		Decline:           func(_ testing.TB, number int) { f.Close(number) },
		AddReview:         add("review", hosting.KindReview),
		AddInline:         add("inline", hosting.KindInline),
		AddComment:        add("comment", hosting.KindComment),
		RejectCredentials: func(testing.TB) { f.RejectCredentials() },
		RateLimit:         func(_ testing.TB, at time.Time) { f.RateLimit(at) },
		BringDown:         func(testing.TB) { f.BringDown() },
	}
}

func names() []string {
	var out []string
	for _, c := range checks {
		out = append(out, c.name)
	}
	return out
}

func runOn(t *testing.T, build func() Subject) map[string]string {
	t.Helper()
	r := newRecorder(t)
	run(r, func(runT) Subject { return build() })
	return r.results
}

func TestSuiteSubtestNamesStable(t *testing.T) {
	want := []string{
		"CreateThenGet",
		"DraftRoundTrip",
		"DuplicateCreateConflicts",
		"FindNone",
		"FindPrefersOpen",
		"FindNewestWhenNoneOpen",
		"FindIgnoresOtherBranches",
		"GetUnknownNumber",
		"MergedState",
		"DeclinedState",
		"FeedbackInline",
		"FeedbackComment",
		"FeedbackReviewChangesRequested",
		"FeedbackReviewApproved",
		"FeedbackIdentifiersUnique",
		"FeedbackOrdered",
		"FeedbackSince",
		"FeedbackEmpty",
		"RejectedCredentials",
		"RateLimited",
		"Unavailable",
	}
	if got := names(); !reflect.DeepEqual(got, want) {
		t.Fatalf("subtest names = %v", got)
	}
	got := runOn(t, func() Subject { f := fake.New(); return fakeSubject(f, f) })
	if len(got) != len(want) {
		t.Fatalf("ran %d subtests, want %d", len(got), len(want))
	}
	for _, n := range want {
		if got[n] != "pass" {
			t.Errorf("%s = %s", n, got[n])
		}
	}
}

func TestSuiteSkipsWhenControlsMissing(t *testing.T) {
	skipped := func(res map[string]string) map[string]bool {
		out := map[string]bool{}
		for n, s := range res {
			if s == "skip" {
				out[n] = true
			} else if s != "pass" {
				t.Errorf("%s = %s", n, s)
			}
		}
		return out
	}

	got := skipped(runOn(t, func() Subject {
		f := fake.New()
		s := fakeSubject(f, f)
		s.RejectCredentials, s.RateLimit, s.BringDown = nil, nil, nil
		return s
	}))
	want := map[string]bool{"RejectedCredentials": true, "RateLimited": true, "Unavailable": true}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("optional controls nil: skipped %v", got)
	}

	got = skipped(runOn(t, func() Subject { return Subject{Provider: fake.New()} }))
	for _, c := range checks {
		if got[c.name] != (len(c.needs) > 0) {
			t.Errorf("no controls: %s skipped = %v", c.name, got[c.name])
		}
	}
	for _, n := range []string{"CreateThenGet", "FindNone", "FeedbackEmpty"} {
		if got[n] {
			t.Errorf("%s should run without controls", n)
		}
	}
}

type nilForEmpty struct{ hosting.Provider }

func (p nilForEmpty) ReviewFeedback(c context.Context, n int, o hosting.FeedbackOptions) ([]hosting.Feedback, error) {
	got, err := p.Provider.ReviewFeedback(c, n, o)
	if len(got) == 0 {
		return nil, err
	}
	return got, err
}

type duplicateIDs struct{ hosting.Provider }

func (p duplicateIDs) ReviewFeedback(c context.Context, n int, o hosting.FeedbackOptions) ([]hosting.Feedback, error) {
	got, err := p.Provider.ReviewFeedback(c, n, o)
	for i := range got {
		got[i].ID = "same"
	}
	return got, err
}

type closedOverOpen struct{ hosting.Provider }

func (p closedOverOpen) FindPullRequest(c context.Context, head string) (hosting.PullRequest, bool, error) {
	var found hosting.PullRequest
	ok := false
	for n := 1; ; n++ {
		pr, err := p.GetPullRequest(c, n)
		if errors.Is(err, hosting.ErrNotFound) {
			break
		}
		if err != nil {
			return pr, false, err
		}
		if pr.Head != head || (ok && found.State != hosting.StateOpen && pr.State == hosting.StateOpen) {
			continue
		}
		found, ok = pr, true
	}
	return found, ok, nil
}

type wrongUnknownError struct{ hosting.Provider }

func (p wrongUnknownError) GetPullRequest(c context.Context, n int) (hosting.PullRequest, error) {
	pr, err := p.Provider.GetPullRequest(c, n)
	if errors.Is(err, hosting.ErrNotFound) {
		return pr, hosting.ErrInvalid
	}
	return pr, err
}

type unordered struct{ hosting.Provider }

func (p unordered) ReviewFeedback(c context.Context, n int, o hosting.FeedbackOptions) ([]hosting.Feedback, error) {
	got, err := p.Provider.ReviewFeedback(c, n, o)
	for i, j := 0, len(got)-1; i < j; i, j = i+1, j-1 {
		got[i], got[j] = got[j], got[i]
	}
	return got, err
}

type ignoresSince struct{ hosting.Provider }

func (p ignoresSince) ReviewFeedback(c context.Context, n int, _ hosting.FeedbackOptions) ([]hosting.Feedback, error) {
	return p.Provider.ReviewFeedback(c, n, hosting.FeedbackOptions{})
}

func TestSuiteFailsBrokenProvider(t *testing.T) {
	tests := []struct {
		name   string
		wrap   func(hosting.Provider) hosting.Provider
		failed []string
	}{
		{"nil slice", func(p hosting.Provider) hosting.Provider { return nilForEmpty{p} }, []string{"FeedbackEmpty"}},
		{"duplicate identifiers", func(p hosting.Provider) hosting.Provider { return duplicateIDs{p} }, []string{"FeedbackIdentifiersUnique"}},
		{"closed ahead of open", func(p hosting.Provider) hosting.Provider { return closedOverOpen{p} }, []string{"FindPrefersOpen"}},
		{"wrong unknown error", func(p hosting.Provider) hosting.Provider { return wrongUnknownError{p} }, []string{"GetUnknownNumber"}},
		{"unordered", func(p hosting.Provider) hosting.Provider { return unordered{p} }, []string{"FeedbackOrdered", "FeedbackSince"}},
		{"ignores since", func(p hosting.Provider) hosting.Provider { return ignoresSince{p} }, []string{"FeedbackSince"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := runOn(t, func() Subject { f := fake.New(); return fakeSubject(tt.wrap(f), f) })
			want := map[string]bool{}
			for _, n := range tt.failed {
				want[n] = true
			}
			for n, s := range res {
				if want[n] && s != "fail" {
					t.Errorf("%s = %s, want fail", n, s)
				}
				if !want[n] && s == "fail" {
					t.Errorf("%s failed unexpectedly", n)
				}
			}
		})
	}
}
