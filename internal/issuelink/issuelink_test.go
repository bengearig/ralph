package issuelink

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/bengearig/ralph/internal/db"
	"github.com/bengearig/ralph/internal/issuesource"
	"github.com/bengearig/ralph/internal/taskmanager"
	"github.com/bengearig/ralph/internal/taskmanager/taskmanagertest"
	"github.com/bengearig/ralph/internal/testutil"
	"github.com/bengearig/ralph/internal/tickets"
	"github.com/bengearig/ralph/internal/units"
)

type env struct {
	t    *testing.T
	ctx  context.Context
	svc  units.Service
	deps tickets.Deps
}

func newEnv(t *testing.T) *env {
	t.Helper()
	d := testutil.NewDB(t, db.Migrations)
	svc := units.NewService(d)
	return &env{t: t, ctx: context.Background(), svc: svc, deps: tickets.Deps{
		Units: svc, DB: d, System: taskmanagertest.NewFake("ABC"),
		Settings: taskmanager.Settings{Provider: "jira", Types: map[string]string{"brief": "Feature", "slice": "Task", "task": "Subtask"}},
	}}
}

func (e *env) unit(level units.Level, parent string) units.Unit {
	e.t.Helper()
	u, err := e.svc.Create(e.ctx, units.NewUnit{Level: level, ParentID: parent, Title: "Unit", Body: "body"})
	if err != nil {
		e.t.Fatal(err)
	}
	return u
}

func (e *env) tree() (b, s, tk units.Unit) {
	b = e.unit(units.LevelBrief, "")
	s = e.unit(units.LevelSlice, b.ID)
	return b, s, e.unit(units.LevelTask, s.ID)
}

func (e *env) link(id, target string) {
	e.t.Helper()
	if err := e.svc.AddLink(e.ctx, id, units.LinkExternal, target); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) links(id string) []string {
	e.t.Helper()
	links, err := e.svc.Links(e.ctx, id)
	if err != nil {
		e.t.Fatal(err)
	}
	var out []string
	for _, l := range links {
		out = append(out, l.Target)
	}
	return out
}

func (e *env) of(id string) (issuesource.Ref, bool) {
	e.t.Helper()
	ref, ok, err := Of(e.ctx, e.svc, id)
	if err != nil {
		e.t.Fatal(err)
	}
	return ref, ok
}

func (e *env) rows() []tickets.Row {
	e.t.Helper()
	rows, err := tickets.NewStore(e.deps.DB).List(e.ctx)
	if err != nil {
		e.t.Fatal(err)
	}
	return rows
}

func issue(key string) issuesource.Issue {
	return issuesource.Issue{Ref: issuesource.Ref{System: "jira", Key: key}, Title: "Theirs"}
}

func TestTargetAndParseTargetRoundTrip(t *testing.T) {
	ref := issuesource.Ref{System: "jira", Key: "ABC-123"}
	if got := Target(ref); got != "issue:jira:ABC-123" {
		t.Fatalf("target = %q", got)
	}
	if back, ok := ParseTarget(Target(ref)); !ok || back != ref {
		t.Fatalf("parsed = %+v, %v", back, ok)
	}
}

func TestParseTargetRejectsOthers(t *testing.T) {
	for _, in := range []string{"jira:ABC-1", "branch:x", "issue:jira:", "issue:jira:abc-1", "issue:ABC-1", "issue:jira:ABC-0", ""} {
		if ref, ok := ParseTarget(in); ok {
			t.Errorf("ParseTarget(%q) = %+v", in, ref)
		}
	}
}

func TestOfOwnLink(t *testing.T) {
	e := newEnv(t)
	b := e.unit(units.LevelBrief, "")
	e.link(b.ID, "issue:jira:ABC-1")
	if ref, ok := e.of(b.ID); !ok || ref.Key != "ABC-1" || ref.System != "jira" {
		t.Fatalf("ref = %+v, %v", ref, ok)
	}
}

func TestOfParentWhenNoOwn(t *testing.T) {
	e := newEnv(t)
	b, s, _ := e.tree()
	e.link(b.ID, "issue:jira:ABC-1")
	if ref, ok := e.of(s.ID); !ok || ref.Key != "ABC-1" {
		t.Fatalf("ref = %+v, %v", ref, ok)
	}
}

