package tickets

import (
	"errors"
	"testing"

	"github.com/bengearig/ralph/internal/units"
)

func TestBuildBodyWithAndWithoutCriteria(t *testing.T) {
	e := newEnv(t)
	e.mapTasks()
	b := e.unit(units.LevelBrief, "", "Brief")
	s := e.unit(units.LevelSlice, b.ID, "Slice")
	withCrit, err := e.svc.Create(e.ctx, units.NewUnit{Level: units.LevelTask, ParentID: s.ID, Title: "T", Body: "Do it.", AcceptanceCriteria: "- works\n- tested"})
	if err != nil {
		t.Fatal(err)
	}
	d, err := Build(e.ctx, e.deps, withCrit, nil)
	if err == nil {
		t.Fatalf("no parent row yet, got %+v", d)
	}
	byID := map[string]Row{b.ID: {Key: "ABC-1"}, s.ID: {Key: "ABC-2"}}
	d, err = Build(e.ctx, e.deps, withCrit, byID)
	if err != nil {
		t.Fatal(err)
	}
	if want := "Do it.\n\n## Acceptance criteria\n\n- works\n- tested"; d.Body != want || d.Title != "T" {
		t.Fatalf("body = %q, title = %q", d.Body, d.Title)
	}
	d, err = Build(e.ctx, e.deps, s, byID)
	if err != nil || d.Body != "body of Slice" {
		t.Fatalf("body = %q, %v", d.Body, err)
	}
}

func TestBuildRef(t *testing.T) {
	e := newEnv(t)
	b := e.unit(units.LevelBrief, "", "Brief")
	d, err := Build(e.ctx, e.deps, b, nil)
	if err != nil || d.Ref != "ralph-proj-"+b.ID {
		t.Fatalf("ref = %q, %v", d.Ref, err)
	}
	if Ref("proj", "B-1") != "ralph-proj-B-1" {
		t.Fatal("Ref helper")
	}
}

func TestBuildTypeFromSettings(t *testing.T) {
	e := newEnv(t)
	e.mapTasks()
	e.deps.Settings.Types = map[string]string{"brief": "Epic", "slice": "Story", "task": "Sub-task"}
	b, s, tk := e.tree()
	byID := map[string]Row{b.ID: {Key: "ABC-1"}, s.ID: {Key: "ABC-2"}}
	for _, c := range []struct {
		u    units.Unit
		want string
	}{{b, "Epic"}, {s, "Story"}, {tk, "Sub-task"}} {
		d, err := Build(e.ctx, e.deps, c.u, byID)
		if err != nil || d.Type != c.want {
			t.Errorf("%s type = %q, %v, want %q", c.u.ID, d.Type, err, c.want)
		}
	}
}

func TestBuildParentNearestMappedAncestor(t *testing.T) {
	e := newEnv(t)
	b, s, tk := e.tree()
	byID := map[string]Row{b.ID: {Key: "ABC-1"}, s.ID: {Key: "ABC-2"}}

	if d, err := Build(e.ctx, e.deps, b, byID); err != nil || d.Parent != "" {
		t.Errorf("brief parent = %q, %v", d.Parent, err)
	}
	if d, err := Build(e.ctx, e.deps, s, byID); err != nil || d.Parent != "ABC-1" {
		t.Errorf("slice parent = %q, %v", d.Parent, err)
	}
	e.deps.Settings.Types = map[string]string{"brief": "Feature", "task": "Subtask"}
	if d, err := Build(e.ctx, e.deps, tk, byID); err != nil || d.Parent != "ABC-1" {
		t.Errorf("task under unmapped slice parent = %q, %v", d.Parent, err)
	}
	e.deps.Settings.Types = map[string]string{"task": "Subtask"}
	if d, err := Build(e.ctx, e.deps, tk, byID); err != nil || d.Parent != "" {
		t.Errorf("task with no mapped ancestor parent = %q, %v", d.Parent, err)
	}
}

func TestBuildParentWaiting(t *testing.T) {
	e := newEnv(t)
	b, s, _ := e.tree()
	_, err := Build(e.ctx, e.deps, s, map[string]Row{})
	if !errors.Is(err, ErrParentWaiting) || err.Error() != "parent "+b.ID+" has no ticket yet" {
		t.Fatalf("err = %v", err)
	}
	if _, err := Build(e.ctx, e.deps, s, map[string]Row{b.ID: {}}); !errors.Is(err, ErrParentWaiting) {
		t.Fatalf("row without key: err = %v", err)
	}
}
