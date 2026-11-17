package search_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bengearig/ralph/internal/embed/core"
	"github.com/bengearig/ralph/internal/search"
)

const staleReason = "model a -> b"

func staleNotice(reason string) string {
	return "semantic search is off: the index was built with a different embedding configuration (" + reason + "); to rebuild it run \"ralph index --rebuild\""
}

// spySemantic reports a settable stale state and counts the other calls.
type spySemantic struct {
	mu                sync.Mutex
	reason            string
	err               error
	queries, catchUps int
	staleCalls        int
}

func (s *spySemantic) set(reason string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reason, s.err = reason, err
}

func (s *spySemantic) Stale(context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.staleCalls++
	return s.reason, s.err
}

func (s *spySemantic) CatchUp(context.Context, int) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.catchUps++
	return 0, nil
}

func (s *spySemantic) Empty(context.Context) (bool, error) { return false, nil }

func (s *spySemantic) Query(context.Context, search.SemanticQuery) ([]search.SemanticHit, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.queries++
	return nil, nil
}

func staleEnv(t *testing.T) (*fenv, *spySemantic) {
	e := newFenv(t)
	e.task("lemur feeding", "fruit")
	return e, &spySemantic{}
}

func TestHybridStaleFallsBackWithOffNotice(t *testing.T) {
	e, spy := staleEnv(t)
	spy.set(staleReason, nil)
	h := search.NewHybrid(search.New(e.d), spy, search.HybridOptions{Now: e.clock})
	res := e.search(h, "lemur")
	checkFallback(t, e, res, "lemur", staleNotice(staleReason))
	want := e.keywordOnly("lemur")
	if !reflect.DeepEqual(res.Hits, want.Hits) {
		t.Errorf("hits = %+v, want %+v", res.Hits, want.Hits)
	}
	for _, h := range res.Hits {
		if len(h.Sources) != 0 {
			t.Errorf("sources = %v", h.Sources)
		}
	}
	if spy.queries != 0 || spy.catchUps != 0 {
		t.Errorf("queries %d, catch-ups %d", spy.queries, spy.catchUps)
	}
}

func TestHybridStaleIsNotAnOutage(t *testing.T) {
	e, spy := staleEnv(t)
	e.sync()
	spy.set(staleReason, nil)
	h := search.NewHybrid(search.New(e.d), spy, search.HybridOptions{Now: e.clock})
	if res := e.search(h, "lemur"); res.Mode != search.ModeKeywordFallback {
		t.Fatalf("mode = %q", res.Mode)
	}
	spy.set("", nil)
	if res := e.search(h, "lemur"); res.Mode != search.ModeHybrid || spy.queries != 1 {
		t.Errorf("mode = %q, queries = %d", res.Mode, spy.queries)
	}
}

func TestHybridStaleCheckedAfterOutageMemory(t *testing.T) {
	e := newFenv(t)
	e.task("lemur feeding", "fruit")
	e.sync()
	spy := &spySemantic{}
	e.fake.FailWith(unavailable())
	failing := &failingQuery{spySemantic: spy}
	h := search.NewHybrid(search.New(e.d), failing, search.HybridOptions{Now: e.clock})
	e.search(h, "lemur")
	spy.set(staleReason, nil)
	before := spy.staleCalls
	res := e.search(h, "lemur")
	if want := fallbackNotice(strings.TrimPrefix(unavailable().Error(), "embed: ")); res.Notice != want {
		t.Errorf("notice = %q, want %q", res.Notice, want)
	}
	if spy.staleCalls != before {
		t.Error("Stale was called during the remembered outage")
	}
	e.advance(61 * time.Second)
	if res := e.search(h, "lemur"); res.Notice != staleNotice(staleReason) {
		t.Errorf("notice = %q", res.Notice)
	}
}

// failingQuery fails in CatchUp the way an unreachable provider does.
type failingQuery struct{ *spySemantic }

func (f *failingQuery) CatchUp(context.Context, int) (int, error) { return 0, unavailable() }

func TestHybridStaleErrorHandledLikeOtherErrors(t *testing.T) {
	e, spy := staleEnv(t)
	h := search.NewHybrid(search.New(e.d), spy, search.HybridOptions{Now: e.clock})

	spy.set("", unavailable())
	res := e.search(h, "lemur")
	if want := fallbackNotice(strings.TrimPrefix(unavailable().Error(), "embed: ")); res.Mode != search.ModeKeywordFallback || res.Notice != want {
		t.Errorf("res = %+v, want notice %q", res, want)
	}
	before := spy.staleCalls
	e.search(h, "lemur")
	if spy.staleCalls != before {
		t.Error("an outage from Stale was not remembered")
	}
	// An unrecognised error is returned as it is.
	e2, spy2 := staleEnv(t)
	boom := errors.New("boom")
	spy2.set("", boom)
	h2 := search.NewHybrid(search.New(e2.d), spy2, search.HybridOptions{Now: e2.clock})
	if _, err := h2.Search(bg, search.Query{Text: "lemur"}); !errors.Is(err, boom) {
		t.Errorf("err = %v", err)
	}
}

func TestHybridNotStaleSearchesNormally(t *testing.T) {
	e := newFenv(t)
	e.task("lemur feeding", "fruit")
	e.sync()
	e.fake.SetInfo(core.Info{Digest: "fake", Dimensions: 16})
	res := e.search(e.hybrid(search.HybridOptions{}), "lemur")
	if res.Mode != search.ModeHybrid || res.Notice != "" {
		t.Errorf("res = %+v", res)
	}
}
