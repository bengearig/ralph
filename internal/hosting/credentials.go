package hosting

import (
	"errors"
	"strings"
)

// CredentialError reports credentials a provider could not use or that its
// host refused, naming the setting that holds them.
type CredentialError struct {
	Setting string
	Reason  string
}

func (e *CredentialError) Error() string { return ErrUnauthorized.Error() + ": " + e.Reason }

// Is makes errors.Is(err, ErrUnauthorized) true.
func (e *CredentialError) Is(target error) bool { return target == ErrUnauthorized }

// NewCredentialError builds a CredentialError for the named setting.
func NewCredentialError(setting, reason string) *CredentialError {
	return &CredentialError{Setting: setting, Reason: reason}
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

// ProviderForHost names the provider that serves a well-known host, or returns
// an empty string.
func ProviderForHost(host string) string {
	switch strings.ToLower(host) {
	case "github.com", "www.github.com":
		return "github"
	case "bitbucket.org", "www.bitbucket.org":
		return "bitbucket"
	}
	return ""
}
