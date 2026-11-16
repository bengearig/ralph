package units

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/bengearig/ralph/internal/db"
)

const columns = "id, level, number, parent_id, position, title, body, acceptance_criteria, state, created_at, updated_at"

type Store struct {
	// Now supplies the time stamped on writes.
	Now func() time.Time
	db  *db.DB
}

func New(d *db.DB) *Store {
	return &Store{Now: time.Now, db: d}
}

func (s *Store) stamp() string {
	return s.Now().UTC().Format(time.RFC3339Nano)
}

func validate(n NewUnit) error {
	if !n.Level.valid() {
		return fmt.Errorf("%w: level %q is not brief, slice or task", ErrInvalid, n.Level)
	}
	if strings.TrimSpace(n.Title) == "" {
		return fmt.Errorf("%w: title is empty", ErrInvalid)
	}
	if n.Level == LevelBrief && n.Body == "" {
		return fmt.Errorf("%w: body of a brief is empty", ErrInvalid)
	}
	if n.Level != LevelTask && n.AcceptanceCriteria != "" {
		return fmt.Errorf("%w: acceptance criteria belong to tasks, not to a %s", ErrInvalid, n.Level)
	}
	if n.Position < 0 {
		return fmt.Errorf("%w: position %d is negative", ErrInvalid, n.Position)
	}
	if n.ParentID == "" && n.Position != 0 {
		return fmt.Errorf("%w: position %d given for a unit with no parent; it must be 0", ErrInvalid, n.Position)
	}
	return nil
}

func wantParent(l Level) (Level, bool) {
	switch l {
	case LevelSlice:
		return LevelBrief, true
	case LevelTask:
		return LevelSlice, false
	}
	return "", false
}

// Create adds a unit; a task starts in PROPOSED.
func (s *Store) Create(ctx context.Context, n NewUnit) (Unit, error) {
	if err := validate(n); err != nil {
		return Unit{}, err
	}
	want, required := wantParent(n.Level)
	switch {
	case n.Level == LevelBrief && n.ParentID != "":
		return Unit{}, fmt.Errorf("%w: a brief has no parent, got %q", ErrInvalidParent, n.ParentID)
	case required && n.ParentID == "":
		return Unit{}, fmt.Errorf("%w: a %s needs a %s as its parent", ErrInvalidParent, n.Level, want)
	}

	var id string
	err := s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		if n.ParentID != "" {
			var level string
			err := tx.QueryRowContext(ctx, "SELECT level FROM units WHERE id = ?", n.ParentID).Scan(&level)
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("%w: parent %q", ErrNotFound, n.ParentID)
			}
			if err != nil {
				return fmt.Errorf("units: look up parent %q: %w", n.ParentID, err)
			}
			if Level(level) != want {
				return fmt.Errorf("%w: a %s needs a %s as its parent, but %q is a %s", ErrInvalidParent, n.Level, want, n.ParentID, level)
			}
		}

		var number int
		err := tx.QueryRowContext(ctx,
			"INSERT INTO counters (level, last) VALUES (?, 1) ON CONFLICT (level) DO UPDATE SET last = last + 1 RETURNING last",
			string(n.Level)).Scan(&number)
		if err != nil {
			return fmt.Errorf("units: allocate %s number: %w", n.Level, err)
		}
		id = n.Level.prefix() + "-" + strconv.Itoa(number)
		now := s.stamp()

		var parent any
		position := 0
		if n.ParentID != "" {
			parent = n.ParentID
			var last int
			if err := tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(position), 0) FROM units WHERE parent_id = ?", n.ParentID).Scan(&last); err != nil {
				return fmt.Errorf("units: find last sibling of %q: %w", n.ParentID, err)
			}
			position = last + 1
			if n.Position > 0 && n.Position < position {
				position = n.Position
				if _, err := tx.ExecContext(ctx,
					"UPDATE units SET position = position + 1, updated_at = ? WHERE parent_id = ? AND position >= ?",
					now, n.ParentID, position); err != nil {
					return fmt.Errorf("units: make room at position %d under %q: %w", position, n.ParentID, err)
				}
			}
		}

		var state any
		if n.Level == LevelTask {
			state = string(StateProposed)
		}
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO units ("+columns+") VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
			id, string(n.Level), number, parent, position, n.Title, n.Body, n.AcceptanceCriteria, state, now, now); err != nil {
			return fmt.Errorf("units: insert %s: %w", id, err)
		}
		return nil
	})
	if err != nil {
		return Unit{}, err
	}
	return s.Get(ctx, id)
}

type scanner interface{ Scan(dest ...any) error }

func scan(r scanner) (Unit, error) {
	var (
		u             Unit
		level         string
		parent, state sql.NullString
		created, upd  string
	)
	if err := r.Scan(&u.ID, &level, &u.Number, &parent, &u.Position, &u.Title, &u.Body, &u.AcceptanceCriteria, &state, &created, &upd); err != nil {
		return Unit{}, err
	}
	u.Level = Level(level)
	u.ParentID = parent.String
	u.State = State(state.String)
	var err error
	if u.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
		return Unit{}, fmt.Errorf("units: %s has bad created_at %q: %w", u.ID, created, err)
	}
	if u.UpdatedAt, err = time.Parse(time.RFC3339Nano, upd); err != nil {
		return Unit{}, fmt.Errorf("units: %s has bad updated_at %q: %w", u.ID, upd, err)
	}
	u.Fingerprint = fingerprint(u)
	return u, nil
}

// Get returns the unit with the given ID.
func (s *Store) Get(ctx context.Context, id string) (Unit, error) {
	u, err := scan(s.db.SQL().QueryRowContext(ctx, "SELECT "+columns+" FROM units WHERE id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return Unit{}, fmt.Errorf("%w: unit %q", ErrNotFound, id)
	}
	if err != nil {
		return Unit{}, fmt.Errorf("units: get %q: %w", id, err)
	}
	return u, nil
}

// List returns the matching units ordered by parent, position and number.
func (s *Store) List(ctx context.Context, f Filter) ([]Unit, error) {
	var (
		where []string
		args  []any
	)
	if f.Level != "" {
		if !f.Level.valid() {
			return nil, fmt.Errorf("%w: filter level %q is not brief, slice or task", ErrInvalid, f.Level)
		}
		where = append(where, "level = ?")
		args = append(args, string(f.Level))
	}
	if f.ParentID != nil {
		if *f.ParentID == "" {
			where = append(where, "parent_id IS NULL")
		} else {
			where = append(where, "parent_id = ?")
			args = append(args, *f.ParentID)
		}
	}
	if len(f.States) > 0 {
		marks := make([]string, len(f.States))
		for i, st := range f.States {
			if !st.valid() {
				return nil, fmt.Errorf("%w: filter state %q is not a task state", ErrInvalid, st)
			}
			marks[i] = "?"
			args = append(args, string(st))
		}
		where = append(where, "state IN ("+strings.Join(marks, ", ")+")")
	}
	q := "SELECT " + columns + " FROM units"
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY parent_id, position, number, level"
	rows, err := s.db.SQL().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("units: list: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Unit
	for rows.Next() {
		u, err := scan(rows)
		if err != nil {
			return nil, fmt.Errorf("units: list: %w", err)
		}
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("units: list: %w", err)
	}
	return out, nil
}
