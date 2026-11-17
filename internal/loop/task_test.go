package loop_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bengearig/ralph/internal/harness"
	"github.com/bengearig/ralph/internal/headless"
	"github.com/bengearig/ralph/internal/loop"
	"github.com/bengearig/ralph/internal/units"
	"github.com/bengearig/ralph/internal/verify"
)

const makeCheckFailure = "verification failed: make check (exit 2)"

func (a *attemptFixture) taskInput(max int) loop.TaskInput {
	return loop.TaskInput{Task: a.task, Slice: a.slice, Branch: sliceBranch, MaxFailures: max}
}

func (a *attemptFixture) runTask(t *testing.T, max int) loop.TaskResult {
	t.Helper()
	res, err := loop.RunTask(ctx, a.deps, a.taskInput(max))
	if err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	return res
}

func failingReport(output string) verify.Report {
	return verify.Report{Results: []verify.Result{{Command: "make check", ExitCode: 2, Output: output}}}
}

// scriptVerifier fails the first n runs and passes afterwards.
func (a *attemptFixture) failFirst(n int, output string) {
	a.deps.Verifier = verifierFunc(func() verify.Report {
		a.verifier.calls++
		if a.verifier.calls <= n {
			return failingReport(output)
		}
		return verify.Report{Results: []verify.Result{{Command: "make check"}}}
	})
}

type verifierFunc func() verify.Report

func (f verifierFunc) Run(context.Context, []string) (verify.Report, error) { return f(), nil }

