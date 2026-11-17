package cli

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"
	"syscall"
	"testing"

	"github.com/bengearig/ralph/internal/db"
	"github.com/bengearig/ralph/internal/harness"
	"github.com/bengearig/ralph/internal/harness/claudecode"
	"github.com/bengearig/ralph/internal/headless"
	"github.com/bengearig/ralph/internal/loop"
	"github.com/bengearig/ralph/internal/testutil"
	"github.com/bengearig/ralph/internal/units"
)

type fakeLoopAdapter struct {
	harness.Adapter
	name string
	det  harness.Detection
}

func (f fakeLoopAdapter) Name() string { return f.name }
func (f fakeLoopAdapter) Detect(context.Context, string) harness.Detection {
	return f.det
}

func withLoopAdapters(t *testing.T, adapters ...harness.Adapter) {
	t.Helper()
	orig := loopAdapters
	t.Cleanup(func() { loopAdapters = orig })
	loopAdapters = func() []harness.Adapter { return adapters }
}

func foundAdapter(name string) harness.Adapter {
	return fakeLoopAdapter{Adapter: claudecode.New(), name: name, det: harness.Detection{Found: true, Version: "1.0"}}
}

func okResult() headless.Result {
	return headless.Result{Outcome: harness.Outcome{Finished: true, Success: true}}
}

type loopEnv struct {
	repo  *testutil.Repo
	slice units.Unit
	tasks []units.Unit
	execs int
	do    func(ctx context.Context, n int) headless.Result
}

const verifyTwo = "verify:\n  commands:\n    - \"true\"\n    - \"test -d .\"\n"

// newLoopEnv makes an initialized project with one fake harness, a slice and TODO tasks.
func newLoopEnv(t *testing.T, project string, titles ...string) *loopEnv {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("verification commands are shell commands")
	}
	repo := inRepo(t)
	if project != "" {
		repo.WriteFile(".ralph/project.yaml", project)
	}
	if code, _, stderr := run(t, "init"); code != 0 {
		t.Fatalf("init: %d %q", code, stderr)
	}
	repo.Commit("ralph files")
	withLoopAdapters(t, foundAdapter("claude-code"))

	e := &loopEnv{repo: repo}
	e.do = func(_ context.Context, n int) headless.Result {
		repo.WriteFile(fmt.Sprintf("work%d.txt", n), "work\n")
		return okResult()
	}
	orig := loopExecute
	t.Cleanup(func() { loopExecute = orig })
	loopExecute = func(ctx context.Context, _ harness.Adapter, _ harness.HeadlessRequest, _ headless.Options) (headless.Result, error) {
		e.execs++
		return e.do(ctx, e.execs), nil
	}

	svc, closeStore := e.units(t)
	defer closeStore()
	ctx := context.Background()
	b, err := svc.Create(ctx, units.NewUnit{Level: units.LevelBrief, Title: "Ship it", Body: "Brief."})
	if err != nil {
		t.Fatal(err)
	}
	if e.slice, err = svc.Create(ctx, units.NewUnit{Level: units.LevelSlice, ParentID: b.ID, Title: "The slice", Body: "Slice."}); err != nil {
		t.Fatal(err)
	}
	for _, title := range titles {
		task, err := svc.Create(ctx, units.NewUnit{Level: units.LevelTask, ParentID: e.slice.ID, Title: title, Body: "Body.", AcceptanceCriteria: "- works"})
		if err != nil {
			t.Fatal(err)
		}
		if task, err = svc.SetState(ctx, task.ID, task.Fingerprint, units.StateTodo, "ready"); err != nil {
			t.Fatal(err)
		}
		e.tasks = append(e.tasks, task)
	}
	return e
}

func (e *loopEnv) units(t *testing.T) (units.Service, func()) {
	t.Helper()
	d, err := openStore(context.Background(), loadedApp(t), db.Options{MustExist: true})
	if err != nil {
		t.Fatal(err)
	}
	return units.NewService(d), func() { _ = d.Close() }
}

func (e *loopEnv) state(t *testing.T, id string) units.State {
	t.Helper()
	svc, closeStore := e.units(t)
	defer closeStore()
	u, err := svc.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return u.State
}

func (e *loopEnv) setState(t *testing.T, id string, to units.State, reason string) {
	t.Helper()
	svc, closeStore := e.units(t)
	defer closeStore()
	u, err := svc.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetState(context.Background(), id, u.Fingerprint, to, reason); err != nil {
		t.Fatal(err)
	}
}

