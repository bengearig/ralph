package mcpserver

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bengearig/ralph/internal/db"
	"github.com/bengearig/ralph/internal/documents"
	"github.com/bengearig/ralph/internal/embed"
	"github.com/bengearig/ralph/internal/embed/embedtest"
	"github.com/bengearig/ralph/internal/search"
	"github.com/bengearig/ralph/internal/semantic"
	"github.com/bengearig/ralph/internal/testutil"
	"github.com/bengearig/ralph/internal/units"
	"github.com/bengearig/ralph/internal/vecindex"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type fixedSearch struct{ res search.Result }

func (f fixedSearch) Search(context.Context, search.Query) (search.Result, error) { return f.res, nil }

func fixedSearchClient(t *testing.T, res search.Result) *mcp.ClientSession {
	t.Helper()
	d := testutil.NewDB(t, db.Migrations)
	return connect(t, Options{
		Name: "ralph", Actor: actor,
		Open:      staticOpen(units.NewService(d)),
		Documents: staticDocs(documents.NewService(d)),
		Search:    func(context.Context) (search.Service, error) { return fixedSearch{res}, nil },
	})
}

func TestMCPSearchHybridTextFirstLines(t *testing.T) {
	hits := []search.Hit{{Kind: search.KindTask, ID: "T-1", Title: "Feed", Status: "TODO", Snippet: "fruit", Score: 0.03, Sources: []string{"keyword", "semantic"}}}
	for _, c := range []struct {
		mode, notice, first string
	}{
		{search.ModeHybrid, "", "Hybrid search: 1 result(s)"},
		{search.ModeKeyword, "", "Keyword search: 1 result(s)"},
		{search.ModeKeywordFallback, "semantic search is unavailable (down); showing keyword results", "Keyword search: 1 result(s)"},
		{search.ModeHybrid, "semantic index is behind by 3 records: run ralph index", "Hybrid search: 1 result(s)"},
	} {
		cs := fixedSearchClient(t, search.Result{Mode: c.mode, Notice: c.notice, Hits: hits})
		res := call(t, cs, "search", map[string]any{"query": "feed"})
		if res.IsError {
			t.Fatal(text(res))
		}
		lines := strings.Split(text(res), "\n")
		want := []string{c.first}
		if c.notice != "" {
			want = append(want, "Notice: "+c.notice)
		}
		for i, w := range want {
			if lines[i] != w {
				t.Errorf("%s: line %d = %q, want %q", c.mode, i, lines[i], w)
			}
		}
		if c.notice == "" && strings.HasPrefix(lines[1], "Notice") {
			t.Errorf("%s: unexpected notice line", c.mode)
		}
		out := res.StructuredContent.(map[string]any)
		if out["mode"] != c.mode || (c.notice != "" && out["notice"] != c.notice) {
			t.Errorf("structured = %#v", out)
		}
		if src := searchHits(t, out)[0]["sources"].([]any); len(src) != 2 {
			t.Errorf("sources = %v", src)
		}
	}
}

func TestMCPSearchDescriptionMentionsModes(t *testing.T) {
	d := tools(t, newSearchClient(t))["search"].Description
	for _, want := range []string{
		"semantic index is configured", "combine keyword and semantic matches", "mode says which search ran",
		"sources says how each hit was found", "rank score that cannot be compared with a keyword score",
	} {
		if !strings.Contains(d, want) {
			t.Errorf("description lacks %q", want)
		}
	}
}

func TestMCPSearchKeywordOutputUnchanged(t *testing.T) {
	cs := newSearchClient(t)
	_, taskID := seedSearch(t, cs)
	res := call(t, cs, "search", map[string]any{"query": "fruit", "kinds": []string{"task"}})
	want := "Keyword search: 1 result(s)\n" + taskID + " task PROPOSED Feed the lemur\n  [fruit] twice daily fed"
	if got := text(res); got != want {
		t.Errorf("text = %q", got)
	}
	out := res.StructuredContent.(map[string]any)
	if _, ok := out["notice"]; ok {
		t.Error("notice present for keyword search")
	}
	if _, ok := searchHits(t, out)[0]["sources"]; ok {
		t.Error("sources present for keyword search")
	}
}

func fallbackSearchClient(t *testing.T) *mcp.ClientSession {
	t.Helper()
	d := testutil.NewDB(t, db.Migrations)
	us := units.NewService(d)
	if _, err := us.Create(context.Background(), units.NewUnit{Level: units.LevelBrief, Title: "Lemur brief", Body: "b"}); err != nil {
		t.Fatal(err)
	}
	fake := embedtest.NewFake(8)
	fake.FailWith(embed.ErrUnavailable)
	idx, err := vecindex.Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = idx.Close() })
	sem := semantic.NewSearcher(semantic.SearcherDeps{DB: d, Index: idx, Provider: fake, Settings: embed.Settings{ChunkChars: 1200}})
	svc := search.NewHybrid(search.New(d), sem, search.HybridOptions{})
	return connect(t, Options{
		Name: "ralph", Actor: actor,
		Open:      staticOpen(us),
		Documents: staticDocs(documents.NewService(d)),
		Search:    func(context.Context) (search.Service, error) { return svc, nil },
	})
}

func TestMCPSearchFallbackText(t *testing.T) {
	res := call(t, fallbackSearchClient(t), "search", map[string]any{"query": "lemur"})
	if res.IsError {
		t.Fatal(text(res))
	}
	lines := strings.Split(text(res), "\n")
	if lines[0] != "Keyword search: 1 result(s)" || !strings.HasPrefix(lines[1], "Notice: semantic search is unavailable (") ||
		!strings.HasSuffix(lines[1], "); showing keyword results") {
		t.Errorf("lines = %q", lines[:2])
	}
}

func TestMCPSearchFallbackStructuredNotice(t *testing.T) {
	res := call(t, fallbackSearchClient(t), "search", map[string]any{"query": "lemur"})
	out := res.StructuredContent.(map[string]any)
	notice, _ := out["notice"].(string)
	if out["mode"] != search.ModeKeywordFallback || !strings.HasPrefix(notice, "semantic search is unavailable (") {
		t.Errorf("structured = %#v", out)
	}
}
