package documents_test

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bengearig/ralph/internal/documents"
)

func (f fixture) setNow(t time.Time) {
	f.docs.Now = func() time.Time { return t }
}

// versionAges gives every earlier version of id a creation time of
// now minus its age in days, taken from days indexed by version number - 1.
func (f fixture) versionAges(t *testing.T, id string, now time.Time, days ...int) {
	t.Helper()
	for i, d := range days {
		at := now.Add(-time.Duration(d) * 24 * time.Hour).Format(time.RFC3339Nano)
		if _, err := f.d.SQL().Exec("UPDATE document_versions SET created_at = ? WHERE document_id = ? AND version = ?", at, id, i+1); err != nil {
			t.Fatal(err)
		}
	}
}

func purge(t *testing.T, f fixture, o documents.PurgeOptions) documents.PurgeResult {
	t.Helper()
	res, err := f.docs.Purge(ctx, o)
	if err != nil {
		t.Fatalf("purge %+v: %v", o, err)
	}
	return res
}

func entryVersions(res documents.PurgeResult, id string) []int {
	for _, e := range res.Entries {
		if e.ID == id {
			return e.Versions
		}
	}
	return nil
}

func TestPurgeNeedsCriteria(t *testing.T) {
	f := newFixture(t)
	_, err := f.docs.Purge(ctx, documents.PurgeOptions{Apply: true})
	if !errors.Is(err, documents.ErrInvalid) || !strings.Contains(err.Error(), "--keep") || !strings.Contains(err.Error(), "--older-than") {
		t.Fatalf("err = %v", err)
	}
}

func TestPurgeRejectsNegative(t *testing.T) {
	f := newFixture(t)
	if _, err := f.docs.Purge(ctx, documents.PurgeOptions{Keep: ptr(-1)}); !errors.Is(err, documents.ErrInvalid) {
		t.Errorf("negative keep: %v", err)
	}
	if _, err := f.docs.Purge(ctx, documents.PurgeOptions{OlderThan: -time.Hour}); !errors.Is(err, documents.ErrInvalid) {
		t.Errorf("negative older-than: %v", err)
	}
	if _, err := f.docs.Purge(ctx, documents.PurgeOptions{Keep: ptr(1), OlderThan: -time.Hour}); !errors.Is(err, documents.ErrInvalid) {
		t.Errorf("negative older-than with keep: %v", err)
	}
}

func TestPurgeBadDocumentID(t *testing.T) {
	f := newFixture(t)
	for _, apply := range []bool{false, true} {
		if _, err := f.docs.Purge(ctx, documents.PurgeOptions{Keep: ptr(0), DocumentID: "nope", Apply: apply}); !errors.Is(err, documents.ErrInvalid) {
			t.Errorf("apply %v malformed: %v", apply, err)
		}
		if _, err := f.docs.Purge(ctx, documents.PurgeOptions{Keep: ptr(0), DocumentID: "D-99", Apply: apply}); !errors.Is(err, documents.ErrNotFound) {
			t.Errorf("apply %v unknown: %v", apply, err)
		}
	}
}

func TestPurgeKeepOnly(t *testing.T) {
	f := newFixture(t)
	d := f.editN(t, f.create(t, documents.NewDocument{}), 5)
	res := purge(t, f, documents.PurgeOptions{Keep: ptr(2)})
	if got := entryVersions(res, d.ID); !equalInts(got, []int{1, 2, 3}) || res.Versions != 3 {
		t.Fatalf("result = %+v", res)
	}
}

func TestPurgeKeepZeroRemovesAllEarlier(t *testing.T) {
	f := newFixture(t)
	d := f.editN(t, f.create(t, documents.NewDocument{}), 3)
	res := purge(t, f, documents.PurgeOptions{Keep: ptr(0)})
	if got := entryVersions(res, d.ID); !equalInts(got, []int{1, 2, 3}) {
		t.Fatalf("result = %+v", res)
	}
}

func TestPurgeKeepMoreThanExist(t *testing.T) {
	f := newFixture(t)
	f.editN(t, f.create(t, documents.NewDocument{}), 2)
	if res := purge(t, f, documents.PurgeOptions{Keep: ptr(5)}); len(res.Entries) != 0 || res.Versions != 0 {
		t.Fatalf("result = %+v", res)
	}
}

