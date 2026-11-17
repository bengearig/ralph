package taskmanager

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestErrorsIs(t *testing.T) {
	for _, e := range []error{ErrNotFound, ErrUnauthorized, ErrInvalid, ErrRateLimited, ErrUnavailable} {
		wrapped := fmt.Errorf("while doing it: %w", e)
		if !errors.Is(wrapped, e) {
			t.Errorf("errors.Is(%v) is false", e)
		}
		for _, other := range []error{ErrNotFound, ErrUnauthorized, ErrInvalid, ErrRateLimited, ErrUnavailable} {
			if other != e && errors.Is(wrapped, other) {
				t.Errorf("%v matches %v", e, other)
			}
		}
	}
}

func TestCredentialErrorNamesSetting(t *testing.T) {
	err := fmt.Errorf("check: %w", NewCredentialError("jira.token", "the token was refused"))
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("err = %v", err)
	}
	if s, ok := CredentialSetting(err); !ok || s != "jira.token" {
		t.Errorf("CredentialSetting = %q, %v", s, ok)
	}
	if _, ok := CredentialSetting(ErrNotFound); ok {
		t.Error("CredentialSetting found a setting in ErrNotFound")
	}
}

func TestRateLimitErrorCarriesReset(t *testing.T) {
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	err := fmt.Errorf("get: %w", &RateLimitError{ResetAt: at})
	var rl *RateLimitError
	if !errors.Is(err, ErrRateLimited) || !errors.As(err, &rl) || !rl.ResetAt.Equal(at) {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(err.Error(), "2026-01-02T03:04:05Z") {
		t.Errorf("message %q lacks the reset time", err)
	}
	if got := (&RateLimitError{}).Error(); got != ErrRateLimited.Error() {
		t.Errorf("zero reset message = %q", got)
	}
}

func TestErrorsNeverContainToken(t *testing.T) {
	const token = "s3cr3t-token-value"
	for _, err := range []error{
		NewCredentialError("jira.token", "the token was refused"),
		&RateLimitError{ResetAt: time.Now()},
		fmt.Errorf("check: %w", ErrUnauthorized),
	} {
		if strings.Contains(err.Error(), token) {
			t.Errorf("%v contains the token", err)
		}
	}
}
