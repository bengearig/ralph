package search

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/bengearig/ralph/internal/db"
	"github.com/bengearig/ralph/internal/documents"
	"github.com/bengearig/ralph/internal/testutil"
	"github.com/bengearig/ralph/internal/units"
)

type env struct {
	t     *testing.T
	d     *db.DB
	units units.Service
	docs  documents.Service
	svc   Service
	brief units.Unit
	slice units.Unit
}

func newEnv(t *testing.T) *env {
	t.Helper()
	d := testutil.NewDB(t, db.Migrations)
	e := &env{t: t, d: d, units: units.NewService(d), docs: documents.NewService(d), svc: New(d)}
	ctx := context.Background()
	var err error
	if e.brief, err = e.units.Create(ctx, units.NewUnit{Level: units.LevelBrief, Title: "brief", Body: "brief body"}); err != nil {
		t.Fatal(err)
	}
	if e.slice, err = e.units.Create(ctx, units.NewUnit{Level: units.LevelSlice, ParentID: e.brief.ID, Title: "slice", Body: "slice body"}); err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *env) index(rowid int, title, body, kind, ref, unit string) {
	e.t.Helper()
	if _, err := e.d.SQL().Exec("INSERT INTO search_fts (rowid, title, body, kind, ref, unit) VALUES (?, ?, ?, ?, ?, ?)",
		rowid, title, body, kind, ref, unit); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) task(title, body string) units.Unit {
	e.t.Helper()
	u, err := e.units.Create(context.Background(), units.NewUnit{Level: units.LevelTask, ParentID: e.slice.ID, Title: title, Body: body})
	if err != nil {
		e.t.Fatal(err)
	}
	e.index(u.Number*5+2, title, body, "task", u.ID, "")
	return u
}

func (e *env) doc(title, body string) documents.Document {
	e.t.Helper()
	d, err := e.docs.Create(context.Background(), documents.NewDocument{Kind: documents.KindDesign, Title: title, Body: body})
	if err != nil {
		e.t.Fatal(err)
	}
	e.index(d.Number*5+3, title, body, "document", d.ID, "")
	return d
}

func (e *env) note(unitID, body string) units.Note {
	e.t.Helper()
	n, err := e.units.AddNote(context.Background(), unitID, units.NoteGeneral, body)
	if err != nil {
		e.t.Fatal(err)
	}
	e.index(n.ID*5+4, "", body, "note", fmt.Sprint(n.ID), unitID)
	return n
}

func (e *env) search(q Query) Result {
	e.t.Helper()
	r, err := e.svc.Search(context.Background(), q)
	if err != nil {
		e.t.Fatalf("search %+v: %v", q, err)
	}
	return r
}

func ids(r Result) []string {
	out := []string{}
	for _, h := range r.Hits {
		out = append(out, h.ID)
	}
	return out
}

func TestFTSSyntaxIsInert(t *testing.T) {
	e := newEnv(t)
	e.task("plain title", "plain body")
	for _, in := range []string{"AND", "OR", "NOT", "foo*", "title:foo", "(a", "NEAR(a b)", "-x", "a OR b", `"unterminated`, "^start", "a:b:c"} {
		if _, err := e.svc.Search(context.Background(), Query{Text: in}); err != nil {
			t.Errorf("%q: %v", in, err)
		}
	}
	e.task("not and or", "near")
	if got := ids(e.search(Query{Text: "NOT AND OR NEAR"})); len(got) != 1 {
		t.Errorf("operator words as plain words: %v", got)
	}
	if got := e.search(Query{Text: "plai*"}); len(got.Hits) != 0 {
		t.Errorf("prefix star acted as syntax: %v", ids(got))
	}
}

func TestKeywordFindsStemmedWords(t *testing.T) {
	e := newEnv(t)
	u := e.task("Add a theme", "")
	if got := ids(e.search(Query{Text: "themes"})); len(got) != 1 || got[0] != u.ID {
		t.Errorf("got %v", got)
	}
}

func TestKeywordIgnoresCaseAndDiacritics(t *testing.T) {
	e := newEnv(t)
	u := e.task("Café menu", "")
	for _, q := range []string{"CAFE", "cafÉ", "café"} {
		if got := ids(e.search(Query{Text: q})); len(got) != 1 || got[0] != u.ID {
			t.Errorf("%q: %v", q, got)
		}
	}
}

func TestKeywordRanksTitleAboveBody(t *testing.T) {
	e := newEnv(t)
	body := e.task("other", "the zebra is here")
	title := e.task("zebra", "other")
	if got := ids(e.search(Query{Text: "zebra"})); len(got) != 2 || got[0] != title.ID || got[1] != body.ID {
		t.Errorf("got %v", got)
	}
}

