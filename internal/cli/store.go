package cli

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/bengearig/ralph/internal/db"
	"github.com/bengearig/ralph/internal/project"
	"github.com/spf13/cobra"
)

var errNotInRepository = errors.New("not in a git repository")

func storeOptions(a *app, opts db.Options) (string, db.Options, error) {
	if a.root == "" {
		return "", opts, errNotInRepository
	}
	opts.Mode = db.MigrateAuto
	if v, ok := a.cfg.Get("storage.migrate"); ok && v.Value == "manual" {
		opts.Mode = db.MigrateManual
	}
	opts.BackupDir = project.BackupsDir(a.root)
	if v, ok := a.cfg.Get("storage.backups.keep"); ok {
		if n, err := strconv.Atoi(v.Value); err == nil {
			opts.BackupKeep = n
		}
	}
	return project.StorePath(a.root), opts, nil
}

// openStore opens the project's database, honouring storage.migrate.
func openStore(ctx context.Context, a *app, opts db.Options) (*db.DB, error) {
	path, opts, err := storeOptions(a, opts)
	if err != nil {
		return nil, err
	}
	return db.Open(ctx, path, opts)
}

func newMigrateCommand(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "migrate",
		Short: "Apply pending database migrations",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			path, opts, err := storeOptions(a, db.Options{})
			if err != nil {
				return err
			}
			res, err := db.Migrate(cmd.Context(), path, opts)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			_, _ = fmt.Fprintf(out, "schema version %d\n", res.To)
			if len(res.Applied) > 0 {
				_, _ = fmt.Fprintf(out, "applied: %s\n", strings.Join(res.Applied, ", "))
			}
			return nil
		},
	}
}
