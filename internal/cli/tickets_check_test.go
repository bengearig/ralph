package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/bengearig/ralph/internal/db"
	"github.com/bengearig/ralph/internal/taskmanager"
	"github.com/bengearig/ralph/internal/tickets"
	"github.com/bengearig/ralph/internal/units"
)

// syncedTickets makes a brief and two slices and pushes them: B-1 RAL-1, S-1 RAL-2, S-2 RAL-3.
func (f *ticketsFixture) syncedTickets() {
	f.t.Helper()
	f.with(func(svc units.Service) {
		b := f.create(svc, units.LevelBrief, "", "Brief", "")
		f.create(svc, units.LevelSlice, b, "First slice", "")
		f.create(svc, units.LevelSlice, b, "Second slice", "")
	})
	if code, out, stderr := run(f.t, "tickets", "sync"); code != 0 {
		f.t.Fatalf("sync: %d %q %q", code, out, stderr)
	}
}

// pushedStatus makes ralph remember that it left the ticket of unit in status.
func (f *ticketsFixture) pushedStatus(unit, status string) {
	f.t.Helper()
	d, err := db.Open(f.ctx, f.repo.Dir+"/.ralph/store.db", db.Options{})
	if err != nil {
		f.t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	st := tickets.NewStore(d)
	row, ok, err := st.Get(f.ctx, unit)
	if err != nil || !ok {
		f.t.Fatalf("row %s: %v %v", unit, ok, err)
	}
	row.PushedStatus = status
	if err := st.Put(f.ctx, row); err != nil {
		f.t.Fatal(err)
	}
}

func TestTicketsCommandCheckOutputLines(t *testing.T) {
	f := newTicketsFixture(t)
	f.syncedTickets()
	code, out, stderr := run(t, "tickets", "check")
	if code != 0 || out != "tickets: no differences\n" || stderr != "" {
		t.Fatalf("code %d out %q stderr %q", code, out, stderr)
	}

	f.srv.Edit("RAL-2", taskmanager.Fields{Title: "Edited in Jira", Body: "body of First slice"})
	f.pushedStatus("S-1", "In Progress")
	f.srv.Delete("RAL-3")
	code, out, stderr = run(t, "tickets", "check")
	want := "S-1 RAL-2: title and status differ in Jira\n" +
		"S-2 RAL-3: ticket no longer exists in Jira\n" +
		"tickets: 2 with differences\n"
	if code != 0 || out != want || stderr != "" {
		t.Fatalf("code %d\nout  %q\nwant %q\nstderr %q", code, out, want, stderr)
	}
}

func TestTicketsCommandCheckFollowedLine(t *testing.T) {
	f := newTicketsFixture(t)
	f.syncedTickets()
	f.srv.MoveKey("RAL-3", "RAL-40")
	code, out, _ := run(t, "tickets", "check")
	want := "followed S-2 RAL-3 -> RAL-40 (moved in Jira)\ntickets: no differences\n"
	if code != 0 || out != want {
		t.Fatalf("code %d out %q", code, out)
	}
	_, list, _ := run(t, "tickets")
	if !strings.Contains(list, "S-2  RAL-40  ") {
		t.Fatalf("list = %q", list)
	}
}

func TestTicketsCommandCheckJSON(t *testing.T) {
	f := newTicketsFixture(t)
	f.syncedTickets()
	f.srv.Edit("RAL-2", taskmanager.Fields{Title: "Edited in Jira", Body: "body of First slice"})
	f.srv.Delete("RAL-3")
	f.srv.MoveKey("RAL-1", "RAL-41")
	code, out, stderr := run(t, "tickets", "check", "--json")
	if code != 0 || stderr != "" {
		t.Fatalf("code %d stderr %q", code, stderr)
	}
	var got struct {
		Drifts []struct {
			Unit    string   `json:"unit"`
			Key     string   `json:"key"`
			Fields  []string `json:"fields"`
			MovedTo string   `json:"moved_to"`
		} `json:"drifts"`
		Found, Cleared, Followed int
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("%v: %q", err, out)
	}
	if len(got.Drifts) != 3 || got.Found != 2 || got.Cleared != 0 || got.Followed != 1 {
		t.Fatalf("json = %q", out)
	}
	if d := got.Drifts[0]; d.Unit != "B-1" || d.MovedTo != "RAL-41" || len(d.Fields) != 0 {
		t.Fatalf("drift = %+v", d)
	}
	if d := got.Drifts[1]; d.Unit != "S-1" || d.Key != "RAL-2" || strings.Join(d.Fields, ",") != "title" || d.MovedTo != "" {
		t.Fatalf("drift = %+v", d)
	}
	if !strings.Contains(out, `"fields": []`) {
		t.Fatalf("fields must be an array: %q", out)
	}
	_, none, _ := run(t, "tickets", "check", "--json")
	if !strings.Contains(none, `"drifts": [`) || !strings.Contains(none, `"found": 2`) {
		t.Fatalf("second check = %q", none)
	}
}

func TestTicketsCommandCheckGivenUnits(t *testing.T) {
	f := newTicketsFixture(t)
	f.syncedTickets()
	f.srv.Delete("RAL-2")
	f.srv.Delete("RAL-3")
	code, out, _ := run(t, "tickets", "check", "S-2")
	want := "S-2 RAL-3: ticket no longer exists in Jira\ntickets: 1 with differences\n"
	if code != 0 || out != want {
		t.Fatalf("code %d out %q", code, out)
	}
	_, list, _ := run(t, "tickets", "--json")
	var rows []ticketJSON
	if err := json.Unmarshal([]byte(list), &rows); err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if (r.Unit == "S-2") != (strings.Join(r.Drift, ",") == "deleted") {
			t.Fatalf("row %+v", r)
		}
	}
}

func TestTicketsCommandCheckUnknownUnit(t *testing.T) {
	f := newTicketsFixture(t)
	f.syncedTickets()
	f.with(func(svc units.Service) { f.create(svc, units.LevelTask, "S-1", "No ticket", "") })
	for _, id := range []string{"S-99", "T-1"} {
		code, out, stderr := run(t, "tickets", "check", id)
		if code != 1 || out != "" || !strings.Contains(stderr, id) {
			t.Fatalf("%s: code %d out %q stderr %q", id, code, out, stderr)
		}
	}
}

func TestTicketsCommandCheckJiraDownExitsOne(t *testing.T) {
	f := newTicketsFixture(t)
	f.syncedTickets()
	f.srv.Delete("RAL-3")
	f.srv.FailWith(503, `{"errorMessages":["down"]}`)
	code, out, stderr := run(t, "tickets", "check")
	if code != 1 || out != "" || stderr == "" {
		t.Fatalf("code %d out %q stderr %q", code, out, stderr)
	}
	f.srv.Heal()
	_, list, _ := run(t, "tickets", "--json")
	if strings.Contains(list, `"deleted"`) {
		t.Fatalf("a failed check invented a drift: %q", list)
	}
}

func TestTicketsCommandCheckIgnoresPollInterval(t *testing.T) {
	f := newTicketsFixture(t)
	f.syncedTickets()
	t.Setenv("RALPH_JIRA_POLL_INTERVAL", "24h")
	run(t, "tickets", "check")
	f.srv.Delete("RAL-3")
	code, out, _ := run(t, "tickets", "check")
	if code != 0 || !strings.Contains(out, "S-2 RAL-3: ticket no longer exists in Jira") {
		t.Fatalf("code %d out %q", code, out)
	}
}
