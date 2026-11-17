package units

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// PullRequestState is where a pull request stands on the host.
type PullRequestState string

const (
	PullRequestOpen   PullRequestState = "open"
	PullRequestClosed PullRequestState = "closed"
	PullRequestMerged PullRequestState = "merged"
)

func (s PullRequestState) valid() bool {
	return s == PullRequestOpen || s == PullRequestClosed || s == PullRequestMerged
}

// PullRequestRecord is a pull request ralph opened for a slice. Resolution is
// empty until a decision is recorded after the pull request was closed.
type PullRequestRecord struct {
	SliceID    string
	Number     int
	URL        string
	Head, Base string
	State      PullRequestState
	Resolution string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// RecordPullRequest inserts the pull request, or for an existing slice and
// number updates its URL, branches and state, keeping created_at and
// resolution. The times in rec are ignored.
func (s *Store) RecordPullRequest(ctx context.Context, rec PullRequestRecord) error {
	switch {
	case rec.Number <= 0:
		return fmt.Errorf("%w: pull request number %d must be above 0", ErrInvalid, rec.Number)
	case strings.TrimSpace(rec.URL) == "":
		return fmt.Errorf("%w: pull request URL is empty", ErrInvalid)
	case strings.TrimSpace(rec.Head) == "":
		return fmt.Errorf("%w: pull request head branch is empty", ErrInvalid)
	case strings.TrimSpace(rec.Base) == "":
		return fmt.Errorf("%w: pull request base branch is empty", ErrInvalid)
	case !rec.State.valid():
		return fmt.Errorf("%w: pull request state %q is not open, closed or merged", ErrInvalid, rec.State)
	}
	return s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		if err := sliceIn(ctx, tx, rec.SliceID, "unit"); err != nil {
			return err
		}
		now := s.stamp()
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO pull_requests (slice_id, number, url, head, base, state, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			 ON CONFLICT (slice_id, number) DO UPDATE SET
			 	url = excluded.url, head = excluded.head, base = excluded.base,
			 	state = excluded.state, updated_at = excluded.updated_at`,
			rec.SliceID, rec.Number, rec.URL, rec.Head, rec.Base, string(rec.State), now, now); err != nil {
			return fmt.Errorf("units: record pull request %d of %s: %w", rec.Number, rec.SliceID, err)
		}
		return nil
	})
}

// PullRequests returns the pull requests recorded for a slice, oldest first
// by creation and then by number.
func (s *Store) PullRequests(ctx context.Context, sliceID string) ([]PullRequestRecord, error) {
	tx, err := s.db.SQL().BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("units: pull requests of %s: %w", sliceID, err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := sliceIn(ctx, tx, sliceID, "unit"); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx,
		"SELECT number, url, head, base, state, resolution, created_at, updated_at FROM pull_requests WHERE slice_id = ?", sliceID)
	if err != nil {
		return nil, fmt.Errorf("units: pull requests of %s: %w", sliceID, err)
	}
	defer func() { _ = rows.Close() }()
	out := []PullRequestRecord{}
	for rows.Next() {
		rec := PullRequestRecord{SliceID: sliceID}
		var state, created, updated string
		if err := rows.Scan(&rec.Number, &rec.URL, &rec.Head, &rec.Base, &state, &rec.Resolution, &created, &updated); err != nil {
			return nil, fmt.Errorf("units: pull requests of %s: %w", sliceID, err)
		}
		rec.State = PullRequestState(state)
		if rec.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
			return nil, fmt.Errorf("units: pull request %d of %s has bad created_at %q: %w", rec.Number, sliceID, created, err)
		}
		if rec.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated); err != nil {
			return nil, fmt.Errorf("units: pull request %d of %s has bad updated_at %q: %w", rec.Number, sliceID, updated, err)
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("units: pull requests of %s: %w", sliceID, err)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].Number < out[j].Number
	})
	return out, nil
}

// SetPullRequestState records the state the host reports for a pull request.
// Setting the state it already has changes nothing.
func (s *Store) SetPullRequestState(ctx context.Context, sliceID string, number int, state PullRequestState) error {
	if !state.valid() {
		return fmt.Errorf("%w: pull request state %q is not open, closed or merged", ErrInvalid, state)
	}
	return s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		var current string
		err := tx.QueryRowContext(ctx,
			"SELECT state FROM pull_requests WHERE slice_id = ? AND number = ?", sliceID, number).Scan(&current)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: pull request %d of %s", ErrNotFound, number, sliceID)
		}
		if err != nil {
			return fmt.Errorf("units: pull request %d of %s: %w", number, sliceID, err)
		}
		if current == string(state) {
			return nil
		}
		if _, err := tx.ExecContext(ctx,
			"UPDATE pull_requests SET state = ?, updated_at = ? WHERE slice_id = ? AND number = ?",
			string(state), s.stamp(), sliceID, number); err != nil {
			return fmt.Errorf("units: set state of pull request %d of %s: %w", number, sliceID, err)
		}
		return nil
	})
}

// ResolvePullRequest records the decision taken after a pull request was
// closed without merging. The record must be closed and not yet resolved.
func (s *Store) ResolvePullRequest(ctx context.Context, sliceID string, number int, resolution string) error {
	switch resolution {
	case "reopened", "rearticulated", "cancelled":
	default:
		return fmt.Errorf("%w: pull request resolution %q is not reopened, rearticulated or cancelled", ErrInvalid, resolution)
	}
	return s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		var state, current string
		err := tx.QueryRowContext(ctx,
			"SELECT state, resolution FROM pull_requests WHERE slice_id = ? AND number = ?", sliceID, number).Scan(&state, &current)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: pull request %d of %s", ErrNotFound, number, sliceID)
		}
		if err != nil {
			return fmt.Errorf("units: pull request %d of %s: %w", number, sliceID, err)
		}
		if state != string(PullRequestClosed) {
			return fmt.Errorf("%w: pull request %d of %s is %s, and only a closed pull request can be resolved", ErrInvalid, number, sliceID, state)
		}
		if current != "" {
			return fmt.Errorf("%w: pull request %d of %s is already resolved as %s", ErrInvalid, number, sliceID, current)
		}
		if _, err := tx.ExecContext(ctx,
			"UPDATE pull_requests SET resolution = ?, updated_at = ? WHERE slice_id = ? AND number = ?",
			resolution, s.stamp(), sliceID, number); err != nil {
			return fmt.Errorf("units: resolve pull request %d of %s: %w", number, sliceID, err)
		}
		return nil
	})
}
