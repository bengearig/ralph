package tickets

import (
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bengearig/ralph/internal/filelock"
	"github.com/bengearig/ralph/internal/project"
	"github.com/bengearig/ralph/internal/taskmanager"
	"github.com/bengearig/ralph/internal/units"
)

func TestSyncCreatesInOrder(t *testing.T) {
	e := newEnv(t)
	e.mapTasks()
	e.tree()
	e.tree()
	r := e.mustSync()
	wantKeys(t, r.Created, "B-1=ABC-1", "B-2=ABC-2", "S-1=ABC-3", "S-2=ABC-4", "T-1=ABC-5", "T-2=ABC-6")
	var calls []string
	for _, c := range e.fake.Calls() {
		if c == "Create" {
			calls = append(calls, c)
		}
	}
	if len(calls) != 6 || e.count("Find") != 6 {
		t.Fatalf("calls = %q", e.fake.Calls())
	}
	var kinds []string
	for _, ev := range e.log {
		kinds = append(kinds, ev.Kind+" "+ev.Unit+" "+ev.Key)
	}
	if want := []string{"created B-1 ABC-1", "created B-2 ABC-2", "created S-1 ABC-3"}; !slices.Equal(kinds[:3], want) {
		t.Fatalf("events = %q", kinds)
	}
}

func TestSyncCreateRecordsRowAndLink(t *testing.T) {
	e := newEnv(t)
	b := e.unit(units.LevelBrief, "", "Brief")
	s, err := e.svc.Create(e.ctx, units.NewUnit{Level: units.LevelSlice, ParentID: b.ID, Title: "Slice", Body: "text\n\n"})
	if err != nil {
		t.Fatal(err)
	}
	e.mustSync()
	row := e.row(s.ID)
	want := Row{
		UnitID: s.ID, System: "fake", Key: "ABC-2", ID: "10002",
		PushedTitle: Hash("Slice"), PushedBody: Hash("text"), PushedStatus: "To Do", PushedParent: "ABC-1",
		LinkConfirmed: true, SyncedAt: testNow,
	}
	if !row.SyncedAt.Equal(testNow) {
		t.Fatalf("synced_at = %v", row.SyncedAt)
	}
	row.SyncedAt = testNow
	if row != want {
		t.Fatalf("row = %+v, want %+v", row, want)
	}
	if got := e.links(s.ID); !slices.Equal(got, []string{"external jira:ABC-2"}) {
		t.Fatalf("links = %q", got)
	}
	got, err := e.fake.Get(e.ctx, []string{"ABC-2"})
	if err != nil || got["ABC-2"].Parent != "ABC-1" || got["ABC-2"].Ref != "ralph-proj-"+s.ID || got["ABC-2"].Type != "Task" {
		t.Fatalf("ticket = %+v, %v", got, err)
	}
}

func TestSyncCreateUnmappedLevelSkipped(t *testing.T) {
	e := newEnv(t)
	_, _, tk := e.tree()
	r := e.mustSync()
	wantKeys(t, r.Created, "B-1=ABC-1", "S-1=ABC-2")
	e.noRow(tk.ID)
	if len(r.Skipped) != 0 || e.count("Create") != 2 {
		t.Fatalf("skipped %+v, creates %d", r.Skipped, e.count("Create"))
	}
}

func TestSyncCreateCancelledSkipped(t *testing.T) {
	e := newEnv(t)
	e.mapTasks()
	_, s, tk := e.tree()
	keep := e.unit(units.LevelTask, s.ID, "Keep")
	if _, err := e.svc.SetState(e.ctx, tk.ID, tk.Fingerprint, units.StateCancelled, "no"); err != nil {
		t.Fatal(err)
	}
	r := e.mustSync()
	e.noRow(tk.ID)
	e.row(keep.ID)
	if len(r.Skipped) != 1 || r.Skipped[0] != (Item{Unit: tk.ID, Reason: "cancelled"}) {
		t.Fatalf("skipped = %+v", r.Skipped)
	}
}

