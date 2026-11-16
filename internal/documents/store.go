package documents

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/bengearig/ralph/internal/db"
	"github.com/bengearig/ralph/internal/units"
)

const (
	columns        = "id, number, kind, brief_id, title, body, status, superseded_by, version, created_at, updated_at, created_by, updated_by"
	maxTitleLength = 200
	maxBodyBytes   = 1_000_000
	counterKey     = "document"
)

type Store struct {
	// Now supplies the time stamped on writes.
	Now  func() time.Time
	db   *db.DB
	keep int
}

// Option configures a Store.
type Option func(*Store)

// WithKeepVersions keeps only the newest n earlier versions of a document,
// trimming at each real edit. Zero, and a negative number, keep every one.
func WithKeepVersions(n int) Option {
	return func(s *Store) { s.keep = max(n, 0) }
}

func New(d *db.DB, opts ...Option) *Store {
	s := &Store{Now: time.Now, db: d}
	for _, o := range opts {
		o(s)
	}
	return s
}

func (s *Store) stamp() string {
	return s.Now().UTC().Format(time.RFC3339Nano)
}

func validateKind(k Kind) error {
	if !k.valid() {
		return fmt.Errorf("%w: kind %q is not architecture, design, decision or reference", ErrInvalid, k)
	}
	return nil
}

func validateTitle(title string) error {
	if strings.ContainsAny(title, "\r\n") {
		return fmt.Errorf("%w: title must be one line", ErrInvalid)
	}
	n := utf8.RuneCountInString(strings.TrimSpace(title))
	if n == 0 {
		return fmt.Errorf("%w: title is empty", ErrInvalid)
	}
	if n > maxTitleLength {
		return fmt.Errorf("%w: title is %d characters, at most %d are allowed", ErrInvalid, n, maxTitleLength)
	}
	return nil
}

func validateBody(body string) error {
	if strings.TrimSpace(body) == "" {
		return fmt.Errorf("%w: body is blank", ErrInvalid)
	}
	if len(body) > maxBodyBytes {
		return fmt.Errorf("%w: body is %d bytes, at most %d are allowed", ErrInvalid, len(body), maxBodyBytes)
	}
	return nil
}

// Create adds an ACTIVE document at version 1.
func (s *Store) Create(ctx context.Context, n NewDocument) (Document, error) {
	if err := validateKind(n.Kind); err != nil {
		return Document{}, err
	}
	n.Title = strings.TrimSpace(n.Title)
	if err := validateTitle(n.Title); err != nil {
		return Document{}, err
	}
	if err := validateBody(n.Body); err != nil {
		return Document{}, err
	}

	var id string
	err := s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		var brief any
		if n.BriefID != "" {
			var level string
			err := tx.QueryRowContext(ctx, "SELECT level FROM units WHERE id = ?", n.BriefID).Scan(&level)
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("%w: brief %q", ErrNotFound, n.BriefID)
			}
			if err != nil {
				return fmt.Errorf("documents: look up brief %q: %w", n.BriefID, err)
			}
			if units.Level(level) != units.LevelBrief {
				return fmt.Errorf("%w: a document is tied to a brief, but %q is a %s", ErrInvalid, n.BriefID, level)
			}
			brief = n.BriefID
		}

		var number int
		err := tx.QueryRowContext(ctx,
			"INSERT INTO counters (level, last) VALUES (?, 1) ON CONFLICT (level) DO UPDATE SET last = last + 1 RETURNING last",
			counterKey).Scan(&number)
		if err != nil {
			return fmt.Errorf("documents: allocate number: %w", err)
		}
		id = formatID(number)
		now := s.stamp()
		actor := units.ActorFrom(ctx)
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO documents ("+columns+") VALUES (?, ?, ?, ?, ?, ?, ?, NULL, 1, ?, ?, ?, ?)",
			id, number, string(n.Kind), brief, n.Title, n.Body, string(StatusActive), now, now, actor, actor); err != nil {
			return fmt.Errorf("documents: insert %s: %w", id, err)
		}
		return nil
	})
	if err != nil {
		return Document{}, err
	}
	return s.Get(ctx, id)
}

type scanner interface{ Scan(dest ...any) error }

func scan(r scanner) (Document, error) {
	var (
		d                  Document
		kind, status       string
		brief, supersedor  sql.NullString
		created, updatedAt string
	)
	if err := r.Scan(&d.ID, &d.Number, &kind, &brief, &d.Title, &d.Body, &status, &supersedor, &d.Version, &created, &updatedAt, &d.CreatedBy, &d.UpdatedBy); err != nil {
		return Document{}, err
	}
	d.Kind = Kind(kind)
	d.Status = Status(status)
	d.BriefID = brief.String
	d.SupersededBy = supersedor.String
	var err error
	if d.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
		return Document{}, fmt.Errorf("documents: %s has bad created_at %q: %w", d.ID, created, err)
	}
	if d.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt); err != nil {
		return Document{}, fmt.Errorf("documents: %s has bad updated_at %q: %w", d.ID, updatedAt, err)
	}
	d.Fingerprint = fingerprint(d)
	return d, nil
}

