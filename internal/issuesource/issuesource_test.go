package issuesource

import (
	"errors"
	"fmt"
	"testing"

	"github.com/bengearig/ralph/internal/config"
	"github.com/bengearig/ralph/internal/taskmanager"
)

func TestRefString(t *testing.T) {
	if got := (Ref{System: "jira", Key: "ABC-123"}).String(); got != "jira:ABC-123" {
		t.Errorf("String = %q", got)
	}
}

func TestInstructionWithAndWithoutBody(t *testing.T) {
	i := Issue{Ref: Ref{System: "jira", Key: "ABC-123"}, Title: "Fix the login redirect"}
	if got, want := Instruction(i), "Jira ABC-123: Fix the login redirect"; got != want {
		t.Errorf("no body: %q, want %q", got, want)
	}
	i.Body = " \n\t"
	if got, want := Instruction(i), "Jira ABC-123: Fix the login redirect"; got != want {
		t.Errorf("blank body: %q, want %q", got, want)
	}
	i.Body = "It loops.\n\n- step one"
	if got, want := Instruction(i), "Jira ABC-123: Fix the login redirect\n\nIt loops.\n\n- step one"; got != want {
		t.Errorf("body: %q, want %q", got, want)
	}
}

func TestFromConfigDefault(t *testing.T) {
	cfg, err := config.Load(config.Options{
		ProjectRoot: t.TempDir(),
		Getenv:      func(string) (string, bool) { return "", false },
	})
	if err != nil {
		t.Fatal(err)
	}
	s, err := FromConfig(cfg)
	if err != nil || s.Provider != "none" {
		t.Fatalf("FromConfig = %+v, %v", s, err)
	}
}

func TestErrorsAliasTaskManager(t *testing.T) {
	pairs := []struct{ ours, theirs error }{
		{ErrNotFound, taskmanager.ErrNotFound},
		{ErrUnauthorized, taskmanager.ErrUnauthorized},
		{ErrInvalid, taskmanager.ErrInvalid},
		{ErrUnavailable, taskmanager.ErrUnavailable},
		{ErrRateLimited, taskmanager.ErrRateLimited},
	}
	for _, p := range pairs {
		if !errors.Is(fmt.Errorf("x: %w", p.theirs), p.ours) || !errors.Is(fmt.Errorf("x: %w", p.ours), p.theirs) {
			t.Errorf("%v does not match across packages", p.ours)
		}
	}
	if !errors.Is(&taskmanager.RateLimitError{}, ErrRateLimited) {
		t.Error("RateLimitError is not ErrRateLimited")
	}
	if !errors.Is(taskmanager.NewCredentialError("jira.token", "no"), ErrUnauthorized) {
		t.Error("CredentialError is not ErrUnauthorized")
	}
}