func TestSyncCreateFindsExistingByRefAndAdopts(t *testing.T) {
	e := newEnv(t)
	b := e.unit(units.LevelBrief, "", "Brief")
	pre, err := e.fake.Create(e.ctx, taskmanager.New{Type: "Feature", Title: "Brief", Ref: Ref("proj", b.ID)})
	if err != nil {
		t.Fatal(err)
	}
	e.useSprint("Sprint 42")
	e.deps.Settings.SprintLevels = []string{"brief"}
	r := e.mustSync()
	if e.count("Create") != 1 || len(r.Created) != 0 {
		t.Fatalf("creates %d, created %+v", e.count("Create"), r.Created)
	}
	if len(r.Adopted) != 1 || r.Adopted[0] != (Item{Unit: b.ID, Key: pre.Key, Reason: "found by its reference"}) {
		t.Fatalf("adopted = %+v", r.Adopted)
	}
	row := e.row(b.ID)
	if row.Key != pre.Key || row.ID != pre.ID || !row.LinkConfirmed || row.PushedTitle != "" || row.PushedStatus != "" {
		t.Fatalf("row = %+v", row)
	}
	if got := e.links(b.ID); !slices.Equal(got, []string{"external jira:" + pre.Key}) {
		t.Fatalf("links = %q", got)
	}
}

func TestSyncCreateIdempotent(t *testing.T) {
	e := newEnv(t)
	e.mapTasks()
	e.tree()
	e.useSprint("Sprint 42")
	e.mustSync()
	before := len(e.fake.Calls())
	r := e.mustSync()
	if got := len(e.fake.Calls()); got != before {
		t.Fatalf("second pass made calls: %q", e.fake.Calls()[before:])
	}
	if len(r.Created)+len(r.Adopted)+len(r.Scheduled)+len(r.Skipped)+len(r.Failed) != 0 {
		t.Fatalf("report = %+v", r)
	}
}

func TestSyncAdoptFromLinkWithoutRow(t *testing.T) {
	e := newEnv(t)
	b := e.unit(units.LevelBrief, "", "Brief")
	if err := e.svc.AddLink(e.ctx, b.ID, units.LinkExternal, "jira:ABC-77"); err != nil {
		t.Fatal(err)
	}
	e.useSprint("Sprint 42")
	e.deps.Settings.SprintLevels = []string{"brief"}
	r := e.mustSync()
	if len(e.fake.Calls()) != 0 {
		t.Fatalf("calls = %q", e.fake.Calls())
	}
	row := e.row(b.ID)
	want := Row{UnitID: b.ID, System: "fake", Key: "ABC-77", LinkConfirmed: true, Scheduled: true}
	row.SyncedAt = time.Time{}
	if row != want {
		t.Fatalf("row = %+v", row)
	}
	if len(r.Adopted) != 1 || r.Adopted[0].Reason != "from its link" || len(r.Created) != 0 {
		t.Fatalf("report = %+v", r)
	}
}

func TestSyncAdoptLinkChangedKeyReplacesRow(t *testing.T) {
	e := newEnv(t)
	b := e.unit(units.LevelBrief, "", "Brief")
	e.mustSync()
	if err := e.svc.RemoveLink(e.ctx, b.ID, units.LinkExternal, "jira:ABC-1"); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.AddLink(e.ctx, b.ID, units.LinkExternal, "jira:ABC-9"); err != nil {
		t.Fatal(err)
	}
	before := len(e.fake.Calls())
	e.mustSync()
	row := e.row(b.ID)
	if row.Key != "ABC-9" || row.ID != "" || row.PushedTitle != "" || row.PushedStatus != "" || !row.LinkConfirmed {
		t.Fatalf("row = %+v", row)
	}
	if len(e.fake.Calls()) != before {
		t.Fatalf("calls = %q", e.fake.Calls()[before:])
	}
}

