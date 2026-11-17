package followup_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/bengearig/ralph/internal/followup"
	"github.com/bengearig/ralph/internal/hosting"
	"github.com/bengearig/ralph/internal/units"
)

var viewTime = time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)

func view(t *testing.T, e *env, opt followup.ViewOptions) followup.View {
	t.Helper()
	opt.Now = func() time.Time { return viewTime }
	v, err := followup.BuildView(ctx, e.deps, opt)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func (e *env) bareSlice(t *testing.T, brief units.Unit, title string, position int) units.Unit {
	t.Helper()
	return e.create(t, units.NewUnit{Level: units.LevelSlice, ParentID: brief.ID, Title: title, Body: "b", Position: position})
}

func (e *env) doneTask(t *testing.T, s units.Unit) {
	t.Helper()
	task := e.create(t, units.NewUnit{Level: units.LevelTask, ParentID: s.ID, Title: "t", Body: "b", AcceptanceCriteria: "c"})
	for _, st := range []units.State{units.StateTodo, units.StateInProgress, units.StateDone} {
		var err error
		if task, err = e.units.SetState(ctx, task.ID, task.Fingerprint, st, ""); err != nil {
			t.Fatal(err)
		}
	}
}

func sliceByID(t *testing.T, v followup.View, id string) followup.SliceView {
	t.Helper()
	for _, b := range v.Briefs {
		for _, s := range b.Slices {
			if s.ID == id {
				return s
			}
		}
	}
	t.Fatalf("no slice %s in %+v", id, v)
	return followup.SliceView{}
}

func TestBuildViewWritesNothing(t *testing.T) {
	e := newEnv(t)
	s, pr := e.slice(t, "one", units.PullRequestOpen)
	e.provider.Merge(pr.Number)
	e.provider.AddFeedback(pr.Number, inline("c1"))
	e.branch(t, s)
	before := e.snapshot(t)
	v := view(t, e, followup.ViewOptions{Poll: true})
	if len(e.provider.Calls()) == 0 || len(v.Followups) == 0 {
		t.Fatalf("nothing polled: %+v", v)
	}
	if after := e.snapshot(t); after != before {
		t.Errorf("state changed:\n%s\n---\n%s", before, after)
	}
	if rec := e.record(t, s); rec.State != units.PullRequestOpen {
		t.Errorf("recorded state = %s", rec.State)
	}
	var locks []string
	_ = filepath.WalkDir(e.repo.Dir, func(p string, _ os.DirEntry, _ error) error {
		if strings.Contains(strings.ToLower(filepath.Base(p)), "lock") && !strings.Contains(p, ".git") {
			locks = append(locks, p)
		}
		return nil
	})
	if len(locks) > 0 {
		t.Errorf("lock files: %v", locks)
	}
}

func TestBuildViewSkipsFinishedUnlessAll(t *testing.T) {
	e := newEnv(t)
	open := e.bareSlice(t, e.brief, "open", 0)
	done := e.bareSlice(t, e.brief, "done", 0)
	e.doneTask(t, done)
	finishedBrief := e.create(t, units.NewUnit{Level: units.LevelBrief, Title: "Old", Body: "b"})
	oldSlice := e.bareSlice(t, finishedBrief, "old", 0)
	e.doneTask(t, oldSlice)

	v := view(t, e, followup.ViewOptions{})
	if len(v.Briefs) != 1 || len(v.Briefs[0].Slices) != 1 || v.Briefs[0].Slices[0].ID != open.ID {
		t.Errorf("default view = %+v", v.Briefs)
	}
	v = view(t, e, followup.ViewOptions{All: true})
	if len(v.Briefs) != 2 || len(v.Briefs[0].Slices) != 2 || len(v.Briefs[1].Slices) != 1 {
		t.Errorf("all view = %+v", v.Briefs)
	}
}

func TestBuildViewIDRestrictsAndRejectsTaskAndUnknown(t *testing.T) {
	e := newEnv(t)
	a := e.bareSlice(t, e.brief, "a", 0)
	b := e.bareSlice(t, e.brief, "b", 0)
	other := e.create(t, units.NewUnit{Level: units.LevelBrief, Title: "Other", Body: "b"})
	e.bareSlice(t, other, "c", 0)
	task := e.create(t, units.NewUnit{Level: units.LevelTask, ParentID: a.ID, Title: "t", Body: "b"})

	v := view(t, e, followup.ViewOptions{ID: e.brief.ID})
	if len(v.Briefs) != 1 || v.Briefs[0].ID != e.brief.ID || len(v.Briefs[0].Slices) != 2 {
		t.Errorf("brief view = %+v", v.Briefs)
	}
	v = view(t, e, followup.ViewOptions{ID: b.ID})
	if len(v.Briefs) != 1 || len(v.Briefs[0].Slices) != 1 || v.Briefs[0].Slices[0].ID != b.ID {
		t.Errorf("slice view = %+v", v.Briefs)
	}
	for _, id := range []string{task.ID, "S-99", "B-99"} {
		if _, err := followup.BuildView(ctx, e.deps, followup.ViewOptions{ID: id}); err == nil {
			t.Errorf("%s: no error", id)
		}
	}
}

func TestBuildViewSlicesInPositionOrder(t *testing.T) {
	e := newEnv(t)
	first := e.bareSlice(t, e.brief, "first", 0)
	second := e.bareSlice(t, e.brief, "second", 0)
	third := e.bareSlice(t, e.brief, "third", 1)
	_ = first
	v := view(t, e, followup.ViewOptions{})
	var got []string
	for _, s := range v.Briefs[0].Slices {
		got = append(got, s.ID)
	}
	if want := []string{third.ID, first.ID, second.ID}; !reflect.DeepEqual(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
}

func TestBuildViewWaitingOn(t *testing.T) {
	e := newEnv(t)
	a := e.bareSlice(t, e.brief, "a", 0)
	b := e.bareSlice(t, e.brief, "b", 0)
	if err := e.units.AddDependency(ctx, b.ID, a.ID); err != nil {
		t.Fatal(err)
	}
	v := view(t, e, followup.ViewOptions{})
	if got := sliceByID(t, v, b.ID).WaitingOn; !reflect.DeepEqual(got, []string{a.ID}) {
		t.Errorf("waiting on = %v", got)
	}
	if got := sliceByID(t, v, a.ID).WaitingOn; len(got) != 0 {
		t.Errorf("a waits on %v", got)
	}
}

func TestBuildViewPullRequestHostDiffersFromRecord(t *testing.T) {
	e := newEnv(t)
	s, pr := e.slice(t, "one", units.PullRequestOpen)
	e.provider.Merge(pr.Number)
	sv := sliceByID(t, view(t, e, followup.ViewOptions{Poll: true}), s.ID)
	want := followup.PullRequestView{Number: pr.Number, URL: pr.URL, Recorded: "open", Host: "merged"}
	if sv.PullRequest == nil || *sv.PullRequest != want {
		t.Errorf("pull request = %+v, want %+v", sv.PullRequest, want)
	}
	if len(sv.Followups) == 0 || sv.Followups[0].Kind != followup.KindRecord {
		t.Errorf("followups = %+v", sv.Followups)
	}
}

func TestBuildViewFeedbackCount(t *testing.T) {
	e := newEnv(t)
	s, pr := e.slice(t, "one", units.PullRequestOpen)
	e.provider.AddFeedback(pr.Number, inline("c1"))
	e.provider.AddFeedback(pr.Number, inline("c2"))
	if got := sliceByID(t, view(t, e, followup.ViewOptions{Poll: true}), s.ID).NewFeedback; got != 2 {
		t.Errorf("new feedback = %d", got)
	}
}

func TestBuildViewFollowupsPerSlice(t *testing.T) {
	e := newEnv(t)
	s1, pr1 := e.slice(t, "one", units.PullRequestOpen)
	s2, _ := e.slice(t, "two", units.PullRequestOpen)
	e.provider.Close(pr1.Number)
	v := view(t, e, followup.ViewOptions{Poll: true})
	if got := sliceByID(t, v, s1.ID).Followups; len(got) != 2 {
		t.Errorf("s1 followups = %+v", got)
	}
	if got := sliceByID(t, v, s2.ID).Followups; len(got) != 0 {
		t.Errorf("s2 followups = %+v", got)
	}
	if len(v.Followups) != 2 {
		t.Errorf("view followups = %+v", v.Followups)
	}
}

func TestBuildViewProviderUnavailableShowsRecorded(t *testing.T) {
	e := newEnv(t)
	s, _ := e.slice(t, "one", units.PullRequestOpen)
	e.deps.Provider = func(context.Context) (hosting.Provider, error) { return nil, errors.New("no token") }
	v := view(t, e, followup.ViewOptions{Poll: true})
	if v.Provider != "unavailable (no token)" {
		t.Errorf("provider = %q", v.Provider)
	}
	if sv := sliceByID(t, v, s.ID); sv.PullRequest == nil || sv.PullRequest.Recorded != "open" || sv.PullRequest.Host != "" || sv.Error != "" {
		t.Errorf("slice = %+v", sv)
	}
}

func TestBuildViewRateLimited(t *testing.T) {
	e := newEnv(t)
	e.slice(t, "one", units.PullRequestOpen)
	reset := time.Date(2026, 11, 17, 11, 0, 0, 0, time.UTC)
	e.provider.RateLimit(reset)
	if got := view(t, e, followup.ViewOptions{Poll: true}).Provider; got != "rate limited until 2026-11-17T11:00:00Z" {
		t.Errorf("provider = %q", got)
	}
	e.provider.RateLimit(time.Time{})
	if got := view(t, e, followup.ViewOptions{Poll: true}).Provider; got != "rate limited until later" {
		t.Errorf("provider = %q", got)
	}
}

func TestBuildViewNoPoll(t *testing.T) {
	e := newEnv(t)
	s, pr := e.slice(t, "one", units.PullRequestOpen)
	e.provider.Merge(pr.Number)
	before := len(e.provider.Calls())
	v := view(t, e, followup.ViewOptions{})
	if v.Provider != "not polled" || len(e.provider.Calls()) != before {
		t.Errorf("provider = %q, calls = %d (was %d)", v.Provider, len(e.provider.Calls()), before)
	}
	if sv := sliceByID(t, v, s.ID); sv.PullRequest.Host != "" || len(sv.Followups) != 0 {
		t.Errorf("slice = %+v", sv)
	}
}

func TestBuildViewSliceError(t *testing.T) {
	e := newEnv(t)
	bad, _ := e.slice(t, "bad", units.PullRequestOpen)
	if err := e.units.RecordPullRequest(ctx, units.PullRequestRecord{SliceID: bad.ID, Number: 99, URL: "https://x.test/99", Head: "h", Base: "main", State: units.PullRequestOpen}); err != nil {
		t.Fatal(err)
	}
	good, _ := e.slice(t, "good", units.PullRequestOpen)
	v := view(t, e, followup.ViewOptions{Poll: true})
	if sv := sliceByID(t, v, bad.ID); !strings.Contains(sv.Error, "gone from the host") || sv.PullRequest == nil || sv.PullRequest.Host != "" {
		t.Errorf("bad = %+v", sv)
	}
	if sv := sliceByID(t, v, good.ID); sv.Error != "" || sv.PullRequest.Host != "open" {
		t.Errorf("good = %+v", sv)
	}
}
