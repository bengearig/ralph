package followup_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/bengearig/ralph/internal/followup"
	"github.com/bengearig/ralph/internal/units"
)

// readySlice adds a slice with one TODO task, so its status is READY.
func (e *env) readySlice(t *testing.T, title string) units.Unit {
	t.Helper()
	s := e.create(t, units.NewUnit{Level: units.LevelSlice, ParentID: e.brief.ID, Title: title, Body: "b"})
	task := e.create(t, units.NewUnit{Level: units.LevelTask, ParentID: s.ID, Title: "Task of " + title, Body: "b", AcceptanceCriteria: "c"})
	if _, err := e.units.SetState(ctx, task.ID, task.Fingerprint, units.StateTodo, "ready"); err != nil {
		t.Fatal(err)
	}
	return s
}

func (e *env) autoStarted(t *testing.T, s units.Unit) bool {
	t.Helper()
	links, err := e.units.Links(ctx, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	return slices.ContainsFunc(links, func(l units.Link) bool {
		return l.Kind == units.LinkExternal && l.Target == followup.LinkAutoStarted
	})
}

type stubLoop struct {
	calls  []string
	status int
	err    error
	run    func(string)
}

func (s *stubLoop) Run(_ context.Context, id string) (int, error) {
	s.calls = append(s.calls, id)
	if s.run != nil {
		s.run(id)
	}
	return s.status, s.err
}

func (e *env) mergedObservation(t *testing.T, s units.Unit) followup.Observation {
	t.Helper()
	next, err := followup.NextSlice(ctx, e.units, s)
	if err != nil {
		t.Fatal(err)
	}
	return followup.Observation{Slice: s, Record: e.record(t, s), NextSlice: next}
}

func continues(plan []followup.Followup) []followup.Followup {
	var out []followup.Followup
	for _, f := range plan {
		if f.Kind == followup.KindContinue {
			out = append(out, f)
		}
	}
	return out
}

func TestPlanContinueNextByPosition(t *testing.T) {
	e := newEnv(t)
	first, _ := e.slice(t, "First", units.PullRequestMerged)
	two := e.readySlice(t, "Two")
	e.readySlice(t, "Three")
	plan := followup.Plan([]followup.Observation{e.mergedObservation(t, first)}, followup.PlanOptions{})
	got := continues(plan)
	if len(got) != 1 || got[0].Slice != first.ID || got[0].Next != two.ID || got[0].Number != 0 ||
		got[0].Summary != "start the loop for "+two.ID {
		t.Errorf("plan = %+v", plan)
	}
}

func TestPlanContinueSkipsWaitingAndNotReady(t *testing.T) {
	e := newEnv(t)
	first, _ := e.slice(t, "First", units.PullRequestMerged)
	empty := e.create(t, units.NewUnit{Level: units.LevelSlice, ParentID: e.brief.ID, Title: "Empty", Body: "b"})
	waiting := e.readySlice(t, "Waiting")
	blocker := e.readySlice(t, "Blocker")
	if err := e.units.AddDependency(ctx, waiting.ID, blocker.ID); err != nil {
		t.Fatal(err)
	}
	next, err := followup.NextSlice(ctx, e.units, first)
	if err != nil || next != blocker.ID {
		t.Errorf("next = %q, %v; want %s (not %s or %s)", next, err, blocker.ID, empty.ID, waiting.ID)
	}
}

func TestPlanContinueSkipsAutoStarted(t *testing.T) {
	e := newEnv(t)
	first, _ := e.slice(t, "First", units.PullRequestMerged)
	two := e.readySlice(t, "Two")
	three := e.readySlice(t, "Three")
	if err := e.units.AddLink(ctx, two.ID, units.LinkExternal, followup.LinkAutoStarted); err != nil {
		t.Fatal(err)
	}
	next, err := followup.NextSlice(ctx, e.units, first)
	if err != nil || next != three.ID {
		t.Errorf("next = %q, %v; want %s", next, err, three.ID)
	}
}

func TestPlanContinueNoNextSlice(t *testing.T) {
	e := newEnv(t)
	e.readySlice(t, "Earlier")
	last, _ := e.slice(t, "Last", units.PullRequestMerged)
	plan := followup.Plan([]followup.Observation{e.mergedObservation(t, last)}, followup.PlanOptions{})
	if got := continues(plan); len(got) != 0 {
		t.Errorf("plan = %+v", plan)
	}
}

func TestPlanContinueNeedsBrief(t *testing.T) {
	e := newEnv(t)
	next, err := followup.NextSlice(ctx, e.units, units.Unit{ID: "S-9", Level: units.LevelSlice})
	if err != nil || next != "" {
		t.Errorf("next = %q, %v", next, err)
	}
}

func TestPlanContinueOnlyForMerged(t *testing.T) {
	e := newEnv(t)
	open, _ := e.slice(t, "Open", units.PullRequestOpen)
	closed, _ := e.slice(t, "Closed", units.PullRequestClosed)
	two := e.readySlice(t, "Two")
	for _, s := range []units.Unit{open, closed} {
		o := followup.Observation{Slice: s, Record: e.record(t, s), NextSlice: two.ID}
		if got := continues(followup.Plan([]followup.Observation{o}, followup.PlanOptions{})); len(got) != 0 {
			t.Errorf("%s: plan = %+v", s.ID, got)
		}
	}
}

type continueCase struct {
	e      *env
	merged units.Unit
	next   units.Unit
	f      followup.Followup
}

func newContinue(t *testing.T) *continueCase {
	t.Helper()
	e := newEnv(t)
	e.origin(t)
	merged, _ := e.slice(t, "First", units.PullRequestMerged)
	next := e.readySlice(t, "Second")
	return &continueCase{e: e, merged: merged, next: next, f: followup.Followup{
		Kind: followup.KindContinue, Slice: merged.ID, Next: next.ID, Summary: "start the loop for " + next.ID,
	}}
}

func (c *continueCase) refused(t *testing.T, reason string) {
	t.Helper()
	stub := &stubLoop{}
	res, err := followup.Continue(ctx, c.e.deps, stub.Run, c.f)
	if err != nil {
		t.Fatal(err)
	}
	want := "not starting " + c.next.ID + ": "
	if len(res.Lines) != 1 || !strings.HasPrefix(res.Lines[0], want) || !strings.Contains(res.Lines[0], reason) {
		t.Errorf("lines = %q, want %q...%q", res.Lines, want, reason)
	}
	if len(stub.calls) != 0 || c.e.autoStarted(t, c.next) || res.Slice != "" {
		t.Errorf("calls = %v, linked = %v, slice = %q", stub.calls, c.e.autoStarted(t, c.next), res.Slice)
	}
}

func TestContinueRefusesOffBase(t *testing.T) {
	c := newContinue(t)
	c.e.repo.Git("switch", "-c", "feature")
	c.refused(t, `the current branch is "feature"`)
}

func TestContinueRefusesDirtyTree(t *testing.T) {
	c := newContinue(t)
	c.e.repo.WriteFile("dirty.txt", "x")
	c.refused(t, "the working tree has changes: dirty.txt")
}

func TestContinueRefusesBehindRemote(t *testing.T) {
	c := newContinue(t)
	other := t.TempDir()
	c.e.repo.Git("clone", c.e.repo.Git("remote", "get-url", "origin"), other)
	c.e.repo.Git("-C", other, "config", "user.name", "Other")
	c.e.repo.Git("-C", other, "config", "user.email", "o@example.com")
	c.e.repo.Git("-C", other, "commit", "--allow-empty", "-m", "upstream")
	c.e.repo.Git("-C", other, "push", "origin", "main")
	c.e.repo.WriteFile("local.txt", "x")
	c.e.repo.Commit("local")
	c.refused(t, "cannot be fast-forwarded")
}

func TestContinueRefusesAheadOfRemote(t *testing.T) {
	c := newContinue(t)
	c.e.repo.WriteFile("local.txt", "x")
	c.e.repo.Commit("local")
	c.refused(t, "1 commit(s) that origin/main does not")
}

func TestContinueAddsLinkNoteAndRuns(t *testing.T) {
	c := newContinue(t)
	stub := &stubLoop{run: func(string) {
		if !c.e.autoStarted(t, c.next) {
			t.Error("the link was not added before the loop ran")
		}
	}}
	res, err := followup.Continue(ctx, c.e.deps, stub.Run, c.f)
	if err != nil || res.Slice != c.next.ID || res.Status != 0 {
		t.Fatalf("result = %+v, %v", res, err)
	}
	if !slices.Equal(stub.calls, []string{c.next.ID}) || !c.e.autoStarted(t, c.next) {
		t.Errorf("calls = %v", stub.calls)
	}
	notes, err := c.e.units.Notes(ctx, c.next.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := "Started automatically after " + c.merged.ID + " was merged."
	if !slices.ContainsFunc(notes, func(n units.Note) bool { return n.Body == want }) {
		t.Errorf("notes = %+v", notes)
	}
	again, err := followup.NextSlice(ctx, c.e.units, c.merged)
	if err != nil || again != "" {
		t.Errorf("next after start = %q, %v", again, err)
	}
}

func TestContinueLinkStaysAfterLoopFailure(t *testing.T) {
	c := newContinue(t)
	stub := &stubLoop{status: 2, err: errors.New("stalled")}
	res, err := followup.Continue(ctx, c.e.deps, stub.Run, c.f)
	if err == nil || res.Status != 2 || res.Slice != c.next.ID || !c.e.autoStarted(t, c.next) {
		t.Errorf("result = %+v, %v, linked = %v", res, err, c.e.autoStarted(t, c.next))
	}
}

func (e *env) openNext(t *testing.T, title string) units.Unit {
	t.Helper()
	return e.readySlice(t, title)
}

func TestApplyContinueOnlyWhenSelected(t *testing.T) {
	e := newEnv(t)
	e.origin(t)
	merged, _ := e.mergedOnHost(t, "First")
	next := e.openNext(t, "Second")
	stub := &stubLoop{}
	e.deps.Loop = stub.Run
	res := e.apply(t, followup.KindClose)
	if len(stub.calls) != 0 || e.autoStarted(t, next) || res.LoopStatus != 0 {
		t.Errorf("calls = %v, result = %+v", stub.calls, res)
	}
	want := "not applied: continue: " + merged.ID + ": start the loop for " + next.ID + "; apply it with --continue"
	if !slices.Contains(res.Lines, want) {
		t.Errorf("lines = %q, want %q", res.Lines, want)
	}
}

func TestApplyContinueOnceOnly(t *testing.T) {
	e := newEnv(t)
	e.origin(t)
	e.mergedOnHost(t, "First")
	e.mergedOnHost(t, "Second")
	third := e.openNext(t, "Third")
	stub := &stubLoop{}
	e.deps.Loop = stub.Run
	res := e.apply(t, followup.KindClose, followup.KindContinue)
	if !slices.Equal(stub.calls, []string{third.ID}) {
		t.Errorf("calls = %v", stub.calls)
	}
	if !slices.Contains(res.Lines, "not applied: continue: only one slice is started per run") || res.NotApplied != 1 {
		t.Errorf("result = %+v", res)
	}
}

func TestApplyContinueStopsAfterFailureStatus(t *testing.T) {
	e := newEnv(t)
	e.origin(t)
	e.mergedOnHost(t, "First")
	e.mergedOnHost(t, "Second")
	third := e.openNext(t, "Third")
	stub := &stubLoop{status: 2}
	e.deps.Loop = stub.Run
	res := e.apply(t, followup.KindContinue)
	if res.LoopStatus != 2 || !slices.Equal(stub.calls, []string{third.ID}) || len(res.Failures) != 1 {
		t.Errorf("calls = %v, result = %+v", stub.calls, res)
	}
	if !slices.Contains(res.Lines, "not applied: continue: only one slice is started per run") {
		t.Errorf("lines = %q", res.Lines)
	}

	e2 := newEnv(t)
	e2.origin(t)
	e2.mergedOnHost(t, "First")
	e2.openNext(t, "Second")
	failing := &stubLoop{err: errors.New("boom")}
	e2.deps.Loop = failing.Run
	res = e2.apply(t, followup.KindContinue)
	if res.LoopStatus != 1 || !strings.Contains(strings.Join(res.Failures, "\n"), "boom") {
		t.Errorf("result = %+v", res)
	}
}

func TestApplyContinueAfterClose(t *testing.T) {
	e := newEnv(t)
	e.origin(t)
	merged, name := e.mergedOnHost(t, "First")
	next := e.openNext(t, "Second")
	if err := e.units.AddDependency(ctx, next.ID, merged.ID); err != nil {
		t.Fatal(err)
	}
	stub := &stubLoop{run: func(string) {
		if e.branchExists(t, name) {
			t.Error("the branch was not closed before the loop ran")
		}
	}}
	e.deps.Loop = stub.Run
	res := e.apply(t, followup.KindClose, followup.KindContinue)
	if res.Recorded != 1 || res.Closed != 1 || !slices.Equal(stub.calls, []string{next.ID}) || res.LoopStatus != 0 {
		t.Fatalf("calls = %v, result = %+v", stub.calls, res)
	}
	if got := e.record(t, merged).State; got != units.PullRequestMerged || !e.autoStarted(t, next) {
		t.Errorf("state = %s, linked = %v", got, e.autoStarted(t, next))
	}
}

func TestApplyContinueNilRunner(t *testing.T) {
	e := newEnv(t)
	e.origin(t)
	e.mergedOnHost(t, "First")
	next := e.openNext(t, "Second")
	res := e.apply(t, followup.KindContinue)
	if !slices.Contains(res.Lines, "not applied: continue: no loop runner") || e.autoStarted(t, next) || res.LoopStatus != 0 {
		t.Errorf("result = %+v", res)
	}
}
