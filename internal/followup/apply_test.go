package followup_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bengearig/ralph/internal/followup"
	"github.com/bengearig/ralph/internal/hosting"
	"github.com/bengearig/ralph/internal/prstep"
	"github.com/bengearig/ralph/internal/units"
)

var allKinds = []followup.Kind{followup.KindClose, followup.KindFeedback}

func (e *env) origin(t *testing.T) {
	t.Helper()
	bare := t.TempDir()
	e.repo.Git("init", "--bare", "-b", "main", bare)
	e.repo.Git("remote", "add", "origin", bare)
	e.repo.Git("push", "origin", "main")
}

// mergedOnHost is a slice recorded open whose pull request the host merged,
// with a pushed branch whose tip is the head commit the host reports.
func (e *env) mergedOnHost(t *testing.T, title string) (units.Unit, string) {
	t.Helper()
	s, pr := e.slice(t, title, units.PullRequestOpen)
	name, err := prstep.BranchName(branchTemplate, s)
	if err != nil {
		t.Fatal(err)
	}
	e.repo.Git("switch", "-c", name)
	e.repo.WriteFile(s.ID+".txt", "work")
	head := e.repo.Commit("work " + s.ID)
	e.repo.Git("push", "origin", name)
	e.repo.Git("switch", "main")
	e.provider.SetHeadCommit(pr.Number, head)
	e.provider.Merge(pr.Number)
	return s, name
}

