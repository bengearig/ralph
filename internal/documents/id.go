package documents

import (
	"fmt"
	"strconv"
	"strings"
)

const idPrefix = "D-"

func formatID(number int) string {
	return idPrefix + strconv.Itoa(number)
}

// parseID checks that id is "D-" followed by digits only.
func parseID(id string) error {
	digits, ok := strings.CutPrefix(id, idPrefix)
	if !ok || digits == "" {
		return fmt.Errorf("%w: document ID %q must be D- followed by a number", ErrInvalid, id)
	}
	for _, r := range digits {
		if r < '0' || r > '9' {
			return fmt.Errorf("%w: document ID %q must be D- followed by a number", ErrInvalid, id)
		}
	}
	return nil
}
