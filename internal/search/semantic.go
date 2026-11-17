package search

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/bengearig/ralph/internal/embed"
	"github.com/bengearig/ralph/internal/vecindex"
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
	// CatchUp embeds up to maxRecords changed or new records and returns how many are still waiting.
	CatchUp(ctx context.Context, maxRecords int) (waiting int, err error)
	// Empty reports whether the index holds no vectors.
	Empty(ctx context.Context) (bool, error)
	// Stale returns why the index cannot be searched because another configuration built it, or an empty string.
	Stale(ctx context.Context) (reason string, err error)
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

var errEmptyIndex = errors.New("the semantic index is empty")

type staleError struct{ reason string }

func (e staleError) Error() string { return "the semantic index is stale: " + e.reason }

const (
	defaultHybridTimeout = 5 * time.Second
	defaultCatchUp       = 5
	outageMemory         = 60 * time.Second
	emptyReason          = "the semantic index is empty: run ralph index"
	dimensionsReason     = "the index was built with a different embedding model"
)

// HybridOptions tunes NewHybrid; a zero value means the default.
type HybridOptions struct {
	// Timeout limits the semantic part of a search; the default is 5 seconds.
	Timeout time.Duration
	// CatchUpRecords is the most records embedded before a search; the default is 5.
	CatchUpRecords int
	// Now is the clock; the default is time.Now.
	Now func() time.Time
}

type hybrid struct {
	keyword Service
	sem     Semantic
	opts    HybridOptions

	mu         sync.Mutex
	failedAt   time.Time
	failReason string
}

// NewHybrid returns a search that combines keyword with sem; a nil sem gives keyword itself.
// When the semantic part fails it falls back to keyword search.
func NewHybrid(keyword Service, sem Semantic, opts HybridOptions) Service {
	if sem == nil {
		return keyword
	}
	if opts.Timeout <= 0 {
		opts.Timeout = defaultHybridTimeout
	}
	if opts.CatchUpRecords <= 0 {
		opts.CatchUpRecords = defaultCatchUp
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &hybrid{keyword: keyword, sem: sem, opts: opts}
}

func (h *hybrid) recentFailure() (string, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.failReason != "" && h.opts.Now().Sub(h.failedAt) < outageMemory {
		return h.failReason, true
	}
	return "", false
}

func (h *hybrid) recordFailure(reason string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.failedAt, h.failReason = h.opts.Now(), reason
}

// failure classifies err: reason is set for a provider failure, and outage when it is to be remembered.
func (h *hybrid) failure(err error) (reason string, outage, ok bool) {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "timed out after " + h.opts.Timeout.String(), true, true
	case errors.Is(err, embed.ErrUnavailable), errors.Is(err, embed.ErrModelNotFound), errors.Is(err, embed.ErrInvalid):
		return strings.TrimPrefix(err.Error(), "embed: "), true, true
	case errors.Is(err, vecindex.ErrDimensions):
		return dimensionsReason, false, true
	}
	return "", false, false
}

func (h *hybrid) fallback(ctx context.Context, q Query, reason string) (Result, error) {
	res, err := h.keyword.Search(ctx, q)
	if err != nil {
		return Result{}, err
	}
	res.Mode = ModeKeywordFallback
	res.Notice = fmt.Sprintf("semantic search is unavailable (%s); showing keyword results", reason)
	return res, nil
}

func (h *hybrid) staleFallback(ctx context.Context, q Query, reason string) (Result, error) {
	res, err := h.keyword.Search(ctx, q)
	if err != nil {
		return Result{}, err
	}
	res.Mode = ModeKeywordFallback
	res.Notice = fmt.Sprintf("semantic search is off: the index was built with a different embedding configuration (%s); to rebuild it run \"ralph index --rebuild\"", reason)
	return res, nil
}

func (h *hybrid) Search(ctx context.Context, q Query) (Result, error) {
	_, limit, err := validate(q)
	if err != nil {
		return Result{}, err
	}
	if reason, ok := h.recentFailure(); ok {
		return h.fallback(ctx, q, reason)
	}
	res, err := h.semanticSearch(ctx, q, limit)
	if err == nil {
		return res, nil
	}
	if ctx.Err() != nil {
		return Result{}, err
	}
	var stale staleError
	if errors.As(err, &stale) {
		return h.staleFallback(ctx, q, stale.reason)
	}
	if errors.Is(err, errEmptyIndex) {
		return h.fallback(ctx, q, emptyReason)
	}
	reason, outage, ok := h.failure(err)
	if !ok {
		return Result{}, err
	}
	if outage {
		h.recordFailure(reason)
	}
	return h.fallback(ctx, q, reason)
}

func (h *hybrid) semanticSearch(ctx context.Context, q Query, limit int) (Result, error) {
	depth := min(hybridMaxDep, max(minDepth, depthFactor*limit))

	cctx, cancel := context.WithTimeout(ctx, h.opts.Timeout)
	defer cancel()
	reason, err := h.sem.Stale(cctx)
	if err != nil {
		return Result{}, err
	}
	if reason != "" {
		return Result{}, staleError{reason}
	}
	waiting, err := h.sem.CatchUp(cctx, h.opts.CatchUpRecords)
	if err != nil {
		return Result{}, err
	}
	empty, err := h.sem.Empty(cctx)
	if err != nil {
		return Result{}, err
	}
	if empty {
		return Result{}, errEmptyIndex
	}

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
	shits, err := h.sem.Query(cctx, SemanticQuery{Text: q.Text, Kinds: q.Kinds, IncludeInactive: q.IncludeInactive, Depth: depth})
	if err != nil {
		return Result{}, fmt.Errorf("semantic search: %w", err)
	}
	res := Result{Mode: ModeHybrid, Relaxed: kres.Relaxed, Hits: Fuse(kres.Hits, shits, limit)}
	if waiting > 0 {
		noun := "records"
		if waiting == 1 {
			noun = "record"
		}
		res.Notice = fmt.Sprintf("semantic index is behind by %d %s: run ralph index", waiting, noun)
	}
	return res, nil
}
