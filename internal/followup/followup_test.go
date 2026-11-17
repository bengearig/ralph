package followup_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/bengearig/ralph/internal/db"
	"github.com/bengearig/ralph/internal/followup"
	"github.com/bengearig/ralph/internal/hosting"
	"github.com/bengearig/ralph/internal/hosting/fake"
	"github.com/bengearig/ralph/internal/prstep"
	"github.com/bengearig/ralph/internal/quick"
	"github.com/bengearig/ralph/internal/testutil"
	"github.com/bengearig/ralph/internal/units"
)

var ctx = context.Background()

const branchTemplate = "ralph/{{.ID}}"

type env struct {
	repo     *testutil.Repo
	units    units.Service
	brief    units.Unit
	provider *fake.Provider
	deps     followup.Deps
	slices   []units.Unit
}

func newEnv(t *testing.T) *env {
	t.Helper()
	testutil.IsolateEnv(t)
	repo := testutil.NewRepo(t)
	e := &env{repo: repo, provider: fake.New()}
	e.units = units.NewService(testutil.NewDB(t, db.Migrations))
	e.brief = e.create(t, units.NewUnit{Level: units.LevelBrief, Title: "Brief", Body: "b"})
	ws, err := quick.NewLocalWorkspace(repo.Dir)
	if err != nil {
		t.Fatal(err)
	}
	provider := func(context.Context) (hosting.Provider, error) { return e.provider, nil }
	e.deps = followup.Deps{
		PR: prstep.Deps{
			Units: e.units, Workspace: ws, Provider: provider,
			Config: prstep.Config{Root: repo.Dir, Remote: "origin", Base: "main", BranchTemplate: "git.branch"},
		},
		Units: e.units, Workspace: ws, Provider: provider,
	}
	e.deps.PR.Config.BranchTemplate = branchTemplate
	return e
}

