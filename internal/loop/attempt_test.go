package loop_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bengearig/ralph/internal/config"
	"github.com/bengearig/ralph/internal/db"
	"github.com/bengearig/ralph/internal/doclinks"
	"github.com/bengearig/ralph/internal/documents"
	"github.com/bengearig/ralph/internal/harness"
	"github.com/bengearig/ralph/internal/harness/claudecode"
	"github.com/bengearig/ralph/internal/headless"
	"github.com/bengearig/ralph/internal/loop"
	"github.com/bengearig/ralph/internal/testutil"
	"github.com/bengearig/ralph/internal/units"
	"github.com/bengearig/ralph/internal/verify"
	"github.com/bengearig/ralph/internal/workspace"
)

const sliceBranch = "ralph/the-slice"

type stubVerifier struct {
	report verify.Report
	err    error
	calls  int
}

func (v *stubVerifier) Run(_ context.Context, _ []string) (verify.Report, error) {
	v.calls++
	return v.report, v.err
}

type stubRecorder struct {
	beginErr  error
	finishErr error
	infos     []loop.AttemptInfo
	finishes  int
	prompt    string
	verified  int
	out       *bytes.Buffer
}

func (r *stubRecorder) Begin(_ context.Context, info loop.AttemptInfo) (loop.AttemptRecord, error) {
	r.infos = append(r.infos, info)
	if r.beginErr != nil {
		return nil, r.beginErr
	}
	r.out = &bytes.Buffer{}
	return r, nil
}

func (r *stubRecorder) Prompt(text string) error { r.prompt = text; return nil }
func (r *stubRecorder) Stdout() io.Writer        { return r.out }
func (r *stubRecorder) Verification(verify.Report) error {
	r.verified++
	return nil
}
func (r *stubRecorder) Finish(loop.AttemptResult, headless.Result) (string, error) {
	r.finishes++
	return "/runs/x", r.finishErr
}

type attemptFixture struct {
	*fixture
	repo     *testutil.Repo
	cfg      *config.Config
	deps     loop.Deps
	verifier *stubVerifier
	events   []loop.Event
	execs    int
	req      harness.HeadlessRequest
	opt      headless.Options
	do       func()
	result   headless.Result
}

func cleanResult() headless.Result {
	return headless.Result{Outcome: harness.Outcome{Finished: true, Success: true}}
}

