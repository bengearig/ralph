package tickets

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/bengearig/ralph/internal/db"
	"github.com/bengearig/ralph/internal/taskmanager"
	"github.com/bengearig/ralph/internal/units"
)

// ErrParentWaiting means the unit's nearest mapped ancestor has no ticket yet.
var ErrParentWaiting = errors.New("parent has no ticket yet")

// ParentWaitingError names the ancestor that has no ticket.
type ParentWaitingError struct{ Parent string }

func (e *ParentWaitingError) Error() string {
	return fmt.Sprintf("parent %s has no ticket yet", e.Parent)
}

// Is makes errors.Is(err, ErrParentWaiting) true.
func (e *ParentWaitingError) Is(target error) bool { return target == ErrParentWaiting }

// Event is something a pass did, reported as it happens. Kind is created, adopted, updated, moved, scheduled, held,
// deferred, skipped or failed. Detail is the statuses a move passed through.
type Event struct {
	Kind, Unit, Key, Message, Detail string
}

// Deps is what a pass needs.
type Deps struct {
	Units     units.Service
	DB        *db.DB
	System    taskmanager.System
	Settings  taskmanager.Settings
	ProjectID string
	// Dir is the ralph directory that holds the sync lock.
	Dir    string
	Now    func() time.Time
	Report func(Event)
}

func (d Deps) now() time.Time {
	if d.Now == nil {
		return time.Now()
	}
	return d.Now()
}

func (d Deps) emit(e Event) {
	if d.Report != nil {
		d.Report(e)
	}
}

// Desired is the ticket a unit should have.
type Desired struct {
	Type, Title, Body, Parent, Ref string
	// State is the unit's derived status or task state; Status is the ticket status it maps to, empty when none.
	State, Status string
}

const criteriaHeading = "## Acceptance criteria"

// Ref is the reference a ticket is created with, which Find looks up.
func Ref(projectID, unitID string) string { return "ralph-" + projectID + "-" + unitID }

// Build works out the ticket of u. byID holds the rows known so far, by unit ID.
func Build(ctx context.Context, d Deps, u units.Unit, byID map[string]Row) (Desired, error) {
	state, err := d.Units.Status(ctx, u.ID)
	if err != nil {
		return Desired{}, fmt.Errorf("tickets: status of %s: %w", u.ID, err)
	}
	return build(ctx, d, u, byID, string(state))
}

func build(ctx context.Context, d Deps, u units.Unit, byID map[string]Row, state string) (Desired, error) {
	parent, err := parentKey(ctx, d, u, byID)
	if err != nil {
		return Desired{}, err
	}
	want := content(d, u, state)
	want.Parent = parent
	return want, nil
}

// content is the ticket of u without its parent.
func content(d Deps, u units.Unit, state string) Desired {
	body := u.Body
	if u.AcceptanceCriteria != "" {
		body += "\n\n" + criteriaHeading + "\n\n" + u.AcceptanceCriteria
	}
	status, _ := StatusFor(d.Settings, state)
	return Desired{
		Type:   d.Settings.Types[string(u.Level)],
		Title:  u.Title,
		Body:   body,
		Ref:    Ref(d.ProjectID, u.ID),
		State:  state,
		Status: status,
	}
}

func parentKey(ctx context.Context, d Deps, u units.Unit, byID map[string]Row) (string, error) {
	for id := u.ParentID; id != ""; {
		anc, err := d.Units.Get(ctx, id)
		if err != nil {
			return "", fmt.Errorf("tickets: look up %s: %w", id, err)
		}
		if d.Settings.Mapped(string(anc.Level)) {
			if row, ok := byID[anc.ID]; ok && row.Key != "" {
				return row.Key, nil
			}
			return "", &ParentWaitingError{Parent: anc.ID}
		}
		id = anc.ParentID
	}
	return "", nil
}
