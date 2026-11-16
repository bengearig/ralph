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
