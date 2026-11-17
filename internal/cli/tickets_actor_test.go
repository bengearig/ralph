package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bengearig/ralph/internal/taskmanager"
	"github.com/bengearig/ralph/internal/units"
)

const gitActor = "Test User <test@example.com>"

func (f *ticketsFixture) noteWith(id, prefix string) (n units.Note, found bool) {
	f.t.Helper()
	f.with(func(svc units.Service) {
		notes, err := svc.Notes(f.ctx, id)
		if err != nil {
			f.t.Fatal(err)
		}
		for _, c := range notes {
			if strings.HasPrefix(c.Body, prefix) {
				n, found = c, true
			}
		}
	})
	return n, found
}

func TestTicketsCommandAcceptRecordsActor(t *testing.T) {
	f := newTicketsFixture(t)
	f.editedInJira(taskmanager.Fields{Title: "Edited in Jira", Body: "New body"})
	if code, _, stderr := run(t, "tickets", "accept", "S-1", "--yes"); code != 0 {
		t.Fatalf("accept: %d %q", code, stderr)
	}
	if u := f.unit("S-1"); u.Title != "Edited in Jira" || u.UpdatedBy != gitActor {
		t.Fatalf("unit title %q updated by %q, want %q", u.Title, u.UpdatedBy, gitActor)
	}
	n, ok := f.noteWith("S-1", "Accepted from RAL-2")
	if !ok || n.Author != gitActor {
		t.Fatalf("note = %+v found %v, want author %q", n, ok, gitActor)
	}
}

func TestTicketsCommandOverwriteRecordsActor(t *testing.T) {
	f := newTicketsFixture(t)
	f.editedInJira(taskmanager.Fields{Title: "Edited in Jira", Body: "body of First slice"})
	if code, _, stderr := run(t, "tickets", "overwrite", "S-1", "--yes"); code != 0 {
		t.Fatalf("overwrite: %d %q", code, stderr)
	}
	n, ok := f.noteWith("S-1", "Overwrote Jira's")
	if !ok || n.Author != gitActor {
		t.Fatalf("note = %+v found %v, want author %q", n, ok, gitActor)
	}
}

func TestTicketsCommandSyncRecordsActor(t *testing.T) {
	f := newTicketsFixture(t)
	f.with(func(svc units.Service) {
		b := f.create(svc, units.LevelBrief, "", "Brief", "")
		f.create(svc, units.LevelSlice, b, "First slice", "")
	})
	if code, _, stderr := run(t, "tickets", "sync"); code != 0 {
		t.Fatalf("sync: %d %q", code, stderr)
	}
	f.with(func(svc units.Service) {
		links, err := svc.Links(f.ctx, "S-1")
		if err != nil || len(links) != 1 || links[0].Target != "jira:RAL-2" {
			t.Fatalf("links = %+v, %v", links, err)
		}
	})
}

func TestTicketsCommandListWritesNothing(t *testing.T) {
	f := newTicketsFixture(t)
	f.syncedTickets()
	path := filepath.Join(f.repo.Dir, ".ralph", "store.db")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if code, _, stderr := run(t, "tickets"); code != 0 {
		t.Fatalf("tickets: %d %q", code, stderr)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("database file changed")
	}
}
