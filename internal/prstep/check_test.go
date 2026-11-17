package prstep_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/bengearig/ralph/internal/hosting"
	"github.com/bengearig/ralph/internal/prstep"
	"github.com/bengearig/ralph/internal/units"
)

func (e *env) records(t *testing.T) []units.PullRequestRecord {
	t.Helper()
	recs, err := e.units.PullRequests(ctx, e.slice.ID)
	if err != nil {
		t.Fatal(err)
	}
	return recs
}

func (e *env) noteBodies(t *testing.T) []string {
	t.Helper()
	notes, err := e.units.Notes(ctx, e.slice.ID)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, n := range notes {
		out = append(out, n.Body)
	}
	return out
}

func (e *env) statuses(t *testing.T) map[string]units.Status {
	t.Helper()
	ids := []string{e.slice.ID}
	for _, tk := range e.tasks {
		ids = append(ids, tk.ID)
	}
	got, err := e.units.Statuses(ctx, ids)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestApplyOpenToClosedNotesAndRecords(t *testing.T) {
	e := newEnv(t)
	pr := e.mustRun(t).PullRequest
	rec := e.records(t)[0]
	got, err := prstep.Apply(ctx, e.units, e.slice, rec, hosting.PullRequest{Number: pr.Number, URL: pr.URL, State: hosting.StateClosed})
	if err != nil {
		t.Fatal(err)
	}
	want := "Pull request " + pr.URL + " was closed without merging."
	if got.Kind != prstep.ChangeClosed || got.Message != want {
		t.Errorf("change = %+v", got)
	}
	if e.records(t)[0].State != units.PullRequestClosed {
		t.Errorf("record = %+v", e.records(t)[0])
	}
	notes := e.noteBodies(t)
	if notes[len(notes)-1] != want {
		t.Errorf("notes = %q", notes)
	}
	if e.statuses(t)[e.slice.ID] != units.StatusNeedsAttention {
		t.Errorf("status = %s", e.statuses(t)[e.slice.ID])
	}
}

func TestApplyMergedNotesOnly(t *testing.T) {
	e := newEnv(t)
	pr := e.mustRun(t).PullRequest
	before := e.statuses(t)
	rec := e.records(t)[0]
	got, err := prstep.Apply(ctx, e.units, e.slice, rec, hosting.PullRequest{Number: pr.Number, URL: pr.URL, State: hosting.StateMerged})
	if err != nil {
		t.Fatal(err)
	}
	want := "Pull request " + pr.URL + " was merged."
	if got.Kind != prstep.ChangeMerged || got.Message != want {
		t.Errorf("change = %+v", got)
	}
	if e.records(t)[0].State != units.PullRequestMerged {
		t.Errorf("record = %+v", e.records(t)[0])
	}
	after := e.statuses(t)
	for id, st := range before {
		if after[id] != st {
			t.Errorf("%s changed from %s to %s", id, st, after[id])
		}
	}
	for _, tk := range e.tasks {
		u, _ := e.units.Get(ctx, tk.ID)
		if u.State != units.StateDone {
			t.Errorf("%s is %s", tk.ID, u.State)
		}
	}
	notes := e.noteBodies(t)
	if notes[len(notes)-1] != want {
		t.Errorf("notes = %q", notes)
	}
}

func TestApplyClosedToOpen(t *testing.T) {
	e := newEnv(t)
	pr := e.mustRun(t).PullRequest
	if err := e.units.SetPullRequestState(ctx, e.slice.ID, pr.Number, units.PullRequestClosed); err != nil {
		t.Fatal(err)
	}
	rec := e.records(t)[0]
	got, err := prstep.Apply(ctx, e.units, e.slice, rec, hosting.PullRequest{Number: pr.Number, URL: pr.URL, State: hosting.StateOpen})
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != prstep.ChangeReopenedOnHost || !strings.Contains(got.Message, "reopened") || !strings.Contains(got.Message, pr.URL) {
		t.Errorf("change = %+v", got)
	}
	if e.records(t)[0].State != units.PullRequestOpen {
		t.Errorf("record = %+v", e.records(t)[0])
	}
	if e.statuses(t)[e.slice.ID] != units.StatusDone {
		t.Errorf("status = %s", e.statuses(t)[e.slice.ID])
	}
}

func TestApplyUnchangedWritesNothing(t *testing.T) {
	e := newEnv(t)
	pr := e.mustRun(t).PullRequest
	notes := e.noteBodies(t)
	rec := e.records(t)[0]
	got, err := prstep.Apply(ctx, e.units, e.slice, rec, hosting.PullRequest{Number: pr.Number, URL: pr.URL, State: hosting.StateOpen})
	if err != nil || got.Kind != prstep.ChangeUnchanged || got.Message != "" {
		t.Fatalf("change = %+v, %v", got, err)
	}
	if after := e.records(t)[0]; !after.UpdatedAt.Equal(rec.UpdatedAt) {
		t.Errorf("record touched: %+v", after)
	}
	if len(e.noteBodies(t)) != len(notes) {
		t.Errorf("notes = %q", e.noteBodies(t))
	}
}

func TestDecisionError(t *testing.T) {
	e := &prstep.NeedsDecisionError{Slice: "S-2", Number: 5, URL: "https://x.test/5"}
	want := "S-2 needs a decision: pull request #5 (https://x.test/5) was closed without merging. Choose one: ralph pr reopen S-2, ralph pr rearticulate S-2 or ralph pr cancel S-2"
	if e.Error() != want {
		t.Errorf("got %q", e.Error())
	}
}

func TestDecisionNilWhenOpenMergedOrResolved(t *testing.T) {
	e := newEnv(t)
	if err := prstep.Decision(ctx, e.units, e.slice.ID); err != nil {
		t.Errorf("no record: %v", err)
	}
	pr := e.mustRun(t).PullRequest
	if err := prstep.Decision(ctx, e.units, e.slice.ID); err != nil {
		t.Errorf("open: %v", err)
	}
	if err := e.units.SetPullRequestState(ctx, e.slice.ID, pr.Number, units.PullRequestClosed); err != nil {
		t.Fatal(err)
	}
	var nd *prstep.NeedsDecisionError
	if err := prstep.Decision(ctx, e.units, e.slice.ID); !errors.As(err, &nd) || nd.Number != pr.Number || nd.URL != pr.URL || nd.Slice != e.slice.ID {
		t.Errorf("closed: %v", err)
	}
	if err := e.units.SetPullRequestState(ctx, e.slice.ID, pr.Number, units.PullRequestMerged); err != nil {
		t.Fatal(err)
	}
	if err := prstep.Decision(ctx, e.units, e.slice.ID); err != nil {
		t.Errorf("merged: %v", err)
	}
}

func TestCreateRefusesStoppedSlice(t *testing.T) {
	e := newEnv(t)
	pr := e.mustRun(t).PullRequest
	if err := e.units.SetPullRequestState(ctx, e.slice.ID, pr.Number, units.PullRequestClosed); err != nil {
		t.Fatal(err)
	}
	e.provider.Close(pr.Number)
	calls := len(e.provider.Calls())
	head := e.originHead(t)
	_, err := e.run(t)
	var nd *prstep.NeedsDecisionError
	if !errors.As(err, &nd) {
		t.Fatalf("err = %v", err)
	}
	if len(e.provider.Calls()) != calls || e.originHead(t) != head {
		t.Error("the provider or the remote was touched")
	}
}
