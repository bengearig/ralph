package tickets

import (
	"testing"

	"github.com/bengearig/ralph/internal/taskmanager"
	"github.com/bengearig/ralph/internal/units"
)

func TestStatusForBuiltInDefaults(t *testing.T) {
	want := map[string]string{
		"EMPTY": "To Do", "ARTICULATING": "To Do", "READY": "To Do", "PROPOSED": "To Do", "TODO": "To Do",
		"IN_PROGRESS": "In Progress", "NEEDS_ATTENTION": "In Progress", "DONE": "Done", "MERGED": "Done",
	}
	for state, status := range want {
		if got, ok := StatusFor(taskmanager.Settings{}, state); !ok || got != status {
			t.Errorf("%s = %q, %v; want %q", state, got, ok, status)
		}
	}
}

func TestStatusForConfiguredWins(t *testing.T) {
	s := taskmanager.Settings{States: map[string]string{"DONE": "Closed", "BLOCKED": "On Hold"}}
	for state, want := range map[string]string{"DONE": "Closed", "BLOCKED": "On Hold", "TODO": "To Do"} {
		if got, ok := StatusFor(s, state); !ok || got != want {
			t.Errorf("%s = %q, %v; want %q", state, got, ok, want)
		}
	}
}

func TestStatusForUnmappedIsFalse(t *testing.T) {
	for _, state := range []string{"BLOCKED", "IN_REVIEW", "CANCELLED", "OTHER", ""} {
		if got, ok := StatusFor(taskmanager.Settings{}, state); ok || got != "" {
			t.Errorf("%q = %q, %v; want none", state, got, ok)
		}
	}
}

func TestWantedStatusMapsUnitState(t *testing.T) {
	e := newEnv(t)
	e.mapTasks()
	_, s, tk := e.tree()
	got := func(id string) string {
		t.Helper()
		w, err := WantedStatus(e.ctx, e.deps, id)
		if err != nil {
			t.Fatal(err)
		}
		return w
	}
	e.setState(tk.ID, units.StateTodo)
	if w := got(tk.ID); w != "To Do" {
		t.Errorf("todo task = %q", w)
	}
	e.setState(tk.ID, units.StateInProgress)
	if w := got(s.ID); w != "In Progress" {
		t.Errorf("derived slice = %q", w)
	}
	e.deps.Settings.States = map[string]string{"IN_PROGRESS": "Doing"}
	if w := got(s.ID); w != "Doing" {
		t.Errorf("configured = %q", w)
	}
	e.deps.Settings.States = map[string]string{}
	e.setState(tk.ID, units.StateBlocked)
	if w := got(tk.ID); w != "" {
		t.Errorf("unmapped state = %q", w)
	}
}
