package identity_test

import (
	"context"
	"os"
	"os/user"
	"testing"

	"github.com/bengearig/ralph/internal/identity"
	"github.com/bengearig/ralph/internal/testutil"
)

func isolateGit(t *testing.T) {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
}

func osUser(t *testing.T) string {
	t.Helper()
	u, err := user.Current()
	if err != nil {
		t.Skipf("no current user: %v", err)
	}
	return u.Username
}

func TestResolveNameAndEmail(t *testing.T) {
	isolateGit(t)
	r := testutil.NewRepo(t)
	if got, want := identity.Resolve(context.Background(), r.Dir), "Test User <test@example.com>"; got != want {
		t.Errorf("Resolve = %q, want %q", got, want)
	}
}

func TestResolveNameOnly(t *testing.T) {
	isolateGit(t)
	r := testutil.NewRepo(t)
	r.Git("config", "--unset", "user.email")
	if got, want := identity.Resolve(context.Background(), r.Dir), "Test User"; got != want {
		t.Errorf("Resolve = %q, want %q", got, want)
	}
}

func TestResolveEmailOnly(t *testing.T) {
	isolateGit(t)
	r := testutil.NewRepo(t)
	r.Git("config", "--unset", "user.name")
	if got, want := identity.Resolve(context.Background(), r.Dir), "test@example.com"; got != want {
		t.Errorf("Resolve = %q, want %q", got, want)
	}
}

func TestResolveFallsBackToOSUser(t *testing.T) {
	isolateGit(t)
	want := osUser(t)
	if got := identity.Resolve(context.Background(), t.TempDir()); got != want {
		t.Errorf("Resolve = %q, want %q", got, want)
	}
}

func TestResolveNeverEmpty(t *testing.T) {
	isolateGit(t)
	t.Setenv("PATH", "")
	for _, dir := range []string{t.TempDir(), filepathMissing(t)} {
		if got := identity.Resolve(context.Background(), dir); got == "" {
			t.Errorf("Resolve(%q) is empty", dir)
		}
	}
}

func filepathMissing(t *testing.T) string {
	return t.TempDir() + string(os.PathSeparator) + "missing"
}

func TestResolveTrimsWhitespace(t *testing.T) {
	isolateGit(t)
	r := testutil.NewRepo(t)
	r.Git("config", "user.name", "  Padded Name  ")
	r.Git("config", "user.email", " padded@example.com ")
	if got, want := identity.Resolve(context.Background(), r.Dir), "Padded Name <padded@example.com>"; got != want {
		t.Errorf("Resolve = %q, want %q", got, want)
	}
}
