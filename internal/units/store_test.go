package units_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/bengearig/ralph/internal/db"
	"github.com/bengearig/ralph/internal/testutil"
	"github.com/bengearig/ralph/internal/units"
)

func newStore(t *testing.T) *units.Store {
	t.Helper()
	return units.New(testutil.NewDB(t, db.Migrations))
}

func create(t *testing.T, s *units.Store, n units.NewUnit) units.Unit {
	t.Helper()
	u, err := s.Create(context.Background(), n)
	if err != nil {
		t.Fatalf("create %+v: %v", n, err)
	}
	return u
}

func brief(t *testing.T, s *units.Store) units.Unit {
	return create(t, s, units.NewUnit{Level: units.LevelBrief, Title: "b", Body: "do it"})
}

func slice(t *testing.T, s *units.Store, parent string) units.Unit {
	return create(t, s, units.NewUnit{Level: units.LevelSlice, ParentID: parent, Title: "s"})
}

func task(t *testing.T, s *units.Store, parent string, title string) units.Unit {
	return create(t, s, units.NewUnit{Level: units.LevelTask, ParentID: parent, Title: title})
}

func TestCreateBriefSliceTask(t *testing.T) {
	s := newStore(t)
	b := brief(t, s)
	sl := slice(t, s, b.ID)
	tk := create(t, s, units.NewUnit{Level: units.LevelTask, ParentID: sl.ID, Title: "t", Body: "b", AcceptanceCriteria: "c"})
	if b.ID != "B-1" || sl.ID != "S-1" || tk.ID != "T-1" {
		t.Fatalf("ids = %s %s %s", b.ID, sl.ID, tk.ID)
	}
	if b.ParentID != "" || sl.ParentID != "B-1" || tk.ParentID != "S-1" {
		t.Errorf("parents = %q %q %q", b.ParentID, sl.ParentID, tk.ParentID)
	}
	if tk.Body != "b" || tk.AcceptanceCriteria != "c" || tk.Level != units.LevelTask || tk.Number != 1 {
		t.Errorf("task = %+v", tk)
	}
	if b.State != "" || sl.State != "" {
		t.Errorf("states = %q %q, want none", b.State, sl.State)
	}
	if b.CreatedAt.IsZero() || !b.CreatedAt.Equal(b.UpdatedAt) || b.Fingerprint == "" {
		t.Errorf("brief = %+v", b)
	}
	got, err := s.Get(context.Background(), "T-1")
	if err != nil || got != tk {
		t.Errorf("get = %+v, %v; want %+v", got, err, tk)
	}
}

func TestNowIsStoredAsUTC(t *testing.T) {
	s := newStore(t)
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.FixedZone("x", 3600))
	s.Now = func() time.Time { return at }
	b := brief(t, s)
	if !b.CreatedAt.Equal(at) || b.CreatedAt.Location() != time.UTC {
		t.Errorf("created = %v, want %v in UTC", b.CreatedAt, at)
	}
}

func TestIDsCountedPerLevel(t *testing.T) {
	s := newStore(t)
	b1 := brief(t, s)
	b2 := brief(t, s)
	sl := slice(t, s, b1.ID)
	if b2.ID != "B-2" || sl.ID != "S-1" {
		t.Errorf("ids = %s %s", b2.ID, sl.ID)
	}
}

func TestOrphanTaskHasNoParent(t *testing.T) {
	s := newStore(t)
	tk := task(t, s, "", "t")
	if tk.ParentID != "" || tk.State != units.StateProposed || tk.Position != 0 {
		t.Errorf("task = %+v", tk)
	}
}

func TestTaskStartsProposed(t *testing.T) {
	s := newStore(t)
	sl := slice(t, s, brief(t, s).ID)
	if tk := task(t, s, sl.ID, "t"); tk.State != units.StateProposed {
		t.Errorf("state = %q", tk.State)
	}
}

func TestBriefBodyKeptVerbatim(t *testing.T) {
	s := newStore(t)
	body := "\n\n  keep this \n\n"
	b := create(t, s, units.NewUnit{Level: units.LevelBrief, Title: "b", Body: body})
	got, err := s.Get(context.Background(), b.ID)
	if err != nil || got.Body != body || b.Body != body {
		t.Errorf("body = %q, %v; want %q", got.Body, err, body)
	}
}

