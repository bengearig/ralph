package tickets

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bengearig/ralph/internal/taskmanager"
	"github.com/bengearig/ralph/internal/units"
)

func TestSyncPushTitleAndBodyWhenChanged(t *testing.T) {
	e := newEnv(t)
	_, b, _ := e.tree()
	e.mustSync()
	e.edit(b.ID, units.Change{Title: ptr("New title"), Body: ptr("New body")})
	r := e.mustSync()
	if e.count("Update") != 1 {
		t.Fatalf("calls = %q", e.fake.Calls())
	}
	if len(r.Updated) != 1 || r.Updated[0] != (Item{Unit: b.ID, Key: "ABC-2", Reason: "title, body"}) {
		t.Fatalf("updated = %+v", r.Updated)
	}
	if got := e.ticket("ABC-2"); got.Title != "New title" || got.Body != "New body" {
		t.Fatalf("ticket = %+v", got)
	}
	last := e.log[len(e.log)-1]
	if last.Kind != "updated" || last.Message != "title, body" {
		t.Fatalf("event = %+v", last)
	}
}

func TestSyncPushNothingWhenUnchanged(t *testing.T) {
	e := newEnv(t)
	e.mapTasks()
	_, _, tk := e.tree()
	e.setState(tk.ID, units.StateTodo, units.StateInProgress)
	e.mustSync()
	before := len(e.fake.Calls())
	r := e.mustSync()
	if got := e.fake.Calls()[before:]; len(got) != 0 {
		t.Fatalf("second pass calls = %q", got)
	}
	if len(r.Updated)+len(r.Moved)+len(r.Held)+len(r.Failed) != 0 {
		t.Fatalf("report = %+v", r)
	}
}

func TestSyncPushNormalizedHashesIgnoreTrailingSpaceAndLongTitleCut(t *testing.T) {
	e := newEnv(t)
	long := strings.Repeat("é", 300)
	b, err := e.svc.Create(e.ctx, units.NewUnit{Level: units.LevelSlice, ParentID: e.unit(units.LevelBrief, "", "Brief").ID, Title: long, Body: "text  \n\n"})
	if err != nil {
		t.Fatal(err)
	}
	e.mustSync()
	e.mustSync()
	if e.count("Update") != 0 {
		t.Fatalf("calls = %q", e.fake.Calls())
	}
	e.edit(b.ID, units.Change{Body: ptr("text \n")})
	e.mustSync()
	if e.count("Update") != 0 {
		t.Fatalf("trailing space pushed: %q", e.fake.Calls())
	}
}

func TestSyncPushAdoptedRowPushesBoth(t *testing.T) {
	e := newEnv(t)
	b := e.unit(units.LevelBrief, "", "Brief")
	pre, err := e.fake.Create(e.ctx, taskmanager.New{Type: "Feature", Title: "Old", Ref: Ref("proj", b.ID)})
	if err != nil {
		t.Fatal(err)
	}
	e.mustSync()
	if e.count("Update") != 0 {
		t.Fatalf("calls = %q", e.fake.Calls())
	}
	r := e.mustSync()
	if len(r.Updated) != 1 || r.Updated[0].Reason != "title, body" {
		t.Fatalf("updated = %+v", r.Updated)
	}
	row := e.row(b.ID)
	if row.PushedTitle != Hash("Brief") || row.PushedBody != Hash("body of Brief") || row.PushedStatus != "To Do" {
		t.Fatalf("row = %+v", row)
	}
	if got := e.ticket(pre.Key); got.Title != "Brief" {
		t.Fatalf("ticket = %+v", got)
	}
}

func TestSyncPushOnlyChangedFieldHash(t *testing.T) {
	e := newEnv(t)
	_, b, _ := e.tree()
	e.mustSync()
	before := e.row(b.ID)
	e.edit(b.ID, units.Change{Body: ptr("other")})
	r := e.mustSync()
	after := e.row(b.ID)
	if after.PushedTitle != before.PushedTitle || after.PushedBody == before.PushedBody || after.PushedBody != Hash("other") {
		t.Fatalf("before %+v after %+v", before, after)
	}
	if len(r.Updated) != 1 || r.Updated[0].Reason != "body" {
		t.Fatalf("updated = %+v", r.Updated)
	}
}

