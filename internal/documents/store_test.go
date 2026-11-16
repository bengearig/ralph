package documents_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bengearig/ralph/internal/db"
	"github.com/bengearig/ralph/internal/documents"
	"github.com/bengearig/ralph/internal/testutil"
	"github.com/bengearig/ralph/internal/units"
)

type fixture struct {
	d     *db.DB
	docs  *documents.Store
	units *units.Store
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	d := testutil.NewDB(t, db.Migrations)
	f := fixture{d: d, docs: documents.New(d), units: units.New(d)}
	base := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	var mu sync.Mutex
	f.docs.Now = func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		base = base.Add(time.Second)
		return base
	}
	return f
}

var ctx = context.Background()

func (f fixture) create(t *testing.T, n documents.NewDocument) documents.Document {
	t.Helper()
	if n.Kind == "" {
		n.Kind = documents.KindDesign
	}
	if n.Title == "" {
		n.Title = "title"
	}
	if n.Body == "" {
		n.Body = "body"
	}
	doc, err := f.docs.Create(ctx, n)
	if err != nil {
		t.Fatalf("create %+v: %v", n, err)
	}
	return doc
}

func (f fixture) unit(t *testing.T, n units.NewUnit) units.Unit {
	t.Helper()
	u, err := f.units.Create(ctx, n)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func (f fixture) brief(t *testing.T) units.Unit {
	return f.unit(t, units.NewUnit{Level: units.LevelBrief, Title: "b", Body: "do it"})
}

func (f fixture) setStatus(t *testing.T, id, status string) {
	t.Helper()
	q := "UPDATE documents SET status = ? WHERE id = ?"
	if status == "SUPERSEDED" {
		q = "UPDATE documents SET status = ?, superseded_by = 'D-1' WHERE id = ?"
	}
	if _, err := f.d.SQL().Exec(q, status, id); err != nil {
		t.Fatal(err)
	}
}

func ptr[T any](v T) *T { return &v }

func ids(docs []documents.Document) []string {
	out := make([]string, len(docs))
	for i, d := range docs {
		out[i] = d.ID
	}
	return out
}

func TestCreateProjectWide(t *testing.T) {
	f := newFixture(t)
	d := f.create(t, documents.NewDocument{Kind: documents.KindArchitecture, Title: "  Layout  ", Body: "text"})
	if d.BriefID != "" || d.Kind != documents.KindArchitecture || d.Title != "Layout" || d.Body != "text" || d.Number != 1 {
		t.Errorf("document = %+v", d)
	}
}

func TestCreateTiedToBrief(t *testing.T) {
	f := newFixture(t)
	b := f.brief(t)
	d := f.create(t, documents.NewDocument{BriefID: b.ID})
	if d.BriefID != b.ID {
		t.Errorf("brief = %q, want %q", d.BriefID, b.ID)
	}
}

func TestCreateBriefMustBeBrief(t *testing.T) {
	f := newFixture(t)
	b := f.brief(t)
	s := f.unit(t, units.NewUnit{Level: units.LevelSlice, ParentID: b.ID, Title: "s"})
	tk := f.unit(t, units.NewUnit{Level: units.LevelTask, ParentID: s.ID, Title: "t"})
	for _, id := range []string{s.ID, tk.ID} {
		_, err := f.docs.Create(ctx, documents.NewDocument{Kind: documents.KindDesign, BriefID: id, Title: "t", Body: "b"})
		if !errors.Is(err, documents.ErrInvalid) {
			t.Errorf("%s: err = %v", id, err)
		}
	}
	_, err := f.docs.Create(ctx, documents.NewDocument{Kind: documents.KindDesign, BriefID: "B-99", Title: "t", Body: "b"})
	if !errors.Is(err, documents.ErrNotFound) {
		t.Errorf("unknown: err = %v", err)
	}
	if docs, _ := f.docs.List(ctx, documents.Filter{}); len(docs) != 0 {
		t.Errorf("documents created despite errors: %v", ids(docs))
	}
}

func TestCreateValidation(t *testing.T) {
	f := newFixture(t)
	cases := []struct {
		name string
		in   documents.NewDocument
		ok   bool
	}{
		{"bad kind", documents.NewDocument{Kind: "notes", Title: "t", Body: "b"}, false},
		{"no kind", documents.NewDocument{Title: "t", Body: "b"}, false},
		{"empty title", documents.NewDocument{Kind: "design", Title: "", Body: "b"}, false},
		{"whitespace title", documents.NewDocument{Kind: "design", Title: " \t ", Body: "b"}, false},
		{"newline in title", documents.NewDocument{Kind: "design", Title: "a\nb", Body: "b"}, false},
		{"carriage return in title", documents.NewDocument{Kind: "design", Title: "a\rb", Body: "b"}, false},
		{"201 characters", documents.NewDocument{Kind: "design", Title: strings.Repeat("a", 201), Body: "b"}, false},
		{"200 characters", documents.NewDocument{Kind: "design", Title: strings.Repeat("a", 200), Body: "b"}, true},
		{"200 multibyte characters", documents.NewDocument{Kind: "design", Title: strings.Repeat("é", 200), Body: "b"}, true},
		{"blank body", documents.NewDocument{Kind: "design", Title: "t", Body: " \n\t"}, false},
		{"empty body", documents.NewDocument{Kind: "design", Title: "t", Body: ""}, false},
		{"1,000,000 bytes", documents.NewDocument{Kind: "design", Title: "t", Body: strings.Repeat("a", 1_000_000)}, true},
		{"1,000,001 bytes", documents.NewDocument{Kind: "design", Title: "t", Body: strings.Repeat("a", 1_000_001)}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := f.docs.Create(ctx, c.in)
			if c.ok && err != nil {
				t.Errorf("err = %v", err)
			}
			if !c.ok && !errors.Is(err, documents.ErrInvalid) {
				t.Errorf("err = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestCreateSetsVersionOneAndActive(t *testing.T) {
	f := newFixture(t)
	d := f.create(t, documents.NewDocument{})
	if d.Version != 1 || d.Status != documents.StatusActive || d.SupersededBy != "" {
		t.Errorf("document = %+v", d)
	}
	if d.CreatedAt.IsZero() || !d.CreatedAt.Equal(d.UpdatedAt) {
		t.Errorf("times = %v, %v", d.CreatedAt, d.UpdatedAt)
	}
}

func TestIDsAreSequential(t *testing.T) {
	f := newFixture(t)
	for i, want := range []string{"D-1", "D-2", "D-3"} {
		if d := f.create(t, documents.NewDocument{}); d.ID != want || d.Number != i+1 {
			t.Errorf("got %s (%d), want %s", d.ID, d.Number, want)
		}
	}
}

func TestCounterSeparateFromUnits(t *testing.T) {
	f := newFixture(t)
	f.create(t, documents.NewDocument{})
	f.create(t, documents.NewDocument{})
	b := f.brief(t)
	s := f.unit(t, units.NewUnit{Level: units.LevelSlice, ParentID: b.ID, Title: "s"})
	tk := f.unit(t, units.NewUnit{Level: units.LevelTask, ParentID: s.ID, Title: "t"})
	if b.ID != "B-1" || s.ID != "S-1" || tk.ID != "T-1" {
		t.Errorf("unit IDs = %s %s %s", b.ID, s.ID, tk.ID)
	}
	if d := f.create(t, documents.NewDocument{}); d.ID != "D-3" {
		t.Errorf("document ID = %s", d.ID)
	}
}

func TestGetReturnsStoredFields(t *testing.T) {
	f := newFixture(t)
	b := f.brief(t)
	ctx := units.WithActor(ctx, "ann")
	made, err := f.docs.Create(ctx, documents.NewDocument{Kind: documents.KindDecision, BriefID: b.ID, Title: "T", Body: "line\n\nmore"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := f.docs.Get(ctx, made.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got != made {
		t.Errorf("get = %+v, create = %+v", got, made)
	}
	if got.Kind != documents.KindDecision || got.BriefID != b.ID || got.Title != "T" || got.Body != "line\n\nmore" || got.CreatedBy != "ann" || got.Fingerprint == "" {
		t.Errorf("document = %+v", got)
	}
}

func TestGetNotFound(t *testing.T) {
	f := newFixture(t)
	if _, err := f.docs.Get(ctx, "D-7"); !errors.Is(err, documents.ErrNotFound) || !errors.Is(err, db.ErrNotFound) {
		t.Errorf("err = %v", err)
	}
}

func TestGetBadID(t *testing.T) {
	f := newFixture(t)
	for _, id := range []string{"d-1", "D1", "D-", "D-x", "D-1x", "D- 1", "T-1", ""} {
		if _, err := f.docs.Get(ctx, id); !errors.Is(err, documents.ErrInvalid) {
			t.Errorf("Get(%q) err = %v", id, err)
		}
		if _, err := f.docs.Update(ctx, id, "fp", documents.Change{}); !errors.Is(err, documents.ErrInvalid) {
			t.Errorf("Update(%q) err = %v", id, err)
		}
	}
}

func TestListOrderedByNumber(t *testing.T) {
	f := newFixture(t)
	for _, title := range []string{"c", "a", "b"} {
		f.create(t, documents.NewDocument{Title: title})
	}
	docs, err := f.docs.List(ctx, documents.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(ids(docs), ","); got != "D-1,D-2,D-3" {
		t.Errorf("order = %s", got)
	}
}

func TestListFilterByBrief(t *testing.T) {
	f := newFixture(t)
	b := f.brief(t)
	f.create(t, documents.NewDocument{})
	f.create(t, documents.NewDocument{BriefID: b.ID})
	cases := []struct {
		name string
		by   *string
		want string
	}{
		{"nil", nil, "D-1,D-2"},
		{"brief", ptr(b.ID), "D-2"},
		{"project-wide", ptr(""), "D-1"},
		{"other brief", ptr("B-99"), ""},
	}
	for _, c := range cases {
		docs, err := f.docs.List(ctx, documents.Filter{BriefID: c.by})
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got := strings.Join(ids(docs), ","); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestListFilterByKind(t *testing.T) {
	f := newFixture(t)
	f.create(t, documents.NewDocument{Kind: documents.KindArchitecture})
	f.create(t, documents.NewDocument{Kind: documents.KindDesign})
	f.create(t, documents.NewDocument{Kind: documents.KindDecision})
	docs, err := f.docs.List(ctx, documents.Filter{Kinds: []documents.Kind{documents.KindDesign, documents.KindDecision}})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(ids(docs), ","); got != "D-2,D-3" {
		t.Errorf("got %s", got)
	}
	if _, err := f.docs.List(ctx, documents.Filter{Kinds: []documents.Kind{"notes"}}); !errors.Is(err, documents.ErrInvalid) {
		t.Errorf("bad kind: err = %v", err)
	}
}

func TestListDefaultsToActiveOnly(t *testing.T) {
	f := newFixture(t)
	for range 3 {
		f.create(t, documents.NewDocument{})
	}
	f.setStatus(t, "D-2", "ARCHIVED")
	f.setStatus(t, "D-3", "SUPERSEDED")
	docs, err := f.docs.List(ctx, documents.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(ids(docs), ","); got != "D-1" {
		t.Errorf("default = %s", got)
	}
	docs, err = f.docs.List(ctx, documents.Filter{Statuses: []documents.Status{documents.StatusArchived, documents.StatusSuperseded}})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(ids(docs), ","); got != "D-2,D-3" {
		t.Errorf("explicit = %s", got)
	}
	if _, err := f.docs.List(ctx, documents.Filter{Statuses: []documents.Status{"GONE"}}); !errors.Is(err, documents.ErrInvalid) {
		t.Errorf("bad status: err = %v", err)
	}
}

func TestFingerprintChangesWithEveryField(t *testing.T) {
	f := newFixture(t)
	d := f.create(t, documents.NewDocument{})
	again, err := f.docs.Get(ctx, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if again.Fingerprint != d.Fingerprint {
		t.Error("fingerprint differs across reads")
	}
	seen := map[string]string{d.Fingerprint: "original"}
	changes := map[string]documents.Change{
		"title": {Title: ptr("other")},
		"body":  {Body: ptr("other")},
		"kind":  {Kind: ptr(documents.KindReference)},
	}
	for name, c := range changes {
		got, err := f.docs.Update(ctx, d.ID, d.Fingerprint, c)
		if err != nil {
			t.Fatal(err)
		}
		if prev, dup := seen[got.Fingerprint]; dup {
			t.Errorf("%s gives the fingerprint of %s", name, prev)
		}
		seen[got.Fingerprint] = name
		// Restore the original content for the next field.
		if _, err := f.d.SQL().Exec("UPDATE documents SET title = ?, body = ?, kind = ? WHERE id = ?", d.Title, d.Body, string(d.Kind), d.ID); err != nil {
			t.Fatal(err)
		}
	}
}

func TestUpdateChangesFields(t *testing.T) {
	cases := map[string]documents.Change{
		"title":    {Title: ptr("new title")},
		"body":     {Body: ptr("new body")},
		"kind":     {Kind: ptr(documents.KindDecision)},
		"together": {Title: ptr("new title"), Body: ptr("new body"), Kind: ptr(documents.KindDecision)},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			created, err := f.docs.Create(units.WithActor(ctx, "ann"), documents.NewDocument{Kind: documents.KindDesign, Title: "t", Body: "b"})
			if err != nil {
				t.Fatal(err)
			}
			got, err := f.docs.Update(units.WithActor(ctx, "bob"), created.ID, created.Fingerprint, c)
			if err != nil {
				t.Fatal(err)
			}
			want := created
			if c.Title != nil {
				want.Title = *c.Title
			}
			if c.Body != nil {
				want.Body = *c.Body
			}
			if c.Kind != nil {
				want.Kind = *c.Kind
			}
			if got.Title != want.Title || got.Body != want.Body || got.Kind != want.Kind {
				t.Errorf("content = %q %q %q", got.Title, got.Body, got.Kind)
			}
			if got.Version != created.Version+1 {
				t.Errorf("version = %d, want %d", got.Version, created.Version+1)
			}
			if !got.UpdatedAt.After(created.UpdatedAt) || got.UpdatedBy != "bob" {
				t.Errorf("updated = %v by %q", got.UpdatedAt, got.UpdatedBy)
			}
			if !got.CreatedAt.Equal(created.CreatedAt) || got.CreatedBy != "ann" || got.Number != created.Number {
				t.Errorf("creation data changed: %+v", got)
			}
			if got.Fingerprint == created.Fingerprint {
				t.Error("fingerprint unchanged")
			}
		})
	}
}

func TestUpdateNeedsFingerprint(t *testing.T) {
	f := newFixture(t)
	d := f.create(t, documents.NewDocument{})
	if _, err := f.docs.Update(ctx, d.ID, "", documents.Change{Title: ptr("x")}); err == nil {
		t.Fatal("expected error")
	}
	if got, _ := f.docs.Get(ctx, d.ID); got != d {
		t.Error("document changed")
	}
}

func TestUpdateStale(t *testing.T) {
	f := newFixture(t)
	d := f.create(t, documents.NewDocument{})
	other, err := f.docs.Update(ctx, d.ID, d.Fingerprint, documents.Change{Title: ptr("theirs")})
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.docs.Update(ctx, d.ID, d.Fingerprint, documents.Change{Title: ptr("mine")})
	if !errors.Is(err, db.ErrStale) {
		t.Fatalf("err = %v", err)
	}
	if got, _ := f.docs.Get(ctx, d.ID); got != other {
		t.Errorf("document = %+v, want %+v", got, other)
	}
}

func TestUpdateNothingChangedKeepsEverything(t *testing.T) {
	f := newFixture(t)
	d := f.create(t, documents.NewDocument{Kind: documents.KindDesign, Title: "t", Body: "b"})
	for name, c := range map[string]documents.Change{
		"empty":              {},
		"same values":        {Title: ptr("t"), Body: ptr("b"), Kind: ptr(documents.KindDesign)},
		"same title, padded": {Title: ptr(" t ")},
	} {
		got, err := f.docs.Update(units.WithActor(ctx, "bob"), d.ID, d.Fingerprint, c)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got != d {
			t.Errorf("%s: got %+v, want %+v", name, got, d)
		}
	}
}

func TestUpdateOnlyActive(t *testing.T) {
	f := newFixture(t)
	f.create(t, documents.NewDocument{})
	for _, status := range []string{"ARCHIVED", "SUPERSEDED"} {
		d := f.create(t, documents.NewDocument{})
		f.setStatus(t, d.ID, status)
		d, err := f.docs.Get(ctx, d.ID)
		if err != nil {
			t.Fatal(err)
		}
		_, err = f.docs.Update(ctx, d.ID, d.Fingerprint, documents.Change{Title: ptr("x")})
		if !errors.Is(err, documents.ErrInvalid) || !strings.Contains(err.Error(), "restore") {
			t.Errorf("%s: err = %v", status, err)
		}
		_, err = f.docs.Update(ctx, d.ID, d.Fingerprint, documents.Change{})
		if !errors.Is(err, documents.ErrInvalid) {
			t.Errorf("%s, no change: err = %v", status, err)
		}
	}
}

func TestUpdateValidation(t *testing.T) {
	f := newFixture(t)
	d := f.create(t, documents.NewDocument{})
	cases := map[string]documents.Change{
		"bad kind":         {Kind: ptr(documents.Kind("notes"))},
		"empty title":      {Title: ptr("")},
		"whitespace title": {Title: ptr("  ")},
		"newline title":    {Title: ptr("a\nb")},
		"long title":       {Title: ptr(strings.Repeat("a", 201))},
		"blank body":       {Body: ptr(" \n")},
		"long body":        {Body: ptr(strings.Repeat("a", 1_000_001))},
	}
	for name, c := range cases {
		if _, err := f.docs.Update(ctx, d.ID, d.Fingerprint, c); !errors.Is(err, documents.ErrInvalid) {
			t.Errorf("%s: err = %v", name, err)
		}
		if got, _ := f.docs.Get(ctx, d.ID); got != d {
			t.Errorf("%s: document changed", name)
		}
	}
	long := strings.Repeat("a", 200)
	if _, err := f.docs.Update(ctx, d.ID, d.Fingerprint, documents.Change{Title: &long, Body: ptr(strings.Repeat("a", 1_000_000))}); err != nil {
		t.Errorf("limits: %v", err)
	}
}

func TestUpdateNotFound(t *testing.T) {
	f := newFixture(t)
	if _, err := f.docs.Update(ctx, "D-5", "fp", documents.Change{}); !errors.Is(err, documents.ErrNotFound) {
		t.Errorf("err = %v", err)
	}
}

func TestActorRecorded(t *testing.T) {
	f := newFixture(t)
	d, err := f.docs.Create(units.WithActor(ctx, "ann"), documents.NewDocument{Kind: documents.KindDesign, Title: "t", Body: "b"})
	if err != nil {
		t.Fatal(err)
	}
	if d.CreatedBy != "ann" || d.UpdatedBy != "ann" {
		t.Errorf("create: %q %q", d.CreatedBy, d.UpdatedBy)
	}
	u, err := f.docs.Update(units.WithActor(ctx, "bob"), d.ID, d.Fingerprint, documents.Change{Body: ptr("c")})
	if err != nil {
		t.Fatal(err)
	}
	if u.CreatedBy != "ann" || u.UpdatedBy != "bob" {
		t.Errorf("update: %q %q", u.CreatedBy, u.UpdatedBy)
	}
	if n := f.create(t, documents.NewDocument{}); n.CreatedBy != "" {
		t.Errorf("no actor: %q", n.CreatedBy)
	}
}

func TestUpdateConcurrentWritersOneStale(t *testing.T) {
	f := newFixture(t)
	d := f.create(t, documents.NewDocument{})
	errs := make([]error, 2)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, errs[i] = f.docs.Update(ctx, d.ID, d.Fingerprint, documents.Change{Title: ptr([]string{"one", "two"}[i])})
		}()
	}
	close(start)
	wg.Wait()
	ok, stale := 0, 0
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
		t.Errorf("succeeded = %d, stale = %d", ok, stale)
	}
	if got, _ := f.docs.Get(ctx, d.ID); got.Version != 2 {
		t.Errorf("version = %d", got.Version)
	}
}

func TestNewServiceIsLocalStore(t *testing.T) {
	d := testutil.NewDB(t, db.Migrations)
	svc := documents.NewService(d)
	if _, err := svc.Create(ctx, documents.NewDocument{Kind: documents.KindReference, Title: "t", Body: "b"}); err != nil {
		t.Fatal(err)
	}
}
