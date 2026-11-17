package cli

import (
	"strings"
	"testing"

	"github.com/bengearig/ralph/internal/taskmanager"
	"github.com/bengearig/ralph/internal/tickets"
	"github.com/bengearig/ralph/internal/units"
)

// editedInJira syncs the fixture, changes S-1's ticket in Jira and records the difference.
func (f *ticketsFixture) editedInJira(fields taskmanager.Fields) {
	f.t.Helper()
	f.syncedTickets()
	f.srv.Edit("RAL-2", fields)
	if code, _, stderr := run(f.t, "tickets", "check"); code != 0 {
		f.t.Fatalf("check: %d %q", code, stderr)
	}
}

func (f *ticketsFixture) unit(id string) (u units.Unit) {
	f.t.Helper()
	f.with(func(svc units.Service) {
		var err error
		if u, err = svc.Get(f.ctx, id); err != nil {
			f.t.Fatal(err)
		}
	})
	return u
}

func (f *ticketsFixture) row(id string) (r tickets.Row) {
	f.t.Helper()
	f.store(func(s *tickets.Store) {
		var err error
		if r, _, err = s.Get(f.ctx, id); err != nil {
			f.t.Fatal(err)
		}
	})
	return r
}

func TestTicketsCommandAcceptDryRunPrintsPlan(t *testing.T) {
	f := newTicketsFixture(t)
	f.editedInJira(taskmanager.Fields{Title: "Edited in Jira", Body: "A longer body than before"})
	row, u := f.row("S-1"), f.unit("S-1")
	code, out, stderr := run(t, "tickets", "accept", "S-1")
	want := "accept S-1 from RAL-2\n" +
		"  title: \"First slice\" -> \"Edited in Jira\"\n" +
		"  body: 19 characters -> 25 characters\n" +
		"run again with --yes to apply\n"
	if code != 0 || out != want || stderr != "" {
		t.Fatalf("code %d\nout %q\nwant %q\nstderr %q", code, out, want, stderr)
	}
	if f.row("S-1") != row || f.unit("S-1") != u {
		t.Fatal("database changed")
	}
}

func TestTicketsCommandAcceptYes(t *testing.T) {
	f := newTicketsFixture(t)
	f.editedInJira(taskmanager.Fields{Title: "Edited in Jira", Body: "New body"})
	code, out, stderr := run(t, "tickets", "accept", "S-1", "--yes")
	if want := "accepted S-1 title, body from RAL-2\n"; code != 0 || out != want || stderr != "" {
		t.Fatalf("code %d out %q stderr %q", code, out, stderr)
	}
	if u := f.unit("S-1"); u.Title != "Edited in Jira" || u.Body != "New body" {
		t.Fatalf("unit = %+v", u)
	}
	if r := f.row("S-1"); r.Drift != "" {
		t.Fatalf("row = %+v", r)
	}
}

func TestTicketsCommandOverwriteDryRun(t *testing.T) {
	f := newTicketsFixture(t)
	f.editedInJira(taskmanager.Fields{Title: "Edited in Jira", Body: "body of First slice"})
	row := f.row("S-1")
	code, out, _ := run(t, "tickets", "overwrite", "S-1")
	want := "overwrite RAL-2 from S-1\n" +
		"  title: \"Edited in Jira\" -> \"First slice\"\n" +
		"run again with --yes to apply\n"
	if code != 0 || out != want {
		t.Fatalf("code %d out %q", code, out)
	}
	if f.row("S-1") != row {
		t.Fatal("row changed")
	}
}

func TestTicketsCommandOverwriteYes(t *testing.T) {
	f := newTicketsFixture(t)
	f.editedInJira(taskmanager.Fields{Title: "Edited in Jira", Body: "body of First slice"})
	code, out, stderr := run(t, "tickets", "overwrite", "S-1", "--yes")
	want := "overwrote RAL-2 title from S-1\n" +
		"updated S-1 RAL-2 (title)\n"
	if code != 0 || out != want || stderr != "" {
		t.Fatalf("code %d out %q stderr %q", code, out, stderr)
	}
	if r := f.row("S-1"); r.Drift != "" {
		t.Fatalf("row = %+v", r)
	}
}

