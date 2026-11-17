package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/bengearig/ralph/internal/db"
	"github.com/bengearig/ralph/internal/testutil"
	"github.com/bengearig/ralph/internal/units"
)

func TestPRCheckOutputForEachChange(t *testing.T) {
	e, p, pr := feedbackEnv(t)
	_ = e
	head := "S-1 pull request #1 (%s): " + pr.URL + "\n"
	line := func(state string) string { return strings.Replace(head, "%s", state, 1) }

	code, stdout, stderr := run(t, "pr", "check", "s-1")
	if code != 0 || stdout != line("open")+"unchanged\n" {
		t.Errorf("unchanged: code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}

	p.Close(pr.Number)
	code, stdout, stderr = run(t, "pr", "check", "S-1")
	want := line("closed") + "closed without merging: S-1 needs a decision\n" +
		"  ralph pr reopen S-1\n  ralph pr rearticulate S-1\n  ralph pr cancel S-1\n"
	if code != 0 || stdout != want {
		t.Errorf("closed: code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}

	code, stdout, _ = run(t, "pr", "check", "S-1")
	if code != 0 || stdout != line("closed")+"unchanged\n" {
		t.Errorf("closed again: code = %d, stdout = %q", code, stdout)
	}

	p.Merge(pr.Number)
	code, stdout, stderr = run(t, "pr", "check", "S-1")
	if code != 0 || stdout != line("merged")+"merged\n" {
		t.Errorf("merged: code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
}

func TestPRCheckReopenedOnTheHost(t *testing.T) {
	_, p, pr := feedbackEnv(t)
	d, err := openStore(context.Background(), loadedApp(t), db.Options{MustExist: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := units.NewService(d).SetPullRequestState(context.Background(), "S-1", pr.Number, units.PullRequestClosed); err != nil {
		t.Fatal(err)
	}
	_ = d.Close()
	_ = p
	code, stdout, stderr := run(t, "pr", "check", "S-1")
	want := "S-1 pull request #1 (open): " + pr.URL + "\nreopened on the host\n"
	if code != 0 || stdout != want {
		t.Errorf("code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
}

func TestPRCheckExitStatusZeroForClosed(t *testing.T) {
	_, p, pr := feedbackEnv(t)
	p.Close(pr.Number)
	if code, _, stderr := run(t, "pr", "check", "S-1"); code != 0 || stderr != "" {
		t.Errorf("code = %d, stderr = %q", code, stderr)
	}
}

func TestPRCheckNoPullRequest(t *testing.T) {
	e := newQuickEnv(t, "")
	e.finishedSlice(t)
	code, stdout, stderr := run(t, "pr", "check", "S-1")
	if code != 1 || stdout != "" || stderr != "ralph: S-1 has no pull request: run ralph pr create S-1\n" {
		t.Errorf("code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
}

func TestPRCheckNeedsOneSliceArgument(t *testing.T) {
	testutil.IsolateEnv(t)
	t.Chdir(t.TempDir())
	for _, args := range [][]string{{"pr", "check"}, {"pr", "check", "S-1", "S-2"}} {
		code, stdout, stderr := run(t, args...)
		if code != 1 || stdout != "" || !strings.Contains(stderr, "accepts 1 arg(s)") {
			t.Errorf("%v: code = %d, stdout = %q, stderr = %q", args, code, stdout, stderr)
		}
	}
}

func TestStatusPullRequestsBlock(t *testing.T) {
	e := newStatusEnv(t)
	e.with(func(s units.Service) {
		b := e.create(s, units.LevelBrief, "", "B", "")
		sl := e.create(s, units.LevelSlice, b, "S", "")
		e.create(s, units.LevelTask, sl, "t", units.StateDone)
		for _, r := range []units.PullRequestRecord{
			{SliceID: sl, Number: 4, URL: "https://x.test/pull/4", Head: "h", Base: "main", State: units.PullRequestClosed},
		} {
			if err := s.RecordPullRequest(e.ctx, r); err != nil {
				e.t.Fatal(err)
			}
		}
		if err := s.RecordPullRequest(e.ctx, units.PullRequestRecord{SliceID: sl, Number: 5, URL: "https://x.test/pull/5", Head: "h", Base: "main", State: units.PullRequestOpen}); err != nil {
			e.t.Fatal(err)
		}
	})
	code, out, stderr := e.status("S-1")
	if code != 0 {
		t.Fatalf("code %d %q", code, stderr)
	}
	want := "\nPull requests:\n#4  closed  https://x.test/pull/4\n#5  open  https://x.test/pull/5\n"
	if !strings.Contains(out, want) {
		t.Errorf("out =\n%s\nlacks %q", out, want)
	}
	if !strings.HasPrefix(out, "S-1  DONE  S\n") {
		t.Errorf("out = %q", out)
	}
	if strings.Index(out, "Tasks:") > strings.Index(out, "Pull requests:") {
		t.Errorf("block comes before the tasks:\n%s", out)
	}
}

func TestStatusPullRequestsBlockShowsResolution(t *testing.T) {
	e := newStatusEnv(t)
	e.with(func(s units.Service) {
		b := e.create(s, units.LevelBrief, "", "B", "")
		sl := e.create(s, units.LevelSlice, b, "S", "")
		if err := s.RecordPullRequest(e.ctx, units.PullRequestRecord{SliceID: sl, Number: 4, URL: "https://x.test/pull/4", Head: "h", Base: "main", State: units.PullRequestClosed}); err != nil {
			e.t.Fatal(err)
		}
	})
	d, err := db.Open(e.ctx, e.repo.Dir+"/.ralph/store.db", db.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.SQL().Exec("UPDATE pull_requests SET resolution = 'reopened'"); err != nil {
		t.Fatal(err)
	}
	_ = d.Close()
	_, out, _ := e.status("S-1")
	if !strings.Contains(out, "#4  closed  https://x.test/pull/4  (reopened)\n") {
		t.Errorf("out =\n%s", out)
	}
}

func TestStatusTreeShowsNeedsAttention(t *testing.T) {
	e := newStatusEnv(t)
	e.with(func(s units.Service) {
		b := e.create(s, units.LevelBrief, "", "B", "")
		sl := e.create(s, units.LevelSlice, b, "S", "")
		e.create(s, units.LevelTask, sl, "t", units.StateDone)
		if err := s.RecordPullRequest(e.ctx, units.PullRequestRecord{SliceID: sl, Number: 4, URL: "https://x.test/pull/4", Head: "h", Base: "main", State: units.PullRequestClosed}); err != nil {
			e.t.Fatal(err)
		}
	})
	_, out, _ := e.status()
	requireLine(t, out, "B-1 NEEDS_ATTENTION B")
	requireLine(t, out, "S-1 NEEDS_ATTENTION S 1 DONE")
}