func TestKeywordScoreLargerIsBetter(t *testing.T) {
	e := newEnv(t)
	e.task("other", "the zebra is here")
	e.task("zebra", "other")
	r := e.search(Query{Text: "zebra"})
	if len(r.Hits) != 2 || r.Hits[0].Score < r.Hits[1].Score || r.Hits[0].Score <= 0 {
		t.Errorf("scores = %+v", r.Hits)
	}
}

func TestKeywordTiesOrderedByRowid(t *testing.T) {
	e := newEnv(t)
	var want []string
	for i := 0; i < 4; i++ {
		want = append(want, e.task("same title", "same body").ID)
	}
	got := ids(e.search(Query{Text: "same"}))
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestKeywordStrictAndRequiresAllTerms(t *testing.T) {
	e := newEnv(t)
	both := e.task("apple pear", "")
	e.task("apple", "")
	e.task("pear", "")
	r := e.search(Query{Text: "apple pear"})
	if r.Relaxed || len(r.Hits) != 1 || r.Hits[0].ID != both.ID {
		t.Errorf("result = %+v", r)
	}
}

func TestKeywordRelaxedFallback(t *testing.T) {
	e := newEnv(t)
	a := e.task("apple", "")
	b := e.task("pear", "")
	e.task("grape", "")
	r := e.search(Query{Text: "apple pear"})
	if !r.Relaxed || len(r.Hits) != 2 {
		t.Fatalf("result = %+v", r)
	}
	got := map[string]bool{r.Hits[0].ID: true, r.Hits[1].ID: true}
	if !got[a.ID] || !got[b.ID] {
		t.Errorf("hits = %v", ids(r))
	}
}

func TestKeywordRelaxedFalseWhenStrictMatches(t *testing.T) {
	e := newEnv(t)
	e.task("apple pear", "")
	if r := e.search(Query{Text: "apple pear"}); r.Relaxed {
		t.Error("relaxed with a strict match")
	}
}

func TestKeywordPhrase(t *testing.T) {
	e := newEnv(t)
	together := e.task("mode toggle", "")
	e.task("mode and a toggle", "")
	r := e.search(Query{Text: `"mode toggle"`})
	if r.Relaxed || len(r.Hits) != 1 || r.Hits[0].ID != together.ID {
		t.Errorf("result = %+v", r)
	}
}

func TestKeywordNoHitsIsEmptyNotNil(t *testing.T) {
	e := newEnv(t)
	e.task("apple", "")
	for _, q := range []string{"zzz", "zzz yyy"} {
		r := e.search(Query{Text: q})
		if r.Hits == nil || len(r.Hits) != 0 || r.Relaxed {
			t.Errorf("%q: result = %+v", q, r)
		}
	}
}

func TestKeywordLimit(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < 25; i++ {
		e.task("common", "")
	}
	for limit, want := range map[int]int{0: 20, 1: 1, 100: 25} {
		if r := e.search(Query{Text: "common", Limit: limit}); len(r.Hits) != want {
			t.Errorf("limit %d: %d hits, want %d", limit, len(r.Hits), want)
		}
	}
	for _, limit := range []int{-1, 101} {
		if _, err := e.svc.Search(context.Background(), Query{Text: "common", Limit: limit}); !errors.Is(err, ErrInvalid) {
			t.Errorf("limit %d: err = %v", limit, err)
		}
	}
}

func TestKeywordKindsFilter(t *testing.T) {
	e := newEnv(t)
	e.index(e.brief.Number*5, "shared", "", "brief", e.brief.ID, "")
	e.index(e.slice.Number*5+1, "shared", "", "slice", e.slice.ID, "")
	task := e.task("shared", "")
	e.doc("shared", "text")
	e.note(task.ID, "shared")
	count := func(kinds ...Kind) map[Kind]int {
		m := map[Kind]int{}
		for _, h := range e.search(Query{Text: "shared", Kinds: kinds}).Hits {
			m[h.Kind]++
		}
		return m
	}
	if m := count(); len(m) != 5 {
		t.Errorf("all = %v", m)
	}
	for _, k := range []Kind{KindBrief, KindSlice, KindTask, KindNote, KindDocument} {
		if m := count(k); len(m) != 1 || m[k] != 1 {
			t.Errorf("%s alone = %v", k, m)
		}
	}
	if m := count(KindTask, KindDocument); len(m) != 2 || m[KindTask] != 1 || m[KindDocument] != 1 {
		t.Errorf("two kinds = %v", m)
	}
}

func TestKeywordUnknownKindInvalid(t *testing.T) {
	e := newEnv(t)
	if _, err := e.svc.Search(context.Background(), Query{Text: "x", Kinds: []Kind{"bogus"}}); !errors.Is(err, ErrInvalid) {
		t.Errorf("err = %v", err)
	}
}

func TestQueryValidation(t *testing.T) {
	e := newEnv(t)
	for _, q := range []Query{{Text: ""}, {Text: "..."}, {Text: "x", Limit: -5}, {Text: "x", Limit: 500}} {
		if _, err := e.svc.Search(context.Background(), q); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v: err = %v", q, err)
		}
	}
}

