// Package mcpserver exposes ralph's records to an agent over the Model
// Context Protocol. It never writes to standard output, which belongs to the
// stdio transport.
package mcpserver

import (
	"context"
	"sync"

	"github.com/bengearig/ralph/internal/db"
	"github.com/bengearig/ralph/internal/documents"
	"github.com/bengearig/ralph/internal/issuesource"
	"github.com/bengearig/ralph/internal/search"
	"github.com/bengearig/ralph/internal/tickets"
	"github.com/bengearig/ralph/internal/units"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Options configures the server. Open supplies the units service on the first
// tool call that needs it.
type Options struct {
	Name    string
	Version string
	Open    func(ctx context.Context) (units.Service, error)
	Actor   string
	Sources []NoticeSource
	// UnitSources build notice sources from the units service; each is called
	// once, after the service has been opened.
	UnitSources []func(svc units.Service) NoticeSource
	// Quick builds the quick work service on the first quick tool call; nil
	// leaves the quick tools out.
	Quick func(ctx context.Context, svc units.Service) (Quick, error)
	// Planning is fixed when the server starts.
	Planning PlanningSettings
	// Documents supplies the documents service on the first document tool
	// call; nil leaves the document tools out.
	Documents func(ctx context.Context) (documents.Service, error)
	// Search supplies the search service on the first search call; nil leaves
	// the search tool out.
	Search func(ctx context.Context) (search.Service, error)
	// Issues supplies the issue source on the first issue tool call; nil
	// leaves the issue tools out.
	Issues func(ctx context.Context) (issuesource.Source, error)
	// Tickets supplies what unit_create needs to link a new unit to its ticket; nil, like a nil Issues, leaves
	// unit_create without the issue input's effect.
	Tickets func(ctx context.Context, svc units.Service) (tickets.Deps, error)
	// AfterWrite is called, without being waited for, after a tool that is not read-only has committed a write.
	// Its context outlives the call; nil does nothing.
	AfterWrite func(ctx context.Context)
}

type server struct {
	opts Options

	mu  sync.Mutex
	svc units.Service

	unitSources []NoticeSource
	quickMu     sync.Mutex
	quick       Quick

	docMu sync.Mutex
	docs  documents.Service

	searchMu sync.Mutex
	search   search.Service

	issueMu sync.Mutex
	issues  issuesource.Source

	toolsMu  sync.Mutex
	readOnly map[string]bool

	noticeMu sync.Mutex
	seen     map[Notice]struct{}
	queued   []Notice
}

// New builds a server with ralph's tools registered.
func New(opts Options) *mcp.Server {
	s := &server{opts: opts, readOnly: map[string]bool{}}
	srv := mcp.NewServer(&mcp.Implementation{Name: opts.Name, Version: opts.Version}, nil)
	srv.AddReceivingMiddleware(s.afterWrite)
	s.addStatusTool(srv)
	s.addUnitTools(srv)
	s.addQuickTools(srv)
	s.addDocTools(srv)
	s.addSearchTool(srv)
	s.addIssueTool(srv)
	s.addPlanningTool(srv)
	return srv
}

// noteTool records whether a tool is marked read-only.
func (s *server) noteTool(t *mcp.Tool) {
	s.toolsMu.Lock()
	defer s.toolsMu.Unlock()
	s.readOnly[t.Name] = t.Annotations != nil && t.Annotations.ReadOnlyHint
}

func (s *server) isReadOnly(name string) bool {
	s.toolsMu.Lock()
	defer s.toolsMu.Unlock()
	return s.readOnly[name]
}

// afterWrite calls AfterWrite once a tool call that is not read-only has changed the write count.
func (s *server) afterWrite(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		call, ok := req.(*mcp.CallToolRequest)
		if s.opts.AfterWrite == nil || method != "tools/call" || !ok || call.Params == nil || s.isReadOnly(call.Params.Name) {
			return next(ctx, method, req)
		}
		before := db.Writes()
		res, err := next(ctx, method, req)
		if db.Writes() != before {
			go s.opts.AfterWrite(context.WithoutCancel(ctx))
		}
		return res, err
	}
}

// openError marks a failure to open the service, as opposed to one raised by a
// tool, so that the same sentinel error means "not set up" only in this case.
type openError struct{ err error }

func (e *openError) Error() string { return e.err.Error() }

func (e *openError) Unwrap() error { return e.err }

// service returns the kept service, opening it if this is the first call or
// every earlier attempt failed.
func (s *server) service(ctx context.Context) (units.Service, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.svc != nil {
		return s.svc, nil
	}
	svc, err := s.opts.Open(ctx)
	if err != nil {
		return nil, &openError{err}
	}
	s.svc = svc
	for _, build := range s.opts.UnitSources {
		if build != nil {
			s.unitSources = append(s.unitSources, build(svc))
		}
	}
	return svc, nil
}

// handle is a tool body: it returns the structured result, a short text
// rendering, and an error that becomes an error result.
type handle[In any] func(ctx context.Context, svc units.Service, in In) (out any, text string, err error)

// addTool registers a tool whose failures, including a failure to open the
// service, are error results and never protocol errors.
func addTool[In any](s *server, srv *mcp.Server, t *mcp.Tool, fn handle[In]) {
	if sc, ok := t.OutputSchema.(*jsonschema.Schema); ok {
		withNoticesSchema(sc)
	}
	s.noteTool(t)
	mcp.AddTool(srv, t, func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, any, error) {
		ctx = units.WithActor(ctx, s.opts.Actor)
		res, out := callTool(ctx, s, fn, in)
		return s.deliver(ctx, res, out)
	})
}

// deliver attaches the session's fresh notices to a tool's result.
func (s *server) deliver(ctx context.Context, res *mcp.CallToolResult, out any) (*mcp.CallToolResult, any, error) {
	fresh := s.collectNotices(ctx)
	out, err := attachNotices(res, out, fresh)
	if err != nil {
		res = errorResult(err)
		_, _ = attachNotices(res, nil, fresh)
		out = nil
	}
	return res, out, nil
}

// callTool runs the handler and returns its result and structured output; a
// failure is an error result with no output.
func callTool[In any](ctx context.Context, s *server, fn handle[In], in In) (*mcp.CallToolResult, any) {
	svc, err := s.service(ctx)
	if err != nil {
		return errorResult(err), nil
	}
	out, text, err := fn(ctx, svc, in)
	if err != nil {
		return errorResult(err), nil
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}, out
}
