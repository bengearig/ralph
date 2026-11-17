package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/bengearig/ralph/internal/db"
	"github.com/bengearig/ralph/internal/embed/ollama/ollamatest"
	"github.com/bengearig/ralph/internal/search"
)

func factoryService(t *testing.T) (kind string, closed func()) {
	t.Helper()
	a := loadedApp(t)
	d, err := openStore(context.Background(), a, db.Options{MustExist: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	svc, closeIndex, err := newSearchService(a, d)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(closeIndex)
	return fmt.Sprintf("%T", svc), closeIndex
}

func TestSearchServiceFactoryKeywordWhenNoProvider(t *testing.T) {
	seedSearchProject(t)
	if kind, _ := factoryService(t); kind != "*search.keyword" {
		t.Errorf("service = %s", kind)
	}
}

func TestSearchServiceFactoryCreatesIndexFileWhenConfigured(t *testing.T) {
	seedSearchProject(t)
	srv := ollamatest.NewServer(t, ollamatest.Options{})
	useOllama(t, srv)
	if kind, _ := factoryService(t); kind != "*search.hybrid" {
		t.Errorf("service = %s", kind)
	}
	if _, err := os.Stat(filepath.Join(".ralph", "index.db")); err != nil {
		t.Errorf("the factory did not create the index file: %v", err)
	}
	if len(srv.Requests()) != 0 {
		t.Error("building the service called the provider")
	}
}

func TestSearchServiceFactoryHybridWhenConfigured(t *testing.T) {
	seedSearchProject(t)
	srv := ollamatest.NewServer(t, ollamatest.Options{})
	useOllama(t, srv)
	if code, _, stderr := run(t, "index"); code != 0 {
		t.Fatal(stderr)
	}
	before := len(srv.Requests())
	if kind, _ := factoryService(t); kind != "*search.hybrid" {
		t.Errorf("service = %s", kind)
	}
	if len(srv.Requests()) != before {
		t.Error("building the service called the provider")
	}
}

func hybridJSON(t *testing.T, words ...string) search.Result {
	t.Helper()
	code, out, stderr := run(t, append([]string{"search", "--json"}, words...)...)
	if code != 0 {
		t.Fatalf("code %d stderr %q", code, stderr)
	}
	var res search.Result
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatal(err)
	}
	return res
}

func TestSearchHybridOutputShowsFusedOrder(t *testing.T) {
	f := seedSearchProject(t)
	useOllama(t, ollamatest.NewServer(t, ollamatest.Options{}))
	if code, _, stderr := run(t, "index"); code != 0 {
		t.Fatal(stderr)
	}
	res := hybridJSON(t, "lemur")
	if res.Mode != search.ModeHybrid || len(res.Hits) < 3 {
		t.Fatalf("res = %+v", res)
	}
	for i, h := range res.Hits[:2] {
		if (h.ID != f.doc && h.ID != f.task) || !slices.Equal(h.Sources, []string{"keyword", "semantic"}) {
			t.Errorf("hit %d = %+v", i, h)
		}
	}
	if !slices.Equal(res.Hits[2].Sources, []string{"semantic"}) {
		t.Errorf("third = %+v", res.Hits[2])
	}
	_, out, _ := run(t, "search", "lemur")
	var got []string
	for _, l := range strings.Split(out, "\n") {
		if l != "" && !strings.HasPrefix(l, "    ") {
			got = append(got, strings.Fields(l)[0])
		}
	}
	var want []string
	for _, h := range res.Hits {
		want = append(want, h.ID)
	}
	if !slices.Equal(got, want) {
		t.Errorf("text order %v, json order %v", got, want)
	}
}

func TestSearchNoticePrintedBeforeHits(t *testing.T) {
	var out bytes.Buffer
	res := search.Result{Mode: search.ModeKeywordFallback, Notice: "semantic search is unavailable (down); showing keyword results",
		Hits: []search.Hit{{Kind: search.KindTask, ID: "T-1", Title: "t", Status: "TODO"}}}
	if err := printSearchResult(&out, res, false); err != nil {
		t.Fatal(err)
	}
	if want := "note: semantic search is unavailable (down); showing keyword results\nT-1  task  TODO  t\n"; out.String() != want {
		t.Errorf("out = %q", out.String())
	}
	out.Reset()
	res.Hits = nil
	_ = printSearchResult(&out, res, false)
	if !strings.HasPrefix(out.String(), "note: ") || !strings.HasSuffix(out.String(), "no results\n") {
		t.Errorf("empty out = %q", out.String())
	}
	out.Reset()
	_ = printSearchResult(&out, search.Result{Mode: search.ModeKeyword}, false)
	if out.String() != "no results\n" {
		t.Errorf("no notice out = %q", out.String())
	}
}

func TestSearchJSONHasModeNoticeSources(t *testing.T) {
	var out bytes.Buffer
	res := search.Result{Mode: search.ModeHybrid, Notice: "n", Hits: []search.Hit{{Kind: search.KindTask, ID: "T-1", Sources: []string{"keyword"}}}}
	if err := printSearchResult(&out, res, true); err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(out.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	hit := m["hits"].([]any)[0].(map[string]any)
	if m["mode"] != "hybrid" || m["notice"] != "n" || fmt.Sprint(hit["sources"]) != "[keyword]" {
		t.Errorf("json = %s", out.String())
	}
	out.Reset()
	_ = printSearchResult(&out, search.Result{Mode: search.ModeKeyword, Hits: []search.Hit{{ID: "T-1"}}}, true)
	if strings.Contains(out.String(), "notice") || strings.Contains(out.String(), "sources") {
		t.Errorf("empty fields not omitted: %s", out.String())
	}
}

func TestSearchFallbackPrintsNoteAndKeywordHits(t *testing.T) {
	f := seedSearchProject(t)
	srv := ollamatest.NewServer(t, ollamatest.Options{})
	useOllama(t, srv)
	if code, _, stderr := run(t, "index"); code != 0 {
		t.Fatal(stderr)
	}
	srv.Close()
	code, out, stderr := run(t, "search", "enclosure")
	if code != 0 || stderr != "" {
		t.Fatalf("code %d stderr %q", code, stderr)
	}
	lines := strings.Split(out, "\n")
	if !strings.HasPrefix(lines[0], "note: semantic search is unavailable (") || !strings.HasSuffix(lines[0], "); showing keyword results") {
		t.Errorf("first line = %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], f.doc+"  document  ACTIVE  Lemur habitat") {
		t.Errorf("hits = %q", lines[1:])
	}
	if len(lines) != 4 {
		t.Errorf("output = %q", out)
	}
}

func TestSearchFallbackJSON(t *testing.T) {
	seedSearchProject(t)
	srv := ollamatest.NewServer(t, ollamatest.Options{})
	useOllama(t, srv)
	if code, _, stderr := run(t, "index"); code != 0 {
		t.Fatal(stderr)
	}
	srv.Close()
	res := hybridJSON(t, "enclosure")
	if res.Mode != search.ModeKeywordFallback || !strings.HasPrefix(res.Notice, "semantic search is unavailable (") || len(res.Hits) != 1 || len(res.Hits[0].Sources) != 0 {
		t.Errorf("res = %+v", res)
	}
}

func TestSearchBehindNoticeShown(t *testing.T) {
	seedSearchProject(t)
	useOllama(t, ollamatest.NewServer(t, ollamatest.Options{}))
	if code, _, stderr := run(t, "index"); code != 0 {
		t.Fatal(stderr)
	}
	cs := mcpSession(t)
	for i := range 7 {
		mcpOK(t, cs, "doc_create", map[string]any{"kind": "design", "title": fmt.Sprintf("Lemur extra %d", i), "body": "more"})
	}
	_ = cs.Close()
	code, out, stderr := run(t, "search", "lemur")
	if code != 0 || stderr != "" || !strings.HasPrefix(out, "note: semantic index is behind by 2 records: run ralph index\n") {
		t.Errorf("code %d out %q stderr %q", code, out, stderr)
	}
}

func TestSearchFirstSearchCreatesIndexFileAndCatchesUp(t *testing.T) {
	f := seedSearchProject(t)
	srv := ollamatest.NewServer(t, ollamatest.Options{})
	useOllama(t, srv)
	res := hybridJSON(t, "lemur")
	if _, err := os.Stat(filepath.Join(".ralph", "index.db")); err != nil {
		t.Fatalf("no index file: %v", err)
	}
	if res.Mode != search.ModeHybrid || len(srv.Requests()) == 0 {
		t.Fatalf("res = %+v, requests %d", res, len(srv.Requests()))
	}
	var found bool
	for _, h := range res.Hits {
		found = found || (h.ID == f.task && slices.Contains(h.Sources, "semantic"))
	}
	if !found {
		t.Errorf("task not found semantically: %+v", res.Hits)
	}
}

func TestSearchNoProviderTouchesNoIndexFile(t *testing.T) {
	seedSearchProject(t)
	if code, _, stderr := run(t, "search", "lemur"); code != 0 {
		t.Fatal(stderr)
	}
	if _, err := os.Stat(filepath.Join(".ralph", "index.db")); !os.IsNotExist(err) {
		t.Errorf("index file touched: %v", err)
	}
}

func TestMCPServerRemembersOutageAcrossCalls(t *testing.T) {
	seedSearchProject(t)
	srv := ollamatest.NewServer(t, ollamatest.Options{})
	useOllama(t, srv)
	if code, _, stderr := run(t, "index"); code != 0 {
		t.Fatal(stderr)
	}
	srv.FailWith(503)
	cs := mcpSession(t)
	mcpOK(t, cs, "doc_create", map[string]any{"kind": "design", "title": "Lemur new", "body": "x"})
	first := mcpOK(t, cs, "search", map[string]any{"query": "lemur"})
	if first["mode"] != search.ModeKeywordFallback {
		t.Fatalf("first = %v", first)
	}
	before := len(srv.Requests())
	second := mcpOK(t, cs, "search", map[string]any{"query": "lemur"})
	if second["mode"] != search.ModeKeywordFallback || second["notice"] != first["notice"] {
		t.Errorf("second = %v", second)
	}
	if after := len(srv.Requests()); after != before {
		t.Errorf("second call made %d requests", after-before)
	}
}
