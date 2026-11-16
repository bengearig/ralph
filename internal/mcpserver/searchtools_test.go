package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/bengearig/ralph/internal/db"
	"github.com/bengearig/ralph/internal/documents"
	"github.com/bengearig/ralph/internal/search"
	"github.com/bengearig/ralph/internal/testutil"
	"github.com/bengearig/ralph/internal/units"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func newSearchClient(t *testing.T, sources ...NoticeSource) *mcp.ClientSession {
	t.Helper()
	d := testutil.NewDB(t, db.Migrations)
	svc := search.New(d)
	return connect(t, Options{
		Name: "ralph", Actor: actor, Sources: sources,
		Open:      staticOpen(units.NewService(d)),
		Documents: staticDocs(documents.NewService(d)),
		Search:    func(context.Context) (search.Service, error) { return svc, nil },
	})
}

func searchHits(t *testing.T, out map[string]any) []map[string]any {
	t.Helper()
	raw, ok := out["hits"].([]any)
	if !ok {
		t.Fatalf("hits = %#v", out["hits"])
	}
	hits := []map[string]any{}
	for _, h := range raw {
		hits = append(hits, h.(map[string]any))
	}
	return hits
}

func seedSearch(t *testing.T, cs *mcp.ClientSession) (docID, taskID string) {
	t.Helper()
	docID = docCreate(t, cs, "design", "Lemur habitat", nil)["id"].(string)
	brief := create(t, cs, map[string]any{"level": "brief", "title": "Zoo brief", "body": "words"})["id"].(string)
	slice := create(t, cs, map[string]any{"level": "slice", "parent_id": brief, "title": "Zoo slice"})["id"].(string)
	taskID = create(t, cs, map[string]any{
		"level": "task", "parent_id": slice, "title": "Feed the lemur", "body": "fruit twice daily", "acceptance_criteria": "fed",
	})["id"].(string)
	return docID, taskID
}

func TestSearchToolListed(t *testing.T) {
	tool, found := tools(t, newSearchClient(t))["search"]
	if !found {
		t.Fatal("search not listed")
	}
	if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
		t.Error("search is not read-only")
	}
	sc := schemaOf(t, tool)
	if !slices.Equal(sc.Required, []string{"query"}) {
		t.Errorf("required = %v", sc.Required)
	}
	want := []string{"brief", "slice", "task", "note", "document"}
	if g := enumStrings(sc.Properties["kinds"].Items); !slices.Equal(g, want) {
		t.Errorf("kinds enum = %v", g)
	}
	if lim := sc.Properties["limit"]; lim.Minimum == nil || *lim.Minimum != 1 || lim.Maximum == nil || *lim.Maximum != 100 {
		t.Errorf("limit schema = %+v", lim)
	}
}

func TestSearchToolAbsentWithoutOption(t *testing.T) {
	cs := connect(t, Options{Name: "ralph", Open: staticOpen(newSvc(t))})
	if _, found := tools(t, cs)["search"]; found {
		t.Error("search listed without Options.Search")
	}
}

func TestSearchToolDescriptionMentionsEssentials(t *testing.T) {
	d := tools(t, newSearchClient(t))["search"].Description
	for _, want := range []string{
		"plain words", "double quotes keep a phrase together", "every word", "no record does",
		"include_inactive", "Archived and superseded documents and cancelled tasks", "Earlier versions of documents are not searched",
		"documents, briefs, slices, tasks and the notes",
	} {
		if !strings.Contains(d, want) {
			t.Errorf("description lacks %q", want)
		}
	}
}

func TestSearchToolFindsDocumentAndTask(t *testing.T) {
	cs := newSearchClient(t)
	docID, taskID := seedSearch(t, cs)
	res := call(t, cs, "search", map[string]any{"query": "lemur"})
	if res.IsError {
		t.Fatal(text(res))
	}
	if first, _, _ := strings.Cut(text(res), "\n"); first != "Keyword search: 2 result(s)" {
		t.Errorf("first line = %q", first)
	}
	out := res.StructuredContent.(map[string]any)
	if out["mode"] != "keyword" || out["relaxed"] != false {
		t.Errorf("out = %#v", out)
	}
	ids := []string{}
	for _, h := range searchHits(t, out) {
		ids = append(ids, h["kind"].(string)+":"+h["id"].(string))
	}
	slices.Sort(ids)
	if want := []string{"document:" + docID, "task:" + taskID}; !slices.Equal(ids, want) {
		t.Errorf("hits = %v, want %v", ids, want)
	}
	if !strings.Contains(text(res), taskID+" task TODO") && !strings.Contains(text(res), taskID+" task PROPOSED") {
		t.Errorf("text lacks the task line: %q", text(res))
	}
	if !strings.Contains(text(res), "\n  ") {
		t.Errorf("text lacks an indented snippet: %q", text(res))
	}
}