func TestHitForBrief(t *testing.T) {
	e := newEnv(t)
	e.index(e.brief.Number*5, "Quokka brief", "body", "brief", e.brief.ID, "")
	h := e.search(Query{Text: "quokka"}).Hits[0]
	if h.Kind != KindBrief || h.ID != e.brief.ID || h.Title != "brief" || h.Status != "" {
		t.Errorf("hit = %+v", h)
	}
}

func TestHitForSlice(t *testing.T) {
	e := newEnv(t)
	e.index(e.slice.Number*5+1, "Quokka slice", "body", "slice", e.slice.ID, "")
	h := e.search(Query{Text: "quokka"}).Hits[0]
	if h.Kind != KindSlice || h.ID != e.slice.ID || h.Title != "slice" || h.Status != "" {
		t.Errorf("hit = %+v", h)
	}
}

func TestHitForTaskHasState(t *testing.T) {
	e := newEnv(t)
	u := e.task("quokka", "")
	h := e.search(Query{Text: "quokka"}).Hits[0]
	if h.Kind != KindTask || h.ID != u.ID || h.Title != "quokka" || h.Status != string(units.StateProposed) {
		t.Errorf("hit = %+v", h)
	}
}

func TestHitForDocumentHasStatus(t *testing.T) {
	e := newEnv(t)
	d := e.doc("quokka design", "text")
	h := e.search(Query{Text: "quokka"}).Hits[0]
	if h.Kind != KindDocument || h.ID != d.ID || h.Title != "quokka design" || h.Status != "ACTIVE" {
		t.Errorf("hit = %+v", h)
	}
	if _, err := e.docs.Archive(context.Background(), d.ID, d.Fingerprint); err != nil {
		t.Fatal(err)
	}
	if h := e.search(Query{Text: "quokka"}).Hits[0]; h.Status != "ARCHIVED" {
		t.Errorf("status = %q", h.Status)
	}
}

func TestHitForNoteNamesTheUnit(t *testing.T) {
	e := newEnv(t)
	u := e.task("the unit", "")
	e.note(u.ID, "quokka remark")
	h := e.search(Query{Text: "quokka"}).Hits[0]
	if h.Kind != KindNote || h.ID != u.ID || h.Title != "the unit" || h.Unit != "" {
		t.Errorf("hit = %+v", h)
	}
}

func TestHitSnippetMarksMatch(t *testing.T) {
	e := newEnv(t)
	e.task("title", "the quick brown fox jumps")
	if s := e.search(Query{Text: "fox"}).Hits[0].Snippet; !strings.Contains(s, "[fox]") {
		t.Errorf("snippet = %q", s)
	}
}

func TestHitSnippetFallsBackToBodyStart(t *testing.T) {
	e := newEnv(t)
	body := strings.Repeat("alpha beta gamma ", 20)
	e.task("quokka", body)
	want := strings.TrimSpace(body[:119]) + "…"
	if s := e.search(Query{Text: "quokka"}).Hits[0].Snippet; s != want {
		t.Errorf("snippet = %q, want %q", s, want)
	}
	e.task("zebra", "short body")
	if s := e.search(Query{Text: "zebra"}).Hits[0].Snippet; s != "short body" {
		t.Errorf("short snippet = %q", s)
	}
}

func TestKeywordSkipsEntryWithoutRecord(t *testing.T) {
	e := newEnv(t)
	e.index(9995, "ghost", "", "task", "T-999", "")
	e.index(9994, "ghost", "", "document", "D-999", "")
	e.index(9999*5+4, "", "ghost", "note", "999", "T-999")
	live := e.task("ghost", "")
	r := e.search(Query{Text: "ghost", Limit: 1})
	if len(r.Hits) != 1 || r.Hits[0].ID != live.ID {
		t.Errorf("result = %+v", r)
	}
}

func TestResultModeIsKeyword(t *testing.T) {
	e := newEnv(t)
	e.task("apple", "")
	for _, q := range []string{"apple", "nothing"} {
		if m := e.search(Query{Text: q}).Mode; m != "keyword" {
			t.Errorf("mode = %q", m)
		}
	}
}

func TestKeywordConcurrentReads(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < 5; i++ {
		e.task("common", "words")
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				if r, err := e.svc.Search(context.Background(), Query{Text: "common"}); err != nil || len(r.Hits) != 5 {
					t.Errorf("hits = %d, err = %v", len(r.Hits), err)
					return
				}
			}
		}()
	}
	wg.Wait()
}
