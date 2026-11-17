package cli

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/bengearig/ralph/internal/db"
	"github.com/bengearig/ralph/internal/tickets"
	"github.com/bengearig/ralph/internal/units"
)

func (f *ticketsFixture) store(fn func(s *tickets.Store)) {
	f.t.Helper()
	d, err := db.Open(f.ctx, filepath.Join(f.repo.Dir, ".ralph", "store.db"), db.Options{})
	if err != nil {
		f.t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	fn(tickets.NewStore(d))
}

func (f *ticketsFixture) walkFlow() {
	f.srv.SetWorkflow("To Do", map[string][]string{
		"To Do":       {"In Progress"},
		"In Progress": {"In Review"},
		"In Review":   {"Done"},
	})
	f.t.Setenv("RALPH_JIRA_FLOW", `["To Do","In Progress","In Review","Done"]`)
}

func TestTicketsCommandSyncPushOutputLines(t *testing.T) {
	f := newTicketsFixture(t)
	f.walkFlow()
	var slice string
	f.with(func(svc units.Service) {
		b := f.create(svc, units.LevelBrief, "", "Brief", "")
		slice = f.create(svc, units.LevelSlice, b, "Slice", "")
	})
	if code, _, stderr := run(t, "tickets", "sync"); code != 0 {
		t.Fatal(stderr)
	}
	f.with(func(svc units.Service) {
		u, err := svc.Get(f.ctx, slice)
		if err != nil {
			t.Fatal(err)
		}
		title := "Renamed slice"
		if _, err := svc.Update(f.ctx, slice, u.Fingerprint, units.Change{Title: &title}); err != nil {
			t.Fatal(err)
		}
		f.create(svc, units.LevelTask, slice, "Task", units.StateDone)
	})
	code, out, stderr := run(t, "tickets", "sync")
	want := "moved B-1 RAL-1 To Do -> Done (via In Progress, In Review)\n" +
		"updated S-1 RAL-2 (title)\n" +
		"moved S-1 RAL-2 To Do -> Done (via In Progress, In Review)\n" +
		"tickets: 0 created, 0 adopted, 1 updated, 2 moved, 0 scheduled, 0 held, 0 skipped, 0 failed\n"
	if code != 0 || stderr != "" || out != want {
		t.Fatalf("code %d\nout %q\nwant %q\nstderr %q", code, out, want, stderr)
	}
	if got := f.srv.Status("RAL-2"); got != "Done" {
		t.Fatalf("status = %q", got)
	}
}

func TestTicketsCommandSyncMovedWithoutVia(t *testing.T) {
	f := newTicketsFixture(t)
	f.with(func(svc units.Service) { f.create(svc, units.LevelBrief, "", "Brief", "") })
	if code, _, stderr := run(t, "tickets", "sync"); code != 0 {
		t.Fatal(stderr)
	}
	f.with(func(svc units.Service) {
		b := f.create(svc, units.LevelBrief, "", "Second", "")
		sl := f.create(svc, units.LevelSlice, b, "Slice", "")
		f.create(svc, units.LevelTask, sl, "Task", units.StateInProgress)
	})
	_, out, _ := run(t, "tickets", "sync")
	if want := "moved B-2 RAL-2 To Do -> In Progress\n"; !strings.Contains(out, want) {
		t.Fatalf("out %q lacks %q", out, want)
	}
}

func TestTicketsCommandSyncHeldLine(t *testing.T) {
	f := newTicketsFixture(t)
	f.with(func(svc units.Service) { f.create(svc, units.LevelBrief, "", "Brief", "") })
	if code, _, stderr := run(t, "tickets", "sync"); code != 0 {
		t.Fatal(stderr)
	}
	f.store(func(s *tickets.Store) {
		row, ok, err := s.Get(f.ctx, "B-1")
		if err != nil || !ok {
			t.Fatalf("row: %v %v", ok, err)
		}
		row.Drift = "title"
		if err := s.Put(f.ctx, row); err != nil {
			t.Fatal(err)
		}
	})
	code, out, _ := run(t, "tickets", "sync")
	want := "held B-1 RAL-1: differs in Jira (title); run ralph tickets\n" +
		"tickets: 0 created, 0 adopted, 0 updated, 0 moved, 0 scheduled, 1 held, 0 skipped, 0 failed\n"
	if code != 0 || out != want {
		t.Fatalf("code %d out %q", code, out)
	}
}

func TestTicketsCommandSyncDeferredLine(t *testing.T) {
	ev := tickets.Event{Kind: "deferred", Unit: "S-1", Key: "RAL-2", Message: "move: failed 2m ago, retry after 2026-03-04T05:16:07Z"}
	if got, want := eventLine(ev), "deferred S-1 move: failed 2m ago, retry after 2026-03-04T05:16:07Z"; got != want {
		t.Fatalf("line = %q, want %q", got, want)
	}
}

func TestTicketsCommandSyncSummaryLine(t *testing.T) {
	f := newTicketsFixture(t)
	var s1 string
	f.with(func(svc units.Service) {
		b := f.create(svc, units.LevelBrief, "", "Brief", "")
		s1 = f.create(svc, units.LevelSlice, b, "One", "")
		f.create(svc, units.LevelSlice, b, "Two", "")
	})
	if code, _, stderr := run(t, "tickets", "sync"); code != 0 {
		t.Fatal(stderr)
	}
	f.store(func(s *tickets.Store) {
		row, _, err := s.Get(f.ctx, "S-2")
		if err != nil {
			t.Fatal(err)
		}
		row.Drift = "body"
		if err := s.Put(f.ctx, row); err != nil {
			t.Fatal(err)
		}
	})
	f.with(func(svc units.Service) {
		u, err := svc.Get(f.ctx, s1)
		if err != nil {
			t.Fatal(err)
		}
		title := "One renamed"
		if _, err := svc.Update(f.ctx, s1, u.Fingerprint, units.Change{Title: &title}); err != nil {
			t.Fatal(err)
		}
		f.create(svc, units.LevelTask, s1, "Task", units.StateInProgress)
	})
	code, out, _ := run(t, "tickets", "sync")
	if want := "tickets: 0 created, 0 adopted, 1 updated, 2 moved, 0 scheduled, 1 held, 0 skipped, 0 failed\n"; code != 0 || !strings.Contains(out, want) {
		t.Fatalf("code %d out %q", code, out)
	}
}
