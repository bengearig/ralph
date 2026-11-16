package units

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/bengearig/ralph/internal/db"
)

type LinkKind string

const (
	LinkDocument LinkKind = "document"
	LinkExternal LinkKind = "external"
)

const maxTargetLen = 512

// Link ties a unit to a document or to an item in an outside system. Its
// target is never checked against anything.
type Link struct {
	Kind      LinkKind
	Target    string
	CreatedAt time.Time
}

func (k LinkKind) valid() bool {
	return k == LinkDocument || k == LinkExternal
}

func validateLink(kind LinkKind, target string) error {
	if !kind.valid() {
		return fmt.Errorf("%w: link kind %q is not document or external", ErrInvalid, kind)
	}
	if n := utf8.RuneCountInString(target); n < 1 || n > maxTargetLen {
		return fmt.Errorf("%w: link target has %d characters, want 1 to %d", ErrInvalid, n, maxTargetLen)
	}
	for _, r := range target {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return fmt.Errorf("%w: link target %q has whitespace or a control character", ErrInvalid, target)
		}
	}
	return nil
}

func unitExists(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, id string) error {
	var one int
	err := q.QueryRowContext(ctx, "SELECT 1 FROM units WHERE id = ?", id).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: unit %q", db.ErrNotFound, id)
	}
	if err != nil {
		return fmt.Errorf("units: look up %q: %w", id, err)
	}
	return nil
}

// AddLink adds a link; adding one that exists changes nothing.
func (s *Store) AddLink(ctx context.Context, unitID string, kind LinkKind, target string) error {
	if err := validateLink(kind, target); err != nil {
		return err
	}
	return s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		if err := unitExists(ctx, tx, unitID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO links (unit_id, kind, target, created_at) VALUES (?, ?, ?, ?) ON CONFLICT DO NOTHING",
			unitID, string(kind), target, s.stamp()); err != nil {
			return fmt.Errorf("units: add link to %s: %w", unitID, err)
		}
		return nil
	})
}

// RemoveLink removes a link; removing one that does not exist is not an error.
func (s *Store) RemoveLink(ctx context.Context, unitID string, kind LinkKind, target string) error {
	return s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		if err := unitExists(ctx, tx, unitID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			"DELETE FROM links WHERE unit_id = ? AND kind = ? AND target = ?",
			unitID, string(kind), target); err != nil {
			return fmt.Errorf("units: remove link from %s: %w", unitID, err)
		}
		return nil
	})
}

// Links returns a unit's links ordered by kind, then target.
func (s *Store) Links(ctx context.Context, unitID string) ([]Link, error) {
	if err := unitExists(ctx, s.db.SQL(), unitID); err != nil {
		return nil, err
	}
	rows, err := s.db.SQL().QueryContext(ctx,
		"SELECT kind, target, created_at FROM links WHERE unit_id = ? ORDER BY kind, target", unitID)
	if err != nil {
		return nil, fmt.Errorf("units: links of %s: %w", unitID, err)
	}
	defer func() { _ = rows.Close() }()
	out := []Link{}
	for rows.Next() {
		var (
			l       Link
			kind    string
			created string
		)
		if err := rows.Scan(&kind, &l.Target, &created); err != nil {
			return nil, fmt.Errorf("units: links of %s: %w", unitID, err)
		}
		l.Kind = LinkKind(kind)
		if l.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
			return nil, fmt.Errorf("units: link of %s has bad created_at %q: %w", unitID, created, err)
		}
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("units: links of %s: %w", unitID, err)
	}
	return out, nil
}
