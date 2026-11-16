package units_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bengearig/ralph/internal/db"
	"github.com/bengearig/ralph/internal/units"
)

func ptr[T any](v T) *T { return &v }

func titles(t *testing.T, s *units.Store, parent string) []string {
	t.Helper()
	list, err := s.List(context.Background(), units.Filter{ParentID: &parent})
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, len(list))
	for i, u := range list {
		if u.Position != i+1 {
			t.Errorf("%s has position %d, want %d", u.Title, u.Position, i+1)
		}
		out[i] = u.Title
	}
	return out
}

func fiveTasks(t *testing.T, s *units.Store) (units.Unit, []units.Unit) {
	sl := slice(t, s, brief(t, s).ID)
	var tasks []units.Unit
	for _, n := range []string{"a", "b", "c", "d", "e"} {
		tasks = append(tasks, task(t, s, sl.ID, n))
	}
	return sl, tasks
}

func TestUpdateChangesFieldsAndFingerprint(t *testing.T) {
	s := newStore(t)
	tk := task(t, s, slice(t, s, brief(t, s).ID).ID, "old")
	got, err := s.Update(context.Background(), tk.ID, tk.Fingerprint, units.Change{
		Title: ptr("new"), Body: ptr("body"), AcceptanceCriteria: ptr("crit"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "new" || got.Body != "body" || got.AcceptanceCriteria != "crit" || got.Position != tk.Position {
		t.Errorf("got %+v", got)
	}
	if got.Fingerprint == tk.Fingerprint {
		t.Error("fingerprint unchanged")
	}
	again, _ := s.Get(context.Background(), tk.ID)
	if again.Fingerprint != got.Fingerprint {
		t.Error("returned fingerprint differs from stored")
	}
}

func TestUpdateStaleFingerprintRejected(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	tk := task(t, s, slice(t, s, brief(t, s).ID).ID, "t")
	if _, err := s.Update(ctx, tk.ID, tk.Fingerprint, units.Change{Title: ptr("first")}); err != nil {
		t.Fatal(err)
	}
	before, _ := s.Get(ctx, tk.ID)
	_, err := s.Update(ctx, tk.ID, tk.Fingerprint, units.Change{Title: ptr("second")})
	if !errors.Is(err, db.ErrStale) {
		t.Fatalf("err = %v", err)
	}
	after, _ := s.Get(ctx, tk.ID)
	if after != before {
		t.Errorf("unit changed: %+v -> %+v", before, after)
	}
}

func TestUpdateUnknownUnit(t *testing.T) {
	s := newStore(t)
	if _, err := s.Update(context.Background(), "T-9", "x", units.Change{}); !errors.Is(err, units.ErrNotFound) {
		t.Errorf("err = %v", err)
	}
}

func TestUpdateBlankTitle(t *testing.T) {
	s := newStore(t)
	tk := task(t, s, slice(t, s, brief(t, s).ID).ID, "t")
	_, err := s.Update(context.Background(), tk.ID, tk.Fingerprint, units.Change{Title: ptr("  ")})
	if !errors.Is(err, units.ErrInvalid) {
		t.Errorf("err = %v", err)
	}
}

func TestUpdateBriefBodyRejected(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	b := brief(t, s)
	if _, err := s.Update(ctx, b.ID, b.Fingerprint, units.Change{Body: ptr("x")}); !errors.Is(err, units.ErrInvalid) {
		t.Errorf("err = %v", err)
	}
	got, err := s.Update(ctx, b.ID, b.Fingerprint, units.Change{Title: ptr("renamed")})
	if err != nil || got.Body != b.Body {
		t.Errorf("title update: %+v, %v", got, err)
	}
}

func TestUpdateCriteriaOnlyOnTasks(t *testing.T) {
	s := newStore(t)
	b := brief(t, s)
	sl := slice(t, s, b.ID)
	for _, u := range []units.Unit{b, sl} {
		if _, err := s.Update(context.Background(), u.ID, u.Fingerprint, units.Change{AcceptanceCriteria: ptr("c")}); !errors.Is(err, units.ErrInvalid) {
			t.Errorf("%s: err = %v", u.ID, err)
		}
	}
}

func TestUpdateFinishedTaskRejected(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	sl := slice(t, s, brief(t, s).ID)
	for _, path := range [][]units.State{
		{units.StateTodo, units.StateInProgress, units.StateDone},
		{units.StateCancelled},
	} {
		u := task(t, s, sl.ID, "t")
		var err error
		for _, st := range path {
			if u, err = s.SetState(ctx, u.ID, u.Fingerprint, st, ""); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := s.Update(ctx, u.ID, u.Fingerprint, units.Change{Title: ptr("x")}); !errors.Is(err, units.ErrInvalid) {
			t.Errorf("%s: err = %v", u.State, err)
		}
	}
}

func TestUpdateSetsUpdatedAt(t *testing.T) {
	s := newStore(t)
	at := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	tk := task(t, s, slice(t, s, brief(t, s).ID).ID, "t")
	s.Now = func() time.Time { return at }
	got, err := s.Update(context.Background(), tk.ID, tk.Fingerprint, units.Change{Title: ptr("u")})
	if err != nil {
		t.Fatal(err)
	}
	if !got.UpdatedAt.Equal(at) || !got.CreatedAt.Equal(tk.CreatedAt) {
		t.Errorf("created %v updated %v", got.CreatedAt, got.UpdatedAt)
	}
}

func TestUpdateMovesPosition(t *testing.T) {
	s := newStore(t)
	sl, tasks := fiveTasks(t, s)
	if _, err := s.Update(context.Background(), tasks[4].ID, tasks[4].Fingerprint, units.Change{Position: ptr(1)}); err != nil {
		t.Fatal(err)
	}
	if got := titles(t, s, sl.ID); !slices.Equal(got, []string{"e", "a", "b", "c", "d"}) {
		t.Errorf("order = %v", got)
	}
}

func TestUpdateMoveShiftsOthersBothDirections(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	sl, tasks := fiveTasks(t, s)
	if _, err := s.Update(ctx, tasks[1].ID, tasks[1].Fingerprint, units.Change{Position: ptr(4)}); err != nil {
		t.Fatal(err)
	}
	if got := titles(t, s, sl.ID); !slices.Equal(got, []string{"a", "c", "d", "b", "e"}) {
		t.Errorf("down: %v", got)
	}
	d, _ := s.Get(ctx, tasks[3].ID)
	if _, err := s.Update(ctx, d.ID, d.Fingerprint, units.Change{Position: ptr(2)}); err != nil {
		t.Fatal(err)
	}
	if got := titles(t, s, sl.ID); !slices.Equal(got, []string{"a", "d", "c", "b", "e"}) {
		t.Errorf("up: %v", got)
	}
	d, _ = s.Get(ctx, d.ID)
	if _, err := s.Update(ctx, d.ID, d.Fingerprint, units.Change{Position: ptr(99)}); err != nil {
		t.Fatal(err)
	}
	if got := titles(t, s, sl.ID); !slices.Equal(got, []string{"a", "c", "b", "e", "d"}) {
		t.Errorf("past end: %v", got)
	}
	d, _ = s.Get(ctx, d.ID)
	if _, err := s.Update(ctx, d.ID, d.Fingerprint, units.Change{Position: ptr(5)}); err != nil {
		t.Fatal(err)
	}
	if got := titles(t, s, sl.ID); !slices.Equal(got, []string{"a", "c", "b", "e", "d"}) {
		t.Errorf("same place: %v", got)
	}
}

func TestUpdateNegativePosition(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	b := brief(t, s)
	tk := task(t, s, slice(t, s, b.ID).ID, "t")
	for name, c := range map[string]struct {
		u units.Unit
		p int
	}{
		"negative":           {tk, -1},
		"zero under":         {tk, 0},
		"nonzero no parent":  {b, 1},
		"negative no parent": {b, -1},
	} {
		if _, err := s.Update(ctx, c.u.ID, c.u.Fingerprint, units.Change{Position: ptr(c.p)}); !errors.Is(err, units.ErrInvalid) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if _, err := s.Update(ctx, b.ID, b.Fingerprint, units.Change{Position: ptr(0)}); err != nil {
		t.Errorf("zero without parent: %v", err)
	}
}

func TestSetStateTable(t *testing.T) {
	all := []units.State{units.StateProposed, units.StateTodo, units.StateInProgress, units.StateBlocked, units.StateCancelled, units.StateDone}
	paths := map[units.State][]units.State{
		units.StateProposed:   nil,
		units.StateTodo:       {units.StateTodo},
		units.StateInProgress: {units.StateTodo, units.StateInProgress},
		units.StateBlocked:    {units.StateTodo, units.StateBlocked},
		units.StateCancelled:  {units.StateCancelled},
		units.StateDone:       {units.StateTodo, units.StateInProgress, units.StateDone},
	}
	s := newStore(t)
	ctx := context.Background()
	sl := slice(t, s, brief(t, s).ID)
	for _, from := range all {
		for _, to := range all {
			if from == to {
				continue
			}
			u := task(t, s, sl.ID, "t")
			var err error
			for _, st := range paths[from] {
				if u, err = s.SetState(ctx, u.ID, u.Fingerprint, st, ""); err != nil {
					t.Fatalf("reach %s: %v", from, err)
				}
			}
			got, err := s.SetState(ctx, u.ID, u.Fingerprint, to, "")
			if slices.Contains(units.AllowedNext(from), to) {
				if err != nil || got.State != to {
					t.Errorf("%s to %s: %v, state %s", from, to, err, got.State)
				}
				continue
			}
			var te *units.TransitionError
			if !errors.As(err, &te) || !errors.Is(err, units.ErrInvalidTransition) {
				t.Errorf("%s to %s: err = %v", from, to, err)
				continue
			}
			if te.ID != u.ID || te.From != from || te.To != to || !slices.Equal(te.Allowed, units.AllowedNext(from)) {
				t.Errorf("%s to %s: %+v", from, to, te)
			}
			for _, a := range te.Allowed {
				if !strings.Contains(err.Error(), string(a)) {
					t.Errorf("message %q omits %s", err, a)
				}
			}
			if after, _ := s.Get(ctx, u.ID); after.State != from {
				t.Errorf("%s to %s changed state to %s", from, to, after.State)
			}
		}
	}
}

func TestSetStateSameStateInvalid(t *testing.T) {
	s := newStore(t)
	tk := task(t, s, slice(t, s, brief(t, s).ID).ID, "t")
	_, err := s.SetState(context.Background(), tk.ID, tk.Fingerprint, units.StateProposed, "")
	if !errors.Is(err, units.ErrInvalid) || errors.Is(err, units.ErrInvalidTransition) {
		t.Errorf("err = %v", err)
	}
}

func TestSetStateUnknownState(t *testing.T) {
	s := newStore(t)
	tk := task(t, s, slice(t, s, brief(t, s).ID).ID, "t")
	if _, err := s.SetState(context.Background(), tk.ID, tk.Fingerprint, "WAITING", ""); !errors.Is(err, units.ErrInvalid) {
		t.Errorf("err = %v", err)
	}
}

func TestSetStateOnlyTasks(t *testing.T) {
	s := newStore(t)
	b := brief(t, s)
	sl := slice(t, s, b.ID)
	for _, u := range []units.Unit{b, sl} {
		if _, err := s.SetState(context.Background(), u.ID, u.Fingerprint, units.StateTodo, ""); !errors.Is(err, units.ErrInvalid) {
			t.Errorf("%s: err = %v", u.ID, err)
		}
	}
}

func TestSetStateStale(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	tk := task(t, s, slice(t, s, brief(t, s).ID).ID, "t")
	if _, err := s.Update(ctx, tk.ID, tk.Fingerprint, units.Change{Title: ptr("x")}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetState(ctx, tk.ID, tk.Fingerprint, units.StateTodo, "why"); !errors.Is(err, db.ErrStale) {
		t.Fatalf("err = %v", err)
	}
	if notes, _ := s.Notes(ctx, tk.ID); len(notes) != 0 {
		t.Errorf("notes = %+v", notes)
	}
	if _, err := s.SetState(ctx, "T-9", "x", units.StateTodo, ""); !errors.Is(err, units.ErrNotFound) {
		t.Errorf("unknown: %v", err)
	}
}

func TestSetStateReasonCreatesNote(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	tk := task(t, s, slice(t, s, brief(t, s).ID).ID, "t")
	u, err := s.SetState(ctx, tk.ID, tk.Fingerprint, units.StateTodo, "ready")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetState(ctx, u.ID, u.Fingerprint, units.StateInProgress, "starting now"); err != nil {
		t.Fatal(err)
	}
	notes, err := s.Notes(ctx, tk.ID)
	if err != nil || len(notes) != 2 {
		t.Fatalf("notes = %+v, %v", notes, err)
	}
	if notes[0].Body != "PROPOSED to TODO: ready" || notes[1].Body != "TODO to IN_PROGRESS: starting now" || notes[1].Kind != units.NoteGeneral {
		t.Errorf("notes = %+v", notes)
	}
}

func TestSetStateNoReasonNoNote(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	tk := task(t, s, slice(t, s, brief(t, s).ID).ID, "t")
	u, _ := s.SetState(ctx, tk.ID, tk.Fingerprint, units.StateTodo, "")
	if _, err := s.SetState(ctx, u.ID, u.Fingerprint, units.StateBlocked, "  \n"); err != nil {
		t.Fatal(err)
	}
	if notes, _ := s.Notes(ctx, tk.ID); len(notes) != 0 {
		t.Errorf("notes = %+v", notes)
	}
}

func TestTransitionErrorFinal(t *testing.T) {
	err := &units.TransitionError{ID: "T-1", From: units.StateDone, To: units.StateTodo}
	if !strings.Contains(err.Error(), "final") {
		t.Errorf("message = %q", err)
	}
}

func TestAllowedNext(t *testing.T) {
	want := map[units.State][]units.State{
		units.StateProposed:   {units.StateTodo, units.StateCancelled},
		units.StateTodo:       {units.StateInProgress, units.StateProposed, units.StateBlocked, units.StateCancelled},
		units.StateInProgress: {units.StateTodo, units.StateDone, units.StateBlocked, units.StateCancelled},
		units.StateBlocked:    {units.StateTodo, units.StateProposed, units.StateCancelled},
		units.StateDone:       {},
		units.StateCancelled:  {},
	}
	for from, w := range want {
		got := units.AllowedNext(from)
		if len(got) != len(w) || (len(w) > 0 && !slices.Equal(got, w)) {
			t.Errorf("%s: %v, want %v", from, got, w)
		}
	}
	got := units.AllowedNext(units.StateProposed)
	got[0] = units.StateDone
	if units.AllowedNext(units.StateProposed)[0] != units.StateTodo {
		t.Error("caller can modify the table")
	}
}

func TestAddNoteAndNotes(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	b := brief(t, s)
	sl := slice(t, s, b.ID)
	tk := task(t, s, sl.ID, "t")
	for _, u := range []units.Unit{b, sl, tk} {
		for _, body := range []string{"one", "two"} {
			n, err := s.AddNote(ctx, u.ID, units.NoteGeneral, body)
			if err != nil || n.UnitID != u.ID || n.Body != body || n.Kind != units.NoteGeneral || n.ID == 0 || n.CreatedAt.IsZero() {
				t.Fatalf("%s: %+v, %v", u.ID, n, err)
			}
		}
		got, err := s.Notes(ctx, u.ID)
		if err != nil || len(got) != 2 || got[0].Body != "one" || got[1].Body != "two" || got[0].ID >= got[1].ID {
			t.Errorf("%s: %+v, %v", u.ID, got, err)
		}
	}
	empty := task(t, s, sl.ID, "quiet")
	if got, err := s.Notes(ctx, empty.ID); err != nil || got == nil || len(got) != 0 {
		t.Errorf("empty: %#v, %v", got, err)
	}
}

func TestNoteValidation(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	tk := task(t, s, slice(t, s, brief(t, s).ID).ID, "t")
	if _, err := s.AddNote(ctx, tk.ID, units.NoteGeneral, " \t"); !errors.Is(err, units.ErrInvalid) {
		t.Errorf("blank: %v", err)
	}
	if _, err := s.AddNote(ctx, tk.ID, "ITERATION", "x"); !errors.Is(err, units.ErrInvalid) {
		t.Errorf("kind: %v", err)
	}
	if _, err := s.AddNote(ctx, "T-99", units.NoteGeneral, "x"); !errors.Is(err, units.ErrNotFound) {
		t.Errorf("unit: %v", err)
	}
	if _, err := s.Notes(ctx, "T-99"); !errors.Is(err, units.ErrNotFound) {
		t.Errorf("notes: %v", err)
	}
}

func TestNotesDoNotChangeFingerprint(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	tk := task(t, s, slice(t, s, brief(t, s).ID).ID, "t")
	s.Now = func() time.Time { return tk.CreatedAt.Add(time.Hour) }
	if _, err := s.AddNote(ctx, tk.ID, units.NoteGeneral, "hello"); err != nil {
		t.Fatal(err)
	}
	after, _ := s.Get(ctx, tk.ID)
	if after != tk {
		t.Errorf("unit changed: %+v -> %+v", tk, after)
	}
}

func TestNoLostUpdatesUnderConcurrency(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	tk := task(t, s, slice(t, s, brief(t, s).ID).ID, "start")
	const workers = 8
	var (
		wg      sync.WaitGroup
		applied [workers]int
	)
	for w := range workers {
		wg.Go(func() {
			title := fmt.Sprintf("worker-%d", w)
			for applied[w] < 5 {
				u, err := s.Get(ctx, tk.ID)
				if err != nil {
					t.Error(err)
					return
				}
				_, err = s.Update(ctx, tk.ID, u.Fingerprint, units.Change{Title: &title, Body: ptr(fmt.Sprintf("%s-%d", title, applied[w]))})
				switch {
				case err == nil:
					applied[w]++
				case !errors.Is(err, db.ErrStale):
					t.Error(err)
					return
				}
			}
		})
	}
	wg.Wait()
	final, _ := s.Get(ctx, tk.ID)
	ok := false
	for w := range workers {
		ok = ok || (final.Title == fmt.Sprintf("worker-%d", w) && final.Body == fmt.Sprintf("worker-%d-4", w))
	}
	if !ok {
		t.Errorf("final = %q / %q", final.Title, final.Body)
	}
	for w, n := range applied {
		if n != 5 {
			t.Errorf("worker %d applied %d", w, n)
		}
	}
}
