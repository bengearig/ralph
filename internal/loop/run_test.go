package loop_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/bengearig/ralph/internal/harness"
	"github.com/bengearig/ralph/internal/headless"
	"github.com/bengearig/ralph/internal/loop"
	"github.com/bengearig/ralph/internal/units"
)

func TestLoopRunWorksTasksInOrder(t *testing.T) {
	p := newPrepFixture(t, 3)
	plan := p.prepare(t, false)
	sum, err := loop.Run(ctx, p.deps, loop.RunInput{Plan: plan})
	if err != nil {
		t.Fatal(err)
	}
	if sum.Done != 3 || sum.Blocked != 0 || sum.Stopped != "" || sum.Branch != loopBranch {
		t.Fatalf("summary = %+v", sum)
	}
	subjects := strings.Split(p.repo.Git("log", "--reverse", "--format=%s", "main..HEAD"), "\n")
	if len(subjects) != 3 || subjects[0] != "T-1: Task A" || subjects[1] != "T-2: Task B" || subjects[2] != "T-3: Task C" {
		t.Errorf("commits = %q", subjects)
	}
	for _, task := range p.tasks {
		if st := p.stateOf(t, task.ID); st != units.StateDone {
			t.Errorf("%s = %s", task.ID, st)
		}
	}
}

func TestLoopRunStopsOnStall(t *testing.T) {
	p := newPrepFixture(t, 3)
	for _, n := range []int{2, 3, 4} {
		p.verifyFail[n] = true
	}
	plan := p.prepare(t, false)
	sum, err := loop.Run(ctx, p.deps, loop.RunInput{Plan: plan})
	if err != nil {
		t.Fatal(err)
	}
	if sum.Done != 1 || sum.Blocked != 1 || !strings.HasPrefix(sum.Stopped, p.tasks[1].ID+" stalled after 3 failed attempts") {
		t.Fatalf("summary = %+v", sum)
	}
	if st := p.stateOf(t, p.tasks[1].ID); st != units.StateBlocked {
		t.Errorf("second = %s", st)
	}
	if st := p.stateOf(t, p.tasks[2].ID); st != units.StateTodo {
		t.Errorf("third = %s", st)
	}
}

func TestLoopRunStopsAtNotReadyTask(t *testing.T) {
	p := newPrepFixture(t, 1)
	p.unit(t, units.LevelTask, p.slice.ID, "Not ready", "Body.", "- x")
	plan := p.prepare(t, false)
	sum, err := loop.Run(ctx, p.deps, loop.RunInput{Plan: plan})
	if err != nil || sum.Done != 1 || sum.Blocked != 0 || !strings.Contains(sum.Stopped, "is PROPOSED") {
		t.Fatalf("summary = %+v, %v", sum, err)
	}
}

func TestLoopRunHardStopReturnsError(t *testing.T) {
	p := newPrepFixture(t, 2)
	p.deps.Execute = func(context.Context, harness.Adapter, harness.HeadlessRequest, headless.Options) (headless.Result, error) {
		p.repo.Git("switch", "main")
		return cleanResult(), nil
	}
	plan := p.prepare(t, false)
	sum, err := loop.Run(ctx, p.deps, loop.RunInput{Plan: plan})
	if !errors.Is(err, loop.ErrBranchChanged) || sum.Done != 0 {
		t.Fatalf("summary = %+v, err = %v", sum, err)
	}
	if st := p.stateOf(t, p.tasks[1].ID); st != units.StateTodo {
		t.Errorf("second = %s", st)
	}
}
