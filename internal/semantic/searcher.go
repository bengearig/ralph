package semantic

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/bengearig/ralph/internal/db"
	"github.com/bengearig/ralph/internal/embed"
	"github.com/bengearig/ralph/internal/search"
	"github.com/bengearig/ralph/internal/vecindex"
)

const chunkFactor = 4

// SearcherDeps is what a semantic search reads.
type SearcherDeps struct {
	DB       *db.DB
	Index    *vecindex.Store
	Provider embed.Provider
	Settings embed.Settings
}

// Searcher finds records by meaning. It reads the index and the store and writes neither.
type Searcher struct {
	d SearcherDeps
}

var _ search.Semantic = (*Searcher)(nil)

// NewSearcher returns a Searcher over the index.
func NewSearcher(d SearcherDeps) *Searcher { return &Searcher{d: d} }

// current is a record as the store has it now.
type current struct {
	rec      Record
	title    string
	status   string
	unit     string
	inactive bool
}

const (
	unitCurrentSQL = `SELECT id, title, ` + unitTextSQL + `, coalesce(state, ''), coalesce(state = 'CANCELLED', 0) FROM units WHERE level = ? AND id = ?`
	docCurrentSQL  = `SELECT id, title, body, status, status IN ('ARCHIVED', 'SUPERSEDED') FROM documents WHERE id = ?`
	noteCurrentSQL = `SELECT n.unit_id, n.body, u.title, coalesce(u.state, ''), coalesce(u.state = 'CANCELLED', 0)
FROM notes n JOIN units u ON u.id = n.unit_id WHERE CAST(n.id AS TEXT) = ?`
)

// lookup reads one record; ok is false when it no longer exists.
func (s *Searcher) lookup(ctx context.Context, kind search.Kind, ref string) (c current, ok bool, err error) {
	c.rec = Record{Kind: kind, Ref: ref}
	var row *sql.Row
	switch kind {
	case search.KindDocument:
		row = s.d.DB.SQL().QueryRowContext(ctx, docCurrentSQL, ref)
		err = row.Scan(&c.rec.Ref, &c.rec.Title, &c.rec.Text, &c.status, &c.inactive)
		c.title = c.rec.Title
	case search.KindNote:
		row = s.d.DB.SQL().QueryRowContext(ctx, noteCurrentSQL, ref)
		err = row.Scan(&c.rec.Unit, &c.rec.Text, &c.title, &c.status, &c.inactive)
		c.unit = c.rec.Unit
	default:
		row = s.d.DB.SQL().QueryRowContext(ctx, unitCurrentSQL, string(kind), ref)
		err = row.Scan(&c.rec.Ref, &c.rec.Title, &c.rec.Text, &c.status, &c.inactive)
		c.title = c.rec.Title
	}
	if errors.Is(err, sql.ErrNoRows) {
		return current{}, false, nil
	}
	if err != nil {
		return current{}, false, fmt.Errorf("semantic: read %s %s: %w", kind, ref, err)
	}
	c.rec = finish(c.rec)
	return c, true, nil
}

// Query embeds the text and returns the records whose best chunk is closest to it.
func (s *Searcher) Query(ctx context.Context, q search.SemanticQuery) ([]search.SemanticHit, error) {
	if q.Depth <= 0 {
		return []search.SemanticHit{}, nil
	}
	_, prefix := embed.Prefixes(s.d.Settings)
	vectors, err := s.d.Provider.Embed(ctx, []string{prefix + q.Text})
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}
	if len(vectors) != 1 {
		return nil, fmt.Errorf("%w: the provider returned %d vectors for the query", embed.ErrInvalid, len(vectors))
	}
	chunks, err := s.d.Index.Search(ctx, vectors[0], chunkFactor*q.Depth)
	if err != nil {
		return nil, fmt.Errorf("search the index: %w", err)
	}

	wantKind := map[search.Kind]bool{}
	for _, k := range q.Kinds {
		wantKind[k] = true
	}
	seen := map[vecindex.Key]bool{}
	hits := []search.SemanticHit{}
	for _, ch := range chunks {
		if len(hits) == q.Depth {
			break
		}
		key := vecindex.Key{Kind: ch.Kind, Ref: ch.Ref}
		if seen[key] {
			continue
		}
		seen[key] = true
		kind := search.Kind(ch.Kind)
		if len(wantKind) > 0 && !wantKind[kind] {
			continue
		}
		c, ok, err := s.lookup(ctx, kind, ch.Ref)
		if err != nil {
			return nil, err
		}
		if !ok || c.rec.Hash != ch.Hash {
			continue
		}
		if c.inactive && !q.IncludeInactive {
			continue
		}
		id := c.rec.Ref
		if kind == search.KindNote {
			id = c.unit
		}
		hits = append(hits, search.SemanticHit{
			Kind: kind, ID: id, Title: c.title, Excerpt: ch.Excerpt, Status: c.status,
			Unit: c.unit, Inactive: c.inactive, Score: ch.Score,
		})
	}
	return hits, nil
}
