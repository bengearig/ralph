package cli

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os/signal"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/bengearig/ralph/internal/db"
	"github.com/bengearig/ralph/internal/embed"
	"github.com/bengearig/ralph/internal/project"
	"github.com/bengearig/ralph/internal/search"
	"github.com/bengearig/ralph/internal/semantic"
	"github.com/bengearig/ralph/internal/vecindex"
	"github.com/spf13/cobra"
)

var searchKinds = []search.Kind{search.KindBrief, search.KindSlice, search.KindTask, search.KindNote, search.KindDocument}

const maxSearchTimeout = 5 * time.Second

// newSearchService returns the keyword search, or the keyword and semantic search combined when a provider
// is configured; the index file is created if it is missing. The returned function closes the index file.
func newSearchService(a *app, d *db.DB) (search.Service, func(), error) {
	keyword := search.New(d)
	noop := func() {}
	provider, settings, err := embed.FromConfig(a.cfg)
	if err != nil {
		return nil, noop, err
	}
	if provider == nil {
		return keyword, noop, nil
	}
	path := project.IndexPath(a.root)
	idx, err := vecindex.Open(path)
	if err != nil {
		return nil, noop, err
	}
	searcher := semantic.NewSearcher(semantic.SearcherDeps{DB: d, Index: idx, Provider: provider, Settings: settings, Describer: semantic.NewDescriber(provider, time.Now), Dir: project.RalphDir(a.root)})
	return search.NewHybrid(keyword, searcher, search.HybridOptions{Timeout: min(settings.Timeout, maxSearchTimeout)}), func() { _ = idx.Close() }, nil
}

func printSearchResult(out io.Writer, res search.Result, asJSON bool) error {
	if asJSON {
		if res.Hits == nil {
			res.Hits = []search.Hit{}
		}
		return writeJSON(out, res)
	}
	if res.Notice != "" {
		_, _ = fmt.Fprintf(out, "note: %s\n", res.Notice)
	}
	if len(res.Hits) == 0 {
		_, _ = fmt.Fprintln(out, "no results")
		return nil
	}
	if res.Relaxed {
		_, _ = fmt.Fprintln(out, "no result had every word; these match some of them:")
	}
	for _, h := range res.Hits {
		status := h.Status
		if status == "" {
			status = "-"
		}
		line := fmt.Sprintf("%s  %s  %s  %s", h.ID, h.Kind, status, h.Title)
		if h.Inactive {
			line += " (inactive)"
		}
		_, _ = fmt.Fprintln(out, line)
		if h.Snippet != "" {
			_, _ = fmt.Fprintf(out, "    %s\n", strings.ReplaceAll(h.Snippet, "\n", " "))
		}
	}
	return nil
}

func newSearchCommand(a *app) *cobra.Command {
	var (
		kinds  []string
		limit  int
		all    bool
		asJSON bool
	)
	cmd := &cobra.Command{
		Use:   "search <words...>",
		Short: "Find documents, briefs, slices, tasks and notes by their words",
		Long: "Find documents, briefs, slices, tasks and notes by their words. The words are plain text, not search syntax;\n" +
			"double quotes keep a phrase together. Every word must match unless no record has them all.\n" +
			"Archived and superseded documents and cancelled tasks are left out unless --all is given.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			q := search.Query{Text: strings.Join(args, " "), IncludeInactive: all}
			allowed := make([]string, len(searchKinds))
			for i, k := range searchKinds {
				allowed[i] = string(k)
			}
			for _, k := range kinds {
				if !slices.Contains(allowed, k) {
					return fmt.Errorf("--kind %q: use one of %s", k, strings.Join(allowed, ", "))
				}
				q.Kinds = append(q.Kinds, search.Kind(k))
			}
			if cmd.Flags().Changed("limit") {
				if limit < 1 || limit > 100 {
					return fmt.Errorf("--limit %d: give a whole number from 1 to 100", limit)
				}
				q.Limit = limit
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

			svc, closeIndex, err := newSearchService(a, d)
			if err != nil {
				return err
			}
			defer closeIndex()

			res, err := svc.Search(ctx, q)
			if err != nil {
				return err
			}
			return printSearchResult(cmd.OutOrStdout(), res, asJSON)
		},
	}
	cmd.Flags().StringArrayVar(&kinds, "kind", nil, "only this kind: brief, slice, task, note or document; repeat for several")
	cmd.Flags().IntVar(&limit, "limit", 0, "most results to show, 1 to 100; default 20")
	cmd.Flags().BoolVar(&all, "all", false, "include archived and superseded documents and cancelled tasks")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the result as JSON")
	return cmd
}
