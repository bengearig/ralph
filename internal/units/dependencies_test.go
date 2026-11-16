package units_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/bengearig/ralph/internal/units"
)

func addDep(t *testing.T, s *units.Store, sliceID, dependsOn string) {
	t.Helper()
	if err := s.AddDependency(context.Background(), sliceID, dependsOn); err != nil {
		t.Fatalf("%s waits for %s: %v", sliceID, dependsOn, err)
	}
}

func todoTask(t *testing.T, s *units.Store, parent string) units.Unit {
	t.Helper()
	return toTodo(t, s, task(t, s, parent, "t"))
}

func finish(t *testing.T, s *units.Store, id string) {
	t.Helper()
	advance(t, s, id, units.StateInProgress)
	advance(t, s, id, units.StateDone)
}

func twoSlices(t *testing.T) (*units.Store, units.Unit, units.Unit, units.Unit) {
	t.Helper()
	s := newStore(t)
	b := brief(t, s)
	return s, b, slice(t, s, b.ID), slice(t, s, b.ID)
}

func TestAddDependencyBasic(t *testing.T) {
	s, _, a, b := twoSlices(t)
	todoTask(t, s, b.ID)
	addDep(t, s, a.ID, b.ID)
	got, err := s.Dependencies(context.Background(), a.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := units.Dependency{ID: b.ID, Title: "s", Status: units.StatusReady, Satisfied: false}
	if len(got) != 1 || got[0] != want {
		t.Errorf("Dependencies = %+v, want [%+v]", got, want)
	}
	if other, err := s.Dependencies(context.Background(), b.ID); err != nil || len(other) != 0 {
		t.Errorf("reverse Dependencies = %+v, %v", other, err)
	}
}

func TestAddDependencyIdempotent(t *testing.T) {
	s, _, a, b := twoSlices(t)
	addDep(t, s, a.ID, b.ID)
	addDep(t, s, a.ID, b.ID)
	if got, _ := s.Dependencies(context.Background(), a.ID); len(got) != 1 {
		t.Errorf("Dependencies = %+v", got)
	}
}

func TestAddDependencyUnknownUnit(t *testing.T) {
	s, _, a, _ := twoSlices(t)
	ctx := context.Background()
	if err := s.AddDependency(ctx, a.ID, "S-99"); !errors.Is(err, units.ErrNotFound) {
		t.Errorf("unknown target: %v", err)
	}
	if err := s.AddDependency(ctx, "S-99", a.ID); !errors.Is(err, units.ErrNotFound) {
		t.Errorf("unknown source: %v", err)
	}
}

func TestAddDependencyNotSlices(t *testing.T) {
	s, br, a, _ := twoSlices(t)
	tk := task(t, s, a.ID, "t")
	ctx := context.Background()
	cases := []struct {
		name, from, to, want string
	}{
		{"task waiting", tk.ID, a.ID, tk.ID + " is a task"},
		{"waits for task", a.ID, tk.ID, tk.ID + " is a task"},
		{"brief waiting", br.ID, a.ID, br.ID + " is a brief"},
		{"waits for brief", a.ID, br.ID, br.ID + " is a brief"},
	}
	for _, tc := range cases {
		err := s.AddDependency(ctx, tc.from, tc.to)
		if !errors.Is(err, units.ErrInvalid) || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want ErrInvalid mentioning %q", tc.name, err, tc.want)
		}
	}
}

func TestAddDependencySelf(t *testing.T) {
	s, _, a, _ := twoSlices(t)
	if err := s.AddDependency(context.Background(), a.ID, a.ID); !errors.Is(err, units.ErrInvalid) {
		t.Errorf("err = %v", err)
	}
}

func TestAddDependencyCycleDirect(t *testing.T) {
	s, _, a, b := twoSlices(t)
	addDep(t, s, a.ID, b.ID)
	err := s.AddDependency(context.Background(), b.ID, a.ID)
	if !errors.Is(err, units.ErrInvalid) || !strings.Contains(err.Error(), "S-2 -> S-1 -> S-2") {
		t.Errorf("err = %v", err)
	}
	if got, _ := s.Dependencies(context.Background(), b.ID); len(got) != 0 {
		t.Errorf("refused dependency was stored: %+v", got)
	}
}

