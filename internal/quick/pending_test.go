package quick

import (
	"context"
	"testing"

	"github.com/bengearig/ralph/internal/db"
	"github.com/bengearig/ralph/internal/testutil"
	"github.com/bengearig/ralph/internal/units"
)

func pendingSvc(t *testing.T) units.Service {
	t.Helper()
	return units.NewService(testutil.NewDB(t, db.Migrations))
}

func pendingTask(t *testing.T, svc units.Service, parent, title string, state units.State, link string) string {
	t.Helper()
	ctx := units.WithActor(context.Background(), "Ann <ann@example.com>")
	u, err := svc.Create(ctx, units.NewUnit{Level: units.LevelTask, ParentID: parent, Title: title, Body: "b", AcceptanceCriteria: "c"})
	if err != nil {
		t.Fatal(err)
	}
	if link != "" {
		if err := svc.AddLink(ctx, u.ID, units.LinkExternal, link); err != nil {
			t.Fatal(err)
		}
	}
	steps := map[units.State][]units.State{
		units.StateProposed:   nil,
		units.StateTodo:       {units.StateTodo},
		units.StateInProgress: {units.StateTodo, units.StateInProgress},
		units.StateDone:       {units.StateTodo, units.StateInProgress, units.StateDone},
		units.StateBlocked:    {units.StateTodo, units.StateInProgress, units.StateBlocked},
		units.StateCancelled:  {units.StateCancelled},
	}[state]
	for _, st := range steps {
		reason := ""
		if st == units.StateBlocked {
			reason = "stuck"
		}
		if u, err = svc.SetState(ctx, u.ID, u.Fingerprint, st, reason); err != nil {
			t.Fatal(err)
		}
	}
	return u.ID
}

func TestPendingListsInProgressOrphanWithBranch(t *testing.T) {
	svc := pendingSvc(t)
	id := pendingTask(t, svc, "", "fix it", units.StateInProgress, "branch:ralph/t-1-fix-it")
	got, err := Pending(context.Background(), svc)
	if err != nil {
		t.Fatal(err)
	}
	want := PendingTask{ID: id, Title: "fix it", Branch: "ralph/t-1-fix-it"}
	if len(got) != 1 || got[0] != want {
		t.Errorf("got %+v, want [%+v]", got, want)
	}
}

func TestPendingSkipsTaskWithoutBranchLink(t *testing.T) {
	svc := pendingSvc(t)
	pendingTask(t, svc, "", "none", units.StateInProgress, "")
	pendingTask(t, svc, "", "other link", units.StateInProgress, "pr:https://example.com/1")
	pendingTask(t, svc, "", "empty branch", units.StateInProgress, "branch:")
	got, err := Pending(context.Background(), svc)
	if err != nil || len(got) != 0 {
		t.Errorf("got %+v, %v", got, err)
	}
}

func TestPendingSkipsTaskWithParent(t *testing.T) {
	svc := pendingSvc(t)
	ctx := context.Background()
	b, err := svc.Create(units.WithActor(ctx, "a"), units.NewUnit{Level: units.LevelBrief, Title: "b", Body: "b"})
	if err != nil {
		t.Fatal(err)
	}
	s, err := svc.Create(units.WithActor(ctx, "a"), units.NewUnit{Level: units.LevelSlice, ParentID: b.ID, Title: "s", Body: "b"})
	if err != nil {
		t.Fatal(err)
	}
	pendingTask(t, svc, s.ID, "child", units.StateInProgress, "branch:x")
	got, err := Pending(ctx, svc)
	if err != nil || len(got) != 0 {
		t.Errorf("got %+v, %v", got, err)
	}
}

func TestPendingSkipsOtherStates(t *testing.T) {
	svc := pendingSvc(t)
	for _, st := range []units.State{units.StateTodo, units.StateDone, units.StateBlocked, units.StateCancelled} {
		pendingTask(t, svc, "", string(st), st, "branch:b-"+string(st))
	}
	got, err := Pending(context.Background(), svc)
	if err != nil || len(got) != 0 {
		t.Errorf("got %+v, %v", got, err)
	}
}

func TestPendingOrderedByID(t *testing.T) {
	svc := pendingSvc(t)
	var want []string
	for _, title := range []string{"a", "b", "c"} {
		want = append(want, pendingTask(t, svc, "", title, units.StateInProgress, "branch:"+title))
	}
	got, err := Pending(context.Background(), svc)
	if err != nil || len(got) != 3 {
		t.Fatalf("got %+v, %v", got, err)
	}
	for i, p := range got {
		if p.ID != want[i] {
			t.Errorf("position %d = %s, want %s", i, p.ID, want[i])
		}
	}
}

func TestPendingEmpty(t *testing.T) {
	got, err := Pending(context.Background(), pendingSvc(t))
	if err != nil || got == nil || len(got) != 0 {
		t.Errorf("got %#v, %v", got, err)
	}
}

func TestPendingMessagesText(t *testing.T) {
	svc := pendingSvc(t)
	id := pendingTask(t, svc, "", "fix it", units.StateInProgress, "branch:ralph/x")
	got, err := PendingMessages(context.Background(), svc)
	if err != nil {
		t.Fatal(err)
	}
	want := id + ` "fix it" was started and not finalized (branch ralph/x). Ask the agent to run quick_finish, or cancel the task if the work was dropped.`
	if len(got) != 1 || got[0] != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
