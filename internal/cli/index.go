package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os/signal"
	"strings"
	"syscall"
	"time"

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

	BuiltWith    *embed.Fingerprint `json:"built_with,omitempty"`
	State        string             `json:"state"`
	StaleReasons []string           `json:"stale_reasons,omitempty"`
	Rebuild      *rebuildStatus     `json:"rebuild,omitempty"`
}

type rebuildStatus struct {
	Done  int `json:"done"`
	Total int `json:"total"`
}

type rebuildPlanJSON struct {
	Reasons    []string `json:"reasons"`
	Records    int      `json:"records"`
	Chunks     int      `json:"chunks"`
	Done       int      `json:"done"`
	Resuming   bool     `json:"resuming"`
	Discarding bool     `json:"discarding"`
	Model      string   `json:"model"`
	URL        string   `json:"url"`
}

type rebuildResultJSON struct {
	Records int    `json:"records"`
	Chunks  int    `json:"chunks"`
	Model   string `json:"model"`
	URL     string `json:"url"`
}

const rebuildContinue = "run ralph index --rebuild --yes to continue"

func newIndexCommand(a *app) *cobra.Command {
	var status, asJSON, rebuild, yes bool
	cmd := &cobra.Command{
		Use:   "index",
		Short: "Embed the records for semantic search, or show the state of the index",
		Long: "Embed every brief, slice, task, document and note that is not yet in the vector index, and remove the entries of\n" +
			"records that are gone or changed. Running it again continues where it stopped. It needs embeddings.provider to be set.\n" +
			"With --status nothing is embedded: it shows the provider, the model and how much of the index is up to date.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if rebuild && status {
				return errors.New("--rebuild and --status cannot be used together")
			}
			if yes && !rebuild {
				return errors.New("--yes is for --rebuild")
			}
			if asJSON && !status && !rebuild {
				return errors.New("--json is for --status and --rebuild; a plain run prints progress lines")
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
			if rebuild {
				return runRebuild(ctx, cmd, a, d, provider, settings, yes, asJSON)
			}
			if !status {
				release, err := semantic.LockIndex(project.RalphDir(a.root))
				if err != nil {
					return err
				}
				defer release()
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
			finding, err := semantic.Verify(ctx, semantic.VerifyDeps{Index: idx, Describer: semantic.NewDescriber(provider, time.Now), Settings: settings, Now: time.Now})
			if err != nil {
				return err
			}
			if finding.Stale {
				return fmt.Errorf("the semantic index was built with a different embedding configuration (%s); run \"ralph index --rebuild\" to rebuild it", strings.Join(finding.Reasons, "; "))
			}
			return runIndex(ctx, cmd, deps)
		},
	}
	cmd.Flags().BoolVar(&status, "status", false, "embed nothing; show the provider, the model and what is indexed")
	cmd.Flags().BoolVar(&asJSON, "json", false, "with --status or --rebuild, print one JSON object")
	cmd.Flags().BoolVar(&rebuild, "rebuild", false, "build a new index with the current configuration; the old index stays in use until the new one is complete")
	cmd.Flags().BoolVar(&yes, "yes", false, "with --rebuild, start the rebuild; without it the plan is printed and nothing changes")
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

	finding, err := semantic.Inspect(ctx, deps.Index, deps.Settings)
	if err != nil {
		return err
	}
	st.State = "ok"
	switch {
	case finding.Stale:
		st.State, st.StaleReasons = "stale", finding.Reasons
	case finding.Records == 0:
		st.State = "empty"
	}
	if finding.HasRecorded {
		st.BuiltWith = &finding.Recorded
	}

	done, total, ok, err := semantic.RebuildProgress(ctx, deps.DB, project.RalphDir(a.root))
	if err != nil {
		return err
	}
	if ok {
		st.Rebuild = &rebuildStatus{Done: done, Total: total}
	}

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
	switch {
	case finding.Records == 0:
		_, _ = fmt.Fprintln(out, "built with: nothing indexed yet")
	case !finding.HasRecorded:
		_, _ = fmt.Fprintln(out, "built with: not recorded")
	default:
		b := finding.Recorded
		digest := "-"
		if b.Digest != "" {
			digest = b.Digest[:min(len(b.Digest), digestChars)]
		}
		_, _ = fmt.Fprintf(out, "built with: %s %s %d dimensions, chunk size %d\n", b.Model, digest, b.Dimensions, b.ChunkChars)
	}
	if st.State == "stale" {
		_, _ = fmt.Fprintf(out, "state: stale (%s)\n", strings.Join(st.StaleReasons, "; "))
	} else {
		_, _ = fmt.Fprintf(out, "state: %s\n", st.State)
	}
	if st.Rebuild != nil {
		_, _ = fmt.Fprintf(out, "rebuild: %d of %d records\n", st.Rebuild.Done, st.Rebuild.Total)
	}
	return nil
}

