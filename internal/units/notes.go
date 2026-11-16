package units

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

type NoteKind string

const (
	NoteGeneral  NoteKind = "NOTE"
	NoteKickback NoteKind = "KICKBACK"
	NoteBlocked  NoteKind = "BLOCKED"
)

func (k NoteKind) valid() bool {
	return k == NoteGeneral || k == NoteKickback || k == NoteBlocked
}

// Note is a remark on a unit. Notes are outside the fingerprint.
type Note struct {
	ID        int
	UnitID    string
	Kind      NoteKind
	Body      string
	Author    string
	CreatedAt time.Time
}

func insertNote(ctx context.Context, tx *sql.Tx, unitID string, kind NoteKind, body, now string) error {
	if _, err := tx.ExecContext(ctx,
		"INSERT INTO notes (unit_id, kind, body, author, created_at) VALUES (?, ?, ?, ?, ?)",
		unitID, string(kind), body, ActorFrom(ctx), now); err != nil {
		return fmt.Errorf("units: add note to %s: %w", unitID, err)
	}
	return nil
}

// AddNote records a note on a unit of any level.
func (s *Store) AddNote(ctx context.Context, unitID string, kind NoteKind, body string) (Note, error) {
	if !kind.valid() {
		return Note{}, fmt.Errorf("%w: note kind %q is not NOTE, KICKBACK or BLOCKED", ErrInvalid, kind)
	}
	if kind == NoteBlocked {
		return Note{}, fmt.Errorf("%w: a BLOCKED note is written by the state change to BLOCKED, not added directly", ErrInvalid)
	}
	if strings.TrimSpace(body) == "" {
		return Note{}, fmt.Errorf("%w: note body is empty", ErrInvalid)
	}
	var id int64
	err := s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		if err := unitExists(ctx, tx, unitID); err != nil {
			return err
		}
		if kind == NoteKickback {
			if err := checkKickbackTarget(ctx, tx, unitID); err != nil {
				return err
			}
		}
		if err := insertNote(ctx, tx, unitID, kind, body, s.stamp()); err != nil {
			return err
		}
		return tx.QueryRowContext(ctx, "SELECT last_insert_rowid()").Scan(&id)
	})
	if err != nil {
		return Note{}, err
	}
	return s.note(ctx, id)
}

func checkKickbackTarget(ctx context.Context, tx *sql.Tx, unitID string) error {
	u, err := getTx(ctx, tx, unitID)
	if err != nil {
		return err
	}
	if u.Level != LevelTask {
		return fmt.Errorf("%w: a KICKBACK note goes on a task, and %s is a %s", ErrInvalid, unitID, u.Level)
	}
	if u.State == StateDone || u.State == StateCancelled {
		return fmt.Errorf("%w: a KICKBACK note cannot go on %s, which is %s", ErrInvalid, unitID, u.State)
	}
	return nil
}

const noteColumns = "id, unit_id, kind, body, author, created_at"

func scanNote(r scanner) (Note, error) {
	var (
		n       Note
		kind    string
		created string
	)
	if err := r.Scan(&n.ID, &n.UnitID, &kind, &n.Body, &n.Author, &created); err != nil {
		return Note{}, err
	}
	n.Kind = NoteKind(kind)
	var err error
	if n.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
		return Note{}, fmt.Errorf("units: note %d has bad created_at %q: %w", n.ID, created, err)
	}
	return n, nil
}

func (s *Store) note(ctx context.Context, id int64) (Note, error) {
	n, err := scanNote(s.db.SQL().QueryRowContext(ctx, "SELECT "+noteColumns+" FROM notes WHERE id = ?", id))
	if err != nil {
		return Note{}, fmt.Errorf("units: read note %d: %w", id, err)
	}
	return n, nil
}

// Notes returns a unit's notes, oldest first.
func (s *Store) Notes(ctx context.Context, unitID string) ([]Note, error) {
	if err := unitExists(ctx, s.db.SQL(), unitID); err != nil {
		return nil, err
	}
	rows, err := s.db.SQL().QueryContext(ctx, "SELECT "+noteColumns+" FROM notes WHERE unit_id = ? ORDER BY id", unitID)
	if err != nil {
		return nil, fmt.Errorf("units: notes of %s: %w", unitID, err)
	}
	defer func() { _ = rows.Close() }()
	out := []Note{}
	for rows.Next() {
		n, err := scanNote(rows)
		if err != nil {
			return nil, fmt.Errorf("units: notes of %s: %w", unitID, err)
		}
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("units: notes of %s: %w", unitID, err)
	}
	return out, nil
}
