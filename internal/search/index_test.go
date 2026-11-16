package search

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/bengearig/ralph/internal/db"
	"github.com/bengearig/ralph/internal/documents"
	"github.com/bengearig/ralph/internal/testutil"
	"github.com/bengearig/ralph/internal/units"
)

type indexEnv struct {
	t     *testing.T
	d     *db.DB
	units units.Service
	docs  documents.Service
	svc   Service
	brief units.Unit
	slice units.Unit
}

func newIndexEnv(t *testing.T) *indexEnv {
	t.Helper()
	d := testutil.NewDB(t, db.Migrations)
	e := &indexEnv{t: t, d: d, units: units.NewService(d), docs: documents.NewService(d), svc: New(d)}
	ctx := context.Background()
	var err error
	if e.brief, err = e.units.Create(ctx, units.NewUnit{Level: units.LevelBrief, Title: "brief title", Body: "brief body"}); err != nil {
		t.Fatal(err)
	}
	if e.slice, err = e.units.Create(ctx, units.NewUnit{Level: units.LevelSlice, ParentID: e.brief.ID, Title: "slice title", Body: "slice body"}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.assertCountMatches() })
	return e
}

func (e *indexEnv) count(query string) int {
	e.t.Helper()
	var n int
	if err := e.d.SQL().QueryRow(query).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func (e *indexEnv) assertCountMatches() {
	e.t.Helper()
	records := e.count("SELECT (SELECT count(*) FROM units) + (SELECT count(*) FROM documents) + (SELECT count(*) FROM notes)")
	entries := e.count("SELECT count(*) FROM search_fts")
	if records != entries {
		e.t.Errorf("records = %d, index entries = %d", records, entries)
	}
}

func (e *indexEnv) task(title, body, criteria string) units.Unit {
	e.t.Helper()
	u, err := e.units.Create(context.Background(), units.NewUnit{Level: units.LevelTask, ParentID: e.slice.ID, Title: title, Body: body, AcceptanceCriteria: criteria})
	if err != nil {
		e.t.Fatal(err)
	}
	return u
}

func (e *indexEnv) doc(title, body string) documents.Document {
	e.t.Helper()
	d, err := e.docs.Create(context.Background(), documents.NewDocument{Kind: documents.KindDesign, Title: title, Body: body})
	if err != nil {
		e.t.Fatal(err)
	}
	return d
}

func (e *indexEnv) hits(text string) []Hit {
	e.t.Helper()
	r, err := e.svc.Search(context.Background(), Query{Text: text, IncludeInactive: true})
	if err != nil {
		e.t.Fatalf("search %q: %v", text, err)
	}
	return r.Hits
}

func (e *indexEnv) wantHit(text string, kind Kind, id string) Hit {
	e.t.Helper()
	hs := e.hits(text)
	if len(hs) != 1 || hs[0].Kind != kind || hs[0].ID != id {
		e.t.Fatalf("search %q = %+v, want one %s hit on %s", text, hs, kind, id)
	}
	return hs[0]
}

func (e *indexEnv) wantNone(text string) {
	e.t.Helper()
	if hs := e.hits(text); len(hs) != 0 {
		e.t.Errorf("search %q = %+v, want no hits", text, hs)
	}
}

func str(s string) *string { return &s }

func TestIndexNewBriefSliceTaskFound(t *testing.T) {
	e := newIndexEnv(t)
	task := e.task("zebra task", "giraffe text", "")
	for _, c := range []struct {
		kind        Kind
		id          string
		title, body string
	}{
		{KindBrief, e.brief.ID, "brief title", "brief body"},
		{KindSlice, e.slice.ID, "slice title", "slice body"},
		{KindTask, task.ID, "zebra", "giraffe"},
	} {
		for _, text := range []string{c.title, c.body} {
			r, err := e.svc.Search(context.Background(), Query{Text: text, Kinds: []Kind{c.kind}})
			if err != nil {
				t.Fatal(err)
			}
			if len(r.Hits) != 1 || r.Hits[0].ID != c.id {
				t.Errorf("search %q in %s = %+v, want %s", text, c.kind, r.Hits, c.id)
			}
		}
	}
}

func TestIndexTaskCriteriaSearchable(t *testing.T) {
	e := newIndexEnv(t)
	task := e.task("plain", "plain body", "wombat criterion")
	e.wantHit("wombat", KindTask, task.ID)
	e.wantHit("plain body wombat", KindTask, task.ID)
}

func TestIndexNewDocumentFound(t *testing.T) {
	e := newIndexEnv(t)
	d := e.doc("okapi design", "tapir body")
	e.wantHit("okapi", KindDocument, d.ID)
	e.wantHit("tapir", KindDocument, d.ID)
}

func TestIndexNoteFoundAndNamesUnit(t *testing.T) {
	e := newIndexEnv(t)
	task := e.task("lemur task", "", "")
	if _, err := e.units.AddNote(context.Background(), task.ID, units.NoteGeneral, "pangolin remark"); err != nil {
		t.Fatal(err)
	}
	h := e.wantHit("pangolin", KindNote, task.ID)
	if h.Title != "lemur task" {
		t.Errorf("title = %q", h.Title)
	}
}

func TestIndexUnitEditReplacesEntry(t *testing.T) {
	e := newIndexEnv(t)
	task := e.task("old title", "old body", "old criterion")
	u, err := e.units.Update(context.Background(), task.ID, task.Fingerprint, units.Change{
		Title: str("fresh title"), Body: str("fresh body"), AcceptanceCriteria: str("fresh criterion"),
	})
	if err != nil {
		t.Fatal(err)
	}
	e.wantNone("old")
	e.wantHit("fresh", KindTask, u.ID)
	if n := e.count("SELECT count(*) FROM search_fts WHERE ref = '" + u.ID + "'"); n != 1 {
		t.Errorf("entries = %d", n)
	}
}

func TestIndexDocumentEditReplacesEntry(t *testing.T) {
	e := newIndexEnv(t)
	d := e.doc("old heading", "old text")
	d2, err := e.docs.Update(context.Background(), d.ID, d.Fingerprint, documents.Change{Title: str("fresh heading"), Body: str("fresh text")})
	if err != nil {
		t.Fatal(err)
	}
	e.wantNone("old")
	e.wantHit("fresh", KindDocument, d2.ID)
	if n := e.count("SELECT count(*) FROM search_fts WHERE ref = '" + d.ID + "'"); n != 1 {
		t.Errorf("entries = %d", n)
	}
}

func TestIndexStateChangeKeepsEntry(t *testing.T) {
	e := newIndexEnv(t)
	task := e.task("capybara", "gnaw", "bark")
	if h := e.wantHit("capybara", KindTask, task.ID); h.Status != string(units.StateProposed) {
		t.Errorf("status = %q", h.Status)
	}
	if _, err := e.units.SetState(context.Background(), task.ID, task.Fingerprint, units.StateTodo, ""); err != nil {
		t.Fatal(err)
	}
	if h := e.wantHit("capybara", KindTask, task.ID); h.Status != string(units.StateTodo) {
		t.Errorf("status = %q", h.Status)
	}
	if n := e.count("SELECT count(*) FROM search_fts WHERE ref = '" + task.ID + "'"); n != 1 {
		t.Errorf("entries = %d", n)
	}
}

func TestIndexDocumentStatusChangeKeepsEntry(t *testing.T) {
	e := newIndexEnv(t)
	ctx := context.Background()
	d := e.doc("axolotl", "gills")
	repl := e.doc("replacement", "gills")
	check := func(status string) documents.Document {
		t.Helper()
		if h := e.wantHit("axolotl", KindDocument, d.ID); h.Status != status {
			t.Errorf("status = %q, want %s", h.Status, status)
		}
		cur, err := e.docs.Get(ctx, d.ID)
		if err != nil {
			t.Fatal(err)
		}
		return cur
	}
	archived, err := e.docs.Archive(ctx, d.ID, d.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	cur := check("ARCHIVED")
	restored, err := e.docs.Restore(ctx, archived.ID, cur.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	cur = check("ACTIVE")
	if _, err := e.docs.Supersede(ctx, restored.ID, cur.Fingerprint, repl.ID); err != nil {
		t.Fatal(err)
	}
	cur = check("SUPERSEDED")
	if _, err := e.docs.Restore(ctx, d.ID, cur.Fingerprint); err != nil {
		t.Fatal(err)
	}
	check("ACTIVE")
}

func TestIndexNoopDocumentEditKeepsEntry(t *testing.T) {
	e := newIndexEnv(t)
	d := e.doc("narwhal", "tusk")
	if _, err := e.docs.Update(context.Background(), d.ID, d.Fingerprint, documents.Change{Title: str("narwhal"), Body: str("tusk")}); err != nil {
		t.Fatal(err)
	}
	e.wantHit("narwhal tusk", KindDocument, d.ID)
	if n := e.count("SELECT count(*) FROM search_fts WHERE ref = '" + d.ID + "'"); n != 1 {
		t.Errorf("entries = %d", n)
	}
}

func TestIndexEarlierVersionsNotIndexed(t *testing.T) {
	e := newIndexEnv(t)
	d := e.doc("heading", "ancient words")
	if _, err := e.docs.Update(context.Background(), d.ID, d.Fingerprint, documents.Change{Body: str("modern words")}); err != nil {
		t.Fatal(err)
	}
	if n := e.count("SELECT count(*) FROM document_versions"); n == 0 {
		t.Fatal("no earlier version was kept")
	}
	e.wantNone("ancient")
	e.wantHit("modern", KindDocument, d.ID)
}

func TestIndexDeleteRemovesEntry(t *testing.T) {
	e := newIndexEnv(t)
	task := e.task("hedgehog", "", "")
	d := e.doc("platypus", "bill")
	other := e.task("badger", "", "")
	n, err := e.units.AddNote(context.Background(), other.ID, units.NoteGeneral, "marmot")
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []struct {
		sql  string
		args []any
		word string
	}{
		{"DELETE FROM units WHERE id = ?", []any{task.ID}, "hedgehog"},
		{"DELETE FROM documents WHERE id = ?", []any{d.ID}, "platypus"},
		{"DELETE FROM notes WHERE id = ?", []any{n.ID}, "marmot"},
	} {
		if len(e.hits(q.word)) != 1 {
			t.Fatalf("%q not indexed", q.word)
		}
		if _, err := e.d.SQL().Exec(q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
		if n := e.count(fmt.Sprintf("SELECT count(*) FROM search_fts WHERE search_fts MATCH '%s'", q.word)); n != 0 {
			t.Errorf("%q entries after delete = %d", q.word, n)
		}
	}
}

func TestIndexEntryAndRecordAreAtomic(t *testing.T) {
	e := newIndexEnv(t)
	ctx := context.Background()
	const insert = "INSERT INTO units (id, level, number, title, created_at, updated_at) VALUES (?, 'brief', ?, ?, 'x', 'x')"
	boom := errors.New("boom")
	err := e.d.WriteTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, insert, "I-900", 900, "rolled back aardvark"); err != nil {
			return err
		}
		if n := 0; tx.QueryRowContext(ctx, "SELECT count(*) FROM search_fts WHERE search_fts MATCH 'aardvark'").Scan(&n) != nil || n != 1 {
			return errors.New("entry not visible inside the transaction")
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	e.wantNone("aardvark")
	if err := e.d.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, insert, "I-901", 901, "committed ibex")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	e.wantHit("ibex", KindBrief, "I-901")
}

func TestIndexRowidsStableAcrossVacuum(t *testing.T) {
	e := newIndexEnv(t)
	ctx := context.Background()
	var tasks []units.Unit
	for i := range 12 {
		u := e.task(fmt.Sprintf("kestrel%d", i), "", "")
		for j := range 3 {
			var err error
			if u, err = e.units.Update(ctx, u.ID, u.Fingerprint, units.Change{Body: str(fmt.Sprintf("edit%d%d", i, j))}); err != nil {
				t.Fatal(err)
			}
		}
		tasks = append(tasks, u)
	}
	docs := []documents.Document{e.doc("osprey", "one"), e.doc("heron", "two")}
	for i := 0; i < len(tasks); i += 3 {
		if _, err := e.d.SQL().Exec("DELETE FROM units WHERE id = ?", tasks[i].ID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.d.SQL().Exec("VACUUM"); err != nil {
		t.Fatal(err)
	}
	for i, u := range tasks {
		if i%3 == 0 {
			e.wantNone(fmt.Sprintf("kestrel%d", i))
			continue
		}
		e.wantHit(fmt.Sprintf("kestrel%d", i), KindTask, u.ID)
		e.wantHit(fmt.Sprintf("edit%d2", i), KindTask, u.ID)
	}
	e.wantHit("osprey", KindDocument, docs[0].ID)
	e.wantHit("heron", KindDocument, docs[1].ID)
}

func TestIndexRowidsDistinctAcrossKinds(t *testing.T) {
	e := newIndexEnv(t)
	task := e.task("marker", "", "")
	d := e.doc("marker", "marker")
	n, err := e.units.AddNote(context.Background(), task.ID, units.NoteGeneral, "marker")
	if err != nil {
		t.Fatal(err)
	}
	if e.brief.Number != 1 || e.slice.Number != 1 || task.Number != 1 || d.Number != 1 || n.ID != 1 {
		t.Fatalf("numbers = %d %d %d %d %d, want all 1", e.brief.Number, e.slice.Number, task.Number, d.Number, n.ID)
	}
	if got := e.count("SELECT count(DISTINCT rowid) FROM search_fts"); got != 5 {
		t.Errorf("distinct entries = %d, want 5", got)
	}
	r, err := e.svc.Search(context.Background(), Query{Text: "marker title body"})
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[Kind]bool{}
	for _, h := range r.Hits {
		kinds[h.Kind] = true
	}
	for _, k := range []Kind{KindTask, KindDocument, KindNote} {
		if !kinds[k] {
			t.Errorf("kind %s not found in %+v", k, r.Hits)
		}
	}
	e.wantHit("brief title", KindBrief, e.brief.ID)
	e.wantHit("slice title", KindSlice, e.slice.ID)
}

func TestIndexCountMatchesRecords(t *testing.T) {
	e := newIndexEnv(t)
	ctx := context.Background()
	e.assertCountMatches()
	a := e.task("a", "described", "criteria")
	b := e.task("b", "", "")
	d := e.doc("d", "text")
	e.assertCountMatches()
	if _, err := e.units.AddNote(ctx, a.ID, units.NoteGeneral, "note"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.units.Update(ctx, b.ID, b.Fingerprint, units.Change{Title: str("b2")}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.units.SetState(ctx, a.ID, a.Fingerprint, units.StateTodo, ""); err != nil {
		t.Fatal(err)
	}
	d2, err := e.docs.Update(ctx, d.ID, d.Fingerprint, documents.Change{Body: str("more text")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.docs.Archive(ctx, d2.ID, d2.Fingerprint); err != nil {
		t.Fatal(err)
	}
	e.assertCountMatches()
	if _, err := e.d.SQL().Exec("DELETE FROM units WHERE id = ?", b.ID); err != nil {
		t.Fatal(err)
	}
	e.assertCountMatches()
}

func TestIndexConcurrentWritesAndSearch(t *testing.T) {
	e := newIndexEnv(t)
	ctx := context.Background()
	const writers, rounds = 3, 15
	var wg sync.WaitGroup
	errs := make(chan error, 64)
	report := func(err error) {
		select {
		case errs <- err:
		default:
		}
	}
	done := make(chan struct{})
	for w := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range rounds {
				u, err := e.units.Create(ctx, units.NewUnit{Level: units.LevelTask, ParentID: e.slice.ID, Title: fmt.Sprintf("worker%d item%d", w, i), Body: "initial"})
				if err != nil {
					report(err)
					return
				}
				if _, err := e.units.Update(ctx, u.ID, u.Fingerprint, units.Change{Body: str("revised")}); err != nil {
					report(err)
					return
				}
				d, err := e.docs.Create(ctx, documents.NewDocument{Kind: documents.KindDesign, Title: fmt.Sprintf("worker%d paper%d", w, i), Body: "initial"})
				if err != nil {
					report(err)
					return
				}
				if _, err := e.docs.Update(ctx, d.ID, d.Fingerprint, documents.Change{Body: str("revised")}); err != nil {
					report(err)
					return
				}
				if _, err := e.units.AddNote(ctx, u.ID, units.NoteGeneral, "worker remark"); err != nil {
					report(err)
					return
				}
			}
		}()
	}
	var readers sync.WaitGroup
	for range 2 {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-done:
					return
				default:
				}
				r, err := e.svc.Search(ctx, Query{Text: "worker", Limit: 100})
				if err != nil {
					report(err)
					return
				}
				for _, h := range r.Hits {
					var err error
					if h.Kind == KindDocument {
						_, err = e.docs.Get(ctx, h.ID)
					} else {
						_, err = e.units.Get(ctx, h.ID)
					}
					if err != nil {
						report(fmt.Errorf("hit %+v has no record: %w", h, err))
						return
					}
				}
			}
		}()
	}
	wg.Wait()
	close(done)
	readers.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	e.wantNone("initial")
}
