package testutil

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewRepo(t *testing.T) {
	r := NewRepo(t)
	if got := r.Git("rev-parse", "--is-inside-work-tree"); got != "true" {
		t.Fatalf("is-inside-work-tree = %q, want true", got)
	}
	if got := r.Git("rev-parse", "--abbrev-ref", "HEAD"); got != "main" {
		t.Fatalf("branch = %q, want main", got)
	}
	if got := r.Git("rev-list", "--count", "HEAD"); got != "1" {
		t.Fatalf("commit count = %q, want 1", got)
	}
}

func TestRepoCommit(t *testing.T) {
	r := NewRepo(t)
	r.WriteFile("dir/file.txt", "hello")
	hash := r.Commit("add file")
	if len(hash) != 40 {
		t.Fatalf("hash %q has length %d, want 40", hash, len(hash))
	}
	if head := r.Git("rev-parse", "HEAD"); head != hash {
		t.Fatalf("HEAD = %q, want %q", head, hash)
	}
	if got := r.Git("rev-list", "--count", "HEAD"); got != "2" {
		t.Fatalf("commit count = %q, want 2", got)
	}
}

func TestAddWorktree(t *testing.T) {
	r := NewRepo(t)
	path := r.AddWorktree("wt", "feature")
	if info, err := os.Stat(path); err != nil || !info.IsDir() {
		t.Fatalf("worktree path %q is not a directory: %v", path, err)
	}
	if got := r.gitIn(path, "rev-parse", "--abbrev-ref", "HEAD"); got != "feature" {
		t.Fatalf("worktree branch = %q, want feature", got)
	}
	common := r.gitIn(path, "rev-parse", "--git-common-dir")
	if !filepath.IsAbs(common) {
		common = filepath.Join(path, common)
	}
	gotDir, err := filepath.EvalSymlinks(common)
	if err != nil {
		t.Fatal(err)
	}
	wantDir, err := filepath.EvalSymlinks(filepath.Join(r.Dir, ".git"))
	if err != nil {
		t.Fatal(err)
	}
	if gotDir != wantDir {
		t.Fatalf("git-common-dir = %q, want %q", gotDir, wantDir)
	}
}

func TestGitIgnoresHostConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	if err := os.WriteFile(filepath.Join(home, ".gitconfig"), []byte("[user]\n\tname = leaked\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := NewRepo(t)
	if got := r.Git("config", "user.name"); got == "leaked" {
		t.Fatalf("host git config leaked: user.name = %q", got)
	}
}

func TestIsolateEnv(t *testing.T) {
	t.Setenv("RALPH_EXAMPLE", "set")
	IsolateEnv(t)
	tmp := os.TempDir()
	for _, name := range []string{"HOME", "XDG_CONFIG_HOME"} {
		got := os.Getenv(name)
		if got == "" || !strings.HasPrefix(got, tmp) {
			t.Errorf("%s = %q, want a path inside %q", name, got, tmp)
		}
	}
	if got := os.Getenv("RALPH_EXAMPLE"); got != "" {
		t.Errorf("RALPH_EXAMPLE = %q, want empty", got)
	}
}
