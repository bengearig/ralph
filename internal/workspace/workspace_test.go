package workspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bengearig/ralph/internal/testutil"
)

var ctx = context.Background()

func newRepo(t *testing.T) (*testutil.Repo, *Local) {
	t.Helper()
	testutil.IsolateEnv(t)
	r := testutil.NewRepo(t)
	return r, NewLocal(r.Dir)
}

func withRemote(t *testing.T, r *testutil.Repo) string {
	t.Helper()
	bare := t.TempDir()
	r.Git("init", "--bare", "-b", "main", bare)
	r.Git("remote", "add", "origin", bare)
	return bare
}

func state(t *testing.T, w Workspace) State {
	t.Helper()
	st, err := w.State(ctx)
	if err != nil {
		t.Fatalf("State: %v", err)
	}
	return st
}

func equal(a, b []string) bool { return strings.Join(a, "\x00") == strings.Join(b, "\x00") }

func TestStateCleanRepo(t *testing.T) {
	_, w := newRepo(t)
	st := state(t, w)
	if st.Branch != "main" || len(st.Head) != 40 || len(st.Changes) != 0 {
		t.Errorf("state = %+v", st)
	}
}

func TestStateReportsChanges(t *testing.T) {
	r, w := newRepo(t)
	r.WriteFile("mod.txt", "1")
	r.WriteFile("del.txt", "1")
	r.WriteFile("old.txt", "rename me, with enough content to be detected")
	r.Commit("files")
	r.WriteFile("mod.txt", "2")
	r.WriteFile("staged.txt", "new")
	r.Git("add", "staged.txt")
	r.Git("rm", "-q", "del.txt")
	r.Git("mv", "old.txt", "new name.txt")
	r.WriteFile("sub/dir/untracked.txt", "u")
	want := []string{"del.txt", "mod.txt", "new name.txt", "staged.txt", "sub/dir/untracked.txt"}
	if got := state(t, w).Changes; !equal(got, want) {
		t.Errorf("Changes = %q, want %q", got, want)
	}
}

func TestStateIgnoresGitIgnored(t *testing.T) {
	r, w := newRepo(t)
	r.WriteFile(".gitignore", "ignored.txt\n")
	r.Git("add", ".gitignore")
	r.Git("commit", "-m", "ignore")
	r.WriteFile("ignored.txt", "x")
	if got := state(t, w).Changes; len(got) != 0 {
		t.Errorf("Changes = %q", got)
	}
}

func TestStateWithIgnoredPaths(t *testing.T) {
	r, _ := newRepo(t)
	w := NewLocal(r.Dir, WithIgnored([]string{"a/file.txt", "dir/"}))
	r.WriteFile("a/file.txt", "x")
	r.WriteFile("a/sibling.txt", "x")
	r.WriteFile("dir/deep/f.txt", "x")
	r.WriteFile("dirx/f.txt", "x")
	want := []string{"a/sibling.txt", "dirx/f.txt"}
	if got := state(t, w).Changes; !equal(got, want) {
		t.Errorf("Changes = %q, want %q", got, want)
	}
}

func TestStateDetachedHead(t *testing.T) {
	r, w := newRepo(t)
	r.Git("checkout", "--detach")
	if _, err := w.State(ctx); !errors.Is(err, ErrDetachedHead) {
		t.Errorf("err = %v", err)
	}
}

