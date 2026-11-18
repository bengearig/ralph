package quick

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bengearig/ralph/internal/db"
	"github.com/bengearig/ralph/internal/issuesource"
	"github.com/bengearig/ralph/internal/testutil"
	"github.com/bengearig/ralph/internal/tickets"
	"github.com/bengearig/ralph/internal/units"
	"github.com/bengearig/ralph/internal/verify"
	"github.com/bengearig/ralph/internal/workspace"
)

const testTemplate = "ralph/{{.ID | lower}}-{{.Slug}}"

type fixture struct {
	db    *db.DB
	repo  *testutil.Repo
	units units.Service
	ws    workspace.Workspace
	cfg   Config
	// issues, when set, is the issue source of the service.
	issues issuesource.Source
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	testutil.IsolateEnv(t)
	repo := testutil.NewRepo(t)
	ws, err := NewLocalWorkspace(repo.Dir)
	if err != nil {
		t.Fatal(err)
	}
	d := testutil.NewDB(t, db.Migrations)
	return &fixture{
		db:    d,
		repo:  repo,
		units: units.NewService(d),
		ws:    ws,
		cfg:   Config{Root: repo.Dir, Remote: "origin", BranchTemplate: testTemplate},
	}
}

func (f *fixture) service() *Service {
	return New(Deps{Units: f.units, Workspace: f.ws, Config: f.cfg, Issues: f.issues, Tickets: tickets.Deps{DB: f.db}})
}

func (f *fixture) start(t *testing.T, title string) (Started, error) {
	t.Helper()
	return f.service().Start(context.Background(), StartInput{Title: title})
}

func (f *fixture) noUnits(t *testing.T) {
	t.Helper()
	list, err := f.units.List(context.Background(), units.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Fatalf("units = %v, want none", list)
	}
}

