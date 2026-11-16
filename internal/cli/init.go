package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/bengearig/ralph/internal/config"
	"github.com/bengearig/ralph/internal/db"
	"github.com/bengearig/ralph/internal/project"
	"github.com/spf13/cobra"
)

func newInitCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Set up ralph in the current git repository",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := os.Getwd()
			if err != nil {
				return err
			}
			root, err := project.FindRoot(dir)
			if errors.Is(err, project.ErrNotRepository) {
				return fmt.Errorf("%w: run `git init` first", errNotInRepository)
			}
			if err != nil {
				return err
			}

			// Validate the project file before anything is written.
			if err := project.CheckProjectFile(root); err != nil {
				return err
			}
			path := project.StorePath(root)
			if err := os.MkdirAll(project.RalphDir(root), 0o755); err != nil {
				return err
			}
			res, err := db.Migrate(cmd.Context(), path, db.Options{
				Mode:      db.MigrateAuto,
				BackupDir: project.BackupsDir(root),
			})
			if err != nil {
				return err
			}
			id, changed, err := project.EnsureProjectFile(root)
			if err != nil {
				return err
			}

			if _, err := config.Load(config.Options{
				Registry:    config.DefaultRegistry(),
				UserFile:    config.DefaultUserFile(),
				ProjectRoot: root,
				Getenv:      os.LookupEnv,
			}); err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			ralphDir := project.RalphDir(root)
			if changed || len(res.Applied) > 0 {
				_, _ = fmt.Fprintf(out, "initialized ralph in %s\n", ralphDir)
			} else {
				_, _ = fmt.Fprintf(out, "ralph is already set up in %s\n", ralphDir)
			}
			_, _ = fmt.Fprintf(out, "project id: %s\n", id)
			_, _ = fmt.Fprintf(out, "database: .ralph/%s (schema version %d)\n", project.StoreFile, res.To)
			return nil
		},
	}
}
