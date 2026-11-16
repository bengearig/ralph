package project

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bengearig/ralph/internal/testutil"
)

func sameDir(t *testing.T, got, want string) {
	t.Helper()
	g, err := filepath.EvalSymlinks(got)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q): %v", got, err)
	}
	w, err := filepath.EvalSymlinks(want)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q): %v", want, err)
	}
	if g != w {
		t.Fatalf("root = %q, want %q", g, w)
	}
}

func TestFindRootRegularRepo(t *testing.T) {
	testutil.IsolateEnv(t)
	r := testutil.NewRepo(t)
	got, err := FindRoot(r.Dir)
	if err != nil {
		t.Fatal(err)
	}
	sameDir(t, got, r.Dir)
}

func TestFindRootSubdirectory(t *testing.T) {
	testutil.IsolateEnv(t)
	r := testutil.NewRepo(t)
	sub := filepath.Join(r.Dir, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := FindRoot(sub)
	if err != nil {
		t.Fatal(err)
	}
	sameDir(t, got, r.Dir)
}

func TestFindRootLinkedWorktreeReturnsPrimary(t *testing.T) {
	testutil.IsolateEnv(t)
	r := testutil.NewRepo(t)
	wt := r.AddWorktree("linked", "feature")
	got, err := FindRoot(wt)
	if err != nil {
		t.Fatal(err)
	}
	sameDir(t, got, r.Dir)
}

func TestFindRootPathWithSpaces(t *testing.T) {
	testutil.IsolateEnv(t)
	dir := filepath.Join(t.TempDir(), "my repo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", dir, "init", "-b", "main").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	got, err := FindRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	sameDir(t, got, dir)
}

func TestFindRootNotRepository(t *testing.T) {
	testutil.IsolateEnv(t)
	dir := t.TempDir()
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(dir))
	_, err := FindRoot(dir)
	if !errors.Is(err, ErrNotRepository) {
		t.Fatalf("err = %v, want ErrNotRepository", err)
	}
}

func TestFindRootBareRepository(t *testing.T) {
	testutil.IsolateEnv(t)
	r := testutil.NewRepo(t)
	bare := filepath.Join(t.TempDir(), "bare.git")
	r.Git("init", "--bare", bare)
	_, err := FindRoot(bare)
	if !errors.Is(err, ErrBareRepository) {
		t.Fatalf("err = %v, want ErrBareRepository", err)
	}
}

func TestFindRootGitMissing(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", t.TempDir())
	_, err := FindRoot(dir)
	if err == nil || !strings.Contains(err.Error(), "git") {
		t.Fatalf("err = %v, want an error mentioning git", err)
	}
}

func TestFindRootMissingDirectory(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	_, err := FindRoot(missing)
	if err == nil || !strings.Contains(err.Error(), missing) {
		t.Fatalf("err = %v, want an error naming %q", err, missing)
	}
}

func TestRalphDir(t *testing.T) {
	root := filepath.Join("some", "root")
	if got, want := RalphDir(root), filepath.Join(root, ".ralph"); got != want {
		t.Fatalf("RalphDir = %q, want %q", got, want)
	}
}

func TestStorePath(t *testing.T) {
	root := t.TempDir()
	if got, want := StorePath(root), filepath.Join(root, ".ralph", "store.db"); got != want {
		t.Fatalf("StorePath = %q, want %q", got, want)
	}
}

func TestBackupsDir(t *testing.T) {
	root := t.TempDir()
	if got, want := BackupsDir(root), filepath.Join(root, ".ralph", "backups"); got != want {
		t.Fatalf("BackupsDir = %q, want %q", got, want)
	}
}
