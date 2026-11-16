package units_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/bengearig/ralph/internal/db"
	"github.com/bengearig/ralph/internal/testutil"
	"github.com/bengearig/ralph/internal/units"
)

var _ units.Service = (*units.Store)(nil)

func newService(t *testing.T) units.Service {
	t.Helper()
	return units.NewService(testutil.NewDB(t, db.Migrations))
}

func exportedMethods(typ reflect.Type) []string {
	var names []string
	for i := range typ.NumMethod() {
		names = append(names, typ.Method(i).Name)
	}
	slices.Sort(names)
	return names
}

func TestServiceInterfaceCovered(t *testing.T) {
	iface := exportedMethods(reflect.TypeFor[units.Service]())
	store := exportedMethods(reflect.TypeFor[*units.Store]())
	if !slices.Equal(iface, store) {
		t.Errorf("Service methods = %v, *Store methods = %v", iface, store)
	}
}

func TestNewServiceReturnsStore(t *testing.T) {
	svc := newService(t)
	if _, ok := svc.(*units.Store); !ok {
		t.Errorf("NewService returned %T, want *units.Store", svc)
	}
}

func mustCreate(t *testing.T, svc units.Service, n units.NewUnit) units.Unit {
	t.Helper()
	u, err := svc.Create(context.Background(), n)
	if err != nil {
		t.Fatalf("create %+v: %v", n, err)
	}
	return u
}

func advance(t *testing.T, svc units.Service, id string, to units.State) units.Unit {
	t.Helper()
	ctx := context.Background()
	u, err := svc.Get(ctx, id)
	if err != nil {
		t.Fatalf("get %s: %v", id, err)
	}
	u, err = svc.SetState(ctx, id, u.Fingerprint, to, "step")
	if err != nil {
		t.Fatalf("%s to %s: %v", id, to, err)
	}
	return u
}

func wantStatuses(t *testing.T, svc units.Service, step string, want map[string]units.Status) {
	t.Helper()
	ctx := context.Background()
	ids := make([]string, 0, len(want))
	for id, w := range want {
		ids = append(ids, id)
		got, err := svc.Status(ctx, id)
		if err != nil || got != w {
			t.Errorf("%s: status of %s = %q, %v; want %q", step, id, got, err, w)
		}
	}
	all, err := svc.Statuses(ctx, ids)
	if err != nil || !reflect.DeepEqual(all, want) {
		t.Errorf("%s: statuses = %v, %v; want %v", step, all, err, want)
	}
}

