// Package testutil provides test helpers that isolate tests from the host's
// git and ralph configuration.
package testutil

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type Repo struct {
	Dir string

	t   testing.TB
	env []string
}

// NewRepo creates a repository on branch main with one initial commit.
func NewRepo(t testing.TB) *Repo {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Fatalf("testutil: git is required on PATH: %v", err)
	}
	r := &Repo{Dir: t.TempDir(), t: t, env: gitEnv(t.TempDir())}
	r.Git("init", "-b", "main")
	r.Git("config", "user.name", "Test User")
	r.Git("config", "user.email", "test@example.com")
	r.Git("config", "commit.gpgsign", "false")
	r.Git("commit", "--allow-empty", "-m", "initial commit")
	return r
}

// Git runs git in the repository directory and returns trimmed stdout.
func (r *Repo) Git(args ...string) string {
	r.t.Helper()
	return r.gitIn(r.Dir, args...)
}

func (r *Repo) gitIn(dir string, args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = r.env
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		r.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, stderr.String())
	}
	return strings.TrimSpace(stdout.String())
}

// WriteFile writes content to rel inside the repository, creating parent directories.
func (r *Repo) WriteFile(rel, content string) {
	r.t.Helper()
	path := filepath.Join(r.Dir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		r.t.Fatalf("testutil: create directory for %s: %v", rel, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		r.t.Fatalf("testutil: write %s: %v", rel, err)
	}
}

// Commit stages everything, commits, and returns the new commit hash.
func (r *Repo) Commit(message string) string {
	r.t.Helper()
	r.Git("add", "-A")
	r.Git("commit", "-m", message)
	return r.Git("rev-parse", "HEAD")
}

// AddWorktree creates a worktree on a new branch and returns its path.
func (r *Repo) AddWorktree(name, branch string) string {
	r.t.Helper()
	path := filepath.Join(r.t.TempDir(), name)
	r.Git("worktree", "add", "-b", branch, path)
	return path
}

// IsolateEnv points HOME and XDG_CONFIG_HOME at temporary directories and
// blanks every RALPH_ variable for the duration of the test.
func IsolateEnv(t testing.TB) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(name, "RALPH_") {
			t.Setenv(name, "")
		}
	}
}

// gitEnv drops inherited GIT_ variables so none can redirect or reconfigure git.
func gitEnv(home string) []string {
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "GIT_") || strings.HasPrefix(kv, "HOME=") {
			continue
		}
		env = append(env, kv)
	}
	return append(env,
		"HOME="+home,
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_CONFIG_SYSTEM="+os.DevNull,
	)
}
