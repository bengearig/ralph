package documents

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Versions lists the earlier versions of a document, oldest first.
func (s *Store) Versions(ctx context.Context, id string) ([]VersionInfo, error) {
	if _, err := s.Get(ctx, id); err != nil {
		return nil, err
	}
	rows, err := s.db.SQL().QueryContext(ctx,
		"SELECT version, kind, title, LENGTH(CAST(body AS BLOB)), created_at, created_by FROM document_versions WHERE document_id = ? ORDER BY version", id)
	if err != nil {
		return nil, fmt.Errorf("documents: list versions of %s: %w", id, err)
	}
	defer func() { _ = rows.Close() }()
	out := []VersionInfo{}
	for rows.Next() {
		var (
			v       VersionInfo
			kind    string
			created string
		)
		if err := rows.Scan(&v.Version, &kind, &v.Title, &v.Size, &created, &v.CreatedBy); err != nil {
			return nil, fmt.Errorf("documents: list versions of %s: %w", id, err)
		}
		v.Kind = Kind(kind)
		if v.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
			return nil, fmt.Errorf("documents: %s version %d has bad created_at %q: %w", id, v.Version, created, err)
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("documents: list versions of %s: %w", id, err)
	}
	return out, nil
}

// Version returns version n of a document.
func (s *Store) Version(ctx context.Context, id string, n int) (DocumentVersion, error) {
	if err := parseID(id); err != nil {
		return DocumentVersion{}, err
	}
	if n < 1 {
		return DocumentVersion{}, fmt.Errorf("%w: version %d is not a version number; they start at 1", ErrInvalid, n)
	}
	d, err := s.Get(ctx, id)
	if err != nil {
		return DocumentVersion{}, err
	}
	if n == d.Version {
		return DocumentVersion{ID: id, Version: n, Current: true, Kind: d.Kind, Title: d.Title, Body: d.Body, CreatedAt: d.UpdatedAt, CreatedBy: d.UpdatedBy}, nil
	}
	v := DocumentVersion{ID: id, Version: n}
	var kind, created string
	err = s.db.SQL().QueryRowContext(ctx,
		"SELECT kind, title, body, created_at, created_by FROM document_versions WHERE document_id = ? AND version = ?", id, n).
		Scan(&kind, &v.Title, &v.Body, &created, &v.CreatedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return DocumentVersion{}, fmt.Errorf("%w: version %d of document %q", ErrNotFound, n, id)
	}
	if err != nil {
		return DocumentVersion{}, fmt.Errorf("documents: get version %d of %s: %w", n, id, err)
	}
	v.Kind = Kind(kind)
	if v.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
		return DocumentVersion{}, fmt.Errorf("documents: %s version %d has bad created_at %q: %w", id, n, created, err)
	}
	return v, nil
}