func getTx(ctx context.Context, tx *sql.Tx, id string) (Document, error) {
	d, err := scan(tx.QueryRowContext(ctx, "SELECT "+columns+" FROM documents WHERE id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return Document{}, fmt.Errorf("%w: document %q", ErrNotFound, id)
	}
	if err != nil {
		return Document{}, fmt.Errorf("documents: get %q: %w", id, err)
	}
	return d, nil
}

// Get returns the document with the given ID.
func (s *Store) Get(ctx context.Context, id string) (Document, error) {
	if err := parseID(id); err != nil {
		return Document{}, err
	}
	d, err := scan(s.db.SQL().QueryRowContext(ctx, "SELECT "+columns+" FROM documents WHERE id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return Document{}, fmt.Errorf("%w: document %q", ErrNotFound, id)
	}
	if err != nil {
		return Document{}, fmt.Errorf("documents: get %q: %w", id, err)
	}
	return d, nil
}

// List returns the matching documents ordered by number.
func (s *Store) List(ctx context.Context, f Filter) ([]Document, error) {
	var (
		where []string
		args  []any
	)
	if f.BriefID != nil {
		if *f.BriefID == "" {
			where = append(where, "brief_id IS NULL")
		} else {
			where = append(where, "brief_id = ?")
			args = append(args, *f.BriefID)
		}
	}
	if len(f.Kinds) > 0 {
		marks := make([]string, len(f.Kinds))
		for i, k := range f.Kinds {
			if err := validateKind(k); err != nil {
				return nil, err
			}
			marks[i] = "?"
			args = append(args, string(k))
		}
		where = append(where, "kind IN ("+strings.Join(marks, ", ")+")")
	}
	statuses := f.Statuses
	if len(statuses) == 0 {
		statuses = []Status{StatusActive}
	}
	marks := make([]string, len(statuses))
	for i, st := range statuses {
		if !st.valid() {
			return nil, fmt.Errorf("%w: status %q is not ACTIVE, ARCHIVED or SUPERSEDED", ErrInvalid, st)
		}
		marks[i] = "?"
		args = append(args, string(st))
	}
	where = append(where, "status IN ("+strings.Join(marks, ", ")+")")

	q := "SELECT " + columns + " FROM documents WHERE " + strings.Join(where, " AND ") + " ORDER BY number"
	rows, err := s.db.SQL().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("documents: list: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Document
	for rows.Next() {
		d, err := scan(rows)
		if err != nil {
			return nil, fmt.Errorf("documents: list: %w", err)
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("documents: list: %w", err)
	}
	return out, nil
}

// Update applies c to an ACTIVE document if its fingerprint is still expected.
// A change that leaves title, body and kind as stored writes nothing.
func (s *Store) Update(ctx context.Context, id, expected string, c Change) (Document, error) {
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
		if d.Status != StatusActive {
			return fmt.Errorf("%w: document %s is %s and cannot be edited; restore it first", ErrInvalid, id, d.Status)
		}
		title, body, kind := d.Title, d.Body, d.Kind
		if c.Title != nil {
			title = strings.TrimSpace(*c.Title)
		}
		if c.Body != nil {
			body = *c.Body
		}
		if c.Kind != nil {
			kind = *c.Kind
		}
		if err := validateKind(kind); err != nil {
			return err
		}
		if err := validateTitle(title); err != nil {
			return err
		}
		if err := validateBody(body); err != nil {
			return err
		}
		if title == d.Title && body == d.Body && kind == d.Kind {
			return nil
		}
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO document_versions (document_id, version, kind, title, body, created_at, created_by) VALUES (?, ?, ?, ?, ?, ?, ?)",
			id, d.Version, string(d.Kind), d.Title, d.Body, d.UpdatedAt.UTC().Format(time.RFC3339Nano), d.UpdatedBy); err != nil {
			return fmt.Errorf("documents: keep version %d of %s: %w", d.Version, id, err)
		}
		if _, err := tx.ExecContext(ctx,
			"UPDATE documents SET title = ?, body = ?, kind = ?, version = version + 1, updated_at = ?, updated_by = ? WHERE id = ?",
			title, body, string(kind), s.stamp(), units.ActorFrom(ctx), id); err != nil {
			return fmt.Errorf("documents: update %s: %w", id, err)
		}
		if s.keep > 0 {
			if _, err := tx.ExecContext(ctx,
				"DELETE FROM document_versions WHERE document_id = ? AND version NOT IN (SELECT version FROM document_versions WHERE document_id = ? ORDER BY version DESC LIMIT ?)",
				id, id, s.keep); err != nil {
				return fmt.Errorf("documents: trim versions of %s: %w", id, err)
			}
		}
		return nil
	})
	if err != nil {
		return Document{}, err
	}
	return s.Get(ctx, id)
}
