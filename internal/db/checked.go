package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

var (
	ErrStale    = errors.New("record changed since it was read")
	ErrNotFound = errors.New("record not found")
)

// StaleError reports a write based on a read that another writer has outdated.
type StaleError struct {
	Record, Expected, Found string
}

func (e *StaleError) Error() string {
	return fmt.Sprintf("record %q changed since it was read (expected fingerprint %s, found %s): re-read it, reapply your change and try again",
		e.Record, short(e.Expected), short(e.Found))
}

func (e *StaleError) Is(target error) bool { return target == ErrStale }

func short(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

// CheckedWrite runs write only if current, evaluated in the same write
// transaction, still returns expected. A missing record is reported by current
// returning ErrNotFound.
func (d *DB) CheckedWrite(ctx context.Context, record, expected string, current func(*sql.Tx) (string, error), write func(*sql.Tx) error) error {
	if expected == "" {
		return fmt.Errorf("db: checked write of %q needs the fingerprint it was read with", record)
	}
	return d.WriteTx(ctx, func(tx *sql.Tx) error {
		found, err := current(tx)
		if err != nil {
			return err
		}
		if found != expected {
			return &StaleError{Record: record, Expected: expected, Found: found}
		}
		return write(tx)
	})
}