func (e *env) apply(t *testing.T, kinds ...followup.Kind) followup.ApplyResult {
	t.Helper()
	res, err := followup.Apply(ctx, e.deps, followup.ApplyOptions{Kinds: kinds})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func (e *env) state(t *testing.T, s units.Unit) units.PullRequestRecord {
	t.Helper()
	return e.record(t, s)
}

func (e *env) branchExists(t *testing.T, name string) bool {
	t.Helper()
	ok, err := e.deps.Workspace.BranchExists(ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	return ok
}

func hasPrefixLine(lines []string, prefix string) bool {
	return slices.ContainsFunc(lines, func(l string) bool { return strings.HasPrefix(l, prefix) })
}

func (e *env) feedbackOn(p hosting.PullRequest, ids ...string) {
	for _, id := range ids {
		e.provider.AddFeedback(p.Number, hosting.Feedback{ID: id, Kind: hosting.KindInline, Author: "rev", Body: "fix " + id, Path: "a.go", Line: 1, CreatedAt: time.Now()})
	}
}

func TestApplyAlwaysRecordsState(t *testing.T) {
	e := newEnv(t)
	merged, mpr := e.slice(t, "Merged", units.PullRequestOpen)
	closed, cpr := e.slice(t, "Closed", units.PullRequestOpen)
	e.provider.Merge(mpr.Number)
	e.provider.Close(cpr.Number)
	res := e.apply(t)
	if res.Recorded != 2 || !hasPrefixLine(res.Lines, "recorded "+merged.ID+": ") || !hasPrefixLine(res.Lines, "recorded "+closed.ID+": ") {
		t.Fatalf("result = %+v", res)
	}
	if got := e.state(t, merged).State; got != units.PullRequestMerged {
		t.Errorf("merged recorded as %s", got)
	}
	if got := e.state(t, closed).State; got != units.PullRequestClosed {
		t.Errorf("closed recorded as %s", got)
	}
}

func TestApplyNoKindsAppliesNothingElse(t *testing.T) {
	e := newEnv(t)
	e.origin(t)
	s, name := e.mergedOnHost(t, "Merged")
	open, pr := e.slice(t, "Open", units.PullRequestOpen)
	e.feedbackOn(pr, "c1")
	before := len(e.allTasks(t))
	res := e.apply(t)
	if !e.branchExists(t, name) || len(e.allTasks(t)) != before {
		t.Error("something was applied")
	}
	if res.Created != 0 || res.Closed != 0 || res.NotApplied != 2 {
		t.Errorf("result = %+v", res)
	}
	if !hasPrefixLine(res.Lines, "not applied: close: "+s.ID+": delete branch "+name) || !strings.Contains(strings.Join(res.Lines, "\n"), "--close") {
		t.Errorf("lines = %q", res.Lines)
	}
	if !hasPrefixLine(res.Lines, "not applied: feedback: "+open.ID+": 1 new review feedback item; apply it with --feedback") {
		t.Errorf("lines = %q", res.Lines)
	}
}

func (e *env) allTasks(t *testing.T) []units.Unit {
	t.Helper()
	all, err := e.units.List(ctx, units.Filter{Level: units.LevelTask})
	if err != nil {
		t.Fatal(err)
	}
	return all
}

func TestApplyFeedbackCreatesTasks(t *testing.T) {
	e := newEnv(t)
	s, pr := e.slice(t, "Open", units.PullRequestOpen)
	e.feedbackOn(pr, "c1", "c2")
	res := e.apply(t, followup.KindFeedback)
	tasks := e.allTasks(t)
	if res.Created != 2 || len(tasks) != 2 {
		t.Fatalf("result = %+v, tasks = %d", res, len(tasks))
	}
	want := "created " + tasks[0].ID + ", " + tasks[1].ID + " from review feedback on " + s.ID
	if !slices.Contains(res.Lines, want) {
		t.Errorf("lines = %q, want %q", res.Lines, want)
	}
}

func TestApplyCloseDeletesBranchAfterRecordingMerge(t *testing.T) {
	e := newEnv(t)
	e.origin(t)
	s, name := e.mergedOnHost(t, "Merged")
	res := e.apply(t, followup.KindClose)
	if res.Recorded != 1 || res.Closed != 1 || e.branchExists(t, name) {
		t.Fatalf("result = %+v", res)
	}
	if got := e.state(t, s).State; got != units.PullRequestMerged {
		t.Errorf("state = %s", got)
	}
	rec := slices.IndexFunc(res.Lines, func(l string) bool { return strings.HasPrefix(l, "recorded "+s.ID) })
	del := slices.Index(res.Lines, s.ID+": deleted local branch "+name)
	if rec != 0 || del < rec {
		t.Errorf("lines = %q", res.Lines)
	}
}

func TestApplyCloseOfAlreadyRecordedMerge(t *testing.T) {
	e := newEnv(t)
	e.origin(t)
	_, name := e.mergedOnHost(t, "Merged")
	e.apply(t)
	res := e.apply(t, followup.KindClose)
	if res.Recorded != 0 || res.Closed != 1 || e.branchExists(t, name) {
		t.Fatalf("result = %+v", res)
	}
}

func TestApplyDecisionNeverApplied(t *testing.T) {
	e := newEnv(t)
	s, pr := e.slice(t, "Closed", units.PullRequestOpen)
	e.provider.Close(pr.Number)
	res := e.apply(t, followup.KindClose, followup.KindFeedback)
	if !hasPrefixLine(res.Lines, "needs you: ") || !strings.Contains(strings.Join(res.Lines, "\n"), "ralph pr reopen") {
		t.Errorf("lines = %q", res.Lines)
	}
	if got := e.record(t, s); got.State != units.PullRequestClosed || got.Resolution != "" {
		t.Errorf("record = %+v", got)
	}
}

func TestApplyKindsOrderRecordFeedbackClose(t *testing.T) {
	e := newEnv(t)
	e.origin(t)
	m, name := e.mergedOnHost(t, "Merged")
	o, pr := e.slice(t, "Open", units.PullRequestOpen)
	e.feedbackOn(pr, "c1")
	res := e.apply(t, followup.KindClose, followup.KindFeedback)
	want := []string{"recorded " + m.ID + ": ", m.ID + ": deleted local branch " + name, "created "}
	at := -1
	for _, w := range want {
		i := slices.IndexFunc(res.Lines, func(l string) bool { return strings.HasPrefix(l, w) })
		if i <= at {
			t.Fatalf("%q out of order in %q", w, res.Lines)
		}
		at = i
	}
	if !strings.HasSuffix(res.Lines[at], "from review feedback on "+o.ID) {
		t.Errorf("lines = %q", res.Lines)
	}
}

func TestApplyOneSliceFailureDoesNotStopOthers(t *testing.T) {
	e := newEnv(t)
	gone, _ := e.slice(t, "Gone", units.PullRequestOpen)
	if err := e.units.RecordPullRequest(ctx, units.PullRequestRecord{SliceID: gone.ID, Number: 99, URL: "https://x.test/99", Head: "h", Base: "main", State: units.PullRequestClosed}); err != nil {
		t.Fatal(err)
	}
	_, pr := e.slice(t, "Other", units.PullRequestOpen)
	e.provider.Merge(pr.Number)
	res := e.apply(t, allKinds...)
	if res.Recorded != 1 || len(res.Failures) != 1 || !strings.HasPrefix(res.Failures[0], gone.ID+": ") || res.NothingObserved {
		t.Errorf("result = %+v", res)
	}
}

func TestApplySliceFilter(t *testing.T) {
	e := newEnv(t)
	a, apr := e.slice(t, "A", units.PullRequestOpen)
	b, bpr := e.slice(t, "B", units.PullRequestOpen)
	e.provider.Merge(apr.Number)
	e.provider.Merge(bpr.Number)
	res, err := followup.Apply(ctx, e.deps, followup.ApplyOptions{Slices: []string{b.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Recorded != 1 || e.record(t, a).State != units.PullRequestOpen || e.record(t, b).State != units.PullRequestMerged {
		t.Errorf("result = %+v", res)
	}
	if _, err := followup.Apply(ctx, e.deps, followup.ApplyOptions{Slices: []string{"S-99"}}); err == nil {
		t.Error("unknown slice accepted")
	}
}

func TestApplyProviderUnavailable(t *testing.T) {
	e := newEnv(t)
	s, _ := e.slice(t, "Open", units.PullRequestOpen)
	e.deps.Provider = func(context.Context) (hosting.Provider, error) { return nil, errors.New("no token") }
	res := e.apply(t, allKinds...)
	if !res.NothingObserved || res.Recorded != 0 || !hasPrefixLine(res.Lines, s.ID+": not observed: provider unavailable (no token)") {
		t.Errorf("result = %+v", res)
	}
}

func TestApplyRateLimitedSlicesReported(t *testing.T) {
	e := newEnv(t)
	a, _ := e.slice(t, "One", units.PullRequestOpen)
	b, _ := e.slice(t, "Two", units.PullRequestOpen)
	e.provider.RateLimit(time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC))
	res := e.apply(t, allKinds...)
	if !res.NothingObserved || !hasPrefixLine(res.Lines, a.ID+": not observed: rate limited") || !hasPrefixLine(res.Lines, b.ID+": not observed: rate limited") {
		t.Errorf("result = %+v", res)
	}
}

func TestApplyIsIdempotent(t *testing.T) {
	e := newEnv(t)
	e.origin(t)
	e.mergedOnHost(t, "Merged")
	_, pr := e.slice(t, "Open", units.PullRequestOpen)
	e.feedbackOn(pr, "c1")
	first := e.apply(t, allKinds...)
	if first.Recorded != 1 || first.Closed != 1 || first.Created != 1 {
		t.Fatalf("first = %+v", first)
	}
	snap := e.snapshot(t)
	second := e.apply(t, allKinds...)
	if second.Recorded != 0 || second.Closed != 0 || second.Created != 0 || len(second.Failures) != 0 || e.snapshot(t) != snap {
		t.Errorf("second = %+v", second)
	}
}
