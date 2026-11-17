package mcpserver

import (
	"context"
	"fmt"
	"strings"

	"github.com/bengearig/ralph/internal/search"
	"github.com/bengearig/ralph/internal/units"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const searchText = `Search over the project's records: documents, briefs, slices, tasks and the notes on them. query is plain words, not search syntax: punctuation has no special meaning, and double quotes keep a phrase together. A record must contain every word; only when no record does, the result is relaxed and its hits match some of the words. Archived and superseded documents and cancelled tasks (with their notes) are left out unless include_inactive is true, and then they are marked inactive. Earlier versions of documents are not searched; read them with doc_versions and doc_get. Each hit gives kind, id (for a note, the task, slice or brief it is on), title, status and a snippet with the matching words in square brackets, best match first. Read a hit with doc_get for a document and unit_get for anything else. When a semantic index is configured, results combine keyword and semantic matches: mode says which search ran (hybrid, keyword or keyword-fallback) and sources says how each hit was found (keyword, semantic or both); the score of a combined result is a rank score that cannot be compared with a keyword score. A notice, when present, says something about the search. kinds limits the search to some kinds; limit is 1 to 100 and defaults to 20.`

var searchKindEnum = []any{"brief", "slice", "task", "note", "document"}

type searchInput struct {
	Query           string   `json:"query" jsonschema:"plain words to search for; double quotes keep a phrase together"`
	Kinds           []string `json:"kinds,omitempty" jsonschema:"only records of these kinds; omit for all"`
	Limit           int      `json:"limit,omitempty" jsonschema:"most hits to return, 1 to 100; default 20"`
	IncludeInactive bool     `json:"include_inactive,omitempty" jsonschema:"also search archived and superseded documents and cancelled tasks"`
}

// searchService returns the kept search service, building it if this is the
// first call or every earlier attempt failed.
func (s *server) searchService(ctx context.Context) (search.Service, error) {
	s.searchMu.Lock()
	defer s.searchMu.Unlock()
	if s.search != nil {
		return s.search, nil
	}
	svc, err := s.opts.Search(ctx)
	if err != nil {
		return nil, &openError{err}
	}
	s.search = svc
	return svc, nil
}

func (s *server) addSearchTool(srv *mcp.Server) {
	if s.opts.Search == nil {
		return
	}
	out := outputFor[search.Result]()
	withNoticesSchema(out)
	one := 1.0
	hundred := 100.0
	tool := &mcp.Tool{
		Name:        "search",
		Description: searchText,
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
		InputSchema: schemaFor[searchInput](func(p map[string]*jsonschema.Schema) {
			p["kinds"].Items = enum(searchKindEnum)
			p["limit"].Minimum, p["limit"].Maximum = &one, &hundred
		}),
		OutputSchema: out,
	}
	s.noteTool(tool)
	mcp.AddTool(srv, tool, func(ctx context.Context, _ *mcp.CallToolRequest, in searchInput) (*mcp.CallToolResult, any, error) {
		ctx = units.WithActor(ctx, s.opts.Actor)
		res, o := s.runSearch(ctx, in)
		return s.deliver(ctx, res, o)
	})
}

func (s *server) runSearch(ctx context.Context, in searchInput) (*mcp.CallToolResult, any) {
	svc, err := s.searchService(ctx)
	if err != nil {
		return errorResult(err), nil
	}
	q := search.Query{Text: in.Query, Limit: in.Limit, IncludeInactive: in.IncludeInactive}
	for _, k := range in.Kinds {
		q.Kinds = append(q.Kinds, search.Kind(k))
	}
	r, err := svc.Search(ctx, q)
	if err != nil {
		return errorResult(err), nil
	}
	if r.Hits == nil {
		r.Hits = []search.Hit{}
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: searchSummary(r)}}}, r
}

func searchSummary(r search.Result) string {
	var b strings.Builder
	name := "Keyword"
	if r.Mode == search.ModeHybrid {
		name = "Hybrid"
	}
	fmt.Fprintf(&b, "%s search: %d result(s)", name, len(r.Hits))
	if r.Notice != "" {
		b.WriteString("\nNotice: " + r.Notice)
	}
	if r.Relaxed {
		b.WriteString("\nno result had every word; these match some of them")
	}
	for _, h := range r.Hits {
		fmt.Fprintf(&b, "\n%s %s %s %s", h.ID, h.Kind, h.Status, h.Title)
		if h.Snippet != "" {
			b.WriteString("\n  " + strings.ReplaceAll(h.Snippet, "\n", " "))
		}
	}
	return b.String()
}
