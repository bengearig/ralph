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
	"unicode/utf8"

	"github.com/bengearig/ralph/internal/db"
	"github.com/bengearig/ralph/internal/project"
	"github.com/bengearig/ralph/internal/taskmanager"
	"github.com/bengearig/ralph/internal/tickets"
	"github.com/bengearig/ralph/internal/units"
	"github.com/spf13/cobra"
)

func newTicketsCommand(a *app) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "tickets",
		Short: "List the tickets of units, or create them in the task manager",
		Long: "List the tickets the units have in the task manager, from what ralph recorded locally.\n" +
			"Use `ralph tickets sync` to create the tickets of the units that have none.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			d, _, settings, err := openTicketsEnv(ctx, a)
			if err != nil {
				return err
			}
			defer func() { _ = d.Close() }()
			return listTickets(ctx, cmd.OutOrStdout(), d, settings, asJSON)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	cmd.AddCommand(newTicketsSyncCommand(a))
	cmd.AddCommand(newTicketsCheckCommand(a))
	cmd.AddCommand(newTicketsResolveCommand(a, tickets.ActionAccept))
	cmd.AddCommand(newTicketsResolveCommand(a, tickets.ActionOverwrite))
	return cmd
}

func newTicketsSyncCommand(a *app) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Create the tickets of the units that have none and push what changed",
		Long: "Create a ticket in the task manager for every brief, slice and task whose level has a ticket type and that has none yet,\n" +
			"and put the new tickets in the configured sprint. For the tickets that exist, push a changed title or body and move\n" +
			"the ticket to the status the unit's state maps to. A ticket with a difference in Jira is held.\n" +
			"The command exits 1 when anything failed or the pass stopped early.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()
			ctx = a.actorContext(ctx, cmd)
			d, system, settings, err := openTicketsEnv(ctx, a)
			if err != nil {
				return err
			}
			defer func() { _ = d.Close() }()

			out := cmd.OutOrStdout()
			deps := tickets.Deps{
				Units: units.NewService(d), DB: d, System: system, Settings: settings,
				ProjectID: a.setting("project.id"), Dir: project.RalphDir(a.root), Now: time.Now,
			}
			if !asJSON {
				deps.Report = func(ev tickets.Event) { _, _ = fmt.Fprintln(out, eventLine(ev)) }
			}
			report, syncErr := tickets.Sync(ctx, deps, tickets.SyncOptions{})
			if asJSON {
				if err := writeJSON(out, syncJSON(report)); err != nil {
					return err
				}
			} else {
				_, _ = fmt.Fprintf(out, "tickets: %d created, %d adopted, %d updated, %d moved, %d scheduled, %d held, %d skipped, %d failed\n",
					len(report.Created), len(report.Adopted), len(report.Updated), len(report.Moved), len(report.Scheduled), len(report.Held),
					len(report.Skipped), len(report.Failed))
			}
			if syncErr != nil {
				return syncErr
			}
			if len(report.Failed) > 0 {
				return &reportedError{errors.New("some tickets failed")}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	return cmd
}

func newTicketsCheckCommand(a *app) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "check [<unit>...]",
		Short: "Look for tickets changed in the task manager and record the differences",
		Long: "Read the tickets of the units (all that have one, when none are named) and record where they differ from what ralph\n" +
			"last pushed: the title, the body, the status, or a ticket that no longer exists. A ticket that moved to a new key is followed.\n" +
			"Nothing is changed in ralph's units or in the task manager. The command exits 1 only when the task manager cannot be\n" +
			"reached or refuses.",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()
			ctx = a.actorContext(ctx, cmd)
			d, system, settings, err := openTicketsEnv(ctx, a)
			if err != nil {
				return err
			}
			defer func() { _ = d.Close() }()
			deps := tickets.Deps{
				Units: units.NewService(d), DB: d, System: system, Settings: settings,
				ProjectID: a.setting("project.id"), Dir: project.RalphDir(a.root), Now: time.Now,
			}
			res, err := tickets.Check(ctx, deps, tickets.ObserveOptions{Units: args})
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if asJSON {
				return writeJSON(out, checkJSON(res))
			}
			return writeCheck(out, res)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	return cmd
}

