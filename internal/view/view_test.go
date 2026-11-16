package view

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/bengearig/ralph/internal/db"
	"github.com/bengearig/ralph/internal/testutil"
	"github.com/bengearig/ralph/internal/units"
)

func seed(t *testing.T) units.Service {
	t.Helper()
	svc := units.NewService(testutil.NewDB(t, db.Migrations))
	ctx := units.WithActor(context.Background(), "Ann <ann@example.com>")
	mk := func(level units.Level, parent, title string) units.Unit {
		n := units.NewUnit{Level: level, ParentID: parent, Title: title, Body: "b"}
		if level == units.LevelTask {
			n.AcceptanceCriteria = "c"
		}
		u, err := svc.Create(ctx, n)
		if err != nil {
			t.Fatal(err)
		}
		return u
	}
	b := mk(units.LevelBrief, "", "brief")
	s := mk(units.LevelSlice, b.ID, "slice")
	mk(units.LevelTask, s.ID, "in slice")
	o := mk(units.LevelTask, "", "orphan")
	for _, st := range []units.State{units.StateTodo, units.StateInProgress, units.StateDone} {
		var err error
		if o, err = svc.SetState(ctx, o.ID, o.Fingerprint, st, ""); err != nil {
			t.Fatal(err)
		}
	}
	return svc
}

func TestSummary(t *testing.T) {
	svc := seed(t)
	ctx := context.Background()

	got, err := Summary(ctx, svc, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Briefs) != 1 || len(got.Briefs[0].Slices) != 1 || got.Briefs[0].Slices[0].Tasks["PROPOSED"] != 1 {
		t.Errorf("briefs = %+v", got.Briefs)
	}
	if len(got.Tasks) != 0 || got.Hidden != 1 {
		t.Errorf("tasks = %+v, hidden = %d; want the finished orphan hidden", got.Tasks, got.Hidden)
	}

	got, err = Summary(ctx, svc, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Tasks) != 1 || got.Tasks[0].Status != "DONE" || got.Hidden != 0 {
		t.Errorf("tasks = %+v, hidden = %d", got.Tasks, got.Hidden)
	}
}

func TestUnitDetail(t *testing.T) {
	svc := seed(t)
	ctx := context.Background()

	v, err := UnitDetail(ctx, svc, "S-1")
	if err != nil {
		t.Fatal(err)
	}
	if v.Level != "slice" || v.Parent == nil || v.Parent.ID != "B-1" || len(v.Children) != 1 || v.Children[0].ID != "T-1" {
		t.Errorf("slice = %+v", v)
	}
	v, err = UnitDetail(ctx, svc, "T-2")
	if err != nil {
		t.Fatal(err)
	}
	if v.Parent != nil || v.Children == nil || v.Links == nil || v.Notes == nil {
		t.Errorf("orphan task = %+v", v)
	}
}

func TestUnitDetailNotFound(t *testing.T) {
	_, err := UnitDetail(context.Background(), seed(t), "T-99")
	var nf *NotFoundError
	if !errors.As(err, &nf) || !errors.Is(err, units.ErrNotFound) || err.Error() != "unit T-99 was not found" {
		t.Errorf("err = %v", err)
	}
}

