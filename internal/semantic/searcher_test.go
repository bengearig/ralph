package semantic

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/bengearig/ralph/internal/embed"
	"github.com/bengearig/ralph/internal/embed/embedtest"
	"github.com/bengearig/ralph/internal/search"
	"github.com/bengearig/ralph/internal/units"
	"github.com/bengearig/ralph/internal/vecindex"
)

// topics is a provider whose vectors are designed: a text gets the vector of the first
// topic word it contains, so records and queries that share no words can still be close.
type topics struct {
	mu    sync.Mutex
	rules []topicRule
	calls [][]string
}

type topicRule struct {
	word string
	vec  []float32
}

func unit3(x, y, z float64) []float32 {
	n := math.Sqrt(x*x + y*y + z*z)
	return []float32{float32(x / n), float32(y / n), float32(z / n)}
}

func (p *topics) Embed(_ context.Context, texts []string) ([][]float32, error) {
	p.mu.Lock()
	p.calls = append(p.calls, slices.Clone(texts))
	p.mu.Unlock()
	out := make([][]float32, len(texts))
	for i, t := range texts {
		out[i] = unit3(0, 0, 1)
		for _, r := range p.rules {
			if strings.Contains(t, r.word) {
				out[i] = r.vec
				break
			}
		}
	}
	return out, nil
}

func (p *topics) Describe(context.Context) (embed.Info, error) {
	return embed.Info{Model: "topics"}, nil
}

func newTopics() *topics {
	return &topics{rules: []topicRule{
		{"lemur", unit3(1, 0, 0)}, {"monkey", unit3(1, 0.05, 0)}, {"zebra", unit3(0, 1, 0)},
	}}
}

func (e *env) searcher(p embed.Provider) *Searcher {
	return NewSearcher(SearcherDeps{DB: e.d, Index: e.index, Provider: p, Settings: e.settings})
}

func (e *env) syncWith(p embed.Provider) {
	e.t.Helper()
	d := e.deps()
	d.Provider = p
	if _, err := Sync(ctx, d, SyncOptions{}); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) query(s *Searcher, q search.SemanticQuery) []search.SemanticHit {
	e.t.Helper()
	if q.Depth == 0 {
		q.Depth = 30
	}
	hits, err := s.Query(ctx, q)
	if err != nil {
		e.t.Fatal(err)
	}
	return hits
}

func hitRefs(hits []search.SemanticHit) []string {
	out := []string{}
	for _, h := range hits {
		out = append(out, string(h.Kind)+":"+h.ID)
	}
	return out
}

func TestSearcherFindsRelatedRecordWithoutSharedWords(t *testing.T) {
	e := newEnv(t)
	p := newTopics()
	lemur := e.task("Feed the lemur", "fruit twice daily", "")
	zebra := e.task("Paint the zebra", "stripes", "")
	e.syncWith(p)
	hits := e.query(e.searcher(p), search.SemanticQuery{Text: "monkey", Kinds: []search.Kind{search.KindTask}})
	if len(hits) < 2 || hits[0].ID != lemur.ID || hits[1].ID != zebra.ID {
		t.Fatalf("hits = %v", hitRefs(hits))
	}
	h := hits[0]
	if h.Kind != search.KindTask || h.Title != "Feed the lemur" || h.Status != string(units.StateProposed) || h.Excerpt == "" || h.Score <= hits[1].Score || h.Inactive || h.Unit != "" {
		t.Errorf("hit = %+v", h)
	}
}

func TestSearcherDropsEditedRecordUntilReindexed(t *testing.T) {
	e := newEnv(t)
	p := newTopics()
	task := e.task("Feed the lemur", "fruit twice daily", "")
	e.syncWith(p)
	s := e.searcher(p)
	e.exec(`UPDATE units SET body = 'nothing about animals' WHERE id = ?`, task.ID)
	for _, h := range e.query(s, search.SemanticQuery{Text: "monkey"}) {
		if h.ID == task.ID {
			t.Fatalf("stale record returned: %+v", h)
		}
	}
	e.syncWith(p)
	found := false
	for _, h := range e.query(s, search.SemanticQuery{Text: "monkey"}) {
		found = found || h.ID == task.ID
	}
	if !found {
		t.Error("reindexed record not found")
	}
}

