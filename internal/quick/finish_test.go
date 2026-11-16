package quick

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bengearig/ralph/internal/hosting"
	"github.com/bengearig/ralph/internal/hosting/fake"
	"github.com/bengearig/ralph/internal/units"
	"github.com/bengearig/ralph/internal/verify"
	"github.com/bengearig/ralph/internal/workspace"
)

type fakeRunner struct {
	report verify.Report
	err    error
	calls  [][]string
}

func (r *fakeRunner) Run(_ context.Context, commands []string) (verify.Report, error) {
	r.calls = append(r.calls, commands)
	return r.report, r.err
}

type finishFixture struct {
	*fixture
	origin   string
	prov     *fake.Provider
	provider hosting.Provider
	provErr  error
	runner   *fakeRunner
	unitsDep units.Service
}

func newFinishFixture(t *testing.T) *finishFixture {
	t.Helper()
	f := newFixture(t)
	origin := filepath.Join(t.TempDir(), "origin.git")
	f.repo.Git("init", "--bare", "-b", "main", origin)
	f.repo.Git("remote", "add", "origin", origin)
	f.cfg.CommitTemplate = "{{.ID}}: {{.Title}}"
	f.cfg.CommitTrailer = true
	f.cfg.Draft = true
	prov := fake.New()
	return &finishFixture{fixture: f, origin: origin, prov: prov, provider: prov, runner: &fakeRunner{}}
}

func (f *finishFixture) finishService() *Service {
	u := f.units
	if f.unitsDep != nil {
		u = f.unitsDep
	}
	return New(Deps{
		Units: u, Workspace: f.ws, Config: f.cfg, Runner: f.runner,
		Provider: func(context.Context) (hosting.Provider, error) { return f.provider, f.provErr },
	})
}

func (f *finishFixture) finish(in FinishInput) (Finished, error) {
	return f.finishService().Finish(context.Background(), in)
}

// started starts a task and leaves a new file in the tree.
func (f *finishFixture) started(t *testing.T, title string) Started {
	t.Helper()
	got, err := f.start(t, title)
	if err != nil {
		t.Fatal(err)
	}
	f.repo.WriteFile("new.txt", "hello\n")
	return got
}

func (f *finishFixture) remoteHas(branch string) bool {
	return f.repo.Git("ls-remote", f.origin, branch) != ""
}

func (f *finishFixture) commits() string { return f.repo.Git("rev-list", "--count", "HEAD") }