func TestSyncPushCriteriaChangeIsPushed(t *testing.T) {
	e := newEnv(t)
	e.mapTasks()
	_, _, tk := e.tree()
	e.mustSync()
	e.edit(tk.ID, units.Change{AcceptanceCriteria: ptr("- it works")})
	r := e.mustSync()
	if len(r.Updated) != 1 || r.Updated[0].Reason != "body" {
		t.Fatalf("updated = %+v", r.Updated)
	}
	if got := e.ticket("ABC-3"); got.Body != "body of Task\n\n## Acceptance criteria\n\n- it works" {
		t.Fatalf("body = %q", got.Body)
	}
}

func TestSyncMoveToMappedStatus(t *testing.T) {
	e := newEnv(t)
	e.mapTasks()
	_, _, tk := e.tree()
	e.direct()
	e.mustSync()
	e.setState(tk.ID, units.StateTodo, units.StateInProgress, units.StateDone)
	r := e.mustSync()
	if got := e.ticket("ABC-3").Status; got != "Done" {
		t.Fatalf("status = %q", got)
	}
	if len(r.Moved) == 0 || r.Moved[len(r.Moved)-1].Unit != tk.ID {
		t.Fatalf("moved = %+v", r.Moved)
	}
	if row := e.row(tk.ID); row.PushedStatus != "Done" {
		t.Fatalf("row = %+v", row)
	}
	var ev Event
	for _, x := range e.log {
		if x.Kind == "moved" && x.Unit == tk.ID {
			ev = x
		}
	}
	if ev.Message != "To Do -> Done" || ev.Detail != "" || ev.Key != "ABC-3" {
		t.Fatalf("event = %+v", ev)
	}
}

func TestSyncMoveDerivedSliceStatus(t *testing.T) {
	e := newEnv(t)
	e.mapTasks()
	_, s, tk := e.tree()
	e.direct()
	e.mustSync()
	e.setState(tk.ID, units.StateTodo, units.StateInProgress, units.StateDone)
	e.mustSync()
	if got := e.ticket("ABC-2").Status; got != "Done" {
		t.Fatalf("slice status = %q", got)
	}
	if got := e.ticket("ABC-1").Status; got != "Done" {
		t.Fatalf("brief status = %q", got)
	}
	if row := e.row(s.ID); row.PushedStatus != "Done" {
		t.Fatalf("row = %+v", row)
	}
}

func TestSyncMoveAfterCreateWhenNotInitialStatus(t *testing.T) {
	e := newEnv(t)
	e.mapTasks()
	_, _, tk := e.tree()
	e.direct()
	e.fake.SetWorkflow("Backlog", map[string][]string{"Backlog": {"To Do", "In Progress", "Done"}, "To Do": {"In Progress"}})
	r := e.mustSync()
	if len(r.Created) != 3 || len(r.Moved) != 3 {
		t.Fatalf("report = %+v", r)
	}
	if got := e.ticket("ABC-3").Status; got != "To Do" {
		t.Fatalf("status = %q", got)
	}
	if row := e.row(tk.ID); row.PushedStatus != "To Do" {
		t.Fatalf("row = %+v", row)
	}
	if e.count("Move") != 3 {
		t.Fatalf("calls = %q", e.fake.Calls())
	}
}

func TestSyncMoveUnmappedStateLeavesTicket(t *testing.T) {
	e := newEnv(t)
	e.mapTasks()
	_, _, tk := e.tree()
	other := e.unit(units.LevelTask, "S-1", "Other")
	e.mustSync()
	e.setState(tk.ID, units.StateTodo, units.StateInProgress, units.StateBlocked)
	e.setState(other.ID, units.StateCancelled)
	before := len(e.fake.Calls())
	r := e.mustSync()
	if got := e.fake.Calls()[before:]; slices.Contains(got, "Move") {
		t.Fatalf("calls = %q", got)
	}
	if len(r.Moved)+len(r.Failed) != 0 || e.row(tk.ID).PushedStatus != "To Do" || e.row(other.ID).PushedStatus != "To Do" {
		t.Fatalf("report = %+v", r)
	}
}

