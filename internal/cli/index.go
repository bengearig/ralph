package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os/signal"
	"syscall"

	"github.com/bengearig/ralph/internal/db"
	"github.com/bengearig/ralph/internal/embed"
	"github.com/bengearig/ralph/internal/project"
	"github.com/bengearig/ralph/internal/semantic"
	"github.com/bengearig/ralph/internal/vecindex"
	"github.com/spf13/cobra"
)

const errNoProvider = "no embedding provider is configured: set embeddings.provider to ollama"

const digestChars = 12

type indexStatus struct {
	Provider   string `json:"provider"`
	URL        string `json:"url"`
	Model      string `json:"model"`
	Digest     string `json:"digest"`
	Reachable  bool   `json:"reachable"`
	Reason     string `json:"reason,omitempty"`
	Dimensions int    `json:"dimensions"`
	Indexed    int    `json:"indexed"`
	Total      int    `json:"total"`
	Waiting    int    `json:"waiting"`
	Gone       int    `json:"gone"`
	File       string `json:"file"`
	Bytes      int64  `json:"bytes"`
}

func newIndexCommand(a *app) *cobra.Command {
	var status, asJSON bool
	cmd := &cobra.Command{
		Use:   "index",
		Short: "Embed the records for semantic search, or show the state of the index",
		Long: "Embed every brief, slice, task, document and note that is not yet in the vector index, and remove the entries of\n" +
			"records that are gone or changed. Running it again continues where it stopped. It needs embeddings.provider to be set.\n" +
			"With --status nothing is embedded: it shows the provider, the model and how much of the index is up to date.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if asJSON && !status {
				return errors.New("--json is for --status; a plain run prints progress lines")
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()
			d, err := openStore(ctx, a, db.Options{MustExist: true})
			if errors.Is(err, fs.ErrNotExist) {
				return errors.New(errNoDatabase)
			}
			if err != nil {
				return err
			}
			defer func() { _ = d.Close() }()

			provider, settings, err := embed.FromConfig(a.cfg)
			if err != nil {
				return err
			}
			if provider == nil {
				return errors.New(errNoProvider)
			}
			idx, err := vecindex.Open(project.IndexPath(a.root))
			if err != nil {
				return err
			}
			defer func() { _ = idx.Close() }()

			deps := semantic.SyncDeps{DB: d, Index: idx, Provider: provider, Settings: settings}
			if status {
				return runIndexStatus(ctx, cmd, a, deps, asJSON)
			}
			return runIndex(ctx, cmd, deps)
		},
	}
	cmd.Flags().BoolVar(&status, "status", false, "embed nothing; show the provider, the model and what is indexed")
	cmd.Flags().BoolVar(&asJSON, "json", false, "with --status, print one JSON object")
	return cmd
}

func runIndex(ctx context.Context, cmd *cobra.Command, deps semantic.SyncDeps) error {
	out := cmd.OutOrStdout()
	deps.Report = func(line string) { _, _ = fmt.Fprintln(out, line) }
	res, err := semantic.Sync(ctx, deps, semantic.SyncOptions{})
	if errors.Is(err, context.Canceled) && ctx.Err() != nil {
		return fmt.Errorf("interrupted after %d records; run ralph index again to continue", res.Embedded)
	}
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "indexed %d records (%d chunks), removed %d\n", res.Embedded, res.Chunks, res.Removed)
	if res.Embedded == 0 && res.Removed == 0 {
		_, _ = fmt.Fprintln(out, "index is up to date")
	}
	return nil
}

func runIndexStatus(ctx context.Context, cmd *cobra.Command, a *app, deps semantic.SyncDeps, asJSON bool) error {
	st := indexStatus{Provider: deps.Settings.Provider, URL: deps.Settings.URL, Model: deps.Settings.Model, File: project.IndexPath(a.root)}
	info, err := deps.Provider.Describe(ctx)
	if err != nil {
		st.Reason = err.Error()
	} else {
		st.Reachable = true
		if info.Model != "" {
			st.Model = info.Model
		}
		st.Digest = info.Digest
		if len(st.Digest) > digestChars {
			st.Digest = st.Digest[:digestChars]
		}
	}
	stats, err := deps.Index.Stats(ctx)
	if err != nil {
		return err
	}
	st.Dimensions, st.Bytes = stats.Dimensions, stats.Bytes
	waiting, gone, err := semantic.Pending(ctx, deps)
	if err != nil {
		return err
	}
	records, err := semantic.Records(ctx, deps.DB)
	if err != nil {
		return err
	}
	st.Total, st.Waiting, st.Gone = len(records), len(waiting), len(gone)
	st.Indexed = st.Total - st.Waiting

	out := cmd.OutOrStdout()
	if asJSON {
		return writeJSON(out, st)
	}
	_, _ = fmt.Fprintf(out, "provider: %s %s\n", st.Provider, st.URL)
	if st.Digest != "" {
		_, _ = fmt.Fprintf(out, "model: %s %s\n", st.Model, st.Digest)
	} else {
		_, _ = fmt.Fprintf(out, "model: %s\n", st.Model)
	}
	if st.Reachable {
		_, _ = fmt.Fprintln(out, "reachable: yes")
	} else {
		_, _ = fmt.Fprintf(out, "reachable: no (%s)\n", st.Reason)
	}
	if st.Dimensions > 0 {
		_, _ = fmt.Fprintf(out, "dimensions: %d\n", st.Dimensions)
	} else {
		_, _ = fmt.Fprintln(out, "dimensions: none yet")
	}
	_, _ = fmt.Fprintf(out, "records: %d of %d indexed, %d waiting, %d to remove\n", st.Indexed, st.Total, st.Waiting, st.Gone)
	_, _ = fmt.Fprintf(out, "file: %s (%s)\n", st.File, formatSize(st.Bytes))
	return nil
}
