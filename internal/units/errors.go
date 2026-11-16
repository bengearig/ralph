package units

import (
	"errors"
	"fmt"

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
