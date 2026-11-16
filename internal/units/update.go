package units

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// Change lists the fields Update sets; a nil field is left alone.
type Change struct {
	Title              *string
	Body               *string
	AcceptanceCriteria *string
	Position           *int
}

func getTx(ctx context.Context, tx *sql.Tx, id string) (Unit, error) {
	u, err := scan(tx.QueryRowContext(ctx, "SELECT "+columns+" FROM units WHERE id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return Unit{}, fmt.Errorf("%w: unit %q", ErrNotFound, id)
	}
	if err != nil {
		return Unit{}, fmt.Errorf("units: get %q: %w", id, err)
	}
	return u, nil
}

func currentFingerprint(ctx context.Context, id string) func(*sql.Tx) (string, error) {
	return func(tx *sql.Tx) (string, error) {
		u, err := getTx(ctx, tx, id)
		if err != nil {
			return "", err
		}
		return u.Fingerprint, nil
	}
}

func finished(u Unit) bool {
	return u.State == StateDone || u.State == StateCancelled
}

func validateChange(u Unit, c Change) error {
	if c.Title != nil && strings.TrimSpace(*c.Title) == "" {
		return fmt.Errorf("%w: title is empty", ErrInvalid)
	}
	if c.Body != nil && u.Level == LevelBrief {
		return fmt.Errorf("%w: the body of brief %s is the instruction kept verbatim and cannot be changed", ErrInvalid, u.ID)
	}
	if c.AcceptanceCriteria != nil && u.Level != LevelTask {
		return fmt.Errorf("%w: acceptance criteria belong to tasks, not to a %s", ErrInvalid, u.Level)
	}
	if u.Level == LevelTask && finished(u) {
		return fmt.Errorf("%w: task %s is %s and cannot be edited", ErrInvalid, u.ID, u.State)
	}
	if c.Position != nil {
		switch {
		case *c.Position < 0:
			return fmt.Errorf("%w: position %d is negative", ErrInvalid, *c.Position)
		case u.ParentID == "" && *c.Position != 0:
			return fmt.Errorf("%w: position %d given for a unit with no parent; it must be 0", ErrInvalid, *c.Position)
		case u.ParentID != "" && *c.Position == 0:
			return fmt.Errorf("%w: positions under a parent start at 1", ErrInvalid)
		}
	}
	return nil
}

// Update applies c to a unit if its fingerprint is still expected. Moving a
// unit to a position shifts its siblings so positions stay 1, 2, 3 and so on;
// a position past the last sibling moves it to the end.
func (s *Store) Update(ctx context.Context, id, expected string, c Change) (Unit, error) {
	err := s.db.CheckedWrite(ctx, id, expected, currentFingerprint(ctx, id), func(tx *sql.Tx) error {
		u, err := getTx(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := validateChange(u, c); err != nil {
			return err
		}
		if u.Level == LevelTask && (u.State == StateTodo || u.State == StateInProgress) {
			edited := u
			if c.Body != nil {
				edited.Body = *c.Body
			}
			if c.AcceptanceCriteria != nil {
				edited.AcceptanceCriteria = *c.AcceptanceCriteria
			}
			if missing := Readiness(edited); len(missing) > 0 {
				return &NotReadyError{ID: id, Missing: missing, Editing: true}
			}
		}
		now := s.stamp()
		if c.Position != nil && u.ParentID != "" {
			if err := move(ctx, tx, u, *c.Position, now); err != nil {
				return err
			}
		}
		title, body, criteria := u.Title, u.Body, u.AcceptanceCriteria
		if c.Title != nil {
			title = *c.Title
		}
		if c.Body != nil {
			body = *c.Body
		}
		if c.AcceptanceCriteria != nil {
			criteria = *c.AcceptanceCriteria
		}
		if _, err := tx.ExecContext(ctx,
			"UPDATE units SET title = ?, body = ?, acceptance_criteria = ?, updated_at = ?, updated_by = ? WHERE id = ?",
			title, body, criteria, now, ActorFrom(ctx), id); err != nil {
			return fmt.Errorf("units: update %s: %w", id, err)
		}
		return nil
	})
	if err != nil {
		return Unit{}, err
	}
	return s.Get(ctx, id)
}

// move places u at position among its siblings, closing the gap it leaves.
func move(ctx context.Context, tx *sql.Tx, u Unit, position int, now string) error {
	var last int
	if err := tx.QueryRowContext(ctx, "SELECT MAX(position) FROM units WHERE parent_id = ?", u.ParentID).Scan(&last); err != nil {
		return fmt.Errorf("units: find last sibling of %q: %w", u.ParentID, err)
	}
	position = min(position, last)
	if position == u.Position {
		return nil
	}
	query, args := "", []any{now, ActorFrom(ctx), u.ParentID, position, u.Position, u.ID}
	if position < u.Position {
		query = "UPDATE units SET position = position + 1, updated_at = ?, updated_by = ? WHERE parent_id = ? AND position >= ? AND position < ? AND id <> ?"
	} else {
		query = "UPDATE units SET position = position - 1, updated_at = ?, updated_by = ? WHERE parent_id = ? AND position <= ? AND position > ? AND id <> ?"
	}
	if _, err := tx.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("units: shift siblings of %s: %w", u.ID, err)
	}
	if _, err := tx.ExecContext(ctx, "UPDATE units SET position = ? WHERE id = ?", position, u.ID); err != nil {
		return fmt.Errorf("units: move %s to position %d: %w", u.ID, position, err)
	}
	return nil
}
