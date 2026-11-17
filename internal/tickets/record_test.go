package tickets

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/bengearig/ralph/internal/taskmanager"
	"github.com/bengearig/ralph/internal/units"
)

func (e *env) check(ids ...string) CheckResult {
	e.t.Helper()
	res, err := Check(e.ctx, e.deps, ObserveOptions{Units: ids})
	if err != nil {
		e.t.Fatal(err)
	}
	return res
}

func TestRecordDriftWritesFieldsAndSeenAt(t *testing.T) {
	e := newEnv(t)
	b, s := e.synced()
	e.fake.Edit("ABC-1", taskmanager.Fields{Title: "Edited", Body: "Body"})
	e.fake.ForceStatus("ABC-1", "Done")
	e.fake.Delete("ABC-2")
	res := e.check()
	if res.Result != (RecordResult{Found: 2}) {
		t.Fatalf("result = %+v", res.Result)
	}
	if r := e.row(b.ID); r.Drift != "title,body,status" || !r.DriftSeenAt.Equal(testNow) {
		t.Fatalf("row = %+v", r)
	}
	if r := e.row(s.ID); r.Drift != "deleted" || !r.DriftSeenAt.Equal(testNow) {
		t.Fatalf("row = %+v", r)
	}
}

func TestRecordDriftClearsWhenReverted(t *testing.T) {
	e := newEnv(t)
	b, _ := e.synced()
	orig := e.ticket("ABC-1")
	e.fake.Edit("ABC-1", taskmanager.Fields{Title: "Edited", Body: orig.Body})
	e.check()
	if e.row(b.ID).Drift != "title" {
		t.Fatalf("row = %+v", e.row(b.ID))
	}
	e.fake.Edit("ABC-1", taskmanager.Fields{Title: orig.Title, Body: orig.Body})
	res := e.check()
	if res.Result != (RecordResult{Cleared: 1}) || len(res.Drifts) != 0 || e.row(b.ID).Drift != "" {
		t.Fatalf("result %+v, row %+v", res.Result, e.row(b.ID))
	}
}

func TestRecordDriftKeepsDeletedWhileAbsent(t *testing.T) {
	e := newEnv(t)
	_, s := e.synced()
	e.fake.Delete("ABC-2")
	e.check()
	seen := e.observe(s.ID)
	res, err := Record(e.ctx, e.deps, seen.Observations, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Cleared != 0 || e.row(s.ID).Drift != "deleted" {
		t.Fatalf("result %+v, row %+v", res, e.row(s.ID))
	}
}

// moving reports the ticket asked for under a new key.
type moving struct {
	*flaky
	from, to string
}

func (m *moving) Get(ctx context.Context, keys []string) (map[string]taskmanager.Ticket, error) {
	got, err := m.flaky.Get(ctx, keys)
	if t, ok := got[m.from]; ok {
		t.Key = m.to
		got[m.from] = t
	}
	return got, err
}

func TestRecordDriftFollowsMovedTicket(t *testing.T) {
	e := newEnv(t)
	b, _ := e.synced()
	e.deps.System = &moving{flaky: e.sys, from: "ABC-1", to: "ABC-40"}
	res := e.check()
	if res.Result != (RecordResult{Followed: 1}) {
		t.Fatalf("result = %+v", res.Result)
	}
	if r := e.row(b.ID); r.Key != "ABC-40" || r.Drift != "" || r.PushedTitle == "" {
		t.Fatalf("row = %+v", r)
	}
	if got := e.links(b.ID); !reflect.DeepEqual(got, []string{"external jira:ABC-40"}) {
		t.Fatalf("links = %v", got)
	}
}

func TestRecordDriftSetsLastPoll(t *testing.T) {
	e := newEnv(t)
	e.synced()
	e.check()
	got, err := e.store.State(e.ctx, stateLastPoll)
	if err != nil || got != testNow.Format(time.RFC3339) {
		t.Fatalf("last_poll = %q, err %v", got, err)
	}
}

// brokenLinks fails AddLink for one unit.
type brokenLinks struct {
	units.Service
	unit string
}

func (b brokenLinks) AddLink(ctx context.Context, id string, k units.LinkKind, target string) error {
	if id == b.unit {
		return errors.New("link refused")
	}
	return b.Service.AddLink(ctx, id, k, target)
}

func TestRecordDriftIsOneTransaction(t *testing.T) {
	e := newEnv(t)
	b, s := e.synced()
	e.deps.System = &moving{flaky: e.sys, from: "ABC-1", to: "ABC-40"}
	e.fake.Delete("ABC-2")
	e.deps.Units = brokenLinks{Service: e.svc, unit: s.ID}
	obs, err := Observe(e.ctx, e.deps, ObserveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	drifts := Plan(obs.Observations, e.deps.System.Normalize)
	drifts = append(drifts, Drift{Unit: s.ID, Key: "ABC-2", MovedTo: "ABC-41"})
	before := e.snapshot()
	if _, err := Record(e.ctx, e.deps, obs.Observations, drifts); err == nil || !strings.Contains(err.Error(), "link refused") {
		t.Fatalf("err = %v", err)
	}
	if after := e.snapshot(); after != before {
		t.Fatalf("a failed Record changed state:\nbefore %q\nafter  %q", before, after)
	}
	if e.row(b.ID).Key != "ABC-1" {
		t.Fatalf("row = %+v", e.row(b.ID))
	}
}

func TestRecordDriftRollsBackRowsOnWriteFailure(t *testing.T) {
	e := newEnv(t)
	b, _ := e.synced()
	e.deps.System = &moving{flaky: e.sys, from: "ABC-1", to: "ABC-2"}
	before := e.snapshot()
	if _, err := Check(e.ctx, e.deps, ObserveOptions{}); err == nil {
		t.Fatal("following onto a key another unit holds must fail")
	}
	if after := e.snapshot(); after != before {
		t.Fatalf("state changed:\nbefore %q\nafter  %q", before, after)
	}
	if got := e.links(b.ID); !reflect.DeepEqual(got, []string{"external jira:ABC-1"}) {
		t.Fatalf("links of %s = %v", b.ID, got)
	}
}