type driftJSON struct {
	Unit    string   `json:"unit"`
	Key     string   `json:"key"`
	Fields  []string `json:"fields"`
	MovedTo string   `json:"moved_to"`
}

type checkReportJSON struct {
	Drifts   []driftJSON `json:"drifts"`
	Found    int         `json:"found"`
	Cleared  int         `json:"cleared"`
	Followed int         `json:"followed"`
}

func checkJSON(res tickets.CheckResult) checkReportJSON {
	out := checkReportJSON{Drifts: make([]driftJSON, 0, len(res.Drifts)), Found: res.Result.Found, Cleared: res.Result.Cleared, Followed: res.Result.Followed}
	for _, dr := range res.Drifts {
		fields := dr.Fields
		if fields == nil {
			fields = []string{}
		}
		out.Drifts = append(out.Drifts, driftJSON{Unit: dr.Unit, Key: dr.Key, Fields: fields, MovedTo: dr.MovedTo})
	}
	return out
}

func writeCheck(out io.Writer, res tickets.CheckResult) error {
	differing := 0
	for _, dr := range res.Drifts {
		if len(dr.Fields) == 0 {
			continue
		}
		differing++
		if _, err := fmt.Fprintf(out, "%s %s: %s\n", dr.Unit, dr.Key, driftText(dr.Fields)); err != nil {
			return err
		}
	}
	for _, dr := range res.Drifts {
		if dr.MovedTo == "" {
			continue
		}
		if _, err := fmt.Fprintf(out, "followed %s %s -> %s (moved in Jira)\n", dr.Unit, dr.Key, dr.MovedTo); err != nil {
			return err
		}
	}
	if differing == 0 {
		_, err := fmt.Fprintln(out, "tickets: no differences")
		return err
	}
	_, err := fmt.Fprintf(out, "tickets: %d with differences\n", differing)
	return err
}

func driftText(fields []string) string {
	if len(fields) == 1 && fields[0] == "deleted" {
		return "ticket no longer exists in Jira"
	}
	return joinAnd(fields) + " differ in Jira"
}

func joinAnd(items []string) string {
	switch len(items) {
	case 1:
		return items[0]
	case 2:
		return items[0] + " and " + items[1]
	}
	return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
}

// openTicketsEnv opens the database and the task manager; the database comes first so a project that is not
// initialized is reported before a missing setting.
func openTicketsEnv(ctx context.Context, a *app) (*db.DB, taskmanager.System, taskmanager.Settings, error) {
	d, err := openStore(ctx, a, db.Options{MustExist: true})
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, taskmanager.Settings{}, errors.New(errNoDatabase)
	}
	if err != nil {
		return nil, nil, taskmanager.Settings{}, err
	}
	system, settings, err := openTaskManager(ctx, a)
	if err != nil {
		_ = d.Close()
		return nil, nil, taskmanager.Settings{}, err
	}
	return d, system, settings, nil
}

func eventLine(ev tickets.Event) string {
	switch ev.Kind {
	case "created":
		return fmt.Sprintf("created %s %s", ev.Unit, ev.Key)
	case "adopted":
		return fmt.Sprintf("adopted %s %s (%s)", ev.Unit, ev.Key, ev.Message)
	case "updated":
		return fmt.Sprintf("updated %s %s (%s)", ev.Unit, ev.Key, ev.Message)
	case "moved":
		line := fmt.Sprintf("moved %s %s %s", ev.Unit, ev.Key, ev.Message)
		if ev.Detail != "" {
			line += fmt.Sprintf(" (via %s)", ev.Detail)
		}
		return line
	case "held":
		return fmt.Sprintf("held %s %s: differs in Jira (%s); run ralph tickets", ev.Unit, ev.Key, ev.Message)
	case "scheduled":
		return fmt.Sprintf("scheduled %s %s in sprint %q", ev.Unit, ev.Key, ev.Message)
	case "skipped":
		return fmt.Sprintf("skipped %s: %s", ev.Unit, ev.Message)
	default:
		return fmt.Sprintf("%s %s %s", ev.Kind, ev.Unit, ev.Message)
	}
}

