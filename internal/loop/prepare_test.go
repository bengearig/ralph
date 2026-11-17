package loop_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bengearig/ralph/internal/db"
	"github.com/bengearig/ralph/internal/doclinks"
	"github.com/bengearig/ralph/internal/documents"
	"github.com/bengearig/ralph/internal/harness"
	"github.com/bengearig/ralph/internal/headless"
	"github.com/bengearig/ralph/internal/loop"
	"github.com/bengearig/ralph/internal/project"
	"github.com/bengearig/ralph/internal/testutil"
	"github.com/bengearig/ralph/internal/units"
	"github.com/bengearig/ralph/internal/verify"
	"github.com/bengearig/ralph/internal/workspace"
)

const (
	loopBranch  = "ralph/s-1-the-slice"
	reasonNoRun = "loop restarted; the interrupted attempt left no commit"
)

type prepFixture struct {
	*fixture
	repo   *testutil.Repo
	deps   loop.Deps
	tasks  []units.Unit
	events []loop.Event
	execs  int
	// verifyFail says which verifier calls (1-based) fail.
	verifyFail map[int]bool
	verifyN    int
	do         func(n int)
}

func newPrepFixture(t *testing.T, taskCount int) *prepFixture {
	t.Helper()
	testutil.IsolateEnv(t)
	repo := testutil.NewRepo(t)
	if _, err := project.EnsureIgnore(repo.Dir, project.LocalIgnoreEntries()); err != nil {
		t.Fatal(err)
	}
	repo.Commit("ignore local ralph files")

	d := testutil.NewDB(t, db.Migrations)
	u, ds := units.New(d), documents.New(d)
	f := &fixture{units: u, docs: ds, links: doclinks.New(u, ds)}
	f.brief = f.unit(t, units.LevelBrief, "", "Ship it", "The brief body.", "")
	f.slice = f.unit(t, units.LevelSlice, f.brief.ID, "The slice", "The slice body.", "")
	p := &prepFixture{fixture: f, repo: repo, verifyFail: map[int]bool{}}
	for i := 0; i < taskCount; i++ {
		task := f.unit(t, units.LevelTask, f.slice.ID, "Task "+string(rune('A'+i)), "Body.", "- works")
		p.tasks = append(p.tasks, task)
		p.move(t, task.ID, units.StateTodo)
	}
	p.do = func(n int) { repo.WriteFile("work"+string(rune('0'+n))+".txt", "work\n") }
	p.deps = loop.Deps{
		Units:     u,
		Workspace: workspace.NewLocal(repo.Dir),
		Prompt:    loop.PromptDeps{Units: u, Links: f.links, Docs: ds},
		Config:    loadConfig(t, repo.Dir, ""), Root: repo.Dir,
		MCP:            harness.MCPServer{Name: "ralph", Command: "/bin/ralph", Args: []string{"mcp"}},
		CommitTemplate: "{{.ID}}: {{.Title}}",
		Verify:         verify.Settings{Commands: []string{"make check"}},
		Verifier: verifierFunc(func() verify.Report {
			p.verifyN++
			if p.verifyFail[p.verifyN] {
				return failingReport("out")
			}
			return verify.Report{Results: []verify.Result{{Command: "make check"}}}
		}),
		Execute: func(context.Context, harness.Adapter, harness.HeadlessRequest, headless.Options) (headless.Result, error) {
			p.execs++
			p.do(p.execs)
			return cleanResult(), nil
		},
		Report: func(e loop.Event) { p.events = append(p.events, e) },
	}
	return p
}

