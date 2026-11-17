package search

import (
	"cmp"
	"context"
	"fmt"
	"slices"
)

const (
	rrfK         = 60
	minDepth     = 30
	depthFactor  = 3
	hybridMaxDep = 100
)

// Semantic finds records by meaning. Hits come best first; the fields have the
// meanings they have in Hit, and Excerpt is the matching part of the text.
type Semantic interface {
	Query(ctx context.Context, q SemanticQuery) ([]SemanticHit, error)
}

// SemanticQuery asks for at most Depth hits.
type SemanticQuery struct {
	Text            string
	Kinds           []Kind
	IncludeInactive bool
	Depth           int
}

// SemanticHit is one record found by meaning; Score is the similarity.
type SemanticHit struct {
	Kind     Kind
	ID       string
	Title    string
	Excerpt  string
	Status   string
	Unit     string
	Inactive bool
	Score    float64
}

type hitKey struct {
	kind Kind
	id   string
	unit string
}

type fused struct {
	hit         Hit
	score       float64
	keywordRank int
}

// Fuse merges a keyword list and a semantic list, each best first, by
// reciprocal rank fusion and returns at most limit hits, best first.
func Fuse(keyword []Hit, semantic []SemanticHit, limit int) []Hit {
	byKey := map[hitKey]*fused{}
	var order []*fused

	seen := map[hitKey]bool{}
	rank := 0
	for _, h := range keyword {
		k := hitKey{h.Kind, h.ID, h.Unit}
		if seen[k] {
			continue
		}
		seen[k] = true
		rank++
		h.Sources = []string{SourceKeyword}
		f := &fused{hit: h, score: 1 / float64(rrfK+rank), keywordRank: rank}
		byKey[k] = f
		order = append(order, f)
	}
	clear(seen)
	rank = 0
	for _, s := range semantic {
		k := hitKey{s.Kind, s.ID, s.Unit}
		if seen[k] {
			continue
		}
		seen[k] = true
		rank++
		part := 1 / float64(rrfK+rank)
		if f, ok := byKey[k]; ok {
			f.score += part
			f.hit.Sources = []string{SourceKeyword, SourceSemantic}
			continue
		}
		order = append(order, &fused{keywordRank: -1, score: part, hit: Hit{
			Kind: s.Kind, ID: s.ID, Title: s.Title, Snippet: s.Excerpt, Status: s.Status, Unit: s.Unit,
			Inactive: s.Inactive, Sources: []string{SourceSemantic},
		}})
	}

	slices.SortStableFunc(order, func(a, b *fused) int {
		return cmp.Or(
			-cmp.Compare(a.score, b.score),
			cmp.Compare(rankOrLast(a.keywordRank), rankOrLast(b.keywordRank)),
			cmp.Compare(a.hit.Kind, b.hit.Kind),
			cmp.Compare(a.hit.ID, b.hit.ID),
		)
	})
	if limit >= 0 && len(order) > limit {
		order = order[:limit]
	}
	out := make([]Hit, len(order))
	for i, f := range order {
		out[i] = f.hit
		out[i].Score = f.score
	}
	return out
}

func rankOrLast(r int) int {
	if r < 0 {
		return int(^uint(0) >> 1)
	}
	return r
}

type hybrid struct {
	keyword Service
	sem     Semantic
}

// NewHybrid returns a search that combines keyword with sem; a nil sem gives keyword itself.
func NewHybrid(keyword Service, sem Semantic) Service {
	if sem == nil {
		return keyword
	}
	return &hybrid{keyword: keyword, sem: sem}
}

func (h *hybrid) Search(ctx context.Context, q Query) (Result, error) {
	_, limit, err := validate(q)
	if err != nil {
		return Result{}, err
	}
	depth := min(hybridMaxDep, max(minDepth, depthFactor*limit))

	kq := q
	kq.Limit = depth
	kres, err := h.keyword.Search(ctx, kq)
	if err != nil {
		return Result{}, err
	}
	for i := range kres.Hits {
		if kres.Hits[i].Kind == KindNote && kres.Hits[i].Unit == "" {
			kres.Hits[i].Unit = kres.Hits[i].ID
		}
	}
	shits, err := h.sem.Query(ctx, SemanticQuery{Text: q.Text, Kinds: q.Kinds, IncludeInactive: q.IncludeInactive, Depth: depth})
	if err != nil {
		return Result{}, fmt.Errorf("semantic search: %w", err)
	}
	return Result{Mode: ModeHybrid, Relaxed: kres.Relaxed, Hits: Fuse(kres.Hits, shits, limit)}, nil
}
