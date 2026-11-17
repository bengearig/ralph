package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os/signal"
	"syscall"
	"time"

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
	return cmd
}

func newTicketsSyncCommand(a *app) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Create the tickets of the units that have none",
		Long: "Create a ticket in the task manager for every brief, slice and task whose level has a ticket type and that has none yet,\n" +
			"and put the new tickets in the configured sprint. Tickets that exist are not changed.\n" +
			"The command exits 1 when anything failed or the pass stopped early.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()
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
				_, _ = fmt.Fprintf(out, "tickets: %d created, %d adopted, %d scheduled, %d skipped, %d failed\n",
					len(report.Created), len(report.Adopted), len(report.Scheduled), len(report.Skipped), len(report.Failed))
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
	Scheduled []itemJSON    `json:"scheduled"`
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
	out := syncReportJSON{Created: items(r.Created), Adopted: items(r.Adopted), Scheduled: items(r.Scheduled), Skipped: items(r.Skipped), Failed: make([]failureJSON, 0, len(r.Failed))}
	for _, f := range r.Failed {
		out.Failed = append(out.Failed, failureJSON(f))
	}
	return out
}

type ticketJSON struct {
	Unit         string `json:"unit"`
	System       string `json:"system"`
	Key          string `json:"key"`
	TicketID     string `json:"ticket_id"`
	Type         string `json:"type"`
	Title        string `json:"title"`
	PushedStatus string `json:"pushed_status"`
	PushedParent string `json:"pushed_parent"`
	Scheduled    bool   `json:"scheduled"`
	Detached     bool   `json:"detached"`
	Drift        string `json:"drift"`
	FailedStep   string `json:"failed_step"`
	FailedAt     string `json:"failed_at"`
	LastError    string `json:"last_error"`
	Attempts     int    `json:"attempts"`
	SyncedAt     string `json:"synced_at"`
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
			Drift:    r.Drift, FailedStep: r.FailedStep, FailedAt: timeText(r.FailedAt), LastError: r.LastError,
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

func timeText(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
