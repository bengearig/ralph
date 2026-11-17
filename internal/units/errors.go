package units

import (
	"errors"
	"fmt"
	"strings"

	"github.com/bengearig/ralph/internal/db"
)

var (
	ErrNotFound      = db.ErrNotFound
	ErrInvalid       = errors.New("invalid input")
	ErrInvalidParent = errors.New("invalid parent")
)

var ErrInvalidTransition = errors.New("invalid transition")

var ErrReasonRequired = errors.New("reason required")

// ReasonRequiredError reports a move that needs a reason and was given none.
type ReasonRequiredError struct {
	ID       string
	From, To State
}

func (e *ReasonRequiredError) Error() string {
	return fmt.Sprintf("a reason is required to move %s from %s to %s", e.ID, e.From, e.To)
}

func (e *ReasonRequiredError) Is(target error) bool { return target == ErrReasonRequired }

var ErrWaiting = errors.New("slice waiting")

// WaitingError reports a task that cannot start because its slice waits for
// other slices that are not satisfied.
type WaitingError struct {
	ID      string
	Slice   string
	Waiting []Dependency
}

func (e *WaitingError) Error() string {
	names := make([]string, len(e.Waiting))
	for i, d := range e.Waiting {
		names[i] = fmt.Sprintf("%s (%s)", d.ID, d.Status)
	}
	return fmt.Sprintf("%s cannot start: slice %s waits for %s; finish them or remove the dependency", e.ID, e.Slice, strings.Join(names, ", "))
}

func (e *WaitingError) Is(target error) bool { return target == ErrWaiting }
