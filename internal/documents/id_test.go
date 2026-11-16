package documents

import (
	"errors"
	"testing"
)

func TestParseID(t *testing.T) {
	for _, id := range []string{"D-1", "D-42", "D-007"} {
		if err := parseID(id); err != nil {
			t.Errorf("%q: %v", id, err)
		}
	}
	for _, id := range []string{"d-1", "D1", "D-", "D-x", "D-1x", "D--1", "D-+1", "T-1", ""} {
		if err := parseID(id); !errors.Is(err, ErrInvalid) {
			t.Errorf("%q: err = %v", id, err)
		}
	}
	if got := formatID(12); got != "D-12" {
		t.Errorf("formatID = %q", got)
	}
}
