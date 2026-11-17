package taskmanager

import (
	"errors"
	"time"
)

var (
	ErrNotFound     = errors.New("not found")
	ErrUnauthorized = errors.New("unauthorized")
	ErrInvalid      = errors.New("invalid request")
	ErrUnavailable  = errors.New("task manager unavailable")
	ErrRateLimited  = errors.New("rate limited")
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

// CredentialError reports credentials a system could not use or refused,
// naming the setting that holds them.
type CredentialError struct {
	Setting string
	Reason  string
}

func (e *CredentialError) Error() string { return ErrUnauthorized.Error() + ": " + e.Reason }

// Is makes errors.Is(err, ErrUnauthorized) true.
func (e *CredentialError) Is(target error) bool { return target == ErrUnauthorized }

// NewCredentialError builds a CredentialError for the named setting.
func NewCredentialError(setting, msg string) *CredentialError {
	return &CredentialError{Setting: setting, Reason: msg}
}

// CredentialSetting returns the setting named by a CredentialError anywhere in
// err's chain.
func CredentialSetting(err error) (setting string, ok bool) {
	var ce *CredentialError
	if errors.As(err, &ce) {
		return ce.Setting, true
	}
	return "", false
}
