package quick

import (
	"errors"
	"fmt"
	"strings"

	"github.com/bengearig/ralph/internal/units"
)

const maxTitle = 200

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", units.ErrInvalid, fmt.Sprintf(format, args...))
}

func validateTitle(raw string) (string, error) {
	title := strings.TrimSpace(raw)
	switch {
	case title == "":
		return "", invalid("the title is empty")
	case strings.ContainsAny(title, "\r\n"):
		return "", invalid("the title must be one line")
	case len([]rune(title)) > maxTitle:
		return "", invalid("the title has %d characters; the most allowed is %d", len([]rune(title)), maxTitle)
	}
	return title, nil
}

func afterBranchError(branch string, cause, cancelErr error) error {
	err := fmt.Errorf("quick work: %w; the branch %s was created and was left in place", cause, branch)
	if cancelErr != nil {
		return errors.Join(err, fmt.Errorf("quick work: cancel the task: %w", cancelErr))
	}
	return err
}
