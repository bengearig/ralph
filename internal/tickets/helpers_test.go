package tickets

import (
	"context"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/bengearig/ralph/internal/db"
	"github.com/bengearig/ralph/internal/taskmanager"
	"github.com/bengearig/ralph/internal/taskmanager/taskmanagertest"
	"github.com/bengearig/ralph/internal/testutil"
	"github.com/bengearig/ralph/internal/units"
)

var testNow = time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)

// flaky fails the calls it is told to and leaves the rest to the fake.
type flaky struct {
	*taskmanagertest.Fake
	createFails   map[string]error
	scheduleFails map[string]error
	updateFails   map[string]error
	moveFails     map[string]error
	// moveEnds makes a move report that the ticket ended at this status.
	moveEnds map[string]string
}

func (f *flaky) Update(ctx context.Context, key string, fields taskmanager.Fields) error {
	if err := f.updateFails[key]; err != nil {
		return err
	}
	return f.Fake.Update(ctx, key, fields)
}

func (f *flaky) Move(ctx context.Context, key, status string, flow []string) (taskmanager.Move, error) {
	if err := f.moveFails[key]; err != nil {
		return taskmanager.Move{}, err
	}
	m, err := f.Fake.Move(ctx, key, status, flow)
	if to := f.moveEnds[key]; err == nil && to != "" {
		m.To = to
	}
	return m, err
}

func (f *flaky) Create(ctx context.Context, n taskmanager.New) (taskmanager.Ticket, error) {
	if err := f.createFails[n.Title]; err != nil {
		return taskmanager.Ticket{}, err
	}
	return f.Fake.Create(ctx, n)
}

func (f *flaky) Schedule(ctx context.Context, key, plan string) error {
	if err := f.scheduleFails[key]; err != nil {
		return err
	}
	return f.Fake.Schedule(ctx, key, plan)
}

type env struct {
	t     *testing.T
	ctx   context.Context
	db    *db.DB
	svc   units.Service
	fake  *taskmanagertest.Fake
	sys   *flaky
	store *Store
	deps  Deps
	log   []Event
	// base is how many fake calls had been made when a test finished its setup.
	base int
}

func newEnv(t *testing.T) *env {
	t.Helper()
	d := testutil.NewDB(t, db.Migrations)
	e := &env{t: t, ctx: context.Background(), db: d, svc: units.NewService(d), fake: taskmanagertest.NewFake("ABC"), store: NewStore(d)}
	e.sys = &flaky{Fake: e.fake, createFails: map[string]error{}, scheduleFails: map[string]error{}, updateFails: map[string]error{}, moveFails: map[string]error{}}
	e.deps = Deps{
		Units: e.svc, DB: d, System: e.sys,
		Settings:  taskmanager.Settings{Provider: "jira", Types: map[string]string{"brief": "Feature", "slice": "Task"}, SprintLevels: []string{"slice", "task"}},
		ProjectID: "proj", Dir: filepath.Join(t.TempDir(), ".ralph"),
		Now:    func() time.Time { return testNow },
		Report: func(ev Event) { e.log = append(e.log, ev) },
	}
	return e
}

func (e *env) mapTasks()             { e.deps.Settings.Types["task"] = "Subtask" }
func (e *env) useSprint(name string) { e.deps.Settings.Sprint = name; e.fake.SetPlans(name) }
func (e *env) sync(opt SyncOptions) (Report, error) {
	e.t.Helper()
	return Sync(e.ctx, e.deps, opt)
}

func (e *env) mustSync() Report {
	e.t.Helper()
	r, err := e.sync(SyncOptions{})
	if err != nil {
		e.t.Fatal(err)
	}
	return r
}

func (e *env) unit(level units.Level, parent, title string) units.Unit {
	e.t.Helper()
	u, err := e.svc.Create(e.ctx, units.NewUnit{Level: level, ParentID: parent, Title: title, Body: "body of " + title})
	if err != nil {
		e.t.Fatal(err)
	}
	return u
}

func (e *env) row(id string) Row {
	e.t.Helper()
	r, ok, err := e.store.Get(e.ctx, id)
	if err != nil || !ok {
		e.t.Fatalf("row %s: ok %v, err %v", id, ok, err)
	}
	return r
}

func (e *env) noRow(id string) {
	e.t.Helper()
	if _, ok, err := e.store.Get(e.ctx, id); err != nil || ok {
		e.t.Fatalf("row %s: ok %v, err %v, want none", id, ok, err)
	}
}

func (e *env) links(id string) []string {
	e.t.Helper()
	links, err := e.svc.Links(e.ctx, id)
	if err != nil {
		e.t.Fatal(err)
	}
	var out []string
	for _, l := range links {
		out = append(out, string(l.Kind)+" "+l.Target)
	}
	return out
}

func (e *env) count(method string) int {
	n := 0
	for _, c := range e.fake.Calls() {
		if c == method {
			n++
		}
	}
	return n
}

// tree makes a brief with a slice and a task under it.
func (e *env) tree() (b, s, tk units.Unit) {
	e.t.Helper()
	b = e.unit(units.LevelBrief, "", "Brief")
	s = e.unit(units.LevelSlice, b.ID, "Slice")
	tk = e.unit(units.LevelTask, s.ID, "Task")
	return b, s, tk
}

func keys(items []Item) []string {
	var out []string
	for _, it := range items {
		out = append(out, it.Unit+"="+it.Key)
	}
	return out
}

func wantKeys(t *testing.T, items []Item, want ...string) {
	t.Helper()
	if got := keys(items); !slices.Equal(got, want) {
		t.Fatalf("items = %q, want %q", got, want)
	}
}

// direct lets a ticket go from any status to any other in one transition.
func (e *env) direct() {
	all := []string{"To Do", "In Progress", "Done"}
	e.fake.SetWorkflow("To Do", map[string][]string{"To Do": all, "In Progress": all, "Done": all})
}

// setState walks a unit through the states to to.
func (e *env) setState(id string, to ...units.State) {
	e.t.Helper()
	u, err := e.svc.Get(e.ctx, id)
	if err != nil {
		e.t.Fatal(err)
	}
	for _, st := range to {
		if st == units.StateTodo && u.AcceptanceCriteria == "" {
			if u, err = e.svc.Update(e.ctx, id, u.Fingerprint, units.Change{AcceptanceCriteria: ptr("it works")}); err != nil {
				e.t.Fatal(err)
			}
		}
		if u, err = e.svc.SetState(e.ctx, id, u.Fingerprint, st, "because"); err != nil {
			e.t.Fatal(err)
		}
	}
}

func ptr(s string) *string { return &s }

func (e *env) edit(id string, c units.Change) {
	e.t.Helper()
	u, err := e.svc.Get(e.ctx, id)
	if err != nil {
		e.t.Fatal(err)
	}
	if _, err := e.svc.Update(e.ctx, id, u.Fingerprint, c); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) ticket(key string) taskmanager.Ticket {
	e.t.Helper()
	got, err := e.fake.Get(e.ctx, []string{key})
	if err != nil {
		e.t.Fatal(err)
	}
	return got[key]
}
