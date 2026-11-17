package tickets

import (
	"context"
	"fmt"

	"github.com/bengearig/ralph/internal/taskmanager"
)

var defaultStatuses = map[string]string{
	"EMPTY":           "To Do",
	"ARTICULATING":    "To Do",
	"READY":           "To Do",
	"PROPOSED":        "To Do",
	"TODO":            "To Do",
	"IN_PROGRESS":     "In Progress",
	"NEEDS_ATTENTION": "In Progress",
	"DONE":            "Done",
	"MERGED":          "Done",
}

// StatusFor returns the status the ticket of a unit in state should have: the one the settings name, else the
// built-in default. A state with neither has none, and its ticket is left where it is.
func StatusFor(s taskmanager.Settings, state string) (string, bool) {
	if status, ok := s.States[state]; ok && status != "" {
		return status, true
	}
	status, ok := defaultStatuses[state]
	return status, ok
}

// WantedStatus returns the ticket status the unit's derived status or task state maps to now, empty when none.
func WantedStatus(ctx context.Context, d Deps, unitID string) (string, error) {
	states, err := d.Units.Statuses(ctx, []string{unitID})
	if err != nil {
		return "", fmt.Errorf("tickets: status of %s: %w", unitID, err)
	}
	status, _ := StatusFor(d.Settings, string(states[unitID]))
	return status, nil
}
