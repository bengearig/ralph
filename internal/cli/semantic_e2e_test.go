package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/bengearig/ralph/internal/db"
	"github.com/bengearig/ralph/internal/embed/embedtest"
	"github.com/bengearig/ralph/internal/embed/ollama/ollamatest"
	"github.com/bengearig/ralph/internal/project"
	"github.com/bengearig/ralph/internal/search"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func (e *e2eEnv) userFile(t *testing.T) string {
	t.Helper()
	for _, kv := range e.env {
		if dir, ok := strings.CutPrefix(kv, "XDG_CONFIG_HOME="); ok {
			return filepath.Join(dir, "ralph", "config.yaml")
		}
	}
	t.Fatal("no XDG_CONFIG_HOME in the test environment")
	return ""
}

func (e *e2eEnv) writeUserConfig(t *testing.T, yaml string) {
	t.Helper()
	path := e.userFile(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (e *e2eEnv) useStub(t *testing.T, srv *ollamatest.Server) {
	t.Helper()
	e.writeUserConfig(t, "embeddings:\n  provider: ollama\n  url: "+srv.URL()+"\n")
}

func (e *e2eEnv) searchJSON(t *testing.T, args ...string) search.Result {
	t.Helper()
	r := e.run(t, append([]string{"search", "--json"}, args...)...)
	if r.code != 0 {
		t.Fatalf("search %v: exit %d: %s", args, r.code, r.stderr)
	}
	var res search.Result
	if err := json.Unmarshal([]byte(r.stdout), &res); err != nil {
		t.Fatalf("search %v: %v\n%s", args, err, r.stdout)
	}
	return res
}

func (e *e2eEnv) wantIndexed(t *testing.T, wantLines ...string) {
	t.Helper()
	r := e.run(t, "index")
	if r.code != 0 {
		t.Fatalf("index: exit %d: %s", r.code, r.stderr)
	}
	for _, w := range wantLines {
		if !strings.Contains(r.stdout, w) {
			t.Errorf("index output %q lacks %q", r.stdout, w)
		}
	}
}

func hitIDs(res search.Result) []string {
	out := []string{}
	for _, h := range res.Hits {
		out = append(out, h.ID)
	}
	return out
}

func findHit(res search.Result, id string) (search.Hit, bool) {
	for _, h := range res.Hits {
		if h.ID == id {
			return h, true
		}
	}
	return search.Hit{}, false
}

func hitIndex(res search.Result, id string) int {
	return slices.Index(hitIDs(res), id)
}

type semanticData struct {
	searchData
	archived, superseded, target string
	query                        string
}

// collidingWord returns a word the stub embeds exactly like base, so the two share no letters but a vector.
func collidingWord(t *testing.T, base string) string {
	t.Helper()
	want := embedtest.Vector(base, 64)
	for i := 0; i < 100000; i++ {
		w := fmt.Sprintf("zq%d", i)
		if reflect.DeepEqual(embedtest.Vector(w, 64), want) {
			return w
		}
	}
	t.Fatalf("no word collides with %q", base)
	return ""
}

func semanticSeed(t *testing.T, cs *mcp.ClientSession) semanticData {
	t.Helper()
	d := semanticData{searchData: e2eSearchData(t, cs)}
	d.archived = e2eOK(t, cs, "doc_create", map[string]any{"kind": "reference", "title": "Old gateway", "body": "The gateway of the past."})["id"].(string)
	d.superseded = e2eOK(t, cs, "doc_create", map[string]any{"kind": "decision", "title": "Gateway v1", "body": "The gateway before the rewrite."})["id"].(string)
	d.query = collidingWord(t, "heron")
	d.target = e2eOK(t, cs, "doc_create", map[string]any{"kind": "reference", "title": "Birds", "body": "heron"})["id"].(string)
	archive := e2eOK(t, cs, "doc_get", map[string]any{"id": d.archived})["fingerprint"]
	e2eOK(t, cs, "doc_archive", map[string]any{"id": d.archived, "fingerprint": archive})
	sup := e2eOK(t, cs, "doc_get", map[string]any{"id": d.superseded})["fingerprint"]
	e2eOK(t, cs, "doc_supersede", map[string]any{"id": d.superseded, "fingerprint": sup, "superseded_by": d.wide})
	return d
}

func TestE2ESemanticIndexAndSearch(t *testing.T) {
	e := newE2EEnv(t)
	e.init(t)
	cs := e.connect(t, io.Discard)
	d := semanticSeed(t, cs)
	_ = cs.Close()

	before := e.searchJSON(t, "gateway")
	if before.Mode != search.ModeKeyword || before.Notice != "" {
		t.Errorf("provider none: mode %q notice %q", before.Mode, before.Notice)
	}
	for _, h := range before.Hits {
		if h.Sources != nil {
			t.Errorf("provider none: hit %s has sources %v", h.ID, h.Sources)
		}
	}
	if _, err := os.Stat(project.IndexPath(e.dir)); !os.IsNotExist(err) {
		t.Fatalf("index.db exists with provider none: %v", err)
	}
	if r := e.run(t, "index"); r.code != 1 || !strings.Contains(r.stderr, "no embedding provider is configured") {
		t.Errorf("index with provider none: exit %d, stderr %q", r.code, r.stderr)
	}
	if plain := e.run(t, "search", "gateway"); plain.code != 0 || !strings.HasPrefix(plain.stdout, d.wide+"  document  ACTIVE  System overview\n") || strings.Contains(plain.stdout, "note:") {
		t.Errorf("keyword text output = %q", plain.stdout)
	}

	srv := ollamatest.NewServer(t, ollamatest.Options{})
	e.useStub(t, srv)

	r := e.run(t, "index", "--status")
	if r.code != 0 {
		t.Fatalf("index --status: exit %d: %s", r.code, r.stderr)
	}
	for _, want := range []string{
		"provider: ollama " + srv.URL() + "\n",
		"model: nomic-embed-text sha256:nomic",
		"reachable: yes\n",
		"dimensions: none yet\n",
		"records: 0 of 10 indexed, 10 waiting, 0 to remove\n",
	} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("--status output %q lacks %q", r.stdout, want)
		}
	}
	var st indexStatus
	if sr := e.run(t, "index", "--status", "--json"); sr.code != 0 || json.Unmarshal([]byte(sr.stdout), &st) != nil || !st.Reachable || st.Waiting != 10 || st.Total != 10 {
		t.Errorf("--status --json = %q (exit %d) %+v", sr.stdout, sr.code, st)
	}

	r = e.run(t, "index")
	if r.code != 0 || r.stderr != "" {
		t.Fatalf("index: exit %d: %s", r.code, r.stderr)
	}
	lines := strings.Split(strings.TrimRight(r.stdout, "\n"), "\n")
	wantOrder := []string{d.brief, d.slice, d.task, d.wide, d.tied, d.broker, d.archived, d.superseded, d.target}
	if len(lines) != len(wantOrder)+2 {
		t.Fatalf("index output = %q", r.stdout)
	}
	for i, id := range wantOrder {
		if want := "embedding " + id + " (1 chunks)"; lines[i] != want {
			t.Errorf("line %d = %q, want %q", i, lines[i], want)
		}
	}
	if want := "embedding note N-1 on " + d.task + " (1 chunks)"; lines[len(wantOrder)] != want {
		t.Errorf("note line = %q, want %q", lines[len(wantOrder)], want)
	}
	if want := "indexed 10 records (10 chunks), removed 0"; lines[len(lines)-1] != want {
		t.Errorf("summary = %q, want %q", lines[len(lines)-1], want)
	}
	if r = e.run(t, "index"); r.stdout != "indexed 0 records (0 chunks), removed 0\nindex is up to date\n" {
		t.Errorf("second index = %q", r.stdout)
	}
	if r = e.run(t, "index", "--status"); !strings.Contains(r.stdout, "records: 10 of 10 indexed, 0 waiting, 0 to remove\n") {
		t.Errorf("status after index = %q", r.stdout)
	}

	res := e.searchJSON(t, "gateway")
	if res.Mode != search.ModeHybrid || res.Notice != "" {
		t.Fatalf("mode %q notice %q", res.Mode, res.Notice)
	}
	if len(res.Hits) == 0 || res.Hits[0].ID != d.wide || !slices.Equal(res.Hits[0].Sources, []string{"keyword", "semantic"}) {
		t.Errorf("both-ways hit should come first: %+v", res.Hits)
	}
	if i := hitIndex(res, d.tied); i < 1 {
		t.Errorf("a semantic-only hit ranks at %d, want after the both-ways hit", i)
	} else if !slices.Equal(res.Hits[i].Sources, []string{"semantic"}) {
		t.Errorf("sources of %s = %v", d.tied, res.Hits[i].Sources)
	}
	for _, id := range []string{d.archived, d.superseded} {
		if _, ok := findHit(res, id); ok {
			t.Errorf("inactive document %s returned without --all", id)
		}
	}
	all := e.searchJSON(t, "gateway", "--all")
	for _, id := range []string{d.archived, d.superseded} {
		if h, ok := findHit(all, id); !ok || !h.Inactive {
			t.Errorf("inactive document %s with --all = %+v, %v", id, h, ok)
		}
	}
	if text := e.run(t, "search", "gateway"); text.code != 0 || strings.Contains(text.stdout, "note:") || !strings.HasPrefix(text.stdout, d.wide+"  document  ACTIVE  System overview\n") {
		t.Errorf("hybrid text output = %q", text.stdout)
	}

	sem := e.searchJSON(t, d.query)
	if sem.Mode != search.ModeHybrid || len(sem.Hits) == 0 || sem.Hits[0].ID != d.target {
		t.Fatalf("query %q: %+v", d.query, sem)
	}
	if !slices.Equal(sem.Hits[0].Sources, []string{"semantic"}) {
		t.Errorf("sources = %v, want [semantic]", sem.Hits[0].Sources)
	}
	if kw := e.run(t, "search", "--kind", "document", "heron"); !strings.Contains(kw.stdout, d.target) {
		t.Errorf("keyword search for the shared word = %q", kw.stdout)
	}
}

