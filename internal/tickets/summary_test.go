package tickets

import (
	"bytes"
	"os"
	"slices"
	"testing"

	"github.com/bengearig/ralph/internal/units"
)

func (e *env) putRow(id, key string, edit func(*Row)) {
	e.t.Helper()
	r := Row{UnitID: id, System: "jira", Key: key, ID: key}
	if edit != nil {
		edit(&r)
	}
	if err := e.store.Put(e.ctx, r); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) summarize(provider string) Summary {
	e.t.Helper()
	s, err := Summarize(e.ctx, SummaryDeps{DB: e.db, Provider: provider})
	if err != nil {
		e.t.Fatal(err)
	}
	return s
}

func TestSummarizeEmptyWhenProviderNone(t *testing.T) {
	e := newEnv(t)
	b := e.unit(units.LevelBrief, "", "Brief")
	e.putRow(b.ID, "ABC-1", func(r *Row) { r.Drift = "title"; r.LastError = "x" })
	for _, p := range []string{"none", ""} {
		if s := e.summarize(p); s.Drifted != 0 || s.Failed != 0 || len(s.Units) != 0 {
			t.Errorf("%q: %+v", p, s)
		}
	}
}

func TestSummarizeCountsDriftAndFailures(t *testing.T) {
	e := newEnv(t)
	b := e.unit(units.LevelBrief, "", "Brief")
	s1 := e.unit(units.LevelSlice, b.ID, "One")
	s2 := e.unit(units.LevelSlice, b.ID, "Two")
	s3 := e.unit(units.LevelSlice, b.ID, "Three")
	e.putRow(b.ID, "ABC-1", nil)
	e.putRow(s1.ID, "ABC-2", func(r *Row) { r.Drift = "title,body" })
	e.putRow(s2.ID, "ABC-3", func(r *Row) { r.FailedStep, r.LastError = "move", "no transition" })
	e.putRow(s3.ID, "ABC-4", func(r *Row) { r.Drift = "status"; r.FailedStep, r.LastError = "update", "boom" })
	s := e.summarize("jira")
	if s.Drifted != 2 || s.Failed != 2 || len(s.Units) != 4 {
		t.Errorf("%+v", s)
	}
}

func TestSummarizeIgnoresDetachedFailures(t *testing.T) {
	e := newEnv(t)
	b := e.unit(units.LevelBrief, "", "Brief")
	e.putRow(b.ID, "ABC-1", func(r *Row) { r.LinkConfirmed = true; r.FailedStep, r.LastError = "update", "boom" })
	if s := e.summarize("jira"); s.Failed != 0 || len(s.Units) != 0 {
		t.Errorf("detached: %+v", s)
	}
	if err := e.svc.AddLink(e.ctx, b.ID, units.LinkExternal, "jira:ABC-1"); err != nil {
		t.Fatal(err)
	}
	if s := e.summarize("jira"); s.Failed != 1 {
		t.Errorf("linked: %+v", s)
	}
}

func TestSummarizeUnitOrder(t *testing.T) {
	e := newEnv(t)
	b := e.unit(units.LevelBrief, "", "Brief")
	var ids []string
	for range 11 {
		ids = append(ids, e.unit(units.LevelSlice, b.ID, "S").ID)
	}
	for i := len(ids) - 1; i >= 0; i-- {
		e.putRow(ids[i], "ABC-"+ids[i], func(r *Row) { r.Drift = "title" })
	}
	var got []string
	for _, u := range e.summarize("jira").Units {
		got = append(got, u.Unit)
	}
	if !slices.Equal(got, ids) {
		t.Errorf("order %v, want %v", got, ids)
	}
}

func TestSummarizeDetails(t *testing.T) {
	e := newEnv(t)
	b := e.unit(units.LevelBrief, "", "Brief")
	s1 := e.unit(units.LevelSlice, b.ID, "One")
	e.putRow(b.ID, "ABC-1", func(r *Row) { r.Drift = "title,body" })
	e.putRow(s1.ID, "ABC-2", func(r *Row) { r.FailedStep, r.LastError = "move", "no transition" })
	want := []SummaryUnit{
		{Unit: b.ID, Key: "ABC-1", Kind: "drift", Detail: "title, body"},
		{Unit: s1.ID, Key: "ABC-2", Kind: "failure", Detail: "move: no transition"},
	}
	if got := e.summarize("jira").Units; !slices.Equal(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestSummarizeReadsOnly(t *testing.T) {
	e := newEnv(t)
	b := e.unit(units.LevelBrief, "", "Brief")
	e.putRow(b.ID, "ABC-1", func(r *Row) { r.Drift = "title"; r.LastError = "x" })
	path := e.db.Path()
	read := func() []byte {
		var all []byte
		for _, p := range []string{path, path + "-wal"} {
			data, err := os.ReadFile(p)
			if err != nil && !os.IsNotExist(err) {
				e.t.Fatal(err)
			}
			all = append(all, data...)
		}
		return all
	}
	before := read()
	e.summarize("jira")
	if !bytes.Equal(before, read()) {
		t.Error("the database file changed")
	}
}
