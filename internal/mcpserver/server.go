// Package mcpserver exposes ralph's records to an agent over the Model
// Context Protocol. It never writes to standard output, which belongs to the
// stdio transport.
package mcpserver

import (
	"context"
	"sync"

	"github.com/bengearig/ralph/internal/units"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Options configures the server. Open supplies the units service on the first
// tool call that needs it.
type Options struct {
	Name    string
	Version string
	Open    func(ctx context.Context) (units.Service, error)
	Actor   string
}

type server struct {
	opts Options

	mu  sync.Mutex
	svc units.Service
}

// New builds a server with ralph's tools registered.
func New(opts Options) *mcp.Server {
	s := &server{opts: opts}
	srv := mcp.NewServer(&mcp.Implementation{Name: opts.Name, Version: opts.Version}, nil)
	s.addStatusTool(srv)
	return srv
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
	return svc, nil
}

// handle is a tool body: it returns the structured result, a short text
// rendering, and an error that becomes an error result.
type handle[In any] func(ctx context.Context, svc units.Service, in In) (out any, text string, err error)

// addTool registers a tool whose failures, including a failure to open the
// service, are error results and never protocol errors.
func addTool[In any](s *server, srv *mcp.Server, t *mcp.Tool, fn handle[In]) {
	mcp.AddTool(srv, t, func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, any, error) {
		ctx = units.WithActor(ctx, s.opts.Actor)
		svc, err := s.service(ctx)
		if err != nil {
			return errorResult(err), nil, nil
		}
		out, text, err := fn(ctx, svc, in)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}, out, nil
	})
}
