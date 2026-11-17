package tickets

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bengearig/ralph/internal/taskmanager"
	"github.com/bengearig/ralph/internal/units"
)

func TestCheckEndToEndWithFake(t *testing.T) {
	e := newEnv(t)
	b := e.unit(units.LevelBrief, "", "Brief")
	s1 := e.unit(units.LevelSlice, b.ID, "One")
	s2 := e.unit(units.LevelSlice, b.ID, "Two")
	e.mustSync()
	e.fake.Edit("ABC-2", taskmanager.Fields{Title: "Edited", Body: "body of One"})
	e.fake.ForceStatus("ABC-2", "Done")
	e.fake.Delete("ABC-3")

	res := e.check()
	want := []Drift{
		{Unit: s1.ID, Key: "ABC-2", Fields: []string{"title", "status"}},
		{Unit: s2.ID, Key: "ABC-3", Fields: []string{"deleted"}},
	}
	if len(res.Drifts) != 2 || res.Drifts[0].Unit != want[0].Unit || strings.Join(res.Drifts[0].Fields, ",") != "title,status" ||
		res.Drifts[1].Unit != want[1].Unit || strings.Join(res.Drifts[1].Fields, ",") != "deleted" {
		t.Fatalf("drifts = %+v", res.Drifts)
	}
	if e.row(b.ID).Drift != "" || e.row(s1.ID).Drift != "title,status" || e.row(s2.ID).Drift != "deleted" {
		t.Fatalf("rows: %+v %+v %+v", e.row(b.ID), e.row(s1.ID), e.row(s2.ID))
	}

	e.edit(s1.ID, units.Change{Title: ptr("Ralph's new title")})
	e.edit(s2.ID, units.Change{Title: ptr("Ralph's other title")})
	updates, creates := e.count("Update"), e.count("Create")
	rep := e.mustSync()
	if len(rep.Held) != 2 || len(rep.Updated) != 0 || len(rep.Created) != 0 {
		t.Fatalf("report = %+v", rep)
	}
	if e.count("Update") != updates || e.count("Create") != creates {
		t.Fatal("a held unit was pushed")
	}
}

func (e *env) autoDeps() {
	e.deps.Settings.AutoSync = true
	e.deps.Settings.PollInterval = 5 * time.Minute
}

func TestAutoPollWhenLastPollOld(t *testing.T) {
	e := newEnv(t)
	e.autoDeps()
	b := e.unit(units.LevelBrief, "", "Brief")
	e.mustSync()
	if err := e.store.SetState(e.ctx, stateLastPoll, testNow.Add(-10*time.Minute).Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	e.fake.Edit("ABC-1", taskmanager.Fields{Title: "Edited", Body: "body of Brief"})
	e.edit(b.ID, units.Change{Title: ptr("Ralph's change")})
	if err := Auto(e.ctx, e.deps); err != nil {
		t.Fatal(err)
	}
	if got := e.row(b.ID).Drift; got != "title" {
		t.Fatalf("drift = %q", got)
	}
	if e.ticket("ABC-1").Title != "Edited" {
		t.Fatal("the held unit was pushed over Jira's edit")
	}
	if last, _ := e.store.State(e.ctx, stateLastPoll); last != testNow.Format(time.RFC3339) {
		t.Fatalf("last_poll = %q", last)
	}
}

func TestAutoPollWhenNeverPolled(t *testing.T) {
	e := newEnv(t)
	e.autoDeps()
	e.unit(units.LevelBrief, "", "Brief")
	e.mustSync()
	n := e.count("Get")
	if err := Auto(e.ctx, e.deps); err != nil {
		t.Fatal(err)
	}
	if e.count("Get") != n+1 {
		t.Fatalf("Get calls = %d, want %d", e.count("Get"), n+1)
	}
}

func TestAutoNoPollWithinInterval(t *testing.T) {
	e := newEnv(t)
	e.autoDeps()
	e.unit(units.LevelBrief, "", "Brief")
	e.mustSync()
	if err := e.store.SetState(e.ctx, stateLastPoll, testNow.Add(-time.Minute).Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	n := e.count("Get")
	if err := Auto(e.ctx, e.deps); err != nil {
		t.Fatal(err)
	}
	if e.count("Get") != n {
		t.Fatal("polled within the interval")
	}
}

func TestAutoPollFailureStillPushes(t *testing.T) {
	e := newEnv(t)
	e.autoDeps()
	buf := captureLog(t)
	b := e.unit(units.LevelBrief, "", "Brief")
	e.mustSync()
	e.deps.System = &getFails{flaky: e.sys}
	e.edit(b.ID, units.Change{Title: ptr("Changed")})
	if err := Auto(e.ctx, e.deps); err != nil {
		t.Fatal(err)
	}
	if e.ticket("ABC-1").Title != "Changed" {
		t.Fatal("the pass did not push after a failed poll")
	}
	if out := buf.String(); !strings.Contains(out, "level=WARN") || !strings.Contains(out, "poll") {
		t.Fatalf("log = %q", out)
	}
	if last, _ := e.store.State(e.ctx, stateLastPoll); last != "" {
		t.Fatalf("a failed poll set last_poll = %q", last)
	}
}

type getFails struct{ *flaky }

func (g *getFails) Get(_ context.Context, _ []string) (map[string]taskmanager.Ticket, error) {
	return nil, errors.New("jira unreachable: " + taskmanager.ErrUnavailable.Error())
}