func runRebuild(ctx context.Context, cmd *cobra.Command, a *app, d *db.DB, provider embed.Provider, settings embed.Settings, yes, asJSON bool) error {
	out := cmd.OutOrStdout()
	deps := semantic.RebuildDeps{
		DB: d, Dir: project.RalphDir(a.root), Provider: provider, Settings: settings,
		Describer: semantic.NewDescriber(provider, time.Now), Now: time.Now,
	}
	plan, err := semantic.PlanRebuild(ctx, deps)
	if err != nil {
		return err
	}
	if !yes {
		return printRebuildPlan(out, plan, asJSON)
	}

	done := plan.Done
	deps.Report = func(line string) {
		if asJSON {
			return
		}
		if strings.HasPrefix(line, "embedding ") {
			done++
			line = fmt.Sprintf("[%d/%d] %s", done, plan.Records, line)
		}
		_, _ = fmt.Fprintln(out, line)
	}
	res, err := semantic.Rebuild(ctx, deps)
	switch {
	case ctx.Err() != nil && errors.Is(err, ctx.Err()):
		_, _ = fmt.Fprintln(out, "interrupted; "+rebuildContinue)
		return &exitStatusError{status: exitInterrupted, err: err}
	case errors.Is(err, semantic.ErrIndexBusy):
		return err
	case err != nil:
		return fmt.Errorf("%w\n%s", err, rebuildContinue)
	}
	if asJSON {
		return writeJSON(out, rebuildResultJSON{Records: res.Records, Chunks: res.Chunks, Model: settings.Model, URL: settings.URL})
	}
	_, _ = fmt.Fprintf(out, "rebuilt %d records (%d chunks); semantic search now uses %s\n", res.Records, res.Chunks, settings.Model)
	return nil
}

func printRebuildPlan(out io.Writer, plan semantic.RebuildPlan, asJSON bool) error {
	if asJSON {
		reasons := plan.Reasons
		if reasons == nil {
			reasons = []string{}
		}
		return writeJSON(out, rebuildPlanJSON{
			Reasons: reasons, Records: plan.Records, Chunks: plan.Chunks, Done: plan.Done,
			Resuming: plan.Resuming, Discarding: plan.Discarding, Model: plan.Model, URL: plan.URL,
		})
	}
	_, _ = fmt.Fprintln(out, "rebuild plan")
	if len(plan.Reasons) > 0 {
		_, _ = fmt.Fprintf(out, "reason: %s\n", strings.Join(plan.Reasons, "; "))
	} else {
		_, _ = fmt.Fprintln(out, "reason: no change in configuration; a rebuild was asked for")
	}
	_, _ = fmt.Fprintf(out, "records: %d (about %d chunks)\n", plan.Records, plan.Chunks)
	_, _ = fmt.Fprintf(out, "model: %s at %s\n", plan.Model, plan.URL)
	if plan.Resuming {
		_, _ = fmt.Fprintf(out, "done already: %d records, will continue\n", plan.Done)
	}
	if plan.Discarding {
		_, _ = fmt.Fprintln(out, "starting again: the earlier rebuild used a different configuration")
	}
	_, _ = fmt.Fprintln(out, "the current index stays in use until the new one is complete")
	_, _ = fmt.Fprintln(out, "run again with --yes to start")
	return nil
}