// move walks a task to a state through the allowed transitions.
func (p *prepFixture) move(t *testing.T, id string, to units.State) {
	t.Helper()
	path := map[units.State][]units.State{
		units.StateTodo:       {units.StateTodo},
		units.StateInProgress: {units.StateTodo, units.StateInProgress},
		units.StateDone:       {units.StateTodo, units.StateInProgress, units.StateDone},
		units.StateBlocked:    {units.StateTodo, units.StateBlocked},
		units.StateCancelled:  {units.StateCancelled},
	}[to]
	for _, s := range path {
		cur, err := p.units.Get(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if cur.State == s {
			continue
		}
		if _, err := p.units.SetState(ctx, id, cur.Fingerprint, s, "test: "+string(s)); err != nil {
			t.Fatalf("move %s to %s: %v", id, s, err)
		}
	}
}

func (p *prepFixture) stateOf(t *testing.T, id string) units.State {
	t.Helper()
	u, err := p.units.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	return u.State
}

func (p *prepFixture) prepare(t *testing.T, dry bool) loop.Plan {
	t.Helper()
	plan, err := loop.Prepare(ctx, p.deps, loop.PrepareInput{Slice: p.slice.ID, DryRun: dry})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	t.Cleanup(plan.Close)
	return plan
}

func (p *prepFixture) refusal(t *testing.T, dry bool) error {
	t.Helper()
	plan, err := loop.Prepare(ctx, p.deps, loop.PrepareInput{Slice: p.slice.ID, DryRun: dry})
	if err == nil {
		plan.Close()
		t.Fatal("Prepare succeeded, want a refusal")
	}
	return err
}

func (p *prepFixture) switchToSliceBranch(t *testing.T) {
	t.Helper()
	p.repo.Git("switch", "-c", loopBranch)
}

func TestPrepareRefusesNonSlice(t *testing.T) {
	p := newPrepFixture(t, 1)
	for _, id := range []string{p.brief.ID, p.tasks[0].ID} {
		_, err := loop.Prepare(ctx, p.deps, loop.PrepareInput{Slice: id})
		var ns *loop.NotSliceError
		if !errors.As(err, &ns) || ns.ID != id || !strings.Contains(err.Error(), "not a slice") {
			t.Errorf("%s: err = %v", id, err)
		}
	}
}

func TestPrepareRefusesWaitingSlice(t *testing.T) {
	p := newPrepFixture(t, 1)
	other := p.unit(t, units.LevelSlice, p.brief.ID, "Earlier slice", "Body.", "")
	if err := p.units.AddDependency(ctx, p.slice.ID, other.ID); err != nil {
		t.Fatal(err)
	}
	p.unit(t, units.LevelTask, other.ID, "Unfinished", "Body.", "- x")
	var w *loop.WaitingError
	err := p.refusal(t, false)
	if !errors.As(err, &w) || !strings.Contains(err.Error(), other.ID+" (Earlier slice)") {
		t.Fatalf("err = %v", err)
	}
}

func TestPrepareRefusesWhenLockHeld(t *testing.T) {
	p := newPrepFixture(t, 1)
	first := p.prepare(t, false)
	var held *loop.LockHeldError
	err := p.refusal(t, false)
	if !errors.As(err, &held) || !strings.Contains(err.Error(), "loop.lock") {
		t.Fatalf("err = %v", err)
	}
	first.Close()
}

func TestPrepareLockReleasedOnExit(t *testing.T) {
	p := newPrepFixture(t, 1)
	first := p.prepare(t, false)
	first.Close()
	first.Close()
	p.repo.Git("switch", "main")
	p.repo.Git("branch", "-D", loopBranch)
	again := p.prepare(t, false)
	again.Close()
}

func TestPrepareRefusesUnignoredRuns(t *testing.T) {
	p := newPrepFixture(t, 1)
	if err := os.WriteFile(filepath.Join(p.repo.Dir, ".ralph", ".gitignore"), []byte("/store.db*\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p.repo.Commit("drop the loop entries")
	var ni *loop.NotIgnoredError
	err := p.refusal(t, false)
	if !errors.As(err, &ni) || !strings.Contains(err.Error(), `.ralph/runs/`) || !strings.Contains(err.Error(), `.ralph/loop.lock`) ||
		!strings.Contains(err.Error(), `run "ralph init", commit the .ralph/.gitignore change, and try again`) {
		t.Fatalf("err = %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(p.repo.Dir, ".ralph", "loop.lock")); statErr == nil {
		t.Error("the refused run left a lock file behind")
	}
	if err := p.refusal(t, true); !errors.As(err, &ni) {
		t.Fatalf("dry run err = %v", err)
	}
}

func TestPrepareRefusesOtherBranch(t *testing.T) {
	p := newPrepFixture(t, 1)
	p.repo.Git("switch", "-c", "elsewhere")
	var wb *loop.WrongBranchError
	err := p.refusal(t, false)
	want := `the current branch is "elsewhere", which is neither the base branch "main" nor the slice's branch "` + loopBranch +
		`": run git switch main to start the slice, or git switch ` + loopBranch + ` to continue it`
	if !errors.As(err, &wb) || err.Error() != want {
		t.Fatalf("err = %v\nwant %s", err, want)
	}
}

func TestPrepareRefusesDirtyTree(t *testing.T) {
	p := newPrepFixture(t, 1)
	p.repo.WriteFile(".ralph/runs/S-1/x/record.json", "{}")
	p.repo.WriteFile(".ralph/loop.lock", "")
	p.deps.Workspace = workspace.NewLocal(p.repo.Dir, workspace.WithIgnored([]string{"kept-local.txt"}))
	p.repo.WriteFile("kept-local.txt", "x")
	plan := p.prepare(t, true)
	plan.Close()

	p.repo.WriteFile("b.txt", "x")
	p.repo.WriteFile("a.txt", "x")
	var de *workspace.DirtyError
	err := p.refusal(t, false)
	if !errors.As(err, &de) || strings.Join(de.Paths, ",") != "a.txt,b.txt" {
		t.Fatalf("err = %v", err)
	}
}

func TestPrepareRefusesBaseAheadOfRemote(t *testing.T) {
	p := newPrepFixture(t, 1)
	bare := t.TempDir()
	p.repo.Git("init", "--bare", "-b", "main", bare)
	p.repo.Git("remote", "add", "origin", bare)
	p.repo.Git("push", "origin", "main")
	p.repo.WriteFile("local.txt", "x")
	p.repo.Commit("not pushed")
	var ba *workspace.BaseAheadError
	if err := p.refusal(t, false); !errors.As(err, &ba) {
		t.Fatalf("err = %v", err)
	}
}

func TestPrepareRefusesExistingBranchFromBase(t *testing.T) {
	p := newPrepFixture(t, 1)
	p.repo.Git("branch", loopBranch)
	var be *loop.BranchExistsError
	err := p.refusal(t, false)
	if !errors.As(err, &be) || !strings.Contains(err.Error(), "git switch "+loopBranch) {
		t.Fatalf("err = %v", err)
	}
}

func TestPrepareCreatesBranchFromBase(t *testing.T) {
	p := newPrepFixture(t, 2)
	head := p.repo.Git("rev-parse", "HEAD")
	plan := p.prepare(t, false)
	if plan.Action != loop.Create || plan.Branch != loopBranch || plan.Base != "main" {
		t.Fatalf("plan = %+v", plan)
	}
	if cur := p.repo.Git("branch", "--show-current"); cur != loopBranch {
		t.Errorf("on %q", cur)
	}
	if p.repo.Git("rev-parse", "HEAD") != head {
		t.Error("HEAD moved")
	}
	if plan.Slice.ID != p.slice.ID || plan.Brief.ID != p.brief.ID || len(plan.Tasks) != 2 || len(plan.Runnable) != 2 || plan.Stop != nil {
		t.Errorf("plan = %+v", plan)
	}
}

func TestPrepareResumesOnSliceBranch(t *testing.T) {
	p := newPrepFixture(t, 1)
	p.switchToSliceBranch(t)
	plan := p.prepare(t, false)
	if plan.Action != loop.Resume || plan.Branch != loopBranch {
		t.Fatalf("plan = %+v", plan)
	}
}

func TestPrepareDryRunTakesNoLockAndChangesNothing(t *testing.T) {
	p := newPrepFixture(t, 2)
	held := p.prepare(t, false)
	defer held.Close()
	p.repo.Git("switch", "main")
	p.repo.Git("branch", "-D", loopBranch)
	p.move(t, p.tasks[0].ID, units.StateInProgress)
	before := p.repo.Git("rev-parse", "HEAD")
	plan := p.prepare(t, true)
	if plan.Action != loop.Create {
		t.Errorf("action = %s", plan.Action)
	}
	if cur := p.repo.Git("branch", "--show-current"); cur != "main" || p.repo.Git("rev-parse", "HEAD") != before {
		t.Errorf("checkout moved to %q", cur)
	}
	if exists := p.repo.Git("branch", "--list", loopBranch); exists != "" {
		t.Errorf("branch created: %q", exists)
	}
	if st := p.stateOf(t, p.tasks[0].ID); st != units.StateInProgress {
		t.Errorf("task state = %s", st)
	}
	if len(plan.Runnable) != 2 {
		t.Errorf("runnable = %d", len(plan.Runnable))
	}
}

func TestPrepareRecoversInProgressWithCommit(t *testing.T) {
	p := newPrepFixture(t, 2)
	p.switchToSliceBranch(t)
	p.move(t, p.tasks[0].ID, units.StateInProgress)
	p.repo.WriteFile("a.txt", "a")
	hash := p.repo.Commit("T-1: Task A\n\nRalph-Task: " + p.tasks[0].ID)
	plan := p.prepare(t, false)
	if st := p.stateOf(t, p.tasks[0].ID); st != units.StateDone {
		t.Fatalf("state = %s", st)
	}
	links, err := p.units.Links(ctx, p.tasks[0].ID)
	if err != nil || len(links) != 1 || links[0].Target != "commit:"+hash {
		t.Errorf("links = %+v, %v", links, err)
	}
	notes, _ := p.units.Notes(ctx, p.tasks[0].ID)
	found := false
	for _, n := range notes {
		found = found || n.Body == "recovered: commit "+hash
	}
	if !found {
		t.Errorf("notes = %+v", notes)
	}
	if len(plan.Runnable) != 1 || plan.Runnable[0].ID != p.tasks[1].ID {
		t.Errorf("runnable = %+v", plan.Runnable)
	}
}

func TestPrepareRecoversInProgressWithoutCommit(t *testing.T) {
	p := newPrepFixture(t, 1)
	p.switchToSliceBranch(t)
	p.move(t, p.tasks[0].ID, units.StateInProgress)
	plan := p.prepare(t, false)
	if st := p.stateOf(t, p.tasks[0].ID); st != units.StateTodo {
		t.Fatalf("state = %s", st)
	}
	notes, _ := p.units.Notes(ctx, p.tasks[0].ID)
	if last := notes[len(notes)-1]; !strings.Contains(last.Body, reasonNoRun) {
		t.Errorf("last note = %q", last.Body)
	}
	if len(plan.Runnable) != 1 {
		t.Errorf("runnable = %d", len(plan.Runnable))
	}
}

func TestPrepareStopsAtProposedTask(t *testing.T) {
	p := newPrepFixture(t, 1)
	extra := p.unit(t, units.LevelTask, p.slice.ID, "Not ready", "Body.", "- x")
	p.unit(t, units.LevelTask, p.slice.ID, "After", "Body.", "- x")
	plan := p.prepare(t, false)
	if plan.Stop == nil || plan.Stop.Task != extra.ID || plan.Stop.State != units.StateProposed ||
		!strings.Contains(plan.Stop.Reason, extra.ID+" is PROPOSED") {
		t.Fatalf("stop = %+v", plan.Stop)
	}
	if len(plan.Runnable) != 1 {
		t.Errorf("runnable = %d", len(plan.Runnable))
	}
}

func TestPrepareStopsAtBlockedTaskWithReason(t *testing.T) {
	p := newPrepFixture(t, 2)
	cur, _ := p.units.Get(ctx, p.tasks[0].ID)
	if _, err := p.units.SetState(ctx, cur.ID, cur.Fingerprint, units.StateBlocked, "needs the API key"); err != nil {
		t.Fatal(err)
	}
	plan := p.prepare(t, false)
	want := p.tasks[0].ID + " is BLOCKED: needs the API key"
	if plan.Stop == nil || plan.Stop.Reason != want || len(plan.Runnable) != 0 {
		t.Fatalf("stop = %+v, runnable = %d", plan.Stop, len(plan.Runnable))
	}
}

func TestPrepareSkipsDoneAndCancelled(t *testing.T) {
	p := newPrepFixture(t, 3)
	p.move(t, p.tasks[0].ID, units.StateDone)
	p.move(t, p.tasks[1].ID, units.StateCancelled)
	plan := p.prepare(t, false)
	if len(plan.Runnable) != 1 || plan.Runnable[0].ID != p.tasks[2].ID || plan.Stop != nil {
		t.Fatalf("runnable = %+v, stop = %+v", plan.Runnable, plan.Stop)
	}
}

func TestPrepareNothingToDo(t *testing.T) {
	p := newPrepFixture(t, 1)
	p.move(t, p.tasks[0].ID, units.StateDone)
	plan := p.prepare(t, false)
	if len(plan.Runnable) != 0 || plan.Stop != nil {
		t.Fatalf("plan = %+v", plan)
	}
	sum, err := loop.Run(ctx, p.deps, loop.RunInput{Plan: plan})
	if err != nil || sum.Done != 0 || sum.Stopped != "" {
		t.Fatalf("summary = %+v, %v", sum, err)
	}
}

func TestLoopPrepareRefusesStoppedSlice(t *testing.T) {
	p := newPrepFixture(t, 1)
	other := p.unit(t, units.LevelSlice, p.brief.ID, "Earlier slice", "Body.", "")
	if err := p.units.AddDependency(ctx, p.slice.ID, other.ID); err != nil {
		t.Fatal(err)
	}
	p.unit(t, units.LevelTask, other.ID, "Unfinished", "Body.", "- x")
	err := p.units.RecordPullRequest(ctx, units.PullRequestRecord{
		SliceID: p.slice.ID, Number: 3, URL: "https://x.test/pull/3", Head: loopBranch, Base: "main", State: units.PullRequestClosed,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, dry := range []bool{false, true} {
		got := p.refusal(t, dry)
		want := "S-1 needs a decision: pull request #3 (https://x.test/pull/3) was closed without merging. Choose one: ralph pr reopen S-1, ralph pr rearticulate S-1 or ralph pr cancel S-1"
		if got.Error() != want {
			t.Errorf("dry=%v: err = %q", dry, got)
		}
	}
	if _, err := os.Stat(lockPathFor(p.repo.Dir)); err == nil {
		t.Error("a lock file was left")
	}
}

func lockPathFor(root string) string {
	return filepath.Join(project.RalphDir(root), project.LoopLockFile)
}
