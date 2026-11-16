package cli

import (
	"context"
	"errors"
	"io"
	"os/signal"
	"sync"
	"syscall"

	"github.com/bengearig/ralph/internal/db"
	"github.com/bengearig/ralph/internal/harness/all"
	"github.com/bengearig/ralph/internal/identity"
	"github.com/bengearig/ralph/internal/integration"
	"github.com/bengearig/ralph/internal/mcpserver"
	"github.com/bengearig/ralph/internal/units"
	"github.com/bengearig/ralph/internal/version"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"
)

func newMCPCommand(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "mcp",
		Short: "Serve ralph's records to an agent over the Model Context Protocol on standard input and output",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()
			return runMCP(ctx, a, &mcp.StdioTransport{})
		},
	}
}

func integrationNotice(root string) mcpserver.NoticeSource {
	return func(ctx context.Context) ([]mcpserver.Notice, error) {
		stale, err := integration.Check(ctx, root, all.Adapters())
		if err != nil || len(stale) == 0 {
			return nil, err
		}
		return []mcpserver.Notice{{
			Code:    "integration_outdated",
			Message: `ralph's integration files are out of date. Run "ralph init" to update them.`,
		}}, nil
	}
}

// runMCP serves the MCP server on t until the client disconnects or ctx ends,
// then closes the database if a tool call opened it.
func runMCP(ctx context.Context, a *app, t mcp.Transport) error {
	if a.root == "" {
		return errNotInRepository
	}

	var (
		mu     sync.Mutex
		opened *db.DB
	)
	defer func() {
		mu.Lock()
		defer mu.Unlock()
		if opened != nil {
			_ = opened.Close()
		}
	}()

	srv := mcpserver.New(mcpserver.Options{
		Name:    "ralph",
		Version: version.String(),
		Actor:   identity.Resolve(ctx, a.root),
		Sources: []mcpserver.NoticeSource{integrationNotice(a.root)},
		Quick: func(ctx context.Context, svc units.Service) (mcpserver.Quick, error) {
			return newQuickService(ctx, a, svc)
		},
		Open: func(ctx context.Context) (units.Service, error) {
			d, err := openStore(ctx, a, db.Options{MustExist: true})
			if err != nil {
				return nil, err
			}
			mu.Lock()
			opened = d
			mu.Unlock()
			return units.NewService(d), nil
		},
	})
	err := srv.Run(ctx, t)
	if err == nil || errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}
