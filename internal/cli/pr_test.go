package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/bengearig/ralph/internal/db"
	"github.com/bengearig/ralph/internal/testutil"
	"github.com/bengearig/ralph/internal/units"
)

// finishedSlice adds a slice with one DONE task whose commit is on the slice's branch.
func (e *quickEnv) finishedSlice(t *testing.T) string {
	t.Helper()
	d, err := openStore(context.Background(), loadedApp(t), db.Options{MustExist: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	ctx := context.Background()
	svc := units.NewService(d)
	b, err := svc.Create(ctx, units.NewUnit{Level: units.LevelBrief, Title: "Ship it", Body: "The brief."})
	if err != nil {
		t.Fatal(err)
	}
	s, err := svc.Create(ctx, units.NewUnit{Level: units.LevelSlice, ParentID: b.ID, Title: "The slice", Body: "Body."})
	if err != nil {
		t.Fatal(err)
	}
	task, err := svc.Create(ctx, units.NewUnit{Level: units.LevelTask, ParentID: s.ID, Title: "Do it", Body: "b", AcceptanceCriteria: "c"})
	if err != nil {
		t.Fatal(err)
	}
	for _, st := range []units.State{units.StateTodo, units.StateInProgress, units.StateDone} {
		cur, err := svc.Get(ctx, task.ID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := svc.SetState(ctx, task.ID, cur.Fingerprint, st, "test"); err != nil {
			t.Fatal(err)
		}
	}
	branch := "ralph/s-1-the-slice"
	e.repo.Git("switch", "-c", branch)
	e.repo.WriteFile("done.txt", "work\n")
	e.repo.Commit("T-1: Do it")
	return branch
}

func TestPRCreateHelpListsCreate(t *testing.T) {
	testutil.IsolateEnv(t)
	t.Chdir(t.TempDir())
	code, stdout, stderr := run(t, "pr")
	if code != 0 || !strings.Contains(stdout, "create") {
		t.Fatalf("code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
}

func TestPRCreateOutputLines(t *testing.T) {
	e := newQuickEnv(t, "pr:\n  draft: true\n")
	branch := e.finishedSlice(t)
	code, stdout, stderr := run(t, "pr", "create", "s-1")
	if code != 0 {
		t.Fatalf("code = %d: %s", code, stderr)
	}
	want := "verification: none configured\n" +
		"pushed " + branch + " to origin\n" +
		"pull request: https://example.com/o/r/pull/7 (draft)\n" +
		"nothing has been merged: a person reviews the pull request\n"
	if stdout != want {
		t.Errorf("stdout =\n%q\nwant\n%q", stdout, want)
	}
	if !e.remoteHasBranch(branch) {
		t.Error("the branch is not on the origin")
	}
	if len(e.posted) != 1 || e.posted[0]["title"] != "S-1: The slice" {
		t.Errorf("posted = %+v", e.posted)
	}
}

func TestPRCreateNeedsOneSliceArgument(t *testing.T) {
	testutil.IsolateEnv(t)
	t.Chdir(t.TempDir())
	for _, args := range [][]string{{"pr", "create"}, {"pr", "create", "S-1", "S-2"}} {
		code, stdout, stderr := run(t, args...)
		if code != 1 || stdout != "" || !strings.Contains(stderr, "accepts 1 arg(s)") {
			t.Errorf("%v: code = %d, stdout = %q, stderr = %q", args, code, stdout, stderr)
		}
	}
}

func TestPRCreateNotInitialized(t *testing.T) {
	inRepo(t)
	code, stdout, stderr := run(t, "pr", "create", "S-1")
	want := "ralph: this project has no ralph database: run \"ralph init\" first\n"
	if code != 1 || stdout != "" || stderr != want {
		t.Errorf("code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
}

func TestPRCreateOutsideRepository(t *testing.T) {
	testutil.IsolateEnv(t)
	t.Chdir(t.TempDir())
	code, _, stderr := run(t, "pr", "create", "S-1")
	if code != 1 || stderr != "ralph: not in a git repository\n" {
		t.Errorf("code = %d, stderr = %q", code, stderr)
	}
}