func loopLines(out string) []string { return strings.Split(strings.TrimRight(out, "\n"), "\n") }

func equalLines(t *testing.T, got string, want ...string) {
	t.Helper()
	g := loopLines(got)
	if strings.Join(g, "\n") != strings.Join(want, "\n") {
		t.Fatalf("output:\n%s\nwant:\n%s", strings.Join(g, "\n"), strings.Join(want, "\n"))
	}
}

func TestLoopCommandListedInHelp(t *testing.T) {
	testutil.IsolateEnv(t)
	t.Chdir(t.TempDir())
	_, stdout, _ := run(t, "--help")
	if !strings.Contains(stdout, "  loop ") {
		t.Errorf("root help lacks loop:\n%s", stdout)
	}
	code, stdout, stderr := run(t, "loop", "--help")
	if code != 0 {
		t.Fatalf("code = %d: %s", code, stderr)
	}
	for _, want := range []string{"loop <slice>", "--dry-run", "--harness", "--model", "--permissions"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("help lacks %q:\n%s", want, stdout)
		}
	}
}

func TestLoopCommandNeedsOneSliceArgument(t *testing.T) {
	testutil.IsolateEnv(t)
	t.Chdir(t.TempDir())
	for _, args := range [][]string{{"loop"}, {"loop", "S-1", "S-2"}} {
		if code, _, stderr := run(t, args...); code != 1 || !strings.Contains(stderr, "accepts 1 arg") {
			t.Errorf("%v: code = %d, stderr = %q", args, code, stderr)
		}
	}
}