type itemJSON struct {
	Unit   string `json:"unit"`
	Key    string `json:"key"`
	Reason string `json:"reason"`
}

type failureJSON struct {
	Unit    string `json:"unit"`
	Step    string `json:"step"`
	Message string `json:"message"`
}

type syncReportJSON struct {
	Created   []itemJSON    `json:"created"`
	Adopted   []itemJSON    `json:"adopted"`
	Updated   []itemJSON    `json:"updated"`
	Moved     []itemJSON    `json:"moved"`
	Scheduled []itemJSON    `json:"scheduled"`
	Held      []itemJSON    `json:"held"`
	Deferred  []itemJSON    `json:"deferred"`
	Skipped   []itemJSON    `json:"skipped"`
	Failed    []failureJSON `json:"failed"`
}

func syncJSON(r tickets.Report) syncReportJSON {
	items := func(in []tickets.Item) []itemJSON {
		out := make([]itemJSON, 0, len(in))
		for _, it := range in {
			out = append(out, itemJSON(it))
		}
		return out
	}
	out := syncReportJSON{
		Created: items(r.Created), Adopted: items(r.Adopted), Updated: items(r.Updated), Moved: items(r.Moved), Scheduled: items(r.Scheduled),
		Held: items(r.Held), Deferred: items(r.Deferred), Skipped: items(r.Skipped), Failed: make([]failureJSON, 0, len(r.Failed)),
	}
	for _, f := range r.Failed {
		out.Failed = append(out.Failed, failureJSON(f))
	}
	return out
}

type ticketJSON struct {
	Unit         string   `json:"unit"`
	System       string   `json:"system"`
	Key          string   `json:"key"`
	TicketID     string   `json:"ticket_id"`
	Type         string   `json:"type"`
	Title        string   `json:"title"`
	PushedStatus string   `json:"pushed_status"`
	PushedParent string   `json:"pushed_parent"`
	Scheduled    bool     `json:"scheduled"`
	Detached     bool     `json:"detached"`
	Drift        []string `json:"drift"`
	FailedStep   string   `json:"failed_step"`
	FailedAt     string   `json:"failed_at"`
	LastError    string   `json:"last_error"`
	Attempts     int      `json:"attempts"`
	SyncedAt     string   `json:"synced_at"`
}

func listTickets(ctx context.Context, out io.Writer, d *db.DB, settings taskmanager.Settings, asJSON bool) error {
	svc := units.NewService(d)
	rows, err := tickets.NewStore(d).List(ctx)
	if err != nil {
		return err
	}
	list := make([]ticketJSON, 0, len(rows))
	for _, r := range rows {
		u, err := svc.Get(ctx, r.UnitID)
		if err != nil {
			return err
		}
		links, err := svc.Links(ctx, r.UnitID)
		if err != nil {
			return err
		}
		list = append(list, ticketJSON{
			Unit: r.UnitID, System: r.System, Key: r.Key, TicketID: r.ID, Type: settings.Types[string(u.Level)], Title: u.Title,
			PushedStatus: r.PushedStatus, PushedParent: r.PushedParent, Scheduled: r.Scheduled,
			Detached: r.LinkConfirmed && tickets.LinkKey(links) == "",
			Drift:    driftFields(r.Drift), FailedStep: r.FailedStep, FailedAt: timeText(r.FailedAt), LastError: r.LastError,
			Attempts: r.Attempts, SyncedAt: timeText(r.SyncedAt),
		})
	}
	if asJSON {
		return writeJSON(out, list)
	}
	if len(list) == 0 {
		_, err = fmt.Fprintln(out, "no tickets yet")
		return err
	}
	for _, t := range list {
		status, typ := t.PushedStatus, t.Type
		if status == "" {
			status = "-"
		}
		if typ == "" {
			typ = "-"
		}
		line := fmt.Sprintf("%s  %s  %s  %s  %s", t.Unit, t.Key, status, typ, t.Title)
		if len(t.Drift) > 0 {
			line += fmt.Sprintf("  (differs in Jira: %s)", strings.Join(t.Drift, ", "))
		}
		if t.FailedStep != "" {
			line += fmt.Sprintf("  (failed %s: %s)", t.FailedStep, t.LastError)
		}
		if t.Detached {
			line += "  (detached)"
		}
		if _, err := fmt.Fprintln(out, line); err != nil {
			return err
		}
	}
	return nil
}

