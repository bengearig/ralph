package tickets

import (
	"reflect"
	"strings"
	"testing"

	"github.com/bengearig/ralph/internal/taskmanager"
	"github.com/bengearig/ralph/internal/taskmanager/taskmanagertest"
)

func norm() func(taskmanager.Fields) taskmanager.Fields {
	return taskmanagertest.NewFake("ABC").Normalize
}

func pushedRow() Row {
	return Row{
		UnitID: "S-1", Key: "ABC-1",
		PushedTitle: Hash("Title"), PushedBody: Hash("Body"), PushedStatus: "To Do",
	}
}

func seen(row Row, t taskmanager.Ticket) Observation {
	return Observation{Unit: row.UnitID, Row: row, Ticket: &t}
}

func same() taskmanager.Ticket {
	return taskmanager.Ticket{Key: "ABC-1", Title: "Title", Body: "Body", Status: "To Do"}
}

func planOne(t *testing.T, o Observation) []Drift {
	t.Helper()
	return Plan([]Observation{o}, norm())
}

func wantFields(t *testing.T, got []Drift, want ...string) {
	t.Helper()
	if len(got) != 1 || !reflect.DeepEqual(got[0].Fields, want) {
		t.Fatalf("drifts = %+v, want fields %v", got, want)
	}
}

func TestPlanDriftTitle(t *testing.T) {
	tk := same()
	tk.Title = "Edited"
	wantFields(t, planOne(t, seen(pushedRow(), tk)), "title")
}

func TestPlanDriftBody(t *testing.T) {
	tk := same()
	tk.Body = "Edited"
	wantFields(t, planOne(t, seen(pushedRow(), tk)), "body")
}

func TestPlanDriftStatusIgnoresCase(t *testing.T) {
	tk := same()
	tk.Status = "to do"
	if got := planOne(t, seen(pushedRow(), tk)); len(got) != 0 {
		t.Fatalf("drifts = %+v", got)
	}
	tk.Status = "Done"
	wantFields(t, planOne(t, seen(pushedRow(), tk)), "status")
}

func TestPlanDriftFieldsOrder(t *testing.T) {
	tk := taskmanager.Ticket{Key: "ABC-1", Title: "a", Body: "b", Status: "Done"}
	wantFields(t, planOne(t, seen(pushedRow(), tk)), "title", "body", "status")
}

func TestPlanDriftNoneWhenEqual(t *testing.T) {
	if got := planOne(t, seen(pushedRow(), same())); len(got) != 0 {
		t.Fatalf("drifts = %+v", got)
	}
}

func TestPlanDriftNormalizedCompare(t *testing.T) {
	long := strings.Repeat("x", 300)
	n := norm()(taskmanager.Fields{Title: long, Body: "Body"})
	row := pushedRow()
	row.PushedTitle = Hash(n.Title)
	tk := same()
	tk.Title = long
	tk.Body = "Body \n\n"
	if got := planOne(t, seen(row, tk)); len(got) != 0 {
		t.Fatalf("drifts = %+v", got)
	}
}

func TestPlanDriftEmptyPushedNeverDrifts(t *testing.T) {
	row := Row{UnitID: "S-1", Key: "ABC-1"}
	tk := taskmanager.Ticket{Key: "ABC-1", Title: "Any", Body: "Any", Status: "Any"}
	if got := planOne(t, seen(row, tk)); len(got) != 0 {
		t.Fatalf("drifts = %+v", got)
	}
}

func TestPlanDriftDeletedWhenAbsent(t *testing.T) {
	got := planOne(t, Observation{Unit: "S-1", Row: pushedRow()})
	if len(got) != 1 || got[0].Key != "ABC-1" || !reflect.DeepEqual(got[0].Fields, []string{"deleted"}) || got[0].MovedTo != "" {
		t.Fatalf("drifts = %+v", got)
	}
}

func TestPlanDriftMovedKeyFollowed(t *testing.T) {
	tk := same()
	tk.Key = "ABC-40"
	got := planOne(t, seen(pushedRow(), tk))
	if len(got) != 1 || got[0].MovedTo != "ABC-40" || got[0].Key != "ABC-1" || len(got[0].Fields) != 0 {
		t.Fatalf("drifts = %+v", got)
	}
	tk.Title = "Edited"
	got = planOne(t, seen(pushedRow(), tk))
	if len(got) != 1 || got[0].MovedTo != "ABC-40" || !reflect.DeepEqual(got[0].Fields, []string{"title"}) {
		t.Fatalf("drifts = %+v", got)
	}
}

func TestPlanIsPure(t *testing.T) {
	tk := taskmanager.Ticket{Key: "ABC-9", Title: "a", Body: "b", Status: "Done"}
	obs := []Observation{seen(pushedRow(), tk), {Unit: "S-2", Row: Row{UnitID: "S-2", Key: "ABC-2"}}}
	before := []Observation{seen(pushedRow(), tk), {Unit: "S-2", Row: Row{UnitID: "S-2", Key: "ABC-2"}}}
	first := Plan(obs, norm())
	if !reflect.DeepEqual(obs[0].Row, before[0].Row) || *obs[0].Ticket != *before[0].Ticket || obs[1].Ticket != nil {
		t.Fatal("Plan changed its input")
	}
	if second := Plan(obs, norm()); !reflect.DeepEqual(first, second) {
		t.Fatalf("%+v != %+v", first, second)
	}
}