func TestPurgeOlderThanOnly(t *testing.T) {
	f := newFixture(t)
	d := f.editN(t, f.create(t, documents.NewDocument{}), 4)
	now := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	f.versionAges(t, d.ID, now, 100, 50, 10, 1)
	f.setNow(now)
	res := purge(t, f, documents.PurgeOptions{OlderThan: 30 * 24 * time.Hour})
	if got := entryVersions(res, d.ID); !equalInts(got, []int{1, 2}) {
		t.Fatalf("result = %+v", res)
	}
}

func TestPurgeBothCriteria(t *testing.T) {
	f := newFixture(t)
	d := f.editN(t, f.create(t, documents.NewDocument{}), 5)
	now := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	f.versionAges(t, d.ID, now, 100, 90, 80, 2, 1)
	f.setNow(now)
	res := purge(t, f, documents.PurgeOptions{Keep: ptr(1), OlderThan: 30 * 24 * time.Hour})
	if got := entryVersions(res, d.ID); !equalInts(got, []int{1, 2, 3}) {
		t.Fatalf("result = %+v", res)
	}
	res = purge(t, f, documents.PurgeOptions{Keep: ptr(4), OlderThan: 30 * 24 * time.Hour})
	if got := entryVersions(res, d.ID); !equalInts(got, []int{1}) {
		t.Fatalf("keep 4 result = %+v", res)
	}
}

func TestPurgeOneDocument(t *testing.T) {
	f := newFixture(t)
	a := f.editN(t, f.create(t, documents.NewDocument{}), 2)
	b := f.editN(t, f.create(t, documents.NewDocument{}), 2)
	res := purge(t, f, documents.PurgeOptions{Keep: ptr(0), DocumentID: b.ID})
	if len(res.Entries) != 1 || res.Entries[0].ID != b.ID || entryVersions(res, a.ID) != nil {
		t.Fatalf("result = %+v", res)
	}
}