func TestSyncRepairsMissingLinkWhenRowUnconfirmed(t *testing.T) {
	e := newEnv(t)
	b := e.unit(units.LevelBrief, "", "Brief")
	if err := e.store.Put(e.ctx, Row{UnitID: b.ID, System: "fake", Key: "ABC-5", PushedTitle: Hash("Brief"), PushedBody: Hash("body of Brief"), PushedStatus: "To Do", Scheduled: true}); err != nil {
		t.Fatal(err)
	}
	e.mustSync()
	if got := e.links(b.ID); !slices.Equal(got, []string{"external jira:ABC-5"}) {
		t.Fatalf("links = %q", got)
	}
	if row := e.row(b.ID); !row.LinkConfirmed || row.Key != "ABC-5" {
		t.Fatalf("row = %+v", row)
	}
	if len(e.fake.Calls()) != 0 {
		t.Fatalf("calls = %q", e.fake.Calls())
	}
}

func TestSyncDetachedWhenLinkRemovedAfterConfirm(t *testing.T) {
	e := newEnv(t)
	b := e.unit(units.LevelBrief, "", "Brief")
	e.mustSync()
	if err := e.svc.RemoveLink(e.ctx, b.ID, units.LinkExternal, "jira:ABC-1"); err != nil {
		t.Fatal(err)
	}
	before := len(e.fake.Calls())
	r := e.mustSync()
	if len(e.fake.Calls()) != before || len(e.links(b.ID)) != 0 {
		t.Fatalf("calls %q, links %q", e.fake.Calls()[before:], e.links(b.ID))
	}
	if len(r.Created)+len(r.Adopted)+len(r.Skipped)+len(r.Failed) != 0 {
		t.Fatalf("report = %+v", r)
	}
	if e.row(b.ID).Key != "ABC-1" {
		t.Fatal("row changed")
	}
}

func TestSyncReattachWhenLinkAddedAgain(t *testing.T) {
	e := newEnv(t)
	b := e.unit(units.LevelBrief, "", "Brief")
	e.mustSync()
	if err := e.svc.RemoveLink(e.ctx, b.ID, units.LinkExternal, "jira:ABC-1"); err != nil {
		t.Fatal(err)
	}
	e.mustSync()
	if err := e.svc.AddLink(e.ctx, b.ID, units.LinkExternal, "jira:ABC-1"); err != nil {
		t.Fatal(err)
	}
	before := len(e.fake.Calls())
	e.mustSync()
	if len(e.fake.Calls()) != before || e.row(b.ID).Key != "ABC-1" {
		t.Fatal("reattached unit made calls or lost its row")
	}
	if err := e.svc.RemoveLink(e.ctx, b.ID, units.LinkExternal, "jira:ABC-1"); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.AddLink(e.ctx, b.ID, units.LinkExternal, "jira:ABC-8"); err != nil {
		t.Fatal(err)
	}
	e.mustSync()
	if got := e.row(b.ID); got.Key != "ABC-8" || !got.LinkConfirmed {
		t.Fatalf("row = %+v", got)
	}
}

func TestSyncScheduleAfterCreate(t *testing.T) {
	e := newEnv(t)
	e.mapTasks()
	_, s, tk := e.tree()
	e.useSprint("Sprint 42")
	r := e.mustSync()
	wantKeys(t, r.Scheduled, s.ID+"=ABC-2", tk.ID+"=ABC-3")
	if r.Scheduled[0].Reason != "Sprint 42" {
		t.Fatalf("scheduled = %+v", r.Scheduled)
	}
	for _, id := range []string{s.ID, tk.ID} {
		if row := e.row(id); !row.Scheduled || row.FailedStep != "" {
			t.Errorf("%s row = %+v", id, row)
		}
	}
	if got := e.fake.Scheduled("ABC-2"); !slices.Equal(got, []string{"Sprint 42"}) {
		t.Fatalf("scheduled = %q", got)
	}
}

func TestSyncScheduleOnlyConfiguredLevels(t *testing.T) {
	e := newEnv(t)
	e.mapTasks()
	b, s, tk := e.tree()
	e.useSprint("Sprint 42")
	e.deps.Settings.SprintLevels = []string{"task"}
	e.mustSync()
	if e.row(b.ID).Scheduled || e.row(s.ID).Scheduled || !e.row(tk.ID).Scheduled {
		t.Fatal("wrong rows scheduled")
	}
	if e.count("Schedule") != 1 {
		t.Fatalf("calls = %q", e.fake.Calls())
	}

	e2 := newEnv(t)
	e2.tree()
	e2.mustSync()
	if e2.count("Schedule") != 0 {
		t.Fatal("scheduled without a sprint")
	}
}

