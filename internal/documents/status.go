package documents

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/bengearig/ralph/internal/units"
)

// transition runs change inside a checked write on id. change returns the new
// status and superseded-by value ("" for none) for the document it is given.
func (s *Store) transition(ctx context.Context, id, expected string, change func(tx *sql.Tx, d Document) (Status, string, error)) (Document, error) {
	if err := parseID(id); err != nil {
		return Document{}, err
	}
	err := s.db.CheckedWrite(ctx, id, expected, func(tx *sql.Tx) (string, error) {
		d, err := getTx(ctx, tx, id)
		if err != nil {
			return "", err
		}
		return d.Fingerprint, nil
	}, func(tx *sql.Tx) error {
		d, err := getTx(ctx, tx, id)
		if err != nil {
			return err
		}
		status, by, err := change(tx, d)
		if err != nil {
			return err
		}
		var supersededBy any
		if by != "" {
			supersededBy = by
		}
		if _, err := tx.ExecContext(ctx,
			"UPDATE documents SET status = ?, superseded_by = ?, updated_at = ?, updated_by = ? WHERE id = ?",
			string(status), supersededBy, s.stamp(), units.ActorFrom(ctx), id); err != nil {
			return fmt.Errorf("documents: set status of %s: %w", id, err)
		}
		return nil
	})
	if err != nil {
		return Document{}, err
	}
	return s.Get(ctx, id)
}

// Archive moves an ACTIVE document to ARCHIVED.
func (s *Store) Archive(ctx context.Context, id, expected string) (Document, error) {
	return s.transition(ctx, id, expected, func(_ *sql.Tx, d Document) (Status, string, error) {
		if err := requireActive(d, "archived"); err != nil {
			return "", "", err
		}
		return StatusArchived, "", nil
	})
}

// Supersede moves an ACTIVE document to SUPERSEDED, pointing at the ACTIVE
// document that replaces it. The replacement is read in the write transaction.
func (s *Store) Supersede(ctx context.Context, id, expected, supersededBy string) (Document, error) {
	if supersededBy == "" {
		return Document{}, fmt.Errorf("%w: a replacement is required to supersede %s", ErrInvalid, id)
	}
	if err := parseID(supersededBy); err != nil {
		return Document{}, err
	}
	if supersededBy == id {
		return Document{}, fmt.Errorf("%w: document %s cannot be its own replacement", ErrInvalid, id)
	}
	return s.transition(ctx, id, expected, func(tx *sql.Tx, d Document) (Status, string, error) {
		if err := requireActive(d, "superseded"); err != nil {
			return "", "", err
		}
		r, err := getTx(ctx, tx, supersededBy)
		if err != nil {
			return "", "", err
		}
		if r.Status != StatusActive {
			return "", "", fmt.Errorf("%w: replacement %s is %s, but a replacement must be ACTIVE", ErrInvalid, r.ID, r.Status)
		}
		return StatusSuperseded, r.ID, nil
	})
}

// Restore moves an ARCHIVED or SUPERSEDED document back to ACTIVE and clears
// its replacement.
func (s *Store) Restore(ctx context.Context, id, expected string) (Document, error) {
	return s.transition(ctx, id, expected, func(_ *sql.Tx, d Document) (Status, string, error) {
		if d.Status == StatusActive {
			return "", "", fmt.Errorf("%w: document %s is already active", ErrInvalid, d.ID)
		}
		return StatusActive, "", nil
	})
}

func requireActive(d Document, verb string) error {
	if d.Status == StatusActive {
		return nil
	}
	return fmt.Errorf("%w: document %s is %s and cannot be %s; restore it first", ErrInvalid, d.ID, d.Status, verb)
}
