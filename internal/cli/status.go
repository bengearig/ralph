package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"regexp"
	"strings"
	"text/tabwriter"

	"github.com/bengearig/ralph/internal/db"
	"github.com/bengearig/ralph/internal/harness/all"
	"github.com/bengearig/ralph/internal/integration"
	"github.com/bengearig/ralph/internal/units"
	"github.com/bengearig/ralph/internal/view"
	"github.com/spf13/cobra"
)

var unitIDPattern = regexp.MustCompile(`^[BST]-[0-9]+$`)

var taskStateOrder = []units.State{
	units.StateProposed, units.StateTodo, units.StateInProgress,
	units.StateBlocked, units.StateDone, units.StateCancelled,
}

const noDatabaseHint = `No work recorded yet: this project has no ralph database. Run "ralph init" to set it up.`

func newStatusCommand(a *app) *cobra.Command {
	var all, asJSON bool
	cmd := &cobra.Command{
		Use:   "status [id]",
		Short: "Show briefs, slices and tasks with their statuses",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			d, err := openStore(ctx, a, db.Options{MustExist: true})
			if errors.Is(err, fs.ErrNotExist) {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), noDatabaseHint)
				return nil
			}
			if err != nil {
				return err
			}
			defer func() { _ = d.Close() }()
			svc := units.NewService(d)
			if len(args) == 1 {
				return statusUnit(ctx, cmd.OutOrStdout(), svc, args[0], asJSON)
			}
			return statusSummary(ctx, cmd.OutOrStdout(), svc, all, asJSON, outdatedPaths(ctx, a.root))
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "include finished (DONE or CANCELLED) work")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	return cmd
}

const integrationOutdatedLine = `ralph's integration files are out of date: run "ralph init" to update them.`

// outdatedPaths lists the integration files that are out of date. A failed
// check is logged and reported as none, so it never fails the command.
func outdatedPaths(ctx context.Context, root string) []string {
	stale, err := integration.Check(ctx, root, all.Adapters())
	if err != nil {
		slog.Warn("cannot check whether the integration files are out of date", "error", err)
		return nil
	}
	var paths []string
	for _, o := range stale {
		paths = append(paths, o.Path)
	}
	return paths
}

type statusSummaryJSON struct {
	view.SummaryView
	IntegrationOutdated []string `json:"integration_outdated,omitempty"`
}

func statusSummary(ctx context.Context, out io.Writer, svc units.Service, all, asJSON bool, outdated []string) error {
	doc, err := view.Summary(ctx, svc, all)
	if err != nil {
		return err
	}
	if asJSON {
		return writeJSON(out, statusSummaryJSON{SummaryView: doc, IntegrationOutdated: outdated})
	}
	if err := printSummary(out, doc); err != nil {
		return err
	}
	if len(outdated) > 0 {
		_, err = fmt.Fprintf(out, "\n%s\n", integrationOutdatedLine)
	}
	return err
}

func printSummary(out io.Writer, doc view.SummaryView) error {
	var err error
	if len(doc.Briefs) == 0 && len(doc.Tasks) == 0 && doc.Hidden == 0 {
		_, err = fmt.Fprintln(out, "No work recorded yet.")
		return err
	}
	tw := newTabWriter(out)
	for _, b := range doc.Briefs {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\n", b.ID, b.Status, b.Title)
		for _, s := range b.Slices {
			_, _ = fmt.Fprintf(tw, "  %s\t%s\t%s\t%s%s\n", s.ID, s.Status, s.Title, countsText(s.Tasks), waitingSuffix(s.WaitingOn))
		}
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	if len(doc.Tasks) > 0 {
		if len(doc.Briefs) > 0 {
			_, _ = fmt.Fprintln(out)
		}
		_, _ = fmt.Fprintln(out, "Tasks with no slice")
		tw = newTabWriter(out)
		for _, t := range doc.Tasks {
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\n", t.ID, t.Status, t.Title)
		}
		if err := tw.Flush(); err != nil {
			return err
		}
	}
	if len(doc.Unfinalized) > 0 {
		_, _ = fmt.Fprintln(out, "\nUnfinalized quick work")
		for _, u := range doc.Unfinalized {
			_, _ = fmt.Fprintf(out, "%s  %s  branch %s\n", u.ID, u.Title, u.Branch)
		}
		_, _ = fmt.Fprintln(out, `Finish it with "ralph quick finish <ID>", or cancel the task if the work was dropped.`)
	}
	if doc.Hidden > 0 {
		_, err = fmt.Fprintf(out, "%d finished item(s) hidden; use --all\n", doc.Hidden)
		return err
	}
	return nil
}

func newTabWriter(w io.Writer) *tabwriter.Writer {
	return tabwriter.NewWriter(w, 0, 8, 2, ' ', 0)
}

func waitingSuffix(ids []string) string {
	if len(ids) == 0 {
		return ""
	}
	return "  waiting on " + strings.Join(ids, ", ")
}

func countsText(c map[string]int) string {
	var parts []string
	for _, st := range taskStateOrder {
		if n := c[string(st)]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, st))
		}
	}
	if len(parts) == 0 {
		return "no tasks"
	}
	return strings.Join(parts, ", ")
}

