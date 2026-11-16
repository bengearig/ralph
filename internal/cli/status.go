package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"regexp"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/bengearig/ralph/internal/db"
	"github.com/bengearig/ralph/internal/units"
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
			return statusSummary(ctx, cmd.OutOrStdout(), svc, all, asJSON)
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "include finished (DONE or CANCELLED) work")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	return cmd
}

func finished(s units.Status) bool {
	return s == units.StatusDone || s == units.StatusCancelled
}

type itemJSON struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"`
}

type sliceJSON struct {
	itemJSON
	Tasks map[string]int `json:"tasks"`
}

type briefJSON struct {
	itemJSON
	Slices []sliceJSON `json:"slices"`
}

type summaryJSON struct {
	Briefs []briefJSON `json:"briefs"`
	Tasks  []itemJSON  `json:"tasks"`
}

type sliceView struct {
	item   itemJSON
	counts map[units.State]int
}

type briefView struct {
	item   itemJSON
	slices []sliceView
}

func statusSummary(ctx context.Context, out io.Writer, svc units.Service, all, asJSON bool) error {
	briefs, err := svc.List(ctx, units.Filter{Level: units.LevelBrief})
	if err != nil {
		return err
	}
	slices, err := svc.List(ctx, units.Filter{Level: units.LevelSlice})
	if err != nil {
		return err
	}
	tasks, err := svc.List(ctx, units.Filter{Level: units.LevelTask})
	if err != nil {
		return err
	}

	ids := make([]string, 0, len(briefs)+len(slices))
	for _, u := range briefs {
		ids = append(ids, u.ID)
	}
	for _, u := range slices {
		ids = append(ids, u.ID)
	}
	statuses, err := svc.Statuses(ctx, ids)
	if err != nil {
		return err
	}

	counts := map[string]map[units.State]int{}
	var orphans []units.Unit
	for _, t := range tasks {
		if t.ParentID == "" {
			orphans = append(orphans, t)
			continue
		}
		if counts[t.ParentID] == nil {
			counts[t.ParentID] = map[units.State]int{}
		}
		counts[t.ParentID][t.State]++
	}
	slicesOf := map[string][]units.Unit{}
	for _, s := range slices {
		slicesOf[s.ParentID] = append(slicesOf[s.ParentID], s)
	}

	var (
		shown   []briefView
		shownOr []itemJSON
		hidden  int
	)
	for _, b := range briefs {
		if !all && finished(statuses[b.ID]) {
			hidden++
			continue
		}
		bv := briefView{item: itemJSON{b.ID, b.Title, string(statuses[b.ID])}}
		for _, s := range slicesOf[b.ID] {
			if !all && finished(statuses[s.ID]) {
				hidden++
				continue
			}
			bv.slices = append(bv.slices, sliceView{
				item:   itemJSON{s.ID, s.Title, string(statuses[s.ID])},
				counts: counts[s.ID],
			})
		}
		shown = append(shown, bv)
	}
	for _, t := range orphans {
		if !all && finished(units.Status(t.State)) {
			hidden++
			continue
		}
		shownOr = append(shownOr, itemJSON{t.ID, t.Title, string(t.State)})
	}

	if asJSON {
		doc := summaryJSON{Briefs: []briefJSON{}, Tasks: []itemJSON{}}
		for _, b := range shown {
			bj := briefJSON{itemJSON: b.item, Slices: []sliceJSON{}}
			for _, s := range b.slices {
				tc := map[string]int{}
				for st, n := range s.counts {
					tc[string(st)] = n
				}
				bj.Slices = append(bj.Slices, sliceJSON{itemJSON: s.item, Tasks: tc})
			}
			doc.Briefs = append(doc.Briefs, bj)
		}
		doc.Tasks = append(doc.Tasks, shownOr...)
		return writeJSON(out, doc)
	}

	if len(shown) == 0 && len(shownOr) == 0 {
		if hidden == 0 {
			_, err = fmt.Fprintln(out, "No work recorded yet.")
			return err
		}
	}
	tw := newTabWriter(out)
	for _, b := range shown {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\n", b.item.ID, b.item.Status, b.item.Title)
		for _, s := range b.slices {
			_, _ = fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\n", s.item.ID, s.item.Status, s.item.Title, countsText(s.counts))
		}
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	if len(shownOr) > 0 {
		if len(shown) > 0 {
			_, _ = fmt.Fprintln(out)
		}
		_, _ = fmt.Fprintln(out, "Tasks with no slice")
		tw = newTabWriter(out)
		for _, t := range shownOr {
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\n", t.ID, t.Status, t.Title)
		}
		if err := tw.Flush(); err != nil {
			return err
		}
	}
	if hidden > 0 {
		_, err = fmt.Fprintf(out, "%d finished item(s) hidden; use --all\n", hidden)
		return err
	}
	return nil
}

func newTabWriter(w io.Writer) *tabwriter.Writer {
	return tabwriter.NewWriter(w, 0, 8, 2, ' ', 0)
}

func countsText(c map[units.State]int) string {
	var parts []string
	for _, st := range taskStateOrder {
		if n := c[st]; n > 0 {
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

type parentJSON struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

type linkJSON struct {
	Kind   string `json:"kind"`
	Target string `json:"target"`
}

type noteJSON struct {
	Kind      string `json:"kind"`
	Author    string `json:"author,omitempty"`
	Body      string `json:"body"`
	CreatedAt string `json:"created_at"`
}

type unitJSON struct {
	ID                 string      `json:"id"`
	Level              string      `json:"level"`
	Title              string      `json:"title"`
	Status             string      `json:"status"`
	Parent             *parentJSON `json:"parent"`
	Body               string      `json:"body"`
	AcceptanceCriteria string      `json:"acceptance_criteria,omitempty"`
	CreatedAt          string      `json:"created_at"`
	CreatedBy          string      `json:"created_by,omitempty"`
	UpdatedAt          string      `json:"updated_at"`
	UpdatedBy          string      `json:"updated_by,omitempty"`
	Children           []itemJSON  `json:"children"`
	Links              []linkJSON  `json:"links"`
	Notes              []noteJSON  `json:"notes"`
}

func stamp(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func statusUnit(ctx context.Context, out io.Writer, svc units.Service, arg string, asJSON bool) error {
	id := strings.ToUpper(arg)
	if !unitIDPattern.MatchString(id) {
		return fmt.Errorf("%q is not a unit ID: expected B-, S- or T- followed by digits", arg)
	}
	u, err := svc.Get(ctx, id)
	if errors.Is(err, units.ErrNotFound) {
		return fmt.Errorf("unit %s was not found", id)
	}
	if err != nil {
		return err
	}
	status, err := svc.Status(ctx, id)
	if err != nil {
		return err
	}

	v := unitJSON{
		ID: u.ID, Level: string(u.Level), Title: u.Title, Status: string(status),
		Body: u.Body, AcceptanceCriteria: u.AcceptanceCriteria,
		CreatedAt: stamp(u.CreatedAt), CreatedBy: u.CreatedBy,
		UpdatedAt: stamp(u.UpdatedAt), UpdatedBy: u.UpdatedBy,
		Children: []itemJSON{}, Links: []linkJSON{}, Notes: []noteJSON{},
	}
	if u.ParentID != "" {
		p, err := svc.Get(ctx, u.ParentID)
		if err != nil {
			return err
		}
		v.Parent = &parentJSON{ID: p.ID, Title: p.Title}
	}

	if u.Level != units.LevelTask {
		parent := u.ID
		kids, err := svc.List(ctx, units.Filter{ParentID: &parent})
		if err != nil {
			return err
		}
		var slice []string
		for _, k := range kids {
			if k.Level != units.LevelTask {
				slice = append(slice, k.ID)
			}
		}
		derived, err := svc.Statuses(ctx, slice)
		if err != nil {
			return err
		}
		for _, k := range kids {
			st := units.Status(k.State)
			if k.Level != units.LevelTask {
				st = derived[k.ID]
			}
			v.Children = append(v.Children, itemJSON{k.ID, k.Title, string(st)})
		}
	}

	links, err := svc.Links(ctx, id)
	if err != nil {
		return err
	}
	for _, l := range links {
		v.Links = append(v.Links, linkJSON{string(l.Kind), l.Target})
	}
	notes, err := svc.Notes(ctx, id)
	if err != nil {
		return err
	}
	for _, n := range notes {
		v.Notes = append(v.Notes, noteJSON{string(n.Kind), n.Author, n.Body, stamp(n.CreatedAt)})
	}

	if asJSON {
		return writeJSON(out, v)
	}
	return printUnit(out, u, v)
}

func printUnit(out io.Writer, u units.Unit, v unitJSON) error {
	w := func(format string, args ...any) { _, _ = fmt.Fprintf(out, format, args...) }
	w("%s  %s  %s\n", v.ID, v.Status, v.Title)
	switch {
	case v.Parent != nil:
		w("parent: %s %s\n", v.Parent.ID, v.Parent.Title)
	case u.Level == units.LevelTask:
		w("parent: no slice\n")
	default:
		w("parent: none\n")
	}
	w("created: %s%s\n", v.CreatedAt, byActor(v.CreatedBy))
	w("updated: %s%s\n", v.UpdatedAt, byActor(v.UpdatedBy))

	if u.Body != "" {
		w("\n%s", u.Body)
		if !strings.HasSuffix(u.Body, "\n") {
			w("\n")
		}
	}
	if u.Level == units.LevelTask && u.AcceptanceCriteria != "" {
		w("\nAcceptance criteria:\n%s", u.AcceptanceCriteria)
		if !strings.HasSuffix(u.AcceptanceCriteria, "\n") {
			w("\n")
		}
	}

	if len(v.Children) > 0 {
		heading := "Slices:"
		if u.Level == units.LevelSlice {
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
