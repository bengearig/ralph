package documents

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// PurgeOptions says which earlier versions to remove. Keep protects the
// newest Keep earlier versions of each document; OlderThan makes only
// versions made more than that long ago candidates. At least one is required.
// Nothing is written unless Apply is true.
type PurgeOptions struct {
	Keep       *int
	OlderThan  time.Duration
	DocumentID string
	Apply      bool
}

// PurgeEntry is the earlier versions of one document that a purge removes,
// ascending, and the bytes of their bodies.
type PurgeEntry struct {
	ID       string
	Versions []int
	Bytes    int
}

// PurgeResult lists the documents with candidates, ordered by number, and
// the totals.
type PurgeResult struct {
	Entries  []PurgeEntry
	Versions int
	Bytes    int
}

type queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Purge selects earlier versions by opts and, when opts.Apply is set, deletes
// them in one transaction. A document's current content is never touched.
func (s *Store) Purge(ctx context.Context, opts PurgeOptions) (PurgeResult, error) {
	if opts.Keep == nil && opts.OlderThan == 0 {
		return PurgeResult{}, fmt.Errorf("%w: say what to purge: give --keep or --older-than", ErrInvalid)
	}
	if opts.Keep != nil && *opts.Keep < 0 {
		return PurgeResult{}, fmt.Errorf("%w: keep is %d, it must be 0 or more", ErrInvalid, *opts.Keep)
	}
	if opts.OlderThan < 0 {
		return PurgeResult{}, fmt.Errorf("%w: older-than is %s, it must be greater than zero", ErrInvalid, opts.OlderThan)
	}
	if opts.DocumentID != "" {
		if err := parseID(opts.DocumentID); err != nil {
			return PurgeResult{}, err
		}
	}
	if !opts.Apply {
		return s.selectPurge(ctx, s.db.SQL(), opts)
	}
	var res PurgeResult
	err := s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		var err error
		if res, err = s.selectPurge(ctx, tx, opts); err != nil {
			return err
		}
		for _, e := range res.Entries {
			for _, v := range e.Versions {
				if _, err := tx.ExecContext(ctx, "DELETE FROM document_versions WHERE document_id = ? AND version = ?", e.ID, v); err != nil {
					return fmt.Errorf("documents: purge version %d of %s: %w", v, e.ID, err)
				}
			}
		}
		return nil
	})
	if err != nil {
		return PurgeResult{}, err
	}
	return res, nil
}

func (s *Store) selectPurge(ctx context.Context, q queryer, opts PurgeOptions) (PurgeResult, error) {
	query := "SELECT v.document_id, v.version, v.created_at, LENGTH(CAST(v.body AS BLOB)) FROM document_versions v JOIN documents d ON d.id = v.document_id"
	var args []any
	if opts.DocumentID != "" {
		var one int
		err := q.QueryRowContext(ctx, "SELECT 1 FROM documents WHERE id = ?", opts.DocumentID).Scan(&one)
		if errors.Is(err, sql.ErrNoRows) {
			return PurgeResult{}, fmt.Errorf("%w: document %q", ErrNotFound, opts.DocumentID)
		}
		if err != nil {
			return PurgeResult{}, fmt.Errorf("documents: look up %s: %w", opts.DocumentID, err)
		}
		query += " WHERE v.document_id = ?"
		args = append(args, opts.DocumentID)
	}
	query += " ORDER BY d.number, v.version"
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return PurgeResult{}, fmt.Errorf("documents: select versions to purge: %w", err)
	}
	defer func() { _ = rows.Close() }()

	type version struct {
		number  int
		created time.Time
		size    int
	}
	var (
		ids  []string
		byID = map[string][]version{}
	)
	for rows.Next() {
		var (
			id, created string
			v           version
		)
		if err := rows.Scan(&id, &v.number, &created, &v.size); err != nil {
			return PurgeResult{}, fmt.Errorf("documents: select versions to purge: %w", err)
		}
		if v.created, err = time.Parse(time.RFC3339Nano, created); err != nil {
			return PurgeResult{}, fmt.Errorf("documents: %s version %d has bad created_at %q: %w", id, v.number, created, err)
		}
		if _, seen := byID[id]; !seen {
			ids = append(ids, id)
		}
		byID[id] = append(byID[id], v)
	}
	if err := rows.Err(); err != nil {
		return PurgeResult{}, fmt.Errorf("documents: select versions to purge: %w", err)
	}

	cutoff := s.Now().Add(-opts.OlderThan)
	res := PurgeResult{}
	for _, id := range ids {
		vs := byID[id]
		if opts.Keep != nil {
			vs = vs[:max(len(vs)-*opts.Keep, 0)]
		}
		e := PurgeEntry{ID: id}
		for _, v := range vs {
			if opts.OlderThan > 0 && !v.created.Before(cutoff) {
				continue
			}
			e.Versions = append(e.Versions, v.number)
			e.Bytes += v.size
		}
		if len(e.Versions) == 0 {
			continue
		}
		res.Entries = append(res.Entries, e)
		res.Versions += len(e.Versions)
		res.Bytes += e.Bytes
	}
	return res, nil
}