func TestStateNoCommits(t *testing.T) {
	testutil.IsolateEnv(t)
	dir := t.TempDir()
	if out, err := exec.Command("git", "init", "-b", "main", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	if _, err := NewLocal(dir).State(ctx); !errors.Is(err, ErrNoCommits) {
		t.Errorf("err = %v", err)
	}
}

func TestBranchExists(t *testing.T) {
	r, w := newRepo(t)
	r.Git("branch", "other")
	for name, want := range map[string]bool{"main": true, "other": true, "missing": false, "mai": false} {
		got, err := w.BranchExists(ctx, name)
		if err != nil || got != want {
			t.Errorf("BranchExists(%q) = %v, %v; want %v", name, got, err, want)
		}
	}
}

func TestCreateBranchChecksOut(t *testing.T) {
	_, w := newRepo(t)
	before := state(t, w)
	if err := w.CreateBranch(ctx, "ralph/task"); err != nil {
		t.Fatal(err)
	}
	after := state(t, w)
	if after.Branch != "ralph/task" || after.Head != before.Head {
		t.Errorf("after = %+v, before = %+v", after, before)
	}
}

func TestCreateBranchExistsErrors(t *testing.T) {
	_, w := newRepo(t)
	if err := w.CreateBranch(ctx, "main"); !errors.Is(err, ErrBranchExists) {
		t.Errorf("err = %v", err)
	}
}

func TestCreateBranchInvalidName(t *testing.T) {
	_, w := newRepo(t)
	for _, name := range []string{"has space", "a..b"} {
		err := w.CreateBranch(ctx, name)
		if !errors.Is(err, ErrInvalidBranch) || !strings.Contains(err.Error(), fmt.Sprintf("%q", name)) {
			t.Errorf("CreateBranch(%q) err = %v", name, err)
		}
	}
}

func TestCommitAllStagesEverything(t *testing.T) {
	r, w := newRepo(t)
	r.WriteFile("mod.txt", "1")
	r.WriteFile("del.txt", "1")
	r.Commit("files")
	r.WriteFile("mod.txt", "2")
	r.WriteFile("new.txt", "n")
	if err := os.Remove(filepath.Join(r.Dir, "del.txt")); err != nil {
		t.Fatal(err)
	}
	msg := "Subject line\n\nBody text.\n\n# not a comment\nRalph-Task: T-1\n"
	hash, err := w.CommitAll(ctx, msg)
	if err != nil {
		t.Fatal(err)
	}
	if head := r.Git("rev-parse", "HEAD"); hash != head {
		t.Errorf("hash = %s, HEAD = %s", hash, head)
	}
	if got := r.Git("diff-tree", "--no-commit-id", "--name-status", "-r", "HEAD"); got != "D\tdel.txt\nM\tmod.txt\nA\tnew.txt" {
		t.Errorf("commit changes = %q", got)
	}
	if got := r.Git("log", "-1", "--format=%B"); got != strings.TrimSpace(msg) {
		t.Errorf("message = %q, want %q", got, strings.TrimSpace(msg))
	}
}

func TestCommitAllLeavesIgnoredPathsOut(t *testing.T) {
	r, _ := newRepo(t)
	w := NewLocal(r.Dir, WithIgnored([]string{"local.json", "dir/"}))
	r.WriteFile("local.json", "{}")
	r.WriteFile("dir/a.txt", "a")
	r.WriteFile("kept.txt", "k")
	if _, err := w.CommitAll(ctx, "subject"); err != nil {
		t.Fatal(err)
	}
	if got := r.Git("diff-tree", "--no-commit-id", "--name-only", "-r", "HEAD"); got != "kept.txt" {
		t.Errorf("committed = %q, want only kept.txt", got)
	}
}

func TestCommitAllNothing(t *testing.T) {
	_, w := newRepo(t)
	if _, err := w.CommitAll(ctx, "msg"); !errors.Is(err, ErrNothingToCommit) {
		t.Errorf("err = %v", err)
	}
}

func TestCommitAllHookFailure(t *testing.T) {
	r, w := newRepo(t)
	hook := filepath.Join(r.Dir, ".git", "hooks", "pre-commit")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\necho 'lint says no'\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	r.WriteFile("a.txt", "a")
	_, err := w.CommitAll(ctx, "msg")
	var ge *GitError
	if !errors.As(err, &ge) || !strings.Contains(ge.Output, "lint says no") {
		t.Fatalf("err = %v", err)
	}
	if got := r.Git("diff", "--cached", "--name-only"); got != "a.txt" {
		t.Errorf("staged = %q", got)
	}
}

func TestCommitAllRunsOnlyCleanTreeAfter(t *testing.T) {
	r, w := newRepo(t)
	r.WriteFile("sub/a.txt", "a")
	if _, err := w.CommitAll(ctx, "msg"); err != nil {
		t.Fatal(err)
	}
	if got := state(t, w).Changes; len(got) != 0 {
		t.Errorf("Changes = %q", got)
	}
}

func TestPushSetsUpstream(t *testing.T) {
	r, w := newRepo(t)
	bare := withRemote(t, r)
	if err := w.CreateBranch(ctx, "feature"); err != nil {
		t.Fatal(err)
	}
	if err := w.Push(ctx, "origin", "feature"); err != nil {
		t.Fatal(err)
	}
	if got := r.Git("--git-dir", bare, "rev-parse", "feature"); got != r.Git("rev-parse", "HEAD") {
		t.Errorf("remote feature = %s", got)
	}
	if got := r.Git("rev-parse", "--abbrev-ref", "@{u}"); got != "origin/feature" {
		t.Errorf("upstream = %q", got)
	}
}

func TestPushRejectedIsGitError(t *testing.T) {
	r, w := newRepo(t)
	bare := withRemote(t, r)
	r.Git("push", "origin", "main")
	other := filepath.Join(t.TempDir(), "other")
	r.Git("clone", bare, other)
	r.Git("-C", other, "config", "user.name", "O")
	r.Git("-C", other, "config", "user.email", "o@example.com")
	r.Git("-C", other, "commit", "--allow-empty", "-m", "remote work")
	r.Git("-C", other, "push", "origin", "main")
	remoteHead := r.Git("--git-dir", bare, "rev-parse", "main")
	r.Git("commit", "--allow-empty", "-m", "local work")

	err := w.Push(ctx, "origin", "main")
	var ge *GitError
	if !errors.As(err, &ge) || ge.Args[0] != "push" {
		t.Fatalf("err = %v", err)
	}
	if got := r.Git("--git-dir", bare, "rev-parse", "main"); got != remoteHead {
		t.Errorf("remote main moved to %s", got)
	}
}

func TestGitErrorRemovesCredentials(t *testing.T) {
	e := newGitError([]string{"push"}, 1, "fatal: unable to access 'https://user:secret@example.com/x.git': denied")
	if strings.Contains(e.Output, "secret") || !strings.Contains(e.Output, "https://example.com/x.git") {
		t.Errorf("Output = %q", e.Output)
	}
	if want := "git push failed (exit 1): " + e.Output; e.Error() != want {
		t.Errorf("Error = %q", e.Error())
	}
}

func TestGitErrorOutputCut(t *testing.T) {
	e := newGitError([]string{"commit"}, 1, strings.Repeat("x", 5000))
	if len(e.Output) != 2000 {
		t.Errorf("len = %d", len(e.Output))
	}
	e = newGitError([]string{"commit"}, 1, strings.Repeat("é", 2000))
	if len(e.Output) > 2000 {
		t.Errorf("len = %d", len(e.Output))
	}
}

func TestCommitsAhead(t *testing.T) {
	r, w := newRepo(t)
	if n, err := w.CommitsAhead(ctx, "main"); err != nil || n != 0 {
		t.Errorf("on main: %d, %v", n, err)
	}
	if err := w.CreateBranch(ctx, "feature"); err != nil {
		t.Fatal(err)
	}
	r.WriteFile("a.txt", "a")
	r.Commit("work")
	if n, err := w.CommitsAhead(ctx, "main"); err != nil || n != 1 {
		t.Errorf("on feature: %d, %v", n, err)
	}
}

func TestAheadOfRemoteUntracked(t *testing.T) {
	r, w := newRepo(t)
	withRemote(t, r)
	n, tracked, err := w.AheadOfRemote(ctx, "origin", "main")
	if err != nil || tracked || n != 0 {
		t.Errorf("got %d, %v, %v", n, tracked, err)
	}
}

func TestAheadOfRemoteCounts(t *testing.T) {
	r, w := newRepo(t)
	withRemote(t, r)
	if err := w.Push(ctx, "origin", "main"); err != nil {
		t.Fatal(err)
	}
	r.Git("commit", "--allow-empty", "-m", "more")
	n, tracked, err := w.AheadOfRemote(ctx, "origin", "main")
	if err != nil || !tracked || n != 1 {
		t.Errorf("got %d, %v, %v", n, tracked, err)
	}
}

func TestGuardAllowsCleanBase(t *testing.T) {
	_, w := newRepo(t)
	st, err := CheckStartable(ctx, w, GuardOptions{Base: "main", Remote: "origin"})
	if err != nil || st.Branch != "main" || len(st.Head) != 40 {
		t.Errorf("got %+v, %v", st, err)
	}
}

func TestGuardNotOnBase(t *testing.T) {
	r, w := newRepo(t)
	r.Git("switch", "-c", "other")
	_, err := CheckStartable(ctx, w, GuardOptions{Base: "main", Remote: "origin"})
	var nb *NotOnBaseError
	if !errors.As(err, &nb) {
		t.Fatalf("err = %v", err)
	}
	want := `the current branch is "other", but quick work must start from the base branch "main": run git switch main first`
	if err.Error() != want {
		t.Errorf("message = %q", err.Error())
	}
}

func TestGuardDirty(t *testing.T) {
	r, w := newRepo(t)
	r.WriteFile("a.txt", "a")
	_, err := CheckStartable(ctx, w, GuardOptions{Base: "main", Remote: "origin"})
	var d *DirtyError
	if !errors.As(err, &d) || !equal(d.Paths, []string{"a.txt"}) {
		t.Fatalf("err = %v", err)
	}
	for i := 0; i < 11; i++ {
		r.WriteFile(fmt.Sprintf("f%02d.txt", i), "x")
	}
	_, err = CheckStartable(ctx, w, GuardOptions{Base: "main", Remote: "origin"})
	msg := err.Error()
	if !strings.Contains(msg, "and 2 more") || !strings.HasSuffix(msg, "commit or stash these changes first") ||
		strings.Contains(msg, "f10.txt") || !strings.Contains(msg, "f08.txt") {
		t.Errorf("message = %q", msg)
	}
}

func TestGuardDirtyIgnoresConfiguredPaths(t *testing.T) {
	r, _ := newRepo(t)
	w := NewLocal(r.Dir, WithIgnored([]string{".ralph/"}))
	r.WriteFile(".ralph/integration.local.json", "{}")
	if _, err := CheckStartable(ctx, w, GuardOptions{Base: "main", Remote: "origin"}); err != nil {
		t.Errorf("err = %v", err)
	}
}

func TestGuardBaseAhead(t *testing.T) {
	r, w := newRepo(t)
	withRemote(t, r)
	if err := w.Push(ctx, "origin", "main"); err != nil {
		t.Fatal(err)
	}
	r.Git("commit", "--allow-empty", "-m", "unpushed")
	_, err := CheckStartable(ctx, w, GuardOptions{Base: "main", Remote: "origin"})
	var ba *BaseAheadError
	if !errors.As(err, &ba) || ba.Count != 1 {
		t.Fatalf("err = %v", err)
	}
	want := `"main" has 1 commit(s) that origin/main does not; push them or reset the branch first`
	if err.Error() != want {
		t.Errorf("message = %q", err.Error())
	}
}

func TestGuardBaseAheadSkippedWhenUntracked(t *testing.T) {
	r, w := newRepo(t)
	withRemote(t, r)
	r.Git("commit", "--allow-empty", "-m", "local only")
	if _, err := CheckStartable(ctx, w, GuardOptions{Base: "main", Remote: "origin"}); err != nil {
		t.Errorf("err = %v", err)
	}
}

func TestGuardOrder(t *testing.T) {
	r, w := newRepo(t)
	r.Git("switch", "-c", "other")
	r.WriteFile("a.txt", "a")
	_, err := CheckStartable(ctx, w, GuardOptions{Base: "main", Remote: "origin"})
	if !errors.Is(err, ErrNotOnBase) {
		t.Errorf("err = %v", err)
	}
}

func TestGuardErrorsIs(t *testing.T) {
	for _, tc := range []struct {
		err, target error
	}{
		{&NotOnBaseError{}, ErrNotOnBase},
		{&DirtyError{}, ErrDirty},
		{&BaseAheadError{}, ErrBaseAhead},
	} {
		if !errors.Is(fmt.Errorf("wrapped: %w", tc.err), tc.target) {
			t.Errorf("%T is not %v", tc.err, tc.target)
		}
	}
}

func TestWorkspaceLogOldestFirstWithMessages(t *testing.T) {
	r, w := newRepo(t)
	if err := w.CreateBranch(ctx, "feature"); err != nil {
		t.Fatal(err)
	}
	r.WriteFile("a.txt", "a")
	first := r.Commit("first subject\n\nbody line\n\nRalph-Task: T-1")
	r.WriteFile("b.txt", "b")
	second := r.Commit("second subject")
	got, err := w.Log(ctx, "main")
	if err != nil {
		t.Fatal(err)
	}
	want := []Commit{
		{Hash: first, Message: "first subject\n\nbody line\n\nRalph-Task: T-1"},
		{Hash: second, Message: "second subject"},
	}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("Log = %+v, want %+v", got, want)
	}
}

func TestWorkspaceLogEmptyOnBase(t *testing.T) {
	_, w := newRepo(t)
	got, err := w.Log(ctx, "main")
	if err != nil || len(got) != 0 {
		t.Fatalf("Log = %+v, %v", got, err)
	}
}