func TestSyncScheduleFailureKeepsTicketAndRetries(t *testing.T) {
	e := newEnv(t)
	b := e.unit(units.LevelBrief, "", "Brief")
	s := e.unit(units.LevelSlice, b.ID, "Slice")
	e.deps.Settings.Sprint = "Sprint 42"
	r := e.mustSync()
	if len(r.Failed) != 1 || r.Failed[0].Unit != s.ID || r.Failed[0].Step != "schedule" || len(r.Created) != 2 {
		t.Fatalf("report = %+v", r)
	}
	row := e.row(s.ID)
	if row.Scheduled || row.FailedStep != "schedule" || row.LastError == "" || row.Attempts != 1 || !row.FailedAt.Equal(testNow) {
		t.Fatalf("row = %+v", row)
	}
	creates := e.count("Create")

	e.fake.SetPlans("Sprint 42")
	r = e.mustSync()
	row = e.row(s.ID)
	if e.count("Create") != creates || !row.Scheduled || row.FailedStep != "" || row.LastError != "" || row.Attempts != 0 || !row.FailedAt.IsZero() {
		t.Fatalf("row = %+v, creates %d", row, e.count("Create"))
	}
	wantKeys(t, r.Scheduled, s.ID+"=ABC-2")
}

func TestSyncScheduleNotForAdoptedFromLink(t *testing.T) {
	e := newEnv(t)
	b := e.unit(units.LevelBrief, "", "Brief")
	pre, err := e.fake.Create(e.ctx, taskmanager.New{Type: "Feature", Title: "Written in Jira"})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.AddLink(e.ctx, b.ID, units.LinkExternal, "jira:"+pre.Key); err != nil {
		t.Fatal(err)
	}
	e.useSprint("Sprint 42")
	e.deps.Settings.SprintLevels = []string{"brief"}
	for i := 0; i < 3; i++ {
		e.mustSync()
	}
	if e.count("Schedule") != 0 || e.count("Update") == 0 || !e.row(b.ID).Scheduled || e.row(b.ID).PushedTitle == "" {
		t.Fatalf("calls = %q, row = %+v", e.fake.Calls(), e.row(b.ID))
	}
}

func TestSyncScheduleNotForAdoptedByReference(t *testing.T) {
	e := newEnv(t)
	b := e.unit(units.LevelBrief, "", "Brief")
	if _, err := e.fake.Create(e.ctx, taskmanager.New{Type: "Feature", Title: "Brief", Ref: Ref("proj", b.ID)}); err != nil {
		t.Fatal(err)
	}
	e.useSprint("Sprint 42")
	e.deps.Settings.SprintLevels = []string{"brief"}
	for i := 0; i < 3; i++ {
		e.mustSync()
	}
	if e.count("Schedule") != 0 || !e.row(b.ID).Scheduled || e.row(b.ID).PushedTitle == "" {
		t.Fatalf("calls = %q, row = %+v", e.fake.Calls(), e.row(b.ID))
	}
}

