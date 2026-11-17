package search_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bengearig/ralph/internal/db"
	"github.com/bengearig/ralph/internal/embed"
	"github.com/bengearig/ralph/internal/embed/core"
	"github.com/bengearig/ralph/internal/embed/embedtest"
	"github.com/bengearig/ralph/internal/search"
	"github.com/bengearig/ralph/internal/semantic"
	"github.com/bengearig/ralph/internal/testutil"
	"github.com/bengearig/ralph/internal/units"
	"github.com/bengearig/ralph/internal/vecindex"
)

var bg = context.Background()

type fenv struct {
	t        *testing.T
	d        *db.DB
	units    units.Service
	index    *vecindex.Store
	fake     *embedtest.Fake
	settings embed.Settings
	slice    units.Unit
	now      time.Time
	mu       sync.Mutex
}

func newFenv(t *testing.T) *fenv {
	t.Helper()
	d := testutil.NewDB(t, db.Migrations)
	e := &fenv{t: t, d: d, units: units.NewService(d), fake: embedtest.NewFake(16), now: time.Unix(1_000_000, 0),
		settings: embed.Settings{Provider: "ollama", Model: "m", ChunkChars: 1200}}
	idx, err := vecindex.Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = idx.Close() })
	e.index = idx
	brief := e.create(units.NewUnit{Level: units.LevelBrief, Title: "Brief", Body: "b"})
	e.slice = e.create(units.NewUnit{Level: units.LevelSlice, ParentID: brief.ID, Title: "Slice", Body: "s"})
	return e
}

func (e *fenv) create(n units.NewUnit) units.Unit {
	e.t.Helper()
	u, err := e.units.Create(bg, n)
	if err != nil {
		e.t.Fatal(err)
	}
	return u
}

func (e *fenv) task(title, body string) units.Unit {
	return e.create(units.NewUnit{Level: units.LevelTask, ParentID: e.slice.ID, Title: title, Body: body})
}

func (e *fenv) searcher() *semantic.Searcher {
	return semantic.NewSearcher(semantic.SearcherDeps{DB: e.d, Index: e.index, Provider: e.fake, Settings: e.settings})
}

func (e *fenv) clock() time.Time {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.now
}

func (e *fenv) advance(d time.Duration) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.now = e.now.Add(d)
}

func (e *fenv) hybrid(opts search.HybridOptions) search.Service {
	opts.Now = e.clock
	return search.NewHybrid(search.New(e.d), e.searcher(), opts)
}

func (e *fenv) sync() {
	e.t.Helper()
	_, err := semantic.Sync(bg, semantic.SyncDeps{DB: e.d, Index: e.index, Provider: e.fake, Settings: e.settings}, semantic.SyncOptions{})
	if err != nil {
		e.t.Fatal(err)
	}
}

func (e *fenv) search(s search.Service, text string) search.Result {
	e.t.Helper()
	res, err := s.Search(bg, search.Query{Text: text})
	if err != nil {
		e.t.Fatal(err)
	}
	return res
}

func unavailable() error { return fmt.Errorf("%w: connection refused", embed.ErrUnavailable) }

func fallbackNotice(reason string) string {
	return "semantic search is unavailable (" + reason + "); showing keyword results"
}

func (e *fenv) keywordOnly(text string) search.Result {
	e.t.Helper()
	return e.search(search.New(e.d), text)
}

func checkFallback(t *testing.T, e *fenv, res search.Result, text, notice string) {
	t.Helper()
	want := e.keywordOnly(text)
	if res.Mode != search.ModeKeywordFallback || res.Notice != notice {
		t.Errorf("mode %q notice %q, want %q %q", res.Mode, res.Notice, search.ModeKeywordFallback, notice)
	}
	if !reflect.DeepEqual(res.Hits, want.Hits) || res.Relaxed != want.Relaxed {
		t.Errorf("hits %+v, keyword alone %+v", res.Hits, want.Hits)
	}
	for _, h := range res.Hits {
		if len(h.Sources) != 0 {
			t.Errorf("sources = %v", h.Sources)
		}
	}
}

func TestHybridFallbackOnUnavailable(t *testing.T) {
	e := newFenv(t)
	e.task("lemur feeding", "fruit")
	e.task("lemur sleeping", "tree")
	e.sync()
	h := e.hybrid(search.HybridOptions{})
	e.fake.FailWith(unavailable())
	e.task("lemur new", "edited")
	res := e.search(h, "lemur")
	if len(res.Hits) != 3 {
		t.Fatalf("hits = %+v", res.Hits)
	}
	checkFallback(t, e, res, "lemur", fallbackNotice("embedding T-3: embedding service unavailable: connection refused"))
}

