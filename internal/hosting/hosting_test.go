package hosting

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/bengearig/ralph/internal/testutil"
)

func TestParseRemoteForms(t *testing.T) {
	cases := []struct {
		name, in string
		want     Remote
	}{
		{"https", "https://github.com/acme/widgets", Remote{"github.com", "acme", "widgets"}},
		{"https git", "https://github.com/acme/widgets.git", Remote{"github.com", "acme", "widgets"}},
		{"https slash", "https://github.com/acme/widgets/", Remote{"github.com", "acme", "widgets"}},
		{"https git slash", "https://github.com/acme/widgets.git/", Remote{"github.com", "acme", "widgets"}},
		{"http", "http://example.com/acme/widgets.git", Remote{"example.com", "acme", "widgets"}},
		{"ssh port", "ssh://git@github.com:2222/acme/widgets.git", Remote{"github.com", "acme", "widgets"}},
		{"ssh", "ssh://git@github.com/acme/widgets", Remote{"github.com", "acme", "widgets"}},
		{"scp", "git@github.com:acme/widgets.git", Remote{"github.com", "acme", "widgets"}},
		{"scp no git", "git@github.com:acme/widgets", Remote{"github.com", "acme", "widgets"}},
		{"git", "git://github.com/acme/widgets.git", Remote{"github.com", "acme", "widgets"}},
		{"uppercase host", "https://GitHub.COM/Acme/Widgets", Remote{"github.com", "Acme", "Widgets"}},
		{"credentials", "https://user:secret@github.com/acme/widgets.git", Remote{"github.com", "acme", "widgets"}},
		{"three segments", "https://gitlab.com/group/sub/widgets.git", Remote{"gitlab.com", "group/sub", "widgets"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ParseRemote(c.in)
			if err != nil || got != c.want {
				t.Fatalf("ParseRemote(%q) = %+v, %v; want %+v", c.in, got, err, c.want)
			}
		})
	}
}

func TestParseRemoteStripsCredentials(t *testing.T) {
	got, err := ParseRemote("https://user:secret@github.com/acme/widgets")
	if err != nil || strings.Contains(fmt.Sprintf("%+v", got), "secret") {
		t.Fatalf("got %+v, %v", got, err)
	}
	for _, in := range []string{
		"https://user:secret@github.com/acme",
		"https://user:secret@/acme/widgets",
		"https://user:secret@github.com:bad/acme/widgets",
		"ftp://user:secret@github.com/acme/widgets",
		"https://user:secret@github.com/",
		"https://user:secret@%zz/acme/widgets",
	} {
		_, err := ParseRemote(in)
		if err == nil {
			t.Errorf("ParseRemote(%q): expected error", in)
			continue
		}
		if strings.Contains(err.Error(), "secret") {
			t.Errorf("error leaks credentials: %v", err)
		}
	}
}

func TestParseRemoteInvalid(t *testing.T) {
	for _, in := range []string{"", "   ", "https://github.com", "https://github.com/", "https://github.com/acme", "git@github.com:acme", "nonsense"} {
		if _, err := ParseRemote(in); err == nil {
			t.Errorf("ParseRemote(%q): expected error", in)
		}
	}
}

func TestDetectRemote(t *testing.T) {
	r := testutil.NewRepo(t)
	r.Git("remote", "add", "origin", "git@github.com:acme/widgets.git")
	got, err := DetectRemote(context.Background(), r.Dir, "origin")
	if err != nil || got != (Remote{"github.com", "acme", "widgets"}) {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestDetectRemoteMissing(t *testing.T) {
	r := testutil.NewRepo(t)
	_, err := DetectRemote(context.Background(), r.Dir, "upstream")
	if !errors.Is(err, ErrNoRemote) || !strings.Contains(err.Error(), "upstream") {
		t.Fatalf("err = %v", err)
	}
}

func TestBaseBranchConfigured(t *testing.T) {
	r := testutil.NewRepo(t)
	if got := BaseBranch(context.Background(), r.Dir, "origin", "develop"); got != "develop" {
		t.Fatalf("got %q", got)
	}
}

func TestBaseBranchFromRemoteHead(t *testing.T) {
	r := testutil.NewRepo(t)
	r.Git("update-ref", "refs/remotes/origin/trunk", "HEAD")
	r.Git("symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/trunk")
	if got := BaseBranch(context.Background(), r.Dir, "origin", ""); got != "trunk" {
		t.Fatalf("got %q", got)
	}
}

func TestBaseBranchFallsBackToMain(t *testing.T) {
	r := testutil.NewRepo(t)
	if got := BaseBranch(context.Background(), r.Dir, "origin", ""); got != "main" {
		t.Fatalf("got %q", got)
	}
}

func TestErrorsWrap(t *testing.T) {
	for _, s := range []error{ErrNotFound, ErrUnauthorized, ErrConflict, ErrInvalid, ErrUnavailable, ErrRateLimited, ErrNoRemote} {
		err := fmt.Errorf("context: %w", s)
		if !errors.Is(err, s) {
			t.Errorf("errors.Is(%v) = false", s)
		}
	}
}

func TestRateLimitError(t *testing.T) {
	reset := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	err := fmt.Errorf("listing: %w", &RateLimitError{ResetAt: reset})
	if !errors.Is(err, ErrRateLimited) {
		t.Fatal("not ErrRateLimited")
	}
	var rl *RateLimitError
	if !errors.As(err, &rl) || !rl.ResetAt.Equal(reset) {
		t.Fatalf("as = %+v", rl)
	}
	if errors.Is(err, ErrNotFound) {
		t.Fatal("matches unrelated sentinel")
	}
}

func TestErrorsNeverContainToken(t *testing.T) {
	const token = "ghp_supersecret"
	errs := []error{ErrNotFound, ErrUnauthorized, ErrConflict, ErrInvalid, ErrUnavailable, ErrRateLimited, ErrNoRemote, &RateLimitError{ResetAt: time.Now()}, &RateLimitError{}}
	for _, err := range errs {
		if strings.Contains(err.Error(), token) || strings.Contains(err.Error(), "ghp_") {
			t.Errorf("error %q contains a token", err)
		}
	}
}