func TestOfGrandparentForTask(t *testing.T) {
	e := newEnv(t)
	b, _, tk := e.tree()
	e.link(b.ID, "issue:jira:ABC-1")
	if ref, ok := e.of(tk.ID); !ok || ref.Key != "ABC-1" {
		t.Fatalf("ref = %+v, %v", ref, ok)
	}
}

func TestOfNearestWins(t *testing.T) {
	e := newEnv(t)
	b, s, tk := e.tree()
	e.link(b.ID, "issue:jira:ABC-1")
	e.link(s.ID, "issue:jira:ABC-2")
	for _, id := range []string{s.ID, tk.ID} {
		if ref, ok := e.of(id); !ok || ref.Key != "ABC-2" {
			t.Fatalf("%s: ref = %+v, %v", id, ref, ok)
		}
	}
}

func TestOfNoneIsFalse(t *testing.T) {
	e := newEnv(t)
	_, _, tk := e.tree()
	if ref, ok := e.of(tk.ID); ok {
		t.Fatalf("ref = %+v", ref)
	}
}

func TestOfIgnoresMirrorLinks(t *testing.T) {
	e := newEnv(t)
	b := e.unit(units.LevelBrief, "")
	e.link(b.ID, "jira:ABC-1")
	if ref, ok := e.of(b.ID); ok {
		t.Fatalf("ref = %+v", ref)
	}
}

func TestOfIgnoresBadTargets(t *testing.T) {
	e := newEnv(t)
	b, s, _ := e.tree()
	e.link(b.ID, "issue:jira:ABC-1")
	e.link(s.ID, "issue:jira:abc-2")
	if ref, ok := e.of(s.ID); !ok || ref.Key != "ABC-1" {
		t.Fatalf("ref = %+v, %v", ref, ok)
	}
}

func TestCheckWritesNothing(t *testing.T) {
	e := newEnv(t)
	b := e.unit(units.LevelBrief, "")
	if err := Check(e.ctx, e.deps, b.ID, issue("ABC-1").Ref); err != nil {
		t.Fatal(err)
	}
	if err := Check(e.ctx, e.deps, "", issue("ABC-1").Ref); err != nil {
		t.Fatal(err)
	}
	if len(e.links(b.ID)) != 0 || len(e.rows()) != 0 {
		t.Fatalf("links %q, rows %+v", e.links(b.ID), e.rows())
	}
}

func TestCheckAlreadyLinkedToOtherUnit(t *testing.T) {
	e := newEnv(t)
	b := e.unit(units.LevelBrief, "")
	if err := Attach(e.ctx, e.deps, b.ID, issue("ABC-1")); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"", e.unit(units.LevelBrief, "").ID} {
		err := Check(e.ctx, e.deps, id, issue("ABC-1").Ref)
		if !errors.Is(err, ErrAlreadyLinked) || err.Error() != "ABC-1 is already linked to B-1" {
			t.Fatalf("err = %v", err)
		}
	}
}

func TestCheckUnitAlreadyStartedFromTicket(t *testing.T) {
	e := newEnv(t)
	b := e.unit(units.LevelBrief, "")
	e.link(b.ID, "issue:jira:ABC-1")
	err := Check(e.ctx, e.deps, b.ID, issue("ABC-2").Ref)
	if err == nil || err.Error() != "B-1 was already started from ABC-1" {
		t.Fatalf("err = %v", err)
	}
}

func TestAttachWritesBothLinksAndRow(t *testing.T) {
	e := newEnv(t)
	_, _, tk := e.tree()
	if err := Attach(e.ctx, e.deps, tk.ID, issue("ABC-7")); err != nil {
		t.Fatal(err)
	}
	if got := e.links(tk.ID); !slices.Equal(got, []string{"issue:jira:ABC-7", "jira:ABC-7"}) {
		t.Fatalf("links = %q", got)
	}
	rows := e.rows()
	if len(rows) != 1 {
		t.Fatalf("rows = %+v", rows)
	}
	want := tickets.Row{UnitID: tk.ID, System: "jira", Key: "ABC-7", PushedStatus: "To Do", Scheduled: true, LinkConfirmed: true, Origin: "issue"}
	if rows[0] != want {
		t.Fatalf("row = %+v, want %+v", rows[0], want)
	}
}

