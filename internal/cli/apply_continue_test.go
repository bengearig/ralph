package cli

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/bengearig/ralph/internal/harness"
	"github.com/bengearig/ralph/internal/headless"
	"github.com/bengearig/ralph/internal/units"
)

type continueEnv struct {
	*quickEnv
	reqs []harness.HeadlessRequest
	do   func(ctx context.Context, n int) headless.Result
}

// newContinueEnv has S-1 merged on the host and S-2 READY, with a stub harness.
func newContinueEnv(t *testing.T) *continueEnv {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("verification commands are shell commands")
	}
	fixMonitorClock(t)
	e, p, pr := feedbackEnv(t)
	p.Merge(pr.Number)
	e.repo.Git("switch", "main")
	withStore(t, func(ctx context.Context, svc units.Service) {
		s2, err := svc.Create(ctx, units.NewUnit{Level: units.LevelSlice, ParentID: "B-1", Title: "Second slice", Body: "b"})
		if err != nil {
			t.Fatal(err)
		}
		task, err := svc.Create(ctx, units.NewUnit{Level: units.LevelTask, ParentID: s2.ID, Title: "Second task", Body: "b", AcceptanceCriteria: "c"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := svc.SetState(ctx, task.ID, task.Fingerprint, units.StateTodo, "ready"); err != nil {
			t.Fatal(err)
		}
	})
	withLoopAdapters(t, foundAdapter("claude-code"))
	ce := &continueEnv{quickEnv: e}
	ce.do = func(_ context.Context, n int) headless.Result {
		e.repo.WriteFile(fmt.Sprintf("work%d.txt", n), "work\n")
		return okResult()
	}
	execs := 0
	orig := loopExecute
	t.Cleanup(func() { loopExecute = orig })
	loopExecute = func(ctx context.Context, _ harness.Adapter, req harness.HeadlessRequest, _ headless.Options) (headless.Result, error) {
		execs++
		ce.reqs = append(ce.reqs, req)
		return ce.do(ctx, execs), nil
	}
	return ce
}

func (e *continueEnv) autoStarted(t *testing.T, id string) bool {
	t.Helper()
	var found bool
	withStore(t, func(ctx context.Context, svc units.Service) {
		links, err := svc.Links(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		for _, l := range links {
			if l.Kind == units.LinkExternal && l.Target == "loop:auto-started" {
				found = true
			}
		}
	})
	return found
}

func TestApplyContinueFlag(t *testing.T) {
	e := newContinueEnv(t)
	code, stdout, stderr := run(t, "apply", "--continue")
	if code != 0 || stderr != "" {
		t.Fatalf("code = %d, stderr = %q\n%s", code, stderr, stdout)
	}
	if len(e.reqs) != 1 || !e.autoStarted(t, "S-2") {
		t.Errorf("runs = %d, linked = %v", len(e.reqs), e.autoStarted(t, "S-2"))
	}
	for _, want := range []string{
		"\nstarting the loop for S-2\n  slice:    S-2  Second slice\n",
		"\n  T-2  attempt 1 of 3\n",
		"\n  S-2 done: 1 task, branch ralph/s-2-second-slice\n",
	} {
		if !strings.Contains("\n"+stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}
	code, stdout, _ = run(t, "apply", "--continue")
	if code != 0 || strings.Contains(stdout, "starting the loop") || len(e.reqs) != 1 {
		t.Errorf("second apply started again: code = %d, runs = %d\n%s", code, len(e.reqs), stdout)
	}
}

func TestApplyContinueFromSetting(t *testing.T) {
	e := newContinueEnv(t)
	t.Setenv("RALPH_FOLLOW_UP_AUTO", `["continue"]`)
	code, stdout, stderr := run(t, "apply")
	if code != 0 || stderr != "" || len(e.reqs) != 1 || !strings.Contains(stdout, "starting the loop for S-2\n") {
		t.Errorf("code = %d, stderr = %q, runs = %d\n%s", code, stderr, len(e.reqs), stdout)
	}
}

func TestApplyWithoutContinueDoesNotStartLoop(t *testing.T) {
	e := newContinueEnv(t)
	code, stdout, _ := run(t, "apply")
	if code != 0 || len(e.reqs) != 0 || e.autoStarted(t, "S-2") ||
		!strings.Contains(stdout, "not applied: continue: S-1: start the loop for S-2; apply it with --continue") {
		t.Errorf("code = %d, runs = %d\n%s", code, len(e.reqs), stdout)
	}
}

func TestApplyContinueExitStatus(t *testing.T) {
	e := newContinueEnv(t)
	e.do = func(context.Context, int) headless.Result {
		return headless.Result{Outcome: harness.Outcome{Finished: true}}
	}
	code, stdout, stderr := run(t, "apply", "--continue")
	if code != 2 || stderr != "" || !strings.Contains(stdout, "  T-2  stalled: ") {
		t.Fatalf("stall: code = %d, stderr = %q\n%s", code, stderr, stdout)
	}
	if !e.autoStarted(t, "S-2") {
		t.Error("the marker was removed after the stall")
	}

	e = newContinueEnv(t)
	e.do = func(ctx context.Context, _ int) headless.Result {
		if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
			t.Error(err)
		}
		<-ctx.Done()
		return headless.Result{Canceled: true}
	}
	code, stdout, stderr = run(t, "apply", "--continue")
	if code != 130 || stderr != "" || !strings.Contains(stdout, "  S-2 interrupted: ") {
		t.Errorf("interrupt: code = %d, stderr = %q\n%s", code, stderr, stdout)
	}
}

func TestApplyContinueUsesLoopSettings(t *testing.T) {
	e := newContinueEnv(t)
	t.Setenv("RALPH_LOOP_MODEL", "some-model")
	code, stdout, stderr := run(t, "apply", "--continue")
	if code != 0 || stderr != "" || len(e.reqs) != 1 {
		t.Fatalf("code = %d, stderr = %q\n%s", code, stderr, stdout)
	}
	if r := e.reqs[0]; r.Model != "some-model" || (r.Mode != "" && r.Mode != harness.PermissionMode("restricted")) {
		t.Errorf("request = model %q, mode %q", r.Model, r.Mode)
	}
	if !strings.Contains(stdout, "\n  harness:  claude-code, model some-model, permissions restricted\n") {
		t.Errorf("stdout = %s", stdout)
	}

	e = newContinueEnv(t)
	t.Setenv("RALPH_HEADLESS_PERMISSIONS", "auto")
	code, stdout, _ = run(t, "apply", "--continue")
	if code != 0 || len(e.reqs) != 1 || e.reqs[0].Mode != harness.PermissionMode("auto") ||
		!strings.Contains(stdout, "permissions auto") {
		t.Errorf("code = %d, reqs = %+v\n%s", code, e.reqs, stdout)
	}
}

func TestMonitorShowsContinueFollowup(t *testing.T) {
	e := newContinueEnv(t)
	code, stdout, stderr := run(t, "monitor")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "       next: continue: start the loop for S-2\n") {
		t.Errorf("code = %d, stderr = %q\n%s", code, stderr, stdout)
	}
	if len(e.reqs) != 0 || e.autoStarted(t, "S-2") {
		t.Error("the monitor acted")
	}
}

func TestMonitorWatchApplyContinuesWatchingAfterLoop(t *testing.T) {
	e := newContinueEnv(t)
	t.Setenv("RALPH_FOLLOW_UP_AUTO", `["continue"]`)
	var out, errOut lockedBuffer
	done := make(chan int, 1)
	go func() {
		done <- Run([]string{"monitor", "--watch", "--apply", "--interval", "10s"}, &out, &errOut)
	}()
	deadline := time.After(30 * time.Second)
	for !strings.Contains(out.String(), "S-2 done: 1 task") || !strings.Contains(out.String(), "ralph monitor  ") {
		select {
		case <-deadline:
			t.Fatalf("no loop or view: %q %q", out.String(), errOut.String())
		case code := <-done:
			t.Fatalf("exited early with %d: %q %q", code, out.String(), errOut.String())
		case <-time.After(10 * time.Millisecond):
		}
	}
	if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	select {
	case code := <-done:
		if code != 0 || errOut.String() != "" {
			t.Errorf("code = %d, stderr = %q", code, errOut.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("did not stop on SIGINT")
	}
	if len(e.reqs) != 1 || !strings.Contains(out.String(), "starting the loop for S-2\n") {
		t.Errorf("runs = %d\n%s", len(e.reqs), out.String())
	}
}
