package search

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/bengearig/ralph/internal/db"
)

const (
	defaultLimit    = 20
	maxLimit        = 100
	snippetFallback = 120
)

// inactiveSQL is true for a document that is archived or superseded, a task
// that is cancelled and a note on a cancelled task. Slices and briefs have
// no state, so u.state is NULL for them and the comparison is never true.
const inactiveSQL = `(CASE WHEN f.kind = 'document' THEN d.status IN ('ARCHIVED', 'SUPERSEDED')
		ELSE coalesce(u.state = 'CANCELLED', 0) END)`

type keyword struct {
	db *db.DB
}

var _ Service = (*keyword)(nil)

func (k *keyword) Search(ctx context.Context, q Query) (Result, error) {
	parsed, limit, err := validate(q)
	if err != nil {
		return Result{}, err
	}

	res := Result{Mode: ModeKeyword}
	res.Hits, err = k.run(ctx, parsed.Match(joinAnd), q, limit)
	if err != nil {
		return Result{}, err
	}
	if len(res.Hits) == 0 && len(parsed.Terms) > 1 {
		res.Hits, err = k.run(ctx, parsed.Match(joinOr), q, limit)
		if err != nil {
			return Result{}, err
		}
		res.Relaxed = len(res.Hits) > 0
	}
	return res, nil
}

func (k *keyword) run(ctx context.Context, match string, q Query, limit int) ([]Hit, error) {
	var sb strings.Builder
	sb.WriteString(`
SELECT f.kind, coalesce(CASE WHEN f.kind = 'document' THEN d.id ELSE u.id END, ''),
	coalesce(CASE WHEN f.kind = 'document' THEN d.title ELSE u.title END, ''),
	coalesce(CASE WHEN f.kind = 'document' THEN d.status ELSE u.state END, ''),
	bm25(search_fts, 5.0, 1.0) AS score,
	snippet(search_fts, 1, '[', ']', '…', 12),
	f.body,
	` + inactiveSQL + ` AS inactive
FROM search_fts f
LEFT JOIN units u ON f.kind <> 'document' AND u.id = CASE WHEN f.kind = 'note' THEN f.unit ELSE f.ref END
LEFT JOIN documents d ON f.kind = 'document' AND d.id = f.ref
WHERE search_fts MATCH ?
	AND (CASE WHEN f.kind = 'document' THEN d.id ELSE u.id END) IS NOT NULL`)
	args := []any{match}
	if !q.IncludeInactive {
		sb.WriteString(" AND NOT (" + inactiveSQL + ")")
	}
	if len(q.Kinds) > 0 {
		sb.WriteString(" AND f.kind IN (" + strings.TrimSuffix(strings.Repeat("?,", len(q.Kinds)), ",") + ")")
		for _, kind := range q.Kinds {
			args = append(args, string(kind))
		}
	}
	sb.WriteString("\nORDER BY score, f.rowid LIMIT ?")
	args = append(args, limit)

	rows, err := k.db.SQL().QueryContext(ctx, sb.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("search: run query: %w", err)
	}
	defer func() { _ = rows.Close() }()
	hits := []Hit{}
	for rows.Next() {
		var h Hit
		var kind, body string
		var score float64
		if err := rows.Scan(&kind, &h.ID, &h.Title, &h.Status, &score, &h.Snippet, &body, &h.Inactive); err != nil {
			return nil, fmt.Errorf("search: read hit: %w", err)
		}
		h.Kind = Kind(kind)
		h.Score = -score
		if !strings.Contains(h.Snippet, "[") {
			h.Snippet = bodyStart(body)
		}
		hits = append(hits, h)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("search: read hits: %w", err)
	}
	return hits, nil
}

// validate parses the query text and checks the kinds and the limit, returning the limit to use.
func validate(q Query) (Parsed, int, error) {
	parsed, err := ParseQuery(q.Text)
	if err != nil {
		return Parsed{}, 0, err
	}
	limit := q.Limit
	switch {
	case limit == 0:
		limit = defaultLimit
	case limit < 0 || limit > maxLimit:
		return Parsed{}, 0, fmt.Errorf("%w: limit %d is not between 1 and %d", ErrInvalid, q.Limit, maxLimit)
	}
	for _, kind := range q.Kinds {
		if !kind.valid() {
			return Parsed{}, 0, fmt.Errorf("%w: unknown kind %q", ErrInvalid, kind)
		}
	}
	return parsed, limit, nil
}

// bodyStart returns the first characters of body, cut at a word boundary,
// with an ellipsis when it was cut.
func bodyStart(body string) string {
	if utf8.RuneCountInString(body) <= snippetFallback {
		return body
	}
	rs := []rune(body)
	cut := snippetFallback
	if !isSpace(rs[cut]) && !isSpace(rs[cut-1]) {
		for cut > 0 && !isSpace(rs[cut-1]) {
			cut--
		}
		if cut == 0 {
			cut = snippetFallback
		}
	}
	return strings.TrimRight(string(rs[:cut]), " \t\r\n") + "…"
}

func isSpace(r rune) bool { return r == ' ' || r == '\t' || r == '\n' || r == '\r' }
