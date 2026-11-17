package tickets

import (
	"strings"
	"testing"
	"time"

	"github.com/bengearig/ralph/internal/units"
)

func TestRowStoreRoundTrip(t *testing.T) {
	e := newEnv(t)
	b := e.unit(units.LevelBrief, "", "Brief")
	if _, ok, err := e.store.Get(e.ctx, b.ID); ok || err != nil {
		t.Fatalf("get missing: %v %v", ok, err)
	}
	want := Row{
		UnitID: b.ID, System: "jira", Key: "ABC-1", ID: "10001", PushedTitle: "t", PushedBody: "b", PushedStatus: "To Do", PushedParent: "ABC-0",
		Scheduled: true, LinkConfirmed: true, Drift: "title", DriftSeenAt: testNow, FailedStep: "schedule", FailedAt: testNow.Add(time.Hour),
		LastError: "boom", Attempts: 3, SyncedAt: testNow.Add(2 * time.Hour),
	}
	if err := e.store.Put(e.ctx, want); err != nil {
		t.Fatal(err)
	}
	got := e.row(b.ID)
	if !got.DriftSeenAt.Equal(want.DriftSeenAt) || !got.FailedAt.Equal(want.FailedAt) || !got.SyncedAt.Equal(want.SyncedAt) {
		t.Fatalf("times differ: %+v", got)
	}
	got.DriftSeenAt, got.FailedAt, got.SyncedAt = want.DriftSeenAt, want.FailedAt, want.SyncedAt
	if got != want {
		t.Fatalf("row = %+v, want %+v", got, want)
	}

	want.Key, want.Scheduled, want.FailedAt = "ABC-2", false, time.Time{}
	if err := e.store.Put(e.ctx, want); err != nil {
		t.Fatal(err)
	}
	if got := e.row(b.ID); got.Key != "ABC-2" || got.Scheduled || !got.FailedAt.IsZero() {
		t.Fatalf("replaced row = %+v", got)
	}
	if err := e.store.Delete(e.ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	e.noRow(b.ID)
	if err := e.store.Delete(e.ctx, b.ID); err != nil {
		t.Fatalf("delete missing: %v", err)
	}
}

func TestRowStoreListOrderedByUnitNumber(t *testing.T) {
	e := newEnv(t)
	var ids []string
	for i := 0; i < 11; i++ {
		ids = append(ids, e.unit(units.LevelBrief, "", "B").ID)
	}
	for i := len(ids) - 1; i >= 0; i-- {
		if err := e.store.Put(e.ctx, Row{UnitID: ids[i], System: "jira", Key: "ABC-" + ids[i]}); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := e.store.List(e.ctx)
	if err != nil || len(rows) != 11 {
		t.Fatalf("rows %d, %v", len(rows), err)
	}
	for i, r := range rows {
		if r.UnitID != ids[i] {
			t.Fatalf("row %d = %s, want %s", i, r.UnitID, ids[i])
		}
	}
}

func TestRowStoreUniqueKey(t *testing.T) {
	e := newEnv(t)
	a, b := e.unit(units.LevelBrief, "", "A"), e.unit(units.LevelBrief, "", "B")
	if err := e.store.Put(e.ctx, Row{UnitID: a.ID, System: "jira", Key: "ABC-1"}); err != nil {
		t.Fatal(err)
	}
	if err := e.store.Put(e.ctx, Row{UnitID: b.ID, System: "jira", Key: "ABC-1"}); err == nil || !strings.Contains(err.Error(), "UNIQUE") {
		t.Fatalf("err = %v", err)
	}
	if err := e.store.Put(e.ctx, Row{UnitID: b.ID, System: "other", Key: "ABC-1"}); err != nil {
		t.Fatalf("another system: %v", err)
	}
	if got := e.row(a.ID); got.Key != "ABC-1" {
		t.Fatalf("original row lost: %+v", got)
	}
}

func TestRowStoreStateRoundTrip(t *testing.T) {
	e := newEnv(t)
	if v, err := e.store.State(e.ctx, "blocked_until"); v != "" || err != nil {
		t.Fatalf("missing: %q %v", v, err)
	}
	for _, v := range []string{"one", "two"} {
		if err := e.store.SetState(e.ctx, "blocked_until", v); err != nil {
			t.Fatal(err)
		}
		if got, err := e.store.State(e.ctx, "blocked_until"); got != v || err != nil {
			t.Fatalf("state = %q, %v, want %q", got, err, v)
		}
	}
	if got, _ := e.store.State(e.ctx, "other"); got != "" {
		t.Fatalf("other = %q", got)
	}
}

func TestRowStoreCascadesOnUnitDelete(t *testing.T) {
	e := newEnv(t)
	b := e.unit(units.LevelBrief, "", "Brief")
	if err := e.store.Put(e.ctx, Row{UnitID: b.ID, System: "jira", Key: "ABC-1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.SQL().Exec("DELETE FROM units WHERE id = ?", b.ID); err != nil {
		t.Fatal(err)
	}
	e.noRow(b.ID)
}
