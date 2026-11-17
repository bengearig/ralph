// Package taskmanager defines the role of an external system that holds a
// ticket for each unit of work, and the settings that choose and configure it.
package taskmanager

import "context"

// Ticket is what a system holds for one unit of work.
type Ticket struct {
	// Key is the ticket's identity in the system; ID is the system's own
	// stable identifier, which survives a change of key.
	Key    string
	ID     string
	Type   string
	Title  string
	Body   string // Markdown
	Status string
	Parent string // key of the parent ticket, empty when none
	// Ref is the reference the ticket was created with.
	Ref string
}

// New is a ticket to create. Parent is empty for a ticket with no parent.
type New struct {
	Type, Title, Body, Parent, Ref string
}

// Fields are the values of a ticket that can be changed.
type Fields struct {
	Title, Body string
}

// Account is the identity the credentials act as.
type Account struct {
	Name, Email string
}

// Move reports a change of status: where the ticket was, where it is, and the
// statuses it passed through on the way.
type Move struct {
	From, To string
	Via      []string
}

// System is a task manager.
type System interface {
	Name() string
	// Check verifies that the credentials and the configured project work.
	Check(ctx context.Context) (Account, error)
	// Types lists the ticket types the project accepts.
	Types(ctx context.Context) ([]string, error)
	// Find returns the ticket created with ref, or nil and no error when none.
	Find(ctx context.Context, ref string) (*Ticket, error)
	// Create makes a ticket in the system's initial status, carrying n.Ref,
	// and returns the ticket as the system holds it.
	Create(ctx context.Context, n New) (Ticket, error)
	Update(ctx context.Context, key string, f Fields) error
	// Get returns the tickets for keys; a key the system no longer has is
	// absent from the map.
	Get(ctx context.Context, keys []string) (map[string]Ticket, error)
	// Move puts the ticket in status. It does nothing when the ticket is
	// there, uses a direct transition when one exists, and otherwise steps
	// through the statuses of flow between the two. Anything else is an
	// error naming the statuses reachable from the current one.
	Move(ctx context.Context, key, status string, flow []string) (Move, error)
	// Schedule puts the ticket in the named plan; a system may call a plan
	// a sprint, an iteration or a cycle. An unknown plan is ErrNotFound.
	Schedule(ctx context.Context, key, plan string) error
	// Normalize returns what the system would hold if given f.
	Normalize(f Fields) Fields
}
