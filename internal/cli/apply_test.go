package cli

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/bengearig/ralph/internal/hosting"
	"github.com/bengearig/ralph/internal/hosting/fake"
	"github.com/bengearig/ralph/internal/testutil"
	"github.com/bengearig/ralph/internal/units"
)

const sliceBranch = "ralph/s-1-the-slice"

// applyEnv is monitorEnv plus S-4, whose pull request the host closed, and the
// head commit the host reports for the merged pull request of S-1.
func applyEnv(t *testing.T) (*quickEnv, *fake.Provider) {
	t.Helper()
	e, p := monitorEnv(t)
	p.SetHeadCommit(1, strings.TrimSpace(e.repo.Git("rev-parse", sliceBranch)))
	withStore(t, func(ctx context.Context, svc units.Service) {
		s4, err := svc.Create(ctx, units.NewUnit{Level: units.LevelSlice, ParentID: "B-1", Title: "Fourth slice", Body: "b"})
		if err != nil {
			t.Fatal(err)
		}
		pr, err := p.CreatePullRequest(ctx, hosting.NewPullRequest{Head: "ralph/s-4", Base: "main", Title: "t4"})
		if err != nil {
			t.Fatal(err)
		}
		if err := svc.RecordPullRequest(ctx, units.PullRequestRecord{SliceID: s4.ID, Number: pr.Number, URL: pr.URL, Head: pr.Head, Base: pr.Base, State: units.PullRequestOpen}); err != nil {
			t.Fatal(err)
		}
		p.Close(pr.Number)
	})
	return e, p
}

func branchExists(e *quickEnv) bool {
	return strings.Contains(e.repo.Git("branch", "--list", sliceBranch), sliceBranch)
}

