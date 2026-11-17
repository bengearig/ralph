package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"time"

	"github.com/bengearig/ralph/internal/db"
	"github.com/bengearig/ralph/internal/project"
	"github.com/bengearig/ralph/internal/tickets"
	"github.com/bengearig/ralph/internal/units"
	"github.com/spf13/cobra"
)

// autoSyncWanted is the first check of every automatic pass; it reads only the configuration.
func (a *app) autoSyncWanted() bool {
	return a.cfg != nil && a.root != "" && a.setting("taskmanager.provider") == "jira" && a.setting("jira.auto_sync") != "false"
}

// autoRunner builds a Runner for the automatic pass over d, with the sync timeout of the settings. A task manager
// that cannot be built, such as one with no site or token, is logged at debug level and gives ok false.
func (a *app) autoRunner(ctx context.Context, d *db.DB) (r *tickets.Runner, timeout time.Duration, ok bool) {
	system, settings, err := openTaskManager(ctx, a)
	if err != nil {
		slog.Debug("automatic ticket sync skipped", "error", err)
		return nil, 0, false
	}
	deps := tickets.Deps{
		Units: units.NewService(d), DB: d, System: system, Settings: settings,
		ProjectID: a.setting("project.id"), Dir: project.RalphDir(a.root), Now: time.Now,
	}
	return tickets.NewRunner(func(ctx context.Context) error { return tickets.Auto(ctx, deps) }, settings.SyncTimeout), settings.SyncTimeout, true
}

// skipsAutoSync reports whether cmd is one that never starts an automatic pass.
func skipsAutoSync(cmd *cobra.Command) bool {
	for c := cmd; c != nil; c = c.Parent() {
		switch c.Name() {
		case "version", "help", "init", "config", "completion", "mcp", "tickets", cobra.ShellCompRequestCmd, cobra.ShellCompNoDescRequestCmd:
			return true
		}
	}
	return false
}

// autoSyncAfterRun pushes tickets after a command that wrote. It never returns an error: the command has succeeded.
func (a *app) autoSyncAfterRun(cmd *cobra.Command) {
	if skipsAutoSync(cmd) || db.Writes() <= a.writes || !a.autoSyncWanted() {
		return
	}
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	d, err := openStore(ctx, a, db.Options{MustExist: true})
	if errors.Is(err, fs.ErrNotExist) {
		return
	}
	if err != nil {
		slog.Debug("automatic ticket sync skipped", "error", err)
		return
	}
	defer func() { _ = d.Close() }()
	runner, timeout, ok := a.autoRunner(ctx, d)
	if !ok {
		return
	}
	start := time.Now()
	runner.Trigger()
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	runner.Wait(waitCtx)
	reportFailed(ctx, cmd.ErrOrStderr(), d, start)
}

func reportFailed(ctx context.Context, w io.Writer, d *db.DB, since time.Time) {
	rows, err := tickets.NewStore(d).List(ctx)
	if err != nil {
		slog.Debug("automatic ticket sync: read failures", "error", err)
		return
	}
	n := 0
	for _, r := range rows {
		if r.FailedStep != "" && !r.FailedAt.Before(since) {
			n++
		}
	}
	switch n {
	case 0:
	case 1:
		_, _ = fmt.Fprintln(w, `ralph: warning: 1 ticket failed to sync; run "ralph tickets"`)
	default:
		_, _ = fmt.Fprintf(w, "ralph: warning: %d tickets failed to sync; run \"ralph tickets\"\n", n)
	}
}