func TestSearcherDropsDeletedRecord(t *testing.T) {
	e := newEnv(t)
	p := newTopics()
	task := e.task("Feed the lemur", "fruit", "")
	note := e.note(task.ID, "the lemur bites")
	doc := e.doc("Lemur habitat", "enclosure")
	e.syncWith(p)
	e.exec(`DELETE FROM notes WHERE id = ?`, note.ID)
	e.exec(`DELETE FROM documents WHERE id = ?`, doc.ID)
	refs := hitRefs(e.query(e.searcher(p), search.SemanticQuery{Text: "monkey"}))
	if slices.Contains(refs, "document:"+doc.ID) || slices.Contains(refs, "note:"+task.ID) || !slices.Contains(refs, "task:"+task.ID) {
		t.Errorf("hits = %v", refs)
	}
}

func TestSearcherBestChunkPerRecord(t *testing.T) {
	e := newEnv(t)
	e.settings.ChunkChars = 200
	p := newTopics()
	filler := strings.Repeat("plain words here. ", 30)
	body := filler + "\n\nthe zebra paragraph is long enough to be its own chunk. " + strings.Repeat("more. ", 40) + "\n\n" + filler
	doc := e.doc("Mixed", body)
	e.task("Other", "lemur", "")
	e.syncWith(p)
	if info := mustRecords(t, e)[vecindex.Key{Kind: "document", Ref: doc.ID}]; info.Chunks < 3 {
		t.Fatalf("chunks = %d", info.Chunks)
	}
	hits := e.query(e.searcher(p), search.SemanticQuery{Text: "zebra", Kinds: []search.Kind{search.KindDocument}})
	if len(hits) != 1 || hits[0].ID != doc.ID || !strings.Contains(hits[0].Excerpt, "zebra") {
		t.Errorf("hits = %+v", hits)
	}
}

