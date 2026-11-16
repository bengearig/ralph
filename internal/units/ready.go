package units

import (
	"errors"
	"fmt"
	"strings"
)

var ErrNotReady = errors.New("not ready")

const (
	missingDescription = "description"
	missingCriteria    = "acceptance criteria"
)

// NotReadyError reports a task that lacks what TODO requires. Editing is set
// when an edit, not a promotion, would leave the task in that shape.
type NotReadyError struct {
	ID      string
	Missing []string
	Editing bool
}

func (e *NotReadyError) Error() string {
	items := make([]string, len(e.Missing))
	for i, m := range e.Missing {
		items[i] = m
		if m == missingDescription {
			items[i] = "a description"
		}
	}
	joined := strings.Join(items, " and ")
	if e.Editing {
		return fmt.Sprintf("not ready: this change would leave %s without %s; move it back to PROPOSED to rewrite it", e.ID, joined)
	}
	return fmt.Sprintf("not ready: %s needs %s before it can be TODO", e.ID, joined)
}

func (e *NotReadyError) Is(target error) bool { return target == ErrNotReady }

// Readiness lists what a task still lacks before it can be TODO. Acceptance
// criteria are required only of a task in a slice.
func Readiness(u Unit) []string {
	var missing []string
	if strings.TrimSpace(u.Body) == "" {
		missing = append(missing, missingDescription)
	}
	if u.ParentID != "" && strings.TrimSpace(u.AcceptanceCriteria) == "" {
		missing = append(missing, missingCriteria)
	}
	return missing
}