func TestE2ESemanticFollowsRecords(t *testing.T) {
	e := newE2EEnv(t)
	e.init(t)
	cs := e.connect(t, io.Discard)
	d := semanticSeed(t, cs)
	srv := ollamatest.NewServer(t, ollamatest.Options{})
	e.useStub(t, srv)
	e.wantIndexed(t, "indexed 10 records")

	cur := e2eOK(t, cs, "doc_get", map[string]any{"id": d.wide})
	e2eOK(t, cs, "doc_update", map[string]any{"id": d.wide, "fingerprint": cur["fingerprint"], "body": "The harbour handles quokka traffic."})
	for i := 0; i < 5; i++ {
		e2eOK(t, cs, "unit_create", map[string]any{"level": "task", "parent_id": d.slice, "title": fmt.Sprintf("Filler %d", i)})
	}
	if st := e.run(t, "index", "--status"); !strings.Contains(st.stdout, "records: 9 of 15 indexed, 6 waiting, 1 to remove\n") {
		t.Errorf("status after the edit = %q", st.stdout)
	}

	old := e.searchJSON(t, "gateway")
	if _, ok := findHit(old, d.wide); ok {
		t.Errorf("the edited document is returned by its old vectors: %+v", old.Hits)
	}
	if old.Mode != search.ModeHybrid || old.Notice != "semantic index is behind by 1 record: run ralph index" {
		t.Errorf("before the catch-up reaches the edit: mode %q notice %q", old.Mode, old.Notice)
	}

	fresh := e.searchJSON(t, "quokka")
	if fresh.Mode != search.ModeHybrid || fresh.Notice != "" || len(fresh.Hits) == 0 || fresh.Hits[0].ID != d.wide {
		t.Fatalf("quokka after the catch-up: %+v", fresh)
	}
	if !slices.Equal(fresh.Hits[0].Sources, []string{"keyword", "semantic"}) {
		t.Errorf("sources = %v, want both", fresh.Hits[0].Sources)
	}

	e2eOK(t, cs, "doc_archive", map[string]any{"id": d.wide, "fingerprint": e2eOK(t, cs, "doc_get", map[string]any{"id": d.wide})["fingerprint"]})
	if res := e.searchJSON(t, "quokka"); slices.Contains(hitIDs(res), d.wide) {
		t.Errorf("archived document returned: %v", hitIDs(res))
	}
	if res := e.searchJSON(t, "quokka", "--all"); !slices.Contains(hitIDs(res), d.wide) {
		t.Errorf("archived document missing with --all: %v", hitIDs(res))
	}
	e2eOK(t, cs, "doc_supersede", map[string]any{"id": d.tied, "fingerprint": e2eOK(t, cs, "doc_get", map[string]any{"id": d.tied})["fingerprint"], "superseded_by": d.broker})
	if res := e.searchJSON(t, "sqlite"); slices.Contains(hitIDs(res), d.tied) {
		t.Errorf("superseded document returned: %v", hitIDs(res))
	}

	e2eOK(t, cs, "note_add", map[string]any{"id": d.task, "body": "Remember the pangolin edge case."})
	note := e.searchJSON(t, "pangolin")
	h, ok := findHit(note, d.task)
	if !ok || h.Kind != search.KindNote || !slices.Equal(h.Sources, []string{"keyword", "semantic"}) {
		t.Errorf("the new note after the catch-up: %+v", note.Hits)
	}
	_ = cs.Close()

	e.wantIndexed(t)
	store, err := db.Open(t.Context(), project.StorePath(e.dir), db.Options{Migrations: db.Migrations, Mode: db.MigrateAuto})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SQL().Exec(`DELETE FROM notes WHERE body LIKE '%pangolin%'`); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if st := e.run(t, "index", "--status"); !strings.Contains(st.stdout, "0 waiting, 1 to remove\n") {
		t.Errorf("status after a record is gone = %q", st.stdout)
	}
	if r := e.run(t, "index"); !strings.Contains(r.stdout, "indexed 0 records (0 chunks), removed 1\n") {
		t.Errorf("index output = %q", r.stdout)
	}
	if st := e.run(t, "index", "--status"); !strings.Contains(st.stdout, "0 waiting, 0 to remove\n") {
		t.Errorf("status after removal = %q", st.stdout)
	}
}

