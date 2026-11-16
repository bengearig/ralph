package hosting

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestCredentialErrorIsUnauthorized(t *testing.T) {
	err := error(&CredentialError{Setting: "x.token", Reason: "bad"})
	if !errors.Is(err, ErrUnauthorized) || errors.Is(err, ErrNotFound) {
		t.Fatalf("errors.Is wrong for %v", err)
	}
	if !strings.HasPrefix(err.Error(), "unauthorized: ") || err.Error() != "unauthorized: bad" {
		t.Fatalf("message = %q", err)
	}
}

func TestCredentialErrorThroughWrapping(t *testing.T) {
	err := fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", NewCredentialError("x.token", "bad")))
	if s, ok := CredentialSetting(err); !ok || s != "x.token" {
		t.Fatalf("setting = %q, %v", s, ok)
	}
	if s, ok := CredentialSetting(fmt.Errorf("w: %w", ErrUnauthorized)); ok || s != "" {
		t.Fatalf("plain ErrUnauthorized gave %q, %v", s, ok)
	}
}

func TestNewCredentialError(t *testing.T) {
	e := NewCredentialError("a.b", "why")
	if e.Setting != "a.b" || e.Reason != "why" {
		t.Fatalf("%+v", e)
	}
}

func TestProviderForHost(t *testing.T) {
	for host, want := range map[string]string{
		"github.com": "github", "GitHub.com": "github", "www.github.com": "github",
		"bitbucket.org": "bitbucket", "www.bitbucket.org": "bitbucket",
		"gitlab.com": "", "": "", "example.com": "",
	} {
		if got := ProviderForHost(host); got != want {
			t.Errorf("ProviderForHost(%q) = %q, want %q", host, got, want)
		}
	}
}
