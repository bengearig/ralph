package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"

	"github.com/bengearig/ralph/internal/config"
	"github.com/bengearig/ralph/internal/db"
	"github.com/bengearig/ralph/internal/harness"
	"github.com/bengearig/ralph/internal/harness/all"
	"github.com/bengearig/ralph/internal/integration"
	"github.com/bengearig/ralph/internal/project"
	"github.com/bengearig/ralph/internal/version"
	"github.com/spf13/cobra"
)

// initAdapters lists the adapters init considers; tests replace it.
var initAdapters = all.Adapters

func newInitCommand() *cobra.Command {
	var harnessNames []string
	cmd := &cobra.Command{
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

			named, err := namedAdapters(cmd.Context(), root, harnessNames)
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
			ignored, err := project.EnsureIgnore(root, project.LocalIgnoreEntries())
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
			if changed || len(ignored) > 0 || len(res.Applied) > 0 {
				_, _ = fmt.Fprintf(out, "initialized ralph in %s\n", ralphDir)
			} else {
				_, _ = fmt.Fprintf(out, "ralph is already set up in %s\n", ralphDir)
			}
			_, _ = fmt.Fprintf(out, "project id: %s\n", id)
			_, _ = fmt.Fprintf(out, "database: .ralph/%s (schema version %d)\n", project.StoreFile, res.To)
			return installIntegration(cmd, root, named)
		},
	}
	cmd.Flags().StringArrayVar(&harnessNames, "harness", nil, "install the integration for this harness (repeatable); default: each harness detected")
	return cmd
}

func namedAdapters(ctx context.Context, root string, names []string) ([]harness.Adapter, error) {
	known := initAdapters()
	var adapters []harness.Adapter
	for _, n := range names {
		i := slices.IndexFunc(known, func(a harness.Adapter) bool { return a.Name() == n })
		if i < 0 {
			knownNames := make([]string, len(known))
			for j, a := range known {
				knownNames[j] = a.Name()
			}
			sort.Strings(knownNames)
			return nil, fmt.Errorf("unknown harness %q; known harnesses: %s", n, strings.Join(knownNames, ", "))
		}
		if d := known[i].Detect(ctx, root); d.Unsupported != "" {
			return nil, fmt.Errorf("%s: unsupported: %s", n, d.Unsupported)
		}
		adapters = append(adapters, known[i])
	}
	return adapters, nil
}

// installIntegration runs after the project, database and ignore file are in
// place; a failure here does not undo them.
func installIntegration(cmd *cobra.Command, root string, named []harness.Adapter) error {
	out := cmd.OutOrStdout()
	detections := map[string]harness.Detection{}
	selected := named
	if len(selected) == 0 {
		for _, a := range initAdapters() {
			d := a.Detect(cmd.Context(), root)
			if d.Found && d.Unsupported != "" {
				_, _ = fmt.Fprintf(out, "%s: unsupported: %s\n", a.Name(), d.Unsupported)
				continue
			}
			if d.Found {
				selected = append(selected, a)
				detections[a.Name()] = d
			}
		}
	}
	if len(selected) == 0 {
		_, _ = fmt.Fprintf(out, "no harness detected; run \"ralph init --harness <name>\" to install the integration, where <name> is one of: %s\n", strings.Join(all.Names(), ", "))
		return nil
	}
	for _, a := range selected {
		d, ok := detections[a.Name()]
		if !ok {
			d = a.Detect(cmd.Context(), root)
		}
		detail := d.Version
		if detail == "" {
			detail = d.Detail
		}
		_, _ = fmt.Fprintf(out, "%s: %s\n", a.Name(), detail)

		in, err := integration.InputsFor(root)
		if err != nil {
			return err
		}
		arts, err := a.Artifacts(in)
		if err != nil {
			return fmt.Errorf("%s: %w", a.Name(), err)
		}
		results, err := integration.Apply(cmd.Context(), root, a.Name(), arts, version.String())
		for _, r := range results {
			if r.Action == integration.ActionSkipped {
				continue
			}
			line := fmt.Sprintf("%s %s", r.Action, r.Path)
			if r.Detail != "" {
				line += " (" + r.Detail + ")"
			}
			_, _ = fmt.Fprintln(out, line)
			if r.Action == integration.ActionKept {
				_, _ = fmt.Fprintf(out, "  to stop this report, add %q to \"customized\" in .ralph/%s\n", r.Path, project.IntegrationFile)
			}
		}
		if err != nil {
			return fmt.Errorf("%s: %w", a.Name(), err)
		}
	}
	return nil
}
