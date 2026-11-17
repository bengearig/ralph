package issuetest

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bengearig/ralph/internal/issuesource"
	"github.com/bengearig/ralph/internal/taskmanager"
)

// Controls make the system under test fail; Heal undoes them.
type Controls struct {
	RejectCredentials func()
	RateLimit         func()
	Unreachable       func()
	Heal              func()
}

// Subject is a source and the means to exercise it.
type Subject struct {
	Source issuesource.Source
	// Seed makes the system hold i, whose Ref.Key, Title, Body (Markdown),
	// Type and Status are set.
	Seed     func(i issuesource.Issue) error
	Controls Controls
	// Accepted maps what Parse must accept to the key it must give.
	Accepted map[string]string
	// Rejected is what Parse must refuse.
	Rejected []string
	// Accepts is a phrase every Parse error must contain.
	Accepts string
}

// Run checks that s behaves as a source must.
func Run(t *testing.T, s Subject) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	src := s.Source

	t.Run("ParseAccepts", func(t *testing.T) {
		for in, key := range s.Accepted {
			got, err := src.Parse(in)
			if err != nil {
				t.Errorf("Parse(%q): %v", in, err)
				continue
			}
			if got.System != src.Name() || got.Key != key {
				t.Errorf("Parse(%q) = %v, want %s:%s", in, got, src.Name(), key)
			}
		}
	})
	t.Run("ParseRejects", func(t *testing.T) {
		for _, in := range s.Rejected {
			_, err := src.Parse(in)
			if !errors.Is(err, issuesource.ErrInvalid) {
				t.Errorf("Parse(%q) = %v, want ErrInvalid", in, err)
				continue
			}
			if !strings.Contains(err.Error(), s.Accepts) {
				t.Errorf("Parse(%q) error %q does not say what is accepted (%q)", in, err, s.Accepts)
			}
		}
	})

	seeded := issuesource.Issue{
		Ref:    issuesource.Ref{System: src.Name(), Key: "CONF-1"},
		Title:  "Conformance ticket",
		Body:   "First paragraph.\n\n- one\n- two",
		Type:   "Task",
		Status: "To Do",
	}
	if err := s.Seed(seeded); err != nil {
		t.Fatalf("Seed: %v", err)
	}
	t.Run("Get", func(t *testing.T) {
		got, err := src.Get(ctx, seeded.Ref)
		if err != nil {
			t.Fatal(err)
		}
		if got.Ref != seeded.Ref || got.Title != seeded.Title {
			t.Errorf("Get = %+v", got)
		}
		if strings.TrimSpace(got.Body) != seeded.Body {
			t.Errorf("Body = %q, want %q", got.Body, seeded.Body)
		}
		if got.URL == "" || !strings.Contains(got.URL, seeded.Ref.Key) {
			t.Errorf("URL = %q", got.URL)
		}
	})
	t.Run("NotFound", func(t *testing.T) {
		_, err := src.Get(ctx, issuesource.Ref{System: src.Name(), Key: "CONF-9999"})
		if !errors.Is(err, issuesource.ErrNotFound) {
			t.Errorf("err = %v, want ErrNotFound", err)
		}
	})
	failures := []struct {
		name    string
		control func()
		check   func(error) bool
	}{
		{"Unauthorized", s.Controls.RejectCredentials, func(e error) bool {
			_, ok := taskmanager.CredentialSetting(e)
			return errors.Is(e, issuesource.ErrUnauthorized) && ok
		}},
		{"RateLimited", s.Controls.RateLimit, func(e error) bool { return errors.Is(e, issuesource.ErrRateLimited) }},
		{"Unavailable", s.Controls.Unreachable, func(e error) bool { return errors.Is(e, issuesource.ErrUnavailable) }},
	}
	for _, f := range failures {
		t.Run(f.name, func(t *testing.T) {
			if f.control == nil {
				t.Skip("no control")
			}
			f.control()
			defer s.Controls.Heal()
			if _, err := src.Get(ctx, seeded.Ref); err == nil || !f.check(err) {
				t.Errorf("err = %v", err)
			}
		})
	}
}
