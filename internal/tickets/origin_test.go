package tickets

import (
	"strings"
	"testing"

	"github.com/bengearig/ralph/internal/taskmanager"
	"github.com/bengearig/ralph/internal/units"
)

// startedFrom makes a ticket in the fake and a task started from it, with the row an attach writes.
func (e *env) startedFrom(status string) (units.Unit, string) {
	e.t.Helper()
	e.deps.Settings.Types = map[string]string{"task": "Task"}
	_, _, tk := e.tree()
	e.setState(tk.ID, units.StateTodo)
	made, err := e.fake.Create(e.ctx, taskmanager.New{Type: "Task", Title: "Theirs", Body: "their body"})
	if err != nil {
		e.t.Fatal(err)
	}
	if err := e.svc.AddLink(e.ctx, tk.ID, units.LinkExternal, "jira:"+made.Key); err != nil {
		e.t.Fatal(err)
	}
	row := Row{UnitID: tk.ID, System: "jira", Key: made.Key, PushedStatus: status, Scheduled: true, LinkConfirmed: true, Origin: OriginIssue}
	if err := e.store.Put(e.ctx, row); err != nil {
		e.t.Fatal(err)
	}
	e.base = len(e.fake.Calls())
	return tk, made.Key
}

// seen counts the calls of a method made since the ticket was set up.
func (e *env) seen(method string) int {
	n := 0
	for _, c := range e.fake.Calls()[e.base:] {
		if c == method {
			n++
		}
	}
	return n
}

func TestSyncOriginNeverPushesTitleOrBody(t *testing.T) {
	e := newEnv(t)
	tk, _ := e.startedFrom("To Do")
	e.mustSync()
	u := e.unitOf(tk.ID)
	if _, err := e.svc.Update(e.ctx, tk.ID, u.Fingerprint, units.Change{Title: ptr("Changed"), Body: ptr("changed body")}); err != nil {
		t.Fatal(err)
	}
	e.mustSync()
	if n := e.seen("Update"); n != 0 {
		t.Fatalf("Update calls = %d", n)
	}
}

func TestSyncOriginMovesOnMappedStateChange(t *testing.T) {
	e := newEnv(t)
	tk, key := e.startedFrom("To Do")
	r := e.mustSync()
	if e.seen("Move") != 0 || len(r.Moved) != 0 {
		t.Fatalf("first pass moved: %q", e.fake.Calls())
	}
	e.setState(tk.ID, units.StateInProgress)
	r = e.mustSync()
	wantKeys(t, r.Moved, tk.ID+"="+key)
	if got := e.row(tk.ID).PushedStatus; got != "In Progress" {
		t.Fatalf("pushed status = %q", got)
	}
}

func TestSyncOriginNoFirstMoveBackwards(t *testing.T) {
	e := newEnv(t)
	tk, key := e.startedFrom("To Do")
	e.fake.ForceStatus(key, "In Progress")
	e.mustSync()
	if e.seen("Move") != 0 {
		t.Fatalf("calls = %q", e.fake.Calls())
	}
	if got := e.row(tk.ID).PushedStatus; got != "To Do" {
		t.Fatalf("pushed status = %q", got)
	}
}

func TestSyncOriginDoesNotCreateSecondTicket(t *testing.T) {
	e := newEnv(t)
	e.startedFrom("To Do")
	r := e.mustSync()
	if e.seen("Find") != 0 || e.seen("Create") != 0 || len(r.Created) != 0 {
		t.Fatalf("calls = %q", e.fake.Calls())
	}
}

func TestSyncOriginUnmappedLevelNeverSynced(t *testing.T) {
	e := newEnv(t)
	tk, _ := e.startedFrom("To Do")
	delete(e.deps.Settings.Types, "task")
	e.setState(tk.ID, units.StateInProgress)
	e.mustSync()
	if got := e.fake.Calls()[e.base:]; len(got) != 0 {
		t.Fatalf("calls = %q", got)
	}
}

func TestSyncOriginEmptyKeepsOldBehavior(t *testing.T) {
	e := newEnv(t)
	tk, key := e.startedFrom("To Do")
	row := e.row(tk.ID)
	row.Origin = ""
	row.PushedTitle, row.PushedBody = Hash("Theirs"), Hash("their body")
	if err := e.store.Put(e.ctx, row); err != nil {
		t.Fatal(err)
	}
	e.mustSync()
	if e.seen("Update") != 1 {
		t.Fatalf("calls = %q for %s", e.fake.Calls(), key)
	}
}

func TestPlanOriginIgnoresTitleBodyStatus(t *testing.T) {
	row := pushedRow()
	row.Origin = OriginIssue
	tk := same()
	tk.Title, tk.Body, tk.Status = "Edited", "Edited", "Done"
	if got := planOne(t, seen(row, tk)); len(got) != 0 {
		t.Fatalf("drifts = %+v", got)
	}
}

func TestPlanOriginReportsDeleted(t *testing.T) {
	row := pushedRow()
	row.Origin = OriginIssue
	wantFields(t, planOne(t, Observation{Unit: row.UnitID, Row: row}), "deleted")
}

func TestPlanOriginFollowsMovedKey(t *testing.T) {
	row := pushedRow()
	row.Origin = OriginIssue
	tk := same()
	tk.Key = "ABC-9"
	got := planOne(t, seen(row, tk))
	if len(got) != 1 || got[0].MovedTo != "ABC-9" || len(got[0].Fields) != 0 {
		t.Fatalf("drifts = %+v", got)
	}
}

func (e *env) deletedOrigin() (string, string) {
	e.t.Helper()
	tk, key := e.startedFrom("To Do")
	e.fake.Delete(key)
	if _, err := Check(e.ctx, e.deps, ObserveOptions{}); err != nil {
		e.t.Fatal(err)
	}
	if got := e.row(tk.ID).Drift; got != "deleted" {
		e.t.Fatalf("drift = %q", got)
	}
	return tk.ID, key
}

func TestPlanResolveOriginOnlyDeleted(t *testing.T) {
	e := newEnv(t)
	id, _ := e.deletedOrigin()
	_, err := PlanResolve(e.ctx, e.deps, id, ActionAccept, []string{"title"})
	if err == nil || !strings.Contains(err.Error(), `only "deleted"`) {
		t.Fatalf("err = %v", err)
	}
	if _, err := PlanResolve(e.ctx, e.deps, id, ActionAccept, []string{"deleted"}); err != nil {
		t.Fatal(err)
	}
}

func TestResolveOriginDeletedAcceptDetaches(t *testing.T) {
	e := newEnv(t)
	id, key := e.deletedOrigin()
	p, err := PlanResolve(e.ctx, e.deps, id, ActionAccept, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := Resolve(e.ctx, e.deps, p); err != nil {
		t.Fatal(err)
	}
	for _, l := range e.links(id) {
		if l == "external jira:"+key {
			t.Fatalf("link kept: %q", e.links(id))
		}
	}
	if got := e.row(id); got.Drift != "" || got.Origin != OriginIssue {
		t.Fatalf("row = %+v", got)
	}
	e.mustSync()
	if e.seen("Create") != 0 || e.seen("Find") != 0 {
		t.Fatalf("calls = %q", e.fake.Calls()[e.base:])
	}
}
