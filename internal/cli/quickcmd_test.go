package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bengearig/ralph/internal/db"
	"github.com/bengearig/ralph/internal/mcpserver"
	"github.com/bengearig/ralph/internal/testutil"
	"github.com/bengearig/ralph/internal/units"
)

type quickEnv struct {
	repo   *testutil.Repo
	origin string

	mu       sync.Mutex
	requests []string
	posted   []map[string]any
}

func (e *quickEnv) handler(w http.ResponseWriter, r *http.Request) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.requests = append(e.requests, r.Method+" "+r.URL.Path)
	const path = "/repos/o/r/pulls"
	switch {
	case r.Method == http.MethodGet && r.URL.Path == path:
		_, _ = w.Write([]byte("[]"))
	case r.Method == http.MethodPost && r.URL.Path == path:
		var in map[string]any
		_ = json.NewDecoder(r.Body).Decode(&in)
		e.posted = append(e.posted, in)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"number": 7, "html_url": "https://example.com/o/r/pull/7", "state": "open",
			"draft": in["draft"], "title": in["title"], "body": in["body"],
		})
	default:
		http.NotFound(w, r)
	}
}

func (e *quickEnv) requestCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.requests)
}

func newQuickEnv(t *testing.T, project string) *quickEnv {
	t.Helper()
	repo := inRepo(t)
	e := &quickEnv{repo: repo, origin: filepath.Join(t.TempDir(), "origin.git")}
	srv := httptest.NewServer(http.HandlerFunc(e.handler))
	t.Cleanup(srv.Close)
	t.Setenv("RALPH_GITHUB_API_URL", srv.URL)
	t.Setenv("GITHUB_TOKEN", "test-token")

	repo.Git("init", "--bare", "-b", "main", e.origin)
	repo.Git("remote", "add", "origin", e.origin)
	repo.WriteFile(".ralph/project.yaml", "github:\n  owner: o\n  repo: r\n"+project)
	if code, _, stderr := run(t, "init"); code != 0 {
		t.Fatalf("init: %d %q", code, stderr)
	}
	repo.Commit("ralph files")
	repo.Git("push", "origin", "main")
	return e
}

func (e *quickEnv) start(t *testing.T, args ...string) string {
	t.Helper()
	code, stdout, stderr := run(t, append([]string{"quick", "start"}, args...)...)
	if code != 0 {
		t.Fatalf("quick start: %d %q", code, stderr)
	}
	return stdout
}

