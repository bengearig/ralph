package tickets

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/bengearig/ralph/internal/db"
	"github.com/bengearig/ralph/internal/taskmanager"
	"github.com/bengearig/ralph/internal/units"
)

func TestSplitBodyWithCriteria(t *testing.T) {
	body, crit := SplitBody("intro\n\nmore\n\n## Acceptance criteria\n\n- a\n- b\n")
	if body != "intro\n\nmore" || crit != "- a\n- b" {
		t.Fatalf("body %q criteria %q", body, crit)
	}
}

func TestSplitBodyWithout(t *testing.T) {
	body, crit := SplitBody("\njust text\n\n")
	if body != "just text" || crit != "" {
		t.Fatalf("body %q criteria %q", body, crit)
	}
}

func TestSplitBodyLastHeadingWins(t *testing.T) {
	body, crit := SplitBody("a\n## Acceptance criteria\nb\n## Acceptance criteria\nc")
	if body != "a\n## Acceptance criteria\nb" || crit != "c" {
		t.Fatalf("body %q criteria %q", body, crit)
	}
}

func TestSplitBodyRoundTripWithBuild(t *testing.T) {
	e := newEnv(t)
	e.mapTasks()
	_, _, tk := e.tree()
	e.setState(tk.ID, units.StateTodo)
	u, err := e.svc.Get(e.ctx, tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	want, err := Build(e.ctx, e.deps, u, map[string]Row{"S-1": {Key: "ABC-2"}})
	if err != nil {
		t.Fatal(err)
	}
	body, crit := SplitBody(want.Body)
	if body != u.Body || crit != u.AcceptanceCriteria {
		t.Fatalf("body %q criteria %q, unit %q %q", body, crit, u.Body, u.AcceptanceCriteria)
	}
}

// drifted makes a synced slice whose ticket was edited in Jira and checked.
func (e *env) drifted(edit taskmanager.Fields) (unit, key string) {
	e.t.Helper()
	_, s, _ := e.tree()
	e.mustSync()
	row := e.row(s.ID)
	e.fake.Edit(row.Key, edit)
	if _, err := Check(e.ctx, e.deps, ObserveOptions{}); err != nil {
		e.t.Fatal(err)
	}
	return s.ID, row.Key
}

func (e *env) plan(unit, action string, fields ...string) ResolvePlan {
	e.t.Helper()
	p, err := PlanResolve(e.ctx, e.deps, unit, action, fields)
	if err != nil {
		e.t.Fatal(err)
	}
	return p
}

func (e *env) resolve(unit, action string, fields ...string) error {
	e.t.Helper()
	return Resolve(e.ctx, e.deps, e.plan(unit, action, fields...))
}

func (e *env) notes(id string) []string {
	e.t.Helper()
	ns, err := e.svc.Notes(e.ctx, id)
	if err != nil {
		e.t.Fatal(err)
	}
	var out []string
	for _, n := range ns {
		out = append(out, n.Body)
	}
	return out
}

func (e *env) unitOf(id string) units.Unit {
	e.t.Helper()
	u, err := e.svc.Get(e.ctx, id)
	if err != nil {
		e.t.Fatal(err)
	}
	return u
}

func TestPlanResolveWritesNothing(t *testing.T) {
	e := newEnv(t)
	id, key := e.drifted(taskmanager.Fields{Title: "Jira title", Body: "Jira body"})
	before, beforeUnit := e.row(id), e.unitOf(id)
	calls := len(e.fake.Calls())
	e.plan(id, ActionAccept)
	e.plan(id, ActionOverwrite)
	if got := e.row(id); got != before {
		t.Fatalf("row changed: %+v", got)
	}
	if got := e.unitOf(id); got != beforeUnit {
		t.Fatalf("unit changed")
	}
	if got := e.fake.Calls()[calls:]; !reflect.DeepEqual(got, []string{"Get", "Get"}) {
		t.Fatalf("calls = %q for %s", got, key)
	}
	if len(e.notes(id)) != 0 {
		t.Fatal("note added")
	}
}

func TestPlanResolveAcceptTitleBodyStatus(t *testing.T) {
	e := newEnv(t)
	id, key := e.drifted(taskmanager.Fields{Title: "Jira title", Body: "Jira body"})
	e.fake.ForceStatus(key, "Done")
	if _, err := Check(e.ctx, e.deps, ObserveOptions{}); err != nil {
		t.Fatal(err)
	}
	p := e.plan(id, ActionAccept)
	want := ResolvePlan{Unit: id, Key: key, Action: "accept", Changes: []Change{
		{Field: "title", From: "Slice", To: "Jira title"},
		{Field: "body", From: "body of Slice", To: "Jira body"},
		{Field: "status", From: "To Do", To: "Done"},
	}}
	if !reflect.DeepEqual(p, want) {
		t.Fatalf("plan = %+v\nwant %+v", p, want)
	}
}

func TestPlanResolveOverwriteReverses(t *testing.T) {
	e := newEnv(t)
	id, key := e.drifted(taskmanager.Fields{Title: "Jira title", Body: "body of Slice"})
	p := e.plan(id, ActionOverwrite)
	want := ResolvePlan{Unit: id, Key: key, Action: "overwrite", Changes: []Change{{Field: "title", From: "Jira title", To: "Slice"}}}
	if !reflect.DeepEqual(p, want) {
		t.Fatalf("plan = %+v\nwant %+v", p, want)
	}
}

func TestPlanResolveFieldSubset(t *testing.T) {
	e := newEnv(t)
	id, _ := e.drifted(taskmanager.Fields{Title: "Jira title", Body: "Jira body"})
	p := e.plan(id, ActionAccept, "body")
	if !reflect.DeepEqual(p.Fields(), []string{"body"}) {
		t.Fatalf("fields = %q", p.Fields())
	}
}

func TestPlanResolveFieldNotDriftedErrors(t *testing.T) {
	e := newEnv(t)
	id, _ := e.drifted(taskmanager.Fields{Title: "Jira title", Body: "body of Slice"})
	_, err := PlanResolve(e.ctx, e.deps, id, ActionAccept, []string{"body"})
	if err == nil || !strings.Contains(err.Error(), `"body"`) {
		t.Fatalf("err = %v", err)
	}
}

func TestPlanResolveUnknownFieldErrors(t *testing.T) {
	e := newEnv(t)
	id, _ := e.drifted(taskmanager.Fields{Title: "Jira title", Body: "body of Slice"})
	_, err := PlanResolve(e.ctx, e.deps, id, ActionAccept, []string{"colour"})
	if err == nil || !strings.Contains(err.Error(), `"colour"`) {
		t.Fatalf("err = %v", err)
	}
}

func TestPlanResolveNoDriftErrors(t *testing.T) {
	e := newEnv(t)
	_, s, _ := e.tree()
	e.mustSync()
	_, err := PlanResolve(e.ctx, e.deps, s.ID, ActionAccept, nil)
	if err == nil || err.Error() != "S-1 has no differences to resolve" {
		t.Fatalf("err = %v", err)
	}
}

func TestPlanResolveNoRowErrors(t *testing.T) {
	e := newEnv(t)
	_, s, _ := e.tree()
	if _, err := PlanResolve(e.ctx, e.deps, s.ID, ActionAccept, nil); err == nil || !strings.Contains(err.Error(), "has no ticket") {
		t.Fatalf("err = %v", err)
	}
}

func TestPlanResolveDetachedErrors(t *testing.T) {
	e := newEnv(t)
	id, key := e.drifted(taskmanager.Fields{Title: "Jira title", Body: "body of Slice"})
	if err := e.svc.RemoveLink(e.ctx, id, units.LinkExternal, "jira:"+key); err != nil {
		t.Fatal(err)
	}
	if _, err := PlanResolve(e.ctx, e.deps, id, ActionAccept, nil); err == nil || !strings.Contains(err.Error(), "detached") {
		t.Fatalf("err = %v", err)
	}
}

func TestResolveAcceptSetsTitleAndBodyThroughUnitsService(t *testing.T) {
	e := newEnv(t)
	e.mapTasks()
	_, _, tk := e.tree()
	e.setState(tk.ID, units.StateTodo)
	e.mustSync()
	row := e.row(tk.ID)
	e.fake.Edit(row.Key, taskmanager.Fields{Title: "From Jira", Body: "new text\n\n## Acceptance criteria\n\n- shipped"})
	if _, err := Check(e.ctx, e.deps, ObserveOptions{}); err != nil {
		t.Fatal(err)
	}
	ctx := units.WithActor(e.ctx, "Ann <ann@example.com>")
	p := e.plan(tk.ID, ActionAccept)
	if err := Resolve(ctx, e.deps, p); err != nil {
		t.Fatal(err)
	}
	u := e.unitOf(tk.ID)
	if u.Title != "From Jira" || u.Body != "new text" || u.AcceptanceCriteria != "- shipped" || u.UpdatedBy != "Ann <ann@example.com>" {
		t.Fatalf("unit = %+v", u)
	}
}

func TestResolveAcceptClearsCriteriaWhenTicketHasNone(t *testing.T) {
	e := newEnv(t)
	e.mapTasks()
	_, _, tk := e.tree()
	e.edit(tk.ID, units.Change{AcceptanceCriteria: ptr("old criteria")})
	e.mustSync()
	e.fake.Edit(e.row(tk.ID).Key, taskmanager.Fields{Title: "Task", Body: "plain"})
	if _, err := Check(e.ctx, e.deps, ObserveOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := e.resolve(tk.ID, ActionAccept); err != nil {
		t.Fatal(err)
	}
	if u := e.unitOf(tk.ID); u.Body != "plain" || u.AcceptanceCriteria != "" {
		t.Fatalf("unit = %+v", u)
	}
}

func TestResolveAcceptStaleFingerprintWritesNothing(t *testing.T) {
	e := newEnv(t)
	id, _ := e.drifted(taskmanager.Fields{Title: "Jira title", Body: "Jira body"})
	p := e.plan(id, ActionAccept)
	d := e.deps
	stale := staleUnits{Service: e.svc}
	d.Units = stale
	before := e.row(id)
	err := Resolve(e.ctx, d, p)
	if !errors.Is(err, db.ErrStale) {
		t.Fatalf("err = %v", err)
	}
	if e.row(id) != before || len(e.notes(id)) != 0 || e.unitOf(id).Title != "Slice" {
		t.Fatal("something was written")
	}
}

type staleUnits struct{ units.Service }

func (staleUnits) Update(context.Context, string, string, units.Change) (units.Unit, error) {
	return units.Unit{}, db.ErrStale
}

func TestResolveAcceptStatusOnlyAcknowledges(t *testing.T) {
	e := newEnv(t)
	e.direct()
	_, s, _ := e.tree()
	e.mustSync()
	key := e.row(s.ID).Key
	e.fake.ForceStatus(key, "Done")
	if _, err := Check(e.ctx, e.deps, ObserveOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := e.resolve(s.ID, ActionAccept); err != nil {
		t.Fatal(err)
	}
	row := e.row(s.ID)
	if row.PushedStatus != "Done" || row.Drift != "" || !row.DriftSeenAt.IsZero() || e.count("Move") != 0 {
		t.Fatalf("row %+v, moves %d", row, e.count("Move"))
	}
}

func TestResolveAcceptDeletedDetachesUnit(t *testing.T) {
	e := newEnv(t)
	_, s, _ := e.tree()
	e.mustSync()
	key := e.row(s.ID).Key
	e.fake.Delete(key)
	if _, err := Check(e.ctx, e.deps, ObserveOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := e.resolve(s.ID, ActionAccept); err != nil {
		t.Fatal(err)
	}
	if links := e.links(s.ID); len(links) != 0 {
		t.Fatalf("links = %q", links)
	}
	row := e.row(s.ID)
	if !row.LinkConfirmed || row.Drift != "" {
		t.Fatalf("row = %+v", row)
	}
	finds, creates := e.count("Find"), e.count("Create")
	e.mustSync()
	if e.count("Find") != finds || e.count("Create") != creates {
		t.Fatalf("calls = %q", e.fake.Calls())
	}
}

func TestResolveAcceptDeletedThenLinkAgainReattaches(t *testing.T) {
	e := newEnv(t)
	_, s, _ := e.tree()
	e.mustSync()
	key := e.row(s.ID).Key
	e.fake.Delete(key)
	if _, err := Check(e.ctx, e.deps, ObserveOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := e.resolve(s.ID, ActionAccept); err != nil {
		t.Fatal(err)
	}
	other, err := e.fake.Create(e.ctx, taskmanager.New{Type: "Task", Title: "Other"})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.AddLink(e.ctx, s.ID, units.LinkExternal, "jira:"+other.Key); err != nil {
		t.Fatal(err)
	}
	e.mustSync()
	if row := e.row(s.ID); row.Key != other.Key {
		t.Fatalf("row = %+v", row)
	}
}

func TestResolveAcceptUpdatesPushedHashesSoNoPushFollows(t *testing.T) {
	e := newEnv(t)
	id, _ := e.drifted(taskmanager.Fields{Title: "Jira title", Body: "Jira body  \n"})
	if err := e.resolve(id, ActionAccept); err != nil {
		t.Fatal(err)
	}
	e.mustSync()
	if e.count("Update") != 0 {
		t.Fatalf("calls = %q", e.fake.Calls())
	}
}

func TestResolveAcceptRemovesOnlyChosenFields(t *testing.T) {
	e := newEnv(t)
	id, _ := e.drifted(taskmanager.Fields{Title: "Jira title", Body: "Jira body"})
	if err := e.resolve(id, ActionAccept, "title"); err != nil {
		t.Fatal(err)
	}
	row := e.row(id)
	if row.Drift != "body" || row.DriftSeenAt.IsZero() {
		t.Fatalf("row = %+v", row)
	}
	if u := e.unitOf(id); u.Title != "Jira title" || u.Body != "body of Slice" {
		t.Fatalf("unit = %+v", u)
	}
}

func TestResolveAcceptAddsNote(t *testing.T) {
	e := newEnv(t)
	id, key := e.drifted(taskmanager.Fields{Title: "Jira title", Body: "Jira body"})
	if err := e.resolve(id, ActionAccept); err != nil {
		t.Fatal(err)
	}
	want := []string{"Accepted from " + key + " in Jira: title, body"}
	if got := e.notes(id); !reflect.DeepEqual(got, want) {
		t.Fatalf("notes = %q", got)
	}
}

func TestResolveOverwriteClearsHashesAndPushes(t *testing.T) {
	e := newEnv(t)
	id, key := e.drifted(taskmanager.Fields{Title: "Jira title", Body: "Jira body"})
	if err := e.resolve(id, ActionOverwrite); err != nil {
		t.Fatal(err)
	}
	if got := e.ticket(key); got.Title != "Slice" || got.Body != "body of Slice" {
		t.Fatalf("ticket = %+v", got)
	}
	if row := e.row(id); row.Drift != "" || row.PushedTitle != Hash("Slice") {
		t.Fatalf("row = %+v", row)
	}
}

func TestResolveOverwriteStatusMovesTicket(t *testing.T) {
	e := newEnv(t)
	e.direct()
	_, s, _ := e.tree()
	e.mustSync()
	key := e.row(s.ID).Key
	e.fake.ForceStatus(key, "Done")
	if _, err := Check(e.ctx, e.deps, ObserveOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := e.resolve(s.ID, ActionOverwrite); err != nil {
		t.Fatal(err)
	}
	if got := e.ticket(key).Status; got != "To Do" {
		t.Fatalf("status = %q", got)
	}
}

func TestResolveOverwriteDeletedCreatesNewTicket(t *testing.T) {
	e := newEnv(t)
	_, s, _ := e.tree()
	e.mustSync()
	old := e.row(s.ID).Key
	e.fake.Delete(old)
	if _, err := Check(e.ctx, e.deps, ObserveOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := e.resolve(s.ID, ActionOverwrite); err != nil {
		t.Fatal(err)
	}
	row := e.row(s.ID)
	if row.Key == old || row.Drift != "" || !row.LinkConfirmed {
		t.Fatalf("row = %+v", row)
	}
	if links := e.links(s.ID); !reflect.DeepEqual(links, []string{"external jira:" + row.Key}) {
		t.Fatalf("links = %q", links)
	}
}

func TestResolveOverwriteSyncFailureKeepsClearedState(t *testing.T) {
	e := newEnv(t)
	id, key := e.drifted(taskmanager.Fields{Title: "Jira title", Body: "body of Slice"})
	e.sys.updateFails[key] = taskmanager.ErrUnauthorized
	err := e.resolve(id, ActionOverwrite)
	if !errors.Is(err, taskmanager.ErrUnauthorized) {
		t.Fatalf("err = %v", err)
	}
	row := e.row(id)
	if row.Drift != "" || row.PushedTitle != "" {
		t.Fatalf("row = %+v", row)
	}
}

func TestResolveOverwriteAddsNote(t *testing.T) {
	e := newEnv(t)
	id, key := e.drifted(taskmanager.Fields{Title: "Jira title", Body: "Jira body"})
	if err := e.resolve(id, ActionOverwrite); err != nil {
		t.Fatal(err)
	}
	want := []string{"Overwrote Jira's title, body on " + key + " with ralph's values"}
	if got := e.notes(id); !reflect.DeepEqual(got, want) {
		t.Fatalf("notes = %q", got)
	}
}

func (e *env) driftedStatus() (unit, key string) {
	e.t.Helper()
	e.direct()
	_, s, _ := e.tree()
	e.mustSync()
	key = e.row(s.ID).Key
	e.fake.ForceStatus(key, "Done")
	if _, err := Check(e.ctx, e.deps, ObserveOptions{}); err != nil {
		e.t.Fatal(err)
	}
	return s.ID, key
}

func TestResolveAcceptStatusSetsPushedAndMapped(t *testing.T) {
	e := newEnv(t)
	id, _ := e.driftedStatus()
	if err := e.resolve(id, ActionAccept); err != nil {
		t.Fatal(err)
	}
	if row := e.row(id); row.PushedStatus != "Done" || row.MappedStatus != "To Do" {
		t.Fatalf("row = %+v", row)
	}
}

func TestResolveAcceptStatusUnmappedStateLeavesMappedEmpty(t *testing.T) {
	e := newEnv(t)
	e.mapTasks()
	_, _, tk := e.tree()
	e.direct()
	e.mustSync()
	e.setState(tk.ID, units.StateTodo, units.StateBlocked)
	key := e.row(tk.ID).Key
	e.fake.ForceStatus(key, "Done")
	if _, err := Check(e.ctx, e.deps, ObserveOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := e.resolve(tk.ID, ActionAccept, "status"); err != nil {
		t.Fatal(err)
	}
	if row := e.row(tk.ID); row.PushedStatus != "Done" || row.MappedStatus != "" {
		t.Fatalf("row = %+v", row)
	}
}

func TestResolveOverwriteStatusClearsBoth(t *testing.T) {
	e := newEnv(t)
	id, _ := e.driftedStatus()
	row := e.row(id)
	row.MappedStatus = "To Do"
	if err := e.store.Put(e.ctx, row); err != nil {
		t.Fatal(err)
	}
	e.sys.moveFails[row.Key] = errors.New("down")
	_ = e.resolve(id, ActionOverwrite)
	if row := e.row(id); row.PushedStatus != "" || row.MappedStatus != "" {
		t.Fatalf("row = %+v", row)
	}
}

func TestResolveAcceptTitleLeavesStatusColumnsAlone(t *testing.T) {
	e := newEnv(t)
	id, key := e.driftedStatus()
	e.fake.Edit(key, taskmanager.Fields{Title: "Renamed", Body: "body of Slice"})
	if _, err := Check(e.ctx, e.deps, ObserveOptions{}); err != nil {
		t.Fatal(err)
	}
	before := e.row(id)
	if err := e.resolve(id, ActionAccept, "title"); err != nil {
		t.Fatal(err)
	}
	row := e.row(id)
	if row.PushedStatus != before.PushedStatus || row.MappedStatus != before.MappedStatus {
		t.Fatalf("before %+v, after %+v", before, row)
	}
}
