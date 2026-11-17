// Package tickets keeps the record of which unit has which ticket in a task manager and pushes units to it.
package tickets

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/bengearig/ralph/internal/db"
)

// Row is what ralph remembers about the ticket of one unit.
type Row struct {
	UnitID string
	System string
	Key    string
	// ID is the system's own identifier, which survives a change of key.
	ID string
	// Pushed* hold the hashes of the normalized title and body, and the status and parent key, last pushed.
	PushedTitle  string
	PushedBody   string
	PushedStatus string
	PushedParent string
	// MappedStatus is the status ralph last mapped for the unit and acted on; empty for a new or older row.
	MappedStatus string
	Scheduled    bool
	// LinkConfirmed is set once the unit's external link is written; a row without it was cut short by a crash.
	LinkConfirmed bool
	Drift         string
	DriftSeenAt   time.Time
	FailedStep    string
	FailedAt      time.Time
	LastError     string
	Attempts      int
	SyncedAt      time.Time
}

// Store reads and writes the ticket rows of a project database.
type Store struct {
	db *db.DB
}

// NewStore returns the store of d.
func NewStore(d *db.DB) *Store { return &Store{db: d} }

const rowColumns = "unit_id, system, ticket_key, ticket_id, pushed_title, pushed_body, pushed_status, pushed_parent, mapped_status, scheduled, link_confirmed, drift, drift_seen_at, failed_step, failed_at, last_error, attempts, synced_at"

const updateColumns = "system = excluded.system, ticket_key = excluded.ticket_key, ticket_id = excluded.ticket_id, pushed_title = excluded.pushed_title, " +
	"pushed_body = excluded.pushed_body, pushed_status = excluded.pushed_status, pushed_parent = excluded.pushed_parent, mapped_status = excluded.mapped_status, scheduled = excluded.scheduled, " +
	"link_confirmed = excluded.link_confirmed, drift = excluded.drift, drift_seen_at = excluded.drift_seen_at, failed_step = excluded.failed_step, " +
	"failed_at = excluded.failed_at, last_error = excluded.last_error, attempts = excluded.attempts, synced_at = excluded.synced_at"

type scanner interface{ Scan(dest ...any) error }

func scanRow(s scanner) (Row, error) {
	var (
		r                                Row
		scheduled, confirmed             int
		driftSeen, failedAt, syncedAtStr string
	)
	err := s.Scan(&r.UnitID, &r.System, &r.Key, &r.ID, &r.PushedTitle, &r.PushedBody, &r.PushedStatus, &r.PushedParent, &r.MappedStatus,
		&scheduled, &confirmed, &r.Drift, &driftSeen, &r.FailedStep, &failedAt, &r.LastError, &r.Attempts, &syncedAtStr)
	if err != nil {
		return Row{}, err
	}
	r.Scheduled, r.LinkConfirmed = scheduled != 0, confirmed != 0
	for _, f := range []struct {
		in  string
		out *time.Time
	}{{driftSeen, &r.DriftSeenAt}, {failedAt, &r.FailedAt}, {syncedAtStr, &r.SyncedAt}} {
		if f.in == "" {
			continue
		}
		t, err := time.Parse(time.RFC3339Nano, f.in)
		if err != nil {
			return Row{}, fmt.Errorf("read time %q: %w", f.in, err)
		}
		*f.out = t
	}
	return r, nil
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func flag(b bool) int {
	if b {
		return 1
	}
	return 0
}

// Get returns the row of a unit and whether it exists.
func (s *Store) Get(ctx context.Context, unitID string) (Row, bool, error) {
	r, err := scanRow(s.db.SQL().QueryRowContext(ctx, "SELECT "+rowColumns+" FROM tickets WHERE unit_id = ?", unitID))
	if errors.Is(err, sql.ErrNoRows) {
		return Row{}, false, nil
	}
	if err != nil {
		return Row{}, false, fmt.Errorf("tickets: get %s: %w", unitID, err)
	}
	return r, true, nil
}

// List returns every row ordered by unit number.
func (s *Store) List(ctx context.Context) ([]Row, error) {
	rows, err := s.db.SQL().QueryContext(ctx, "SELECT "+rowColumns+" FROM tickets ORDER BY CAST(substr(unit_id, 3) AS INTEGER), unit_id")
	if err != nil {
		return nil, fmt.Errorf("tickets: list: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Row
	for rows.Next() {
		r, err := scanRow(rows)
		if err != nil {
			return nil, fmt.Errorf("tickets: list: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("tickets: list: %w", err)
	}
	return out, nil
}

// Put inserts the row or replaces the one of the same unit; a ticket key another unit holds is an error.
func (s *Store) Put(ctx context.Context, r Row) error {
	err := s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "INSERT INTO tickets ("+rowColumns+") VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT (unit_id) DO UPDATE SET "+updateColumns,
			r.UnitID, r.System, r.Key, r.ID, r.PushedTitle, r.PushedBody, r.PushedStatus, r.PushedParent, r.MappedStatus,
			flag(r.Scheduled), flag(r.LinkConfirmed), r.Drift, formatTime(r.DriftSeenAt), r.FailedStep, formatTime(r.FailedAt),
			r.LastError, r.Attempts, formatTime(r.SyncedAt))
		return err
	})
	if err != nil {
		return fmt.Errorf("tickets: put %s: %w", r.UnitID, err)
	}
	return nil
}

// Delete removes the row of a unit; removing a missing one is not an error.
func (s *Store) Delete(ctx context.Context, unitID string) error {
	err := s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "DELETE FROM tickets WHERE unit_id = ?", unitID)
		return err
	})
	if err != nil {
		return fmt.Errorf("tickets: delete %s: %w", unitID, err)
	}
	return nil
}

// State returns a stored value, empty when there is none.
func (s *Store) State(ctx context.Context, key string) (string, error) {
	var v string
	err := s.db.SQL().QueryRowContext(ctx, "SELECT value FROM ticket_state WHERE key = ?", key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("tickets: read state %s: %w", key, err)
	}
	return v, nil
}

// SetState stores a value under key.
func (s *Store) SetState(ctx context.Context, key, value string) error {
	err := s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "INSERT INTO ticket_state (key, value) VALUES (?, ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value", key, value)
		return err
	})
	if err != nil {
		return fmt.Errorf("tickets: write state %s: %w", key, err)
	}
	return nil
}