func (e *env) create(t *testing.T, n units.NewUnit) units.Unit {
	t.Helper()
	u, err := e.units.Create(ctx, n)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// slice adds a slice with a pull request on the fake host in the given recorded state.
func (e *env) slice(t *testing.T, title string, state units.PullRequestState) (units.Unit, hosting.PullRequest) {
	t.Helper()
	s := e.create(t, units.NewUnit{Level: units.LevelSlice, ParentID: e.brief.ID, Title: title, Body: "b"})
	e.slices = append(e.slices, s)
	pr := e.pr(t, s, state)
	return s, pr
}

func (e *env) pr(t *testing.T, s units.Unit, state units.PullRequestState) hosting.PullRequest {
	t.Helper()
	pr, err := e.provider.CreatePullRequest(ctx, hosting.NewPullRequest{Head: "ralph/" + s.ID, Base: "main", Title: s.Title})
	if err != nil {
		t.Fatal(err)
	}
	rec := units.PullRequestRecord{SliceID: s.ID, Number: pr.Number, URL: pr.URL, Head: "ralph/" + s.ID, Base: "main", State: units.PullRequestOpen}
	if err := e.units.RecordPullRequest(ctx, rec); err != nil {
		t.Fatal(err)
	}
	if state != units.PullRequestOpen {
		if err := e.units.SetPullRequestState(ctx, s.ID, pr.Number, state); err != nil {
			t.Fatal(err)
		}
	}
	return pr
}

func (e *env) record(t *testing.T, s units.Unit) units.PullRequestRecord {
	t.Helper()
	recs, err := e.units.PullRequests(ctx, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	return recs[len(recs)-1]
}

func (e *env) snapshot(t *testing.T) string {
	t.Helper()
	var b strings.Builder
	all, err := e.units.List(ctx, units.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range all {
		fmt.Fprintf(&b, "%+v\n", u)
		notes, _ := e.units.Notes(ctx, u.ID)
		fmt.Fprintf(&b, "%+v\n", notes)
		links, _ := e.units.Links(ctx, u.ID)
		fmt.Fprintf(&b, "%+v\n", links)
		if u.Level == units.LevelSlice {
			recs, _ := e.units.PullRequests(ctx, u.ID)
			fmt.Fprintf(&b, "%+v\n", recs)
		}
	}
	b.WriteString(e.repo.Git("for-each-ref"))
	return b.String()
}

func (e *env) branch(t *testing.T, s units.Unit) {
	t.Helper()
	name, err := prstep.BranchName(branchTemplate, s)
	if err != nil {
		t.Fatal(err)
	}
	e.repo.Git("branch", name)
}

func (e *env) byID(t *testing.T, res followup.ObserveResult, id string) followup.Observation {
	t.Helper()
	for _, o := range res.Observations {
		if o.Slice.ID == id {
			return o
		}
	}
	t.Fatalf("no observation for %s in %+v", id, res.Observations)
	return followup.Observation{}
}

func observe(t *testing.T, e *env, opt followup.ObserveOptions) followup.ObserveResult {
	t.Helper()
	res, err := followup.Observe(ctx, e.deps, opt)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func inline(id string) hosting.Feedback {
	return hosting.Feedback{ID: id, Kind: hosting.KindInline, Author: "rev", Body: "fix " + id, Path: "a.go", Line: 1, CreatedAt: time.Now()}
}

func TestObserveWritesNothing(t *testing.T) {
	e := newEnv(t)
	open, openPR := e.slice(t, "Open", units.PullRequestOpen)
	e.provider.AddFeedback(openPR.Number, inline("c1"))
	e.slice(t, "Closed", units.PullRequestClosed)
	_, mergedPR := e.slice(t, "Merged", units.PullRequestMerged)
	e.provider.Merge(mergedPR.Number)
	e.branch(t, open)
	before := e.snapshot(t)
	for _, poll := range []bool{false, true} {
		observe(t, e, followup.ObserveOptions{Poll: poll})
		followup.Plan(observe(t, e, followup.ObserveOptions{Poll: poll}).Observations, followup.PlanOptions{})
		if after := e.snapshot(t); after != before {
			t.Fatalf("poll=%v changed state:\n%s\n---\n%s", poll, before, after)
		}
	}
}

func TestObserveDefaultsToSlicesWithPullRequests(t *testing.T) {
	e := newEnv(t)
	e.slice(t, "One", units.PullRequestOpen)
	e.create(t, units.NewUnit{Level: units.LevelSlice, ParentID: e.brief.ID, Title: "No PR", Body: "b"})
	e.slice(t, "Three", units.PullRequestOpen)
	res := observe(t, e, followup.ObserveOptions{})
	if len(res.Observations) != 2 || res.Observations[0].Slice.ID != e.slices[0].ID || res.Observations[1].Slice.ID != e.slices[1].ID {
		t.Fatalf("observations = %+v", res.Observations)
	}
	if res.Observations[0].Slice.Number >= res.Observations[1].Slice.Number {
		t.Error("not ordered by number")
	}
}

func TestObservePollsOpenAndUnresolvedClosedOnly(t *testing.T) {
	e := newEnv(t)
	open, _ := e.slice(t, "Open", units.PullRequestOpen)
	closed, _ := e.slice(t, "Closed", units.PullRequestClosed)
	resolved, _ := e.slice(t, "Resolved", units.PullRequestClosed)
	merged, _ := e.slice(t, "Merged", units.PullRequestMerged)
	if err := e.units.ResolvePullRequest(ctx, resolved.ID, e.record(t, resolved).Number, "cancelled"); err != nil {
		t.Fatal(err)
	}
	res := observe(t, e, followup.ObserveOptions{Poll: true})
	for id, want := range map[string]bool{open.ID: true, closed.ID: true, resolved.ID: false, merged.ID: false} {
		if got := e.byID(t, res, id).Polled; got != want {
			t.Errorf("%s polled = %v", id, got)
		}
	}
	for _, c := range e.provider.Calls() {
		if c.Number == e.record(t, resolved).Number || c.Number == e.record(t, merged).Number {
			if c.Op != "create" {
				t.Errorf("unexpected call %+v", c)
			}
		}
	}
}

func TestObserveNoPollUsesRecordedState(t *testing.T) {
	e := newEnv(t)
	e.slice(t, "Open", units.PullRequestOpen)
	calls := len(e.provider.Calls())
	res := observe(t, e, followup.ObserveOptions{})
	if o := res.Observations[0]; o.Polled || o.Record.State != units.PullRequestOpen || o.PullRequest.Number != 0 {
		t.Errorf("observation = %+v", o)
	}
	if len(e.provider.Calls()) != calls {
		t.Error("the provider was called")
	}
}

func TestObserveNewFeedbackExcludesHandledAndIneligible(t *testing.T) {
	e := newEnv(t)
	s, pr := e.slice(t, "Open", units.PullRequestOpen)
	e.provider.AddFeedback(pr.Number, inline("new"))
	e.provider.AddFeedback(pr.Number, inline("done"))
	e.provider.AddFeedback(pr.Number, hosting.Feedback{ID: "chat", Kind: hosting.KindComment, Body: "hi", CreatedAt: time.Now()})
	task := e.create(t, units.NewUnit{Level: units.LevelTask, ParentID: s.ID, Title: "t", Body: "b", AcceptanceCriteria: "c"})
	if err := e.units.AddLink(ctx, task.ID, units.LinkExternal, prstep.FeedbackKey(pr.Number, "done")); err != nil {
		t.Fatal(err)
	}
	o := observe(t, e, followup.ObserveOptions{Poll: true}).Observations[0]
	if len(o.NewFeedback) != 1 || o.NewFeedback[0].Feedback.ID != "new" {
		t.Errorf("new feedback = %+v", o.NewFeedback)
	}
}

func TestObserveOneFailureDoesNotStopOthers(t *testing.T) {
	e := newEnv(t)
	gone, _ := e.slice(t, "Gone", units.PullRequestOpen)
	if err := e.units.RecordPullRequest(ctx, units.PullRequestRecord{SliceID: gone.ID, Number: 99, URL: "https://x.test/99", Head: "h", Base: "main", State: units.PullRequestClosed}); err != nil {
		t.Fatal(err)
	}
	other, _ := e.slice(t, "Other", units.PullRequestOpen)
	res := observe(t, e, followup.ObserveOptions{Poll: true})
	g := e.byID(t, res, gone.ID)
	if !errors.Is(g.Err, hosting.ErrNotFound) || g.Polled {
		t.Errorf("gone = %+v", g)
	}
	if o := e.byID(t, res, other.ID); o.Err != nil || !o.Polled {
		t.Errorf("other = %+v", o)
	}
}

func TestObserveRateLimitStopsPolling(t *testing.T) {
	e := newEnv(t)
	e.slice(t, "One", units.PullRequestOpen)
	e.slice(t, "Two", units.PullRequestOpen)
	reset := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	e.provider.RateLimit(reset)
	calls := len(e.provider.Calls())
	res := observe(t, e, followup.ObserveOptions{Poll: true})
	if res.RateLimited == nil || !res.RateLimited.ResetAt.Equal(reset) {
		t.Fatalf("rate limited = %v", res.RateLimited)
	}
	for _, o := range res.Observations {
		if o.Polled || o.Err != nil {
			t.Errorf("observation = %+v", o)
		}
	}
	if len(e.provider.Calls())-calls != 1 {
		t.Errorf("calls = %v", e.provider.Calls())
	}
}

func TestObserveProviderBuildFailure(t *testing.T) {
	e := newEnv(t)
	s, _ := e.slice(t, "One", units.PullRequestOpen)
	e.branch(t, s)
	e.deps.Provider = func(context.Context) (hosting.Provider, error) { return nil, errors.New("no token") }
	res := observe(t, e, followup.ObserveOptions{Poll: true})
	if res.ProviderErr == nil || res.ProviderErr.Error() != "no token" {
		t.Fatalf("provider err = %v", res.ProviderErr)
	}
	o := res.Observations[0]
	if o.Polled || o.Record.State != units.PullRequestOpen || !o.LocalBranchExists {
		t.Errorf("observation = %+v", o)
	}
}

func TestObserveUnknownAndNonSliceIDs(t *testing.T) {
	e := newEnv(t)
	s, _ := e.slice(t, "One", units.PullRequestOpen)
	task := e.create(t, units.NewUnit{Level: units.LevelTask, ParentID: s.ID, Title: "t", Body: "b", AcceptanceCriteria: "c"})
	for _, id := range []string{"S-99", e.brief.ID, task.ID} {
		if _, err := followup.Observe(ctx, e.deps, followup.ObserveOptions{Slices: []string{id}}); err == nil {
			t.Errorf("%s: no error", id)
		}
	}
}

func TestObserveLatestRecordOfTwo(t *testing.T) {
	e := newEnv(t)
	s, first := e.slice(t, "One", units.PullRequestClosed)
	e.provider.Close(first.Number)
	second := e.pr(t, s, units.PullRequestOpen)
	o := observe(t, e, followup.ObserveOptions{Poll: true}).Observations[0]
	if o.Record.Number != second.Number || o.Record.Number == first.Number || !o.Polled {
		t.Errorf("observation = %+v", o)
	}
}

func TestObserveLocalBranchExists(t *testing.T) {
	e := newEnv(t)
	with, _ := e.slice(t, "With", units.PullRequestOpen)
	without, _ := e.slice(t, "Without", units.PullRequestOpen)
	e.branch(t, with)
	res := observe(t, e, followup.ObserveOptions{})
	w, wo := e.byID(t, res, with.ID), e.byID(t, res, without.ID)
	if !w.LocalBranchExists || w.LocalBranch != "ralph/"+with.ID {
		t.Errorf("with = %+v", w)
	}
	if wo.LocalBranchExists || wo.LocalBranch != "ralph/"+without.ID {
		t.Errorf("without = %+v", wo)
	}
}

func obs(num int, rec units.PullRequestState, res string) followup.Observation {
	return followup.Observation{
		Slice:  units.Unit{ID: fmt.Sprintf("S-%d", num), Number: num, Level: units.LevelSlice},
		Record: units.PullRequestRecord{SliceID: fmt.Sprintf("S-%d", num), Number: num * 10, State: rec, Resolution: res},
	}
}

func polled(o followup.Observation, state hosting.PRState) followup.Observation {
	o.Polled = true
	o.PullRequest = hosting.PullRequest{Number: o.Record.Number, State: state}
	return o
}

func kinds(f []followup.Followup) []followup.Kind {
	var k []followup.Kind
	for _, x := range f {
		k = append(k, x.Kind)
	}
	return k
}

func TestPlanRecordWhenHostDiffers(t *testing.T) {
	got := followup.Plan([]followup.Observation{polled(obs(1, units.PullRequestOpen, ""), hosting.StateMerged)}, followup.PlanOptions{})
	if len(got) == 0 || got[0].Kind != followup.KindRecord || got[0].Number != 10 || got[0].Slice != "S-1" ||
		got[0].Summary != "pull request #10: merged on the host (recorded: open)" {
		t.Errorf("plan = %+v", got)
	}
	same := followup.Plan([]followup.Observation{polled(obs(1, units.PullRequestOpen, ""), hosting.StateOpen)}, followup.PlanOptions{})
	if len(same) != 0 {
		t.Errorf("plan = %+v", same)
	}
}

func TestPlanDecisionForClosedUnresolved(t *testing.T) {
	got := followup.Plan([]followup.Observation{polled(obs(2, units.PullRequestOpen, ""), hosting.StateClosed)}, followup.PlanOptions{})
	want := "pull request #20 was closed without merging; choose ralph pr reopen, rearticulate or cancel S-2"
	if len(got) != 2 || got[1].Kind != followup.KindDecision || got[1].Summary != want {
		t.Errorf("plan = %+v", got)
	}
	if got := followup.Plan([]followup.Observation{obs(2, units.PullRequestClosed, "cancelled")}, followup.PlanOptions{}); len(got) != 0 {
		t.Errorf("resolved plan = %+v", got)
	}
	if got := followup.Plan([]followup.Observation{obs(2, units.PullRequestClosed, "")}, followup.PlanOptions{}); len(got) != 1 || got[0].Kind != followup.KindDecision {
		t.Errorf("recorded plan = %+v", got)
	}
}

func TestPlanFeedbackWhenNewItems(t *testing.T) {
	o := polled(obs(1, units.PullRequestOpen, ""), hosting.StateOpen)
	o.NewFeedback = []prstep.FeedbackItem{{}}
	got := followup.Plan([]followup.Observation{o}, followup.PlanOptions{})
	if len(got) != 1 || got[0].Kind != followup.KindFeedback || got[0].Summary != "1 new review feedback item" {
		t.Errorf("singular = %+v", got)
	}
	o.NewFeedback = []prstep.FeedbackItem{{}, {}}
	got = followup.Plan([]followup.Observation{o}, followup.PlanOptions{})
	if got[0].Summary != "2 new review feedback items" {
		t.Errorf("plural = %+v", got)
	}
	closed := polled(o, hosting.StateClosed)
	if k := kinds(followup.Plan([]followup.Observation{closed}, followup.PlanOptions{})); len(k) != 2 || k[0] != followup.KindRecord || k[1] != followup.KindDecision {
		t.Errorf("closed kinds = %v", k)
	}
}

func TestPlanCloseWhenMergedAndBranchExists(t *testing.T) {
	o := obs(1, units.PullRequestMerged, "")
	o.LocalBranch, o.LocalBranchExists = "ralph/s-1", true
	got := followup.Plan([]followup.Observation{o}, followup.PlanOptions{})
	if len(got) != 1 || got[0].Kind != followup.KindClose || got[0].Summary != "delete branch ralph/s-1" {
		t.Errorf("plan = %+v", got)
	}
	o.LocalBranchExists = false
	if got := followup.Plan([]followup.Observation{o}, followup.PlanOptions{}); len(got) != 0 {
		t.Errorf("no branch plan = %+v", got)
	}
	open := obs(1, units.PullRequestOpen, "")
	open.LocalBranchExists = true
	if got := followup.Plan([]followup.Observation{open}, followup.PlanOptions{}); len(got) != 0 {
		t.Errorf("open plan = %+v", got)
	}
}

func TestPlanCloseRespectsDeleteMode(t *testing.T) {
	o := obs(1, units.PullRequestMerged, "")
	o.LocalBranch, o.LocalBranchExists = "b", true
	for mode, want := range map[string]int{"none": 0, "": 1, "local": 1, "both": 1} {
		if got := followup.Plan([]followup.Observation{o}, followup.PlanOptions{DeleteBranches: mode}); len(got) != want {
			t.Errorf("mode %q: plan = %+v", mode, got)
		}
	}
}

func TestPlanUnpolledUsesRecordedState(t *testing.T) {
	o := obs(1, units.PullRequestOpen, "")
	o.PullRequest = hosting.PullRequest{State: hosting.StateMerged}
	o.NewFeedback = []prstep.FeedbackItem{{}}
	got := followup.Plan([]followup.Observation{o}, followup.PlanOptions{})
	if len(got) != 1 || got[0].Kind != followup.KindFeedback {
		t.Errorf("plan = %+v", got)
	}
	failed := polled(obs(1, units.PullRequestOpen, ""), hosting.StateMerged)
	failed.Err = errors.New("x")
	if got := followup.Plan([]followup.Observation{failed}, followup.PlanOptions{}); len(got) != 0 {
		t.Errorf("failed plan = %+v", got)
	}
}

func TestPlanOrderBySliceThenKind(t *testing.T) {
	a := polled(obs(2, units.PullRequestOpen, ""), hosting.StateMerged)
	a.LocalBranch, a.LocalBranchExists = "b2", true
	b := polled(obs(1, units.PullRequestOpen, ""), hosting.StateOpen)
	b.NewFeedback = []prstep.FeedbackItem{{}}
	c := polled(obs(1, units.PullRequestClosed, ""), hosting.StateClosed)
	c.Slice.ID, c.Slice.Number = "S-3", 3
	got := followup.Plan([]followup.Observation{c, a, b}, followup.PlanOptions{})
	var order []string
	for _, f := range got {
		order = append(order, f.Slice+":"+string(f.Kind))
	}
	want := "S-1:feedback S-2:record S-2:close S-3:decision"
	if strings.Join(order, " ") != want {
		t.Errorf("order = %v", order)
	}
	if (followup.Kind("continue")).Order() <= followup.KindClose.Order() {
		t.Error("continue does not come last")
	}
}

func TestPlanIsPure(t *testing.T) {
	o := polled(obs(1, units.PullRequestOpen, ""), hosting.StateClosed)
	o.NewFeedback = []prstep.FeedbackItem{{}}
	in := []followup.Observation{o, obs(0, units.PullRequestMerged, "")}
	in[1].LocalBranchExists = true
	snap := fmt.Sprintf("%+v", in)
	followup.Plan(in, followup.PlanOptions{})
	if fmt.Sprintf("%+v", in) != snap {
		t.Error("input modified")
	}
}

func TestRecordAppliesChange(t *testing.T) {
	e := newEnv(t)
	s, pr := e.slice(t, "One", units.PullRequestOpen)
	e.provider.Close(pr.Number)
	o := observe(t, e, followup.ObserveOptions{Poll: true}).Observations[0]
	got, err := followup.Record(ctx, e.deps, o)
	if err != nil {
		t.Fatal(err)
	}
	want := "Pull request " + pr.URL + " was closed without merging."
	if got.Kind != prstep.ChangeClosed || got.Message != want {
		t.Errorf("change = %+v", got)
	}
	if e.record(t, s).State != units.PullRequestClosed {
		t.Errorf("record = %+v", e.record(t, s))
	}
	notes, _ := e.units.Notes(ctx, s.ID)
	if notes[len(notes)-1].Body != want {
		t.Errorf("notes = %+v", notes)
	}
}

func TestRecordUnpolledIsUnchanged(t *testing.T) {
	e := newEnv(t)
	s, pr := e.slice(t, "One", units.PullRequestOpen)
	e.provider.Close(pr.Number)
	o := observe(t, e, followup.ObserveOptions{}).Observations[0]
	got, err := followup.Record(ctx, e.deps, o)
	if err != nil || got.Kind != prstep.ChangeUnchanged || got.Message != "" {
		t.Errorf("change = %+v, %v", got, err)
	}
	o.Polled, o.Err = true, errors.New("x")
	if got, err := followup.Record(ctx, e.deps, o); err != nil || got.Kind != prstep.ChangeUnchanged {
		t.Errorf("errored change = %+v, %v", got, err)
	}
	if e.record(t, s).State != units.PullRequestOpen {
		t.Error("record changed")
	}
}

func TestCheckSliceRecordsClosure(t *testing.T) {
	e := newEnv(t)
	s, pr := e.slice(t, "One", units.PullRequestOpen)
	e.provider.Close(pr.Number)
	o, change, err := followup.CheckSlice(ctx, e.deps, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if change.Kind != prstep.ChangeClosed || o.Slice.ID != s.ID || o.Record.Number != pr.Number || o.PullRequest.State != hosting.StateClosed {
		t.Errorf("observation %+v, change %+v", o, change)
	}
	if e.record(t, s).State != units.PullRequestClosed {
		t.Error("closure not recorded")
	}
	_, change, err = followup.CheckSlice(ctx, e.deps, s.ID)
	if err != nil || change.Kind != prstep.ChangeUnchanged {
		t.Errorf("second check = %+v, %v", change, err)
	}
	for _, c := range e.provider.Calls() {
		if c.Op == "feedback" {
			t.Error("check read feedback")
		}
	}
}

func TestCheckSliceErrors(t *testing.T) {
	t.Run("observation error", func(t *testing.T) {
		e := newEnv(t)
		s, _ := e.slice(t, "One", units.PullRequestOpen)
		if err := e.units.RecordPullRequest(ctx, units.PullRequestRecord{SliceID: s.ID, Number: 77, URL: "https://x.test/77", Head: "h", Base: "main", State: units.PullRequestOpen}); err != nil {
			t.Fatal(err)
		}
		_, _, err := followup.CheckSlice(ctx, e.deps, s.ID)
		if !errors.Is(err, hosting.ErrNotFound) || !strings.Contains(err.Error(), "gone from the host") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("provider failure", func(t *testing.T) {
		e := newEnv(t)
		s, _ := e.slice(t, "One", units.PullRequestOpen)
		e.deps.Provider = func(context.Context) (hosting.Provider, error) { return nil, errors.New("no token") }
		if _, _, err := followup.CheckSlice(ctx, e.deps, s.ID); err == nil || err.Error() != "no token" {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("rate limit", func(t *testing.T) {
		e := newEnv(t)
		s, _ := e.slice(t, "One", units.PullRequestOpen)
		e.provider.RateLimit(time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC))
		_, _, err := followup.CheckSlice(ctx, e.deps, s.ID)
		var rl *hosting.RateLimitError
		if !errors.As(err, &rl) || !errors.Is(err, hosting.ErrRateLimited) {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("no pull request and not a slice", func(t *testing.T) {
		e := newEnv(t)
		s := e.create(t, units.NewUnit{Level: units.LevelSlice, ParentID: e.brief.ID, Title: "Bare", Body: "b"})
		_, _, err := followup.CheckSlice(ctx, e.deps, s.ID)
		if err == nil || err.Error() != s.ID+" has no pull request: run ralph pr create "+s.ID {
			t.Errorf("err = %v", err)
		}
		if _, _, err := followup.CheckSlice(ctx, e.deps, e.brief.ID); err == nil {
			t.Error("brief accepted")
		}
	})
}
