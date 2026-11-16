package units

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Status is a task's state, or for a slice or brief the status derived from
// its children.
type Status string

const (
	StatusProposed     Status = Status(StateProposed)
	StatusTodo         Status = Status(StateTodo)
	StatusInProgress   Status = Status(StateInProgress)
	StatusBlocked      Status = Status(StateBlocked)
	StatusCancelled    Status = Status(StateCancelled)
	StatusDone         Status = Status(StateDone)
	StatusEmpty        Status = "EMPTY"
	StatusArticulating Status = "ARTICULATING"
	StatusReady        Status = "READY"
)

// DeriveStatus derives the status of a slice from its tasks' statuses or of a
// brief from its slices' statuses. It is not used for tasks.
func DeriveStatus(level Level, children []Status) Status {
	var live []Status
	cancelled := false
	for _, c := range children {
		if c == StatusCancelled {
			cancelled = true
			continue
		}
		live = append(live, c)
	}
	if len(live) == 0 {
		if cancelled {
			return StatusCancelled
		}
		return StatusEmpty
	}
	has := func(want ...Status) bool {
		for _, c := range live {
			for _, w := range want {
				if c == w {
					return true
				}
			}
		}
		return false
	}
	if has(StatusBlocked) {
		return StatusBlocked
	}
	done := true
	for _, c := range live {
		if c != StatusDone {
			done = false
			break
		}
	}
	if done {
		return StatusDone
	}
	if has(StatusInProgress) {
		return StatusInProgress
	}
	if level == LevelBrief {
		if has(StatusArticulating, StatusEmpty) {
			return StatusArticulating
		}
	} else if has(StatusProposed) {
		return StatusArticulating
	}
	return StatusReady
}

// Status returns a task's own state, or the status derived from a slice's
// tasks or a brief's slices.
func (s *Store) Status(ctx context.Context, id string) (Status, error) {
	got, err := s.Statuses(ctx, []string{id})
	if err != nil {
		return "", err
	}
	return got[id], nil
}

// Statuses returns the status of each unit in one read transaction. An
// unknown ID fails the whole call with ErrNotFound.
func (s *Store) Statuses(ctx context.Context, ids []string) (map[string]Status, error) {
	tx, err := s.db.SQL().BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("units: status: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	out := make(map[string]Status, len(ids))
	for _, id := range ids {
		if _, ok := out[id]; ok {
			continue
		}
		st, err := statusIn(ctx, tx, id, out)
		if err != nil {
			return nil, err
		}
		out[id] = st
	}
	return out, nil
}

// statusIn reuses and fills memo with the statuses of slices met on the way.
func statusIn(ctx context.Context, tx *sql.Tx, id string, memo map[string]Status) (Status, error) {
	var level string
	var state sql.NullString
	err := tx.QueryRowContext(ctx, "SELECT level, state FROM units WHERE id = ?", id).Scan(&level, &state)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("%w: unit %q", ErrNotFound, id)
	}
	if err != nil {
		return "", fmt.Errorf("units: status of %q: %w", id, err)
	}
	if Level(level) == LevelTask {
		return Status(state.String), nil
	}
	rows, err := tx.QueryContext(ctx, "SELECT id, state FROM units WHERE parent_id = ? ORDER BY position, number", id)
	if err != nil {
		return "", fmt.Errorf("units: status of %q: %w", id, err)
	}
	type child struct {
		id    string
		state sql.NullString
	}
	var kids []child
	for rows.Next() {
		var c child
		if err := rows.Scan(&c.id, &c.state); err != nil {
			_ = rows.Close()
			return "", fmt.Errorf("units: status of %q: %w", id, err)
		}
		kids = append(kids, c)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return "", fmt.Errorf("units: status of %q: %w", id, err)
	}
	if err := rows.Close(); err != nil {
		return "", fmt.Errorf("units: status of %q: %w", id, err)
	}
	statuses := make([]Status, len(kids))
	for i, k := range kids {
		if Level(level) == LevelSlice {
			statuses[i] = Status(k.state.String)
			continue
		}
		st, ok := memo[k.id]
		if !ok {
			if st, err = statusIn(ctx, tx, k.id, memo); err != nil {
				return "", err
			}
			memo[k.id] = st
		}
		statuses[i] = st
	}
	return DeriveStatus(Level(level), statuses), nil
}