func TestE2ESemanticFallbackWhenProviderDown(t *testing.T) {
	seeded := func(t *testing.T, index bool) (*e2eEnv, *ollamatest.Server) {
		t.Helper()
		e := newE2EEnv(t)
		e.init(t)
		cs := e.connect(t, io.Discard)
		semanticSeed(t, cs)
		_ = cs.Close()
		srv := ollamatest.NewServer(t, ollamatest.Options{})
		e.useStub(t, srv)
		if index {
			e.wantIndexed(t, "indexed 10 records")
		}
		return e, srv
	}
	keywordOnly := func(t *testing.T, e *e2eEnv, args ...string) search.Result {
		t.Helper()
		saved := e.env
		e.env = append(slices.Clone(saved), "RALPH_EMBEDDINGS_PROVIDER=none")
		defer func() { e.env = saved }()
		return e.searchJSON(t, args...)
	}

	t.Run("shut down", func(t *testing.T) {
		e, srv := seeded(t, true)
		srv.Close()
		res := e.searchJSON(t, "gateway")
		if res.Mode != search.ModeKeywordFallback || !strings.HasPrefix(res.Notice, "semantic search is unavailable (") || !strings.HasSuffix(res.Notice, "); showing keyword results") {
			t.Fatalf("mode %q notice %q", res.Mode, res.Notice)
		}
		want := keywordOnly(t, e, "gateway")
		if !slices.Equal(hitIDs(res), hitIDs(want)) || len(want.Hits) == 0 {
			t.Errorf("fallback hits %v, keyword hits %v", hitIDs(res), hitIDs(want))
		}
		text := e.run(t, "search", "gateway")
		if text.code != 0 || !strings.HasPrefix(text.stdout, "note: semantic search is unavailable (") {
			t.Errorf("text: exit %d out %q", text.code, text.stdout)
		}
	})

	t.Run("failing then back", func(t *testing.T) {
		e, srv := seeded(t, true)
		cs := e.connect(t, io.Discard)
		srv.FailWith(503)
		first := e2eCall(t, cs, "search", map[string]any{"query": "gateway"})
		if first.IsError {
			t.Fatalf("search: %s", e2eText(first))
		}
		fm, _ := first.StructuredContent.(map[string]any)
		notice, _ := fm["notice"].(string)
		if fm["mode"] != search.ModeKeywordFallback || !strings.HasPrefix(notice, "semantic search is unavailable (") || !strings.Contains(e2eText(first), "Notice: "+notice) {
			t.Fatalf("fallback = %v / %q", fm, e2eText(first))
		}
		requests := len(srv.Requests())
		srv.FailWith(0)
		second := e2eOK(t, cs, "search", map[string]any{"query": "gateway"})
		if second["mode"] != search.ModeKeywordFallback || second["notice"] != notice {
			t.Errorf("second call within a minute = %v", second)
		}
		if got := len(srv.Requests()); got != requests {
			t.Errorf("the second call made %d requests to the stub", got-requests)
		}
		fresh := e.connect(t, io.Discard)
		if res := e2eOK(t, fresh, "search", map[string]any{"query": "gateway"}); res["mode"] != search.ModeHybrid || res["notice"] != nil {
			t.Errorf("fresh process once the service is back = %v", res)
		}
	})

	t.Run("unknown model", func(t *testing.T) {
		e, srv := seeded(t, false)
		e.writeUserConfig(t, "embeddings:\n  provider: ollama\n  url: "+srv.URL()+"\n  model: no-such-model\n")
		res := e.searchJSON(t, "gateway")
		if res.Mode != search.ModeKeywordFallback || !strings.Contains(res.Notice, "ollama pull no-such-model") {
			t.Errorf("mode %q notice %q", res.Mode, res.Notice)
		}
	})

	t.Run("empty index", func(t *testing.T) {
		e := newE2EEnv(t)
		e.init(t)
		e.useStub(t, ollamatest.NewServer(t, ollamatest.Options{}))
		res := e.searchJSON(t, "gateway")
		if res.Mode != search.ModeKeywordFallback || !strings.Contains(res.Notice, "the semantic index is empty: run ralph index") {
			t.Errorf("mode %q notice %q", res.Mode, res.Notice)
		}
		if r := e.run(t, "search", "gateway"); r.code != 0 || !strings.HasPrefix(r.stdout, "note: semantic search is unavailable (the semantic index is empty: run ralph index)") {
			t.Errorf("text: exit %d out %q", r.code, r.stdout)
		}
	})
}