func TestLoopCommandDryRunOutput(t *testing.T) {
	e := newLoopEnv(t, verifyTwo, "First task", "Second task")
	before := e.repo.Git("rev-parse", "HEAD")
	code, stdout, stderr := run(t, "loop", "s-1", "--dry-run", "--model", "haiku")
	if code != 0 || stderr != "" {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	equalLines(t, stdout,
		"slice:    S-1  The slice",
		"brief:    B-1  Ship it",
		"branch:   ralph/s-1-the-slice (creating from main)",
		"harness:  claude-code, model haiku, permissions restricted",
		"verify:   true",
		"verify:   test -d .",
		"tasks:",
		"  T-1  TODO  First task",
		"  T-2  TODO  Second task",
	)
	if e.repo.Git("rev-parse", "HEAD") != before || e.repo.Git("branch", "--show-current") != "main" || e.execs != 0 {
		t.Error("the dry run changed something")
	}
	if e.repo.Git("status", "--short") != "" {
		t.Errorf("the tree is dirty: %s", e.repo.Git("status", "--short"))
	}
}

func TestLoopCommandHeaderOmitsMissingBrief(t *testing.T) {
	var out strings.Builder
	plan := loop.Plan{Slice: units.Unit{ID: "S-1", Title: "The slice"}, Base: "main", Branch: "ralph/s-1-the-slice", Action: loop.Create}
	printLoopHeader(&out, plan, loopChoice{adapter: foundAdapter("opencode"), mode: harness.PermissionAuto}, nil)
	equalLines(t, out.String(),
		"slice:    S-1  The slice",
		"branch:   ralph/s-1-the-slice (creating from main)",
		"harness:  opencode, model (harness default), permissions auto",
		"verify:   none configured",
	)
}

func TestLoopCommandDryRunResumingWithStop(t *testing.T) {
	e := newLoopEnv(t, "", "Only task")
	e.repo.Git("switch", "-c", "ralph/s-1-the-slice")
	e.setState(t, "T-1", units.StateProposed, "rework")
	code, stdout, stderr := run(t, "loop", "S-1", "--dry-run")
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	equalLines(t, stdout,
		"slice:    S-1  The slice",
		"brief:    B-1  Ship it",
		"branch:   ralph/s-1-the-slice (resuming)",
		"harness:  claude-code, model (harness default), permissions restricted",
		"verify:   none configured",
		"tasks:",
		"  T-1  PROPOSED  Only task",
		"would stop at T-1: PROPOSED: it must be articulated and made TODO before the loop can run it",
	)
}

func TestLoopCommandHarnessSelection(t *testing.T) {
	newLoopEnv(t, "", "Task")
	headline := func(args ...string) (int, string, string) {
		return run(t, append([]string{"loop", "S-1", "--dry-run"}, args...)...)
	}
	pick := func(out string) string {
		for _, l := range loopLines(out) {
			if strings.HasPrefix(l, "harness:") {
				return strings.Fields(l)[1]
			}
		}
		return ""
	}

	withLoopAdapters(t, foundAdapter("claude-code"), foundAdapter("opencode"))
	code, _, stderr := headline()
	if code != 1 || !strings.Contains(stderr, "loop.harness") || !strings.Contains(stderr, "--harness") {
		t.Errorf("several found: code = %d, stderr = %q", code, stderr)
	}
	if code, out, stderr := headline("--harness", "opencode"); code != 0 || pick(out) != "opencode," {
		t.Errorf("flag: code = %d, out = %q, stderr = %q", code, out, stderr)
	}
	t.Setenv("RALPH_LOOP_HARNESS", "claude-code")
	if code, out, stderr := headline(); code != 0 || pick(out) != "claude-code," {
		t.Errorf("setting: code = %d, out = %q, stderr = %q", code, out, stderr)
	}
	if code, out, stderr := headline("--harness", "opencode"); code != 0 || pick(out) != "opencode," {
		t.Errorf("flag over setting: code = %d, out = %q, stderr = %q", code, out, stderr)
	}
	t.Setenv("RALPH_LOOP_HARNESS", "")

	withLoopAdapters(t, foundAdapter("claude-code"), fakeLoopAdapter{Adapter: claudecode.New(), name: "opencode"})
	if code, out, stderr := headline(); code != 0 || pick(out) != "claude-code," {
		t.Errorf("one found: code = %d, out = %q, stderr = %q", code, out, stderr)
	}
	withLoopAdapters(t, fakeLoopAdapter{Adapter: claudecode.New(), name: "claude-code"})
	if code, _, stderr := headline(); code != 1 || !strings.Contains(stderr, "no harness was found") {
		t.Errorf("none found: code = %d, stderr = %q", code, stderr)
	}
	withLoopAdapters(t, fakeLoopAdapter{Adapter: claudecode.New(), name: "claude-code",
		det: harness.Detection{Found: true, Unsupported: "version too old"}})
	if code, _, stderr := headline(); code != 1 || !strings.Contains(stderr, "version too old") {
		t.Errorf("unsupported: code = %d, stderr = %q", code, stderr)
	}
	if code, _, stderr := headline("--harness", "claude-code"); code != 1 || !strings.Contains(stderr, "version too old") {
		t.Errorf("unsupported by flag: code = %d, stderr = %q", code, stderr)
	}
}

func TestLoopCommandPermissionsFlagOverridesSetting(t *testing.T) {
	newLoopEnv(t, "", "Task")
	t.Setenv("RALPH_HEADLESS_PERMISSIONS", "auto")
	_, out, _ := run(t, "loop", "S-1", "--dry-run")
	if !strings.Contains(out, "permissions auto") {
		t.Errorf("setting not used:\n%s", out)
	}
	_, out, _ = run(t, "loop", "S-1", "--dry-run", "--permissions", "restricted")
	if !strings.Contains(out, "permissions restricted") {
		t.Errorf("flag not used:\n%s", out)
	}
	if code, _, stderr := run(t, "loop", "S-1", "--dry-run", "--permissions", "yolo"); code != 1 || !strings.Contains(stderr, "restricted or auto") {
		t.Errorf("bad mode: code = %d, stderr = %q", code, stderr)
	}
}

func TestLoopCommandRunOutputLines(t *testing.T) {
	e := newLoopEnv(t, verifyTwo, "First task", "Second task")
	code, stdout, stderr := run(t, "loop", "S-1")
	if code != 0 || stderr != "" {
		t.Fatalf("code = %d, stderr = %q\n%s", code, stderr, stdout)
	}
	hashes := strings.Split(e.repo.Git("log", "--reverse", "--format=%H", "main..HEAD"), "\n")
	if len(hashes) != 2 {
		t.Fatalf("commits = %q", hashes)
	}
	equalLines(t, stdout,
		"slice:    S-1  The slice",
		"brief:    B-1  Ship it",
		"branch:   ralph/s-1-the-slice (creating from main)",
		"harness:  claude-code, model (harness default), permissions restricted",
		"verify:   true",
		"verify:   test -d .",
		"T-1  attempt 1 of 3",
		"T-1  done  commit "+hashes[0][:12]+"  0s",
		"T-2  attempt 1 of 3",
		"T-2  done  commit "+hashes[1][:12]+"  0s",
		"S-1 done: 2 tasks, branch ralph/s-1-the-slice",
	)
	if e.state(t, "T-1") != units.StateDone || e.state(t, "T-2") != units.StateDone {
		t.Error("tasks are not done")
	}
}

func TestLoopCommandStallOutputAndExitStatus(t *testing.T) {
	e := newLoopEnv(t, "verify:\n  commands:\n    - \"false\"\n", "First task", "Second task")
	code, stdout, stderr := run(t, "loop", "S-1")
	if code != 2 || stderr != "" {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	fail := "verification failed: false (exit 1)"
	stall := "stalled after 3 failed attempts: " + fail
	equalLines(t, stdout,
		"slice:    S-1  The slice",
		"brief:    B-1  Ship it",
		"branch:   ralph/s-1-the-slice (creating from main)",
		"harness:  claude-code, model (harness default), permissions restricted",
		"verify:   false",
		"T-1  attempt 1 of 3",
		"T-1  attempt 1 of 3 failed: "+fail,
		"T-1  attempt 2 of 3",
		"T-1  attempt 2 of 3 failed: "+fail,
		"T-1  attempt 3 of 3",
		"T-1  attempt 3 of 3 failed: "+fail,
		"T-1  stalled: "+stall,
		"S-1 stopped: T-1 "+stall,
		"the tree was left as the agent left it; look at the run records under .ralph/runs/S-1/",
		"fix or revert it, unblock T-1 with a reason, and run the loop again",
	)
	if e.state(t, "T-1") != units.StateBlocked || e.state(t, "T-2") != units.StateTodo {
		t.Error("unexpected states")
	}
}

func TestLoopCommandInterruptExitStatus(t *testing.T) {
	e := newLoopEnv(t, "", "First task", "Second task")
	e.do = func(ctx context.Context, _ int) headless.Result {
		if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
			t.Error(err)
		}
		<-ctx.Done()
		return headless.Result{Canceled: true}
	}
	code, stdout, stderr := run(t, "loop", "S-1")
	if code != 130 || stderr != "" {
		t.Fatalf("code = %d, stderr = %q\n%s", code, stderr, stdout)
	}
	if !strings.Contains(stdout, "S-1 interrupted: the tree has changes, and the next run refuses until they are committed or discarded") {
		t.Errorf("stdout = %q", stdout)
	}
	if e.state(t, "T-1") != units.StateTodo || e.execs != 1 {
		t.Errorf("state = %s, execs = %d", e.state(t, "T-1"), e.execs)
	}
}

func TestLoopCommandRefusalExitStatusOne(t *testing.T) {
	e := newLoopEnv(t, "", "Task")
	code, stdout, stderr := run(t, "loop", "T-1")
	if code != 1 || stdout != "" || !strings.Contains(stderr, "ralph: T-1 is a task, not a slice") {
		t.Errorf("not a slice: code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
	e.repo.WriteFile("dirty.txt", "x")
	code, stdout, stderr = run(t, "loop", "S-1", "--dry-run")
	if code != 1 || stdout != "" || !strings.Contains(stderr, "dirty.txt") {
		t.Errorf("dirty: code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
}

func TestLoopCommandNotInitialized(t *testing.T) {
	inRepo(t)
	code, _, stderr := run(t, "loop", "S-1")
	if code != 1 || stderr != "ralph: this project has no ralph database: run \"ralph init\" first\n" {
		t.Errorf("code = %d, stderr = %q", code, stderr)
	}
}

func TestLoopCommandOutsideRepository(t *testing.T) {
	testutil.IsolateEnv(t)
	t.Chdir(t.TempDir())
	code, _, stderr := run(t, "loop", "S-1")
	if code != 1 || stderr != "ralph: not in a git repository\n" {
		t.Errorf("code = %d, stderr = %q", code, stderr)
	}
}

func TestExitStatusErrorType(t *testing.T) {
	e := newLoopEnv(t, "", "First task")
	e.setState(t, "T-1", units.StateBlocked, "needs a key")
	code, stdout, stderr := run(t, "loop", "S-1")
	if code != 2 || stderr != "" {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	if !strings.Contains(stdout, "S-1 stopped: T-1 is BLOCKED: needs a key\n") || strings.Contains(stdout, "the tree was left") {
		t.Errorf("stdout = %q", stdout)
	}
	if e.execs != 0 {
		t.Errorf("execs = %d", e.execs)
	}
}
