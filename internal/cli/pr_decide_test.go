package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/bengearig/ralph/internal/db"
	"github.com/bengearig/ralph/internal/testutil"
	"github.com/bengearig/ralph/internal/units"
)

// closedSlice has S-1 with a closed, unresolved pull request #1.
func closedSlice(t *testing.T) (*quickEnv, string) {
	t.Helper()
	e, p, pr := feedbackEnv(t)
	p.Close(pr.Number)
	if code, _, stderr := run(t, "pr", "check", "S-1"); code != 0 {
		t.Fatalf("check: %d %s", code, stderr)
	}
	return e, pr.URL
}

func withService(t *testing.T, fn func(ctx context.Context, svc units.Service)) {
	t.Helper()
	d, err := openStore(context.Background(), loadedApp(t), db.Options{MustExist: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	fn(context.Background(), units.NewService(d))
}

func addTaskIn(t *testing.T, svc units.Service, parent, title string, todo bool) units.Unit {
	t.Helper()
	ctx := context.Background()
	task, err := svc.Create(ctx, units.NewUnit{Level: units.LevelTask, ParentID: parent, Title: title, Body: "b", AcceptanceCriteria: "c"})
	if err != nil {
		t.Fatal(err)
	}
	if todo {
		if task, err = svc.SetState(ctx, task.ID, task.Fingerprint, units.StateTodo, ""); err != nil {
			t.Fatal(err)
		}
	}
	return task
}

func taskState(t *testing.T, id string) units.State {
	t.Helper()
	var st units.State
	withService(t, func(ctx context.Context, svc units.Service) {
		u, err := svc.Get(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		st = u.State
	})
	return st
}

func TestPRReopenOutput(t *testing.T) {
	_, _ = closedSlice(t)
	code, stdout, stderr := run(t, "pr", "reopen", "s-1")
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	lines := strings.Split(strings.TrimSuffix(stdout, "\n"), "\n")
	n := len(lines)
	if n < 3 || !strings.HasPrefix(lines[n-3], "pull request: https://fake.example/pull/2") ||
		lines[n-2] != "nothing has been merged: a person reviews the pull request" || lines[n-1] != "resolved pull request #1 as reopened" {
		t.Errorf("stdout = %q", stdout)
	}
	if code, _, _ := run(t, "pr", "reopen", "S-1"); code != 1 {
		t.Errorf("second reopen code = %d", code)
	}
}

func TestPRRearticulateOutputWithAndWithoutBrief(t *testing.T) {
	_, _ = closedSlice(t)
	withService(t, func(ctx context.Context, svc units.Service) {
		addTaskIn(t, svc, "S-1", "open", true)
	})
	code, stdout, stderr := run(t, "pr", "rearticulate", "s-1")
	want := "moved T-2 back to PROPOSED\nkept T-1 (done)\nresolved pull request #1 as rearticulated\n" +
		"plan S-1 again: /ralph:receive B-1 to revise the plan, or /ralph:articulate S-1 to work out the tasks (/ralph/receive and /ralph/articulate in OpenCode)\n"
	if code != 0 || stdout != want {
		t.Errorf("code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
	if got := taskState(t, "T-2"); got != units.StateProposed {
		t.Errorf("T-2 = %s", got)
	}

	var loose string
	withService(t, func(ctx context.Context, svc units.Service) {
		s, err := svc.Create(ctx, units.NewUnit{Level: units.LevelSlice, ParentID: "B-1", Title: "Loose"})
		if err != nil {
			t.Fatal(err)
		}
		loose = s.ID
		rec := units.PullRequestRecord{SliceID: s.ID, Number: 5, URL: "https://x.test/5", Head: "h", Base: "main", State: units.PullRequestClosed}
		if err := svc.RecordPullRequest(ctx, rec); err != nil {
			t.Fatal(err)
		}
	})
	code, stdout, stderr = run(t, "pr", "rearticulate", loose)
	want = "no unfinished tasks to move\nresolved pull request #5 as rearticulated\n" +
		"plan " + loose + " again: /ralph:receive B-1 to revise the plan, or /ralph:articulate " + loose + " to work out the tasks (/ralph/receive and /ralph/articulate in OpenCode)\n"
	if code != 0 || stdout != want {
		t.Errorf("code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
}

func TestPRCancelDryRunOutput(t *testing.T) {
	_, _ = closedSlice(t)
	withService(t, func(ctx context.Context, svc units.Service) {
		addTaskIn(t, svc, "S-1", "open", true)
	})
	code, stdout, stderr := run(t, "pr", "cancel", "s-1")
	if code != 0 || stdout != "would cancel T-2\nrun again with --yes to do it\n" {
		t.Errorf("code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
	if got := taskState(t, "T-2"); got != units.StateTodo {
		t.Errorf("T-2 = %s", got)
	}
}

func TestPRCancelYesOutput(t *testing.T) {
	_, _ = closedSlice(t)
	withService(t, func(ctx context.Context, svc units.Service) {
		addTaskIn(t, svc, "S-1", "open", true)
	})
	code, stdout, stderr := run(t, "pr", "cancel", "S-1", "--yes")
	if code != 0 || stdout != "cancelled T-2\nresolved pull request #1 as cancelled\n" {
		t.Errorf("code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
	if got := taskState(t, "T-2"); got != units.StateCancelled {
		t.Errorf("T-2 = %s", got)
	}
	if got := taskState(t, "T-1"); got != units.StateDone {
		t.Errorf("T-1 = %s", got)
	}
}

func dependentSlice(t *testing.T) (slice, task string) {
	t.Helper()
	withService(t, func(ctx context.Context, svc units.Service) {
		s, err := svc.Create(ctx, units.NewUnit{Level: units.LevelSlice, ParentID: "B-1", Title: "Waits"})
		if err != nil {
			t.Fatal(err)
		}
		if err := svc.AddDependency(ctx, s.ID, "S-1"); err != nil {
			t.Fatal(err)
		}
		slice, task = s.ID, addTaskIn(t, svc, s.ID, "later", true).ID
	})
	return slice, task
}

func TestPRCancelDependentsOutput(t *testing.T) {
	_, _ = closedSlice(t)
	dep, task := dependentSlice(t)

	code, stdout, stderr := run(t, "pr", "cancel", "S-1")
	want := "would cancel no unfinished tasks\nslices waiting for S-1: " + dep + "; they stay waiting, or add --dependents\nrun again with --yes to do it\n"
	if code != 0 || stdout != want {
		t.Errorf("dry: code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
	code, stdout, stderr = run(t, "pr", "cancel", "S-1", "--dependents")
	want = "would cancel no unfinished tasks\nwould also cancel the unfinished tasks of " + dep + "\nrun again with --yes to do it\n"
	if code != 0 || stdout != want {
		t.Errorf("dry with flag: code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
	if got := taskState(t, task); got != units.StateTodo {
		t.Errorf("dry run changed %s: %s", task, got)
	}
	code, stdout, stderr = run(t, "pr", "cancel", "S-1", "--dependents", "--yes")
	want = "cancelled no unfinished tasks\nalso cancelled the unfinished tasks of " + dep + "\nresolved pull request #1 as cancelled\n"
	if code != 0 || stdout != want {
		t.Errorf("yes: code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
	if got := taskState(t, task); got != units.StateCancelled {
		t.Errorf("%s = %s", task, got)
	}
}

func TestPRDecisionsNeedOneSliceArgument(t *testing.T) {
	testutil.IsolateEnv(t)
	t.Chdir(t.TempDir())
	for _, cmd := range []string{"reopen", "rearticulate", "cancel"} {
		for _, args := range [][]string{{"pr", cmd}, {"pr", cmd, "S-1", "S-2"}} {
			code, stdout, stderr := run(t, args...)
			if code != 1 || stdout != "" || !strings.Contains(stderr, "accepts 1 arg(s)") {
				t.Errorf("%v: code = %d, stdout = %q, stderr = %q", args, code, stdout, stderr)
			}
		}
	}
}

func TestPRDecisionsNotWaiting(t *testing.T) {
	feedbackEnv(t)
	for _, args := range [][]string{{"pr", "reopen", "S-1"}, {"pr", "rearticulate", "S-1"}, {"pr", "cancel", "S-1", "--yes"}} {
		code, stdout, stderr := run(t, args...)
		if code != 1 || stdout != "" || stderr != "ralph: S-1 is not waiting for a decision\n" {
			t.Errorf("%v: code = %d, stdout = %q, stderr = %q", args, code, stdout, stderr)
		}
	}
}

func TestPRHelpListsAllSubcommands(t *testing.T) {
	testutil.IsolateEnv(t)
	t.Chdir(t.TempDir())
	code, stdout, stderr := run(t, "pr")
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	for _, name := range []string{"create", "feedback", "check", "reopen", "rearticulate", "cancel"} {
		if !strings.Contains(stdout, "  "+name+" ") {
			t.Errorf("help lacks %s: %q", name, stdout)
		}
	}
}