func TestTicketsCommandResolveFieldFlagRepeatable(t *testing.T) {
	f := newTicketsFixture(t)
	f.editedInJira(taskmanager.Fields{Title: "Edited in Jira", Body: "Edited body"})
	code, out, _ := run(t, "tickets", "accept", "S-1", "--field", "body", "--field", "title")
	if want := "accept S-1 from RAL-2\n  title: \"First slice\" -> \"Edited in Jira\"\n  body: 19 characters -> 11 characters\nrun again with --yes to apply\n"; code != 0 || out != want {
		t.Fatalf("code %d out %q", code, out)
	}
	if code, out, _ = run(t, "tickets", "accept", "S-1", "--field", "body", "--yes"); code != 0 || out != "accepted S-1 body from RAL-2\n" {
		t.Fatalf("code %d out %q", code, out)
	}
	if r := f.row("S-1"); r.Drift != "title" {
		t.Fatalf("drift = %q", r.Drift)
	}
}

func TestTicketsCommandResolveNoDriftExit1(t *testing.T) {
	f := newTicketsFixture(t)
	f.syncedTickets()
	code, out, stderr := run(t, "tickets", "accept", "S-1", "--yes")
	if code != 1 || out != "" || !strings.Contains(stderr, "S-1 has no differences to resolve") {
		t.Fatalf("code %d out %q stderr %q", code, out, stderr)
	}
}

func TestTicketsCommandResolveUnknownUnit(t *testing.T) {
	f := newTicketsFixture(t)
	f.syncedTickets()
	code, out, stderr := run(t, "tickets", "overwrite", "S-99")
	if code != 1 || out != "" || !strings.Contains(stderr, "S-99") {
		t.Fatalf("code %d out %q stderr %q", code, out, stderr)
	}
}

func TestTicketsCommandResolveLongValuesCut(t *testing.T) {
	f := newTicketsFixture(t)
	long := strings.Repeat("x", 100)
	f.editedInJira(taskmanager.Fields{Title: long, Body: "body of First slice"})
	_, out, _ := run(t, "tickets", "accept", "S-1")
	want := "  title: \"First slice\" -> \"" + strings.Repeat("x", 80) + "…\"\n"
	if !strings.Contains(out, want) {
		t.Fatalf("out %q", out)
	}
}

func TestTicketsCommandAcceptPlanShowsStaysLine(t *testing.T) {
	f := newTicketsFixture(t)
	f.syncedTickets()
	f.srv.Edit("RAL-2", taskmanager.Fields{Title: "Edited in Jira", Body: "body of First slice"})
	f.pushedStatus("S-1", "In Progress")
	if code, _, stderr := run(t, "tickets", "check"); code != 0 {
		t.Fatalf("check: %d %q", code, stderr)
	}
	code, out, stderr := run(t, "tickets", "accept", "S-1")
	want := "accept S-1 from RAL-2\n" +
		"  title: \"First slice\" -> \"Edited in Jira\"\n" +
		"  status: In Progress -> To Do\n" +
		"  the ticket stays at To Do until ralph maps the unit to another status\n" +
		"run again with --yes to apply\n"
	if code != 0 || out != want || stderr != "" {
		t.Fatalf("code %d\nout  %q\nwant %q\nstderr %q", code, out, want, stderr)
	}
	code, out, stderr = run(t, "tickets", "accept", "S-1", "--field", "title")
	if code != 0 || strings.Contains(out, "stays at") || stderr != "" {
		t.Fatalf("title only: code %d out %q stderr %q", code, out, stderr)
	}
	code, out, _ = run(t, "tickets", "overwrite", "S-1")
	if code != 0 || strings.Contains(out, "stays at") {
		t.Fatalf("overwrite: code %d out %q", code, out)
	}
}