func TestHybridFallbackOnModelNotFound(t *testing.T) {
	e := newFenv(t)
	e.task("lemur feeding", "fruit")
	e.sync()
	e.fake.FailWith(fmt.Errorf("embed: %w: run ollama pull m", embed.ErrModelNotFound))
	res := e.search(e.hybrid(search.HybridOptions{}), "lemur")
	if res.Mode != search.ModeKeywordFallback || !strings.Contains(res.Notice, "ollama pull m") || strings.Contains(res.Notice, "(embed: ") {
		t.Errorf("res = %+v", res)
	}
	checkFallback(t, e, res, "lemur", res.Notice)
}

func TestHybridFallbackOnDeadline(t *testing.T) {
	e := newFenv(t)
	e.task("lemur feeding", "fruit")
	e.sync()
	e.task("lemur extra", "changed")
	e.fake.SetDelay(10 * time.Second)
	h := e.hybrid(search.HybridOptions{Timeout: 150 * time.Millisecond})
	start := time.Now()
	res := e.search(h, "lemur")
	if took := time.Since(start); took > 3*time.Second {
		t.Errorf("search took %v", took)
	}
	checkFallback(t, e, res, "lemur", fallbackNotice("timed out after 150ms"))
}

func TestHybridFallbackOnDimensionMismatch(t *testing.T) {
	e := newFenv(t)
	e.task("lemur feeding", "fruit")
	e.sync()
	other := embedtest.NewFake(8)
	other.SetInfo(core.Info{Digest: "fake", Dimensions: 16})
	s := semantic.NewSearcher(semantic.SearcherDeps{DB: e.d, Index: e.index, Provider: other, Settings: e.settings})
	h := search.NewHybrid(search.New(e.d), s, search.HybridOptions{Now: e.clock})
	e.task("lemur again", "new")
	reason := "the index was built with a different embedding model"
	for range 2 {
		res := e.search(h, "lemur")
		checkFallback(t, e, res, "lemur", fallbackNotice(reason))
	}
	if n := len(other.Calls()); n < 2 {
		t.Errorf("provider calls = %d; a dimension mismatch must not be remembered", n)
	}
}

func TestHybridEmptyIndexFallsBackWithRunIndexNotice(t *testing.T) {
	d := testutil.NewDB(t, db.Migrations)
	idx, err := vecindex.Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = idx.Close() })
	fake := embedtest.NewFake(16)
	s := semantic.NewSearcher(semantic.SearcherDeps{DB: d, Index: idx, Provider: fake, Settings: embed.Settings{ChunkChars: 1200}})
	h := search.NewHybrid(search.New(d), s, search.HybridOptions{})
	for range 2 {
		res, err := h.Search(bg, search.Query{Text: "lemur"})
		if err != nil {
			t.Fatal(err)
		}
		if res.Mode != search.ModeKeywordFallback || res.Notice != fallbackNotice("the semantic index is empty: run ralph index") {
			t.Fatalf("res = %+v", res)
		}
	}
	if len(fake.Calls()) != 0 {
		t.Errorf("provider calls = %v", fake.Calls())
	}
	u := units.NewService(d)
	if _, err := u.Create(bg, units.NewUnit{Level: units.LevelBrief, Title: "lemur", Body: "x"}); err != nil {
		t.Fatal(err)
	}
	res, err := h.Search(bg, search.Query{Text: "lemur"})
	if err != nil || res.Mode != search.ModeHybrid {
		t.Errorf("the empty index was remembered: %+v %v", res, err)
	}
}

func TestHybridBreakerSkipsProviderFor60Seconds(t *testing.T) {
	e := newFenv(t)
	e.task("lemur feeding", "fruit")
	e.sync()
	e.task("lemur extra", "changed")
	h := e.hybrid(search.HybridOptions{})
	e.fake.FailWith(unavailable())
	first := e.search(h, "lemur")
	calls := len(e.fake.Calls())
	e.fake.SetDelay(5 * time.Second)
	start := time.Now()
	second := e.search(h, "lemur")
	if time.Since(start) > time.Second || len(e.fake.Calls()) != calls {
		t.Errorf("second search called the provider or waited")
	}
	if second.Notice != first.Notice || second.Mode != search.ModeKeywordFallback {
		t.Errorf("second = %+v, first %+v", second, first)
	}
	e.advance(59 * time.Second)
	e.search(h, "lemur")
	if len(e.fake.Calls()) != calls {
		t.Error("tried again before 60 seconds")
	}
	e.advance(2 * time.Second)
	e.fake.Recover()
	e.fake.SetDelay(0)
	res := e.search(h, "lemur")
	if len(e.fake.Calls()) == calls || res.Mode != search.ModeHybrid || res.Notice != "" {
		t.Errorf("did not recover: %+v", res)
	}
}

