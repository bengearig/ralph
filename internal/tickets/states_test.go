package tickets

import (
	"testing"

	"github.com/bengearig/ralph/internal/taskmanager"
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