func (a *attemptFixture) hasNote(t *testing.T, kind units.NoteKind, sub string) bool {
	t.Helper()
	notes, err := a.units.Notes(ctx, a.task.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range notes {
		if n.Kind == kind && strings.Contains(n.Body, sub) {
			return true
		}
	}
	return false
}

func TestRunTaskDoneFirstAttempt(t *testing.T) {
	a := newAttemptFixture(t)
	res := a.runTask(t, 3)
	if res.Outcome != loop.TaskDone || len(res.Attempts) != 1 || res.Reason != "" {
		t.Fatalf("result = %+v", res)
	}
	if a.state(t) != units.StateDone || a.commits() != "2" {
		t.Errorf("state = %s commits = %s", a.state(t), a.commits())
	}
}

func TestRunTaskDoneAfterRetries(t *testing.T) {
	a := newAttemptFixture(t)
	a.failFirst(2, "boom")
	var prompts []string
	inner := a.deps.Execute
	a.deps.Execute = func(c context.Context, ad harness.Adapter, r harness.HeadlessRequest, o headless.Options) (headless.Result, error) {
		prompts = append(prompts, r.Prompt)
		return inner(c, ad, r, o)
	}
	res := a.runTask(t, 3)
	if res.Outcome != loop.TaskDone || len(res.Attempts) != 3 {
		t.Fatalf("result = %s, %d attempts", res.Outcome, len(res.Attempts))
	}
	if strings.Contains(prompts[0], "previous attempt failed") {
		t.Error("first prompt has a retry section")
	}
	for i, want := range []string{"attempt 1 of 3", "attempt 2 of 3"} {
		if p := prompts[i+1]; !strings.Contains(p, "The previous attempt failed ("+want+")") || !strings.Contains(p, makeCheckFailure) {
			t.Errorf("prompt %d lacks the retry section:\n%s", i+2, p)
		}
	}
	if a.state(t) != units.StateDone {
		t.Errorf("state = %s", a.state(t))
	}
}

func TestRunTaskStallsAfterMaxFailures(t *testing.T) {
	a := newAttemptFixture(t)
	a.verifier.report = failingReport("out")
	res := a.runTask(t, 3)
	want := "stalled after 3 failed attempts: " + makeCheckFailure
	if res.Outcome != loop.TaskStalled || res.Reason != want || len(res.Attempts) != 3 {
		t.Fatalf("result = %s %q, %d attempts", res.Outcome, res.Reason, len(res.Attempts))
	}
	if a.state(t) != units.StateBlocked {
		t.Errorf("state = %s", a.state(t))
	}
	if !a.hasNote(t, units.NoteBlocked, want) {
		t.Error("no BLOCKED note with the reason")
	}
	var stalled []loop.Event
	for _, e := range a.events {
		if e.Kind == loop.EventTaskStalled {
			stalled = append(stalled, e)
		}
	}
	if len(stalled) != 1 || stalled[0].Message != want {
		t.Errorf("task_stalled events = %+v", stalled)
	}
	if a.commits() != "1" || a.repo.Git("status", "--short") == "" {
		t.Errorf("commits = %s: the tree was touched", a.commits())
	}
}

func TestRunTaskStallReasonUsesLastFailure(t *testing.T) {
	a := newAttemptFixture(t)
	a.verifier.report = failingReport("out")
	n := 0
	a.do = func() {
		n++
		if n == 3 {
			a.verifier.report = verify.Report{Results: []verify.Result{{Command: "make lint", ExitCode: 1}}}
		}
		a.repo.WriteFile("work.txt", "work\n")
	}
	res := a.runTask(t, 3)
	if want := "stalled after 3 failed attempts: verification failed: make lint (exit 1)"; res.Reason != want {
		t.Errorf("reason = %q, want %q", res.Reason, want)
	}
}

func TestRunTaskMaxFailuresOne(t *testing.T) {
	a := newAttemptFixture(t)
	a.verifier.report = failingReport("out")
	res := a.runTask(t, 1)
	if res.Outcome != loop.TaskStalled || len(res.Attempts) != 1 || a.execs != 1 {
		t.Fatalf("result = %s, %d attempts, %d runs", res.Outcome, len(res.Attempts), a.execs)
	}
	if res.Reason != "stalled after 1 failed attempts: "+makeCheckFailure || a.state(t) != units.StateBlocked {
		t.Errorf("reason = %q state = %s", res.Reason, a.state(t))
	}
}

func TestRunTaskInvalidMaxFailures(t *testing.T) {
	for _, n := range []int{0, -1} {
		a := newAttemptFixture(t)
		if _, err := loop.RunTask(ctx, a.deps, a.taskInput(n)); err == nil {
			t.Errorf("MaxFailures %d accepted", n)
		}
		if a.execs != 0 || a.state(t) != units.StateTodo {
			t.Errorf("work started: runs = %d state = %s", a.execs, a.state(t))
		}
	}
}

func TestRunTaskRetryKeepsFiles(t *testing.T) {
	a := newAttemptFixture(t)
	a.failFirst(1, "x")
	n := 0
	var seen string
	a.do = func() {
		n++
		if n == 1 {
			a.repo.WriteFile("first.txt", "one\n")
			return
		}
		seen = a.repo.Git("status", "--short")
	}
	a.runTask(t, 3)
	if !strings.Contains(seen, "first.txt") {
		t.Errorf("status at attempt 2 = %q", seen)
	}
}

func TestRunTaskRetryCarriesDenials(t *testing.T) {
	a := newAttemptFixture(t)
	a.verifier.report = failingReport("")
	a.result.Outcome.Denials = []harness.Denial{{Tool: "Bash", Target: "rm -rf x", Reason: "not allowed"}}
	res := a.runTask(t, 2)
	if len(res.Attempts[0].Denials) != 1 {
		t.Fatalf("denials = %v", res.Attempts[0].Denials)
	}
	if !strings.Contains(a.req.Prompt, "Denied actions:") || !strings.Contains(a.req.Prompt, "Bash rm -rf x: not allowed") {
		t.Errorf("last prompt lacks the denials:\n%s", a.req.Prompt)
	}
}

func TestRunTaskVerificationOutputCutToLastChars(t *testing.T) {
	long := strings.Repeat("a", 6000) + strings.Repeat("b", 4000)
	multi := strings.Repeat("é", 3000) + strings.Repeat("世", 4000)
	for name, tc := range map[string]struct{ out, want string }{
		"long":  {long, "…" + strings.Repeat("b", 4000)},
		"short": {"short output", "short output"},
		"multi": {multi, "…" + strings.Repeat("世", 4000)},
		"exact": {strings.Repeat("c", 4000), strings.Repeat("c", 4000)},
	} {
		t.Run(name, func(t *testing.T) {
			a := newAttemptFixture(t)
			a.verifier.report = failingReport(tc.out)
			a.runTask(t, 2)
			if !strings.Contains(a.req.Prompt, "\n"+tc.want+"\n") {
				t.Errorf("prompt lacks the expected output (len %d)", len(a.req.Prompt))
			}
			if tc.want != tc.out && strings.Contains(a.req.Prompt, "\n"+tc.out+"\n") {
				t.Error("prompt carries the uncut output")
			}
		})
	}
}

func TestRunTaskBranchChangedIsNotCounted(t *testing.T) {
	a := newAttemptFixture(t)
	a.do = func() { a.repo.Git("switch", "-c", "elsewhere") }
	res, err := loop.RunTask(ctx, a.deps, a.taskInput(3))
	if !errors.Is(err, loop.ErrBranchChanged) || res.Outcome != loop.TaskAborted {
		t.Fatalf("err = %v outcome = %s", err, res.Outcome)
	}
	if a.execs != 1 || a.state(t) == units.StateBlocked {
		t.Errorf("runs = %d state = %s", a.execs, a.state(t))
	}
}

func TestRunTaskInterruptPassesThrough(t *testing.T) {
	a := newAttemptFixture(t)
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	a.deps.Execute = func(context.Context, harness.Adapter, harness.HeadlessRequest, headless.Options) (headless.Result, error) {
		a.execs++
		cancel()
		return headless.Result{Canceled: true}, nil
	}
	res, err := loop.RunTask(cctx, a.deps, a.taskInput(3))
	if !errors.Is(err, context.Canceled) || res.Outcome != loop.TaskAborted {
		t.Fatalf("err = %v outcome = %s", err, res.Outcome)
	}
	if a.execs != 1 || a.state(t) != units.StateTodo {
		t.Errorf("runs = %d state = %s", a.execs, a.state(t))
	}
}

func TestRunTaskExecuteErrorIsNotCounted(t *testing.T) {
	a := newAttemptFixture(t)
	boom := errors.New("claude was not found")
	a.deps.Execute = func(context.Context, harness.Adapter, harness.HeadlessRequest, headless.Options) (headless.Result, error) {
		a.execs++
		return headless.Result{}, boom
	}
	res, err := loop.RunTask(ctx, a.deps, a.taskInput(3))
	if !errors.Is(err, boom) || res.Outcome != loop.TaskAborted {
		t.Fatalf("err = %v outcome = %s", err, res.Outcome)
	}
	if a.execs != 1 || a.state(t) == units.StateBlocked {
		t.Errorf("runs = %d state = %s", a.execs, a.state(t))
	}
}

func TestRunTaskCountsRestartFromZero(t *testing.T) {
	a := newAttemptFixture(t)
	a.verifier.report = failingReport("out")
	if res := a.runTask(t, 2); res.Outcome != loop.TaskStalled {
		t.Fatalf("first outcome = %s", res.Outcome)
	}
	a.setState(t, units.StateTodo)
	a.events = nil
	res := a.runTask(t, 2)
	if res.Outcome != loop.TaskStalled || len(res.Attempts) != 2 {
		t.Fatalf("second result = %s, %d attempts", res.Outcome, len(res.Attempts))
	}
	var first *loop.Event
	for i, e := range a.events {
		if e.Kind == loop.EventAttemptStarted {
			first = &a.events[i]
			break
		}
	}
	if first == nil || first.Attempt != 1 || first.Max != 2 {
		t.Errorf("first event of the second call = %+v", first)
	}
}

func TestRunTaskNoSleepBetweenAttempts(t *testing.T) {
	a := newAttemptFixture(t)
	a.verifier.report = failingReport("out")
	start := time.Now()
	a.runTask(t, 5)
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("took %s", d)
	}
	if a.execs != 5 {
		t.Errorf("runs = %d", a.execs)
	}
}