func TestCreateRejectsBadParents(t *testing.T) {
	s := newStore(t)
	b := brief(t, s)
	sl := slice(t, s, b.ID)
	tk := task(t, s, sl.ID, "t")
	tests := []struct {
		name string
		in   units.NewUnit
	}{
		{"brief with parent", units.NewUnit{Level: units.LevelBrief, ParentID: b.ID, Title: "x", Body: "y", Position: 0}},
		{"slice with no parent", units.NewUnit{Level: units.LevelSlice, Title: "x"}},
		{"slice under slice", units.NewUnit{Level: units.LevelSlice, ParentID: sl.ID, Title: "x"}},
		{"task under brief", units.NewUnit{Level: units.LevelTask, ParentID: b.ID, Title: "x"}},
		{"task under task", units.NewUnit{Level: units.LevelTask, ParentID: tk.ID, Title: "x"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := s.Create(context.Background(), tt.in)
			if !errors.Is(err, units.ErrInvalidParent) {
				t.Errorf("err = %v, want ErrInvalidParent", err)
			}
		})
	}
	all, err := s.List(context.Background(), units.Filter{})
	if err != nil || len(all) != 3 {
		t.Errorf("units after rejections = %d, %v; want 3", len(all), err)
	}
	if next := brief(t, s); next.ID != "B-2" {
		t.Errorf("number was consumed by a rejected create: %s", next.ID)
	}
}

func TestCreateUnknownParent(t *testing.T) {
	s := newStore(t)
	for _, l := range []units.Level{units.LevelSlice, units.LevelTask} {
		_, err := s.Create(context.Background(), units.NewUnit{Level: l, ParentID: "X-9", Title: "x"})
		if !errors.Is(err, units.ErrNotFound) {
			t.Errorf("%s: err = %v, want ErrNotFound", l, err)
		}
	}
}