func TestScenarioEndToEnd(t *testing.T) {
	svc := newService(t)
	ctx := context.Background()

	b := mustCreate(t, svc, units.NewUnit{Level: units.LevelBrief, Title: "brief", Body: "instructions"})
	wantStatuses(t, svc, "brief created", map[string]units.Status{b.ID: units.StatusEmpty})

	s1 := mustCreate(t, svc, units.NewUnit{Level: units.LevelSlice, ParentID: b.ID, Title: "first"})
	s2 := mustCreate(t, svc, units.NewUnit{Level: units.LevelSlice, ParentID: b.ID, Title: "second"})
	wantStatuses(t, svc, "slices created", map[string]units.Status{
		b.ID: units.StatusArticulating, s1.ID: units.StatusEmpty, s2.ID: units.StatusEmpty,
	})

	var tasks []units.Unit
	for _, p := range []struct{ parent, title string }{
		{s1.ID, "t1"}, {s1.ID, "t2"}, {s2.ID, "t3"}, {s2.ID, "t4"},
	} {
		tasks = append(tasks, mustCreate(t, svc, units.NewUnit{
			Level: units.LevelTask, ParentID: p.parent, Title: p.title, Body: "do it", AcceptanceCriteria: "works",
		}))
	}
	t1, t2, t3, t4 := tasks[0], tasks[1], tasks[2], tasks[3]
	orphan := mustCreate(t, svc, units.NewUnit{Level: units.LevelTask, Title: "orphan", Body: "do it"})
	wantStatuses(t, svc, "tasks created", map[string]units.Status{
		b.ID: units.StatusArticulating, s1.ID: units.StatusArticulating, s2.ID: units.StatusArticulating,
		t1.ID: units.StatusProposed, t4.ID: units.StatusProposed, orphan.ID: units.StatusProposed,
	})

	if err := svc.AddLink(ctx, b.ID, units.LinkDocument, "D-43"); err != nil {
		t.Fatal(err)
	}
	if err := svc.AddLink(ctx, t1.ID, units.LinkExternal, "https://example.com/1"); err != nil {
		t.Fatal(err)
	}
	if err := svc.AddLink(ctx, t1.ID, units.LinkDocument, "D-12"); err != nil {
		t.Fatal(err)
	}
	links, err := svc.Links(ctx, t1.ID)
	if err != nil || len(links) != 2 || links[0].Kind != units.LinkDocument || links[1].Kind != units.LinkExternal {
		t.Fatalf("links = %+v, %v", links, err)
	}
	if err := svc.RemoveLink(ctx, t1.ID, units.LinkExternal, "https://example.com/1"); err != nil {
		t.Fatal(err)
	}
	if links, err = svc.Links(ctx, t1.ID); err != nil || len(links) != 1 {
		t.Fatalf("links after remove = %+v, %v", links, err)
	}
	note, err := svc.AddNote(ctx, s1.ID, units.NoteGeneral, "slice remark")
	if err != nil || note.UnitID != s1.ID || note.Body != "slice remark" {
		t.Fatalf("note = %+v, %v", note, err)
	}
	wantStatuses(t, svc, "links and notes", map[string]units.Status{
		b.ID: units.StatusArticulating, s1.ID: units.StatusArticulating, s2.ID: units.StatusArticulating,
	})

	advance(t, svc, t1.ID, units.StateTodo)
	wantStatuses(t, svc, "t1 todo", map[string]units.Status{
		b.ID: units.StatusArticulating, s1.ID: units.StatusArticulating, s2.ID: units.StatusArticulating,
		t1.ID: units.StatusTodo,
	})
	advance(t, svc, t2.ID, units.StateTodo)
	wantStatuses(t, svc, "t2 todo", map[string]units.Status{
		b.ID: units.StatusArticulating, s1.ID: units.StatusReady, s2.ID: units.StatusArticulating,
	})

	advance(t, svc, t3.ID, units.StateTodo)
	title := "renamed"
	_, err = svc.Update(ctx, t3.ID, t3.Fingerprint, units.Change{Title: &title})
	if !errors.Is(err, db.ErrStale) {
		t.Fatalf("stale update err = %v, want db.ErrStale", err)
	}
	if got, err := svc.Get(ctx, t3.ID); err != nil || got.Title != "t3" {
		t.Fatalf("task after rejected update = %+v, %v", got, err)
	}
	fresh, err := svc.Get(ctx, t3.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := svc.Update(ctx, t3.ID, fresh.Fingerprint, units.Change{Title: &title}); err != nil || got.Title != title {
		t.Fatalf("fresh update = %+v, %v", got, err)
	}
	wantStatuses(t, svc, "t3 updated", map[string]units.Status{
		b.ID: units.StatusArticulating, s1.ID: units.StatusReady, s2.ID: units.StatusArticulating,
	})

	advance(t, svc, t4.ID, units.StateTodo)
	wantStatuses(t, svc, "all todo", map[string]units.Status{
		b.ID: units.StatusReady, s1.ID: units.StatusReady, s2.ID: units.StatusReady,
		orphan.ID: units.StatusProposed,
	})

	advance(t, svc, t1.ID, units.StateInProgress)
	wantStatuses(t, svc, "t1 in progress", map[string]units.Status{
		b.ID: units.StatusInProgress, s1.ID: units.StatusInProgress, s2.ID: units.StatusReady,
		t1.ID: units.StatusInProgress,
	})
	advance(t, svc, t1.ID, units.StateDone)
	wantStatuses(t, svc, "t1 done", map[string]units.Status{
		b.ID: units.StatusReady, s1.ID: units.StatusReady, s2.ID: units.StatusReady,
		t1.ID: units.StatusDone,
	})

	advance(t, svc, t2.ID, units.StateInProgress)
	advance(t, svc, t2.ID, units.StateDone)
	wantStatuses(t, svc, "slice one done", map[string]units.Status{
		b.ID: units.StatusReady, s1.ID: units.StatusDone, s2.ID: units.StatusReady,
	})

	for _, id := range []string{t3.ID, t4.ID} {
		advance(t, svc, id, units.StateInProgress)
		advance(t, svc, id, units.StateDone)
	}
	wantStatuses(t, svc, "all done", map[string]units.Status{
		b.ID: units.StatusDone, s1.ID: units.StatusDone, s2.ID: units.StatusDone,
		orphan.ID: units.StatusProposed,
	})

	notes, err := svc.Notes(ctx, t1.ID)
	if err != nil || len(notes) != 3 {
		t.Errorf("t1 notes = %+v, %v; want one per transition", notes, err)
	}
	if notes, err = svc.Notes(ctx, s1.ID); err != nil || len(notes) != 1 {
		t.Errorf("s1 notes = %+v, %v", notes, err)
	}
}

func TestScenarioOrphanTasks(t *testing.T) {
	svc := newService(t)
	ctx := context.Background()
	b := mustCreate(t, svc, units.NewUnit{Level: units.LevelBrief, Title: "b", Body: "x"})
	s := mustCreate(t, svc, units.NewUnit{Level: units.LevelSlice, ParentID: b.ID, Title: "s"})
	owned := mustCreate(t, svc, units.NewUnit{Level: units.LevelTask, ParentID: s.ID, Title: "owned"})
	o1 := mustCreate(t, svc, units.NewUnit{Level: units.LevelTask, Title: "o1"})
	o2 := mustCreate(t, svc, units.NewUnit{Level: units.LevelTask, Title: "o2"})

	none := ""
	got, err := svc.List(ctx, units.Filter{Level: units.LevelTask, ParentID: &none})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, u := range got {
		ids = append(ids, u.ID)
	}
	if want := []string{o1.ID, o2.ID}; !slices.Equal(ids, want) {
		t.Errorf("orphans = %v, want %v", ids, want)
	}

	parent := s.ID
	got, err = svc.List(ctx, units.Filter{Level: units.LevelTask, ParentID: &parent})
	if err != nil || len(got) != 1 || got[0].ID != owned.ID {
		t.Errorf("owned = %+v, %v", got, err)
	}
	if got, err = svc.List(ctx, units.Filter{Level: units.LevelTask}); err != nil || len(got) != 3 {
		t.Errorf("all tasks = %+v, %v", got, err)
	}
}

func TestSentinelErrorsThroughInterface(t *testing.T) {
	svc := newService(t)
	ctx := context.Background()
	b := mustCreate(t, svc, units.NewUnit{Level: units.LevelBrief, Title: "b", Body: "x"})
	tk := mustCreate(t, svc, units.NewUnit{Level: units.LevelTask, Title: "t"})

	tests := []struct {
		name string
		err  error
		want error
	}{
		{"get missing", func() error { _, err := svc.Get(ctx, "T-99"); return err }(), units.ErrNotFound},
		{"status missing", func() error { _, err := svc.Status(ctx, "T-99"); return err }(), units.ErrNotFound},
		{"links missing", func() error { _, err := svc.Links(ctx, "T-99"); return err }(), units.ErrNotFound},
		{"empty title", func() error {
			_, err := svc.Create(ctx, units.NewUnit{Level: units.LevelBrief, Title: " "})
			return err
		}(), units.ErrInvalid},
		{"bad filter level", func() error { _, err := svc.List(ctx, units.Filter{Level: "x"}); return err }(), units.ErrInvalid},
		{"slice without brief", func() error {
			_, err := svc.Create(ctx, units.NewUnit{Level: units.LevelSlice, Title: "s"})
			return err
		}(), units.ErrInvalidParent},
		{"task under brief", func() error {
			_, err := svc.Create(ctx, units.NewUnit{Level: units.LevelTask, ParentID: b.ID, Title: "t"})
			return err
		}(), units.ErrInvalidParent},
		{"proposed to done", func() error {
			_, err := svc.SetState(ctx, tk.ID, tk.Fingerprint, units.StateDone, "")
			return err
		}(), units.ErrInvalidTransition},
		{"stale", func() error {
			_, err := svc.SetState(ctx, tk.ID, "stale", units.StateTodo, "")
			return err
		}(), db.ErrStale},
	}
	for _, tt := range tests {
		if !errors.Is(tt.err, tt.want) {
			t.Errorf("%s: err = %v, want errors.Is %v", tt.name, tt.err, tt.want)
		}
	}
}
