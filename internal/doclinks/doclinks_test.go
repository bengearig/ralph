package doclinks_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/bengearig/ralph/internal/db"
	"github.com/bengearig/ralph/internal/doclinks"
	"github.com/bengearig/ralph/internal/documents"
	"github.com/bengearig/ralph/internal/testutil"
	"github.com/bengearig/ralph/internal/units"
)

var ctx = context.Background()

type fixture struct {
	links *doclinks.Service
	units *units.Store
	docs  *documents.Store
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	d := testutil.NewDB(t, db.Migrations)
	u, ds := units.New(d), documents.New(d)
	return fixture{links: doclinks.New(u, ds), units: u, docs: ds}
}

func (f fixture) unit(t *testing.T, level units.Level, parent, title string) units.Unit {
	t.Helper()
	u, err := f.units.Create(ctx, units.NewUnit{Level: level, ParentID: parent, Title: title, Body: "body"})
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func (f fixture) doc(t *testing.T, title, brief string) documents.Document {
	t.Helper()
	d, err := f.docs.Create(ctx, documents.NewDocument{Kind: documents.KindDesign, BriefID: brief, Title: title, Body: "body"})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func (f fixture) tree(t *testing.T) (b, s, tk units.Unit) {
	t.Helper()
	b = f.unit(t, units.LevelBrief, "", "brief")
	s = f.unit(t, units.LevelSlice, b.ID, "slice")
	tk = f.unit(t, units.LevelTask, s.ID, "task")
	return b, s, tk
}

func (f fixture) linkOK(t *testing.T, doc, unit string) {
	t.Helper()
	if err := f.links.Link(ctx, doc, unit); err != nil {
		t.Fatal(err)
	}
}

func ids(ls []doclinks.Linked) string {
	parts := make([]string, len(ls))
	for i, l := range ls {
		parts[i] = l.ID + "<" + l.From
	}
	return strings.Join(parts, ",")
}

func TestLinkActiveDocument(t *testing.T) {
	f := newFixture(t)
	_, _, tk := f.tree(t)
	d := f.doc(t, "Design", "")
	f.linkOK(t, d.ID, tk.ID)
	direct, _, err := f.links.DocumentsOf(ctx, tk.ID)
	if err != nil || len(direct) != 1 || direct[0] != (doclinks.Linked{ID: d.ID, Title: "Design", Kind: "design", Status: "ACTIVE"}) {
		t.Fatalf("direct = %+v, %v", direct, err)
	}
}

func TestLinkTwiceChangesNothing(t *testing.T) {
	f := newFixture(t)
	_, _, tk := f.tree(t)
	d := f.doc(t, "Design", "")
	f.linkOK(t, d.ID, tk.ID)
	before, _ := f.units.Links(ctx, tk.ID)
	f.linkOK(t, d.ID, tk.ID)
	after, _ := f.units.Links(ctx, tk.ID)
	if len(after) != 1 || !after[0].CreatedAt.Equal(before[0].CreatedAt) {
		t.Fatalf("links = %+v, before %+v", after, before)
	}
}

func TestLinkMalformedDocumentID(t *testing.T) {
	f := newFixture(t)
	_, _, tk := f.tree(t)
	for _, in := range []string{"d3", "D-0", "D-03", "X-1", ""} {
		err := f.links.Link(ctx, in, tk.ID)
		if !errors.Is(err, units.ErrInvalid) || !strings.Contains(err.Error(), `"`+strings.ToUpper(in)+`"`) {
			t.Errorf("Link(%q) = %v", in, err)
		}
		if err := f.links.Unlink(ctx, in, tk.ID); !errors.Is(err, units.ErrInvalid) {
			t.Errorf("Unlink(%q) = %v", in, err)
		}
	}
}

func TestLinkMissingDocumentNotFound(t *testing.T) {
	f := newFixture(t)
	_, _, tk := f.tree(t)
	err := f.links.Link(ctx, "D-9", tk.ID)
	if !errors.Is(err, documents.ErrNotFound) || err.Error() != "document D-9 was not found" {
		t.Fatalf("err = %v", err)
	}
}

func TestLinkArchivedDocumentInvalid(t *testing.T) {
	f := newFixture(t)
	_, _, tk := f.tree(t)
	d := f.doc(t, "Old", "")
	if _, err := f.docs.Archive(ctx, d.ID, d.Fingerprint); err != nil {
		t.Fatal(err)
	}
	err := f.links.Link(ctx, d.ID, tk.ID)
	if !errors.Is(err, units.ErrInvalid) || !strings.Contains(err.Error(), "document "+d.ID+" is archived and cannot be linked") {
		t.Fatalf("err = %v", err)
	}
}

func TestLinkSupersededNamesReplacement(t *testing.T) {
	f := newFixture(t)
	_, _, tk := f.tree(t)
	old, repl := f.doc(t, "Old", ""), f.doc(t, "New", "")
	if _, err := f.docs.Supersede(ctx, old.ID, old.Fingerprint, repl.ID); err != nil {
		t.Fatal(err)
	}
	err := f.links.Link(ctx, old.ID, tk.ID)
	want := "document " + old.ID + " is superseded by " + repl.ID + " and cannot be linked"
	if !errors.Is(err, units.ErrInvalid) || !strings.Contains(err.Error(), want) {
		t.Fatalf("err = %v", err)
	}
}

func TestLinkMissingUnitNotFound(t *testing.T) {
	f := newFixture(t)
	d := f.doc(t, "Design", "")
	if err := f.links.Link(ctx, d.ID, "T-99"); !errors.Is(err, units.ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestLinkNormalizesCase(t *testing.T) {
	f := newFixture(t)
	_, _, tk := f.tree(t)
	d := f.doc(t, "Design", "")
	f.linkOK(t, " "+strings.ToLower(d.ID)+" ", strings.ToLower(tk.ID))
	direct, _, _ := f.links.DocumentsOf(ctx, tk.ID)
	if len(direct) != 1 || direct[0].ID != d.ID {
		t.Fatalf("direct = %+v", direct)
	}
}

func TestLinkToBriefSliceAndTask(t *testing.T) {
	f := newFixture(t)
	b, s, tk := f.tree(t)
	d := f.doc(t, "Design", "")
	for _, u := range []string{b.ID, s.ID, tk.ID} {
		f.linkOK(t, d.ID, u)
	}
	refs, err := f.links.UnitsOf(ctx, d.ID)
	if err != nil || len(refs) != 3 {
		t.Fatalf("refs = %+v, %v", refs, err)
	}
}

func TestTiedDocumentIsNotLinked(t *testing.T) {
	f := newFixture(t)
	b, _, _ := f.tree(t)
	f.doc(t, "Tied", b.ID)
	direct, inherited, err := f.links.DocumentsOf(ctx, b.ID)
	if err != nil || len(direct) != 0 || len(inherited) != 0 {
		t.Fatalf("direct = %+v, inherited = %+v, %v", direct, inherited, err)
	}
}

func TestUnlinkRemoves(t *testing.T) {
	f := newFixture(t)
	_, _, tk := f.tree(t)
	d := f.doc(t, "Design", "")
	f.linkOK(t, d.ID, tk.ID)
	if err := f.links.Unlink(ctx, strings.ToLower(d.ID), tk.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.units.Links(ctx, tk.ID); len(got) != 0 {
		t.Fatalf("links = %+v", got)
	}
}

func TestUnlinkMissingLinkIsFine(t *testing.T) {
	f := newFixture(t)
	_, _, tk := f.tree(t)
	if err := f.links.Unlink(ctx, "D-4", tk.ID); err != nil {
		t.Fatal(err)
	}
}

func TestUnlinkArchivedDocumentWorks(t *testing.T) {
	f := newFixture(t)
	_, _, tk := f.tree(t)
	d := f.doc(t, "Design", "")
	f.linkOK(t, d.ID, tk.ID)
	if _, err := f.docs.Archive(ctx, d.ID, d.Fingerprint); err != nil {
		t.Fatal(err)
	}
	if err := f.links.Unlink(ctx, d.ID, tk.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.units.Links(ctx, tk.ID); len(got) != 0 {
		t.Fatalf("links = %+v", got)
	}
}

func TestUnlinkMissingDocumentWorks(t *testing.T) {
	f := newFixture(t)
	_, _, tk := f.tree(t)
	if err := f.units.AddLink(ctx, tk.ID, units.LinkDocument, "D-99"); err != nil {
		t.Fatal(err)
	}
	if err := f.links.Unlink(ctx, "D-99", tk.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.units.Links(ctx, tk.ID); len(got) != 0 {
		t.Fatalf("links = %+v", got)
	}
}

func TestUnlinkUnknownUnitNotFound(t *testing.T) {
	f := newFixture(t)
	if err := f.links.Unlink(ctx, "D-1", "T-99"); !errors.Is(err, units.ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestDocumentsOfDirectAndInherited(t *testing.T) {
	f := newFixture(t)
	b, s, tk := f.tree(t)
	d1, d2, d3, d4 := f.doc(t, "a", ""), f.doc(t, "b", ""), f.doc(t, "c", ""), f.doc(t, "d", "")
	f.linkOK(t, d4.ID, b.ID)
	f.linkOK(t, d3.ID, s.ID)
	f.linkOK(t, d2.ID, tk.ID)
	f.linkOK(t, d1.ID, b.ID)
	direct, inherited, err := f.links.DocumentsOf(ctx, tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := ids(direct), "D-2<"; got != want {
		t.Errorf("direct = %s, want %s", got, want)
	}
	if got, want := ids(inherited), "D-3<S-1,D-1<B-1,D-4<B-1"; got != want {
		t.Errorf("inherited = %s, want %s", got, want)
	}
}

func TestDocumentsOfDedupesNearest(t *testing.T) {
	f := newFixture(t)
	b, s, tk := f.tree(t)
	d1, d2 := f.doc(t, "a", ""), f.doc(t, "b", "")
	f.linkOK(t, d1.ID, b.ID)
	f.linkOK(t, d1.ID, s.ID)
	f.linkOK(t, d2.ID, b.ID)
	f.linkOK(t, d2.ID, tk.ID)
	direct, inherited, err := f.links.DocumentsOf(ctx, tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ids(direct) != "D-2<" || ids(inherited) != "D-1<S-1" {
		t.Errorf("direct = %s, inherited = %s", ids(direct), ids(inherited))
	}
}

func TestDocumentsOfOrphanTaskHasNoAncestors(t *testing.T) {
	f := newFixture(t)
	tk := f.unit(t, units.LevelTask, "", "orphan")
	d := f.doc(t, "a", "")
	f.linkOK(t, d.ID, tk.ID)
	direct, inherited, err := f.links.DocumentsOf(ctx, tk.ID)
	if err != nil || len(direct) != 1 || inherited == nil || len(inherited) != 0 {
		t.Fatalf("direct = %+v, inherited = %#v, %v", direct, inherited, err)
	}
}

func TestDocumentsOfShowsStatusAndReplacement(t *testing.T) {
	f := newFixture(t)
	_, _, tk := f.tree(t)
	old, repl := f.doc(t, "Old", ""), f.doc(t, "New", "")
	f.linkOK(t, old.ID, tk.ID)
	if _, err := f.docs.Supersede(ctx, old.ID, old.Fingerprint, repl.ID); err != nil {
		t.Fatal(err)
	}
	direct, _, err := f.links.DocumentsOf(ctx, tk.ID)
	if err != nil || len(direct) != 1 {
		t.Fatalf("direct = %+v, %v", direct, err)
	}
	if g := direct[0]; g.Status != "SUPERSEDED" || g.SupersededBy != repl.ID || g.Title != "Old" {
		t.Errorf("got %+v", g)
	}
}

func TestDocumentsOfMissingTarget(t *testing.T) {
	f := newFixture(t)
	_, _, tk := f.tree(t)
	for _, target := range []string{"D-99", "D-03", "weird"} {
		if err := f.units.AddLink(ctx, tk.ID, units.LinkDocument, target); err != nil {
			t.Fatal(err)
		}
	}
	direct, _, err := f.links.DocumentsOf(ctx, tk.ID)
	if err != nil || len(direct) != 3 {
		t.Fatalf("direct = %+v, %v", direct, err)
	}
	for _, l := range direct {
		if l.Status != "MISSING" || l.Title != "" || l.Kind != "" {
			t.Errorf("got %+v", l)
		}
	}
}

func TestDocumentsOfEmptyIsNonNil(t *testing.T) {
	f := newFixture(t)
	b, _, _ := f.tree(t)
	direct, inherited, err := f.links.DocumentsOf(ctx, b.ID)
	if err != nil || direct == nil || inherited == nil || len(direct)+len(inherited) != 0 {
		t.Fatalf("direct = %#v, inherited = %#v, %v", direct, inherited, err)
	}
}

func TestUnitsOfListsUnits(t *testing.T) {
	f := newFixture(t)
	b, s, tk := f.tree(t)
	d := f.doc(t, "a", "")
	f.linkOK(t, d.ID, tk.ID)
	f.linkOK(t, d.ID, b.ID)
	f.linkOK(t, d.ID, s.ID)
	refs, err := f.links.UnitsOf(ctx, strings.ToLower(d.ID))
	if err != nil {
		t.Fatal(err)
	}
	want := []doclinks.UnitRef{{b.ID, "brief", "brief"}, {s.ID, "slice", "slice"}, {tk.ID, "task", "task"}}
	if len(refs) != 3 || refs[0] != want[0] || refs[1] != want[1] || refs[2] != want[2] {
		t.Fatalf("refs = %+v", refs)
	}
}

func TestUnitsOfUnknownWellFormedDocument(t *testing.T) {
	f := newFixture(t)
	_, _, tk := f.tree(t)
	if err := f.units.AddLink(ctx, tk.ID, units.LinkDocument, "D-99"); err != nil {
		t.Fatal(err)
	}
	refs, err := f.links.UnitsOf(ctx, "D-99")
	if err != nil || len(refs) != 1 {
		t.Fatalf("refs = %+v, %v", refs, err)
	}
	none, err := f.links.UnitsOf(ctx, "D-98")
	if err != nil || none == nil || len(none) != 0 {
		t.Fatalf("none = %#v, %v", none, err)
	}
}

func TestUnitsOfMalformedInvalid(t *testing.T) {
	f := newFixture(t)
	if _, err := f.links.UnitsOf(ctx, "D-03"); !errors.Is(err, units.ErrInvalid) {
		t.Fatalf("err = %v", err)
	}
}