func loadConfig(t *testing.T, root, yaml string) *config.Config {
	t.Helper()
	if yaml != "" {
		dir := filepath.Join(root, ".ralph")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "project.yaml"), []byte(yaml), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cfg, err := config.Load(config.Options{
		Registry:    config.DefaultRegistry(),
		UserFile:    filepath.Join(t.TempDir(), "user.yaml"),
		ProjectRoot: root,
		Getenv:      func(string) (string, bool) { return "", false },
	})
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func newAttemptFixture(t *testing.T) *attemptFixture {
	t.Helper()
	testutil.IsolateEnv(t)
	repo := testutil.NewRepo(t)
	repo.Git("switch", "-c", sliceBranch)
	d := testutil.NewDB(t, db.Migrations)
	u, ds := units.New(d), documents.New(d)
	f := &fixture{units: u, docs: ds, links: doclinks.New(u, ds)}
	f.brief = f.unit(t, units.LevelBrief, "", "Ship it", "The brief body.", "")
	f.slice = f.unit(t, units.LevelSlice, f.brief.ID, "The slice", "The slice body.", "")
	f.task = f.unit(t, units.LevelTask, f.slice.ID, "Do the thing", "Task body.", "- it works")

	a := &attemptFixture{fixture: f, repo: repo, cfg: loadConfig(t, repo.Dir, ""), verifier: &stubVerifier{}}
	a.result = cleanResult()
	a.setState(t, units.StateTodo)
	a.do = func() { repo.WriteFile("work.txt", "work\n") }
	a.deps = loop.Deps{
		Units:     u,
		Workspace: workspace.NewLocal(repo.Dir),
		Prompt:    loop.PromptDeps{Units: u, Links: f.links, Docs: ds},
		Config:    a.cfg, Root: repo.Dir,
		MCP:            harness.MCPServer{Name: "ralph", Command: "/bin/ralph", Args: []string{"mcp"}},
		CommitTemplate: "{{.ID}}: {{.Title}}",
		Verify:         verify.Settings{Commands: []string{"go test ./..."}},
		Verifier:       a.verifier,
		Execute: func(_ context.Context, _ harness.Adapter, req harness.HeadlessRequest, opt headless.Options) (headless.Result, error) {
			a.execs++
			a.req, a.opt = req, opt
			a.do()
			return a.result, nil
		},
		Report: func(e loop.Event) { a.events = append(a.events, e) },
	}
	return a
}

func (a *attemptFixture) input() loop.AttemptInput {
	return loop.AttemptInput{Task: a.task, Slice: a.slice, Branch: sliceBranch, Attempt: 1, MaxAttempts: 3}
}

func (a *attemptFixture) attempt(t *testing.T) loop.AttemptResult {
	t.Helper()
	res, err := loop.Attempt(ctx, a.deps, a.input())
	if err != nil {
		t.Fatalf("Attempt: %v", err)
	}
	return res
}

func (a *attemptFixture) setState(t *testing.T, to units.State) {
	t.Helper()
	cur, err := a.units.Get(ctx, a.task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cur.State == to {
		return
	}
	if _, err := a.units.SetState(ctx, cur.ID, cur.Fingerprint, to, "test"); err != nil {
		t.Fatal(err)
	}
}

func (a *attemptFixture) state(t *testing.T) units.State {
	t.Helper()
	u, err := a.units.Get(ctx, a.task.ID)
	if err != nil {
		t.Fatal(err)
	}
	return u.State
}

func (a *attemptFixture) attemptNote(t *testing.T) string {
	t.Helper()
	notes, err := a.units.Notes(ctx, a.task.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range notes {
		if strings.HasPrefix(n.Body, "Attempt ") {
			return n.Body
		}
	}
	t.Fatal("no attempt note")
	return ""
}

func (a *attemptFixture) noteCount(t *testing.T, kind units.NoteKind) int {
	t.Helper()
	notes, err := a.units.Notes(ctx, a.task.ID)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, x := range notes {
		if x.Kind == kind && strings.HasPrefix(x.Body, "Attempt ") {
			n++
		}
	}
	return n
}

func (a *attemptFixture) kinds() []loop.EventKind {
	var k []loop.EventKind
	for _, e := range a.events {
		k = append(k, e.Kind)
	}
	return k
}

func (a *attemptFixture) commits() string {
	return a.repo.Git("rev-list", "--count", "HEAD")
}

func TestAttemptDoneCommitsOnce(t *testing.T) {
	a := newAttemptFixture(t)
	res := a.attempt(t)
	if res.Outcome != loop.OutcomeDone {
		t.Fatalf("Outcome = %s (%s)", res.Outcome, res.Failure)
	}
	if got := a.commits(); got != "2" {
		t.Errorf("commits = %s, want 2", got)
	}
	msg := a.repo.Git("log", "-1", "--format=%B")
	if !strings.HasPrefix(msg, "T-1: Do the thing\n") || !strings.Contains(msg, "Ralph-Task: T-1") {
		t.Errorf("message = %q", msg)
	}
	head := a.repo.Git("rev-parse", "HEAD")
	if res.Commit != head || res.EndHead != head {
		t.Errorf("Commit = %s EndHead = %s, HEAD = %s", res.Commit, res.EndHead, head)
	}
	links, err := a.units.Links(ctx, a.task.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, l := range links {
		if l.Kind == units.LinkExternal && l.Target == "commit:"+head {
			found = true
		}
	}
	if !found {
		t.Errorf("links = %v, want commit:%s", links, head)
	}
	if a.state(t) != units.StateDone {
		t.Errorf("state = %s", a.state(t))
	}
	if n := a.noteCount(t, units.NoteGeneral); n != 1 {
		t.Errorf("attempt notes = %d, want 1", n)
	}
	want := "Attempt 1 of 3 passed: commit " + head[:12]
	if got := a.attemptNote(t); got != want {
		t.Errorf("note = %q, want %q", got, want)
	}
	if res.StartHead == res.EndHead {
		t.Error("StartHead equals EndHead")
	}
}

func TestAttemptTrailerAlwaysWritten(t *testing.T) {
	a := newAttemptFixture(t)
	a.cfg = loadConfig(t, a.repo.Dir, "git:\n  commit_trailer: false\n")
	a.deps.Config = a.cfg
	a.attempt(t)
	if msg := a.repo.Git("log", "-1", "--format=%B"); !strings.Contains(msg, "Ralph-Task: T-1") {
		t.Errorf("message = %q", msg)
	}
}

func TestAttemptTodoBecomesInProgress(t *testing.T) {
	a := newAttemptFixture(t)
	a.setState(t, units.StateTodo)
	var during units.State
	a.do = func() { during = a.state(t); a.repo.WriteFile("work.txt", "x") }
	a.attempt(t)
	if during != units.StateInProgress {
		t.Errorf("state during the run = %s", during)
	}
	notes, _ := a.units.Notes(ctx, a.task.ID)
	found := false
	for _, n := range notes {
		if strings.Contains(n.Body, "loop attempt 1") {
			found = true
		}
	}
	if !found {
		t.Errorf("no note with the reason loop attempt 1: %v", notes)
	}
}

func TestAttemptInProgressStays(t *testing.T) {
	a := newAttemptFixture(t)
	a.setState(t, units.StateTodo)
	a.setState(t, units.StateInProgress)
	before, _ := a.units.Notes(ctx, a.task.ID)
	a.do = func() {}
	a.attempt(t)
	after, _ := a.units.Notes(ctx, a.task.ID)
	for _, n := range after[len(before):] {
		if strings.Contains(n.Body, "loop attempt") && !strings.HasPrefix(n.Body, "Attempt ") {
			t.Errorf("unexpected move note %q", n.Body)
		}
	}
	if a.state(t) != units.StateInProgress {
		t.Errorf("state = %s", a.state(t))
	}
}

func TestAttemptNoChangesFails(t *testing.T) {
	a := newAttemptFixture(t)
	a.setState(t, units.StateTodo)
	a.do = func() {}
	res := a.attempt(t)
	if res.Outcome != loop.OutcomeFailed || res.Failure != "the agent made no changes" {
		t.Errorf("result = %s %q", res.Outcome, res.Failure)
	}
	if a.state(t) != units.StateInProgress || a.commits() != "1" {
		t.Errorf("state = %s commits = %s", a.state(t), a.commits())
	}
	if got := a.attemptNote(t); got != "Attempt 1 of 3 failed: the agent made no changes" {
		t.Errorf("note = %q", got)
	}
}

func TestAttemptAgentCommitIsUsed(t *testing.T) {
	a := newAttemptFixture(t)
	var agent string
	a.do = func() {
		a.repo.WriteFile("agent.txt", "a\n")
		agent = a.repo.Commit("agent work")
	}
	res := a.attempt(t)
	if res.Outcome != loop.OutcomeDone {
		t.Fatalf("Outcome = %s (%s)", res.Outcome, res.Failure)
	}
	if got := a.repo.Git("rev-parse", "HEAD"); got != agent || res.Commit != agent {
		t.Errorf("HEAD = %s Commit = %s, agent made %s", got, res.Commit, agent)
	}
	if a.commits() != "2" {
		t.Errorf("commits = %s, want 2", a.commits())
	}
	if a.state(t) != units.StateDone {
		t.Errorf("state = %s", a.state(t))
	}
}

func TestAttemptHarnessFailureSkipsVerification(t *testing.T) {
	a := newAttemptFixture(t)
	a.result.Outcome.Denials = []harness.Denial{{Tool: "Bash", Target: "rm -rf /"}}
	res := a.attempt(t)
	if res.Outcome != loop.OutcomeFailed || !strings.HasPrefix(res.Failure, "denied actions (1): ") {
		t.Errorf("result = %s %q", res.Outcome, res.Failure)
	}
	if a.verifier.calls != 0 {
		t.Errorf("verifier called %d times", a.verifier.calls)
	}
	if len(res.Denials) != 1 {
		t.Errorf("Denials = %v", res.Denials)
	}
	if a.commits() != "1" {
		t.Errorf("commits = %s", a.commits())
	}
}

func TestAttemptTimeoutAndBudgetFail(t *testing.T) {
	for name, tc := range map[string]struct {
		set  func(*headless.Result)
		want string
	}{
		"timeout": {func(r *headless.Result) { r.TimedOut = true }, "timed out"},
		"budget":  {func(r *headless.Result) { r.BudgetStopped = true }, "budget exceeded"},
	} {
		t.Run(name, func(t *testing.T) {
			a := newAttemptFixture(t)
			tc.set(&a.result)
			res := a.attempt(t)
			if res.Outcome != loop.OutcomeFailed || res.Failure != tc.want {
				t.Errorf("result = %s %q", res.Outcome, res.Failure)
			}
			if a.verifier.calls != 0 {
				t.Error("verifier called")
			}
		})
	}
}

func TestAttemptVerificationFailureNamesCommand(t *testing.T) {
	for name, tc := range map[string]struct {
		res  verify.Result
		want string
	}{
		"exit":    {verify.Result{Command: "make lint", ExitCode: 2}, "verification failed: make lint (exit 2)"},
		"timeout": {verify.Result{Command: "make lint", ExitCode: -1, TimedOut: true}, "verification failed: make lint (timed out)"},
	} {
		t.Run(name, func(t *testing.T) {
			a := newAttemptFixture(t)
			a.verifier.report = verify.Report{Results: []verify.Result{{Command: "go build", ExitCode: 0}, tc.res}}
			res := a.attempt(t)
			if res.Outcome != loop.OutcomeFailed || res.Failure != tc.want {
				t.Errorf("result = %s %q", res.Outcome, res.Failure)
			}
			if len(res.Verification.Results) != 2 || a.commits() != "1" {
				t.Errorf("verification = %v commits = %s", res.Verification, a.commits())
			}
		})
	}
}

func TestAttemptNoVerificationCommandsWarns(t *testing.T) {
	a := newAttemptFixture(t)
	a.deps.Verify = verify.Settings{}
	res := a.attempt(t)
	if res.Outcome != loop.OutcomeDone {
		t.Fatalf("Outcome = %s", res.Outcome)
	}
	var warned bool
	for _, e := range a.events {
		if e.Kind == loop.EventWarning && e.Message == "no verification commands configured: the result was not checked" {
			warned = true
		}
	}
	if !warned || a.verifier.calls != 0 {
		t.Errorf("warned = %v calls = %d", warned, a.verifier.calls)
	}
}

func TestAttemptKeepsFilesOnFailure(t *testing.T) {
	a := newAttemptFixture(t)
	a.verifier.report = verify.Report{Results: []verify.Result{{Command: "x", ExitCode: 1}}}
	a.attempt(t)
	if _, err := os.Stat(filepath.Join(a.repo.Dir, "work.txt")); err != nil {
		t.Errorf("the stub's file is gone: %v", err)
	}
	if out := a.repo.Git("status", "--porcelain"); !strings.Contains(out, "work.txt") {
		t.Errorf("status = %q", out)
	}
}

func TestAttemptBranchChangedAborts(t *testing.T) {
	a := newAttemptFixture(t)
	a.setState(t, units.StateTodo)
	a.setState(t, units.StateInProgress)
	a.do = func() { a.repo.Git("switch", "-c", "elsewhere") }
	res, err := loop.Attempt(ctx, a.deps, a.input())
	if !errors.Is(err, loop.ErrBranchChanged) {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(err.Error(), sliceBranch) || !strings.Contains(err.Error(), "elsewhere") {
		t.Errorf("message = %q", err)
	}
	if res.Outcome != loop.OutcomeAborted {
		t.Errorf("Outcome = %s", res.Outcome)
	}
	if a.state(t) != units.StateInProgress || a.commits() != "1" || a.verifier.calls != 0 {
		t.Errorf("state = %s commits = %s calls = %d", a.state(t), a.commits(), a.verifier.calls)
	}
	if n := a.noteCount(t, units.NoteGeneral); n != 0 {
		t.Errorf("attempt notes = %d", n)
	}
}

func TestAttemptDetachedHeadAborts(t *testing.T) {
	a := newAttemptFixture(t)
	a.do = func() { a.repo.Git("checkout", "--detach") }
	res, err := loop.Attempt(ctx, a.deps, a.input())
	if !errors.Is(err, loop.ErrBranchChanged) || res.Outcome != loop.OutcomeAborted {
		t.Fatalf("err = %v outcome = %s", err, res.Outcome)
	}
	if !strings.Contains(err.Error(), "detached") {
		t.Errorf("message = %q", err)
	}
}

func TestAttemptExecuteErrorAborts(t *testing.T) {
	a := newAttemptFixture(t)
	boom := errors.New("claude was not found")
	a.deps.Execute = func(context.Context, harness.Adapter, harness.HeadlessRequest, headless.Options) (headless.Result, error) {
		return headless.Result{}, boom
	}
	rec := &stubRecorder{}
	a.deps.Recorder = rec
	res, err := loop.Attempt(ctx, a.deps, a.input())
	if !errors.Is(err, boom) || res.Outcome != loop.OutcomeAborted {
		t.Fatalf("err = %v outcome = %s", err, res.Outcome)
	}
	if rec.finishes != 1 {
		t.Errorf("finishes = %d", rec.finishes)
	}
}

func TestAttemptInterruptedSetsTodoAndReturnsCanceled(t *testing.T) {
	a := newAttemptFixture(t)
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	a.deps.Execute = func(context.Context, harness.Adapter, harness.HeadlessRequest, headless.Options) (headless.Result, error) {
		a.repo.WriteFile("partial.txt", "p")
		cancel()
		return headless.Result{Canceled: true}, nil
	}
	res, err := loop.Attempt(cctx, a.deps, a.input())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	if res.Outcome != loop.OutcomeAborted || a.state(t) != units.StateTodo {
		t.Errorf("outcome = %s state = %s", res.Outcome, a.state(t))
	}
	notes, _ := a.units.Notes(ctx, a.task.ID)
	if !strings.Contains(notes[len(notes)-1].Body, "loop interrupted") {
		t.Errorf("last note = %q", notes[len(notes)-1].Body)
	}
	if a.verifier.calls != 0 || a.commits() != "1" {
		t.Error("verified or committed after an interrupt")
	}
}

func TestAttemptPromptUsesPreviousFailure(t *testing.T) {
	a := newAttemptFixture(t)
	in := a.input()
	in.Attempt = 2
	in.Previous = &loop.Failure{Line: "verification failed: make lint (exit 2)", VerificationOutput: "LINT-OUTPUT-XYZ"}
	if _, err := loop.Attempt(ctx, a.deps, in); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(a.req.Prompt, "verification failed: make lint (exit 2)") || !strings.Contains(a.req.Prompt, "LINT-OUTPUT-XYZ") {
		t.Errorf("prompt lacks the retry section:\n%s", a.req.Prompt)
	}
}

func TestAttemptRequestCarriesSettings(t *testing.T) {
	a := newAttemptFixture(t)
	rec := &stubRecorder{}
	a.deps.Recorder = rec
	a.deps.Model, a.deps.HarnessVersion = "model-x", "9.9.9"
	a.deps.MCP = harness.MCPServer{Name: "ralph", Command: "/bin/ralph", Args: []string{"mcp"}}
	a.deps.Mode = harness.PermissionAuto
	a.attempt(t)
	r := a.req
	if r.Model != "model-x" || r.Version != "9.9.9" || r.Mode != harness.PermissionAuto || r.MCP.Command != "/bin/ralph" {
		t.Errorf("request = %+v", r)
	}
	if len(r.Allow.Commands) != 0 || len(r.Allow.Tools) != 0 {
		t.Errorf("allowlist = %+v", r.Allow)
	}
	if r.Prompt == "" || r.Prompt != rec.prompt || !strings.Contains(r.Prompt, "T-1") {
		t.Errorf("prompt = %q, recorded %q", r.Prompt, rec.prompt)
	}
	if r.Dir != a.repo.Dir {
		t.Errorf("Dir = %q", r.Dir)
	}
	if a.opt.Stdout != io.Writer(rec.out) {
		t.Error("the stdout writer is not the record's")
	}
	if a.opt.Timeout <= 0 {
		t.Errorf("Timeout = %v", a.opt.Timeout)
	}
}

func TestAttemptRequestKeepsDefaultModeWhenEmpty(t *testing.T) {
	a := newAttemptFixture(t)
	a.attempt(t)
	if a.req.Mode != harness.PermissionRestricted {
		t.Errorf("Mode = %q", a.req.Mode)
	}
}

func TestAttemptEventsOrder(t *testing.T) {
	a := newAttemptFixture(t)
	a.attempt(t)
	if got := fmt.Sprint(a.kinds()); got != "[attempt_started task_done]" {
		t.Errorf("events = %s", got)
	}
	done := a.events[1]
	if done.Task != "T-1" || done.Attempt != 1 || done.Max != 3 || done.Commit == "" || done.Duration <= 0 {
		t.Errorf("task_done = %+v", done)
	}

	b := newAttemptFixture(t)
	b.do = func() {}
	b.attempt(t)
	if got := fmt.Sprint(b.kinds()); got != "[attempt_started attempt_failed]" {
		t.Errorf("events = %s", got)
	}
	if b.events[1].Message != "the agent made no changes" {
		t.Errorf("message = %q", b.events[1].Message)
	}
}

func TestAttemptTaskDoneCarriesCost(t *testing.T) {
	a := newAttemptFixture(t)
	a.result.Outcome.Usage = harness.Usage{CostUSD: 0.42, CostReported: true}
	a.attempt(t)
	if got := a.events[len(a.events)-1].CostUSD; got != 0.42 {
		t.Errorf("CostUSD = %v", got)
	}
}

func TestAttemptRecorderErrorsDoNotFail(t *testing.T) {
	a := newAttemptFixture(t)
	a.deps.Recorder = &stubRecorder{finishErr: errors.New("disk full")}
	res := a.attempt(t)
	if res.Outcome != loop.OutcomeDone || len(res.RecordErrors) != 1 || !strings.Contains(res.RecordErrors[0], "disk full") {
		t.Errorf("outcome = %s errors = %v", res.Outcome, res.RecordErrors)
	}

	b := newAttemptFixture(t)
	b.deps.Recorder = &stubRecorder{beginErr: errors.New("no space")}
	res = b.attempt(t)
	if res.Outcome != loop.OutcomeDone || len(res.RecordErrors) != 1 || !strings.Contains(res.RecordErrors[0], "no space") {
		t.Errorf("outcome = %s errors = %v", res.Outcome, res.RecordErrors)
	}
}

func TestAttemptRecorderGetsHeads(t *testing.T) {
	a := newAttemptFixture(t)
	rec := &stubRecorder{}
	a.deps.Recorder = rec
	start := a.repo.Git("rev-parse", "HEAD")
	res := a.attempt(t)
	if len(rec.infos) != 1 || rec.infos[0].StartHead != start || rec.infos[0].Task != "T-1" || rec.infos[0].Slice != a.slice.ID || rec.infos[0].Attempt != 1 {
		t.Errorf("infos = %+v", rec.infos)
	}
	if rec.finishes != 1 || rec.verified != 1 || res.RecordPath != "/runs/x" {
		t.Errorf("finishes = %d verified = %d path = %q", rec.finishes, rec.verified, res.RecordPath)
	}

	b := newAttemptFixture(t)
	rec = &stubRecorder{}
	b.deps.Recorder = rec
	b.do = func() {}
	b.attempt(t)
	if rec.finishes != 1 {
		t.Errorf("finishes on failure = %d", rec.finishes)
	}
}

func TestAttemptWithFakeClaudeScript(t *testing.T) {
	a := newAttemptFixture(t)
	fixtureFile, err := filepath.Abs(filepath.Join("..", "headless", "testdata", "e2e", "claude", "clean.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	script := fmt.Sprintf("#!/bin/sh\ncat >/dev/null\necho scripted > '%s/scripted.txt'\ncat '%s'\n", a.repo.Dir, fixtureFile)
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	a.deps.Execute = nil
	a.deps.Adapter = claudecode.New()
	rec := &stubRecorder{}
	a.deps.Recorder = rec
	res := a.attempt(t)
	if res.Outcome != loop.OutcomeDone {
		t.Fatalf("Outcome = %s (%s)", res.Outcome, res.Failure)
	}
	if !strings.Contains(a.repo.Git("show", "--stat", "--format=", "HEAD"), "scripted.txt") {
		t.Error("the scripted file is not in the commit")
	}
	if rec.out.Len() == 0 {
		t.Error("the record received no standard output")
	}
}
