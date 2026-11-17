package issuetest

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/bengearig/ralph/internal/issuesource"
	"github.com/bengearig/ralph/internal/taskmanager"
)

func TestFakeConformance(t *testing.T) {
	f := NewFake("", "example.atlassian.net")
	Run(t, Subject{
		Source: f,
		Seed:   func(i issuesource.Issue) error { f.Add(i); return nil },
		Controls: Controls{
			RejectCredentials: func() { f.FailWith(taskmanager.NewCredentialError("jira.token", "rejected")) },
			RateLimit:         func() { f.FailWith(&taskmanager.RateLimitError{}) },
			Unreachable:       func() { f.FailWith(issuesource.ErrUnavailable) },
			Heal:              func() { f.FailWith(nil) },
		},
		Accepted: map[string]string{"abc-1": "ABC-1", "https://example.atlassian.net/browse/ABC-1": "ABC-1"},
		Rejected: []string{"", "abc", "https://other.example/browse/ABC-1"},
		Accepts:  "use a key such as ABC-123",
	})
}

func TestFakeParseKeyAndURL(t *testing.T) {
	f := NewFake("tracker", "Example.test")
	for in, want := range map[string]string{
		"a_b1-7":                          "A_B1-7",
		" ABC-123 ":                       "ABC-123",
		"https://example.test/browse/x-2": "X-2",
	} {
		got, err := f.Parse(in)
		if err != nil || got != (issuesource.Ref{System: "tracker", Key: want}) {
			t.Errorf("Parse(%q) = %v, %v", in, got, err)
		}
	}
	for _, in := range []string{"1-2", "ABC-", "ABC-0x", "http://example.test/browse/ABC-1", "https://other.test/browse/ABC-1", "https://example.test/ABC-1"} {
		if _, err := f.Parse(in); !errors.Is(err, issuesource.ErrInvalid) {
			t.Errorf("Parse(%q) = %v", in, err)
		}
	}
}

func TestFakeCallsRecorded(t *testing.T) {
	f := NewFake("", "example.test")
	f.Add(issuesource.Issue{Ref: issuesource.Ref{Key: "abc-1"}, Title: "T"})
	ref := issuesource.Ref{System: "jira", Key: "ABC-1"}
	if _, err := f.Get(context.Background(), ref); err != nil {
		t.Fatal(err)
	}
	f.Remove("abc-1")
	if _, err := f.Get(context.Background(), ref); !errors.Is(err, issuesource.ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
	if got := f.Calls(); !slices.Equal(got, []string{"jira:ABC-1", "jira:ABC-1"}) {
		t.Errorf("Calls = %v", got)
	}
}