func TestE2ESemanticBehindNotice(t *testing.T) {
	e := newE2EEnv(t)
	e.init(t)
	cs := e.connect(t, io.Discard)
	semanticSeed(t, cs)
	srv := ollamatest.NewServer(t, ollamatest.Options{})
	e.useStub(t, srv)
	e.wantIndexed(t, "indexed 10 records")

	for i := 0; i < 12; i++ {
		e2eOK(t, cs, "doc_create", map[string]any{"kind": "reference", "title": fmt.Sprintf("Extra %d", i), "body": fmt.Sprintf("filler%d", i)})
	}
	_ = cs.Close()

	res := e.searchJSON(t, "gateway")
	if res.Mode != search.ModeHybrid || res.Notice != "semantic index is behind by 7 records: run ralph index" {
		t.Errorf("mode %q notice %q, want hybrid and the behind notice for 7 records", res.Mode, res.Notice)
	}
	if r := e.run(t, "search", "gateway"); !strings.HasPrefix(r.stdout, "note: semantic index is behind by 2 records: run ralph index\n") {
		t.Errorf("text = %q", r.stdout)
	}
	if r := e.run(t, "index"); !strings.Contains(r.stdout, "indexed 2 records") {
		t.Errorf("index = %q", r.stdout)
	}
	if res := e.searchJSON(t, "gateway"); res.Mode != search.ModeHybrid || res.Notice != "" {
		t.Errorf("after index: mode %q notice %q", res.Mode, res.Notice)
	}
}