func TestSearchToolRelaxedTextLine(t *testing.T) {
	cs := newSearchClient(t)
	seedSearch(t, cs)
	res := call(t, cs, "search", map[string]any{"query": "lemur narwhal"})
	lines := strings.Split(text(res), "\n")
	if len(lines) < 2 || lines[1] != "no result had every word; these match some of them" {
		t.Errorf("text = %q", text(res))
	}
	if res.StructuredContent.(map[string]any)["relaxed"] != true {
		t.Error("relaxed not true")
	}
}

func TestSearchToolNoResults(t *testing.T) {
	cs := newSearchClient(t)
	res := call(t, cs, "search", map[string]any{"query": "nothing"})
	if res.IsError {
		t.Fatal(text(res))
	}
	raw, _ := json.Marshal(res.StructuredContent)
	if !strings.Contains(string(raw), `"hits":[]`) {
		t.Errorf("structured = %s", raw)
	}
	if text(res) != "Keyword search: 0 result(s)" {
		t.Errorf("text = %q", text(res))
	}
}

func TestSearchToolInvalidQuery(t *testing.T) {
	cs := newSearchClient(t)
	fails(t, cs, "search", map[string]any{"query": "!!! ---"}, "invalid")
}

func TestSearchToolKindsAndLimit(t *testing.T) {
	cs := newSearchClient(t)
	_, taskID := seedSearch(t, cs)
	out := ok(t, cs, "search", map[string]any{"query": "lemur", "kinds": []string{"task"}})
	hits := searchHits(t, out)
	if len(hits) != 1 || hits[0]["id"] != taskID {
		t.Errorf("hits = %v", hits)
	}
	out = ok(t, cs, "search", map[string]any{"query": "lemur", "limit": 1})
	if n := len(searchHits(t, out)); n != 1 {
		t.Errorf("limit 1 gave %d hits", n)
	}
}

func TestSearchToolIncludeInactive(t *testing.T) {
	cs := newSearchClient(t)
	doc := docCreate(t, cs, "design", "Okapi archive", nil)
	ok(t, cs, "doc_archive", map[string]any{"id": doc["id"], "fingerprint": doc["fingerprint"]})
	if n := len(searchHits(t, ok(t, cs, "search", map[string]any{"query": "okapi"}))); n != 0 {
		t.Errorf("default returned %d hits", n)
	}
	hits := searchHits(t, ok(t, cs, "search", map[string]any{"query": "okapi", "include_inactive": true}))
	if len(hits) != 1 || hits[0]["id"] != doc["id"] || hits[0]["inactive"] != true {
		t.Errorf("hits = %v", hits)
	}
}

func TestSearchToolFactoryLazyAndCached(t *testing.T) {
	var calls atomic.Int32
	d := testutil.NewDB(t, db.Migrations)
	svc := search.New(d)
	cs := connect(t, Options{Name: "ralph", Search: func(context.Context) (search.Service, error) {
		calls.Add(1)
		return svc, nil
	}})
	tools(t, cs)
	if n := calls.Load(); n != 0 {
		t.Fatalf("factory called %d times before a call", n)
	}
	ok(t, cs, "search", map[string]any{"query": "a"})
	ok(t, cs, "search", map[string]any{"query": "b"})
	if n := calls.Load(); n != 1 {
		t.Errorf("factory called %d times, want 1", n)
	}

	var tries atomic.Int32
	cs = connect(t, Options{Name: "ralph", Search: func(context.Context) (search.Service, error) {
		if tries.Add(1) == 1 {
			return nil, errors.New("disk on fire")
		}
		return svc, nil
	}})
	fails(t, cs, "search", map[string]any{"query": "a"}, "internal")
	ok(t, cs, "search", map[string]any{"query": "a"})
	if n := tries.Load(); n != 2 {
		t.Errorf("failing factory called %d times, want 2", n)
	}
}

func TestSearchToolFactoryNotInitialized(t *testing.T) {
	cs := connect(t, Options{Name: "ralph", Search: func(context.Context) (search.Service, error) {
		return nil, fmt.Errorf("open: %w", db.ErrNotFound)
	}})
	res := fails(t, cs, "search", map[string]any{"query": "a"}, "not_initialized")
	if !strings.Contains(text(res), "ralph init") {
		t.Errorf("text = %q", text(res))
	}
}

func TestSearchToolResultsCarryNotices(t *testing.T) {
	cs := newSearchClient(t, fixed(Notice{Code: "x", Message: "hello"}))
	res := call(t, cs, "search", map[string]any{"query": "anything"})
	if res.IsError {
		t.Fatal(text(res))
	}
	if ns := structuredNotices(t, res); len(ns) != 1 || ns[0].Code != "x" {
		t.Errorf("notices = %v", ns)
	}
}
