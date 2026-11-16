package hosting

import (
	"errors"
	"time"
)

var (
	ErrNotFound     = errors.New("not found")
	ErrUnauthorized = errors.New("unauthorized")
	ErrConflict     = errors.New("conflict")
	ErrInvalid      = errors.New("invalid request")
	ErrUnavailable  = errors.New("provider unavailable")
	ErrRateLimited  = errors.New("rate limited")
	// ErrNoRemote means the git remote asked for does not exist.
	ErrNoRemote = errors.New("no such git remote")
)

// RateLimitError reports a rate limit and when it resets.
type RateLimitError struct {
	ResetAt time.Time
}

func (e *RateLimitError) Error() string {
	if e.ResetAt.IsZero() {
		return ErrRateLimited.Error()
	}
	return ErrRateLimited.Error() + ", resets at " + e.ResetAt.UTC().Format(time.RFC3339)
}

// Is makes errors.Is(err, ErrRateLimited) true.
func (e *RateLimitError) Is(target error) bool { return target == ErrRateLimited }