func (f *finishFixture) linkTargets(t *testing.T, id string) []string {
	t.Helper()
	links, err := f.units.Links(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, l := range links {
		out = append(out, l.Target)
	}
	return out
}

func (f *finishFixture) moveOriginAside(t *testing.T) string {
	t.Helper()
	aside := f.origin + ".aside"
	if err := os.Rename(f.origin, aside); err != nil {
		t.Fatal(err)
	}
	return aside
}

func (f *finishFixture) wantInProgress(t *testing.T, id string) {
	t.Helper()
	if st := f.get(t, id).State; st != units.StateInProgress {
		t.Fatalf("state = %s, want IN_PROGRESS", st)
	}
}

func TestFinishHappyPath(t *testing.T) {
	f := newFinishFixture(t)
	st := f.started(t, "Add a file")
	got, err := f.finish(FinishInput{Summary: "added new.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if !got.Committed || got.Branch != st.Branch || got.Base != "main" || got.Commit != f.repo.Git("rev-parse", "HEAD") {
		t.Fatalf("result = %+v", got)
	}
	if msg := f.repo.Git("log", "-1", "--format=%B"); msg != "T-1: Add a file\n\nRalph-Task: T-1" {
		t.Fatalf("message = %q", msg)
	}
	if f.repo.Git("rev-list", "--count", "main..HEAD") != "1" {
		t.Fatal("want one commit on the branch")
	}
	if !f.remoteHas(st.Branch) {
		t.Fatal("branch not on the remote")
	}
	pr, ok, _ := f.prov.FindPullRequest(context.Background(), st.Branch)
	if !ok || pr.Base != "main" || pr.Title != "T-1: Add a file" || !pr.Draft || !strings.Contains(pr.Body, "added new.txt") {
		t.Fatalf("pr = %+v", pr)
	}
	if got.PullRequest.URL != pr.URL || got.PullRequestReused {
		t.Fatalf("result pr = %+v", got)
	}
	if task := f.get(t, "T-1"); task.State != units.StateDone || got.Task.State != units.StateDone {
		t.Fatalf("task = %+v", task)
	}
	links := f.linkTargets(t, "T-1")
	if len(links) != 2 || links[0] != "branch:"+st.Branch || links[1] != "pr:"+pr.URL {
		t.Fatalf("links = %v", links)
	}
	var found bool
	for _, n := range f.notes(t, "T-1") {
		found = found || n.Body == "Pull request opened: "+pr.URL
	}
	if !found {
		t.Fatal("no pull request note")
	}
}

func TestFinishCommitTemplateAndNoTrailer(t *testing.T) {
	f := newFinishFixture(t)
	f.cfg.CommitTemplate = "feat({{.Slug}}): {{.Title}} [{{.ID}}]"
	f.cfg.CommitTrailer = false
	f.started(t, "Add a file")
	got, err := f.finish(FinishInput{ID: "T-1"})
	if err != nil {
		t.Fatal(err)
	}
	if msg := f.repo.Git("log", "-1", "--format=%B"); msg != "feat(add-a-file): Add a file [T-1]" {
		t.Fatalf("message = %q", msg)
	}
	if got.PullRequest.Title != "feat(add-a-file): Add a file [T-1]" {
		t.Fatalf("title = %q", got.PullRequest.Title)
	}
}

func TestFinishDraftSetting(t *testing.T) {
	f := newFinishFixture(t)
	f.cfg.Draft = false
	f.started(t, "Add a file")
	got, err := f.finish(FinishInput{})
	if err != nil {
		t.Fatal(err)
	}
	if got.PullRequest.Draft {
		t.Fatal("pull request is a draft")
	}
}

func TestFinishVerificationRunsConfiguredCommands(t *testing.T) {
	f := newFinishFixture(t)
	f.cfg.Verify = verify.Settings{Commands: []string{"make test", "make lint"}}
	f.runner.report = verify.Report{Results: []verify.Result{{Command: "make test"}, {Command: "make lint"}}}
	f.started(t, "Add a file")
	got, err := f.finish(FinishInput{})
	if err != nil {
		t.Fatal(err)
	}
	if len(f.runner.calls) != 1 || strings.Join(f.runner.calls[0], "|") != "make test|make lint" {
		t.Fatalf("calls = %v", f.runner.calls)
	}
	if !got.VerificationConfigured || len(got.Verification.Results) != 2 {
		t.Fatalf("result = %+v", got)
	}
	if !strings.Contains(got.PullRequest.Body, "- make lint: passed (0s)") {
		t.Fatalf("body = %q", got.PullRequest.Body)
	}
}

func TestFinishVerificationFailureChangesNothing(t *testing.T) {
	f := newFinishFixture(t)
	f.cfg.Verify = verify.Settings{Commands: []string{"make test"}}
	f.runner.report = verify.Report{Results: []verify.Result{{Command: "make test", ExitCode: 2}}}
	st := f.started(t, "Add a file")
	before := f.commits()
	_, err := f.finish(FinishInput{})
	if !errors.Is(err, ErrVerificationFailed) {
		t.Fatalf("err = %v", err)
	}
	var verr *VerificationError
	if !errors.As(err, &verr) {
		t.Fatal("not a VerificationError")
	}
	if res, ok := verr.Report.Failed(); !ok || res.Command != "make test" || res.ExitCode != 2 {
		t.Fatalf("failed = %+v", res)
	}
	if !strings.Contains(err.Error(), "make test") || !strings.Contains(err.Error(), "2") {
		t.Fatalf("message = %q", err)
	}
	if f.commits() != before || f.remoteHas(st.Branch) || len(f.prov.Calls()) != 0 {
		t.Fatal("verification failure changed something")
	}
	f.wantInProgress(t, "T-1")
}

func TestFinishVerificationTimeoutMessage(t *testing.T) {
	err := &VerificationError{Report: verify.Report{Results: []verify.Result{{Command: "sleep 9", TimedOut: true, ExitCode: -1}}}}
	if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("message = %q", err)
	}
}

func TestFinishRunnerContextErrorReturnedAsIs(t *testing.T) {
	f := newFinishFixture(t)
	f.cfg.Verify = verify.Settings{Commands: []string{"x"}}
	f.runner.err = context.DeadlineExceeded
	f.started(t, "Add a file")
	if _, err := f.finish(FinishInput{}); err != context.DeadlineExceeded {
		t.Fatalf("err = %v", err)
	}
}

func TestFinishNoVerificationConfigured(t *testing.T) {
	f := newFinishFixture(t)
	f.started(t, "Add a file")
	got, err := f.finish(FinishInput{})
	if err != nil {
		t.Fatal(err)
	}
	if got.VerificationConfigured || len(f.runner.calls) != 0 {
		t.Fatalf("configured = %v, calls = %v", got.VerificationConfigured, f.runner.calls)
	}
	if !strings.Contains(got.PullRequest.Body, "No verification commands are configured for this project.") {
		t.Fatalf("body = %q", got.PullRequest.Body)
	}
}

func TestFinishNothingToDeliver(t *testing.T) {
	f := newFinishFixture(t)
	st, err := f.start(t, "Nothing")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.finish(FinishInput{}); !errors.Is(err, ErrNothingToDeliver) {
		t.Fatalf("err = %v", err)
	}
	if f.remoteHas(st.Branch) || len(f.prov.Calls()) != 0 {
		t.Fatal("something was pushed")
	}
	f.wantInProgress(t, "T-1")
}

func TestFinishIgnoresRecordedLocalFiles(t *testing.T) {
	f := newFinishFixture(t)
	dir := filepath.Join(f.repo.Dir, ".ralph")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"version":1,"entries":[{"id":"a","path":"local.txt"}]}`
	if err := os.WriteFile(filepath.Join(dir, "integration.local.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	f.repo.Git("add", "-f", ".ralph/integration.local.json")
	f.repo.Git("commit", "-m", "manifest")
	ws, err := NewLocalWorkspace(f.repo.Dir)
	if err != nil {
		t.Fatal(err)
	}
	f.ws = ws
	if _, err := f.start(t, "Nothing"); err != nil {
		t.Fatal(err)
	}
	f.repo.WriteFile("local.txt", "x")
	before := f.commits()
	if _, err := f.finish(FinishInput{}); !errors.Is(err, ErrNothingToDeliver) {
		t.Fatalf("err = %v", err)
	}
	if f.commits() != before {
		t.Fatal("a commit was made")
	}
}

func TestFinishAlreadyCommittedByAgent(t *testing.T) {
	f := newFinishFixture(t)
	st := f.started(t, "Add a file")
	head := f.repo.Commit("agent commit")
	got, err := f.finish(FinishInput{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Committed || got.Commit != head || f.repo.Git("rev-parse", "HEAD") != head {
		t.Fatalf("result = %+v", got)
	}
	if !f.remoteHas(st.Branch) || got.PullRequest.URL == "" {
		t.Fatal("not pushed or no pull request")
	}
}

func TestFinishWrongBranch(t *testing.T) {
	f := newFinishFixture(t)
	st := f.started(t, "Add a file")
	f.repo.Git("stash", "-u")
	f.repo.Git("switch", "main")
	_, err := f.finish(FinishInput{ID: "T-1"})
	if !errors.Is(err, ErrWrongBranch) || !strings.Contains(err.Error(), st.Branch) {
		t.Fatalf("err = %v", err)
	}
	f.wantInProgress(t, "T-1")
}

func TestFinishNotQuickWork(t *testing.T) {
	f := newFinishFixture(t)
	ctx := context.Background()
	parent, err := f.units.Create(ctx, units.NewUnit{Level: units.LevelBrief, Title: "brief", Body: "b"})
	if err != nil {
		t.Fatal(err)
	}
	slice, err := f.units.Create(ctx, units.NewUnit{Level: units.LevelSlice, ParentID: parent.ID, Title: "slice"})
	if err != nil {
		t.Fatal(err)
	}
	child, err := f.units.Create(ctx, units.NewUnit{Level: units.LevelTask, ParentID: slice.ID, Title: "child"})
	if err != nil {
		t.Fatal(err)
	}
	todo, err := f.units.Create(ctx, units.NewUnit{Level: units.LevelTask, Title: "todo"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.units.SetState(ctx, todo.ID, todo.Fingerprint, units.StateTodo, "r"); err != nil {
		t.Fatal(err)
	}
	f.started(t, "no link")
	if err := f.units.RemoveLink(ctx, "T-3", units.LinkExternal, "branch:"+f.repo.Git("branch", "--show-current")); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, id string
		want     error
	}{
		{"parent", child.ID, ErrNotQuickWork},
		{"slice", slice.ID, ErrNotQuickWork},
		{"todo", todo.ID, ErrNotQuickWork},
		{"no link", "T-3", ErrNotQuickWork},
		{"unknown", "T-99", units.ErrNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := f.finish(FinishInput{ID: tc.id}); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestFinishWithoutIDFindsCurrentBranchTask(t *testing.T) {
	f := newFinishFixture(t)
	f.started(t, "Add a file")
	got, err := f.finish(FinishInput{})
	if err != nil || got.Task.ID != "T-1" {
		t.Fatalf("got %+v, err %v", got, err)
	}
}

func TestFinishWithoutIDNoCandidate(t *testing.T) {
	f := newFinishFixture(t)
	if _, err := f.finish(FinishInput{}); !errors.Is(err, ErrNotQuickWork) {
		t.Fatalf("err = %v", err)
	}
}

func TestFinishAmbiguousTask(t *testing.T) {
	f := newFinishFixture(t)
	st := f.started(t, "Add a file")
	ctx := context.Background()
	other, err := f.units.Create(ctx, units.NewUnit{Level: units.LevelTask, Title: "twin"})
	if err != nil {
		t.Fatal(err)
	}
	other, err = f.units.SetState(ctx, other.ID, other.Fingerprint, units.StateTodo, "r")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.units.SetState(ctx, other.ID, other.Fingerprint, units.StateInProgress, "r"); err != nil {
		t.Fatal(err)
	}
	if err := f.units.AddLink(ctx, other.ID, units.LinkExternal, "branch:"+st.Branch); err != nil {
		t.Fatal(err)
	}
	_, err = f.finish(FinishInput{})
	if !errors.Is(err, ErrAmbiguousTask) || !strings.Contains(err.Error(), "T-1") || !strings.Contains(err.Error(), other.ID) {
		t.Fatalf("err = %v", err)
	}
}

func TestFinishCommitHookFailure(t *testing.T) {
	f := newFinishFixture(t)
	f.started(t, "Add a file")
	hook := filepath.Join(f.repo.Dir, ".git", "hooks", "pre-commit")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\necho nope >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := f.finish(FinishInput{})
	var gerr *workspace.GitError
	if !errors.Is(err, ErrCommitFailed) || !errors.As(err, &gerr) {
		t.Fatalf("err = %v", err)
	}
	f.wantInProgress(t, "T-1")
}

func TestFinishPushFailure(t *testing.T) {
	f := newFinishFixture(t)
	f.started(t, "Add a file")
	f.moveOriginAside(t)
	_, err := f.finish(FinishInput{})
	var gerr *workspace.GitError
	if !errors.Is(err, ErrPushFailed) || !errors.As(err, &gerr) {
		t.Fatalf("err = %v", err)
	}
	if f.repo.Git("rev-list", "--count", "main..HEAD") != "1" {
		t.Fatal("commit missing")
	}
	f.wantInProgress(t, "T-1")
}

func TestFinishResumesAfterPushFailure(t *testing.T) {
	f := newFinishFixture(t)
	st := f.started(t, "Add a file")
	aside := f.moveOriginAside(t)
	if _, err := f.finish(FinishInput{}); !errors.Is(err, ErrPushFailed) {
		t.Fatalf("err = %v", err)
	}
	if err := os.Rename(aside, f.origin); err != nil {
		t.Fatal(err)
	}
	got, err := f.finish(FinishInput{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Committed || f.repo.Git("rev-list", "--count", "main..HEAD") != "1" {
		t.Fatalf("second commit made: %+v", got)
	}
	if !f.remoteHas(st.Branch) || f.get(t, "T-1").State != units.StateDone {
		t.Fatal("not delivered")
	}
}

func TestFinishReusesOpenPullRequest(t *testing.T) {
	f := newFinishFixture(t)
	st := f.started(t, "Add a file")
	existing, err := f.prov.CreatePullRequest(context.Background(), hosting.NewPullRequest{Head: st.Branch, Base: "main", Title: "t"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := f.finish(FinishInput{})
	if err != nil {
		t.Fatal(err)
	}
	if !got.PullRequestReused || got.PullRequest.URL != existing.URL {
		t.Fatalf("result = %+v", got)
	}
	creates := 0
	for _, c := range f.prov.Calls() {
		if c.Op == "create" {
			creates++
		}
	}
	if creates != 1 {
		t.Fatalf("creates = %d, want only the setup one", creates)
	}
	var found bool
	for _, n := range f.notes(t, "T-1") {
		found = found || n.Body == "Pull request reused: "+existing.URL
	}
	if !found {
		t.Fatal("no reuse note")
	}
}

type racingProvider struct {
	*fake.Provider
	existing hosting.PullRequest
	finds    int
}

func (p *racingProvider) FindPullRequest(context.Context, string) (hosting.PullRequest, bool, error) {
	p.finds++
	return p.existing, p.finds > 1, nil
}

func (p *racingProvider) CreatePullRequest(context.Context, hosting.NewPullRequest) (hosting.PullRequest, error) {
	return hosting.PullRequest{}, hosting.ErrConflict
}

func TestFinishConflictLooksAgain(t *testing.T) {
	f := newFinishFixture(t)
	f.started(t, "Add a file")
	rp := &racingProvider{Provider: f.prov, existing: hosting.PullRequest{Number: 7, URL: "https://x/pull/7", State: hosting.StateOpen}}
	f.provider = rp
	got, err := f.finish(FinishInput{})
	if err != nil {
		t.Fatal(err)
	}
	if !got.PullRequestReused || got.PullRequest.URL != "https://x/pull/7" || rp.finds != 2 {
		t.Fatalf("result = %+v, finds = %d", got, rp.finds)
	}
}

type unauthorizedProvider struct{ *fake.Provider }

func (unauthorizedProvider) CreatePullRequest(context.Context, hosting.NewPullRequest) (hosting.PullRequest, error) {
	return hosting.PullRequest{}, hosting.ErrUnauthorized
}

func TestFinishProviderUnauthorized(t *testing.T) {
	f := newFinishFixture(t)
	f.started(t, "Add a file")
	f.provider = unauthorizedProvider{f.prov}
	_, err := f.finish(FinishInput{})
	if !errors.Is(err, ErrPullRequestFailed) || !errors.Is(err, hosting.ErrUnauthorized) {
		t.Fatalf("err = %v", err)
	}
	f.wantInProgress(t, "T-1")
}

func TestFinishProviderBuildFailure(t *testing.T) {
	f := newFinishFixture(t)
	f.started(t, "Add a file")
	f.provErr = errors.New("no token")
	f.provider = nil
	_, err := f.finish(FinishInput{})
	if !errors.Is(err, ErrPullRequestFailed) || !strings.Contains(err.Error(), "no token") {
		t.Fatalf("err = %v", err)
	}
	f.wantInProgress(t, "T-1")
}

type failingDone struct{ units.Service }

func (u failingDone) SetState(ctx context.Context, id, fp string, to units.State, reason string) (units.Unit, error) {
	if to == units.StateDone {
		return units.Unit{}, errors.New("database is locked")
	}
	return u.Service.SetState(ctx, id, fp, to, reason)
}

func TestFinishRecordFailureNamesURL(t *testing.T) {
	f := newFinishFixture(t)
	f.started(t, "Add a file")
	f.unitsDep = failingDone{f.units}
	_, err := f.finish(FinishInput{})
	if err == nil || !strings.Contains(err.Error(), "https://fake.example/pull/1") {
		t.Fatalf("err = %v", err)
	}
	f.wantInProgress(t, "T-1")
}

func TestFinishContextCancelled(t *testing.T) {
	f := newFinishFixture(t)
	st := f.started(t, "Add a file")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := f.finishService().Finish(ctx, FinishInput{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	if f.remoteHas(st.Branch) {
		t.Fatal("pushed")
	}
	f.wantInProgress(t, "T-1")
}

func TestFinishLeavesCheckoutOnTaskBranch(t *testing.T) {
	f := newFinishFixture(t)
	st := f.started(t, "Add a file")
	if _, err := f.finish(FinishInput{}); err != nil {
		t.Fatal(err)
	}
	if cur := f.repo.Git("branch", "--show-current"); cur != st.Branch {
		t.Fatalf("checkout on %q", cur)
	}
}
