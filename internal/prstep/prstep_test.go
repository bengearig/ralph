package prstep_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bengearig/ralph/internal/config"
	"github.com/bengearig/ralph/internal/db"
	"github.com/bengearig/ralph/internal/hosting"
	"github.com/bengearig/ralph/internal/hosting/fake"
	"github.com/bengearig/ralph/internal/prstep"
	"github.com/bengearig/ralph/internal/quick"
	"github.com/bengearig/ralph/internal/testutil"
	"github.com/bengearig/ralph/internal/units"
	"github.com/bengearig/ralph/internal/verify"
)

var ctx = context.Background()

type verifierFunc func(commands []string) (verify.Report, error)

func (f verifierFunc) Run(_ context.Context, commands []string) (verify.Report, error) {
	return f(commands)
}

type env struct {
	repo     *testutil.Repo
	origin   string
	units    units.Service
	brief    units.Unit
	slice    units.Unit
	tasks    []units.Unit
	provider *fake.Provider
	deps     prstep.Deps
	lines    []string
	branch   string
	fbCount  int
}

func loadConfig(t *testing.T, root string) *config.Config {
	t.Helper()
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

func setting(cfg *config.Config, path string) string {
	v, _ := cfg.Get(path)
	return v.Value
}

// newEnv makes a repository with a bare origin, a slice with two DONE tasks
// whose commits are on the slice's branch, and the branch checked out.
func newEnv(t *testing.T) *env {
	t.Helper()
	testutil.IsolateEnv(t)
	repo := testutil.NewRepo(t)
	e := &env{repo: repo, origin: filepath.Join(t.TempDir(), "origin.git"), provider: fake.New()}
	repo.Git("init", "--bare", "-b", "main", e.origin)
	repo.Git("remote", "add", "origin", e.origin)
	repo.Git("push", "origin", "main")

	e.units = units.NewService(testutil.NewDB(t, db.Migrations))
	e.brief = e.create(t, units.NewUnit{Level: units.LevelBrief, Title: "Ship it", Body: "The brief."})
	e.slice = e.create(t, units.NewUnit{Level: units.LevelSlice, ParentID: e.brief.ID, Title: "The slice", Body: "The slice body."})
	cfg := loadConfig(t, repo.Dir)
	branch, err := prstep.BranchName(setting(cfg, "git.branch_template"), e.slice)
	if err != nil {
		t.Fatal(err)
	}
	e.branch = branch
	repo.Git("switch", "-c", branch)
	for _, title := range []string{"First task", "Second task"} {
		task := e.create(t, units.NewUnit{Level: units.LevelTask, ParentID: e.slice.ID, Title: title, Body: "b", AcceptanceCriteria: "c"})
		repo.WriteFile(strings.ReplaceAll(title, " ", "-")+".txt", "work\n")
		hash := repo.Commit(task.ID + ": " + title)
		if err := e.units.AddLink(ctx, task.ID, units.LinkExternal, "commit:"+hash); err != nil {
			t.Fatal(err)
		}
		e.move(t, task.ID, units.StateDone)
		e.tasks = append(e.tasks, task)
	}

	ws, err := quick.NewLocalWorkspace(repo.Dir)
	if err != nil {
		t.Fatal(err)
	}
	e.deps = prstep.Deps{
		Units:     e.units,
		Workspace: ws,
		Provider:  func(context.Context) (hosting.Provider, error) { return e.provider, nil },
		Config: prstep.Config{
			Root: repo.Dir, Remote: "origin", Base: "main",
			BranchTemplate: setting(cfg, "git.branch_template"), CommitTemplate: setting(cfg, "git.commit_template"),
			Draft: true,
		},
		Report: func(s string) { e.lines = append(e.lines, s) },
	}
	return e
}

func (e *env) create(t *testing.T, n units.NewUnit) units.Unit {
	t.Helper()
	u, err := e.units.Create(ctx, n)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func (e *env) move(t *testing.T, id string, to units.State) {
	t.Helper()
	path := map[units.State][]units.State{
		units.StateTodo:      {units.StateTodo},
		units.StateDone:      {units.StateTodo, units.StateInProgress, units.StateDone},
		units.StateBlocked:   {units.StateTodo, units.StateBlocked},
		units.StateCancelled: {units.StateCancelled},
	}[to]
	for _, s := range path {
		cur, err := e.units.Get(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if cur.State == s {
			continue
		}
		if _, err := e.units.SetState(ctx, id, cur.Fingerprint, s, "test"); err != nil {
			t.Fatal(err)
		}
	}
}

func (e *env) originHead(t *testing.T) string {
	t.Helper()
	out := e.repo.Git("ls-remote", "origin", "refs/heads/"+e.branch)
	hash, _, _ := strings.Cut(out, "\t")
	return hash
}

func (e *env) run(t *testing.T) (prstep.CreateResult, error) {
	t.Helper()
	return prstep.Create(ctx, e.deps, prstep.CreateInput{SliceID: e.slice.ID})
}

func (e *env) mustRun(t *testing.T) prstep.CreateResult {
	t.Helper()
	res, err := e.run(t)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func (e *env) calls() []string {
	var ops []string
	for _, c := range e.provider.Calls() {
		ops = append(ops, c.Op)
	}
	return ops
}

func TestBranchNameDefaultTemplate(t *testing.T) {
	cfg := loadConfig(t, t.TempDir())
	got, err := prstep.BranchName(setting(cfg, "git.branch_template"), units.Unit{ID: "S-1", Level: units.LevelSlice, Number: 1, Title: "Add the Thing!", ParentID: "B-1"})
	if err != nil || got != "ralph/s-1-add-the-thing" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestBranchNameMatchesLoop(t *testing.T) {
	e := newEnv(t)
	e.repo.Git("switch", "main")
	e.repo.Git("branch", "-D", e.branch)
	cfg := loadConfig(t, e.repo.Dir)
	plan, err := loopPlan(e, cfg)
	if err != nil {
		t.Fatal(err)
	}
	got, err := prstep.BranchName(setting(cfg, "git.branch_template"), e.slice)
	if err != nil || got != plan {
		t.Fatalf("BranchName = %q, %v; loop gives %q", got, err, plan)
	}
}

func TestBuildBodyFull(t *testing.T) {
	got := prstep.BuildBody(prstep.BodyInput{
		SliceID: "S-2", SliceBody: "The slice body.\n", BriefID: "B-1", BriefTitle: "Ship it",
		Tasks: []prstep.BodyTask{
			{ID: "T-4", Title: "First", Commit: "0123456789abcdef0123"},
			{ID: "T-5", Title: "Second", Commit: "abc"},
			{ID: "T-6", Title: "Third"},
		},
		Report: verify.Report{Results: []verify.Result{
			{Command: "make test", Duration: 1234 * time.Millisecond},
			{Command: "make lint", Duration: 40 * time.Millisecond},
		}},
		Configured: true,
	})
	want := "Part of B-1: Ship it.\n\nThe slice body.\n\n" +
		"## Tasks\n\n- T-4: First (0123456789ab)\n- T-5: Second (abc)\n- T-6: Third\n\n" +
		"## Verification\n\n- make test: passed (1.2s)\n- make lint: passed (0s)\n\n" +
		"Opened by ralph for S-2.\n"
	if got != want {
		t.Fatalf("body =\n%q\nwant\n%q", got, want)
	}
}

func TestBuildBodyNoBriefNoCommitNoVerification(t *testing.T) {
	got := prstep.BuildBody(prstep.BodyInput{
		SliceID: "S-1", SliceBody: "Body.", Tasks: []prstep.BodyTask{{ID: "T-1", Title: "Only"}},
	})
	want := "Body.\n\n## Tasks\n\n- T-1: Only\n\n## Verification\n\nNo verification commands are configured for this project.\n\nOpened by ralph for S-1.\n"
	if got != want {
		t.Fatalf("body =\n%q\nwant\n%q", got, want)
	}
}

func TestCreateOpensDraftPullRequest(t *testing.T) {
	e := newEnv(t)
	e.deps.Config.Verify = verify.Settings{Commands: []string{"make check"}}
	e.deps.Verifier = verifierFunc(func(c []string) (verify.Report, error) {
		return verify.Report{Results: []verify.Result{{Command: c[0], Duration: time.Second}}}, nil
	})
	res := e.mustRun(t)
	if res.Reused || res.Branch != e.branch || res.Remote != "origin" {
		t.Fatalf("result = %+v", res)
	}
	pr := res.PullRequest
	if pr.Title != "S-1: The slice" || pr.Base != "main" || pr.Head != e.branch || !pr.Draft {
		t.Errorf("pull request = %+v", pr)
	}
	for _, want := range []string{"Part of B-1: Ship it.", "The slice body.", "## Tasks", "- T-1: First task (", "## Verification", "- make check: passed (1s)", "Opened by ralph for S-1."} {
		if !strings.Contains(pr.Body, want) {
			t.Errorf("body lacks %q:\n%s", want, pr.Body)
		}
	}
	links, _ := e.units.Links(ctx, e.slice.ID)
	if len(links) != 1 || links[0].Target != "pr:"+pr.URL {
		t.Errorf("links = %+v", links)
	}
	notes, _ := e.units.Notes(ctx, e.slice.ID)
	if len(notes) != 1 || notes[0].Body != "Pull request opened: "+pr.URL {
		t.Errorf("notes = %+v", notes)
	}
	recs, _ := e.units.PullRequests(ctx, e.slice.ID)
	if len(recs) != 1 || recs[0].Number != pr.Number || recs[0].State != units.PullRequestOpen || recs[0].Head != e.branch || recs[0].Base != "main" || recs[0].URL != pr.URL {
		t.Errorf("records = %+v", recs)
	}
	if e.originHead(t) != e.repo.Git("rev-parse", "HEAD") {
		t.Error("the branch is not on the origin")
	}
	if len(e.lines) < 2 || e.lines[0] != "verification: make check (passed)" || e.lines[len(e.lines)-1] != "pushed "+e.branch+" to origin" {
		t.Errorf("lines = %q", e.lines)
	}
}

func TestCreateReusesOpenPullRequest(t *testing.T) {
	e := newEnv(t)
	first := e.mustRun(t)
	e.repo.WriteFile("more.txt", "feedback\n")
	head := e.repo.Commit("address feedback")
	second := e.mustRun(t)
	if !second.Reused || second.PullRequest.Number != first.PullRequest.Number {
		t.Fatalf("second = %+v", second)
	}
	if e.originHead(t) != head {
		t.Error("new commits were not pushed")
	}
	recs, _ := e.units.PullRequests(ctx, e.slice.ID)
	if len(recs) != 1 {
		t.Errorf("records = %+v", recs)
	}
	links, _ := e.units.Links(ctx, e.slice.ID)
	if len(links) != 1 {
		t.Errorf("links = %+v", links)
	}
	notes, _ := e.units.Notes(ctx, e.slice.ID)
	if len(notes) != 2 || notes[1].Body != "Pull request reused: "+first.PullRequest.URL {
		t.Errorf("notes = %+v", notes)
	}
	creates := 0
	for _, op := range e.calls() {
		if op == "create" {
			creates++
		}
	}
	if creates != 1 {
		t.Errorf("calls = %v", e.calls())
	}
}

type staleFind struct {
	*fake.Provider
	finds int
}

func (s *staleFind) FindPullRequest(c context.Context, head string) (hosting.PullRequest, bool, error) {
	s.finds++
	if s.finds == 1 {
		return hosting.PullRequest{}, false, nil
	}
	return s.Provider.FindPullRequest(c, head)
}

func TestCreateConflictFindsAgain(t *testing.T) {
	e := newEnv(t)
	existing, err := e.provider.CreatePullRequest(ctx, hosting.NewPullRequest{Head: e.branch, Base: "main", Title: "t"})
	if err != nil {
		t.Fatal(err)
	}
	stale := &staleFind{Provider: e.provider}
	e.deps.Provider = func(context.Context) (hosting.Provider, error) { return stale, nil }
	res := e.mustRun(t)
	if !res.Reused || res.PullRequest.Number != existing.Number {
		t.Fatalf("result = %+v", res)
	}
}

func TestCreateRefusesNonSlice(t *testing.T) {
	e := newEnv(t)
	for _, id := range []string{e.brief.ID, e.tasks[0].ID} {
		_, err := prstep.Create(ctx, e.deps, prstep.CreateInput{SliceID: id})
		var ns *prstep.NotSliceError
		if !errors.As(err, &ns) || ns.ID != id {
			t.Errorf("%s: err = %v", id, err)
		}
	}
	if _, err := prstep.Create(ctx, e.deps, prstep.CreateInput{SliceID: "S-99"}); !errors.Is(err, units.ErrNotFound) {
		t.Errorf("unknown: err = %v", err)
	}
}

func TestCreateRefusesNothingDone(t *testing.T) {
	e := newEnv(t)
	empty := e.create(t, units.NewUnit{Level: units.LevelSlice, ParentID: e.brief.ID, Title: "Empty"})
	_, err := prstep.Create(ctx, e.deps, prstep.CreateInput{SliceID: empty.ID})
	var nd *prstep.NothingDoneError
	if !errors.As(err, &nd) || !strings.Contains(err.Error(), "ralph loop "+empty.ID) {
		t.Fatalf("err = %v", err)
	}
}

func TestCreateRefusesUnfinishedTasks(t *testing.T) {
	e := newEnv(t)
	todo := e.create(t, units.NewUnit{Level: units.LevelTask, ParentID: e.slice.ID, Title: "Todo", Body: "b", AcceptanceCriteria: "c"})
	e.move(t, todo.ID, units.StateTodo)
	blocked := e.create(t, units.NewUnit{Level: units.LevelTask, ParentID: e.slice.ID, Title: "Blocked", Body: "b", AcceptanceCriteria: "c"})
	e.move(t, blocked.ID, units.StateBlocked)
	cancelled := e.create(t, units.NewUnit{Level: units.LevelTask, ParentID: e.slice.ID, Title: "Cancelled"})
	e.move(t, cancelled.ID, units.StateCancelled)
	_, err := e.run(t)
	var ue *prstep.UnfinishedError
	if !errors.As(err, &ue) {
		t.Fatalf("err = %v", err)
	}
	msg := err.Error()
	for _, want := range []string{todo.ID + " (TODO)", blocked.ID + " (BLOCKED)", "ralph loop " + e.slice.ID} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q lacks %q", msg, want)
		}
	}
	if strings.Contains(msg, cancelled.ID) {
		t.Errorf("message names the cancelled task: %q", msg)
	}
}

func TestCreateRefusesOtherBranch(t *testing.T) {
	e := newEnv(t)
	e.repo.Git("switch", "main")
	_, err := e.run(t)
	var nb *prstep.NotOnBranchError
	if !errors.As(err, &nb) || nb.Branch != e.branch || nb.Found != "main" {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(err.Error(), "git switch "+e.branch) {
		t.Errorf("message = %q", err)
	}
}

func TestCreateRefusesMissingBranch(t *testing.T) {
	e := newEnv(t)
	e.repo.Git("switch", "main")
	e.repo.Git("branch", "-D", e.branch)
	_, err := e.run(t)
	want := "branch " + e.branch + " does not exist: run ralph loop S-1 first"
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v, want %q", err, want)
	}
}

func TestCreateRefusesDirtyTree(t *testing.T) {
	e := newEnv(t)
	e.repo.WriteFile("scratch.txt", "x")
	_, err := e.run(t)
	if err == nil || !strings.Contains(err.Error(), "scratch.txt") || !strings.Contains(err.Error(), "changes") {
		t.Fatalf("err = %v", err)
	}
	if e.originHead(t) != "" || len(e.calls()) != 0 {
		t.Error("something left the machine")
	}
}

func TestCreateVerificationFailureNothingPushed(t *testing.T) {
	e := newEnv(t)
	e.deps.Config.Verify = verify.Settings{Commands: []string{"make a", "make b"}}
	e.deps.Verifier = verifierFunc(func([]string) (verify.Report, error) {
		return verify.Report{Results: []verify.Result{{Command: "make a"}, {Command: "make b", ExitCode: 2, Output: "boom"}}}, nil
	})
	_, err := e.run(t)
	var ve *prstep.VerificationError
	if !errors.As(err, &ve) || ve.Result.Command != "make b" {
		t.Fatalf("err = %v", err)
	}
	if e.originHead(t) != "" {
		t.Error("the branch was pushed")
	}
	if len(e.calls()) != 0 {
		t.Errorf("provider calls = %v", e.calls())
	}
	recs, _ := e.units.PullRequests(ctx, e.slice.ID)
	if len(recs) != 0 {
		t.Errorf("records = %+v", recs)
	}
}

func TestCreateNoVerificationCommandsSaysSo(t *testing.T) {
	e := newEnv(t)
	res := e.mustRun(t)
	if len(e.lines) == 0 || e.lines[0] != "verification: none configured" {
		t.Errorf("lines = %q", e.lines)
	}
	if !strings.Contains(res.PullRequest.Body, "No verification commands are configured for this project.") {
		t.Errorf("body = %q", res.PullRequest.Body)
	}
}

func TestCreateProviderFailureAfterPushSaysPushed(t *testing.T) {
	e := newEnv(t)
	e.provider.BringDown()
	_, err := e.run(t)
	if err == nil || !strings.Contains(err.Error(), "the branch is pushed; run ralph pr create again to retry") {
		t.Fatalf("err = %v", err)
	}
	if e.originHead(t) == "" {
		t.Error("the branch is not on the origin")
	}
	recs, _ := e.units.PullRequests(ctx, e.slice.ID)
	if len(recs) != 0 {
		t.Errorf("records = %+v", recs)
	}
}

func TestCreateNeverForcePushes(t *testing.T) {
	e := newEnv(t)
	e.mustRun(t)
	remote := e.originHead(t)
	e.repo.Git("reset", "--hard", "HEAD~1")
	e.repo.WriteFile("diverged.txt", "other\n")
	e.repo.Commit("diverged")
	_, err := e.run(t)
	if err == nil {
		t.Fatal("a diverged branch was pushed")
	}
	if e.originHead(t) != remote {
		t.Error("the remote branch was overwritten")
	}
}
