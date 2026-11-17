package cli

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/bengearig/ralph/internal/config"
	"github.com/bengearig/ralph/internal/project"
	"github.com/spf13/cobra"
)

const logLevelFlag = "log-level"

// app holds what the root command resolves before a command runs.
type app struct {
	root string
	cfg  *config.Config

	actor string
}

func NewRootCommand() *cobra.Command {
	a := &app{}
	cmd := &cobra.Command{
		Use:           "ralph",
		Short:         "Plan and run RALPH-method work with coding agents",
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if skipsConfig(cmd) {
				return nil
			}
			return a.load(cmd)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}
	cmd.PersistentFlags().String(logLevelFlag, "", "minimum log severity: debug, info, warn or error (setting log.level)")
	cmd.AddCommand(newVersionCommand(), newConfigCommand(a), newMigrateCommand(a), newBackupCommand(a), newInitCommand(), newStatusCommand(a), newMCPCommand(a), newQuickCommand(a), newLoopCommand(a), newRunsCommand(a), newDocsCommand(a), newSearchCommand(a), newPRCommand(a))
	return cmd
}

func skipsConfig(cmd *cobra.Command) bool {
	if !cmd.HasParent() {
		return true
	}
	for c := cmd; c.HasParent(); c = c.Parent() {
		switch c.Name() {
		case "version", "init", "help", "completion", cobra.ShellCompRequestCmd, cobra.ShellCompNoDescRequestCmd:
			return true
		}
	}
	return false
}

func (a *app) load(cmd *cobra.Command) error {
	stderr := cmd.ErrOrStderr()
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	root, err := project.FindRoot(dir)
	if err != nil && !errors.Is(err, project.ErrNotRepository) {
		return err
	}
	if err != nil {
		root = ""
	}

	flags := map[string]string{}
	if f := cmd.Flags().Lookup(logLevelFlag); f != nil && f.Changed {
		flags[logLevelFlag] = f.Value.String()
	}
	userFile := config.DefaultUserFile()
	cfg, err := config.Load(config.Options{
		Registry:    config.DefaultRegistry(),
		UserFile:    userFile,
		ProjectRoot: root,
		Getenv:      os.LookupEnv,
		Flags:       flags,
	})
	var cfgErr *config.Error
	if errors.As(err, &cfgErr) {
		writeWarnings(stderr, cfgErr.Warnings)
		return err
	}
	if err != nil {
		return err
	}
	writeWarnings(stderr, cfg.Warnings())
	a.root, a.cfg = root, cfg

	var level slog.Level
	if v, ok := cfg.Get("log.level"); ok {
		if err := level.UnmarshalText([]byte(v.Value)); err != nil {
			return fmt.Errorf("log.level: %w", err)
		}
	}
	logger := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(logger)

	files := existingFiles(userFile, root)
	logger.Debug("configuration loaded", "project_root", root, "files", files)
	return nil
}

func writeWarnings(w io.Writer, warnings []config.Warning) {
	for _, warning := range warnings {
		_, _ = fmt.Fprintf(w, "ralph: warning: %s\n", warning)
	}
}

func existingFiles(userFile, root string) []string {
	candidates := []string{userFile}
	if root != "" {
		dir := project.RalphDir(root)
		candidates = append(candidates, filepath.Join(dir, project.ProjectFile), filepath.Join(dir, project.LocalFile))
	}
	var found []string
	for _, f := range candidates {
		if f == "" {
			continue
		}
		if info, err := os.Stat(f); err == nil && !info.IsDir() {
			found = append(found, f)
		}
	}
	return found
}
