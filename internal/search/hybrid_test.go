package search

import (
	"context"
	"errors"
	"math"
	"reflect"
	"slices"
	"testing"
)

type stubKeyword struct {
	res    Result
	err    error
	called []Query
}

func (s *stubKeyword) Search(_ context.Context, q Query) (Result, error) {
	s.called = append(s.called, q)
	return s.res, s.err
}

type stubSemantic struct {
	hits   []SemanticHit
	err    error
	called []SemanticQuery
}

func (s *stubSemantic) CatchUp(context.Context, int) (int, error) { return 0, nil }

func (s *stubSemantic) Empty(context.Context) (bool, error) { return false, nil }

func (s *stubSemantic) Query(_ context.Context, q SemanticQuery) ([]SemanticHit, error) {
	s.called = append(s.called, q)
	return s.hits, s.err
}

func kw(ids ...string) []Hit {
	out := make([]Hit, len(ids))
	for i, id := range ids {
		out[i] = Hit{Kind: KindTask, ID: id, Title: "kw " + id, Snippet: "[kw] " + id, Status: "TODO", Score: 9}
	}
	return out
}

func sem(ids ...string) []SemanticHit {
	out := make([]SemanticHit, len(ids))
	for i, id := range ids {
		out[i] = SemanticHit{Kind: KindTask, ID: id, Title: "sem " + id, Excerpt: "excerpt " + id, Status: "DONE", Score: 0.5}
	}
	return out
}

func hitIDs(hits []Hit) []string {
	out := []string{}
	for _, h := range hits {
		out = append(out, h.ID)
	}
	return out
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-12 }

func TestFuseReciprocalRankScores(t *testing.T) {
	got := Fuse(kw("T-1", "T-2"), sem("T-2", "T-3"), 10)
	if want := []string{"T-2", "T-1", "T-3"}; !slices.Equal(hitIDs(got), want) {
		t.Fatalf("order = %v, want %v", hitIDs(got), want)
	}
	for i, want := range []float64{1.0/61 + 1.0/62, 1.0 / 61, 1.0 / 62} {
		if !near(got[i].Score, want) {
			t.Errorf("hit %d score = %v, want %v", i, got[i].Score, want)
		}
	}
	wantScore := 1.0/62 + 1.0/61
	if !near(got[0].Score, wantScore) {
		t.Errorf("both-list score = %v", got[0].Score)
	}
}

func TestFuseBothListsSnippetFromKeyword(t *testing.T) {
	got := Fuse(kw("T-1"), sem("T-1"), 10)
	if len(got) != 1 {
		t.Fatalf("hits = %+v", got)
	}
	h := got[0]
	if h.Snippet != "[kw] T-1" || h.Title != "kw T-1" || h.Status != "TODO" || !slices.Equal(h.Sources, []string{"keyword", "semantic"}) {
		t.Errorf("hit = %+v", h)
	}
}

func TestFuseSemanticOnlyUsesExcerpt(t *testing.T) {
	got := Fuse(nil, sem("T-9"), 10)
	h := got[0]
	if h.Snippet != "excerpt T-9" || h.Title != "sem T-9" || h.Status != "DONE" || !slices.Equal(h.Sources, []string{"semantic"}) {
		t.Errorf("hit = %+v", h)
	}
	if got := Fuse(kw("T-1"), nil, 10); !slices.Equal(got[0].Sources, []string{"keyword"}) {
		t.Errorf("keyword-only sources = %v", got[0].Sources)
	}
}

func TestFuseTieBreakKeywordRankThenID(t *testing.T) {
	// Equal fused scores: the record with the better keyword rank first.
	got := Fuse(kw("T-5"), sem("T-2"), 10)
	if want := []string{"T-5", "T-2"}; !slices.Equal(hitIDs(got), want) {
		t.Errorf("keyword vs semantic only = %v, want %v", hitIDs(got), want)
	}
	k := []Hit{{Kind: KindTask, ID: "T-9"}, {Kind: KindDocument, ID: "D-1"}}
	s := []SemanticHit{{Kind: KindDocument, ID: "D-1"}, {Kind: KindTask, ID: "T-9"}}
	got = Fuse(k, s, 10)
	if want := []string{"T-9", "D-1"}; !slices.Equal(hitIDs(got), want) || !near(got[0].Score, got[1].Score) {
		t.Errorf("equal scores = %v, want keyword rank order %v", hitIDs(got), want)
	}
	kk := []Hit{{Kind: KindTask, ID: "T-2"}, {Kind: KindTask, ID: "T-1"}}
	ss := []SemanticHit{{Kind: KindTask, ID: "T-1"}, {Kind: KindTask, ID: "T-2"}}
	if got := Fuse(kk, ss, 10); hitIDs(got)[0] != "T-2" {
		t.Errorf("keyword rank must beat id: %v", hitIDs(got))
	}
	sOnly := []SemanticHit{{Kind: KindTask, ID: "T-2"}, {Kind: KindTask, ID: "T-1"}}
	if got := Fuse(nil, sOnly, 10); hitIDs(got)[0] != "T-2" {
		t.Errorf("rank must beat id: %v", hitIDs(got))
	}
}

func TestFuseCutsToLimit(t *testing.T) {
	got := Fuse(kw("T-1", "T-2", "T-3"), sem("T-4", "T-5"), 2)
	if len(got) != 2 {
		t.Fatalf("hits = %v", hitIDs(got))
	}
	if got := Fuse(nil, nil, 5); len(got) != 0 || got == nil {
		t.Errorf("empty fuse = %#v", got)
	}
}

