package semantic

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/bengearig/ralph/internal/db"
	"github.com/bengearig/ralph/internal/documents"
	"github.com/bengearig/ralph/internal/embed"
	"github.com/bengearig/ralph/internal/embed/embedtest"
	"github.com/bengearig/ralph/internal/search"
	"github.com/bengearig/ralph/internal/testutil"
	"github.com/bengearig/ralph/internal/units"
	"github.com/bengearig/ralph/internal/vecindex"
)

var ctx = context.Background()

type env struct {
	t        *testing.T
	d        *db.DB
	units    units.Service
	docs     documents.Service
	index    *vecindex.Store
	path     string
	fake     *embedtest.Fake
	reports  []string
	brief    units.Unit
	slice    units.Unit
	settings embed.Settings
}

func newEnv(t *testing.T) *env {
	t.Helper()
	d := testutil.NewDB(t, db.Migrations)
	e := &env{t: t, d: d, units: units.NewService(d), docs: documents.NewService(d), fake: embedtest.NewFake(16),
		settings: embed.Settings{Provider: "ollama", Model: "nomic-embed-text", ChunkChars: 1200}}
	e.path = filepath.Join(t.TempDir(), "index.db")
	var err error
	if e.index, err = vecindex.Open(e.path); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.index.Close() })
	e.brief = e.mustUnit(units.NewUnit{Level: units.LevelBrief, Title: "Brief", Body: "brief body"})
	e.slice = e.mustUnit(units.NewUnit{Level: units.LevelSlice, ParentID: e.brief.ID, Title: "Slice", Body: "slice body"})
	return e
}

func (e *env) mustUnit(n units.NewUnit) units.Unit {
	e.t.Helper()
	u, err := e.units.Create(ctx, n)
	if err != nil {
		e.t.Fatal(err)
	}
	return u
}

func (e *env) task(title, body, criteria string) units.Unit {
	return e.mustUnit(units.NewUnit{Level: units.LevelTask, ParentID: e.slice.ID, Title: title, Body: body, AcceptanceCriteria: criteria})
}

func (e *env) doc(title, body string) documents.Document {
	e.t.Helper()
	d, err := e.docs.Create(ctx, documents.NewDocument{Kind: documents.KindDesign, Title: title, Body: body})
	if err != nil {
		e.t.Fatal(err)
	}
	return d
}

func (e *env) note(unit, body string) units.Note {
	e.t.Helper()
	n, err := e.units.AddNote(ctx, unit, units.NoteGeneral, body)
	if err != nil {
		e.t.Fatal(err)
	}
	return n
}

func (e *env) exec(query string, args ...any) {
	e.t.Helper()
	if _, err := e.d.SQL().Exec(query, args...); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) deps() SyncDeps {
	return SyncDeps{DB: e.d, Index: e.index, Provider: e.fake, Settings: e.settings, Report: func(s string) { e.reports = append(e.reports, s) }}
}

func (e *env) sync(opt SyncOptions) (SyncResult, error) {
	e.t.Helper()
	e.reports = nil
	return Sync(ctx, e.deps(), opt)
}

func (e *env) mustSync() SyncResult {
	e.t.Helper()
	res, err := e.sync(SyncOptions{})
	if err != nil {
		e.t.Fatal(err)
	}
	return res
}

func TestRecordsKindsAndText(t *testing.T) {
	e := newEnv(t)
	task := e.task("Task title", "task body", "it works")
	plain := e.task("Plain", "just body", "")
	doc := e.doc("Doc title", "doc body")
	note := e.note(task.ID, "note body")
	recs, err := Records(ctx, e.d)
	if err != nil {
		t.Fatal(err)
	}
	byRef := map[string]Record{}
	for _, r := range recs {
		byRef[string(r.Kind)+"/"+r.Ref] = r
	}
	check := func(key string, kind search.Kind, unit, title, text string) {
		t.Helper()
		r, ok := byRef[key]
		if !ok || r.Kind != kind || r.Unit != unit || r.Title != title || r.Text != text || len(r.Hash) != 64 {
			t.Errorf("%s = %+v", key, r)
		}
	}
	check("brief/"+e.brief.ID, search.KindBrief, "", "Brief", "brief body")
	check("slice/"+e.slice.ID, search.KindSlice, "", "Slice", "slice body")
	check("task/"+task.ID, search.KindTask, "", "Task title", "task body\nit works")
	check("task/"+plain.ID, search.KindTask, "", "Plain", "just body")
	check("document/"+doc.ID, search.KindDocument, "", "Doc title", "doc body")
	check(fmt.Sprintf("note/%d", note.ID), search.KindNote, task.ID, "", "note body")
}

