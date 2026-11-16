package cli

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/bengearig/ralph/internal/db"
	"github.com/bengearig/ralph/internal/project"
	"github.com/spf13/cobra"
)

func newBackupCommand(a *app) *cobra.Command {
	var output string
	cmd := &cobra.Command{
		Use:   "backup",
		Short: "Write a consistent snapshot of the database",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if a.root == "" {
				return errNotInRepository
			}
			dest := output
			if dest == "" {
				name := "manual-" + time.Now().UTC().Format("20060102T150405Z") + ".db"
				dest = filepath.Join(project.BackupsDir(a.root), name)
			}
			abs, err := filepath.Abs(dest)
			if err != nil {
				return fmt.Errorf("resolve %q: %w", dest, err)
			}
			d, err := openStore(cmd.Context(), a, db.Options{MustExist: true, NoMigrate: true})
			if err != nil {
				return err
			}
			err = d.Backup(cmd.Context(), abs)
			if cerr := d.Close(); err == nil {
				err = cerr
			}
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), abs)
			return err
		},
	}
	cmd.Flags().StringVar(&output, "output", "", "write the snapshot to this path instead of the backups directory")
	return cmd
}