func driftFields(drift string) []string {
	if drift == "" {
		return []string{}
	}
	return strings.Split(drift, ",")
}

func timeText(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func newTicketsResolveCommand(a *app, action string) *cobra.Command {
	var (
		fields []string
		yes    bool
	)
	short, long := "Import what was changed in Jira into the unit", "Set the unit's title and body from the ticket, acknowledge a different status, or drop the link of a ticket that was deleted.\n"
	if action == tickets.ActionOverwrite {
		short, long = "Push the unit's values over what was changed in Jira", "Clear the difference and push the unit's title, body and status to the ticket now; a deleted ticket is created again with a new key.\n"
	}
	cmd := &cobra.Command{
		Use:   action + " <unit>",
		Short: short,
		Long: long + "Without --yes the command prints what would change and changes nothing. Use --field (title, body, status or deleted,\n" +
			"repeatable) to resolve only some of the differences.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()
			ctx = a.actorContext(ctx, cmd)
			d, system, settings, err := openTicketsEnv(ctx, a)
			if err != nil {
				return err
			}
			defer func() { _ = d.Close() }()
			var events []tickets.Event
			deps := tickets.Deps{
				Units: units.NewService(d), DB: d, System: system, Settings: settings,
				ProjectID: a.setting("project.id"), Dir: project.RalphDir(a.root), Now: time.Now,
				Report: func(ev tickets.Event) { events = append(events, ev) },
			}
			plan, err := tickets.PlanResolve(ctx, deps, args[0], action, fields)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if !yes {
				return writeResolvePlan(out, plan)
			}
			err = tickets.Resolve(ctx, deps, plan)
			if err == nil {
				if action == tickets.ActionAccept {
					_, _ = fmt.Fprintf(out, "accepted %s %s from %s\n", plan.Unit, strings.Join(plan.Fields(), ", "), plan.Key)
				} else {
					_, _ = fmt.Fprintf(out, "overwrote %s %s from %s\n", plan.Key, strings.Join(plan.Fields(), ", "), plan.Unit)
				}
			}
			for _, ev := range events {
				_, _ = fmt.Fprintln(out, eventLine(ev))
			}
			return err
		},
	}
	cmd.Flags().StringArrayVar(&fields, "field", nil, "resolve only this field (title, body, status or deleted); repeatable")
	cmd.Flags().BoolVar(&yes, "yes", false, "apply the change instead of printing it")
	return cmd
}

func writeResolvePlan(out io.Writer, plan tickets.ResolvePlan) error {
	if plan.Action == tickets.ActionAccept {
		_, _ = fmt.Fprintf(out, "accept %s from %s\n", plan.Unit, plan.Key)
	} else {
		_, _ = fmt.Fprintf(out, "overwrite %s from %s\n", plan.Key, plan.Unit)
	}
	for _, c := range plan.Changes {
		var line string
		switch c.Field {
		case "body":
			line = fmt.Sprintf("body: %d characters -> %d characters", utf8.RuneCountInString(c.From), utf8.RuneCountInString(c.To))
		case "status":
			line = fmt.Sprintf("status: %s -> %s", c.From, c.To)
			if plan.Action == tickets.ActionAccept {
				line += fmt.Sprintf("\n  the ticket stays at %s until ralph maps the unit to another status", c.To)
			}
		case "deleted":
			line = "deleted: " + c.To
		default:
			line = fmt.Sprintf("%s: %q -> %q", c.Field, cut(c.From), cut(c.To))
		}
		if _, err := fmt.Fprintln(out, "  "+line); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintln(out, "run again with --yes to apply")
	return err
}

func cut(s string) string {
	if utf8.RuneCountInString(s) <= 80 {
		return s
	}
	return string([]rune(s)[:80]) + "…"
}