func TestE2ESemanticSettingsAreUserOnly(t *testing.T) {
	e := newE2EEnv(t)
	e.init(t)
	cs := e.connect(t, io.Discard)
	semanticSeed(t, cs)
	_ = cs.Close()
	srv := ollamatest.NewServer(t, ollamatest.Options{})

	e.appendProject(t, "embeddings:\n  provider: ollama\n  url: "+srv.URL()+"\n")
	r := e.run(t, "search", "--json", "gateway")
	if r.code != 0 {
		t.Fatalf("exit %d: %s", r.code, r.stderr)
	}
	if !strings.Contains(r.stderr, `"embeddings.provider" is user-only and was ignored in the project file`) {
		t.Errorf("stderr = %q", r.stderr)
	}
	var res search.Result
	if err := json.Unmarshal([]byte(r.stdout), &res); err != nil || res.Mode != search.ModeKeyword {
		t.Errorf("project file setting: mode %q (%v)", res.Mode, err)
	}
	if len(srv.Requests()) != 0 {
		t.Error("an ignored setting reached the service")
	}
	if _, err := os.Stat(project.IndexPath(e.dir)); !os.IsNotExist(err) {
		t.Errorf("index.db exists: %v", err)
	}

	e.useStub(t, srv)
	e.wantIndexed(t, "indexed 10 records")
	if res := e.searchJSON(t, "gateway"); res.Mode != search.ModeHybrid {
		t.Errorf("user file: mode %q", res.Mode)
	}

	e.writeUserConfig(t, "")
	if res := e.searchJSON(t, "gateway"); res.Mode != search.ModeKeyword {
		t.Errorf("no user file: mode %q", res.Mode)
	}
	e.env = append(e.env, "RALPH_EMBEDDINGS_PROVIDER=ollama", "RALPH_EMBEDDINGS_URL="+srv.URL())
	if res := e.searchJSON(t, "gateway"); res.Mode != search.ModeHybrid {
		t.Errorf("environment: mode %q", res.Mode)
	}
}

