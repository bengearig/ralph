package documents_test

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/bengearig/ralph/internal/db"
	"github.com/bengearig/ralph/internal/documents"
	"github.com/bengearig/ralph/internal/testutil"
	"github.com/bengearig/ralph/internal/units"
)

func (f fixture) edit(t *testing.T, d documents.Document, body string) documents.Document {
	t.Helper()
	got, err := f.docs.Update(ctx, d.ID, d.Fingerprint, documents.Change{Body: &body})
	if err != nil {
		t.Fatalf("edit %s: %v", d.ID, err)
	}
	return got
}

func (f fixture) editN(t *testing.T, d documents.Document, n int) documents.Document {
	t.Helper()
	for i := 1; i <= n; i++ {
		d = f.edit(t, d, fmt.Sprintf("body %d", i+1))
	}
	return d
}

func versionNumbers(t *testing.T, f fixture, id string) []int {
	t.Helper()
	vs, err := f.docs.Versions(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	out := []int{}
	for _, v := range vs {
		out = append(out, v.Version)
	}
	return out
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestEditKeepsEarlierVersion(t *testing.T) {
	f := newFixture(t)
	d := f.create(t, documents.NewDocument{Kind: documents.KindDesign, Title: "old title", Body: "old body"})
	title, kind := "new title", documents.KindDecision
	if _, err := f.docs.Update(ctx, d.ID, d.Fingerprint, documents.Change{Title: &title, Kind: &kind, Body: ptr("new body")}); err != nil {
		t.Fatal(err)
	}
	vs, err := f.docs.Versions(ctx, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 1 || vs[0].Version != 1 || vs[0].Title != "old title" || vs[0].Kind != documents.KindDesign || vs[0].Size != len("old body") {
		t.Fatalf("versions = %+v", vs)
	}
	v, err := f.docs.Version(ctx, d.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if v.Body != "old body" || v.Current || v.ID != d.ID || v.Title != "old title" || v.Kind != documents.KindDesign {
		t.Errorf("version = %+v", v)
	}
}

func TestVersionsSizeIsBytes(t *testing.T) {
	f := newFixture(t)
	d := f.create(t, documents.NewDocument{Body: "héllo"})
	f.edit(t, d, "next")
	vs, _ := f.docs.Versions(ctx, d.ID)
	if len(vs) != 1 || vs[0].Size != len("héllo") {
		t.Errorf("versions = %+v", vs)
	}
}

func TestEditsNumberVersionsInOrder(t *testing.T) {
	f := newFixture(t)
	d := f.editN(t, f.create(t, documents.NewDocument{}), 3)
	if d.Version != 4 {
		t.Errorf("current version = %d, want 4", d.Version)
	}
	if got := versionNumbers(t, f, d.ID); !equalInts(got, []int{1, 2, 3}) {
		t.Errorf("versions = %v", got)
	}
}

func TestVersionCurrentIsTheDocument(t *testing.T) {
	f := newFixture(t)
	d := f.editN(t, f.create(t, documents.NewDocument{}), 2)
	v, err := f.docs.Version(ctx, d.ID, d.Version)
	if err != nil {
		t.Fatal(err)
	}
	if !v.Current || v.Body != d.Body || v.Title != d.Title || v.Kind != d.Kind || v.Version != d.Version || !v.CreatedAt.Equal(d.UpdatedAt) {
		t.Errorf("version = %+v, document = %+v", v, d)
	}
}

func TestVersionRecordsTimeAndActorOfThatContent(t *testing.T) {
	f := newFixture(t)
	d, err := f.docs.Create(units.WithActor(ctx, "alice"), documents.NewDocument{Kind: documents.KindDesign, Title: "t", Body: "one"})
	if err != nil {
		t.Fatal(err)
	}
	d2, err := f.docs.Update(units.WithActor(ctx, "bob"), d.ID, d.Fingerprint, documents.Change{Body: ptr("two")})
	if err != nil {
		t.Fatal(err)
	}
	d3, err := f.docs.Update(units.WithActor(ctx, "carol"), d2.ID, d2.Fingerprint, documents.Change{Body: ptr("three")})
	if err != nil {
		t.Fatal(err)
	}
	v1, err := f.docs.Version(ctx, d.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if v1.CreatedBy != "alice" || !v1.CreatedAt.Equal(d.UpdatedAt) {
		t.Errorf("v1 = %+v, want alice at %v", v1, d.UpdatedAt)
	}
	v2, err := f.docs.Version(ctx, d.ID, 2)
	if err != nil {
		t.Fatal(err)
	}
	if v2.CreatedBy != "bob" || !v2.CreatedAt.Equal(d2.UpdatedAt) || v2.CreatedAt.Equal(d3.UpdatedAt) {
		t.Errorf("v2 = %+v, want bob at %v", v2, d2.UpdatedAt)
	}
}

func TestNoopEditMakesNoVersion(t *testing.T) {
	f := newFixture(t)
	d := f.create(t, documents.NewDocument{Title: "t", Body: "b"})
	if _, err := f.docs.Update(ctx, d.ID, d.Fingerprint, documents.Change{Title: ptr("t"), Body: ptr("b")}); err != nil {
		t.Fatal(err)
	}
	if got := versionNumbers(t, f, d.ID); len(got) != 0 {
		t.Errorf("versions = %v", got)
	}
}

func TestStatusChangesMakeNoVersion(t *testing.T) {
	f := newFixture(t)
	d := f.create(t, documents.NewDocument{})
	r := f.create(t, documents.NewDocument{})
	d, err := f.docs.Archive(ctx, d.ID, d.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if d, err = f.docs.Restore(ctx, d.ID, d.Fingerprint); err != nil {
		t.Fatal(err)
	}
	if d, err = f.docs.Supersede(ctx, d.ID, d.Fingerprint, r.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.docs.Restore(ctx, d.ID, d.Fingerprint); err != nil {
		t.Fatal(err)
	}
	if got := versionNumbers(t, f, d.ID); len(got) != 0 {
		t.Errorf("versions = %v", got)
	}
}

func TestVersionsAreInSameTransactionAsEdit(t *testing.T) {
	f := newFixture(t)
	d := f.create(t, documents.NewDocument{Body: "original"})
	if _, err := f.d.SQL().Exec("CREATE TRIGGER boom BEFORE UPDATE ON documents WHEN NEW.version > OLD.version BEGIN SELECT RAISE(ABORT, 'boom'); END"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.docs.Update(ctx, d.ID, d.Fingerprint, documents.Change{Body: ptr("changed")}); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v", err)
	}
	got, err := f.docs.Get(ctx, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Body != "original" || got.Version != 1 {
		t.Errorf("document = %+v", got)
	}
	if vs := versionNumbers(t, f, d.ID); len(vs) != 0 {
		t.Errorf("versions = %v", vs)
	}
}

func TestVersionNotFound(t *testing.T) {
	f := newFixture(t)
	d := f.editN(t, f.create(t, documents.NewDocument{}), 3)
	kept := documents.New(f.d, documents.WithKeepVersions(1))
	d2, err := kept.Update(ctx, d.ID, d.Fingerprint, documents.Change{Body: ptr("later")})
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []int{1, 2, 3, d2.Version + 1, 99} {
		_, err := f.docs.Version(ctx, d.ID, n)
		if !errors.Is(err, documents.ErrNotFound) || !strings.Contains(err.Error(), fmt.Sprintf("version %d", n)) {
			t.Errorf("version %d: err = %v", n, err)
		}
	}
	if _, err := f.docs.Version(ctx, d.ID, 4); err != nil {
		t.Errorf("version 4: %v", err)
	}
	if _, err := f.docs.Version(ctx, "D-99", 1); !errors.Is(err, documents.ErrNotFound) {
		t.Errorf("unknown document: err = %v", err)
	}
	if _, err := f.docs.Versions(ctx, "D-99"); !errors.Is(err, documents.ErrNotFound) {
		t.Errorf("unknown document versions: err = %v", err)
	}
}

func TestVersionInvalidNumber(t *testing.T) {
	f := newFixture(t)
	d := f.create(t, documents.NewDocument{})
	for _, n := range []int{0, -1} {
		if _, err := f.docs.Version(ctx, d.ID, n); !errors.Is(err, documents.ErrInvalid) {
			t.Errorf("version %d: err = %v", n, err)
		}
	}
}

func TestVersionsOfArchivedDocument(t *testing.T) {
	f := newFixture(t)
	d := f.edit(t, f.create(t, documents.NewDocument{Body: "first"}), "second")
	if _, err := f.docs.Archive(ctx, d.ID, d.Fingerprint); err != nil {
		t.Fatal(err)
	}
	if got := versionNumbers(t, f, d.ID); !equalInts(got, []int{1}) {
		t.Errorf("versions = %v", got)
	}
	if v, err := f.docs.Version(ctx, d.ID, 1); err != nil || v.Body != "first" {
		t.Errorf("version = %+v, %v", v, err)
	}
	if v, err := f.docs.Version(ctx, d.ID, 2); err != nil || !v.Current {
		t.Errorf("current = %+v, %v", v, err)
	}
}

func TestVersionsEmptyForNewDocument(t *testing.T) {
	f := newFixture(t)
	d := f.create(t, documents.NewDocument{})
	vs, err := f.docs.Versions(ctx, d.ID)
	if err != nil || vs == nil || len(vs) != 0 {
		t.Errorf("versions = %#v, %v", vs, err)
	}
}

func keepFixture(t *testing.T, keep int) fixture {
	t.Helper()
	f := newFixture(t)
	now := f.docs.Now
	f.docs = documents.New(f.d, documents.WithKeepVersions(keep))
	f.docs.Now = now
	return f
}

func TestKeepZeroKeepsAll(t *testing.T) {
	f := keepFixture(t, 0)
	d := f.editN(t, f.create(t, documents.NewDocument{}), 10)
	if got := versionNumbers(t, f, d.ID); len(got) != 10 {
		t.Errorf("versions = %v", got)
	}
}

func TestKeepTrimsOldest(t *testing.T) {
	f := keepFixture(t, 2)
	d := f.editN(t, f.create(t, documents.NewDocument{}), 5)
	if got := versionNumbers(t, f, d.ID); !equalInts(got, []int{4, 5}) {
		t.Errorf("versions = %v", got)
	}
	for _, n := range []int{1, 2, 3} {
		if _, err := f.docs.Version(ctx, d.ID, n); !errors.Is(err, documents.ErrNotFound) {
			t.Errorf("version %d: err = %v", n, err)
		}
	}
}

func TestKeepAppliedPerDocument(t *testing.T) {
	f := keepFixture(t, 1)
	a := f.create(t, documents.NewDocument{})
	b := f.create(t, documents.NewDocument{})
	b = f.editN(t, b, 3)
	a = f.editN(t, a, 1)
	if got := versionNumbers(t, f, a.ID); !equalInts(got, []int{1}) {
		t.Errorf("a versions = %v", got)
	}
	if got := versionNumbers(t, f, b.ID); !equalInts(got, []int{3}) {
		t.Errorf("b versions = %v", got)
	}
	f.editN(t, a, 2)
	if got := versionNumbers(t, f, b.ID); !equalInts(got, []int{3}) {
		t.Errorf("b versions after a's edits = %v", got)
	}
}

func TestKeepNegativeTreatedAsZero(t *testing.T) {
	f := keepFixture(t, -3)
	d := f.editN(t, f.create(t, documents.NewDocument{}), 4)
	if got := versionNumbers(t, f, d.ID); !equalInts(got, []int{1, 2, 3, 4}) {
		t.Errorf("versions = %v", got)
	}
}

func TestKeepChangeAppliesOnNextEdit(t *testing.T) {
	f := newFixture(t)
	d := f.editN(t, f.create(t, documents.NewDocument{}), 4)
	if got := versionNumbers(t, f, d.ID); !equalInts(got, []int{1, 2, 3, 4}) {
		t.Fatalf("versions = %v", got)
	}
	lower := documents.New(f.d, documents.WithKeepVersions(2))
	if got := versionNumbers(t, f, d.ID); !equalInts(got, []int{1, 2, 3, 4}) {
		t.Errorf("versions before next edit = %v", got)
	}
	if _, err := lower.Update(ctx, d.ID, d.Fingerprint, documents.Change{Body: ptr("another")}); err != nil {
		t.Fatal(err)
	}
	if got := versionNumbers(t, f, d.ID); !equalInts(got, []int{4, 5}) {
		t.Errorf("versions after edit = %v", got)
	}
}

func TestVersionsConcurrentEdits(t *testing.T) {
	d := testutil.NewDB(t, db.Migrations)
	s := documents.New(d, documents.WithKeepVersions(3))
	doc, err := s.Create(ctx, documents.NewDocument{Kind: documents.KindDesign, Title: "t", Body: "start"})
	if err != nil {
		t.Fatal(err)
	}
	var (
		wg   sync.WaitGroup
		errs = make([]error, 2)
	)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = s.Update(ctx, doc.ID, doc.Fingerprint, documents.Change{Body: ptr(fmt.Sprintf("writer %d", i))})
		}()
	}
	wg.Wait()
	var ok, stale int
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, db.ErrStale):
			stale++
		default:
			t.Errorf("unexpected error: %v", err)
		}
	}
	if ok != 1 || stale != 1 {
		t.Fatalf("ok = %d, stale = %d", ok, stale)
	}
	got, err := s.Get(ctx, doc.ID)
	if err != nil {
		t.Fatal(err)
	}
	vs, err := s.Versions(ctx, doc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != 2 || len(vs) != 1 || vs[0].Version != 1 {
		t.Errorf("version = %d, earlier = %+v", got.Version, vs)
	}
	if v, err := s.Version(ctx, doc.ID, 1); err != nil || v.Body != "start" {
		t.Errorf("version 1 = %+v, %v", v, err)
	}
}
