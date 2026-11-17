package tickets

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/bengearig/ralph/internal/taskmanager"
	"github.com/bengearig/ralph/internal/units"
)

// snapshot is everything Observe must leave alone.
func (e *env) snapshot() string {
	e.t.Helper()
	rows, err := e.store.List(e.ctx)
	if err != nil {
		e.t.Fatal(err)
	}
	var b strings.Builder
	for _, r := range rows {
		b.WriteString(r.UnitID + "|" + r.Key + "|" + r.Drift + "|" + formatTime(r.DriftSeenAt) + "|" + r.PushedTitle + "|" + r.PushedBody + "|" + r.PushedStatus + "\n")
		u, err := e.svc.Get(e.ctx, r.UnitID)
		if err != nil {
			e.t.Fatal(err)
		}
		b.WriteString(u.Fingerprint + " " + strings.Join(e.links(r.UnitID), ",") + "\n")
	}
	state, err := e.store.State(e.ctx, stateLastPoll)
	if err != nil {
		e.t.Fatal(err)
	}
	return b.String() + state
}

// synced makes a brief with a slice, syncs them and returns both.
func (e *env) synced() (b, s units.Unit) {
	e.t.Helper()
	b = e.unit(units.LevelBrief, "", "Brief")
	s = e.unit(units.LevelSlice, b.ID, "Slice")
	e.mustSync()
	return b, s
}

func (e *env) observe(ids ...string) ObserveResult {
	e.t.Helper()
	res, err := Observe(e.ctx, e.deps, ObserveOptions{Units: ids})
	if err != nil {
		e.t.Fatal(err)
	}
	return res
}

func obsUnits(res ObserveResult) []string {
	var out []string
	for _, o := range res.Observations {
		out = append(out, o.Unit)
	}
	return out
}

func TestObserveWritesNothing(t *testing.T) {
	e := newEnv(t)
	e.synced()
	e.fake.Edit("ABC-1", taskmanager.Fields{Title: "Changed", Body: "x"})
	e.fake.Delete("ABC-2")
	before := e.snapshot()
	res := e.observe()
	if len(res.Observations) != 2 {
		t.Fatalf("observations = %+v", res.Observations)
	}
	if after := e.snapshot(); after != before {
		t.Fatalf("Observe wrote:\nbefore %q\nafter  %q", before, after)
	}
}

func TestObserveAllRowsExceptDetached(t *testing.T) {
	e := newEnv(t)
	b, s := e.synced()
	if err := e.svc.RemoveLink(e.ctx, s.ID, units.LinkExternal, "jira:ABC-2"); err != nil {
		t.Fatal(err)
	}
	if got := obsUnits(e.observe()); !reflect.DeepEqual(got, []string{b.ID}) {
		t.Fatalf("observed %v, want only %s", got, b.ID)
	}
}

func TestObserveGivenUnits(t *testing.T) {
	e := newEnv(t)
	_, s := e.synced()
	res := e.observe(s.ID)
	if got := obsUnits(res); !reflect.DeepEqual(got, []string{s.ID}) {
		t.Fatalf("observed %v", got)
	}
	if o := res.Observations[0]; o.Row.Key != "ABC-2" || o.Ticket == nil || o.Ticket.Key != "ABC-2" {
		t.Fatalf("observation = %+v", o)
	}
}

func TestObserveAbsentTicketIsNil(t *testing.T) {
	e := newEnv(t)
	e.synced()
	e.fake.Delete("ABC-2")
	res := e.observe()
	if res.Observations[0].Ticket == nil || res.Observations[1].Ticket != nil {
		t.Fatalf("observations = %+v", res.Observations)
	}
}

func TestObserveUnknownUnitErrors(t *testing.T) {
	e := newEnv(t)
	e.synced()
	_, err := Observe(e.ctx, e.deps, ObserveOptions{Units: []string{"S-99"}})
	if err == nil || !strings.Contains(err.Error(), "S-99") {
		t.Fatalf("err = %v", err)
	}
}

func TestObserveNoTicketRowErrors(t *testing.T) {
	e := newEnv(t)
	e.synced()
	t2 := e.unit(units.LevelBrief, "", "No ticket yet")
	_, err := Observe(e.ctx, e.deps, ObserveOptions{Units: []string{t2.ID}})
	if err == nil || !strings.Contains(err.Error(), t2.ID) {
		t.Fatalf("err = %v", err)
	}
}

func TestObserveGetFailureIsError(t *testing.T) {
	e := newEnv(t)
	e.synced()
	e.fake.Down()
	before := e.snapshot()
	res, err := Observe(e.ctx, e.deps, ObserveOptions{})
	if !errors.Is(err, taskmanager.ErrUnavailable) || len(res.Observations) != 0 {
		t.Fatalf("res %+v, err %v", res, err)
	}
	if e.snapshot() != before {
		t.Fatal("a failed Observe wrote")
	}
}

func TestObserveOneGetCall(t *testing.T) {
	e := newEnv(t)
	e.synced()
	e.unit(units.LevelBrief, "", "Third")
	e.mustSync()
	n := e.count("Get")
	e.observe()
	if got := e.count("Get") - n; got != 1 {
		t.Fatalf("%d Get calls, want 1", got)
	}
}

func TestObserveNoRowsNoGet(t *testing.T) {
	e := newEnv(t)
	e.observe()
	if n := e.count("Get"); n != 0 {
		t.Fatalf("%d Get calls", n)
	}
}
