package units_test

import (
	"context"
	"errors"
	"testing"

	"github.com/bengearig/ralph/internal/units"
)

func toState(t *testing.T, s *units.Store, u units.Unit, path ...units.State) units.Unit {
	t.Helper()
	for _, st := range path {
		var err error
		if u, err = s.SetState(context.Background(), u.ID, u.Fingerprint, st, reasonFor(st)); err != nil {
			t.Fatalf("to %s: %v", st, err)
		}
	}
	return u
}

func readyTask(t *testing.T, s *units.Store) units.Unit {
	t.Helper()
	return task(t, s, slice(t, s, brief(t, s).ID).ID, "t")
}

func requireReason(t *testing.T, s *units.Store, u units.Unit, to units.State, reason string) {
	t.Helper()
	ctx := context.Background()
	before, _ := s.Notes(ctx, u.ID)
	_, err := s.SetState(ctx, u.ID, u.Fingerprint, to, reason)
	var re *units.ReasonRequiredError
	if !errors.Is(err, units.ErrReasonRequired) || !errors.As(err, &re) {
		t.Fatalf("%s to %s: err = %v", u.State, to, err)
	}
	if re.ID != u.ID || re.From != u.State || re.To != to {
		t.Errorf("error = %+v", re)
	}
	want := "a reason is required to move " + u.ID + " from " + string(u.State) + " to " + string(to)
	if err.Error() != want {
		t.Errorf("message = %q, want %q", err, want)
	}
	got, gerr := s.Get(ctx, u.ID)
	if gerr != nil || got.State != u.State || got.Fingerprint != u.Fingerprint {
		t.Errorf("unit changed: %+v, %v", got, gerr)
	}
	if notes, _ := s.Notes(ctx, u.ID); len(notes) != len(before) {
		t.Errorf("notes = %+v", notes)
	}
}

func TestReasonRequiredToBlock(t *testing.T) {
	s := newStore(t)
	todo := toState(t, s, readyTask(t, s), units.StateTodo)
	requireReason(t, s, todo, units.StateBlocked, "")
	prog := toState(t, s, readyTask(t, s), units.StateTodo, units.StateInProgress)
	requireReason(t, s, prog, units.StateBlocked, "")
}

func TestReasonRequiredToCancel(t *testing.T) {
	s := newStore(t)
	for name, path := range map[string][]units.State{
		"proposed":    nil,
		"todo":        {units.StateTodo},
		"in progress": {units.StateTodo, units.StateInProgress},
		"blocked":     {units.StateTodo, units.StateBlocked},
	} {
		u := toState(t, s, readyTask(t, s), path...)
		t.Run(name, func(t *testing.T) { requireReason(t, s, u, units.StateCancelled, "") })
	}
}

func TestKickbackNeedsReason(t *testing.T) {
	s := newStore(t)
	requireReason(t, s, toState(t, s, readyTask(t, s), units.StateTodo), units.StateProposed, "")
	requireReason(t, s, toState(t, s, readyTask(t, s), units.StateTodo, units.StateBlocked), units.StateProposed, "")
}

func TestKickbackWritesKickbackNote(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	todo := toState(t, s, readyTask(t, s), units.StateTodo)
	got, err := s.SetState(ctx, todo.ID, todo.Fingerprint, units.StateProposed, "plan is wrong")
	if err != nil || got.State != units.StateProposed {
		t.Fatalf("got %+v, %v", got, err)
	}
	notes, _ := s.Notes(ctx, todo.ID)
	last := notes[len(notes)-1]
	if len(notes) != 1 || last.Kind != units.NoteKickback || last.Body != "TODO to PROPOSED: plan is wrong" {
		t.Errorf("notes = %+v", notes)
	}
}

func TestBlockedWritesBlockedNote(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	todo := toState(t, s, readyTask(t, s), units.StateTodo)
	if _, err := s.SetState(ctx, todo.ID, todo.Fingerprint, units.StateBlocked, "no access"); err != nil {
		t.Fatal(err)
	}
	notes, _ := s.Notes(ctx, todo.ID)
	last := notes[len(notes)-1]
	if last.Kind != units.NoteBlocked || last.Body != "TODO to BLOCKED: no access" {
		t.Errorf("notes = %+v", notes)
	}
}