func TestSummaryUnfinalizedField(t *testing.T) {
	svc := seed(t)
	ctx := units.WithActor(context.Background(), "Ann <ann@example.com>")

	before, err := Summary(ctx, svc, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(before.Unfinalized) != 0 {
		t.Fatalf("unfinalized = %+v", before.Unfinalized)
	}
	rawBefore, _ := json.Marshal(before)
	if strings.Contains(string(rawBefore), "unfinalized") {
		t.Errorf("json has unfinalized: %s", rawBefore)
	}

	u, err := svc.Create(ctx, units.NewUnit{Level: units.LevelTask, Title: "quick", Body: "b", AcceptanceCriteria: "c"})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.AddLink(ctx, u.ID, units.LinkExternal, "branch:ralph/quick"); err != nil {
		t.Fatal(err)
	}
	for _, st := range []units.State{units.StateTodo, units.StateInProgress} {
		if u, err = svc.SetState(ctx, u.ID, u.Fingerprint, st, ""); err != nil {
			t.Fatal(err)
		}
	}
	got, err := Summary(ctx, svc, false)
	if err != nil {
		t.Fatal(err)
	}
	want := UnfinalizedView{ID: u.ID, Title: "quick", Branch: "ralph/quick"}
	if len(got.Unfinalized) != 1 || got.Unfinalized[0] != want {
		t.Errorf("unfinalized = %+v", got.Unfinalized)
	}
	raw, _ := json.Marshal(got)
	if !strings.Contains(string(raw), `"unfinalized":[{"id":"`+u.ID+`","title":"quick","branch":"ralph/quick"}]`) {
		t.Errorf("json = %s", raw)
	}
}

func TestUnitGetNoteKind(t *testing.T) {
	svc := seed(t)
	ctx := context.Background()
	if _, err := svc.AddNote(ctx, "T-1", units.NoteKickback, "replan"); err != nil {
		t.Fatal(err)
	}
	v, err := UnitDetail(ctx, svc, "T-1")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(v.Notes)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"kind":"KICKBACK"`) {
		t.Errorf("notes = %s", raw)
	}
}

func twoSlices(t *testing.T) (svc units.Service, first, second string) {
	t.Helper()
	svc = seed(t)
	ctx := context.Background()
	b, err := svc.List(ctx, units.Filter{Level: units.LevelBrief})
	if err != nil || len(b) != 1 {
		t.Fatalf("briefs = %v, %v", b, err)
	}
	s, err := svc.Create(ctx, units.NewUnit{Level: units.LevelSlice, ParentID: b[0].ID, Title: "later", Body: "b"})
	if err != nil {
		t.Fatal(err)
	}
	sl, err := svc.List(ctx, units.Filter{Level: units.LevelSlice})
	if err != nil {
		t.Fatal(err)
	}
	return svc, sl[0].ID, s.ID
}

func TestSliceDependenciesInDetail(t *testing.T) {
	svc, first, second := twoSlices(t)
	ctx := context.Background()
	if err := svc.AddDependency(ctx, second, first); err != nil {
		t.Fatal(err)
	}
	v, err := UnitDetail(ctx, svc, second)
	if err != nil {
		t.Fatal(err)
	}
	want := []Dependency{{ID: first, Title: "slice", Status: "ARTICULATING", Satisfied: false}}
	if len(v.DependsOn) != 1 || v.DependsOn[0] != want[0] {
		t.Errorf("depends_on = %+v, want %+v", v.DependsOn, want)
	}
	if len(v.WaitingOn) != 1 || v.WaitingOn[0] != first {
		t.Errorf("waiting_on = %v, want [%s]", v.WaitingOn, first)
	}
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"depends_on":[{"id":"`+first+`","title":"slice","status":"ARTICULATING","satisfied":false}]`) {
		t.Errorf("json = %s", raw)
	}

	tasks, err := svc.List(ctx, units.Filter{ParentID: &first})
	if err != nil || len(tasks) != 1 {
		t.Fatalf("tasks = %v, %v", tasks, err)
	}
	task := tasks[0]
	for _, st := range []units.State{units.StateTodo, units.StateInProgress, units.StateDone} {
		if task, err = svc.SetState(ctx, task.ID, task.Fingerprint, st, ""); err != nil {
			t.Fatal(err)
		}
	}
	v, err = UnitDetail(ctx, svc, second)
	if err != nil {
		t.Fatal(err)
	}
	if len(v.DependsOn) != 1 || !v.DependsOn[0].Satisfied || v.DependsOn[0].Status != "DONE" {
		t.Errorf("depends_on = %+v, want satisfied", v.DependsOn)
	}
	if len(v.WaitingOn) != 0 {
		t.Errorf("waiting_on = %v, want none", v.WaitingOn)
	}
	raw, _ = json.Marshal(v)
	if strings.Contains(string(raw), "waiting_on") || !strings.Contains(string(raw), `"satisfied":true`) {
		t.Errorf("json = %s", raw)
	}
}

func TestSummaryWaitingOn(t *testing.T) {
	svc, first, second := twoSlices(t)
	ctx := context.Background()
	if err := svc.AddDependency(ctx, second, first); err != nil {
		t.Fatal(err)
	}
	got, err := Summary(ctx, svc, false)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]Slice{}
	for _, s := range got.Briefs[0].Slices {
		byID[s.ID] = s
	}
	if w := byID[second].WaitingOn; len(w) != 1 || w[0] != first {
		t.Errorf("waiting_on of %s = %v, want [%s]", second, w, first)
	}
	if w := byID[first].WaitingOn; len(w) != 0 {
		t.Errorf("waiting_on of %s = %v, want none", first, w)
	}
	raw, _ := json.Marshal(got)
	if !strings.Contains(string(raw), `"waiting_on":["`+first+`"]`) {
		t.Errorf("json = %s", raw)
	}
}

func TestNoDependenciesOutputUnchanged(t *testing.T) {
	svc := seed(t)
	ctx := context.Background()
	sum, err := Summary(ctx, svc, true)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(sum)
	if strings.Contains(string(raw), "waiting_on") || strings.Contains(string(raw), "depends_on") {
		t.Errorf("summary json = %s", raw)
	}
	for _, id := range []string{"B-1", "S-2", "T-3"} {
		if _, err := svc.Get(ctx, id); err != nil {
			continue
		}
		v, err := UnitDetail(ctx, svc, id)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(v)
		var m map[string]json.RawMessage
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
		for _, k := range []string{"depends_on", "waiting_on"} {
			if _, ok := m[k]; ok {
				t.Errorf("%s json has %s: %s", id, k, raw)
			}
		}
		for _, k := range []string{"id", "level", "title", "status", "parent", "body", "created_at", "updated_at", "children", "links", "notes"} {
			if _, ok := m[k]; !ok {
				t.Errorf("%s json lacks %s", id, k)
			}
		}
	}
}