func TestRecordsHashChangesWithTitleOrText(t *testing.T) {
	e := newEnv(t)
	task := e.task("Title", "body", "")
	hash := func() string {
		recs, err := Records(ctx, e.d)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range recs {
			if r.Ref == task.ID {
				return r.Hash
			}
		}
		t.Fatal("task missing")
		return ""
	}
	h0 := hash()
	if hash() != h0 {
		t.Fatal("hash is not stable")
	}
	e.exec(`UPDATE units SET title = 'Other' WHERE id = ?`, task.ID)
	h1 := hash()
	e.exec(`UPDATE units SET body = 'changed' WHERE id = ?`, task.ID)
	h2 := hash()
	e.exec(`UPDATE units SET acceptance_criteria = 'crit' WHERE id = ?`, task.ID)
	h3 := hash()
	seen := map[string]bool{h0: true, h1: true, h2: true, h3: true}
	if len(seen) != 4 {
		t.Fatalf("hashes collide: %v", []string{h0, h1, h2, h3})
	}
}

func TestRecordsOrder(t *testing.T) {
	e := newEnv(t)
	t1 := e.task("one", "x", "")
	t2 := e.task("two", "x", "")
	d1 := e.doc("d1", "x")
	d2 := e.doc("d2", "x")
	n1 := e.note(t2.ID, "first")
	n2 := e.note(t1.ID, "second")
	recs, err := Records(ctx, e.d)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range recs {
		got = append(got, string(r.Kind)+":"+r.Ref)
	}
	want := []string{"brief:" + e.brief.ID, "slice:" + e.slice.ID, "task:" + t1.ID, "task:" + t2.ID, "document:" + d1.ID, "document:" + d2.ID,
		fmt.Sprintf("note:%d", n1.ID), fmt.Sprintf("note:%d", n2.ID)}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestSyncEmbedsAllAndReports(t *testing.T) {
	e := newEnv(t)
	task := e.task("Task title", "task body", "")
	doc := e.doc("Doc title", "doc body")
	note := e.note(task.ID, "note body")
	res := e.mustSync()
	if res.Embedded != 5 || res.Chunks != 5 || res.Removed != 0 || res.Waiting != 0 {
		t.Fatalf("result %+v", res)
	}
	want := []string{
		"embedding " + e.brief.ID + " (1 chunks)", "embedding " + e.slice.ID + " (1 chunks)", "embedding " + task.ID + " (1 chunks)",
		"embedding " + doc.ID + " (1 chunks)", fmt.Sprintf("embedding note N-%d on %s (1 chunks)", note.ID, task.ID),
	}
	if !reflect.DeepEqual(e.reports, want) {
		t.Fatalf("reports %q, want %q", e.reports, want)
	}
	calls := e.fake.Calls()
	if len(calls) != 5 || calls[2][0] != "search_document: Task title\n\ntask body" {
		t.Fatalf("calls %q", calls)
	}
	recs, _ := e.index.Records(ctx)
	if info := recs[vecindex.Key{Kind: "note", Ref: fmt.Sprint(note.ID)}]; info.Unit != task.ID || info.Chunks != 1 {
		t.Fatalf("note record %+v", info)
	}
}

func TestSyncSecondRunDoesNothing(t *testing.T) {
	e := newEnv(t)
	e.task("a", "b", "")
	e.mustSync()
	calls := len(e.fake.Calls())
	res := e.mustSync()
	if res != (SyncResult{}) || len(e.reports) != 0 || len(e.fake.Calls()) != calls {
		t.Fatalf("result %+v reports %q", res, e.reports)
	}
}

func TestSyncReembedsChangedRecord(t *testing.T) {
	e := newEnv(t)
	task := e.task("a", "old words", "")
	e.mustSync()
	e.exec(`UPDATE units SET body = 'new words' WHERE id = ?`, task.ID)
	res := e.mustSync()
	if res.Embedded != 1 || res.Removed != 1 || !reflect.DeepEqual(e.reports, []string{"embedding " + task.ID + " (1 chunks)"}) {
		t.Fatalf("result %+v reports %q", res, e.reports)
	}
	hits, err := e.index.Search(ctx, embedtest.Vector("search_document: a\n\nnew words", 16), 1)
	if err != nil || len(hits) != 1 || hits[0].Ref != task.ID || hits[0].Text != "new words" {
		t.Fatalf("hits %+v err %v", hits, err)
	}
}

func TestSyncRemovesDeletedAndChanged(t *testing.T) {
	e := newEnv(t)
	task := e.task("a", "b", "")
	note := e.note(task.ID, "gone soon")
	e.mustSync()
	e.exec(`DELETE FROM notes WHERE id = ?`, note.ID)
	e.exec(`UPDATE units SET title = 'renamed' WHERE id = ?`, task.ID)
	res := e.mustSync()
	if res.Removed != 2 || res.Embedded != 1 {
		t.Fatalf("result %+v", res)
	}
	recs, _ := e.index.Records(ctx)
	if _, ok := recs[vecindex.Key{Kind: "note", Ref: fmt.Sprint(note.ID)}]; ok || len(recs) != 3 {
		t.Fatalf("records %v", recs)
	}
}

func TestSyncMaxRecordsLeavesWaiting(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < 4; i++ {
		e.task(fmt.Sprint("t", i), "b", "")
	}
	res, err := e.sync(SyncOptions{MaxRecords: 2})
	if err != nil || res.Embedded != 2 || res.Waiting != 4 {
		t.Fatalf("result %+v err %v", res, err)
	}
	res, err = e.sync(SyncOptions{})
	if err != nil || res.Embedded != 4 || res.Waiting != 0 {
		t.Fatalf("second result %+v err %v", res, err)
	}
}

func TestSyncProviderFailureKeepsDoneRecords(t *testing.T) {
	e := newEnv(t)
	e.task("a", "b", "")
	res, err := e.sync(SyncOptions{MaxRecords: 2})
	if err != nil || res.Embedded != 2 {
		t.Fatal(res, err)
	}
	e.fake.FailWith(embed.ErrUnavailable)
	res, err = e.sync(SyncOptions{})
	if !errors.Is(err, embed.ErrUnavailable) || res.Embedded != 0 || res.Waiting != 1 {
		t.Fatalf("result %+v err %v", res, err)
	}
	e.fake.Recover()
	e.task("c", "d", "")
	res = e.mustSync()
	if res.Embedded != 2 {
		t.Fatalf("result %+v", res)
	}
	recs, _ := e.index.Records(ctx)
	if len(recs) != 4 {
		t.Fatalf("records %d", len(recs))
	}
}

type failAfter struct {
	embed.Provider
	ok  int
	err error
}

func (f *failAfter) Embed(c context.Context, texts []string) ([][]float32, error) {
	if f.ok == 0 {
		return nil, f.err
	}
	f.ok--
	return f.Provider.Embed(c, texts)
}

func TestSyncProviderFailureMidwayKeepsDone(t *testing.T) {
	e := newEnv(t)
	e.task("a", "b", "")
	deps := e.deps()
	deps.Provider = &failAfter{Provider: e.fake, ok: 2, err: embed.ErrUnavailable}
	res, err := Sync(ctx, deps, SyncOptions{})
	if !errors.Is(err, embed.ErrUnavailable) || res.Embedded != 2 || res.Waiting != 1 {
		t.Fatalf("result %+v err %v", res, err)
	}
	if recs, _ := e.index.Records(ctx); len(recs) != 2 {
		t.Fatalf("records %d", len(recs))
	}
	res = e.mustSync()
	if res.Embedded != 1 {
		t.Fatalf("resumed %+v", res)
	}
}

func TestSyncCancelBetweenRecords(t *testing.T) {
	e := newEnv(t)
	e.task("a", "b", "")
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	deps := e.deps()
	deps.Report = func(string) { cancel() }
	res, err := Sync(cctx, deps, SyncOptions{})
	if !errors.Is(err, context.Canceled) || res.Embedded != 1 || res.Waiting != 2 {
		t.Fatalf("result %+v err %v", res, err)
	}
	if recs, _ := e.index.Records(ctx); len(recs) != 1 {
		t.Fatalf("records %d", len(recs))
	}
}

type shortLast struct{ embed.Provider }

func (s shortLast) Embed(c context.Context, texts []string) ([][]float32, error) {
	v, err := s.Provider.Embed(c, texts)
	if err == nil && len(v) > 1 {
		v[len(v)-1] = v[len(v)-1][:3]
	}
	return v, err
}

func TestSyncRecordAtomicWithManyChunks(t *testing.T) {
	e := newEnv(t)
	e.settings.ChunkChars = 200
	small := e.task("small", "short", "")
	e.mustSync()
	big := e.task("big", strings.Repeat("paragraph of words here.\n\n", 40), "")
	deps := e.deps()
	deps.Provider = shortLast{e.fake}
	res, err := Sync(ctx, deps, SyncOptions{})
	if !errors.Is(err, vecindex.ErrDimensions) || res.Embedded != 0 {
		t.Fatalf("result %+v err %v", res, err)
	}
	recs, _ := e.index.Records(ctx)
	if _, ok := recs[vecindex.Key{Kind: "task", Ref: big.ID}]; ok {
		t.Fatal("partial record stored")
	}
	if _, ok := recs[vecindex.Key{Kind: "task", Ref: small.ID}]; !ok {
		t.Fatal("earlier record lost")
	}
	res = e.mustSync()
	recs, _ = e.index.Records(ctx)
	if info := recs[vecindex.Key{Kind: "task", Ref: big.ID}]; info.Chunks < 3 || res.Chunks != info.Chunks {
		t.Fatalf("record %+v result %+v", info, res)
	}
}

func TestSyncDimensionChangeStopsWithError(t *testing.T) {
	e := newEnv(t)
	task := e.task("a", "b", "")
	e.mustSync()
	e.exec(`UPDATE units SET body = 'changed' WHERE id = ?`, task.ID)
	deps := e.deps()
	deps.Provider = embedtest.NewFake(8)
	_, err := Sync(ctx, deps, SyncOptions{})
	if !errors.Is(err, vecindex.ErrDimensions) || !strings.Contains(err.Error(), "built with vectors of 16 values") {
		t.Fatalf("err = %v", err)
	}
}

func TestPendingWritesNothing(t *testing.T) {
	e := newEnv(t)
	task := e.task("a", "b", "")
	e.mustSync()
	e.exec(`UPDATE units SET body = 'changed' WHERE id = ?`, task.ID)
	note := e.note(task.ID, "new note")
	before, err := os.ReadFile(e.path)
	if err != nil {
		t.Fatal(err)
	}
	st0, _ := e.index.Stats(ctx)
	calls := len(e.fake.Calls())
	waiting, gone, err := Pending(ctx, e.deps())
	if err != nil {
		t.Fatal(err)
	}
	var refs []string
	for _, r := range waiting {
		refs = append(refs, r.Ref)
	}
	if want := []string{task.ID, fmt.Sprint(note.ID)}; !reflect.DeepEqual(refs, want) {
		t.Fatalf("waiting %v, want %v", refs, want)
	}
	if want := []vecindex.Key{{Kind: "task", Ref: task.ID}}; !reflect.DeepEqual(gone, want) {
		t.Fatalf("gone %v, want %v", gone, want)
	}
	after, _ := os.ReadFile(e.path)
	st1, _ := e.index.Stats(ctx)
	if len(e.fake.Calls()) != calls || string(before) != string(after) || st0 != st1 {
		t.Fatal("Pending changed something")
	}
}
