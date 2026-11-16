package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bengearig/ralph/internal/db"
	"github.com/bengearig/ralph/internal/testutil"
	"github.com/bengearig/ralph/internal/units"
)

type statusEnv struct {
	t    *testing.T
	repo *testutil.Repo
	ctx  context.Context
}

func newStatusEnv(t *testing.T) *statusEnv {
	t.Helper()
	repo := inRepo(t)
	if code, _, stderr := run(t, "init"); code != 0 {
		t.Fatalf("init: %d %q", code, stderr)
	}
	return &statusEnv{t: t, repo: repo, ctx: units.WithActor(context.Background(), "Ann <ann@example.com>")}
}

func (e *statusEnv) with(fn func(svc units.Service)) {
	e.t.Helper()
	d, err := db.Open(e.ctx, filepath.Join(e.repo.Dir, ".ralph", "store.db"), db.Options{})
	if err != nil {
		e.t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	fn(units.NewService(d))
}

func (e *statusEnv) create(svc units.Service, level units.Level, parent, title string, state units.State) string {
	e.t.Helper()
	n := units.NewUnit{Level: level, ParentID: parent, Title: title, Body: "body of " + title}
	if level == units.LevelTask {
		n.AcceptanceCriteria = "criteria of " + title
	}
	u, err := svc.Create(e.ctx, n)
	if err != nil {
		e.t.Fatal(err)
	}
	if state != "" && state != units.StateProposed {
		cur := u
		for _, step := range pathTo(state) {
			if cur, err = svc.SetState(e.ctx, u.ID, cur.Fingerprint, step, ""); err != nil {
				e.t.Fatal(err)
			}
		}
	}
	return u.ID
}

func pathTo(s units.State) []units.State {
	switch s {
	case units.StateDone:
		return []units.State{units.StateTodo, units.StateInProgress, units.StateDone}
	case units.StateInProgress:
		return []units.State{units.StateTodo, units.StateInProgress}
	default:
		return []units.State{s}
	}
}

func (e *statusEnv) status(args ...string) (int, string, string) {
	e.t.Helper()
	return run(e.t, append([]string{"status"}, args...)...)
}

func lines(out string) []string {
	var r []string
	for _, l := range strings.Split(out, "\n") {
		r = append(r, strings.Join(strings.Fields(l), " "))
	}
	return r
}

func hasLine(out, want string) bool {
	for _, l := range lines(out) {
		if l == want {
			return true
		}
	}
	return false
}

func requireLine(t *testing.T, out, want string) {
	t.Helper()
	if !hasLine(out, want) {
		t.Errorf("no line %q in:\n%s", want, out)
	}
}

func TestStatusSummaryTree(t *testing.T) {
	e := newStatusEnv(t)
	e.with(func(s units.Service) {
		b := e.create(s, units.LevelBrief, "", "Brief one", "")
		s1 := e.create(s, units.LevelSlice, b, "First slice", "")
		s2 := e.create(s, units.LevelSlice, b, "Second slice", "")
		e.create(s, units.LevelTask, s1, "Task a", units.StateTodo)
		e.create(s, units.LevelTask, s1, "Task b", units.StateDone)
		_ = s2
		e.create(s, units.LevelTask, "", "Loose task", units.StateTodo)
	})
	code, out, stderr := e.status()
	if code != 0 {
		t.Fatalf("code %d %q", code, stderr)
	}
	requireLine(t, out, "B-1 ARTICULATING Brief one")
	requireLine(t, out, "S-1 READY First slice 1 TODO, 1 DONE")
	requireLine(t, out, "S-2 EMPTY Second slice no tasks")
	requireLine(t, out, "Tasks with no slice")
	requireLine(t, out, "T-3 TODO Loose task")
	if !strings.Contains(out, "\n  S-1") {
		t.Errorf("slice not indented:\n%s", out)
	}
	if strings.Contains(out, "hidden") {
		t.Errorf("unexpected hidden line:\n%s", out)
	}
}

func TestStatusCountsOrderAndOmitZero(t *testing.T) {
	e := newStatusEnv(t)
	e.with(func(s units.Service) {
		b := e.create(s, units.LevelBrief, "", "B", "")
		sl := e.create(s, units.LevelSlice, b, "S", "")
		e.create(s, units.LevelTask, sl, "d", units.StateDone)
		e.create(s, units.LevelTask, sl, "t1", units.StateTodo)
		e.create(s, units.LevelTask, sl, "p", "")
		e.create(s, units.LevelTask, sl, "t2", units.StateTodo)
	})
	_, out, _ := e.status()
	requireLine(t, out, "S-1 ARTICULATING S 1 PROPOSED, 2 TODO, 1 DONE")
}

func TestStatusHidesFinishedUnlessAll(t *testing.T) {
	e := newStatusEnv(t)
	e.with(func(s units.Service) {
		done := e.create(s, units.LevelBrief, "", "Done brief", "")
		ds := e.create(s, units.LevelSlice, done, "Done slice", "")
		e.create(s, units.LevelTask, ds, "x", units.StateDone)
		live := e.create(s, units.LevelBrief, "", "Live brief", "")
		cs := e.create(s, units.LevelSlice, live, "Cancelled slice", "")
		e.create(s, units.LevelTask, cs, "y", units.StateCancelled)
		ls := e.create(s, units.LevelSlice, live, "Live slice", "")
		e.create(s, units.LevelTask, ls, "z", units.StateTodo)
		e.create(s, units.LevelTask, "", "Done orphan", units.StateDone)
	})
	_, out, _ := e.status()
	for _, gone := range []string{"Done brief", "Done slice", "Cancelled slice", "Done orphan"} {
		if strings.Contains(out, gone) {
			t.Errorf("%q shown:\n%s", gone, out)
		}
	}
	requireLine(t, out, "B-2 READY Live brief")
	if !strings.Contains(out, "finished item(s) hidden; use --all") {
		t.Errorf("no hidden line:\n%s", out)
	}
	_, out, _ = e.status("--all")
	for _, want := range []string{"Done brief", "Done slice", "Cancelled slice", "Done orphan"} {
		if !strings.Contains(out, want) {
			t.Errorf("%q missing:\n%s", want, out)
		}
	}
	if strings.Contains(out, "hidden") {
		t.Errorf("hidden line with --all:\n%s", out)
	}
}

func TestStatusEmptyProject(t *testing.T) {
	e := newStatusEnv(t)
	code, out, _ := e.status()
	if code != 0 || out != "No work recorded yet.\n" {
		t.Fatalf("code %d out %q", code, out)
	}
}

func TestStatusNoDatabase(t *testing.T) {
	repo := inRepo(t)
	code, out, stderr := run(t, "status")
	if code != 0 || stderr != "" {
		t.Fatalf("code %d stderr %q", code, stderr)
	}
	want := `No work recorded yet: this project has no ralph database. Run "ralph init" to set it up.` + "\n"
	if out != want {
		t.Errorf("out = %q", out)
	}
	if _, err := os.Stat(filepath.Join(repo.Dir, ".ralph")); !os.IsNotExist(err) {
		t.Errorf(".ralph exists: %v", err)
	}
}

func TestStatusOutsideRepository(t *testing.T) {
	testutil.IsolateEnv(t)
	t.Chdir(t.TempDir())
	code, _, stderr := run(t, "status")
	if code != 1 || !strings.Contains(stderr, "not in a git repository") {
		t.Fatalf("code %d stderr %q", code, stderr)
	}
}

func seedTask(e *statusEnv) {
	e.with(func(s units.Service) {
		b := e.create(s, units.LevelBrief, "", "B", "")
		sl := e.create(s, units.LevelSlice, b, "The slice", "")
		id := e.create(s, units.LevelTask, sl, "The task", units.StateTodo)
		if err := s.AddLink(e.ctx, id, units.LinkDocument, "D-1"); err != nil {
			e.t.Fatal(err)
		}
		if _, err := s.AddNote(e.ctx, id, units.NoteGeneral, "first note"); err != nil {
			e.t.Fatal(err)
		}
		if _, err := s.AddNote(e.ctx, id, units.NoteGeneral, "second note"); err != nil {
			e.t.Fatal(err)
		}
	})
}

func TestStatusTask(t *testing.T) {
	e := newStatusEnv(t)
	seedTask(e)
	code, out, stderr := e.status("T-1")
	if code != 0 {
		t.Fatalf("code %d %q", code, stderr)
	}
	requireLine(t, out, "T-1 TODO The task")
	requireLine(t, out, "parent: S-1 The slice")
	requireLine(t, out, "body of The task")
	requireLine(t, out, "Acceptance criteria:")
	requireLine(t, out, "criteria of The task")
	requireLine(t, out, "Links:")
	requireLine(t, out, "document D-1")
	requireLine(t, out, "Notes:")
	if !strings.Contains(out, "created: ") || !strings.Contains(out, " by Ann <ann@example.com>\n") {
		t.Errorf("actor missing:\n%s", out)
	}
	for _, l := range lines(out) {
		if strings.HasPrefix(l, "updated: ") && !strings.HasSuffix(l, "by Ann <ann@example.com>") {
			t.Errorf("updated line = %q", l)
		}
	}
	if strings.Index(out, "first note") > strings.Index(out, "second note") || !strings.Contains(out, "first note") {
		t.Errorf("notes out of order:\n%s", out)
	}
}

func TestStatusOrphanTaskParentLine(t *testing.T) {
	e := newStatusEnv(t)
	e.with(func(s units.Service) { e.create(s, units.LevelTask, "", "Loose", "") })
	_, out, _ := e.status("T-1")
	requireLine(t, out, "parent: no slice")
}

func TestStatusBriefShowsInstructionVerbatim(t *testing.T) {
	e := newStatusEnv(t)
	body := "Line one\n\n  indented  two\nline three"
	e.with(func(s units.Service) {
		if _, err := s.Create(e.ctx, units.NewUnit{Level: units.LevelBrief, Title: "B", Body: body}); err != nil {
			t.Fatal(err)
		}
		e.create(s, units.LevelSlice, "B-1", "Child", "")
	})
	_, out, _ := e.status("B-1")
	if !strings.Contains(out, "\n"+body+"\n") {
		t.Errorf("body not verbatim:\n%s", out)
	}
	requireLine(t, out, "parent: none")
	requireLine(t, out, "S-1 EMPTY Child")
}

func TestStatusSliceListsTasks(t *testing.T) {
	e := newStatusEnv(t)
	seedTask(e)
	_, out, _ := e.status("S-1")
	requireLine(t, out, "S-1 READY The slice")
	requireLine(t, out, "parent: B-1 B")
	requireLine(t, out, "T-1 TODO The task")
}

func TestStatusIDCaseInsensitive(t *testing.T) {
	e := newStatusEnv(t)
	seedTask(e)
	code, out, _ := e.status("t-1")
	if code != 0 || !hasLine(out, "T-1 TODO The task") {
		t.Fatalf("code %d out %q", code, out)
	}
}

func TestStatusBadID(t *testing.T) {
	e := newStatusEnv(t)
	for _, id := range []string{"b1", "X-1", "T-", "T-1x"} {
		code, _, stderr := e.status(id)
		if code != 1 || !strings.Contains(stderr, "not a unit ID") {
			t.Errorf("%s: code %d stderr %q", id, code, stderr)
		}
	}
}

func TestStatusUnknownID(t *testing.T) {
	e := newStatusEnv(t)
	code, _, stderr := e.status("T-99")
	if code != 1 || !strings.Contains(stderr, "not found") {
		t.Fatalf("code %d stderr %q", code, stderr)
	}
}

func TestStatusJSONSummary(t *testing.T) {
	e := newStatusEnv(t)
	e.with(func(s units.Service) {
		b := e.create(s, units.LevelBrief, "", "B", "")
		sl := e.create(s, units.LevelSlice, b, "S", "")
		e.create(s, units.LevelTask, sl, "a", units.StateTodo)
		e.create(s, units.LevelTask, sl, "b", units.StateTodo)
		e.create(s, units.LevelTask, sl, "c", units.StateDone)
		e.create(s, units.LevelTask, "", "o", "")
	})
	code, out, stderr := e.status("--json")
	if code != 0 {
		t.Fatalf("code %d %q", code, stderr)
	}
	var got summaryJSON
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Briefs) != 1 || got.Briefs[0].ID != "B-1" || len(got.Briefs[0].Slices) != 1 {
		t.Fatalf("got %+v", got)
	}
	tasks := got.Briefs[0].Slices[0].Tasks
	if tasks["TODO"] != 2 || tasks["DONE"] != 1 || len(tasks) != 2 {
		t.Errorf("tasks = %v", tasks)
	}
	if len(got.Tasks) != 1 || got.Tasks[0].ID != "T-4" || got.Tasks[0].Status != "PROPOSED" {
		t.Errorf("orphans = %+v", got.Tasks)
	}
}

func TestStatusJSONUnit(t *testing.T) {
	e := newStatusEnv(t)
	seedTask(e)
	_, out, _ := e.status("--json", "S-1")
	var got unitJSON
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if got.ID != "S-1" || got.Status != "READY" || len(got.Children) != 1 || got.Parent == nil {
		t.Errorf("got %+v", got)
	}
	_, out, _ = e.status("--json", "T-1")
	got = unitJSON{}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Links) != 1 || len(got.Notes) != 2 || got.Notes[0].Body != "first note" || got.Children == nil {
		t.Errorf("got %+v", got)
	}
}

func TestStatusReadOnly(t *testing.T) {
	e := newStatusEnv(t)
	seedTask(e)
	path := filepath.Join(e.repo.Dir, ".ralph", "store.db")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{}, {"--all"}, {"--json"}, {"T-1"}, {"B-1", "--json"}} {
		if code, _, stderr := e.status(args...); code != 0 {
			t.Fatalf("%v: %d %q", args, code, stderr)
		}
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Error("database file changed")
	}
}
