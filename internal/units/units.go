// Package units stores the work records: briefs, slices and tasks.
//
// Service is the interface callers depend on. Every implementation, local or
// remote, must return the package's sentinel errors (ErrNotFound, ErrInvalid,
// ErrInvalidParent and ErrInvalidTransition) and db.ErrStale unchanged, so that
// errors.Is gives the same answer either way.
package units

import (
	"strconv"
	"time"

	"github.com/bengearig/ralph/internal/db"
)

type Level string

const (
	LevelBrief Level = "brief"
	LevelSlice Level = "slice"
	LevelTask  Level = "task"
)

func (l Level) valid() bool {
	return l == LevelBrief || l == LevelSlice || l == LevelTask
}

func (l Level) prefix() string {
	switch l {
	case LevelBrief:
		return "B"
	case LevelSlice:
		return "S"
	default:
		return "T"
	}
}

// State is stored on tasks only.
type State string

const (
	StateProposed   State = "PROPOSED"
	StateTodo       State = "TODO"
	StateInProgress State = "IN_PROGRESS"
	StateBlocked    State = "BLOCKED"
	StateCancelled  State = "CANCELLED"
	StateDone       State = "DONE"
)

func (s State) valid() bool {
	switch s {
	case StateProposed, StateTodo, StateInProgress, StateBlocked, StateCancelled, StateDone:
		return true
	}
	return false
}

// Unit is one stored record. ParentID is empty for none and State is empty
// for briefs and slices.
type Unit struct {
	ID                 string
	Level              Level
	Number             int
	ParentID           string
	Position           int
	Title              string
	Body               string
	AcceptanceCriteria string
	State              State
	CreatedAt          time.Time
	UpdatedAt          time.Time
	CreatedBy          string
	UpdatedBy          string
	Fingerprint        string
}

// NewUnit describes a unit to create. A zero Position appends after the last sibling.
type NewUnit struct {
	Level              Level
	ParentID           string
	Title              string
	Body               string
	AcceptanceCriteria string
	Position           int
}

// Filter narrows List. A nil ParentID matches any parent and a pointer to the
// empty string matches units with no parent.
type Filter struct {
	Level    Level
	ParentID *string
	States   []State
}

func fingerprint(u Unit) string {
	return db.Fingerprint(u.ID, string(u.Level), u.ParentID, strconv.Itoa(u.Position),
		u.Title, u.Body, u.AcceptanceCriteria, string(u.State))
}
