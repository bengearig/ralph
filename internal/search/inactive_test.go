package search

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/bengearig/ralph/internal/documents"
	"github.com/bengearig/ralph/internal/units"
)

func (e *indexEnv) find(q Query) Result {
	e.t.Helper()
	r, err := e.svc.Search(context.Background(), q)
	if err != nil {
		e.t.Fatalf("search %+v: %v", q, err)
	}
	return r
}

func (e *indexEnv) hitIDs(q Query) []string {
	e.t.Helper()
	out := []string{}
	for _, h := range e.find(q).Hits {
		out = append(out, string(h.Kind)+":"+h.ID)
	}
	return out
}

func (e *indexEnv) cancel(u units.Unit) {
	e.t.Helper()
	if _, err := e.units.SetState(context.Background(), u.ID, u.Fingerprint, units.StateCancelled, "dropped"); err != nil {
		e.t.Fatal(err)
	}
}

func (e *indexEnv) note(unitID, body string) units.Note {
	e.t.Helper()
	n, err := e.units.AddNote(context.Background(), unitID, units.NoteGeneral, body)
	if err != nil {
		e.t.Fatal(err)
	}
	return n
}

func (e *indexEnv) setStatus(d documents.Document, to string) {
	e.t.Helper()
	ctx := context.Background()
	var err error
	switch to {
	case "ARCHIVED":
		_, err = e.docs.Archive(ctx, d.ID, d.Fingerprint)
	case "SUPERSEDED":
		repl := e.doc("replacement for "+d.ID, "replacement")
		_, err = e.docs.Supersede(ctx, d.ID, d.Fingerprint, repl.ID)
	}
	if err != nil {
		e.t.Fatal(err)
	}
}

func wantIDs(t *testing.T, got []string, want ...string) {
	t.Helper()
	g, w := slices.Clone(got), slices.Clone(want)
	slices.Sort(g)
	slices.Sort(w)
	if !slices.Equal(g, w) {
		t.Errorf("hits = %v, want %v", got, want)
	}
}

func TestInactiveDocumentsExcludedByDefault(t *testing.T) {
	e := newIndexEnv(t)
	live := e.doc("ocelot live", "spots")
	arch := e.doc("ocelot archived", "spots")
	sup := e.doc("ocelot superseded", "spots")
	e.setStatus(arch, "ARCHIVED")
	e.setStatus(sup, "SUPERSEDED")
	wantIDs(t, e.hitIDs(Query{Text: "ocelot"}), "document:"+live.ID)
}

func TestInactiveCancelledTaskExcludedByDefault(t *testing.T) {
	e := newIndexEnv(t)
	live := e.task("lynx live", "tufts", "")
	dead := e.task("lynx dead", "tufts", "")
	e.cancel(dead)
	wantIDs(t, e.hitIDs(Query{Text: "lynx"}), "task:"+live.ID)
}

func TestInactiveNotesOfCancelledTaskExcluded(t *testing.T) {
	e := newIndexEnv(t)
	live := e.task("one", "x", "")
	dead := e.task("two", "x", "")
	e.note(live.ID, "marmot burrow")
	e.note(dead.ID, "marmot burrow")
	e.cancel(dead)
	wantIDs(t, e.hitIDs(Query{Text: "marmot"}), "note:"+live.ID)
}

func TestInactiveNoteOnSliceAndBriefAlwaysActive(t *testing.T) {
	e := newIndexEnv(t)
	e.note(e.slice.ID, "pangolin scales")
	e.note(e.brief.ID, "pangolin scales")
	wantIDs(t, e.hitIDs(Query{Text: "pangolin"}), "note:"+e.slice.ID, "note:"+e.brief.ID)
}

func TestInactiveSliceAndBriefNeverExcluded(t *testing.T) {
	e := newIndexEnv(t)
	task := e.task("only", "x", "")
	e.cancel(task)
	wantIDs(t, e.hitIDs(Query{Text: "slice title"}), "slice:"+e.slice.ID)
	wantIDs(t, e.hitIDs(Query{Text: "brief title"}), "brief:"+e.brief.ID)
	for _, h := range e.find(Query{Text: "title", Kinds: []Kind{KindSlice, KindBrief}}).Hits {
		if h.Inactive {
			t.Errorf("%s hit marked inactive", h.Kind)
		}
	}
}

