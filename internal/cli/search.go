package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os/signal"
	"slices"
	"strings"
	"syscall"

	"github.com/bengearig/ralph/internal/db"
	"github.com/bengearig/ralph/internal/search"
	"github.com/spf13/cobra"
)

var searchKinds = []search.Kind{search.KindBrief, search.KindSlice, search.KindTask, search.KindNote, search.KindDocument}

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

			res, err := search.New(d).Search(ctx, q)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if asJSON {
				if res.Hits == nil {
					res.Hits = []search.Hit{}
				}
				return writeJSON(out, res)
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
		},
	}
	cmd.Flags().StringArrayVar(&kinds, "kind", nil, "only this kind: brief, slice, task, note or document; repeat for several")
	cmd.Flags().IntVar(&limit, "limit", 0, "most results to show, 1 to 100; default 20")
	cmd.Flags().BoolVar(&all, "all", false, "include archived and superseded documents and cancelled tasks")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the result as JSON")
	return cmd
}
