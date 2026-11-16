package units_test

import (
	"context"
	"testing"

	"github.com/bengearig/ralph/internal/units"
)

const (
	alice = "Alice <alice@example.com>"
	bob   = "Bob <bob@example.com>"
)

func as(actor string) context.Context {
	return units.WithActor(context.Background(), actor)
}

func TestWithActorRoundTrip(t *testing.T) {
	if got := units.ActorFrom(as(alice)); got != alice {
		t.Errorf("ActorFrom = %q, want %q", got, alice)
	}
	if got := units.ActorFrom(context.Background()); got != "" {
		t.Errorf("ActorFrom of a bare context = %q, want empty", got)
	}
}

func TestCreateRecordsActor(t *testing.T) {
	s := newStore(t)
	u, err := s.Create(as(alice), units.NewUnit{Level: units.LevelBrief, Title: "b", Body: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if u.CreatedBy != alice || u.UpdatedBy != alice {
		t.Errorf("CreatedBy, UpdatedBy = %q, %q, want %q", u.CreatedBy, u.UpdatedBy, alice)
	}
}

func TestUpdateRecordsUpdatedBy(t *testing.T) {
	s := newStore(t)
	b, err := s.Create(as(alice), units.NewUnit{Level: units.LevelBrief, Title: "b", Body: "x"})
	if err != nil {
		t.Fatal(err)
	}
	title := "renamed"
	u, err := s.Update(as(bob), b.ID, b.Fingerprint, units.Change{Title: &title})
	if err != nil {
		t.Fatal(err)
	}
	if u.CreatedBy != alice || u.UpdatedBy != bob {
		t.Errorf("CreatedBy, UpdatedBy = %q, %q, want %q, %q", u.CreatedBy, u.UpdatedBy, alice, bob)
	}
}

func TestSetStateRecordsActorOnUnitAndNote(t *testing.T) {
	s := newStore(t)
	sl := slice(t, s, brief(t, s).ID)
	tk := task(t, s, sl.ID, "t")
	u, err := s.SetState(as(bob), tk.ID, tk.Fingerprint, units.StateTodo, "ready")
	if err != nil {
		t.Fatal(err)
	}
	if u.UpdatedBy != bob {
		t.Errorf("UpdatedBy = %q, want %q", u.UpdatedBy, bob)
	}
	notes, err := s.Notes(context.Background(), tk.ID)
	if err != nil || len(notes) != 1 {
		t.Fatalf("notes = %+v, err %v", notes, err)
	}
	if notes[0].Author != bob {
		t.Errorf("Author = %q, want %q", notes[0].Author, bob)
	}
}

func TestAddNoteRecordsAuthor(t *testing.T) {
	s := newStore(t)
	b := brief(t, s)
	n, err := s.AddNote(as(alice), b.ID, units.NoteGeneral, "hello")
	if err != nil {
		t.Fatal(err)
	}
	if n.Author != alice {
		t.Errorf("Author = %q, want %q", n.Author, alice)
	}
	notes, err := s.Notes(context.Background(), b.ID)
	if err != nil || len(notes) != 1 || notes[0].Author != alice {
		t.Errorf("notes = %+v, err %v", notes, err)
	}
}

func TestNoActorStoresEmpty(t *testing.T) {
	s := newStore(t)
	b := brief(t, s)
	n, err := s.AddNote(context.Background(), b.ID, units.NoteGeneral, "hello")
	if err != nil {
		t.Fatal(err)
	}
	if b.CreatedBy != "" || b.UpdatedBy != "" || n.Author != "" {
		t.Errorf("unit %q/%q, note %q, want all empty", b.CreatedBy, b.UpdatedBy, n.Author)
	}
}

func TestLinkChangesDoNotRecordActor(t *testing.T) {
	s := newStore(t)
	b, err := s.Create(as(alice), units.NewUnit{Level: units.LevelBrief, Title: "b", Body: "x"})
	if err != nil {
		t.Fatal(err)
	}
	const target = "https://example.com"
	if err := s.AddLink(as(bob), b.ID, units.LinkExternal, target); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get(context.Background(), b.ID); got.UpdatedBy != alice {
		t.Errorf("UpdatedBy after AddLink = %q, want %q", got.UpdatedBy, alice)
	}
	if err := s.RemoveLink(as(bob), b.ID, units.LinkExternal, target); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get(context.Background(), b.ID); got.UpdatedBy != alice {
		t.Errorf("UpdatedBy after RemoveLink = %q, want %q", got.UpdatedBy, alice)
	}
}

func TestActorDoesNotChangeFingerprint(t *testing.T) {
	s1, s2 := newStore(t), newStore(t)
	n := units.NewUnit{Level: units.LevelBrief, Title: "b", Body: "x"}
	a, err := s1.Create(as(alice), n)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s2.Create(as(bob), n)
	if err != nil {
		t.Fatal(err)
	}
	if a.Fingerprint != b.Fingerprint {
		t.Errorf("fingerprints differ: %s vs %s", a.Fingerprint, b.Fingerprint)
	}

	title := a.Title
	u, err := s1.Update(as(bob), a.ID, a.Fingerprint, units.Change{Title: &title})
	if err != nil {
		t.Fatal(err)
	}
	if u.UpdatedBy != bob || u.Fingerprint != a.Fingerprint {
		t.Errorf("UpdatedBy %q, fingerprint %s, want %q, %s", u.UpdatedBy, u.Fingerprint, bob, a.Fingerprint)
	}
}