func (e *quickEnv) tasks(t *testing.T) []units.Unit {
	t.Helper()
	a := loadedApp(t)
	d, err := openStore(context.Background(), a, db.Options{MustExist: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	list, err := units.NewService(d).List(context.Background(), units.Filter{Level: units.LevelTask})
	if err != nil {
		t.Fatal(err)
	}
	return list
}

func (e *quickEnv) remoteHasBranch(branch string) bool {
	return e.repo.Git("ls-remote", "--heads", "origin", branch) != ""
}

func startedBranch(t *testing.T, out string) string {
	t.Helper()
	for _, l := range strings.Split(out, "\n") {
		if rest, ok := strings.CutPrefix(l, "branch: "); ok {
			name, _, _ := strings.Cut(rest, " (")
			return name
		}
	}
	t.Fatalf("no branch line in %q", out)
	return ""
}

func TestQuickHelpListsSubcommands(t *testing.T) {
	testutil.IsolateEnv(t)
	t.Chdir(t.TempDir())
	code, stdout, stderr := run(t, "quick")
	if code != 0 {
		t.Fatalf("code = %d: %s", code, stderr)
	}
	for _, want := range []string{"start", "finish"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("help lacks %q:\n%s", want, stdout)
		}
	}
}

func TestQuickStartCommand(t *testing.T) {
	e := newQuickEnv(t, "")
	out := e.start(t, "Fix the thing")
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 3 || lines[0] != "started T-1: Fix the thing" || lines[2] != "verification: none configured" {
		t.Fatalf("output = %q", out)
	}
	branch := startedBranch(t, out)
	if lines[1] != "branch: "+branch+" (base main)" {
		t.Errorf("branch line = %q", lines[1])
	}
	if cur := e.repo.Git("branch", "--show-current"); cur != branch {
		t.Errorf("current branch = %q, want %q", cur, branch)
	}
	tasks := e.tasks(t)
	if len(tasks) != 1 || tasks[0].State != units.StateInProgress {
		t.Errorf("tasks = %+v", tasks)
	}
}

func TestQuickStartPrintsConfiguredVerification(t *testing.T) {
	e := newQuickEnv(t, "verify:\n  commands:\n    - make test\n    - make lint\n")
	out := e.start(t, "Fix it")
	for _, want := range []string{"verification (project (.ralph/project.yaml:", "):\n  make test\n  make lint\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

func TestQuickStartBodyAndCriteriaFlags(t *testing.T) {
	e := newQuickEnv(t, "")
	e.start(t, "--body", "the body", "--criteria", "it works", "Fix it")
	got := e.tasks(t)[0]
	if got.Body != "the body" || got.AcceptanceCriteria != "it works" {
		t.Errorf("body = %q, criteria = %q", got.Body, got.AcceptanceCriteria)
	}
}

func TestQuickStartBodyFile(t *testing.T) {
	e := newQuickEnv(t, "")
	file := filepath.Join(t.TempDir(), "body.md")
	crit := filepath.Join(t.TempDir(), "crit.md")
	if err := os.WriteFile(file, []byte("from a file"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(crit, []byte("criteria from a file"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.start(t, "--body-file", file, "--criteria-file", crit, "Fix it")
	got := e.tasks(t)[0]
	if got.Body != "from a file" || got.AcceptanceCriteria != "criteria from a file" {
		t.Errorf("body = %q, criteria = %q", got.Body, got.AcceptanceCriteria)
	}
}

func TestQuickStartBodyFromStdin(t *testing.T) {
	e := newQuickEnv(t, "")
	var out, errOut bytes.Buffer
	cmd := NewRootCommand()
	cmd.SetArgs([]string{"quick", "start", "--body-file", "-", "Fix it"})
	cmd.SetIn(strings.NewReader("body on stdin"))
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("%v %s", err, errOut.String())
	}
	if got := e.tasks(t)[0].Body; got != "body on stdin" {
		t.Errorf("body = %q", got)
	}
}

func TestQuickStartConflictingFlagsRejected(t *testing.T) {
	e := newQuickEnv(t, "")
	for _, args := range [][]string{
		{"--body", "a", "--body-file", "b"},
		{"--criteria", "a", "--criteria-file", "b"},
	} {
		code, _, stderr := run(t, append([]string{"quick", "start"}, append(args, "Fix it")...)...)
		if code != 1 || !strings.Contains(stderr, "none of the others can be") {
			t.Errorf("%v: code = %d, stderr = %q", args, code, stderr)
		}
	}
	if n := len(e.tasks(t)); n != 0 {
		t.Errorf("%d tasks created", n)
	}
}

func TestQuickStartNeedsExactlyOneTitle(t *testing.T) {
	newQuickEnv(t, "")
	for _, args := range [][]string{{}, {"one", "two"}} {
		code, stdout, stderr := run(t, append([]string{"quick", "start"}, args...)...)
		if code != 1 || stdout != "" || !strings.Contains(stderr, "arg") {
			t.Errorf("%v: code = %d, stdout = %q, stderr = %q", args, code, stdout, stderr)
		}
	}
}

func TestQuickStartDirtyTree(t *testing.T) {
	e := newQuickEnv(t, "")
	e.repo.WriteFile("changed.txt", "x")
	code, _, stderr := run(t, "quick", "start", "Fix it")
	if code != 1 || !strings.Contains(stderr, "changed.txt") {
		t.Errorf("code = %d, stderr = %q", code, stderr)
	}
	if n := len(e.tasks(t)); n != 0 {
		t.Errorf("%d tasks created", n)
	}
}

func TestQuickStartNotInitialized(t *testing.T) {
	inRepo(t)
	code, stdout, stderr := run(t, "quick", "start", "Fix it")
	want := "ralph: this project has no ralph database: run \"ralph init\" first\n"
	if code != 1 || stdout != "" || stderr != want {
		t.Errorf("code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
}

func TestQuickStartOutsideRepository(t *testing.T) {
	testutil.IsolateEnv(t)
	t.Chdir(t.TempDir())
	code, _, stderr := run(t, "quick", "start", "Fix it")
	if code != 1 || stderr != "ralph: not in a git repository\n" {
		t.Errorf("code = %d, stderr = %q", code, stderr)
	}
}

func TestQuickStartJSON(t *testing.T) {
	e := newQuickEnv(t, "verify:\n  commands:\n    - make test\n")
	code, stdout, stderr := run(t, "quick", "start", "--json", "--body", "why", "Fix it")
	if code != 0 {
		t.Fatalf("code = %d: %s", code, stderr)
	}
	var got mcpserver.QuickStartResult
	dec := json.NewDecoder(strings.NewReader(stdout))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&got); err != nil {
		t.Fatalf("%v: %s", err, stdout)
	}
	if got.Task.ID != "T-1" || got.Task.Title != "Fix it" || got.Task.Body != "why" || got.Task.State != "IN_PROGRESS" {
		t.Errorf("task = %+v", got.Task)
	}
	if cur := e.repo.Git("branch", "--show-current"); got.Branch != cur || got.Base != "main" {
		t.Errorf("branch = %q (checkout %q), base = %q", got.Branch, cur, got.Base)
	}
	if len(got.Verification.Commands) != 1 || got.Verification.Commands[0] != "make test" || got.StartedFrom == "" {
		t.Errorf("result = %+v", got)
	}
}

func (e *quickEnv) finishAfterNewFile(t *testing.T, startArgs []string, finishArgs ...string) (code int, stdout, stderr string) {
	t.Helper()
	e.start(t, startArgs...)
	e.repo.WriteFile("feature.txt", "work\n")
	return run(t, append([]string{"quick", "finish"}, finishArgs...)...)
}

func TestQuickFinishCommand(t *testing.T) {
	e := newQuickEnv(t, "")
	code, stdout, stderr := e.finishAfterNewFile(t, []string{"Fix it"})
	if code != 0 {
		t.Fatalf("code = %d: %s", code, stderr)
	}
	branch := e.repo.Git("branch", "--show-current")
	hash := e.repo.Git("rev-parse", "HEAD")
	want := "verification: none configured\n" +
		"delivered T-1\n" +
		"commit: " + hash + "\n" +
		"pull request: https://example.com/o/r/pull/7 (draft)\n" +
		"nothing has been merged: a person reviews the pull request\n" +
		"return to the base branch with: git switch main\n"
	if stdout != want {
		t.Errorf("stdout = %q, want %q", stdout, want)
	}
	if !e.remoteHasBranch(branch) {
		t.Errorf("origin lacks %s", branch)
	}
	if len(e.posted) != 1 {
		t.Fatalf("posted = %v", e.posted)
	}
	if e.posted[0]["draft"] != true || e.posted[0]["head"] != branch {
		t.Errorf("request = %v", e.posted[0])
	}
	if tasks := e.tasks(t); tasks[0].State != units.StateDone {
		t.Errorf("state = %s", tasks[0].State)
	}
}

func TestQuickFinishPrintsVerificationBeforeRunning(t *testing.T) {
	e := newQuickEnv(t, "verify:\n  commands:\n    - echo ran-one\n    - echo ran-two\n")
	code, stdout, stderr := e.finishAfterNewFile(t, []string{"Fix it"})
	if code != 0 {
		t.Fatalf("code = %d: %s", code, stderr)
	}
	block := strings.Index(stdout, "verification (project (.ralph/project.yaml:")
	cmds := strings.Index(stdout, "):\n  echo ran-one\n  echo ran-two\n")
	delivered := strings.Index(stdout, "delivered T-1")
	if block < 0 || cmds < block || delivered < cmds {
		t.Errorf("stdout = %q", stdout)
	}
}

func TestQuickFinishVerificationFailure(t *testing.T) {
	e := newQuickEnv(t, "verify:\n  commands:\n    - echo some output; echo more output; exit 4\n")
	code, stdout, stderr := e.finishAfterNewFile(t, []string{"Fix it"})
	if code != 1 {
		t.Fatalf("code = %d", code)
	}
	for _, want := range []string{"verification failed: echo some output; echo more output; exit 4 (exit 4)\n", "some output\nmore output\n"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q: %q", want, stderr)
		}
	}
	if strings.Contains(stderr, "ralph:") || strings.Contains(stdout, "delivered") {
		t.Errorf("stdout = %q, stderr = %q", stdout, stderr)
	}
	if n := e.requestCount(); n != 0 {
		t.Errorf("handler saw %d requests", n)
	}
	if branch := e.repo.Git("branch", "--show-current"); e.remoteHasBranch(branch) {
		t.Errorf("origin has %s", branch)
	}
	if got := e.tasks(t)[0].State; got != units.StateInProgress {
		t.Errorf("state = %s", got)
	}
	if e.repo.Git("status", "--porcelain") == "" {
		t.Error("the work was committed")
	}
}

func TestQuickFinishJSON(t *testing.T) {
	e := newQuickEnv(t, "verify:\n  commands:\n    - echo ok\n")
	code, stdout, stderr := e.finishAfterNewFile(t, []string{"Fix it"}, "--json")
	if code != 0 {
		t.Fatalf("code = %d: %s", code, stderr)
	}
	var got mcpserver.QuickFinishResult
	dec := json.NewDecoder(strings.NewReader(stdout))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&got); err != nil {
		t.Fatalf("%v: %s", err, stdout)
	}
	if got.Task.ID != "T-1" || got.Task.State != "DONE" || !got.Committed || got.Commit != e.repo.Git("rev-parse", "HEAD") {
		t.Errorf("result = %+v", got)
	}
	if got.PullRequest.URL != "https://example.com/o/r/pull/7" || !got.PullRequest.Draft || got.Base != "main" {
		t.Errorf("result = %+v", got)
	}
	if !got.Verification.Configured || len(got.Verification.Results) != 1 || !got.Verification.Results[0].Passed {
		t.Errorf("verification = %+v", got.Verification)
	}
}

func TestQuickFinishWithID(t *testing.T) {
	e := newQuickEnv(t, "")
	code, stdout, stderr := e.finishAfterNewFile(t, []string{"Fix it"}, "T-1")
	if code != 0 || !strings.Contains(stdout, "delivered T-1\n") {
		t.Errorf("code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
}

func TestQuickFinishSummaryFlag(t *testing.T) {
	e := newQuickEnv(t, "")
	code, _, stderr := e.finishAfterNewFile(t, []string{"Fix it"}, "--summary", "Changed the widget")
	if code != 0 {
		t.Fatalf("code = %d: %s", code, stderr)
	}
	if body, _ := e.posted[0]["body"].(string); !strings.Contains(body, "Changed the widget") {
		t.Errorf("body = %q", body)
	}
}

func TestQuickFinishSummaryFromStdin(t *testing.T) {
	e := newQuickEnv(t, "")
	e.start(t, "Fix it")
	e.repo.WriteFile("feature.txt", "work\n")
	var out, errOut bytes.Buffer
	cmd := NewRootCommand()
	cmd.SetArgs([]string{"quick", "finish", "--summary-file", "-"})
	cmd.SetIn(strings.NewReader("Summary on stdin"))
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("%v %s", err, errOut.String())
	}
	if body, _ := e.posted[0]["body"].(string); !strings.Contains(body, "Summary on stdin") {
		t.Errorf("body = %q", body)
	}
}

func TestQuickFinishAlreadyCommittedAndOpen(t *testing.T) {
	e := newQuickEnv(t, "")
	e.start(t, "Fix it")
	e.repo.WriteFile("feature.txt", "work\n")
	e.repo.Commit("manual commit")
	code, stdout, stderr := run(t, "quick", "finish")
	if code != 0 {
		t.Fatalf("code = %d: %s", code, stderr)
	}
	if want := "commit: already committed (" + e.repo.Git("rev-parse", "HEAD") + ")\n"; !strings.Contains(stdout, want) {
		t.Errorf("stdout lacks %q: %q", want, stdout)
	}
}

func TestQuickFinishWrongBranch(t *testing.T) {
	e := newQuickEnv(t, "")
	branch := startedBranch(t, e.start(t, "Fix it"))
	e.repo.Git("switch", "main")
	code, _, stderr := run(t, "quick", "finish", "T-1")
	if code != 1 || !strings.Contains(stderr, "git switch "+branch) {
		t.Errorf("code = %d, stderr = %q", code, stderr)
	}
}

func TestQuickFinishInterruptKeepsTaskInProgress(t *testing.T) {
	e := newQuickEnv(t, "verify:\n  commands:\n    - sleep 30\n")
	e.start(t, "Fix it")
	e.repo.WriteFile("feature.txt", "work\n")
	head := e.repo.Git("rev-parse", "HEAD")

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(500*time.Millisecond, cancel)
	var out, errOut bytes.Buffer
	cmd := NewRootCommand()
	cmd.SetArgs([]string{"quick", "finish"})
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	began := time.Now()
	if err := cmd.ExecuteContext(ctx); err == nil {
		t.Fatalf("finish succeeded: %s", out.String())
	}
	if time.Since(began) > 20*time.Second {
		t.Error("the verification command was not stopped")
	}
	if got := e.tasks(t)[0].State; got != units.StateInProgress {
		t.Errorf("state = %s", got)
	}
	if e.repo.Git("rev-parse", "HEAD") != head || e.requestCount() != 0 {
		t.Error("something was committed or opened")
	}
}
