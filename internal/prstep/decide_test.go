package prstep_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/bengearig/ralph/internal/loop"
	"github.com/bengearig/ralph/internal/project"
	"github.com/bengearig/ralph/internal/prstep"
	"github.com/bengearig/ralph/internal/units"
	"github.com/bengearig/ralph/internal/verify"
)

// closeLatest closes the latest pull request on the host and in the store.
func (e *env) closeLatest(t *testing.T) units.PullRequestRecord {
	t.Helper()
	recs := e.records(t)
	rec := recs[len(recs)-1]
	e.provider.Close(rec.Number)
	if err := e.units.SetPullRequestState(ctx, e.slice.ID, rec.Number, units.PullRequestClosed); err != nil {
		t.Fatal(err)
	}
	return rec
}

// stopped opens a pull request and closes it.
func (e *env) stopped(t *testing.T) units.PullRequestRecord {
	t.Helper()
	e.mustRun(t)
	return e.closeLatest(t)
}

func (e *env) notesOf(t *testing.T, id string) []units.Note {
	t.Helper()
	notes, err := e.units.Notes(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	return notes
}

func (e *env) state(t *testing.T, id string) units.State {
	t.Helper()
	u, err := e.units.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	return u.State
}

func (e *env) addTask(t *testing.T, parent, title string, to units.State) units.Unit {
	t.Helper()
	task := e.create(t, units.NewUnit{Level: units.LevelTask, ParentID: parent, Title: title, Body: "b", AcceptanceCriteria: "c"})
	switch to {
	case units.StateProposed:
	case units.StateInProgress:
		e.move(t, task.ID, units.StateTodo)
		cur, _ := e.units.Get(ctx, task.ID)
		if _, err := e.units.SetState(ctx, task.ID, cur.Fingerprint, units.StateInProgress, "test"); err != nil {
			t.Fatal(err)
		}
	default:
		e.move(t, task.ID, to)
	}
	return task
}

func (e *env) latest(t *testing.T) units.PullRequestRecord {
	recs := e.records(t)
	return recs[len(recs)-1]
}

func TestNotWaitingForDecision(t *testing.T) {
	e := newEnv(t)
	want := e.slice.ID + " is not waiting for a decision"
	check := func(label string) {
		t.Helper()
		_, err := prstep.Reopen(ctx, e.deps, e.slice.ID)
		var nw *prstep.NotWaitingError
		if err == nil || err.Error() != want || errors.As(err, new(*prstep.NeedsDecisionError)) || !errors.As(err, &nw) {
			t.Errorf("%s reopen: %v", label, err)
		}
		if _, err := prstep.Rearticulate(ctx, e.units, e.slice.ID); err == nil || err.Error() != want {
			t.Errorf("%s rearticulate: %v", label, err)
		}
		if _, err := prstep.Cancel(ctx, e.units, e.slice.ID, prstep.CancelOptions{Yes: true}); err == nil || err.Error() != want {
			t.Errorf("%s cancel: %v", label, err)
		}
	}
	check("no pull request")
	pr := e.mustRun(t).PullRequest
	check("open")
	if err := e.units.SetPullRequestState(ctx, e.slice.ID, pr.Number, units.PullRequestMerged); err != nil {
		t.Fatal(err)
	}
	check("merged")
	if err := e.units.SetPullRequestState(ctx, e.slice.ID, pr.Number, units.PullRequestClosed); err != nil {
		t.Fatal(err)
	}
	if err := e.units.ResolvePullRequest(ctx, e.slice.ID, pr.Number, "cancelled"); err != nil {
		t.Fatal(err)
	}
	check("resolved")
	if got := e.state(t, e.tasks[0].ID); got != units.StateDone {
		t.Errorf("a refusal changed a task: %s", got)
	}
}

func TestReopenOpensNewPullRequestAndResolvesOld(t *testing.T) {
	e := newEnv(t)
	old := e.stopped(t)
	res, err := prstep.Reopen(ctx, e.deps, e.slice.ID)
	if err != nil {
		t.Fatal(err)
	}
	recs := e.records(t)
	if len(recs) != 2 {
		t.Fatalf("records = %+v", recs)
	}
	if recs[0].Number != old.Number || recs[0].State != units.PullRequestClosed || recs[0].Resolution != "reopened" {
		t.Errorf("old = %+v", recs[0])
	}
	if recs[1].State != units.PullRequestOpen || recs[1].Resolution != "" || recs[1].URL == old.URL || recs[1].URL != res.Created.PullRequest.URL {
		t.Errorf("new = %+v", recs[1])
	}
	if res.Resolved.Number != old.Number {
		t.Errorf("resolved = %+v", res.Resolved)
	}
	want := "Pull request " + old.URL + " was closed without merging; reopened as " + recs[1].URL + "."
	var found bool
	for _, b := range e.noteBodies(t) {
		found = found || b == want
	}
	if !found {
		t.Errorf("notes = %q", e.noteBodies(t))
	}
	if got := e.statuses(t)[e.slice.ID]; got == units.StatusNeedsAttention {
		t.Errorf("status = %s", got)
	}
}

func TestReopenFailureResolvesNothing(t *testing.T) {
	e := newEnv(t)
	old := e.stopped(t)
	e.deps.Config.Verify = verify.Settings{Commands: []string{"make a"}}
	e.deps.Verifier = verifierFunc(func([]string) (verify.Report, error) {
		return verify.Report{Results: []verify.Result{{Command: "make a", ExitCode: 1}}}, nil
	})
	_, err := prstep.Reopen(ctx, e.deps, e.slice.ID)
	var ve *prstep.VerificationError
	if !errors.As(err, &ve) {
		t.Fatalf("err = %v", err)
	}
	e.deps.Verifier = nil
	e.deps.Config.Verify = verify.Settings{}
	e.provider.BringDown()
	if _, err := prstep.Reopen(ctx, e.deps, e.slice.ID); err == nil {
		t.Fatal("no error from a provider that is down")
	}
	recs := e.records(t)
	if len(recs) != 1 || recs[0].Number != old.Number || recs[0].Resolution != "" {
		t.Errorf("records = %+v", recs)
	}
	for _, b := range e.noteBodies(t) {
		if strings.Contains(b, "reopened as") {
			t.Errorf("note = %q", b)
		}
	}
}

func TestRearticulateMovesUnfinishedTasks(t *testing.T) {
	e := newEnv(t)
	e.mustRun(t)
	todo := e.addTask(t, e.slice.ID, "todo", units.StateTodo)
	blocked := e.addTask(t, e.slice.ID, "blocked", units.StateBlocked)
	proposed := e.addTask(t, e.slice.ID, "proposed", units.StateProposed)
	running := e.addTask(t, e.slice.ID, "running", units.StateInProgress)
	cancelled := e.addTask(t, e.slice.ID, "cancelled", units.StateCancelled)
	old := e.closeLatest(t)
	reason := "pull request " + old.URL + " was closed without merging; plan this slice again"

	res, err := prstep.Rearticulate(ctx, e.units, e.slice.ID)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{todo.ID, blocked.ID, proposed.ID, running.ID}; !reflect.DeepEqual(res.Moved, want) {
		t.Errorf("moved = %v, want %v", res.Moved, want)
	}
	if want := []string{e.tasks[0].ID, e.tasks[1].ID}; !reflect.DeepEqual(res.Kept, want) {
		t.Errorf("kept = %v, want %v", res.Kept, want)
	}
	for _, id := range res.Moved {
		if got := e.state(t, id); got != units.StateProposed {
			t.Errorf("%s = %s", id, got)
		}
		var kick []string
		for _, n := range e.notesOf(t, id) {
			if n.Kind == units.NoteKickback {
				kick = append(kick, n.Body)
			}
		}
		if len(kick) != 1 || !strings.HasSuffix(kick[0], reason) {
			t.Errorf("%s kickback notes = %q", id, kick)
		}
	}
	var interrupted bool
	for _, n := range e.notesOf(t, running.ID) {
		interrupted = interrupted || strings.Contains(n.Body, "IN_PROGRESS to TODO: loop interrupted by a closed pull request")
	}
	if !interrupted {
		t.Errorf("running notes = %+v", e.notesOf(t, running.ID))
	}
	for _, id := range []string{e.tasks[0].ID, e.tasks[1].ID} {
		if got := e.state(t, id); got != units.StateDone {
			t.Errorf("%s = %s", id, got)
		}
		if notes := e.notesOf(t, id); len(notes) != 3 {
			t.Errorf("%s notes changed: %+v", id, notes)
		}
	}
	if got := e.state(t, cancelled.ID); got != units.StateCancelled || len(e.notesOf(t, cancelled.ID)) != 1 {
		t.Errorf("cancelled touched: %s", got)
	}
}

func TestRearticulateResolvesAndNotes(t *testing.T) {
	e := newEnv(t)
	e.mustRun(t)
	todo := e.addTask(t, e.slice.ID, "todo", units.StateTodo)
	old := e.closeLatest(t)
	if _, err := prstep.Rearticulate(ctx, e.units, e.slice.ID); err != nil {
		t.Fatal(err)
	}
	if rec := e.latest(t); rec.Resolution != "rearticulated" {
		t.Errorf("record = %+v", rec)
	}
	bodies := e.noteBodies(t)
	want := "Pull request " + old.URL + " was closed without merging; plan this slice again. Moved back to PROPOSED: " + todo.ID + "."
	if got := bodies[len(bodies)-1]; got != want {
		t.Errorf("note = %q, want %q", got, want)
	}
}

func TestRearticulateAllDoneSlice(t *testing.T) {
	e := newEnv(t)
	old := e.stopped(t)
	res, err := prstep.Rearticulate(ctx, e.units, e.slice.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Moved) != 0 || len(res.Kept) != 2 {
		t.Errorf("res = %+v", res)
	}
	if rec := e.latest(t); rec.Resolution != "rearticulated" {
		t.Errorf("record = %+v", rec)
	}
	bodies := e.noteBodies(t)
	if got, want := bodies[len(bodies)-1], "Pull request "+old.URL+" was closed without merging; plan this slice again."; got != want {
		t.Errorf("note = %q", got)
	}
}

func TestCancelWithoutYesChangesNothing(t *testing.T) {
	e := newEnv(t)
	e.mustRun(t)
	todo := e.addTask(t, e.slice.ID, "todo", units.StateTodo)
	e.closeLatest(t)
	notes := len(e.noteBodies(t))
	res, err := prstep.Cancel(ctx, e.units, e.slice.ID, prstep.CancelOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Done || !reflect.DeepEqual(res.Tasks, []string{todo.ID}) {
		t.Errorf("res = %+v", res)
	}
	if got := e.state(t, todo.ID); got != units.StateTodo {
		t.Errorf("task = %s", got)
	}
	if rec := e.latest(t); rec.Resolution != "" || len(e.noteBodies(t)) != notes {
		t.Errorf("record = %+v", rec)
	}
}

func TestCancelCancelsUnfinishedTasksOnly(t *testing.T) {
	e := newEnv(t)
	e.mustRun(t)
	todo := e.addTask(t, e.slice.ID, "todo", units.StateTodo)
	prop := e.addTask(t, e.slice.ID, "prop", units.StateProposed)
	old := e.closeLatest(t)
	res, err := prstep.Cancel(ctx, e.units, e.slice.ID, prstep.CancelOptions{Yes: true})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Done || !reflect.DeepEqual(res.Tasks, []string{todo.ID, prop.ID}) {
		t.Errorf("res = %+v", res)
	}
	reason := "pull request " + old.URL + " was closed without merging; slice cancelled"
	for _, id := range res.Tasks {
		if got := e.state(t, id); got != units.StateCancelled {
			t.Errorf("%s = %s", id, got)
		}
		notes := e.notesOf(t, id)
		if got := notes[len(notes)-1].Body; !strings.HasSuffix(got, reason) {
			t.Errorf("%s note = %q", id, got)
		}
	}
	if rec := e.latest(t); rec.Resolution != "cancelled" {
		t.Errorf("record = %+v", rec)
	}
	bodies := e.noteBodies(t)
	if got := bodies[len(bodies)-1]; !strings.HasPrefix(got, "Pull request "+old.URL+" was closed without merging; slice cancelled.") {
		t.Errorf("slice note = %q", got)
	}
}

func TestCancelSliceShowsCancelled(t *testing.T) {
	e := newEnv(t)
	e.mustRun(t)
	e.addTask(t, e.slice.ID, "todo", units.StateTodo)
	e.closeLatest(t)
	if _, err := prstep.Cancel(ctx, e.units, e.slice.ID, prstep.CancelOptions{Yes: true}); err != nil {
		t.Fatal(err)
	}
	// Both finished tasks stay DONE, so the slice derives to CANCELLED only
	// through the resolution.
	if got := e.statuses(t)[e.slice.ID]; got != units.StatusCancelled {
		t.Errorf("status = %s", got)
	}
}

func dependentSlices(t *testing.T, e *env) (direct, indirect units.Unit, tasks map[string]units.Unit) {
	t.Helper()
	direct = e.create(t, units.NewUnit{Level: units.LevelSlice, ParentID: e.brief.ID, Title: "Direct"})
	indirect = e.create(t, units.NewUnit{Level: units.LevelSlice, ParentID: e.brief.ID, Title: "Indirect"})
	if err := e.units.AddDependency(ctx, direct.ID, e.slice.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.units.AddDependency(ctx, indirect.ID, direct.ID); err != nil {
		t.Fatal(err)
	}
	tasks = map[string]units.Unit{
		"direct-todo":   e.addTask(t, direct.ID, "d1", units.StateTodo),
		"direct-done":   e.addTask(t, direct.ID, "d2", units.StateDone),
		"indirect-prop": e.addTask(t, indirect.ID, "i1", units.StateProposed),
	}
	return direct, indirect, tasks
}

func TestCancelDependentsFlag(t *testing.T) {
	e := newEnv(t)
	e.mustRun(t)
	direct, indirect, tk := dependentSlices(t, e)
	e.closeLatest(t)

	res, err := prstep.Cancel(ctx, e.units, e.slice.ID, prstep.CancelOptions{Yes: true})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{direct.ID, indirect.ID}; !reflect.DeepEqual(res.Dependents, want) || len(res.DependentTasks) != 0 {
		t.Errorf("without flag: %+v", res)
	}
	if got := e.state(t, tk["direct-todo"].ID); got != units.StateTodo {
		t.Errorf("dependent task touched: %s", got)
	}
	if w, _ := e.units.Waiting(ctx, direct.ID); len(w) == 0 {
		t.Error("the dependent no longer waits")
	}
}

func TestCancelDependentsFlagCancelsTheirTasks(t *testing.T) {
	e := newEnv(t)
	e.mustRun(t)
	direct, indirect, tk := dependentSlices(t, e)
	e.closeLatest(t)

	dry, err := prstep.Cancel(ctx, e.units, e.slice.ID, prstep.CancelOptions{Dependents: true})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{tk["direct-todo"].ID, tk["indirect-prop"].ID}; dry.Done || !reflect.DeepEqual(dry.DependentTasks, want) {
		t.Errorf("dry run: %+v", dry)
	}
	if got := e.state(t, tk["direct-todo"].ID); got != units.StateTodo {
		t.Errorf("dry run changed a task: %s", got)
	}

	res, err := prstep.Cancel(ctx, e.units, e.slice.ID, prstep.CancelOptions{Dependents: true, Yes: true})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{direct.ID, indirect.ID}; !reflect.DeepEqual(res.Dependents, want) {
		t.Errorf("dependents = %v", res.Dependents)
	}
	reason := e.slice.ID + " was cancelled; this slice waited for it"
	for _, k := range []string{"direct-todo", "indirect-prop"} {
		id := tk[k].ID
		if got := e.state(t, id); got != units.StateCancelled {
			t.Errorf("%s = %s", id, got)
		}
		notes := e.notesOf(t, id)
		if got := notes[len(notes)-1].Body; !strings.HasSuffix(got, reason) {
			t.Errorf("%s note = %q", id, got)
		}
	}
	for _, id := range []string{direct.ID, indirect.ID} {
		notes := e.notesOf(t, id)
		if len(notes) == 0 || !strings.HasPrefix(notes[len(notes)-1].Body, e.slice.ID+" was cancelled; this slice waited for it.") {
			t.Errorf("%s notes = %+v", id, notes)
		}
	}
}

func TestCancelNeverTouchesDoneTasks(t *testing.T) {
	e := newEnv(t)
	e.mustRun(t)
	_, _, tk := dependentSlices(t, e)
	e.closeLatest(t)
	done := []string{e.tasks[0].ID, e.tasks[1].ID, tk["direct-done"].ID}
	before := map[string]int{}
	for _, id := range done {
		before[id] = len(e.notesOf(t, id))
	}
	if _, err := prstep.Cancel(ctx, e.units, e.slice.ID, prstep.CancelOptions{Dependents: true, Yes: true}); err != nil {
		t.Fatal(err)
	}
	for _, id := range done {
		if got := e.state(t, id); got != units.StateDone {
			t.Errorf("%s = %s", id, got)
		}
		if got := len(e.notesOf(t, id)); got != before[id] {
			t.Errorf("%s notes = %+v", id, e.notesOf(t, id))
		}
	}
}

func TestResolutionAllowsLoopAgain(t *testing.T) {
	e := newEnv(t)
	e.mustRun(t)
	todo := e.addTask(t, e.slice.ID, "todo", units.StateTodo)
	e.closeLatest(t)
	cfg := loadConfig(t, e.repo.Dir)
	if _, err := project.EnsureIgnore(e.repo.Dir, project.LocalIgnoreEntries()); err != nil {
		t.Fatal(err)
	}
	e.repo.Commit("ignore local ralph files")
	prepare := func() (loop.Plan, error) {
		return loop.Prepare(ctx, loop.Deps{Units: e.units, Workspace: e.deps.Workspace, Config: cfg, Root: e.repo.Dir},
			loop.PrepareInput{Slice: e.slice.ID, DryRun: true})
	}
	var nd *prstep.NeedsDecisionError
	if _, err := prepare(); !errors.As(err, &nd) {
		t.Fatalf("before: %v", err)
	}
	if got := e.statuses(t)[e.slice.ID]; got != units.StatusNeedsAttention {
		t.Fatalf("status = %s", got)
	}
	if _, err := prstep.Rearticulate(ctx, e.units, e.slice.ID); err != nil {
		t.Fatal(err)
	}
	if got := e.statuses(t)[e.slice.ID]; got == units.StatusNeedsAttention {
		t.Errorf("status = %s", got)
	}
	plan, err := prepare()
	if err != nil {
		t.Fatal(err)
	}
	if plan.Stop == nil || plan.Stop.Task != todo.ID || plan.Stop.State != units.StateProposed {
		t.Errorf("stop = %+v", plan.Stop)
	}
}
