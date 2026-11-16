package units_test

import (
	"context"
	"errors"
	"testing"

	"github.com/bengearig/ralph/internal/units"
)

func TestDeriveStatusSlice(t *testing.T) {
	const (
		P = units.StatusProposed
		T = units.StatusTodo
		I = units.StatusInProgress
		B = units.StatusBlocked
		C = units.StatusCancelled
		D = units.StatusDone
	)
	tests := []struct {
		name string
		in   []units.Status
		want units.Status
	}{
		{"no tasks", nil, units.StatusEmpty},
		{"only cancelled", []units.Status{C, C}, units.StatusCancelled},
		{"cancelled beside done", []units.Status{C, D}, units.StatusDone},
		{"blocked beats in progress", []units.Status{B, I}, units.StatusBlocked},
		{"all done", []units.Status{D, D}, units.StatusDone},
		{"done beside in progress", []units.Status{D, I}, units.StatusInProgress},
		{"proposed beside todo", []units.Status{P, T}, units.StatusArticulating},
		{"proposed beside done", []units.Status{P, D}, units.StatusArticulating},
		{"all todo", []units.Status{T, T}, units.StatusReady},
		{"todo beside done", []units.Status{T, D}, units.StatusReady},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := units.DeriveStatus(units.LevelSlice, tc.in); got != tc.want {
				t.Errorf("got %s, want %s", got, tc.want)
			}
		})
	}
}

func TestDeriveStatusBrief(t *testing.T) {
	const (
		E = units.StatusEmpty
		A = units.StatusArticulating
		R = units.StatusReady
		D = units.StatusDone
		C = units.StatusCancelled
		B = units.StatusBlocked
		I = units.StatusInProgress
	)
	tests := []struct {
		name string
		in   []units.Status
		want units.Status
	}{
		{"no slices", nil, units.StatusEmpty},
		{"empty slice", []units.Status{E, R}, units.StatusArticulating},
		{"articulating slice", []units.Status{A, D}, units.StatusArticulating},
		{"ready and done", []units.Status{R, D}, units.StatusReady},
		{"all done", []units.Status{D, D}, units.StatusDone},
		{"cancelled slice ignored", []units.Status{C, D}, units.StatusDone},
		{"blocked beats everything", []units.Status{B, I, A, E, R, D}, units.StatusBlocked},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := units.DeriveStatus(units.LevelBrief, tc.in); got != tc.want {
				t.Errorf("got %s, want %s", got, tc.want)
			}
		})
	}
}

func status(t *testing.T, s *units.Store, id string) units.Status {
	t.Helper()
	got, err := s.Status(context.Background(), id)
	if err != nil {
		t.Fatalf("status %s: %v", id, err)
	}
	return got
}

func move(t *testing.T, s *units.Store, id string, path ...units.State) {
	t.Helper()
	ctx := context.Background()
	for _, st := range path {
		u, err := s.Get(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.SetState(ctx, id, u.Fingerprint, st, "because"); err != nil {
			t.Fatalf("%s to %s: %v", id, st, err)
		}
	}
}

func TestStatusOfTask(t *testing.T) {
	s := newStore(t)
	tk := task(t, s, "", "t")
	if got := status(t, s, tk.ID); got != units.StatusProposed {
		t.Errorf("got %s", got)
	}
	move(t, s, tk.ID, units.StateTodo, units.StateInProgress)
	if got := status(t, s, tk.ID); got != units.StatusInProgress {
		t.Errorf("got %s", got)
	}
}

func TestStatusOfSliceFollowsTasks(t *testing.T) {
	s := newStore(t)
	sl := slice(t, s, brief(t, s).ID)
	check := func(step string, want units.Status) {
		t.Helper()
		if got := status(t, s, sl.ID); got != want {
			t.Errorf("%s: got %s, want %s", step, got, want)
		}
	}
	check("no tasks", units.StatusEmpty)
	a := task(t, s, sl.ID, "a")
	b := task(t, s, sl.ID, "b")
	check("proposed", units.StatusArticulating)
	move(t, s, a.ID, units.StateTodo)
	check("one proposed", units.StatusArticulating)
	move(t, s, b.ID, units.StateTodo)
	check("all todo", units.StatusReady)
	move(t, s, a.ID, units.StateInProgress)
	check("one in progress", units.StatusInProgress)
	move(t, s, a.ID, units.StateBlocked)
	check("one blocked", units.StatusBlocked)
	move(t, s, a.ID, units.StateTodo, units.StateInProgress, units.StateDone)
	check("done beside todo", units.StatusReady)
	move(t, s, b.ID, units.StateCancelled)
	check("done beside cancelled", units.StatusDone)
}

func TestStatusOfSliceAllCancelled(t *testing.T) {
	s := newStore(t)
	sl := slice(t, s, brief(t, s).ID)
	a := task(t, s, sl.ID, "a")
	move(t, s, a.ID, units.StateCancelled)
	if got := status(t, s, sl.ID); got != units.StatusCancelled {
		t.Errorf("got %s", got)
	}
}

func TestStatusOfBriefFollowsSlices(t *testing.T) {
	s := newStore(t)
	br := brief(t, s)
	if got := status(t, s, br.ID); got != units.StatusEmpty {
		t.Errorf("no slices: got %s", got)
	}
	sl := slice(t, s, br.ID)
	if got := status(t, s, br.ID); got != units.StatusArticulating {
		t.Errorf("empty slice: got %s", got)
	}
	tk := task(t, s, sl.ID, "t")
	if got := status(t, s, br.ID); got != units.StatusArticulating {
		t.Errorf("proposed task: got %s", got)
	}
	move(t, s, tk.ID, units.StateTodo)
	if got := status(t, s, br.ID); got != units.StatusReady {
		t.Errorf("todo task: got %s", got)
	}
	move(t, s, tk.ID, units.StateInProgress, units.StateDone)
	if got := status(t, s, br.ID); got != units.StatusDone {
		t.Errorf("done task: got %s", got)
	}
}

func TestStatusOrphanTask(t *testing.T) {
	s := newStore(t)
	tk := task(t, s, "", "orphan")
	move(t, s, tk.ID, units.StateTodo)
	if got := status(t, s, tk.ID); got != units.StatusTodo {
		t.Errorf("got %s", got)
	}
}

func TestStatusUnknownUnit(t *testing.T) {
	s := newStore(t)
	if _, err := s.Status(context.Background(), "T-99"); !errors.Is(err, units.ErrNotFound) {
		t.Errorf("Status: got %v", err)
	}
	if _, err := s.Statuses(context.Background(), []string{"T-99"}); !errors.Is(err, units.ErrNotFound) {
		t.Errorf("Statuses: got %v", err)
	}
}

func TestStatusesBatch(t *testing.T) {
	s := newStore(t)
	br := brief(t, s)
	sl := slice(t, s, br.ID)
	tk := task(t, s, sl.ID, "t")
	move(t, s, tk.ID, units.StateTodo, units.StateInProgress)
	ids := []string{br.ID, sl.ID, tk.ID, sl.ID}
	got, err := s.Statuses(context.Background(), ids)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Errorf("got %d entries", len(got))
	}
	for _, id := range ids {
		if want := status(t, s, id); got[id] != want {
			t.Errorf("%s: batch %s, single %s", id, got[id], want)
		}
	}
}