func TestIncludeInactiveReturnsThemMarked(t *testing.T) {
	e := newIndexEnv(t)
	arch := e.doc("civet archived", "musk")
	sup := e.doc("civet superseded", "musk")
	task := e.task("civet task", "musk", "")
	e.setStatus(arch, "ARCHIVED")
	e.setStatus(sup, "SUPERSEDED")
	e.cancel(task)
	e.note(task.ID, "civet note")
	want := map[string]string{
		"document:" + arch.ID: "ARCHIVED",
		"document:" + sup.ID:  "SUPERSEDED",
		"task:" + task.ID:     "CANCELLED",
		"note:" + task.ID:     "CANCELLED",
	}
	hits := e.find(Query{Text: "civet", IncludeInactive: true}).Hits
	if len(hits) != len(want) {
		t.Fatalf("hits = %+v, want %d", hits, len(want))
	}
	for _, h := range hits {
		if st, ok := want[string(h.Kind)+":"+h.ID]; !ok || h.Status != st || !h.Inactive {
			t.Errorf("hit = %+v, want status %q and Inactive", h, st)
		}
	}
}

func TestIncludeInactiveActiveHitsNotMarked(t *testing.T) {
	e := newIndexEnv(t)
	e.doc("tarsier doc", "eyes")
	task := e.task("tarsier task", "eyes", "")
	e.note(task.ID, "tarsier note")
	e.note(e.slice.ID, "tarsier note")
	hits := e.find(Query{Text: "tarsier", IncludeInactive: true}).Hits
	if len(hits) != 4 {
		t.Fatalf("hits = %+v", hits)
	}
	for _, h := range hits {
		if h.Inactive {
			t.Errorf("hit %+v marked inactive", h)
		}
	}
}

func TestInactiveFilterAppliesBeforeLimit(t *testing.T) {
	e := newIndexEnv(t)
	for i := range 25 {
		e.setStatus(e.doc(fmt.Sprintf("quokka quokka quokka %d", i), "quokka"), "ARCHIVED")
	}
	var live []string
	for i := range 3 {
		live = append(live, "document:"+e.doc(fmt.Sprintf("quokka plain %d", i), "filler text here and there").ID)
	}
	r := e.find(Query{Text: "quokka", Limit: 3})
	got := []string{}
	for _, h := range r.Hits {
		got = append(got, string(h.Kind)+":"+h.ID)
		if h.Inactive {
			t.Errorf("inactive hit %+v returned", h)
		}
	}
	wantIDs(t, got, live...)
	if top := e.find(Query{Text: "quokka", Limit: 1, IncludeInactive: true}).Hits; len(top) != 1 || !top[0].Inactive {
		t.Errorf("with IncludeInactive the best hit should be inactive: %+v", top)
	}
}

func TestInactiveFilterAppliesToRelaxedSearch(t *testing.T) {
	e := newIndexEnv(t)
	e.setStatus(e.doc("alpaca llama", "wool"), "ARCHIVED")
	partial := e.doc("alpaca only", "wool")
	r := e.find(Query{Text: "alpaca llama"})
	if !r.Relaxed {
		t.Fatalf("result = %+v, want relaxed", r)
	}
	wantIDs(t, ids(r), partial.ID)

	e.setStatus(partial, "ARCHIVED")
	other := e.doc("llama alone", "wool")
	r = e.find(Query{Text: "alpaca llama"})
	if !r.Relaxed {
		t.Fatalf("result = %+v, want relaxed", r)
	}
	wantIDs(t, ids(r), other.ID)
}

