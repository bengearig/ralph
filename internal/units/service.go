package units

import (
	"context"

	"github.com/bengearig/ralph/internal/db"
)

// Service is the unit layer as callers see it. AllowedNext is a package
// function and is not part of it.
type Service interface {
	// Create adds a unit; a task starts in PROPOSED.
	Create(ctx context.Context, n NewUnit) (Unit, error)
	// Get returns one unit by ID.
	Get(ctx context.Context, id string) (Unit, error)
	// List returns the units matching a filter.
	List(ctx context.Context, f Filter) ([]Unit, error)
	// Update applies a change to a unit if its fingerprint is still expected.
	Update(ctx context.Context, id, expected string, c Change) (Unit, error)
	// SetState moves a task to a new state if its fingerprint is still expected.
	SetState(ctx context.Context, id, expected string, to State, reason string) (Unit, error)
	// AddLink adds a link to a unit; adding one that exists changes nothing.
	AddLink(ctx context.Context, unitID string, kind LinkKind, target string) error
	// RemoveLink removes a link from a unit; removing a missing one is not an error.
	RemoveLink(ctx context.Context, unitID string, kind LinkKind, target string) error
	// Links returns a unit's links ordered by kind, then target.
	Links(ctx context.Context, unitID string) ([]Link, error)
	// AddNote records a note on a unit of any level.
	AddNote(ctx context.Context, unitID string, kind NoteKind, body string) (Note, error)
	// Notes returns a unit's notes.
	Notes(ctx context.Context, unitID string) ([]Note, error)
	// Status returns a task's state, or the status derived from a slice's or brief's children.
	Status(ctx context.Context, id string) (Status, error)
	// Statuses returns the status of each given unit in one read.
	Statuses(ctx context.Context, ids []string) (map[string]Status, error)
}

var _ Service = (*Store)(nil)

// NewService returns the local implementation, for callers that should
// depend on the interface only.
func NewService(d *db.DB) Service {
	return New(d)
}