func mustRecords(t *testing.T, e *env) map[vecindex.Key]vecindex.RecordInfo {
	t.Helper()
	m, err := e.index.Records(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestSearcherKindsFilter(t *testing.T) {
	e := newEnv(t)
	p := newTopics()
	task := e.task("Feed the lemur", "fruit", "")
	e.doc("Lemur habitat", "enclosure")
	e.note(task.ID, "lemur note")
	e.syncWith(p)
	s := e.searcher(p)
	kinds := func(ks ...search.Kind) []string {
		return hitRefs(e.query(s, search.SemanticQuery{Text: "monkey", Kinds: ks}))
	}
	for _, k := range []search.Kind{search.KindTask, search.KindDocument, search.KindNote} {
		got := kinds(k)
		if len(got) != 1 || !strings.HasPrefix(got[0], string(k)+":") {
			t.Errorf("kind %s: %v", k, got)
		}
	}
	if got := kinds(search.KindTask, search.KindNote); len(got) != 2 {
		t.Errorf("two kinds: %v", got)
	}
	if got := kinds(); len(got) < 5 {
		t.Errorf("no filter: %v", got)
	}
}

type inactiveFixture struct {
	archived, superseded string
	cancelled            string
	note                 units.Note
	active               string
}

func (e *env) inactiveFixture() inactiveFixture {
	e.t.Helper()
	var f inactiveFixture
	a := e.doc("Archived zeta", "zeta lemur")
	s := e.doc("Superseded zeta", "zeta lemur")
	repl := e.doc("Replacement", "unrelated")
	if _, err := e.docs.Archive(ctx, a.ID, a.Fingerprint); err != nil {
		e.t.Fatal(err)
	}
	if _, err := e.docs.Supersede(ctx, s.ID, s.Fingerprint, repl.ID); err != nil {
		e.t.Fatal(err)
	}
	c := e.task("Cancelled zeta", "zeta lemur", "")
	if _, err := e.units.SetState(ctx, c.ID, c.Fingerprint, units.StateCancelled, "dropped"); err != nil {
		e.t.Fatal(err)
	}
	f.archived, f.superseded, f.cancelled = a.ID, s.ID, c.ID
	f.note = e.note(c.ID, "zeta lemur note")
	f.active = e.task("Active zeta", "zeta lemur", "").ID
	return f
}

func TestSearcherInactiveParity(t *testing.T) {
	e := newEnv(t)
	p := newTopics()
	f := e.inactiveFixture()
	e.syncWith(p)
	kw := search.New(e.d)
	s := e.searcher(p)
	for _, all := range []bool{false, true} {
		kres, err := kw.Search(ctx, search.Query{Text: "zeta", IncludeInactive: all})
		if err != nil {
			t.Fatal(err)
		}
		want := map[string]bool{}
		for _, h := range kres.Hits {
			want[string(h.Kind)+":"+h.ID+":"+h.Status] = h.Inactive
		}
		got := map[string]bool{}
		for _, h := range e.query(s, search.SemanticQuery{Text: "zeta lemur", IncludeInactive: all}) {
			if _, ok := want[string(h.Kind)+":"+h.ID+":"+h.Status]; ok {
				got[string(h.Kind)+":"+h.ID+":"+h.Status] = h.Inactive
			}
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("all=%v: semantic %v, keyword %v", all, got, want)
		}
		if all && len(want) != 5 {
			t.Errorf("fixture hits = %v", want)
		}
		if !all && len(want) != 1 {
			t.Errorf("active hits = %v (%s)", want, f.active)
		}
	}
}

func TestSearcherIncludeInactiveMarks(t *testing.T) {
	e := newEnv(t)
	p := newTopics()
	f := e.inactiveFixture()
	e.syncWith(p)
	s := e.searcher(p)
	var marked []string
	for _, h := range e.query(s, search.SemanticQuery{Text: "lemur", IncludeInactive: true}) {
		if h.Inactive {
			marked = append(marked, string(h.Kind)+":"+h.ID)
		}
	}
	slices.Sort(marked)
	marked = slices.Compact(marked)
	want := []string{"document:" + f.archived, "document:" + f.superseded, "note:" + f.cancelled, "task:" + f.cancelled}
	slices.Sort(want)
	if !slices.Equal(marked, want) {
		t.Errorf("marked = %v, want %v", marked, want)
	}
	for _, h := range e.query(s, search.SemanticQuery{Text: "lemur", IncludeInactive: true, Kinds: []search.Kind{search.KindNote}}) {
		if h.ID != f.cancelled || h.Unit != f.cancelled || h.Title != "Cancelled zeta" || h.Status != string(units.StateCancelled) {
			t.Errorf("note hit = %+v", h)
		}
	}
}

func TestSearcherUsesQueryPrefix(t *testing.T) {
	e := newEnv(t)
	p := newTopics()
	e.task("Feed the lemur", "fruit", "")
	e.syncWith(p)
	e.settings.QueryPrefix = "Q: "
	p.calls = nil
	e.query(e.searcher(p), search.SemanticQuery{Text: "monkey"})
	if len(p.calls) != 1 || !reflect.DeepEqual(p.calls[0], []string{"Q: monkey"}) {
		t.Errorf("calls = %q", p.calls)
	}
	e.settings = embed.Settings{Model: "nomic-embed-text"}
	p.calls = nil
	e.query(e.searcher(p), search.SemanticQuery{Text: "monkey"})
	if !reflect.DeepEqual(p.calls, [][]string{{"search_query: monkey"}}) {
		t.Errorf("nomic calls = %q", p.calls)
	}
}

func TestSearcherEmptyIndex(t *testing.T) {
	e := newEnv(t)
	e.task("Feed the lemur", "fruit", "")
	hits := e.query(e.searcher(e.fake), search.SemanticQuery{Text: "monkey"})
	if hits == nil || len(hits) != 0 {
		t.Errorf("hits = %#v", hits)
	}
}

func TestSearcherDimensionMismatchError(t *testing.T) {
	e := newEnv(t)
	e.task("Feed the lemur", "fruit", "")
	e.mustSync()
	_, err := e.searcher(embedtest.NewFake(8)).Query(ctx, search.SemanticQuery{Text: "monkey", Depth: 10})
	if !errors.Is(err, vecindex.ErrDimensions) {
		t.Errorf("err = %v", err)
	}
}

func TestSearcherReadsOnly(t *testing.T) {
	e := newEnv(t)
	p := newTopics()
	task := e.task("Feed the lemur", "fruit", "")
	e.note(task.ID, "lemur note")
	e.doc("Lemur habitat", "enclosure")
	e.syncWith(p)
	e.exec(`UPDATE units SET body = 'changed' WHERE id = ?`, task.ID)
	snapshot := func() string {
		recs, err := e.index.Records(ctx)
		if err != nil {
			t.Fatal(err)
		}
		st, err := e.index.Stats(ctx)
		if err != nil {
			t.Fatal(err)
		}
		all, err := Records(ctx, e.d)
		if err != nil {
			t.Fatal(err)
		}
		return fmt.Sprint(recs, st.Records, st.Chunks, all)
	}
	before := snapshot()
	e.query(e.searcher(p), search.SemanticQuery{Text: "monkey"})
	if snapshot() != before {
		t.Error("a search changed the index or the store")
	}
}