func TestAttachStatusBaselineFromMappedState(t *testing.T) {
	e := newEnv(t)
	_, s, _ := e.tree()
	todo, blocked := e.unit(units.LevelTask, s.ID), e.unit(units.LevelTask, s.ID)
	for _, tk := range []units.Unit{todo, blocked} {
		u, err := e.svc.Update(e.ctx, tk.ID, tk.Fingerprint, units.Change{AcceptanceCriteria: ptr("it works")})
		if err != nil {
			t.Fatal(err)
		}
		if u, err = e.svc.SetState(e.ctx, tk.ID, u.Fingerprint, units.StateTodo, "ready"); err != nil {
			t.Fatal(err)
		}
		if tk.ID == blocked.ID {
			if _, err = e.svc.SetState(e.ctx, tk.ID, u.Fingerprint, units.StateBlocked, "stuck"); err != nil {
				t.Fatal(err)
			}
		}
	}
	for i, tk := range []units.Unit{todo, blocked} {
		if err := Attach(e.ctx, e.deps, tk.ID, issue([]string{"ABC-1", "ABC-2"}[i])); err != nil {
			t.Fatal(err)
		}
	}
	got := map[string]string{}
	for _, r := range e.rows() {
		got[r.UnitID] = r.PushedStatus
	}
	if got[todo.ID] != "To Do" || got[blocked.ID] != "" {
		t.Fatalf("statuses = %q", got)
	}
}

func TestAttachRefusesSecondUnitForSameTicket(t *testing.T) {
	e := newEnv(t)
	a, b := e.unit(units.LevelBrief, ""), e.unit(units.LevelBrief, "")
	if err := Attach(e.ctx, e.deps, a.ID, issue("ABC-1")); err != nil {
		t.Fatal(err)
	}
	if err := Attach(e.ctx, e.deps, b.ID, issue("ABC-1")); !errors.Is(err, ErrAlreadyLinked) {
		t.Fatalf("err = %v", err)
	}
	if len(e.links(b.ID)) != 0 || len(e.rows()) != 1 {
		t.Fatalf("links %q, rows %d", e.links(b.ID), len(e.rows()))
	}
}

func TestAttachRepairsAfterPartialFailure(t *testing.T) {
	e := newEnv(t)
	b := e.unit(units.LevelBrief, "")
	e.link(b.ID, "issue:jira:ABC-1")
	if err := Attach(e.ctx, e.deps, b.ID, issue("ABC-1")); err != nil {
		t.Fatal(err)
	}
	if got := e.links(b.ID); !slices.Equal(got, []string{"issue:jira:ABC-1", "jira:ABC-1"}) || len(e.rows()) != 1 {
		t.Fatalf("links %q, rows %d", got, len(e.rows()))
	}
	if err := Attach(e.ctx, e.deps, b.ID, issue("ABC-2")); err == nil {
		t.Fatal("a different ticket was accepted")
	}
}

func TestAttachUnknownUnit(t *testing.T) {
	e := newEnv(t)
	err := Attach(e.ctx, e.deps, "B-9", issue("ABC-1"))
	if !errors.Is(err, units.ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
	if len(e.rows()) != 0 {
		t.Fatal("row written")
	}
}

func TestAttachOnSliceAndBriefAndTask(t *testing.T) {
	e := newEnv(t)
	b, s, tk := e.tree()
	for i, id := range []string{b.ID, s.ID, tk.ID} {
		key := []string{"ABC-1", "ABC-2", "ABC-3"}[i]
		if err := Attach(e.ctx, e.deps, id, issue(key)); err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		if ref, ok := e.of(id); !ok || ref.Key != key {
			t.Fatalf("%s: ref = %+v, %v", id, ref, ok)
		}
	}
	if len(e.rows()) != 3 {
		t.Fatalf("rows = %+v", e.rows())
	}
}

func ptr(s string) *string { return &s }