func writeJSON(out io.Writer, v any) error {
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func statusUnit(ctx context.Context, out io.Writer, svc units.Service, arg string, asJSON bool) error {
	id := strings.ToUpper(arg)
	if !unitIDPattern.MatchString(id) {
		return fmt.Errorf("%q is not a unit ID: expected B-, S- or T- followed by digits", arg)
	}
	v, err := view.UnitDetail(ctx, svc, id)
	if err != nil {
		return err
	}
	if asJSON {
		return writeJSON(out, v)
	}
	return printUnit(out, v)
}

func printUnit(out io.Writer, v view.UnitView) error {
	w := func(format string, args ...any) { _, _ = fmt.Fprintf(out, format, args...) }
	w("%s  %s  %s\n", v.ID, v.Status, v.Title)
	switch {
	case v.Parent != nil:
		w("parent: %s %s\n", v.Parent.ID, v.Parent.Title)
	case v.Level == string(units.LevelTask):
		w("parent: no slice\n")
	default:
		w("parent: none\n")
	}
	w("created: %s%s\n", v.CreatedAt, byActor(v.CreatedBy))
	w("updated: %s%s\n", v.UpdatedAt, byActor(v.UpdatedBy))

	if v.Body != "" {
		w("\n%s", v.Body)
		if !strings.HasSuffix(v.Body, "\n") {
			w("\n")
		}
	}
	if v.Level == string(units.LevelTask) && v.AcceptanceCriteria != "" {
		w("\nAcceptance criteria:\n%s", v.AcceptanceCriteria)
		if !strings.HasSuffix(v.AcceptanceCriteria, "\n") {
			w("\n")
		}
	}

	if len(v.Children) > 0 {
		heading := "Slices:"
		if v.Level == string(units.LevelSlice) {
			heading = "Tasks:"
		}
		w("\n%s\n", heading)
		tw := newTabWriter(out)
		for _, c := range v.Children {
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\n", c.ID, c.Status, c.Title)
		}
		if err := tw.Flush(); err != nil {
			return err
		}
	}
	if len(v.DependsOn) > 0 {
		w("\nDepends on:\n")
		for _, d := range v.DependsOn {
			waiting := ""
			if !d.Satisfied {
				waiting = " (waiting)"
			}
			w("%s  %s  %s%s\n", d.ID, d.Status, d.Title, waiting)
		}
	}
	if len(v.Links) > 0 {
		w("\nLinks:\n")
		for _, l := range v.Links {
			w("%s %s\n", l.Kind, l.Target)
		}
	}
	if len(v.Notes) > 0 {
		w("\nNotes:\n")
		for _, n := range v.Notes {
			w("%s%s\n  %s\n", n.CreatedAt, byActor(n.Author), strings.ReplaceAll(strings.TrimRight(n.Body, "\n"), "\n", "\n  "))
		}
	}
	return nil
}

func byActor(actor string) string {
	if actor == "" {
		return ""
	}
	return " by " + actor
}