func TestCreateRejectsInvalidInput(t *testing.T) {
	s := newStore(t)
	b := brief(t, s)
	sl := slice(t, s, b.ID)
	tests := []struct {
		name string
		in   units.NewUnit
	}{
		{"blank title", units.NewUnit{Level: units.LevelTask, Title: " \t\n"}},
		{"empty title", units.NewUnit{Level: units.LevelTask}},
		{"unknown level", units.NewUnit{Level: "epic", Title: "x"}},
		{"empty brief body", units.NewUnit{Level: units.LevelBrief, Title: "x"}},
		{"criteria on brief", units.NewUnit{Level: units.LevelBrief, Title: "x", Body: "y", AcceptanceCriteria: "c"}},
		{"criteria on slice", units.NewUnit{Level: units.LevelSlice, ParentID: b.ID, Title: "x", AcceptanceCriteria: "c"}},
		{"negative position", units.NewUnit{Level: units.LevelTask, ParentID: sl.ID, Title: "x", Position: -1}},
		{"position under no parent", units.NewUnit{Level: units.LevelTask, Title: "x", Position: 2}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := s.Create(context.Background(), tt.in)
			if !errors.Is(err, units.ErrInvalid) {
				t.Errorf("err = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestPositionAppendsAfterLastSibling(t *testing.T) {
	s := newStore(t)
	sl := slice(t, s, brief(t, s).ID)
	for want := 1; want <= 3; want++ {
		if tk := task(t, s, sl.ID, "t"); tk.Position != want {
			t.Errorf("position = %d, want %d", tk.Position, want)
		}
	}
}

func TestPositionInsertShiftsLaterSiblings(t *testing.T) {
	s := newStore(t)
	sl := slice(t, s, brief(t, s).ID)
	a := task(t, s, sl.ID, "a")
	b := task(t, s, sl.ID, "b")
	c := task(t, s, sl.ID, "c")
	x := create(t, s, units.NewUnit{Level: units.LevelTask, ParentID: sl.ID, Title: "x", Position: 2})
	if x.Position != 2 {
		t.Fatalf("x position = %d", x.Position)
	}
	want := map[string]int{a.ID: 1, x.ID: 2, b.ID: 3, c.ID: 4}
	for id, pos := range want {
		got, err := s.Get(context.Background(), id)
		if err != nil || got.Position != pos {
			t.Errorf("%s position = %d, %v; want %d", id, got.Position, err, pos)
		}
	}
	first := create(t, s, units.NewUnit{Level: units.LevelTask, ParentID: sl.ID, Title: "first", Position: 1})
	if first.Position != 1 {
		t.Errorf("first position = %d", first.Position)
	}
	if end := create(t, s, units.NewUnit{Level: units.LevelTask, ParentID: sl.ID, Title: "far", Position: 99}); end.Position != 6 {
		t.Errorf("past-the-end position = %d, want 6", end.Position)
	}
}

func TestGetNotFound(t *testing.T) {
	s := newStore(t)
	_, err := s.Get(context.Background(), "T-1")
	if !errors.Is(err, units.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func ids(us []units.Unit) []string {
	out := make([]string, len(us))
	for i, u := range us {
		out[i] = u.ID
	}
	return out
}

func eq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestListOrder(t *testing.T) {
	s := newStore(t)
	b := brief(t, s)
	s1 := slice(t, s, b.ID)
	s2 := slice(t, s, b.ID)
	o1 := task(t, s, "", "o1")
	t1 := task(t, s, s2.ID, "t1")
	t2 := task(t, s, s1.ID, "t2")
	t3 := create(t, s, units.NewUnit{Level: units.LevelTask, ParentID: s2.ID, Title: "t3", Position: 1})
	o2 := task(t, s, "", "o2")
	got, err := s.List(context.Background(), units.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{}
	want = append(want, b.ID, o1.ID, o2.ID, s1.ID, s2.ID, t2.ID, t3.ID, t1.ID)
	if !eq(ids(got), want) {
		t.Errorf("order = %v, want %v", ids(got), want)
	}
}

func TestListFilters(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	b := brief(t, s)
	sl := slice(t, s, b.ID)
	o := task(t, s, "", "orphan")
	c := task(t, s, sl.ID, "child")
	empty := ""

	tests := []struct {
		name string
		f    units.Filter
		want []string
	}{
		{"level", units.Filter{Level: units.LevelTask}, []string{o.ID, c.ID}},
		{"parent", units.Filter{ParentID: &sl.ID}, []string{c.ID}},
		{"orphans", units.Filter{Level: units.LevelTask, ParentID: &empty}, []string{o.ID}},
		{"state", units.Filter{States: []units.State{units.StateProposed}}, []string{o.ID, c.ID}},
		{"state none", units.Filter{States: []units.State{units.StateDone, units.StateTodo}}, []string{}},
		{"level and parent", units.Filter{Level: units.LevelSlice, ParentID: &b.ID}, []string{sl.ID}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := s.List(ctx, tt.f)
			if err != nil || !eq(ids(got), tt.want) {
				t.Errorf("got %v, %v; want %v", ids(got), err, tt.want)
			}
		})
	}
	if _, err := s.List(ctx, units.Filter{Level: "epic"}); !errors.Is(err, units.ErrInvalid) {
		t.Errorf("bad level: err = %v", err)
	}
	if _, err := s.List(ctx, units.Filter{States: []units.State{"OPEN"}}); !errors.Is(err, units.ErrInvalid) {
		t.Errorf("bad state: err = %v", err)
	}
}

func TestFingerprintChangesWithContent(t *testing.T) {
	s := newStore(t)
	sl := slice(t, s, brief(t, s).ID)
	a := create(t, s, units.NewUnit{Level: units.LevelTask, ParentID: sl.ID, Title: "same", Body: "body"})
	b := create(t, s, units.NewUnit{Level: units.LevelTask, ParentID: sl.ID, Title: "same", Body: "body"})
	if a.Fingerprint == b.Fingerprint {
		t.Error("different units share a fingerprint")
	}
	before := b.Fingerprint
	if _, err := s.Create(context.Background(), units.NewUnit{Level: units.LevelTask, ParentID: sl.ID, Title: "ins", Position: 1}); err != nil {
		t.Fatal(err)
	}
	after, err := s.Get(context.Background(), b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Fingerprint == before {
		t.Error("fingerprint did not change when the position changed")
	}
}

func TestFingerprintIsStableAcrossReads(t *testing.T) {
	s := newStore(t)
	b := brief(t, s)
	first, err := s.Get(context.Background(), b.ID)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Get(context.Background(), b.ID)
	if err != nil || first.Fingerprint != second.Fingerprint || first.Fingerprint != b.Fingerprint {
		t.Errorf("fingerprints = %s %s %s, %v", b.Fingerprint, first.Fingerprint, second.Fingerprint, err)
	}
	all, err := s.List(context.Background(), units.Filter{})
	if err != nil || len(all) != 1 || all[0].Fingerprint != first.Fingerprint {
		t.Errorf("list fingerprint differs: %+v, %v", all, err)
	}
}

func TestConcurrentCreatesGetDistinctNumbers(t *testing.T) {
	s := newStore(t)
	const workers, each = 8, 10
	var wg sync.WaitGroup
	errs := make(chan error, workers*each)
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range each {
				if _, err := s.Create(context.Background(), units.NewUnit{Level: units.LevelTask, Title: "t"}); err != nil {
					errs <- err
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	all, err := s.List(context.Background(), units.Filter{Level: units.LevelTask})
	if err != nil || len(all) != workers*each {
		t.Fatalf("tasks = %d, %v", len(all), err)
	}
	seen := map[string]bool{}
	for _, u := range all {
		seen[u.ID] = true
	}
	for n := 1; n <= workers*each; n++ {
		if id := fmt.Sprintf("T-%d", n); !seen[id] {
			t.Errorf("missing %s", id)
		}
	}
}
