package units

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

var transitions = map[State][]State{
	StateProposed:   {StateTodo, StateCancelled},
	StateTodo:       {StateInProgress, StateProposed, StateBlocked, StateCancelled},
	StateInProgress: {StateTodo, StateDone, StateBlocked, StateCancelled},
	StateBlocked:    {StateTodo, StateProposed, StateCancelled},
}

// AllowedNext lists the states a task in from may move to.
func AllowedNext(from State) []State {
	return append([]State(nil), transitions[from]...)
}

// TransitionError reports a state change the table does not allow.
type TransitionError struct {
	ID       string
	From, To State
	Allowed  []State
}

func (e *TransitionError) Error() string {
	if len(e.Allowed) == 0 {
		return fmt.Sprintf("invalid transition: %s cannot go from %s to %s; %s is final", e.ID, e.From, e.To, e.From)
	}
	names := make([]string, len(e.Allowed))
	for i, st := range e.Allowed {
		names[i] = string(st)
	}
	return fmt.Sprintf("invalid transition: %s cannot go from %s to %s; allowed: %s", e.ID, e.From, e.To, strings.Join(names, ", "))
}

func (e *TransitionError) Is(target error) bool { return target == ErrInvalidTransition }

func reasonRequired(to State) bool {
	return to == StateBlocked || to == StateProposed || to == StateCancelled
}

func moveNoteKind(to State) NoteKind {
	switch to {
	case StateBlocked:
		return NoteBlocked
	case StateProposed:
		return NoteKickback
	}
	return NoteGeneral
}

// SetState moves a task to a new state if its fingerprint is still expected.
// A move to BLOCKED, PROPOSED or CANCELLED needs a reason; any other move
// takes one optionally. A reason is recorded as a note in the same transaction.
func (s *Store) SetState(ctx context.Context, id, expected string, to State, reason string) (Unit, error) {
	err := s.db.CheckedWrite(ctx, id, expected, currentFingerprint(ctx, id), func(tx *sql.Tx) error {
		u, err := getTx(ctx, tx, id)
		if err != nil {
			return err
		}
		if u.Level != LevelTask {
			return fmt.Errorf("%w: only tasks have a state, and %s is a %s", ErrInvalid, id, u.Level)
		}
		if !to.valid() {
			return fmt.Errorf("%w: %q is not a task state", ErrInvalid, to)
		}
		if to == u.State {
			return fmt.Errorf("%w: %s is already %s", ErrInvalid, id, to)
		}
		allowed := AllowedNext(u.State)
		ok := false
		for _, st := range allowed {
			ok = ok || st == to
		}
		if !ok {
			return &TransitionError{ID: id, From: u.State, To: to, Allowed: allowed}
		}
		if to == StateTodo {
			if missing := Readiness(u); len(missing) > 0 {
				return &NotReadyError{ID: id, Missing: missing}
			}
		}
		blank := strings.TrimSpace(reason) == ""
		if blank && reasonRequired(to) {
			return &ReasonRequiredError{ID: id, From: u.State, To: to}
		}
		if to == StateInProgress && u.ParentID != "" {
			waiting, err := waitingIn(ctx, tx, u.ParentID)
			if err != nil {
				return err
			}
			if len(waiting) > 0 {
				return &WaitingError{ID: id, Slice: u.ParentID, Waiting: waiting}
			}
		}
		now := s.stamp()
		if _, err := tx.ExecContext(ctx, "UPDATE units SET state = ?, updated_at = ?, updated_by = ? WHERE id = ?", string(to), now, ActorFrom(ctx), id); err != nil {
			return fmt.Errorf("units: set state of %s: %w", id, err)
		}
		if !blank {
			return insertNote(ctx, tx, id, moveNoteKind(to), fmt.Sprintf("%s to %s: %s", u.State, to, reason), now)
		}
		return nil
	})
	if err != nil {
		return Unit{}, err
	}
	return s.Get(ctx, id)
}