func TestApplyDefaultsToSettingKinds(t *testing.T) {
	e, _ := applyEnv(t)
	t.Setenv("RALPH_FOLLOW_UP_AUTO", `["close"]`)
	code, stdout, stderr := run(t, "apply")
	if code != 0 || stderr != "" {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	if branchExists(e) || len(e.tasks(t)) != 2 {
		t.Errorf("close not applied or feedback applied: branch %v, tasks %d", branchExists(e), len(e.tasks(t)))
	}
	if !strings.Contains(stdout, "not applied: feedback: S-3") || !strings.Contains(stdout, "closed 1 slices") {
		t.Errorf("stdout = %q", stdout)
	}
}

func TestApplyFlagsOverrideSetting(t *testing.T) {
	e, _ := applyEnv(t)
	t.Setenv("RALPH_FOLLOW_UP_AUTO", `["close"]`)
	code, stdout, _ := run(t, "apply", "--feedback")
	if code != 0 || !branchExists(e) || len(e.tasks(t)) != 4 {
		t.Errorf("code = %d, branch %v, tasks %d, stdout = %q", code, branchExists(e), len(e.tasks(t)), stdout)
	}
	if !strings.Contains(stdout, "not applied: close: S-1") {
		t.Errorf("stdout = %q", stdout)
	}
}

func TestApplyOutputGolden(t *testing.T) {
	applyEnv(t)
	code, stdout, stderr := run(t, "apply", "--close", "--feedback")
	pr1, pr3 := "https://fake.example/pull/1", "https://fake.example/pull/3"
	want := "recorded S-1: Pull request " + pr1 + " was merged.\n" +
		"S-1: deleted local branch ralph/s-1-the-slice\n" +
		"S-1: main is up to date\n" +
		"created T-3, T-4 from review feedback on S-3\n" +
		"recorded S-4: Pull request " + pr3 + " was closed without merging.\n" +
		"needs you: pull request #3 was closed without merging; choose ralph pr reopen, rearticulate or cancel S-4\n" +
		"apply: recorded 2, created 2 tasks, closed 1 slices\n"
	if code != 0 || stdout != want || stderr != "" {
		t.Errorf("code = %d, stderr = %q\n got:\n%s\nwant:\n%s", code, stderr, stdout, want)
	}
}

func TestApplyNothingAppliedHint(t *testing.T) {
	e, _ := applyEnv(t)
	code, stdout, _ := run(t, "apply")
	hint := "nothing was applied; name what to apply with --close, --feedback or --continue, or set follow_up.auto\n"
	if code != 0 || !strings.HasSuffix(stdout, hint) || !branchExists(e) || len(e.tasks(t)) != 2 {
		t.Errorf("code = %d, stdout = %q", code, stdout)
	}
	code, stdout, _ = run(t, "apply", "--close")
	if code != 0 || strings.Contains(stdout, "nothing was applied") {
		t.Errorf("flagged run: code = %d, stdout = %q", code, stdout)
	}
	code, stdout, _ = run(t, "apply")
	if code != 0 || !strings.HasSuffix(stdout, hint) {
		t.Errorf("third run: code = %d, stdout = %q", code, stdout)
	}
}

func TestApplyJSON(t *testing.T) {
	applyEnv(t)
	code, stdout, stderr := run(t, "apply", "--close", "--json")
	if code != 0 || stderr != "" {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	var got struct {
		Lines           []string `json:"lines"`
		Recorded        int      `json:"recorded"`
		Created         int      `json:"created"`
		Closed          int      `json:"closed"`
		Failures        []string `json:"failures"`
		NothingObserved bool     `json:"nothing_observed"`
	}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatal(err)
	}
	if got.Recorded != 2 || got.Closed != 1 || got.Created != 0 || len(got.Lines) == 0 || got.Failures == nil || got.NothingObserved {
		t.Errorf("result = %+v", got)
	}
}

func TestApplyExitStatuses(t *testing.T) {
	e, _ := applyEnv(t)
	if code, _, _ := run(t, "apply", "--close"); code != 0 {
		t.Errorf("plain run: code = %d", code)
	}
	e.repo.Git("switch", "-c", "ralph/s-1-other")
	if code, _, stderr := run(t, "apply", "S-99"); code != 1 || stderr == "" {
		t.Errorf("unknown slice: code = %d, stderr = %q", code, stderr)
	}
	old := providerOpeners["github"]
	providerOpeners["github"] = func(context.Context, *app) (hosting.Provider, error) { return nil, os.ErrPermission }
	t.Cleanup(func() { providerOpeners["github"] = old })
	code, stdout, _ := run(t, "apply")
	if code != 1 || !strings.Contains(stdout, "provider unavailable") {
		t.Errorf("provider down: code = %d, stdout = %q", code, stdout)
	}
}

func TestApplyRefusalExitsZero(t *testing.T) {
	e, _ := applyEnv(t)
	e.repo.Git("switch", sliceBranch)
	e.repo.WriteFile("more.txt", "more")
	e.repo.Commit("more")
	e.repo.Git("switch", "main")
	code, stdout, _ := run(t, "apply", "--close")
	if code != 0 || !strings.Contains(stdout, "has commits that were not in the merged pull request; kept") || !branchExists(e) {
		t.Errorf("code = %d, stdout = %q", code, stdout)
	}
}

func TestApplyNotInitialized(t *testing.T) {
	inRepo(t)
	code, stdout, stderr := run(t, "apply")
	want := "ralph: this project has no ralph database: run \"ralph init\" first\n"
	if code != 1 || stdout != "" || stderr != want {
		t.Errorf("code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
	testutil.IsolateEnv(t)
	t.Chdir(t.TempDir())
	if code, _, stderr := run(t, "apply"); code != 1 || stderr != "ralph: not in a git repository\n" {
		t.Errorf("outside: code = %d, stderr = %q", code, stderr)
	}
}

func TestApplyHelpListsFlags(t *testing.T) {
	code, stdout, _ := run(t, "apply", "--help")
	if code != 0 {
		t.Fatalf("code = %d", code)
	}
	for _, s := range []string{"--close", "--feedback", "--continue", "--json", "follow_up.auto"} {
		if !strings.Contains(stdout, s) {
			t.Errorf("help lacks %s:\n%s", s, stdout)
		}
	}
}

func TestMonitorApplyNeedsWatch(t *testing.T) {
	feedbackEnv(t)
	code, stdout, stderr := run(t, "monitor", "--apply")
	if code != 1 || stdout != "" || stderr != "ralph: --apply needs --watch; use ralph apply for one run\n" {
		t.Errorf("code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
}

func TestMonitorWatchApplyAppliesAutoKinds(t *testing.T) {
	fixMonitorClock(t)
	e, _ := applyEnv(t)
	t.Setenv("RALPH_FOLLOW_UP_AUTO", `["close","feedback"]`)
	old := defaultMonitorInterval
	defaultMonitorInterval = 20 * time.Millisecond
	t.Cleanup(func() { defaultMonitorInterval = old })

	var out, errOut lockedBuffer
	done := make(chan int, 1)
	go func() { done <- Run([]string{"monitor", "--watch", "--apply"}, &out, &errOut) }()
	deadline := time.After(10 * time.Second)
	for strings.Count(out.String(), "ralph monitor  ") < 2 {
		select {
		case <-deadline:
			t.Fatalf("no second round: %q", out.String())
		case code := <-done:
			t.Fatalf("exited early with %d: %q %q", code, out.String(), errOut.String())
		case <-time.After(5 * time.Millisecond):
		}
	}
	if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	if code := <-done; code != 0 || errOut.String() != "" {
		t.Errorf("code = %d, stderr = %q", code, errOut.String())
	}
	s := out.String()
	if strings.Count(s, "recorded S-1:") != 1 || strings.Count(s, "created T-3, T-4 from review feedback on S-3") != 1 || strings.Count(s, "deleted local branch") != 1 {
		t.Errorf("output = %q", s)
	}
	first := strings.Index(s, "recorded S-1:")
	if first > strings.Index(s, "ralph monitor  ") {
		t.Errorf("applied lines do not come before the view: %q", s)
	}
	if branchExists(e) || len(e.tasks(t)) != 4 {
		t.Errorf("branch %v, tasks %d", branchExists(e), len(e.tasks(t)))
	}
}

func TestMonitorWithoutApplyStillWritesNothing(t *testing.T) {
	e, _ := applyEnv(t)
	t.Setenv("RALPH_FOLLOW_UP_AUTO", `["close","feedback"]`)
	before := projectSnapshot(t, e)
	for _, args := range [][]string{{"monitor"}, {"monitor", "--all"}, {"monitor", "--json"}} {
		if code, _, _ := run(t, args...); code != 0 {
			t.Fatalf("%v: code = %d", args, code)
		}
	}
	if after := projectSnapshot(t, e); after != before {
		t.Errorf("the project changed:\n%s\n---\n%s", before, after)
	}
}