func TestHybridBreakerOnlyForOutages(t *testing.T) {
	e := newFenv(t)
	e.task("lemur feeding", "fruit")
	h := e.hybrid(search.HybridOptions{})
	e.fake.FailWith(errors.New("boom"))
	if _, err := h.Search(bg, search.Query{Text: "lemur"}); err == nil {
		t.Fatal("expected an error")
	}
	e.fake.Recover()
	e.sync()
	if res := e.search(h, "lemur"); res.Mode != search.ModeHybrid {
		t.Errorf("res = %+v", res)
	}
}

func TestHybridCatchUpEmbedsUpToFiveRecords(t *testing.T) {
	e := newFenv(t)
	e.task("zebra stripes", "black")
	e.sync()
	h := e.hybrid(search.HybridOptions{})
	e.task("quokka smiling", "photo")
	res := e.search(h, "quokka")
	if res.Mode != search.ModeHybrid || res.Notice != "" || len(res.Hits) == 0 || res.Hits[0].Title != "quokka smiling" || len(res.Hits[0].Sources) != 2 {
		t.Errorf("res = %+v", res)
	}
	st, _ := e.index.Stats(bg)
	if st.Records != 4 {
		t.Errorf("records indexed = %d", st.Records)
	}
}

func TestHybridCatchUpBehindNoticeSingularAndPlural(t *testing.T) {
	e := newFenv(t)
	e.task("seed", "seed")
	e.sync()
	h := e.hybrid(search.HybridOptions{})
	for range 7 {
		e.task("lemur", "new")
	}
	res := e.search(h, "lemur")
	if res.Mode != search.ModeHybrid || res.Notice != "semantic index is behind by 2 records: run ralph index" {
		t.Fatalf("res = %+v", res)
	}
	e.task("lemur", "more")
	res = e.search(h, "lemur")
	if res.Notice != "" {
		t.Fatalf("res = %+v", res)
	}
	e2 := newFenv(t)
	e2.task("seed", "seed")
	e2.sync()
	h2 := e2.hybrid(search.HybridOptions{CatchUpRecords: 1})
	e2.task("lemur", "a")
	e2.task("lemur", "b")
	res = e2.search(h2, "lemur")
	if res.Notice != "semantic index is behind by 1 record: run ralph index" {
		t.Fatalf("res = %+v", res)
	}
}

func TestHybridCatchUpFailureIsFallbackNotError(t *testing.T) {
	e := newFenv(t)
	e.task("lemur feeding", "fruit")
	e.sync()
	e.task("lemur new", "x")
	e.fake.FailWith(unavailable())
	res, err := e.hybrid(search.HybridOptions{}).Search(bg, search.Query{Text: "lemur"})
	if err != nil || res.Mode != search.ModeKeywordFallback {
		t.Errorf("res %+v err %v", res, err)
	}
}

func TestHybridCancelledContextIsAnError(t *testing.T) {
	e := newFenv(t)
	e.task("lemur feeding", "fruit")
	h := e.hybrid(search.HybridOptions{})
	ctx, cancel := context.WithCancel(bg)
	cancel()
	if _, err := h.Search(ctx, search.Query{Text: "lemur"}); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v", err)
	}
	e.fake.FailWith(unavailable())
	e.fake.Recover()
	if res := e.search(h, "lemur"); res.Mode == search.ModeKeywordFallback && strings.Contains(res.Notice, "cancel") {
		t.Errorf("cancellation was remembered: %+v", res)
	}
}

func TestHybridOtherErrorsAreReturned(t *testing.T) {
	e := newFenv(t)
	e.task("lemur feeding", "fruit")
	e.fake.FailWith(errors.New("disk on fire"))
	_, err := e.hybrid(search.HybridOptions{}).Search(bg, search.Query{Text: "lemur"})
	if err == nil || !strings.Contains(err.Error(), "disk on fire") {
		t.Errorf("err = %v", err)
	}
}

func TestHybridNoFallbackWhenHealthy(t *testing.T) {
	e := newFenv(t)
	e.task("lemur feeding", "fruit")
	e.sync()
	res := e.search(e.hybrid(search.HybridOptions{}), "lemur")
	if res.Mode != search.ModeHybrid || res.Notice != "" {
		t.Errorf("res = %+v", res)
	}
}

func TestHybridConcurrentSearchesRaceFree(t *testing.T) {
	e := newFenv(t)
	e.task("lemur feeding", "fruit")
	e.sync()
	h := e.hybrid(search.HybridOptions{})
	e.fake.FailWith(unavailable())
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 5 {
				if _, err := h.Search(bg, search.Query{Text: "lemur"}); err != nil {
					t.Error(err)
				}
				e.advance(time.Second)
			}
		}()
	}
	wg.Wait()
}