func TestFuseIsPure(t *testing.T) {
	k, s := kw("T-1", "T-2"), sem("T-2", "T-3")
	k0, s0 := slices.Clone(k), slices.Clone(s)
	first := Fuse(k, s, 10)
	second := Fuse(k, s, 10)
	if !reflect.DeepEqual(first, second) {
		t.Error("two calls differ")
	}
	if !reflect.DeepEqual(k, k0) || !reflect.DeepEqual(s, s0) {
		t.Error("inputs changed")
	}
}

func TestFuseNotesDistinguishedByUnit(t *testing.T) {
	k := []Hit{{Kind: KindNote, ID: "T-1", Unit: "T-1"}}
	s := []SemanticHit{{Kind: KindNote, ID: "T-1", Unit: "T-1"}, {Kind: KindNote, ID: "T-1", Unit: "T-7"}}
	got := Fuse(k, s, 10)
	if len(got) != 2 {
		t.Fatalf("hits = %+v", got)
	}
	if !slices.Equal(got[0].Sources, []string{"keyword", "semantic"}) || got[1].Unit != "T-7" {
		t.Errorf("hits = %+v", got)
	}
}

func TestHybridNilSemanticIsKeyword(t *testing.T) {
	k := &stubKeyword{}
	if got := NewHybrid(k, nil, HybridOptions{}); got != Service(k) {
		t.Errorf("got %T", got)
	}
}

func TestHybridInvalidQuerySameErrors(t *testing.T) {
	k, s := &stubKeyword{}, &stubSemantic{}
	h := NewHybrid(k, s, HybridOptions{})
	for _, q := range []Query{
		{Text: "   "}, {Text: "x", Limit: -1}, {Text: "x", Limit: 101}, {Text: "x", Kinds: []Kind{"bogus"}},
	} {
		_, err := h.Search(context.Background(), q)
		_, want := New(nil).Search(context.Background(), q)
		if !errors.Is(err, ErrInvalid) || err.Error() != want.Error() {
			t.Errorf("%+v: err %v, want %v", q, err, want)
		}
	}
	if len(k.called) != 0 || len(s.called) != 0 {
		t.Error("a side was called for an invalid query")
	}
}

func TestHybridDepthIsThreeTimesLimitBetween30And100(t *testing.T) {
	for _, c := range []struct{ limit, want int }{{0, 60}, {1, 30}, {10, 30}, {11, 33}, {20, 60}, {34, 100}, {100, 100}} {
		k, s := &stubKeyword{}, &stubSemantic{}
		_, err := NewHybrid(k, s, HybridOptions{}).Search(context.Background(), Query{Text: "x", Limit: c.limit, Kinds: []Kind{KindTask}, IncludeInactive: true})
		if err != nil {
			t.Fatal(err)
		}
		if k.called[0].Limit != c.want || s.called[0].Depth != c.want {
			t.Errorf("limit %d: keyword %d, semantic %d, want %d", c.limit, k.called[0].Limit, s.called[0].Depth, c.want)
		}
		if !s.called[0].IncludeInactive || !slices.Equal(s.called[0].Kinds, []Kind{KindTask}) || !k.called[0].IncludeInactive {
			t.Errorf("filters not passed: %+v %+v", k.called[0], s.called[0])
		}
	}
}

func TestHybridModeAndRelaxedFromKeyword(t *testing.T) {
	k := &stubKeyword{res: Result{Mode: ModeKeyword, Relaxed: true, Hits: kw("T-1")}}
	res, err := NewHybrid(k, &stubSemantic{hits: sem("T-2")}, HybridOptions{}).Search(context.Background(), Query{Text: "a b"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Mode != ModeHybrid || !res.Relaxed || res.Notice != "" || len(res.Hits) != 2 {
		t.Errorf("res = %+v", res)
	}
}

func TestHybridSemanticErrorWrapped(t *testing.T) {
	boom := errors.New("boom")
	_, err := NewHybrid(&stubKeyword{}, &stubSemantic{err: boom}, HybridOptions{}).Search(context.Background(), Query{Text: "x"})
	if !errors.Is(err, boom) || err.Error() != "semantic search: boom" {
		t.Errorf("err = %v", err)
	}
}

func TestHybridSourcesSet(t *testing.T) {
	e := newIndexEnv(t)
	a := e.task("lemur feeding", "fruit", "")
	b := e.task("zebra stripes", "other", "")
	s := &stubSemantic{hits: []SemanticHit{{Kind: KindTask, ID: b.ID, Title: b.Title, Excerpt: "e", Status: "TODO"}, {Kind: KindTask, ID: a.ID, Title: a.Title, Excerpt: "e", Status: "TODO"}}}
	res, err := NewHybrid(e.svc, s, HybridOptions{}).Search(context.Background(), Query{Text: "lemur"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Mode != ModeHybrid || len(res.Hits) != 2 {
		t.Fatalf("res = %+v", res)
	}
	if res.Hits[0].ID != a.ID || !slices.Equal(res.Hits[0].Sources, []string{"keyword", "semantic"}) || res.Hits[0].Snippet == "e" {
		t.Errorf("first = %+v", res.Hits[0])
	}
	if res.Hits[1].ID != b.ID || !slices.Equal(res.Hits[1].Sources, []string{"semantic"}) || res.Hits[1].Snippet != "e" {
		t.Errorf("second = %+v", res.Hits[1])
	}
}

func TestKeywordAloneLeavesSourcesEmpty(t *testing.T) {
	e := newIndexEnv(t)
	e.task("lemur feeding", "fruit", "")
	res, err := e.svc.Search(context.Background(), Query{Text: "lemur"})
	if err != nil || len(res.Hits) != 1 {
		t.Fatalf("%v %+v", err, res)
	}
	if res.Hits[0].Sources != nil || res.Notice != "" || res.Mode != ModeKeyword {
		t.Errorf("res = %+v", res)
	}
}