func (f *fixture) get(t *testing.T, id string) units.Unit {
	t.Helper()
	u, err := f.units.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func (f *fixture) notes(t *testing.T, id string) []units.Note {
	t.Helper()
	n, err := f.units.Notes(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestStartCreatesTaskAndBranch(t *testing.T) {
	f := newFixture(t)
	head := f.repo.Git("rev-parse", "HEAD")
	got, err := f.start(t, "Fix the typo")
	if err != nil {
		t.Fatal(err)
	}
	if got.Task.ID != "T-1" || got.Task.Level != units.LevelTask || got.Task.ParentID != "" || got.Task.State != units.StateInProgress {
		t.Fatalf("task = %+v", got.Task)
	}
	if got.Branch != "ralph/t-1-fix-the-typo" || got.Base != "main" || got.StartedFrom != head {
		t.Fatalf("result = %+v", got)
	}
	if cur := f.repo.Git("branch", "--show-current"); cur != got.Branch {
		t.Fatalf("checkout on %q", cur)
	}
	if stored := f.get(t, "T-1"); stored.Fingerprint != got.Task.Fingerprint {
		t.Fatal("result is not the task as last stored")
	}
}

func TestStartBodyDefaultsToTitle(t *testing.T) {
	f := newFixture(t)
	got, err := f.start(t, "  Fix the typo  ")
	if err != nil {
		t.Fatal(err)
	}
	if got.Task.Title != "Fix the typo" || got.Task.Body != "Fix the typo" {
		t.Fatalf("task = %+v", got.Task)
	}
}

func TestStartStoresBodyAndCriteria(t *testing.T) {
	f := newFixture(t)
	got, err := f.service().Start(context.Background(), StartInput{Title: "T", Body: "the body", AcceptanceCriteria: "it works"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Task.Body != "the body" || got.Task.AcceptanceCriteria != "it works" {
		t.Fatalf("task = %+v", got.Task)
	}
}

func TestStartRecordsLinkAndNote(t *testing.T) {
	f := newFixture(t)
	head := f.repo.Git("rev-parse", "HEAD")
	got, err := f.start(t, "Fix the typo")
	if err != nil {
		t.Fatal(err)
	}
	links, err := f.units.Links(context.Background(), got.Task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 1 || links[0].Kind != units.LinkExternal || links[0].Target != "branch:"+got.Branch {
		t.Fatalf("links = %+v", links)
	}
	want := "Quick work started on branch " + got.Branch + " from " + head[:12] + " (base main)."
	found := false
	for _, n := range f.notes(t, got.Task.ID) {
		found = found || n.Body == want
	}
	if !found {
		t.Fatalf("no note %q in %+v", want, f.notes(t, got.Task.ID))
	}
}

func TestStartResultCarriesVerifySettings(t *testing.T) {
	f := newFixture(t)
	f.cfg.Verify = verify.Settings{Commands: []string{"make test"}, Timeout: time.Minute, Source: "project"}
	got, err := f.start(t, "T")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Verify.Commands) != 1 || got.Verify.Commands[0] != "make test" || got.Verify.Timeout != time.Minute {
		t.Fatalf("verify = %+v", got.Verify)
	}
}

func TestStartBranchFromCustomTemplate(t *testing.T) {
	f := newFixture(t)
	f.cfg.BranchTemplate = "quick/{{.Number}}/{{.Slug}}"
	got, err := f.start(t, "Fix the typo")
	if err != nil {
		t.Fatal(err)
	}
	if got.Branch != "quick/1/fix-the-typo" {
		t.Fatalf("branch = %q", got.Branch)
	}
}

func TestStartTitleValidation(t *testing.T) {
	tests := []struct {
		name, title string
		ok          bool
	}{
		{"empty", "", false},
		{"only spaces", "   ", false},
		{"newline inside", "one\ntwo", false},
		{"carriage return inside", "one\rtwo", false},
		{"201 characters", strings.Repeat("a", 201), false},
		{"200 characters", strings.Repeat("a", 200), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			_, err := f.start(t, tt.title)
			if tt.ok {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if !errors.Is(err, units.ErrInvalid) {
				t.Fatalf("err = %v", err)
			}
			f.noUnits(t)
		})
	}
}

func TestStartRefusesDirtyTree(t *testing.T) {
	f := newFixture(t)
	f.repo.WriteFile("stray.txt", "x")
	_, err := f.start(t, "T")
	if !errors.Is(err, workspace.ErrDirty) {
		t.Fatalf("err = %v", err)
	}
	f.noUnits(t)
}

func TestStartRefusesOffBase(t *testing.T) {
	f := newFixture(t)
	f.repo.Git("switch", "-c", "other")
	_, err := f.start(t, "T")
	var nb *workspace.NotOnBaseError
	if !errors.As(err, &nb) {
		t.Fatalf("err = %v", err)
	}
	f.noUnits(t)
}

func TestStartRefusesBaseAhead(t *testing.T) {
	f := newFixture(t)
	remote := filepath.Join(t.TempDir(), "remote.git")
	f.repo.Git("init", "--bare", "-b", "main", remote)
	f.repo.Git("remote", "add", "origin", remote)
	f.repo.Git("push", "-u", "origin", "main")
	f.repo.Git("commit", "--allow-empty", "-m", "local only")
	_, err := f.start(t, "T")
	if !errors.Is(err, workspace.ErrBaseAhead) {
		t.Fatalf("err = %v", err)
	}
	f.noUnits(t)
}

func TestStartRefusesDetachedHead(t *testing.T) {
	f := newFixture(t)
	f.repo.Git("switch", "--detach")
	_, err := f.start(t, "T")
	if !errors.Is(err, workspace.ErrDetachedHead) {
		t.Fatalf("err = %v", err)
	}
	f.noUnits(t)
}

func TestNewLocalWorkspaceIgnoresManifestPaths(t *testing.T) {
	f := newFixture(t)
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
	f.repo.WriteFile("local.txt", "x")
	ws, err := NewLocalWorkspace(f.repo.Dir)
	if err != nil {
		t.Fatal(err)
	}
	st, err := ws.State(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Changes) != 0 {
		t.Fatalf("changes = %v", st.Changes)
	}
}

func TestStartIgnoresRecordedLocalFile(t *testing.T) {
	f := newFixture(t)
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
	f.repo.WriteFile("local.txt", "x")
	ws, err := NewLocalWorkspace(f.repo.Dir)
	if err != nil {
		t.Fatal(err)
	}
	f.ws = ws
	f.repo.WriteFile("other.txt", "y")
	if _, err := f.start(t, "T"); !errors.Is(err, workspace.ErrDirty) {
		t.Fatalf("unlisted file: err = %v", err)
	}
	if err := os.Remove(filepath.Join(f.repo.Dir, "other.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := f.start(t, "T"); err != nil {
		t.Fatalf("listed file blocked: %v", err)
	}
}

func TestStartUsesConfiguredBase(t *testing.T) {
	f := newFixture(t)
	f.repo.Git("switch", "-c", "develop")
	f.cfg.Base = "develop"
	got, err := f.start(t, "T")
	if err != nil {
		t.Fatal(err)
	}
	if got.Base != "develop" {
		t.Fatalf("base = %q", got.Base)
	}
}

func TestStartBranchExistsCancelsTask(t *testing.T) {
	f := newFixture(t)
	f.repo.Git("branch", "ralph/t-1-fix-the-typo")
	_, err := f.start(t, "Fix the typo")
	if !errors.Is(err, workspace.ErrBranchExists) {
		t.Fatalf("err = %v", err)
	}
	task := f.get(t, "T-1")
	if task.State != units.StateCancelled {
		t.Fatalf("state = %s", task.State)
	}
	reasoned := false
	for _, n := range f.notes(t, "T-1") {
		reasoned = reasoned || strings.Contains(n.Body, "ralph/t-1-fix-the-typo")
	}
	if !reasoned {
		t.Fatalf("no note names the branch: %+v", f.notes(t, "T-1"))
	}
	if cur := f.repo.Git("branch", "--show-current"); cur != "main" {
		t.Fatalf("checkout on %q", cur)
	}
}

type failingWorkspace struct {
	workspace.Workspace
}

func (failingWorkspace) CreateBranch(context.Context, string) error { return errors.New("disk full") }

func TestStartCreateBranchFailureCancelsTask(t *testing.T) {
	f := newFixture(t)
	f.ws = failingWorkspace{f.ws}
	_, err := f.start(t, "T")
	if err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("err = %v", err)
	}
	if st := f.get(t, "T-1").State; st != units.StateCancelled {
		t.Fatalf("state = %s", st)
	}
}

type failingLinks struct {
	units.Service
}

func (failingLinks) AddLink(context.Context, string, units.LinkKind, string) error {
	return errors.New("link store down")
}

func TestStartFailureAfterBranchCancelsAndSaysBranchRemains(t *testing.T) {
	f := newFixture(t)
	f.units = failingLinks{f.units}
	_, err := f.start(t, "Fix the typo")
	if err == nil || !strings.Contains(err.Error(), "link store down") || !strings.Contains(err.Error(), "left in place") {
		t.Fatalf("err = %v", err)
	}
	if st := f.get(t, "T-1").State; st != units.StateCancelled {
		t.Fatalf("state = %s", st)
	}
	if f.repo.Git("branch", "--list", "ralph/t-1-fix-the-typo") == "" {
		t.Fatal("branch was removed")
	}
}

func TestStartSecondTimeIsRefused(t *testing.T) {
	f := newFixture(t)
	if _, err := f.start(t, "One"); err != nil {
		t.Fatal(err)
	}
	_, err := f.start(t, "Two")
	var nb *workspace.NotOnBaseError
	if !errors.As(err, &nb) {
		t.Fatalf("err = %v", err)
	}
}

func TestStartNumbersAreSequential(t *testing.T) {
	f := newFixture(t)
	if _, err := f.start(t, "One"); err != nil {
		t.Fatal(err)
	}
	f.repo.Git("switch", "main")
	got, err := f.start(t, "Two")
	if err != nil {
		t.Fatal(err)
	}
	if got.Task.ID != "T-2" || got.Branch != "ralph/t-2-two" {
		t.Fatalf("result = %+v", got)
	}
}
