package cli

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/bengearig/ralph/internal/db"
	"github.com/bengearig/ralph/internal/documents"
	"github.com/bengearig/ralph/internal/harness/all"
	"github.com/bengearig/ralph/internal/identity"
	"github.com/bengearig/ralph/internal/integration"
	"github.com/bengearig/ralph/internal/issuesource"
	"github.com/bengearig/ralph/internal/mcpserver"
	"github.com/bengearig/ralph/internal/quick"
	"github.com/bengearig/ralph/internal/search"
	"github.com/bengearig/ralph/internal/tickets"
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

func quickNotice(svc units.Service) mcpserver.NoticeSource {
	return func(ctx context.Context) ([]mcpserver.Notice, error) {
		msgs, err := quick.PendingMessages(ctx, svc)
		if err != nil {
			return nil, err
		}
		var notices []mcpserver.Notice
		for _, m := range msgs {
			notices = append(notices, mcpserver.Notice{Code: "quick_unfinalized", Message: m})
		}
		return notices, nil
	}
}

// runMCP serves the MCP server on t until the client disconnects or ctx ends,
// then closes the database if a tool call opened it.
func runMCP(ctx context.Context, a *app, t mcp.Transport) error {
	if a.root == "" {
		return errNotInRepository
	}

	var (
		mu         sync.Mutex
		opened     *db.DB
		closeIndex = func() {}
	)
	defer func() {
		mu.Lock()
		defer mu.Unlock()
		closeIndex()
		if opened != nil {
			_ = opened.Close()
		}
	}()
	// store opens the one database handle on first use; a failure is not kept.
	store := func(ctx context.Context) (*db.DB, error) {
		mu.Lock()
		defer mu.Unlock()
		if opened != nil {
			return opened, nil
		}
		d, err := openStore(ctx, a, db.Options{MustExist: true})
		if err != nil {
			return nil, err
		}
		opened = d
		return d, nil
	}

	var (
		syncOnce   sync.Once
		syncRunner *tickets.Runner
	)
	autoSync := func(ctx context.Context) {
		if !a.autoSyncWanted() {
			return
		}
		syncOnce.Do(func() {
			d, err := store(ctx)
			if err != nil {
				slog.Debug("automatic ticket sync skipped", "error", err)
				return
			}
			if r, _, ok := a.autoRunner(ctx, d); ok {
				syncRunner = r
			}
		})
		if syncRunner != nil {
			syncRunner.Trigger()
		}
	}

	srv := mcpserver.New(mcpserver.Options{
		Name:        "ralph",
		Version:     version.String(),
		Actor:       identity.Resolve(ctx, a.root),
		Planning:    mcpserver.PlanningSettings{ConfirmOutline: a.setting("plan.confirm_outline") == "true"},
		AfterWrite:  autoSync,
		Sources:     []mcpserver.NoticeSource{integrationNotice(a.root), semanticNotice(a, store), ticketsNotice(a, store)},
		UnitSources: []func(units.Service) mcpserver.NoticeSource{quickNotice},
		Quick: func(ctx context.Context, svc units.Service) (mcpserver.Quick, error) {
			return newQuickService(ctx, a, svc)
		},
		Open: func(ctx context.Context) (units.Service, error) {
			d, err := store(ctx)
			if err != nil {
				return nil, err
			}
			return units.NewService(d), nil
		},
		Documents: func(ctx context.Context) (documents.Service, error) {
			d, err := store(ctx)
			if err != nil {
				return nil, err
			}
			return newDocumentsService(a, d), nil
		},
		Issues: func(ctx context.Context) (issuesource.Source, error) { return openIssueSource(ctx, a) },
		Search: func(ctx context.Context) (search.Service, error) {
			d, err := store(ctx)
			if err != nil {
				return nil, err
			}
			svc, closer, err := newSearchService(a, d)
			if err != nil {
				return nil, err
			}
			mu.Lock()
			closeIndex = closer
			mu.Unlock()
			return svc, nil
		},
	})
	err := srv.Run(ctx, t)
	syncOnce.Do(func() {})
	if syncRunner != nil {
		waitCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		syncRunner.Wait(waitCtx)
		cancel()
	}
	if err == nil || errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}