func TestE2ESemanticIndexFileIsLocal(t *testing.T) {
	e := newE2EEnv(t)
	e.init(t)
	cs := e.connect(t, io.Discard)
	semanticSeed(t, cs)
	_ = cs.Close()
	if r := e.run(t, "init"); r.code != 0 {
		t.Fatalf("second init: exit %d: %s", r.code, r.stderr)
	}
	ignore := e.readFile(t, filepath.Join(e.dir, ".ralph", ".gitignore"))
	if !slices.Contains(strings.Split(ignore, "\n"), "/index.db*") {
		t.Errorf(".gitignore = %q", ignore)
	}
	e.commitAll(t, "init ralph")

	srv := ollamatest.NewServer(t, ollamatest.Options{})
	e.useStub(t, srv)
	e.wantIndexed(t, "indexed 10 records")
	if _, err := os.Stat(project.IndexPath(e.dir)); err != nil {
		t.Fatal(err)
	}
	if got := e.gitStatus(t); got != "" {
		t.Errorf("git status after indexing = %q", got)
	}

	dest := filepath.Join(t.TempDir(), "snapshot.db")
	if r := e.run(t, "backup", "--output", dest); r.code != 0 {
		t.Fatalf("backup: exit %d: %s", r.code, r.stderr)
	}
	snap, err := db.Open(t.Context(), dest, db.Options{MustExist: true, NoMigrate: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = snap.Close() }()
	rows, err := snap.SQL().Query(`SELECT name FROM sqlite_master WHERE type = 'table'`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		if name == "chunks" || name == "records" || name == "meta" {
			t.Errorf("the backup holds the index table %q", name)
		}
	}
}