func TestSyncScheduleAdoptedRowIsMarkedScheduled(t *testing.T) {
	e := newEnv(t)
	b := e.unit(units.LevelBrief, "", "Brief")
	b2 := e.unit(units.LevelBrief, "", "Other")
	if err := e.svc.AddLink(e.ctx, b.ID, units.LinkExternal, "jira:ABC-70"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.fake.Create(e.ctx, taskmanager.New{Type: "Feature", Title: "Other", Ref: Ref("proj", b2.ID)}); err != nil {
		t.Fatal(err)
	}
	e.mustSync()
	if !e.row(b.ID).Scheduled || !e.row(b2.ID).Scheduled {
		t.Fatalf("rows = %+v, %+v", e.row(b.ID), e.row(b2.ID))
	}
}

func TestSyncScheduleStillForCreatedTickets(t *testing.T) {
	e := newEnv(t)
	b := e.unit(units.LevelBrief, "", "Brief")
	e.useSprint("Sprint 42")
	e.deps.Settings.SprintLevels = []string{"brief"}
	r := e.mustSync()
	if len(r.Created) != 1 || len(r.Scheduled) != 1 || !e.row(b.ID).Scheduled || e.count("Schedule") != 1 {
		t.Fatalf("report = %+v, calls = %q", r, e.fake.Calls())
	}
}

func TestSyncScheduleFailureStillRetried(t *testing.T) {
	e := newEnv(t)
	b := e.unit(units.LevelBrief, "", "Brief")
	e.deps.Settings.Sprint = "Sprint 42"
	e.deps.Settings.SprintLevels = []string{"brief"}
	e.mustSync()
	if row := e.row(b.ID); row.Scheduled || row.FailedStep != "schedule" {
		t.Fatalf("row = %+v", row)
	}
	e.fake.SetPlans("Sprint 42")
	e.mustSync()
	if row := e.row(b.ID); !row.Scheduled || row.FailedStep != "" {
		t.Fatalf("row = %+v", row)
	}
	if e.count("Schedule") != 2 {
		t.Fatalf("calls = %q", e.fake.Calls())
	}
}

func TestSyncScheduleOnlyConfiguredLevelsStillHolds(t *testing.T) {
	e := newEnv(t)
	e.mapTasks()
	b, s, tk := e.tree()
	e.useSprint("Sprint 42")
	e.deps.Settings.SprintLevels = []string{"slice"}
	e.mustSync()
	e.mustSync()
	if e.row(b.ID).Scheduled || !e.row(s.ID).Scheduled || e.row(tk.ID).Scheduled || e.count("Schedule") != 1 {
		t.Fatalf("calls = %q", e.fake.Calls())
	}
}

func TestSyncScheduleNoSprintSettingNeverSchedules(t *testing.T) {
	e := newEnv(t)
	e.tree()
	e.mustSync()
	e.mustSync()
	if e.count("Schedule") != 0 {
		t.Fatalf("calls = %q", e.fake.Calls())
	}
}

func TestSyncFailureOfOneUnitDoesNotStopOthers(t *testing.T) {
	e := newEnv(t)
	first := e.unit(units.LevelBrief, "", "First")
	second := e.unit(units.LevelBrief, "", "Second")
	e.sys.createFails["First"] = errors.New("boom")
	r, err := e.sync(SyncOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Failed) != 1 || r.Failed[0] != (Failure{Unit: first.ID, Step: "create", Message: "boom"}) {
		t.Fatalf("failed = %+v", r.Failed)
	}
	wantKeys(t, r.Created, second.ID+"=ABC-1")
	e.noRow(first.ID)
	if ev := e.log[0]; ev != (Event{Kind: "failed", Unit: first.ID, Message: "create: boom"}) {
		t.Fatalf("event = %+v", ev)
	}
	delete(e.sys.createFails, "First")
	r = e.mustSync()
	wantKeys(t, r.Created, first.ID+"=ABC-2")
}

func TestSyncUnauthorizedStopsPass(t *testing.T) {
	e := newEnv(t)
	e.unit(units.LevelBrief, "", "A")
	e.unit(units.LevelBrief, "", "B")
	e.fake.RejectCredential()
	r, err := e.sync(SyncOptions{})
	if !errors.Is(err, taskmanager.ErrUnauthorized) {
		t.Fatalf("err = %v", err)
	}
	if len(r.Failed) != 1 || len(e.fake.Calls()) != 1 {
		t.Fatalf("failed %+v, calls %q", r.Failed, e.fake.Calls())
	}
}

func TestSyncRateLimitStopsAndStoresBlockedUntil(t *testing.T) {
	e := newEnv(t)
	e.unit(units.LevelBrief, "", "A")
	e.unit(units.LevelBrief, "", "B")
	until := testNow.Add(10 * time.Minute)
	e.fake.RateLimit(until)
	r, err := e.sync(SyncOptions{})
	if !errors.Is(err, taskmanager.ErrRateLimited) {
		t.Fatalf("err = %v", err)
	}
	if len(r.Failed) != 1 || len(e.fake.Calls()) != 1 {
		t.Fatalf("failed %+v, calls %q", r.Failed, e.fake.Calls())
	}
	if got, _ := e.store.State(e.ctx, "blocked_until"); got != "2026-03-04T05:16:07Z" {
		t.Fatalf("blocked_until = %q", got)
	}

	e.fake.Heal()
	e.fake.RateLimit(time.Time{})
	if err := e.store.SetState(e.ctx, "blocked_until", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := e.sync(SyncOptions{}); !errors.Is(err, taskmanager.ErrRateLimited) {
		t.Fatalf("err = %v", err)
	}
	if got, _ := e.store.State(e.ctx, "blocked_until"); got != "2026-03-04T05:07:07Z" {
		t.Fatalf("unknown reset: blocked_until = %q", got)
	}
}

func TestSyncPausedUntilBlockedUntilPassesUnlessForced(t *testing.T) {
	e := newEnv(t)
	b := e.unit(units.LevelBrief, "", "A")
	if err := e.store.SetState(e.ctx, "blocked_until", "2026-03-04T05:11:07Z"); err != nil {
		t.Fatal(err)
	}
	_, err := e.sync(SyncOptions{})
	if err == nil || err.Error() != "ticket sync is paused until 2026-03-04T05:11:07Z (rate limited)" {
		t.Fatalf("err = %v", err)
	}
	if len(e.fake.Calls()) != 0 {
		t.Fatal("paused pass made calls")
	}
	if _, err := e.sync(SyncOptions{Force: true}); err != nil {
		t.Fatal(err)
	}
	e.row(b.ID)
	if got, _ := e.store.State(e.ctx, "blocked_until"); got != "" {
		t.Fatalf("blocked_until = %q", got)
	}

	e2 := newEnv(t)
	b2 := e2.unit(units.LevelBrief, "", "A")
	if err := e2.store.SetState(e2.ctx, "blocked_until", "2026-03-04T05:00:00Z"); err != nil {
		t.Fatal(err)
	}
	e2.mustSync()
	e2.row(b2.ID)
}

func TestSyncLockBusy(t *testing.T) {
	e := newEnv(t)
	e.unit(units.LevelBrief, "", "A")
	held, err := filelock.Acquire(filepath.Join(e.deps.Dir, project.TicketsLockFile))
	if err != nil {
		t.Fatal(err)
	}
	defer held.Release()
	_, err = e.sync(SyncOptions{})
	if !errors.Is(err, ErrSyncBusy) || err.Error() != "another ticket sync is running" {
		t.Fatalf("err = %v", err)
	}
	if len(e.fake.Calls()) != 0 {
		t.Fatal("busy pass made calls")
	}
}

func TestSyncLockReleased(t *testing.T) {
	e := newEnv(t)
	e.unit(units.LevelBrief, "", "A")
	check := func() {
		t.Helper()
		l, err := filelock.Acquire(filepath.Join(e.deps.Dir, project.TicketsLockFile))
		if err != nil {
			t.Fatalf("lock still held: %v", err)
		}
		l.Release()
	}
	e.mustSync()
	check()
	e.unit(units.LevelBrief, "", "B")
	e.fake.RejectCredential()
	if _, err := e.sync(SyncOptions{Force: true}); err == nil {
		t.Fatal("want error")
	}
	check()
}

func TestSyncOnlyGivenUnits(t *testing.T) {
	e := newEnv(t)
	b1 := e.unit(units.LevelBrief, "", "One")
	b2 := e.unit(units.LevelBrief, "", "Two")
	s := e.unit(units.LevelSlice, b1.ID, "Slice")
	r, err := e.sync(SyncOptions{Units: []string{b2.ID}})
	if err != nil {
		t.Fatal(err)
	}
	wantKeys(t, r.Created, b2.ID+"=ABC-1")
	e.noRow(b1.ID)

	r, err = e.sync(SyncOptions{Units: []string{s.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Created) != 0 || len(r.Skipped) != 1 || r.Skipped[0].Reason != "parent "+b1.ID+" has no ticket yet" {
		t.Fatalf("report = %+v", r)
	}
	if _, err := e.sync(SyncOptions{Units: []string{"B-99"}}); err == nil || !strings.Contains(err.Error(), "B-99") {
		t.Fatalf("unknown unit: err = %v", err)
	}
}