func TestSyncMoveUsesFlow(t *testing.T) {
	e := newEnv(t)
	e.mapTasks()
	_, _, tk := e.tree()
	e.deps.Settings.Flow = []string{"To Do", "In Progress", "Done"}
	e.mustSync()
	e.setState(tk.ID, units.StateTodo, units.StateInProgress, units.StateDone)
	r := e.mustSync()
	if got := e.ticket("ABC-3").Status; got != "Done" {
		t.Fatalf("status = %q", got)
	}
	var moved Item
	for _, it := range r.Moved {
		if it.Unit == tk.ID {
			moved = it
		}
	}
	if moved.Reason != "In Progress" {
		t.Fatalf("moved = %+v", r.Moved)
	}
	var ev Event
	for _, x := range e.log {
		if x.Kind == "moved" && x.Unit == tk.ID {
			ev = x
		}
	}
	if ev.Message != "To Do -> Done" || ev.Detail != "In Progress" {
		t.Fatalf("event = %+v", ev)
	}
}

func TestSyncMoveFailureKeepsPushedStatusAndRecords(t *testing.T) {
	e := newEnv(t)
	e.mapTasks()
	_, _, tk := e.tree()
	e.mustSync()
	e.setState(tk.ID, units.StateTodo, units.StateInProgress, units.StateDone)
	r := e.mustSync()
	row := e.row(tk.ID)
	if row.PushedStatus != "To Do" || row.FailedStep != "move" || row.Attempts != 1 || !row.FailedAt.Equal(testNow) ||
		!strings.Contains(row.LastError, "reachable from \"To Do\": In Progress") {
		t.Fatalf("row = %+v", row)
	}
	var found bool
	for _, f := range r.Failed {
		if f.Unit == tk.ID && f.Step == "move" && f.Message == row.LastError {
			found = true
		}
	}
	if !found {
		t.Fatalf("failed = %+v", r.Failed)
	}
}

func TestSyncMoveIsRetriedByNextCommandPass(t *testing.T) {
	e := newEnv(t)
	e.mapTasks()
	_, _, tk := e.tree()
	e.mustSync()
	e.setState(tk.ID, units.StateTodo, units.StateInProgress, units.StateDone)
	e.deps.Settings.PollInterval = time.Hour
	e.mustSync()
	e.direct()
	r := e.mustSync()
	row := e.row(tk.ID)
	if row.PushedStatus != "Done" || row.FailedStep != "" || row.LastError != "" || row.Attempts != 0 || !row.FailedAt.IsZero() {
		t.Fatalf("row = %+v", row)
	}
	if len(r.Failed) != 0 || !slices.Contains(keys(r.Moved), tk.ID+"=ABC-3") {
		t.Fatalf("report = %+v", r)
	}
}

func TestSyncHeldWhenDriftSet(t *testing.T) {
	e := newEnv(t)
	b := e.unit(units.LevelBrief, "", "Brief")
	e.mustSync()
	row := e.row(b.ID)
	row.Drift = "title,body"
	if err := e.store.Put(e.ctx, row); err != nil {
		t.Fatal(err)
	}
	e.edit(b.ID, units.Change{Title: ptr("Changed")})
	r := e.mustSync()
	if e.count("Update") != 0 || e.count("Move") != 0 {
		t.Fatalf("calls = %q", e.fake.Calls())
	}
	if len(r.Held) != 1 || r.Held[0] != (Item{Unit: b.ID, Key: "ABC-1", Reason: "title, body"}) {
		t.Fatalf("held = %+v", r.Held)
	}
	if last := e.log[len(e.log)-1]; last.Kind != "held" || last.Message != "title, body" {
		t.Fatalf("event = %+v", last)
	}
}

func TestSyncDeletedOnUpdateSetsDriftDeleted(t *testing.T) {
	e := newEnv(t)
	b := e.unit(units.LevelBrief, "", "Brief")
	e.mustSync()
	e.fake.Delete("ABC-1")
	e.edit(b.ID, units.Change{Title: ptr("Changed")})
	r := e.mustSync()
	row := e.row(b.ID)
	if row.Drift != "deleted" || !row.DriftSeenAt.Equal(testNow) {
		t.Fatalf("row = %+v", row)
	}
	if e.count("Move") != 0 || len(r.Held) != 1 || r.Held[0].Reason != "deleted" || len(r.Failed) != 0 {
		t.Fatalf("calls %q report %+v", e.fake.Calls(), r)
	}
	r = e.mustSync()
	if len(r.Held) != 1 || e.count("Update") != 1 {
		t.Fatalf("second pass: calls %q report %+v", e.fake.Calls(), r)
	}
}