func TestAddDependencyCycleTransitive(t *testing.T) {
	s, br, a, b := twoSlices(t)
	c := slice(t, s, br.ID)
	addDep(t, s, a.ID, b.ID)
	addDep(t, s, b.ID, c.ID)
	err := s.AddDependency(context.Background(), c.ID, a.ID)
	if !errors.Is(err, units.ErrInvalid) || !strings.Contains(err.Error(), "S-3 -> S-1 -> S-2 -> S-3") {
		t.Errorf("err = %v", err)
	}
}

func TestAddDependencyAcrossBriefsAllowed(t *testing.T) {
	s := newStore(t)
	a := slice(t, s, brief(t, s).ID)
	b := slice(t, s, brief(t, s).ID)
	addDep(t, s, a.ID, b.ID)
	if got, _ := s.Dependencies(context.Background(), a.ID); len(got) != 1 || got[0].ID != b.ID {
		t.Errorf("Dependencies = %+v", got)
	}
}

func TestRemoveDependency(t *testing.T) {
	s, _, a, b := twoSlices(t)
	ctx := context.Background()
	addDep(t, s, a.ID, b.ID)
	if err := s.RemoveDependency(ctx, a.ID, b.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Dependencies(ctx, a.ID); len(got) != 0 {
		t.Errorf("Dependencies = %+v", got)
	}
	if err := s.RemoveDependency(ctx, a.ID, b.ID); err != nil {
		t.Errorf("removing an absent dependency: %v", err)
	}
}

func TestDependencySatisfiedWhenDone(t *testing.T) {
	s, _, a, b := twoSlices(t)
	ctx := context.Background()
	addDep(t, s, a.ID, b.ID)
	finish(t, s, todoTask(t, s, b.ID).ID)
	deps, err := s.Dependencies(ctx, a.ID)
	if err != nil || len(deps) != 1 || deps[0].Status != units.StatusDone || !deps[0].Satisfied {
		t.Errorf("Dependencies = %+v, %v", deps, err)
	}
	if w, err := s.Waiting(ctx, a.ID); err != nil || len(w) != 0 {
		t.Errorf("Waiting = %+v, %v", w, err)
	}
}

func TestDependencyNotSatisfiedForEmptyCancelledAndInProgress(t *testing.T) {
	s := newStore(t)
	br := brief(t, s)
	waiter := slice(t, s, br.ID)
	build := map[units.Status]func(id string){
		units.StatusEmpty:        func(string) {},
		units.StatusArticulating: func(id string) { task(t, s, id, "t") },
		units.StatusReady:        func(id string) { todoTask(t, s, id) },
		units.StatusInProgress:   func(id string) { advance(t, s, todoTask(t, s, id).ID, units.StateInProgress) },
		units.StatusBlocked:      func(id string) { advance(t, s, todoTask(t, s, id).ID, units.StateBlocked) },
		units.StatusCancelled:    func(id string) { advance(t, s, task(t, s, id, "t").ID, units.StateCancelled) },
	}
	ctx := context.Background()
	for want, fn := range build {
		dep := slice(t, s, br.ID)
		fn(dep.ID)
		addDep(t, s, waiter.ID, dep.ID)
		deps, err := s.Dependencies(ctx, waiter.ID)
		if err != nil {
			t.Fatal(err)
		}
		var got *units.Dependency
		for i := range deps {
			if deps[i].ID == dep.ID {
				got = &deps[i]
			}
		}
		if got == nil || got.Status != want || got.Satisfied {
			t.Errorf("%s: dependency = %+v", want, got)
		}
		waiting, _ := s.Waiting(ctx, waiter.ID)
		found := false
		for _, w := range waiting {
			found = found || w.ID == dep.ID
		}
		if !found {
			t.Errorf("%s: not in Waiting %+v", want, waiting)
		}
	}
}

func TestWaitingOnlyUnsatisfied(t *testing.T) {
	s, br, a, b := twoSlices(t)
	c := slice(t, s, br.ID)
	finish(t, s, todoTask(t, s, b.ID).ID)
	todoTask(t, s, c.ID)
	addDep(t, s, a.ID, b.ID)
	addDep(t, s, a.ID, c.ID)
	w, err := s.Waiting(context.Background(), a.ID)
	if err != nil || len(w) != 1 || w[0].ID != c.ID {
		t.Errorf("Waiting = %+v, %v", w, err)
	}
}

func TestDependenciesOrderedByNumber(t *testing.T) {
	s, br, a, b := twoSlices(t)
	c := slice(t, s, br.ID)
	addDep(t, s, a.ID, c.ID)
	addDep(t, s, a.ID, b.ID)
	got, err := s.Dependencies(context.Background(), a.ID)
	if err != nil || len(got) != 2 || got[0].ID != b.ID || got[1].ID != c.ID {
		t.Errorf("Dependencies = %+v, %v", got, err)
	}
}

func TestDependenciesOfNonSliceInvalid(t *testing.T) {
	s, br, a, _ := twoSlices(t)
	tk := task(t, s, a.ID, "t")
	ctx := context.Background()
	for _, id := range []string{br.ID, tk.ID} {
		if _, err := s.Dependencies(ctx, id); !errors.Is(err, units.ErrInvalid) {
			t.Errorf("Dependencies(%s) = %v", id, err)
		}
		if _, err := s.Waiting(ctx, id); !errors.Is(err, units.ErrInvalid) {
			t.Errorf("Waiting(%s) = %v", id, err)
		}
	}
	if _, err := s.Dependencies(ctx, "S-99"); !errors.Is(err, units.ErrNotFound) {
		t.Errorf("unknown slice: %v", err)
	}
}

func waitingSlice(t *testing.T) (s *units.Store, waiter, d1, d3 units.Unit) {
	t.Helper()
	s = newStore(t)
	br := brief(t, s)
	d1 = slice(t, s, br.ID)
	waiter = slice(t, s, br.ID)
	d3 = slice(t, s, br.ID)
	todoTask(t, s, d1.ID)
	advance(t, s, todoTask(t, s, d3.ID).ID, units.StateInProgress)
	addDep(t, s, waiter.ID, d3.ID)
	addDep(t, s, waiter.ID, d1.ID)
	return s, waiter, d1, d3
}

func TestStartWaitingRejected(t *testing.T) {
	s, waiter, _, _ := waitingSlice(t)
	tk := todoTask(t, s, waiter.ID)
	ctx := context.Background()
	notes, _ := s.Notes(ctx, tk.ID)
	_, err := s.SetState(ctx, tk.ID, tk.Fingerprint, units.StateInProgress, "go")
	var we *units.WaitingError
	if !errors.Is(err, units.ErrWaiting) || !errors.As(err, &we) {
		t.Fatalf("err = %v", err)
	}
	want := tk.ID + " cannot start: slice " + waiter.ID + " waits for S-1 (READY), S-3 (IN_PROGRESS); finish them or remove the dependency"
	if err.Error() != want {
		t.Errorf("message = %q, want %q", err, want)
	}
	if we.ID != tk.ID || we.Slice != waiter.ID || len(we.Waiting) != 2 {
		t.Errorf("WaitingError = %+v", we)
	}
	got, _ := s.Get(ctx, tk.ID)
	if got.State != units.StateTodo || got.Fingerprint != tk.Fingerprint {
		t.Errorf("task changed: %+v", got)
	}
	if after, _ := s.Notes(ctx, tk.ID); len(after) != len(notes) {
		t.Errorf("notes = %+v", after)
	}
}

func TestStartAfterDependencyDone(t *testing.T) {
	s, waiter, d1, d3 := waitingSlice(t)
	tk := todoTask(t, s, waiter.ID)
	tasks, _ := s.List(context.Background(), units.Filter{ParentID: str(d1.ID)})
	finish(t, s, tasks[0].ID)
	advance(t, s, mustTask(t, s, d3.ID), units.StateDone)
	if got := advance(t, s, tk.ID, units.StateInProgress); got.State != units.StateInProgress {
		t.Errorf("state = %s", got.State)
	}
}

func mustTask(t *testing.T, s *units.Store, sliceID string) string {
	t.Helper()
	tasks, err := s.List(context.Background(), units.Filter{ParentID: str(sliceID)})
	if err != nil || len(tasks) == 0 {
		t.Fatalf("tasks of %s: %v, %v", sliceID, tasks, err)
	}
	return tasks[0].ID
}

func TestStartAfterDependencyRemoved(t *testing.T) {
	s, waiter, d1, d3 := waitingSlice(t)
	tk := todoTask(t, s, waiter.ID)
	ctx := context.Background()
	for _, d := range []string{d1.ID, d3.ID} {
		if err := s.RemoveDependency(ctx, waiter.ID, d); err != nil {
			t.Fatal(err)
		}
	}
	if got := advance(t, s, tk.ID, units.StateInProgress); got.State != units.StateInProgress {
		t.Errorf("state = %s", got.State)
	}
}

func TestWaitingDoesNotBlockOtherMoves(t *testing.T) {
	s, waiter, _, _ := waitingSlice(t)
	ctx := context.Background()
	p := task(t, s, waiter.ID, "p")
	if got := toTodo(t, s, p); got.State != units.StateTodo {
		t.Errorf("promote: %s", got.State)
	}
	b := todoTask(t, s, waiter.ID)
	advance(t, s, b.ID, units.StateBlocked)
	k := todoTask(t, s, waiter.ID)
	advance(t, s, k.ID, units.StateProposed)
	c := todoTask(t, s, waiter.ID)
	advance(t, s, c.ID, units.StateCancelled)
	for id, want := range map[string]units.State{b.ID: units.StateBlocked, k.ID: units.StateProposed, c.ID: units.StateCancelled} {
		if got, _ := s.Get(ctx, id); got.State != want {
			t.Errorf("%s = %s, want %s", id, got.State, want)
		}
	}
}

func TestOrphanTaskNeverWaits(t *testing.T) {
	s := newStore(t)
	o := create(t, s, units.NewUnit{Level: units.LevelTask, Title: "o", Body: "do it"})
	o = toTodo(t, s, o)
	if got := advance(t, s, o.ID, units.StateInProgress); got.State != units.StateInProgress {
		t.Errorf("state = %s", got.State)
	}
}

func TestWaitingErrorOrderAfterReadiness(t *testing.T) {
	s, waiter, _, _ := waitingSlice(t)
	p := create(t, s, units.NewUnit{Level: units.LevelTask, ParentID: waiter.ID, Title: "bare"})
	_, err := s.SetState(context.Background(), p.ID, p.Fingerprint, units.StateInProgress, "")
	if !errors.Is(err, units.ErrInvalidTransition) || errors.Is(err, units.ErrWaiting) {
		t.Errorf("err = %v, want the transition check before waiting", err)
	}
	_, err = s.SetState(context.Background(), p.ID, p.Fingerprint, units.StateTodo, "")
	if !errors.Is(err, units.ErrNotReady) || errors.Is(err, units.ErrWaiting) {
		t.Errorf("err = %v, want not ready", err)
	}
}

func TestDependencyNotInFingerprint(t *testing.T) {
	s, _, a, b := twoSlices(t)
	ctx := context.Background()
	before, _ := s.Get(ctx, a.ID)
	addDep(t, s, a.ID, b.ID)
	after, _ := s.Get(ctx, a.ID)
	if before.Fingerprint != after.Fingerprint {
		t.Errorf("fingerprint %q became %q", before.Fingerprint, after.Fingerprint)
	}
	if err := s.RemoveDependency(ctx, a.ID, b.ID); err != nil {
		t.Fatal(err)
	}
	if again, _ := s.Get(ctx, a.ID); again.Fingerprint != before.Fingerprint {
		t.Errorf("fingerprint after removal = %q", again.Fingerprint)
	}
}

func TestStartWaitingConcurrent(t *testing.T) {
	s := newStore(t)
	br := brief(t, s)
	dep := slice(t, s, br.ID)
	waiter := slice(t, s, br.ID)
	depTask := todoTask(t, s, dep.ID)
	advance(t, s, depTask.ID, units.StateInProgress)
	addDep(t, s, waiter.ID, dep.ID)
	const n = 8
	starts := make([]units.Unit, n)
	for i := range starts {
		starts[i] = todoTask(t, s, waiter.ID)
	}
	ctx := context.Background()
	var wg sync.WaitGroup
	errs := make(chan error, n+1)
	for _, u := range starts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 20 {
				_, err := s.SetState(ctx, u.ID, u.Fingerprint, units.StateInProgress, "")
				if err == nil {
					if st, serr := s.Status(ctx, dep.ID); serr != nil || st != units.StatusDone {
						errs <- errors.New(u.ID + " started while " + dep.ID + " was " + string(st))
					}
					return
				}
				if !errors.Is(err, units.ErrWaiting) {
					errs <- err
					return
				}
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		cur, err := s.Get(ctx, depTask.ID)
		if err == nil {
			_, err = s.SetState(ctx, depTask.ID, cur.Fingerprint, units.StateDone, "")
		}
		if err != nil {
			errs <- err
		}
	}()
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}
