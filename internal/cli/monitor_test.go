package cli

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/bengearig/ralph/internal/db"
	"github.com/bengearig/ralph/internal/followup"
	"github.com/bengearig/ralph/internal/hosting"
	"github.com/bengearig/ralph/internal/hosting/fake"
	"github.com/bengearig/ralph/internal/project"
	"github.com/bengearig/ralph/internal/testutil"
	"github.com/bengearig/ralph/internal/units"
)

func fixMonitorClock(t *testing.T) {
	t.Helper()
	old := monitorNow
	monitorNow = func() time.Time { return time.Date(2026, 11, 17, 10, 30, 0, 0, time.UTC) }
	t.Cleanup(func() { monitorNow = old })
}

func withStore(t *testing.T, fn func(ctx context.Context, svc units.Service)) {
	t.Helper()
	d, err := openStore(context.Background(), loadedApp(t), db.Options{MustExist: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	fn(context.Background(), units.NewService(d))
}

// monitorEnv has S-1 in review (merged on the host), S-2 waiting on S-1 and
// S-3 with two new review comments.
func monitorEnv(t *testing.T) (*quickEnv, *fake.Provider) {
	t.Helper()
	fixMonitorClock(t)
	e, p, pr := feedbackEnv(t)
	p.Merge(pr.Number)
	e.repo.Git("switch", "main")
	withStore(t, func(ctx context.Context, svc units.Service) {
		mk := func(n units.NewUnit) units.Unit {
			u, err := svc.Create(ctx, n)
			if err != nil {
				t.Fatal(err)
			}
			return u
		}
		s2 := mk(units.NewUnit{Level: units.LevelSlice, ParentID: "B-1", Title: "Second slice", Body: "b"})
		s3 := mk(units.NewUnit{Level: units.LevelSlice, ParentID: "B-1", Title: "Third slice", Body: "b"})
		mk(units.NewUnit{Level: units.LevelTask, ParentID: s3.ID, Title: "Open task", Body: "b", AcceptanceCriteria: "c"})
		if err := svc.AddDependency(ctx, s2.ID, s3.ID); err != nil {
			t.Fatal(err)
		}
		pr3, err := p.CreatePullRequest(ctx, hosting.NewPullRequest{Head: "ralph/s-3", Base: "main", Title: "t3"})
		if err != nil {
			t.Fatal(err)
		}
		if err := svc.RecordPullRequest(ctx, units.PullRequestRecord{SliceID: s3.ID, Number: pr3.Number, URL: pr3.URL, Head: pr3.Head, Base: pr3.Base, State: units.PullRequestOpen}); err != nil {
			t.Fatal(err)
		}
	})
	for _, id := range []string{"c1", "c2"} {
		p.AddFeedback(2, hosting.Feedback{ID: id, Kind: hosting.KindInline, Author: "rev", Body: "fix", Path: "a.go", Line: 1, CreatedAt: time.Now()})
	}
	return e, p
}

func TestMonitorOutputGolden(t *testing.T) {
	_, p := monitorEnv(t)
	code, stdout, stderr := run(t, "monitor")
	pr1, _ := p.GetPullRequest(context.Background(), 1)
	pr2, _ := p.GetPullRequest(context.Background(), 2)
	want := "ralph monitor  2026-11-17 10:30:00 UTC  provider: ok\n" +
		"B-1  IN_PROGRESS  Ship it\n" +
		"  S-1  IN_REVIEW  The slice\n" +
		"       pull request #1 merged on the host (recorded: open) " + pr1.URL + "\n" +
		"       next: record: pull request #1: merged on the host (recorded: open)\n" +
		"       next: close: delete branch ralph/s-1-the-slice\n" +
		"  S-2  EMPTY  Second slice  waiting on S-3\n" +
		"  S-3  ARTICULATING  Third slice\n" +
		"       pull request #2 open " + pr2.URL + "\n" +
		"       feedback: 2 new\n" +
		"       next: feedback: 2 new review feedback items\n" +
		"3 follow-ups; ralph apply applies the ones named by the follow_up.auto setting or by flags\n"
	if code != 0 || stdout != want || stderr != "" {
		t.Errorf("code = %d, stderr = %q\n got:\n%s\nwant:\n%s", code, stderr, stdout, want)
	}
}

func TestMonitorNothingToDo(t *testing.T) {
	fixMonitorClock(t)
	feedbackEnv(t)
	code, stdout, _ := run(t, "monitor")
	if code != 0 || !strings.HasSuffix(stdout, "\nnothing to do\n") || strings.Contains(stdout, "next:") {
		t.Errorf("code = %d, stdout = %q", code, stdout)
	}
}

func TestMonitorFollowupFooterSingularAndPlural(t *testing.T) {
	e, p, pr := feedbackEnv(t)
	fixMonitorClock(t)
	p.Merge(pr.Number)
	_, stdout, _ := run(t, "monitor")
	if !strings.HasSuffix(stdout, "\n2 follow-ups; ralph apply applies the ones named by the follow_up.auto setting or by flags\n") {
		t.Errorf("plural: %q", stdout)
	}
	e.repo.Git("switch", "main")
	e.repo.Git("branch", "-D", "ralph/s-1-the-slice")
	_, stdout, _ = run(t, "monitor")
	if !strings.HasSuffix(stdout, "\n1 follow-up; ralph apply applies the ones named by the follow_up.auto setting or by flags\n") {
		t.Errorf("singular: %q", stdout)
	}
}

func TestMonitorJSON(t *testing.T) {
	monitorEnv(t)
	code, stdout, stderr := run(t, "monitor", "--json")
	if code != 0 || stderr != "" {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	var got struct {
		At       string `json:"at"`
		Provider string `json:"provider"`
		Briefs   []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
			Slices []struct {
				ID          string   `json:"id"`
				WaitingOn   []string `json:"waiting_on"`
				NewFeedback int      `json:"new_feedback"`
				PullRequest *struct {
					Number   int    `json:"number"`
					Recorded string `json:"recorded"`
					Host     string `json:"host"`
				} `json:"pull_request"`
				Followups []struct {
					Kind string `json:"kind"`
				} `json:"followups"`
			} `json:"slices"`
		} `json:"briefs"`
		Followups []struct {
			Kind  string `json:"kind"`
			Slice string `json:"slice"`
		} `json:"followups"`
	}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatal(err)
	}
	if got.At != "2026-11-17T10:30:00Z" || got.Provider != "ok" || len(got.Briefs) != 1 || len(got.Briefs[0].Slices) != 3 || len(got.Followups) != 3 {
		t.Fatalf("view = %+v", got)
	}
	s1, s2, s3 := got.Briefs[0].Slices[0], got.Briefs[0].Slices[1], got.Briefs[0].Slices[2]
	if s1.PullRequest == nil || s1.PullRequest.Host != "merged" || s1.PullRequest.Recorded != "open" || s1.Followups[0].Kind != "record" {
		t.Errorf("s1 = %+v", s1)
	}
	if len(s2.WaitingOn) != 1 || s2.WaitingOn[0] != "S-3" || s2.PullRequest != nil {
		t.Errorf("s2 = %+v", s2)
	}
	if s3.NewFeedback != 2 {
		t.Errorf("s3 = %+v", s3)
	}
}

func TestMonitorNoPollFlag(t *testing.T) {
	_, p := monitorEnv(t)
	before := len(p.Calls())
	code, stdout, _ := run(t, "monitor", "--no-poll")
	if code != 0 || !strings.Contains(stdout, "provider: not polled\n") || strings.Contains(stdout, "on the host") || len(p.Calls()) != before {
		t.Errorf("code = %d, calls = %d (was %d), stdout = %q", code, len(p.Calls()), before, stdout)
	}
}

func TestMonitorIntervalValidation(t *testing.T) {
	feedbackEnv(t)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"monitor", "--watch", "--interval", "9s"}, "ralph: --interval must be at least 10s\n"},
		{[]string{"monitor", "--interval", "30s"}, "ralph: --interval needs --watch\n"},
	} {
		code, stdout, stderr := run(t, tc.args...)
		if code != 1 || stdout != "" || stderr != tc.want {
			t.Errorf("%v: code = %d, stdout = %q, stderr = %q", tc.args, code, stdout, stderr)
		}
	}
}

type lockedBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func TestMonitorWatchRepeatsAndStopsOnSignal(t *testing.T) {
	fixMonitorClock(t)
	feedbackEnv(t)
	old := defaultMonitorInterval
	defaultMonitorInterval = 20 * time.Millisecond
	t.Cleanup(func() { defaultMonitorInterval = old })

	var out, errOut lockedBuffer
	done := make(chan int, 1)
	go func() { done <- Run([]string{"monitor", "--watch"}, &out, &errOut) }()
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
	select {
	case code := <-done:
		if code != 0 || errOut.String() != "" {
			t.Errorf("code = %d, stderr = %q", code, errOut.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("did not stop on SIGINT")
	}
	if !strings.Contains(out.String(), "nothing to do\n\nralph monitor  ") {
		t.Errorf("rounds are not separated by a blank line: %q", out.String())
	}
}

func TestMonitorProviderUnavailableExitsZero(t *testing.T) {
	_, p, _ := feedbackEnv(t)
	fixMonitorClock(t)
	_ = p
	old := providerOpeners["github"]
	providerOpeners["github"] = func(context.Context, *app) (hosting.Provider, error) { return nil, fmt.Errorf("no token") }
	t.Cleanup(func() { providerOpeners["github"] = old })
	code, stdout, stderr := run(t, "monitor")
	if code != 0 || stderr != "" || !strings.HasPrefix(stdout, "ralph monitor  2026-11-17 10:30:00 UTC  provider: unavailable (no token)\n") || !strings.Contains(stdout, "pull request #1 open ") {
		t.Errorf("code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
}

func projectSnapshot(t *testing.T, e *quickEnv) string {
	t.Helper()
	root := e.repo.Dir
	h := sha256.New()
	dbBytes, err := os.ReadFile(project.StorePath(root))
	if err != nil {
		t.Fatal(err)
	}
	h.Write(dbBytes)
	var files []string
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, _ error) error {
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}
		if !d.IsDir() {
			b, _ := os.ReadFile(p)
			files = append(files, fmt.Sprintf("%s %x", p, sha256.Sum256(b)))
		}
		return nil
	})
	return fmt.Sprintf("%x\n%s\n%s\n%s", h.Sum(nil), strings.Join(files, "\n"),
		e.repo.Git("for-each-ref"), e.repo.Git("status", "--porcelain=v1", "--ignored"))
}

func TestMonitorChangesNothing(t *testing.T) {
	e, p := monitorEnv(t)
	before := projectSnapshot(t, e)
	run(t, "monitor")
	if after := projectSnapshot(t, e); after != before {
		t.Errorf("polling a change altered the project:\n%s\n---\n%s", before, after)
	}
	p.Close(2)
	run(t, "monitor", "--all")
	run(t, "monitor", "--no-poll", "--json")
	if after := projectSnapshot(t, e); after != before {
		t.Errorf("the project changed:\n%s\n---\n%s", before, after)
	}
}

func TestMonitorNotInitialized(t *testing.T) {
	inRepo(t)
	code, stdout, stderr := run(t, "monitor")
	want := "ralph: this project has no ralph database: run \"ralph init\" first\n"
	if code != 1 || stdout != "" || stderr != want {
		t.Errorf("code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
}

func TestMonitorOutsideRepository(t *testing.T) {
	testutil.IsolateEnv(t)
	t.Chdir(t.TempDir())
	code, _, stderr := run(t, "monitor")
	if code != 1 || stderr != "ralph: not in a git repository\n" {
		t.Errorf("code = %d, stderr = %q", code, stderr)
	}
}

func TestMonitorHelpListsFlags(t *testing.T) {
	code, stdout, _ := run(t, "monitor", "--help")
	if code != 0 {
		t.Fatalf("code = %d", code)
	}
	for _, s := range []string{"--all", "--watch", "--interval", "--no-poll", "--json"} {
		if !strings.Contains(stdout, s) {
			t.Errorf("help lacks %s:\n%s", s, stdout)
		}
	}
	_ = followup.ProviderOK
}
