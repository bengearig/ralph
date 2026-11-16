package documents_test

import (
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/bengearig/ralph/internal/db"
	"github.com/bengearig/ralph/internal/documents"
	"github.com/bengearig/ralph/internal/units"
)

func (f fixture) get(t *testing.T, id string) documents.Document {
	t.Helper()
	d, err := f.docs.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func (f fixture) archived(t *testing.T) documents.Document {
	t.Helper()
	d := f.create(t, documents.NewDocument{})
	d, err := f.docs.Archive(ctx, d.ID, d.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func (f fixture) superseded(t *testing.T) documents.Document {
	t.Helper()
	d := f.create(t, documents.NewDocument{})
	r := f.create(t, documents.NewDocument{})
	d, err := f.docs.Supersede(ctx, d.ID, d.Fingerprint, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestArchiveActive(t *testing.T) {
	f := newFixture(t)
	d := f.create(t, documents.NewDocument{})
	actx := units.WithActor(ctx, "alice")
	got, err := f.docs.Archive(actx, d.ID, d.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != documents.StatusArchived {
		t.Errorf("status = %s", got.Status)
	}
	if got.Fingerprint == d.Fingerprint {
		t.Error("fingerprint unchanged")
	}
	if got.Version != d.Version {
		t.Errorf("version = %d, want %d", got.Version, d.Version)
	}
	if !got.UpdatedAt.After(d.UpdatedAt) || got.UpdatedBy != "alice" {
		t.Errorf("updated_at = %v, updated_by = %q", got.UpdatedAt, got.UpdatedBy)
	}
	if stored := f.get(t, d.ID); stored != got {
		t.Errorf("stored %+v != returned %+v", stored, got)
	}
}

func TestArchiveNeedsFingerprintAndIsStale(t *testing.T) {
	f := newFixture(t)
	d := f.create(t, documents.NewDocument{})
	if _, err := f.docs.Archive(ctx, d.ID, ""); err == nil {
		t.Error("empty fingerprint accepted")
	}
	_, err := f.docs.Archive(ctx, d.ID, "stale")
	if !errors.Is(err, db.ErrStale) {
		t.Errorf("err = %v", err)
	}
	if got := f.get(t, d.ID); got != d {
		t.Errorf("document changed: %+v", got)
	}
}

func TestArchiveNotActiveRejected(t *testing.T) {
	f := newFixture(t)
	for _, tc := range []struct {
		d       documents.Document
		restore bool
	}{{f.archived(t), false}, {f.superseded(t), true}} {
		_, err := f.docs.Archive(ctx, tc.d.ID, tc.d.Fingerprint)
		if !errors.Is(err, documents.ErrInvalid) || !strings.Contains(err.Error(), string(tc.d.Status)) {
			t.Errorf("%s: err = %v", tc.d.Status, err)
		}
		if tc.restore && !strings.Contains(err.Error(), "restore") {
			t.Errorf("%s: message does not say to restore: %v", tc.d.Status, err)
		}
	}
}

func TestArchiveBadAndMissingID(t *testing.T) {
	f := newFixture(t)
	if _, err := f.docs.Archive(ctx, "T-1", "x"); !errors.Is(err, documents.ErrInvalid) {
		t.Errorf("bad id: %v", err)
	}
	if _, err := f.docs.Archive(ctx, "D-99", "x"); !errors.Is(err, documents.ErrNotFound) {
		t.Errorf("missing: %v", err)
	}
}

func TestSupersedeActive(t *testing.T) {
	f := newFixture(t)
	d := f.create(t, documents.NewDocument{})
	r := f.create(t, documents.NewDocument{})
	actx := units.WithActor(ctx, "bob")
	got, err := f.docs.Supersede(actx, d.ID, d.Fingerprint, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != documents.StatusSuperseded || got.SupersededBy != r.ID {
		t.Errorf("status = %s, superseded by %q", got.Status, got.SupersededBy)
	}
	if got.Fingerprint == d.Fingerprint || got.Version != d.Version || got.UpdatedBy != "bob" || !got.UpdatedAt.After(d.UpdatedAt) {
		t.Errorf("got %+v", got)
	}
	if after := f.get(t, r.ID); after != r {
		t.Errorf("replacement changed: %+v", after)
	}
}

func TestSupersedeNeedsFingerprintAndIsStale(t *testing.T) {
	f := newFixture(t)
	d := f.create(t, documents.NewDocument{})
	r := f.create(t, documents.NewDocument{})
	if _, err := f.docs.Supersede(ctx, d.ID, "", r.ID); err == nil {
		t.Error("empty fingerprint accepted")
	}
	if _, err := f.docs.Supersede(ctx, d.ID, "stale", r.ID); !errors.Is(err, db.ErrStale) {
		t.Errorf("err = %v", err)
	}
	if got := f.get(t, d.ID); got != d {
		t.Errorf("document changed: %+v", got)
	}
}

func TestSupersedeRequiresReplacement(t *testing.T) {
	f := newFixture(t)
	d := f.create(t, documents.NewDocument{})
	_, err := f.docs.Supersede(ctx, d.ID, d.Fingerprint, "")
	if !errors.Is(err, documents.ErrInvalid) || !strings.Contains(err.Error(), "replacement is required") {
		t.Errorf("err = %v", err)
	}
	if got := f.get(t, d.ID); got != d {
		t.Errorf("document changed: %+v", got)
	}
}

func TestSupersedeReplacementMustExist(t *testing.T) {
	f := newFixture(t)
	d := f.create(t, documents.NewDocument{})
	_, err := f.docs.Supersede(ctx, d.ID, d.Fingerprint, "D-99")
	if !errors.Is(err, documents.ErrNotFound) {
		t.Errorf("err = %v", err)
	}
	if got := f.get(t, d.ID); got != d {
		t.Errorf("document changed: %+v", got)
	}
}

func TestSupersedeSelfRejected(t *testing.T) {
	f := newFixture(t)
	d := f.create(t, documents.NewDocument{})
	_, err := f.docs.Supersede(ctx, d.ID, d.Fingerprint, d.ID)
	if !errors.Is(err, documents.ErrInvalid) {
		t.Errorf("err = %v", err)
	}
	if got := f.get(t, d.ID); got != d {
		t.Errorf("document changed: %+v", got)
	}
}

func TestSupersedeReplacementMustBeActive(t *testing.T) {
	f := newFixture(t)
	for _, r := range []documents.Document{f.archived(t), f.superseded(t)} {
		d := f.create(t, documents.NewDocument{})
		_, err := f.docs.Supersede(ctx, d.ID, d.Fingerprint, r.ID)
		if !errors.Is(err, documents.ErrInvalid) || !strings.Contains(err.Error(), string(r.Status)) {
			t.Errorf("%s replacement: err = %v", r.Status, err)
		}
		if got := f.get(t, d.ID); got != d {
			t.Errorf("document changed: %+v", got)
		}
	}
}

func TestSupersedeBadReplacementID(t *testing.T) {
	f := newFixture(t)
	d := f.create(t, documents.NewDocument{})
	for _, bad := range []string{"T-1", "d-2"} {
		if _, err := f.docs.Supersede(ctx, d.ID, d.Fingerprint, bad); !errors.Is(err, documents.ErrInvalid) {
			t.Errorf("%q: err = %v", bad, err)
		}
	}
}

func TestSupersedeOnlyFromActive(t *testing.T) {
	f := newFixture(t)
	r := f.create(t, documents.NewDocument{})
	for _, d := range []documents.Document{f.archived(t), f.superseded(t)} {
		_, err := f.docs.Supersede(ctx, d.ID, d.Fingerprint, r.ID)
		if !errors.Is(err, documents.ErrInvalid) || !strings.Contains(err.Error(), string(d.Status)) {
			t.Errorf("%s: err = %v", d.Status, err)
		}
		if got := f.get(t, d.ID); got != d {
			t.Errorf("%s document changed: %+v", d.Status, got)
		}
	}
}

func TestRestoreFromArchived(t *testing.T) {
	f := newFixture(t)
	d := f.archived(t)
	got, err := f.docs.Restore(units.WithActor(ctx, "carol"), d.ID, d.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != documents.StatusActive || got.SupersededBy != "" {
		t.Errorf("got %+v", got)
	}
	if got.Fingerprint == d.Fingerprint || got.Version != d.Version || got.UpdatedBy != "carol" || !got.UpdatedAt.After(d.UpdatedAt) {
		t.Errorf("got %+v", got)
	}
}

func TestRestoreFromSuperseded(t *testing.T) {
	f := newFixture(t)
	d := f.superseded(t)
	if d.SupersededBy == "" {
		t.Fatal("no pointer to clear")
	}
	got, err := f.docs.Restore(ctx, d.ID, d.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != documents.StatusActive || got.SupersededBy != "" {
		t.Errorf("got %+v", got)
	}
	if stored := f.get(t, d.ID); stored != got {
		t.Errorf("stored %+v != returned %+v", stored, got)
	}
}

func TestRestoreNeedsFingerprintAndIsStale(t *testing.T) {
	f := newFixture(t)
	d := f.archived(t)
	if _, err := f.docs.Restore(ctx, d.ID, ""); err == nil {
		t.Error("empty fingerprint accepted")
	}
	if _, err := f.docs.Restore(ctx, d.ID, "stale"); !errors.Is(err, db.ErrStale) {
		t.Errorf("err = %v", err)
	}
	if got := f.get(t, d.ID); got != d {
		t.Errorf("document changed: %+v", got)
	}
}

func TestRestoreActiveRejected(t *testing.T) {
	f := newFixture(t)
	d := f.create(t, documents.NewDocument{})
	_, err := f.docs.Restore(ctx, d.ID, d.Fingerprint)
	if !errors.Is(err, documents.ErrInvalid) || !strings.Contains(err.Error(), "already active") {
		t.Errorf("err = %v", err)
	}
}

func TestRestoreThenEditWorks(t *testing.T) {
	f := newFixture(t)
	d := f.archived(t)
	if _, err := f.docs.Update(ctx, d.ID, d.Fingerprint, documents.Change{Title: ptr("new")}); !errors.Is(err, documents.ErrInvalid) {
		t.Fatalf("update of archived: %v", err)
	}
	d, err := f.docs.Restore(ctx, d.ID, d.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	got, err := f.docs.Update(ctx, d.ID, d.Fingerprint, documents.Change{Title: ptr("new")})
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "new" || got.Version != d.Version+1 {
		t.Errorf("got %+v", got)
	}
}

func TestUpdateInactiveMentionsRestore(t *testing.T) {
	f := newFixture(t)
	for _, d := range []documents.Document{f.archived(t), f.superseded(t)} {
		_, err := f.docs.Update(ctx, d.ID, d.Fingerprint, documents.Change{Title: ptr("x")})
		if !errors.Is(err, documents.ErrInvalid) || !strings.Contains(err.Error(), "restore it first") || !strings.Contains(err.Error(), string(d.Status)) {
			t.Errorf("%s: err = %v", d.Status, err)
		}
	}
}

func TestReplacementArchivedLaterKeepsPointer(t *testing.T) {
	f := newFixture(t)
	d := f.create(t, documents.NewDocument{})
	r := f.create(t, documents.NewDocument{})
	if _, err := f.docs.Supersede(ctx, d.ID, d.Fingerprint, r.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.docs.Archive(ctx, r.ID, r.Fingerprint); err != nil {
		t.Fatal(err)
	}
	got := f.get(t, d.ID)
	if got.Status != documents.StatusSuperseded || got.SupersededBy != r.ID {
		t.Errorf("got %+v", got)
	}
}

func TestSupersedeChain(t *testing.T) {
	f := newFixture(t)
	a := f.create(t, documents.NewDocument{})
	b := f.create(t, documents.NewDocument{})
	c := f.create(t, documents.NewDocument{})
	if _, err := f.docs.Supersede(ctx, a.ID, a.Fingerprint, b.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.docs.Supersede(ctx, b.ID, b.Fingerprint, c.ID); err != nil {
		t.Fatal(err)
	}
	if got := f.get(t, a.ID); got.Status != documents.StatusSuperseded || got.SupersededBy != b.ID {
		t.Errorf("a = %+v", got)
	}
	if got := f.get(t, b.ID); got.SupersededBy != c.ID {
		t.Errorf("b = %+v", got)
	}
}

func TestListStatuses(t *testing.T) {
	f := newFixture(t)
	active := f.create(t, documents.NewDocument{})
	archived := f.archived(t)
	sup := f.superseded(t)
	repl, err := f.docs.Get(ctx, sup.SupersededBy)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name     string
		statuses []documents.Status
		want     []string
	}{
		{"default", nil, []string{active.ID, repl.ID}},
		{"archived", []documents.Status{documents.StatusArchived}, []string{archived.ID}},
		{"superseded", []documents.Status{documents.StatusSuperseded}, []string{sup.ID}},
		{"all", []documents.Status{documents.StatusActive, documents.StatusArchived, documents.StatusSuperseded},
			[]string{active.ID, archived.ID, sup.ID, repl.ID}},
	} {
		got, err := f.docs.List(ctx, documents.Filter{Statuses: tc.statuses})
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		g := ids(got)
		slices.Sort(g)
		slices.Sort(tc.want)
		if !slices.Equal(g, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, g, tc.want)
		}
	}
}

func TestStatusChangesAreNotVersions(t *testing.T) {
	f := newFixture(t)
	d := f.create(t, documents.NewDocument{})
	r := f.create(t, documents.NewDocument{})
	d, err := f.docs.Archive(ctx, d.ID, d.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	d, err = f.docs.Restore(ctx, d.ID, d.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	d, err = f.docs.Supersede(ctx, d.ID, d.Fingerprint, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if d.Version != 1 {
		t.Errorf("version = %d", d.Version)
	}
}

func TestSupersedeConcurrentWithArchiveOfReplacement(t *testing.T) {
	for range 20 {
		f := newFixture(t)
		d := f.create(t, documents.NewDocument{})
		r := f.create(t, documents.NewDocument{})
		var supErr, archErr error
		var wg sync.WaitGroup
		start := make(chan struct{})
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			_, supErr = f.docs.Supersede(ctx, d.ID, d.Fingerprint, r.ID)
		}()
		go func() {
			defer wg.Done()
			<-start
			_, archErr = f.docs.Archive(ctx, r.ID, r.Fingerprint)
		}()
		close(start)
		wg.Wait()

		if archErr != nil {
			t.Fatalf("archive: %v", archErr)
		}
		gotD, gotR := f.get(t, d.ID), f.get(t, r.ID)
		switch {
		case supErr == nil:
			if gotD.Status != documents.StatusSuperseded || gotD.SupersededBy != r.ID {
				t.Fatalf("supersede succeeded but document is %+v", gotD)
			}
			if !gotD.UpdatedAt.Before(gotR.UpdatedAt) {
				t.Fatalf("superseded after the replacement was archived: %v vs %v", gotD.UpdatedAt, gotR.UpdatedAt)
			}
		case errors.Is(supErr, documents.ErrInvalid):
			if gotD.Status != documents.StatusActive || gotD.SupersededBy != "" {
				t.Fatalf("supersede failed but document is %+v", gotD)
			}
		default:
			t.Fatalf("supersede: %v", supErr)
		}
	}
}