func TestCancelWritesGeneralNote(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	u := readyTask(t, s)
	if _, err := s.SetState(ctx, u.ID, u.Fingerprint, units.StateCancelled, "not needed"); err != nil {
		t.Fatal(err)
	}
	notes, _ := s.Notes(ctx, u.ID)
	if len(notes) != 1 || notes[0].Kind != units.NoteGeneral || notes[0].Body != "PROPOSED to CANCELLED: not needed" {
		t.Errorf("notes = %+v", notes)
	}
}

func TestOtherMovesTakeOptionalReason(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	for _, reason := range []string{"", "because"} {
		u := readyTask(t, s)
		blocked := toState(t, s, readyTask(t, s), units.StateTodo, units.StateBlocked)
		moves := []struct {
			u  units.Unit
			to units.State
		}{{u, units.StateTodo}, {blocked, units.StateTodo}}
		for _, m := range moves {
			if _, err := s.SetState(ctx, m.u.ID, m.u.Fingerprint, m.to, reason); err != nil {
				t.Errorf("%s to %s, reason %q: %v", m.u.State, m.to, reason, err)
			}
		}
		prog := toState(t, s, readyTask(t, s), units.StateTodo)
		var err error
		for _, to := range []units.State{units.StateInProgress, units.StateTodo, units.StateInProgress, units.StateDone} {
			if prog, err = s.SetState(ctx, prog.ID, prog.Fingerprint, to, reason); err != nil {
				t.Errorf("to %s, reason %q: %v", to, reason, err)
			}
		}
	}
}

func TestBlankReasonIsWhitespaceOnly(t *testing.T) {
	s := newStore(t)
	todo := toState(t, s, readyTask(t, s), units.StateTodo)
	for _, r := range []string{" ", "   ", "\n", " \n\t "} {
		requireReason(t, s, todo, units.StateBlocked, r)
	}
}

func TestReasonRequiredAfterNotReady(t *testing.T) {
	s := newStore(t)
	u := newTask(t, s, sliceID(t, s), "", "")
	_, err := s.SetState(context.Background(), u.ID, u.Fingerprint, units.StateTodo, "")
	if !errors.Is(err, units.ErrNotReady) || errors.Is(err, units.ErrReasonRequired) {
		t.Errorf("err = %v", err)
	}
}

func TestAddNoteKickbackOnProposedAndTodo(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	u := readyTask(t, s)
	for _, st := range []units.State{units.StateProposed, units.StateTodo} {
		if st == units.StateTodo {
			u = toState(t, s, u, units.StateTodo)
		}
		n, err := s.AddNote(ctx, u.ID, units.NoteKickback, "replan")
		if err != nil || n.Kind != units.NoteKickback || n.Body != "replan" {
			t.Errorf("%s: %+v, %v", st, n, err)
		}
	}
}

func TestAddNoteKickbackRejectedOnSliceBriefAndFinished(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	b := brief(t, s)
	sl := slice(t, s, b.ID)
	done := toState(t, s, task(t, s, sl.ID, "d"), units.StateTodo, units.StateInProgress, units.StateDone)
	cancelled := toState(t, s, task(t, s, sl.ID, "c"), units.StateCancelled)
	for _, id := range []string{b.ID, sl.ID, done.ID, cancelled.ID} {
		_, err := s.AddNote(ctx, id, units.NoteKickback, "replan")
		if !errors.Is(err, units.ErrInvalid) {
			t.Errorf("%s: err = %v", id, err)
		}
		if notes, _ := s.Notes(ctx, id); len(notes) > 1 {
			t.Errorf("%s: notes = %+v", id, notes)
		}
	}
}

func TestAddNoteBlockedRejected(t *testing.T) {
	s := newStore(t)
	u := readyTask(t, s)
	_, err := s.AddNote(context.Background(), u.ID, units.NoteBlocked, "stuck")
	if !errors.Is(err, units.ErrInvalid) {
		t.Errorf("err = %v", err)
	}
}

func TestAddNoteBlankBodyStillInvalid(t *testing.T) {
	s := newStore(t)
	u := readyTask(t, s)
	for _, k := range []units.NoteKind{units.NoteGeneral, units.NoteKickback, units.NoteBlocked} {
		if _, err := s.AddNote(context.Background(), u.ID, k, " \n"); !errors.Is(err, units.ErrInvalid) {
			t.Errorf("%s: err = %v", k, err)
		}
	}
}