func TestInactiveRestoreMakesDocumentSearchableAgain(t *testing.T) {
	e := newIndexEnv(t)
	ctx := context.Background()
	d := e.doc("dugong", "seagrass")
	arch, err := e.docs.Archive(ctx, d.ID, d.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if got := e.hitIDs(Query{Text: "dugong"}); len(got) != 0 {
		t.Fatalf("archived document found: %v", got)
	}
	if _, err := e.docs.Restore(ctx, arch.ID, arch.Fingerprint); err != nil {
		t.Fatal(err)
	}
	wantIDs(t, e.hitIDs(Query{Text: "dugong"}), "document:"+d.ID)
}

func TestInactiveUncancelledTask(t *testing.T) {
	e := newIndexEnv(t)
	task := e.task("numbat", "termites", "")
	e.note(task.ID, "numbat note")
	if got := e.hitIDs(Query{Text: "numbat"}); len(got) != 2 {
		t.Fatalf("before cancel: %v", got)
	}
	e.cancel(task)
	if got := e.hitIDs(Query{Text: "numbat"}); len(got) != 0 {
		t.Errorf("after cancel: %v", got)
	}
	if _, err := e.units.SetState(context.Background(), task.ID, "", units.StateTodo, ""); err == nil {
		t.Error("a cancelled task moved to TODO")
	}
}

func TestInactiveKindsFilterStillApplies(t *testing.T) {
	e := newIndexEnv(t)
	live := e.doc("gharial live", "snout")
	e.setStatus(e.doc("gharial old", "snout"), "ARCHIVED")
	task := e.task("gharial task", "snout", "")
	e.cancel(task)
	wantIDs(t, e.hitIDs(Query{Text: "gharial", Kinds: []Kind{KindDocument}}), "document:"+live.ID)
	if got := e.hitIDs(Query{Text: "gharial", Kinds: []Kind{KindTask}}); len(got) != 0 {
		t.Errorf("tasks = %v", got)
	}
	wantIDs(t, e.hitIDs(Query{Text: "gharial", Kinds: []Kind{KindTask}, IncludeInactive: true}), "task:"+task.ID)
}

func TestIncludeInactiveRanksTogether(t *testing.T) {
	e := newIndexEnv(t)
	e.setStatus(e.doc("serval serval serval", "serval"), "ARCHIVED")
	e.doc("serval middling", "filler words")
	e.setStatus(e.doc("filler title", "words serval filler"), "ARCHIVED")
	hits := e.find(Query{Text: "serval", IncludeInactive: true}).Hits
	if len(hits) != 3 {
		t.Fatalf("hits = %+v", hits)
	}
	for i := 1; i < len(hits); i++ {
		if hits[i-1].Score < hits[i].Score {
			t.Errorf("scores not descending: %+v", hits)
		}
	}
	if !hits[0].Inactive || hits[0].Score <= 0 {
		t.Errorf("best hit = %+v, want the inactive one with a positive score", hits[0])
	}
	var active, inactive int
	for _, h := range hits {
		if h.Inactive {
			inactive++
		} else {
			active++
		}
	}
	if active != 1 || inactive != 2 {
		t.Errorf("active = %d, inactive = %d", active, inactive)
	}
}

func TestInactiveDefaultMatchesDocsRule(t *testing.T) {
	e := newIndexEnv(t)
	states := map[string]bool{
		"PROPOSED": false, "TODO": false, "IN_PROGRESS": false, "BLOCKED": false, "DONE": false, "CANCELLED": true,
	}
	tasks := map[string]units.Unit{}
	for st := range states {
		u := e.task("hedgehog "+st, "spines", "")
		e.note(u.ID, "hedgehog note")
		if _, err := e.d.SQL().Exec("UPDATE units SET state = ? WHERE id = ?", st, u.ID); err != nil {
			t.Fatal(err)
		}
		tasks[st] = u
	}
	for st, inactive := range states {
		u := tasks[st]
		var got []Hit
		for _, h := range e.find(Query{Text: "hedgehog", IncludeInactive: true}).Hits {
			if h.ID == u.ID {
				got = append(got, h)
			}
		}
		if len(got) != 2 {
			t.Fatalf("%s: hits = %+v", st, got)
		}
		for _, h := range got {
			if h.Inactive != inactive {
				t.Errorf("%s %s: Inactive = %v, want %v", st, h.Kind, h.Inactive, inactive)
			}
		}
		found := slices.Contains(ids(e.find(Query{Text: "hedgehog", Kinds: []Kind{KindTask}})), u.ID)
		if found == inactive {
			t.Errorf("%s: found by default = %v, want %v", st, found, !inactive)
		}
	}
	for status, inactive := range map[string]bool{"ACTIVE": false, "ARCHIVED": true, "SUPERSEDED": true} {
		d := e.doc("wallaby "+status, "pouch")
		if status != "ACTIVE" {
			e.setStatus(d, status)
		}
		found := slices.Contains(ids(e.find(Query{Text: "wallaby", Kinds: []Kind{KindDocument}})), d.ID)
		if found == inactive {
			t.Errorf("document %s: found by default = %v, want %v", status, found, !inactive)
		}
	}
}