func TestPurgeNeverTouchesCurrent(t *testing.T) {
	f := newFixture(t)
	d := f.editN(t, f.create(t, documents.NewDocument{}), 3)
	purge(t, f, documents.PurgeOptions{Keep: ptr(0), Apply: true})
	got, err := f.docs.Get(ctx, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got != d {
		t.Errorf("document = %+v, want %+v", got, d)
	}
	v, err := f.docs.Version(ctx, d.ID, d.Version)
	if err != nil || !v.Current || v.Body != d.Body {
		t.Errorf("current version = %+v, %v", v, err)
	}
}

func TestPurgeNeverTouchesStatus(t *testing.T) {
	f := newFixture(t)
	d := f.editN(t, f.create(t, documents.NewDocument{}), 2)
	d, err := f.docs.Archive(ctx, d.ID, d.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	res := purge(t, f, documents.PurgeOptions{Keep: ptr(0), Apply: true})
	if got := entryVersions(res, d.ID); !equalInts(got, []int{1, 2}) {
		t.Fatalf("result = %+v", res)
	}
	got, _ := f.docs.Get(ctx, d.ID)
	if got.Status != documents.StatusArchived || got.Version != d.Version {
		t.Errorf("document = %+v", got)
	}
}

func TestPurgeDryRunChangesNothing(t *testing.T) {
	f := newFixture(t)
	d := f.editN(t, f.create(t, documents.NewDocument{}), 4)
	dry := purge(t, f, documents.PurgeOptions{Keep: ptr(1)})
	if got := versionNumbers(t, f, d.ID); !equalInts(got, []int{1, 2, 3, 4}) {
		t.Fatalf("versions after dry run = %v", got)
	}
	applied := purge(t, f, documents.PurgeOptions{Keep: ptr(1), Apply: true})
	if fmt.Sprint(dry) != fmt.Sprint(applied) {
		t.Errorf("dry %+v != applied %+v", dry, applied)
	}
}

func TestPurgeApplyRemoves(t *testing.T) {
	f := newFixture(t)
	d := f.editN(t, f.create(t, documents.NewDocument{}), 4)
	purge(t, f, documents.PurgeOptions{Keep: ptr(1), Apply: true})
	for _, n := range []int{1, 2, 3} {
		if _, err := f.docs.Version(ctx, d.ID, n); !errors.Is(err, documents.ErrNotFound) {
			t.Errorf("version %d: %v", n, err)
		}
	}
	for _, n := range []int{4, 5} {
		if _, err := f.docs.Version(ctx, d.ID, n); err != nil {
			t.Errorf("version %d: %v", n, err)
		}
	}
	if res := purge(t, f, documents.PurgeOptions{Keep: ptr(1), Apply: true}); len(res.Entries) != 0 {
		t.Errorf("second purge = %+v", res)
	}
}

func TestPurgeOrdersEntries(t *testing.T) {
	f := newFixture(t)
	var all []documents.Document
	for range 11 {
		all = append(all, f.editN(t, f.create(t, documents.NewDocument{}), 2))
	}
	res := purge(t, f, documents.PurgeOptions{Keep: ptr(0)})
	if len(res.Entries) != 11 {
		t.Fatalf("entries = %+v", res.Entries)
	}
	for i, e := range res.Entries {
		if e.ID != all[i].ID || !equalInts(e.Versions, []int{1, 2}) {
			t.Errorf("entry %d = %+v, want %s", i, e, all[i].ID)
		}
	}
}

func TestPurgeOmitsDocumentsWithNoCandidates(t *testing.T) {
	f := newFixture(t)
	f.create(t, documents.NewDocument{})
	b := f.editN(t, f.create(t, documents.NewDocument{}), 1)
	res := purge(t, f, documents.PurgeOptions{Keep: ptr(0)})
	if len(res.Entries) != 1 || res.Entries[0].ID != b.ID {
		t.Fatalf("result = %+v", res)
	}
}

func TestPurgeBytesCountBodies(t *testing.T) {
	f := newFixture(t)
	d := f.create(t, documents.NewDocument{Body: "héllo"})
	d = f.edit(t, d, "second body")
	f.edit(t, d, "third")
	res := purge(t, f, documents.PurgeOptions{Keep: ptr(0)})
	want := len("héllo") + len("second body")
	if res.Bytes != want || res.Entries[0].Bytes != want {
		t.Fatalf("bytes = %d, want %d (%+v)", res.Bytes, want, res)
	}
}

func TestPurgeApplyIsOneTransaction(t *testing.T) {
	f := newFixture(t)
	d := f.editN(t, f.create(t, documents.NewDocument{}), 4)
	if _, err := f.d.SQL().Exec("CREATE TRIGGER boom BEFORE DELETE ON document_versions WHEN OLD.version = 3 BEGIN SELECT RAISE(ABORT, 'boom'); END"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.docs.Purge(ctx, documents.PurgeOptions{Keep: ptr(0), Apply: true}); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v", err)
	}
	if got := versionNumbers(t, f, d.ID); !equalInts(got, []int{1, 2, 3, 4}) {
		t.Errorf("versions = %v", got)
	}
}

func TestPurgeConcurrentWithEdit(t *testing.T) {
	f := newFixture(t)
	d := f.editN(t, f.create(t, documents.NewDocument{}), 4)
	var wg sync.WaitGroup
	var editErr, purgeErr error
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, editErr = f.docs.Update(ctx, d.ID, d.Fingerprint, documents.Change{Body: ptr("concurrent edit")})
	}()
	go func() {
		defer wg.Done()
		_, purgeErr = f.docs.Purge(ctx, documents.PurgeOptions{Keep: ptr(1), Apply: true})
	}()
	wg.Wait()
	if editErr != nil || purgeErr != nil {
		t.Fatalf("edit %v, purge %v", editErr, purgeErr)
	}
	got, err := f.docs.Get(ctx, d.ID)
	if err != nil || got.Version != d.Version+1 || got.Body != "concurrent edit" {
		t.Fatalf("document = %+v, %v", got, err)
	}
	vs := versionNumbers(t, f, d.ID)
	if len(vs) == 0 || vs[len(vs)-1] != d.Version || len(vs) > 2 {
		t.Errorf("versions = %v", vs)
	}
	for _, n := range vs {
		if _, err := f.docs.Version(ctx, d.ID, n); err != nil {
			t.Errorf("version %d: %v", n, err)
		}
	}
}
