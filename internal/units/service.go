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
	// UnitsLinkedTo returns the units that have the link, ordered by level
	// (brief, slice, task) and then number.
	UnitsLinkedTo(ctx context.Context, kind LinkKind, target string) ([]Unit, error)
	// AddNote records a note on a unit of any level.
	AddNote(ctx context.Context, unitID string, kind NoteKind, body string) (Note, error)
	// Notes returns a unit's notes.
	Notes(ctx context.Context, unitID string) ([]Note, error)
	// AddDependency makes a slice wait for another; adding one that exists changes nothing.
	AddDependency(ctx context.Context, sliceID, dependsOnID string) error
	// RemoveDependency removes a dependency; removing a missing one is not an error.
	RemoveDependency(ctx context.Context, sliceID, dependsOnID string) error
	// Dependencies returns the slices a slice waits for, ordered by ID number.
	Dependencies(ctx context.Context, sliceID string) ([]Dependency, error)
	// Waiting returns the dependencies of a slice that are not DONE.
	Waiting(ctx context.Context, sliceID string) ([]Dependency, error)
	// Status returns a task's state, or the status derived from a slice's or brief's children.
	Status(ctx context.Context, id string) (Status, error)
	// Statuses returns the status of each given unit in one read.
	Statuses(ctx context.Context, ids []string) (map[string]Status, error)
	// RecordPullRequest inserts a pull request of a slice, or updates the URL,
	// branches and state of the one with the same number.
	RecordPullRequest(ctx context.Context, rec PullRequestRecord) error
	// PullRequests returns a slice's pull requests, oldest first.
	PullRequests(ctx context.Context, sliceID string) ([]PullRequestRecord, error)
}

var _ Service = (*Store)(nil)

// NewService returns the local implementation, for callers that should
// depend on the interface only.
func NewService(d *db.DB) Service {
	return New(d)
}