func TestSyncDeletedOnMoveSetsDriftDeleted(t *testing.T) {
	e := newEnv(t)
	e.mapTasks()
	_, _, tk := e.tree()
	e.direct()
	e.mustSync()
	e.fake.Delete("ABC-3")
	e.setState(tk.ID, units.StateTodo, units.StateInProgress)
	r := e.mustSync()
	row := e.row(tk.ID)
	if row.Drift != "deleted" || !row.DriftSeenAt.Equal(testNow) || row.PushedStatus != "To Do" {
		t.Fatalf("row = %+v", row)
	}
	if len(r.Held) != 1 || r.Held[0].Unit != tk.ID || len(r.Failed) != 0 {
		t.Fatalf("report = %+v", r)
	}
}

func (e *env) failMove(key string) {
	e.sys.moveFails[key] = fmt.Errorf("move: %w", taskmanager.ErrUnavailable)
}

func TestSyncRetryAutomaticDefersRecentFailure(t *testing.T) {
	e := newEnv(t)
	e.mapTasks()
	_, _, tk := e.tree()
	e.deps.Settings.PollInterval = 10 * time.Minute
	e.mustSync()
	e.failMove("ABC-3")
	e.setState(tk.ID, units.StateTodo, units.StateInProgress)
	e.mustSync()
	e.edit(tk.ID, units.Change{Title: ptr("Renamed")})
	e.deps.Now = func() time.Time { return testNow.Add(2 * time.Minute) }
	r, err := e.sync(SyncOptions{Automatic: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Deferred) != 1 || r.Deferred[0].Unit != tk.ID {
		t.Fatalf("deferred = %+v", r.Deferred)
	}
	want := "move: failed 2m ago, retry after " + testNow.Add(10*time.Minute).Format(time.RFC3339)
	if r.Deferred[0].Reason != want {
		t.Fatalf("reason = %q, want %q", r.Deferred[0].Reason, want)
	}
	if last := e.log[len(e.log)-1]; last.Kind != "deferred" || last.Message != want {
		t.Fatalf("event = %+v", last)
	}
	if row := e.row(tk.ID); row.Attempts != 1 || !row.FailedAt.Equal(testNow) {
		t.Fatalf("row = %+v", row)
	}
	if len(r.Updated) != 1 || e.ticket("ABC-3").Title != "Renamed" {
		t.Fatalf("fields step did not run: %+v", r)
	}
}

func TestSyncRetryManualAlwaysTries(t *testing.T) {
	e := newEnv(t)
	e.mapTasks()
	_, _, tk := e.tree()
	e.deps.Settings.PollInterval = time.Hour
	e.mustSync()
	e.failMove("ABC-3")
	e.setState(tk.ID, units.StateTodo, units.StateInProgress)
	e.mustSync()
	r := e.mustSync()
	if len(r.Deferred) != 0 || len(r.Failed) != 1 || e.row(tk.ID).Attempts != 2 {
		t.Fatalf("calls %q report %+v row %+v", e.fake.Calls(), r, e.row(tk.ID))
	}
}

func TestSyncRetryAfterIntervalTries(t *testing.T) {
	e := newEnv(t)
	e.mapTasks()
	_, _, tk := e.tree()
	e.deps.Settings.PollInterval = 5 * time.Minute
	e.mustSync()
	e.failMove("ABC-3")
	e.setState(tk.ID, units.StateTodo, units.StateInProgress)
	e.mustSync()
	delete(e.sys.moveFails, "ABC-3")
	e.direct()
	e.deps.Now = func() time.Time { return testNow.Add(5 * time.Minute) }
	r, err := e.sync(SyncOptions{Automatic: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Deferred) != 0 || len(r.Moved) != 1 || e.row(tk.ID).PushedStatus != "In Progress" {
		t.Fatalf("report = %+v row %+v", r, e.row(tk.ID))
	}
}

func TestSyncFailureClearsOnSuccess(t *testing.T) {
	e := newEnv(t)
	b := e.unit(units.LevelBrief, "", "Brief")
	e.mustSync()
	e.sys.updateFails["ABC-1"] = fmt.Errorf("update: %w", taskmanager.ErrUnavailable)
	e.edit(b.ID, units.Change{Title: ptr("Changed")})
	e.mustSync()
	e.mustSync()
	if row := e.row(b.ID); row.FailedStep != "fields" || row.Attempts != 2 || row.LastError == "" {
		t.Fatalf("row = %+v", row)
	}
	delete(e.sys.updateFails, "ABC-1")
	e.mustSync()
	row := e.row(b.ID)
	if row.FailedStep != "" || row.LastError != "" || row.Attempts != 0 || !row.FailedAt.IsZero() {
		t.Fatalf("row = %+v", row)
	}
}

func TestSyncSyncedAtSetOnSuccess(t *testing.T) {
	e := newEnv(t)
	b := e.unit(units.LevelBrief, "", "Brief")
	e.mustSync()
	later := testNow.Add(time.Hour)
	e.deps.Now = func() time.Time { return later }
	e.mustSync()
	if got := e.row(b.ID).SyncedAt; !got.Equal(testNow) {
		t.Fatalf("synced_at moved without a step: %v", got)
	}
	e.edit(b.ID, units.Change{Title: ptr("Changed")})
	e.mustSync()
	if got := e.row(b.ID).SyncedAt; !got.Equal(later) {
		t.Fatalf("synced_at = %v, want %v", got, later)
	}
}

func TestSyncRateLimitedPushStopsPass(t *testing.T) {
	e := newEnv(t)
	b := e.unit(units.LevelBrief, "", "Brief")
	s := e.unit(units.LevelSlice, b.ID, "Slice")
	e.mustSync()
	reset := testNow.Add(10 * time.Minute)
	e.sys.updateFails["ABC-1"] = &taskmanager.RateLimitError{ResetAt: reset}
	e.edit(b.ID, units.Change{Title: ptr("Changed")})
	e.edit(s.ID, units.Change{Title: ptr("Changed too")})
	r, err := e.sync(SyncOptions{})
	if err == nil || !errors.Is(err, taskmanager.ErrRateLimited) {
		t.Fatalf("err = %v", err)
	}
	if e.ticket("ABC-2").Title != "Slice" || len(r.Failed) != 1 || len(r.Updated) != 0 {
		t.Fatalf("report = %+v", r)
	}
	if v, _ := e.store.State(e.ctx, stateBlockedUntil); v != reset.Format(time.RFC3339) {
		t.Fatalf("blocked_until = %q", v)
	}
	if _, err := e.sync(SyncOptions{}); err == nil || !strings.Contains(err.Error(), "paused") {
		t.Fatalf("second pass err = %v", err)
	}
}

func TestSyncMoveSetsMappedStatusAfterMove(t *testing.T) {
	e := newEnv(t)
	e.mapTasks()
	_, _, tk := e.tree()
	e.mustSync()
	e.setState(tk.ID, units.StateTodo, units.StateInProgress)
	e.mustSync()
	row := e.row(tk.ID)
	if row.PushedStatus != "In Progress" || row.MappedStatus != "In Progress" {
		t.Fatalf("row = %+v", row)
	}
	e.sys.moveEnds = map[string]string{"ABC-3": "Review"}
	e.setState(tk.ID, units.StateDone)
	e.mustSync()
	if row := e.row(tk.ID); row.PushedStatus != "Review" || row.MappedStatus != "Done" {
		t.Fatalf("row after a move that ends elsewhere: %+v", row)
	}
}

func TestSyncMoveFailureKeepsMappedStatus(t *testing.T) {
	e := newEnv(t)
	e.mapTasks()
	_, _, tk := e.tree()
	e.mustSync()
	e.setState(tk.ID, units.StateTodo, units.StateInProgress, units.StateDone)
	e.mustSync()
	row := e.row(tk.ID)
	if row.FailedStep != "move" || row.PushedStatus != "To Do" || row.MappedStatus != "" {
		t.Fatalf("row = %+v", row)
	}
}

func TestSyncMoveNewRowUsesPushedStatusWhenMappedEmpty(t *testing.T) {
	e := newEnv(t)
	e.mapTasks()
	_, _, tk := e.tree()
	e.direct()
	e.mustSync()
	if row := e.row(tk.ID); row.MappedStatus != "" || row.PushedStatus != "To Do" {
		t.Fatalf("created row = %+v", row)
	}
	e.setState(tk.ID, units.StateTodo, units.StateInProgress)
	e.mustSync()
	if got := e.ticket("ABC-3").Status; got != "In Progress" {
		t.Fatalf("status = %q", got)
	}
}

func TestSyncMoveOlderRowBehavesAsBefore(t *testing.T) {
	e := newEnv(t)
	e.mapTasks()
	_, _, tk := e.tree()
	e.direct()
	e.mustSync()
	e.setState(tk.ID, units.StateTodo)
	e.mustSync()
	row := e.row(tk.ID)
	row.MappedStatus = ""
	if err := e.store.Put(e.ctx, row); err != nil {
		t.Fatal(err)
	}
	before := e.count("Move")
	e.mustSync()
	if e.count("Move") != before {
		t.Fatalf("an unchanged unit was moved: %q", e.fake.Calls())
	}
	e.setState(tk.ID, units.StateInProgress)
	e.mustSync()
	if got := e.ticket("ABC-3").Status; got != "In Progress" {
		t.Fatalf("status = %q", got)
	}
}

// acceptedStatus syncs a task, has a person move its ticket to Done, and accepts that.
func (e *env) acceptedStatus() (unit, key string) {
	e.t.Helper()
	e.mapTasks()
	_, _, tk := e.tree()
	e.direct()
	e.mustSync()
	e.setState(tk.ID, units.StateTodo)
	e.mustSync()
	key = e.row(tk.ID).Key
	e.fake.ForceStatus(key, "Done")
	if _, err := Check(e.ctx, e.deps, ObserveOptions{}); err != nil {
		e.t.Fatal(err)
	}
	if err := e.resolve(tk.ID, ActionAccept, "status"); err != nil {
		e.t.Fatal(err)
	}
	return tk.ID, key
}

func TestSyncMoveHoldsAfterAcceptedStatus(t *testing.T) {
	e := newEnv(t)
	id, key := e.acceptedStatus()
	before := e.count("Move")
	e.mustSync()
	e.mustSync()
	if e.count("Move") != before {
		t.Fatalf("moves after accept: %d, before %d", e.count("Move"), before)
	}
	if got := e.ticket(key).Status; got != "Done" {
		t.Fatalf("status = %q", got)
	}
	if row := e.row(id); row.PushedStatus != "Done" || row.MappedStatus != "To Do" {
		t.Fatalf("row = %+v", row)
	}
}

func TestSyncMoveAfterAcceptWhenUnitStateChanges(t *testing.T) {
	e := newEnv(t)
	id, key := e.acceptedStatus()
	e.setState(id, units.StateInProgress)
	e.mustSync()
	if got := e.ticket(key).Status; got != "In Progress" {
		t.Fatalf("status = %q", got)
	}
	if row := e.row(id); row.PushedStatus != "In Progress" || row.MappedStatus != "In Progress" {
		t.Fatalf("row = %+v", row)
	}
}

func TestSyncMoveAfterAcceptWhenStatesSettingChanges(t *testing.T) {
	e := newEnv(t)
	id, key := e.acceptedStatus()
	e.deps.Settings.States = map[string]string{"TODO": "In Progress"}
	e.mustSync()
	if got := e.ticket(key).Status; got != "In Progress" {
		t.Fatalf("status = %q", got)
	}
	if row := e.row(id); row.MappedStatus != "In Progress" {
		t.Fatalf("row = %+v", row)
	}
}

func TestSyncMoveAfterOverwriteStatus(t *testing.T) {
	e := newEnv(t)
	e.direct()
	_, s, _ := e.tree()
	e.mustSync()
	key := e.row(s.ID).Key
	e.fake.ForceStatus(key, "Done")
	if _, err := Check(e.ctx, e.deps, ObserveOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := e.resolve(s.ID, ActionOverwrite, "status"); err != nil {
		t.Fatal(err)
	}
	if got := e.ticket(key).Status; got != "To Do" {
		t.Fatalf("status = %q", got)
	}
	if row := e.row(s.ID); row.PushedStatus != "To Do" || row.MappedStatus != "To Do" {
		t.Fatalf("row = %+v", row)
	}
}
