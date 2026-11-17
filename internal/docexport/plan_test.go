package docexport

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/bengearig/ralph/internal/db"
	"github.com/bengearig/ralph/internal/documents"
	"github.com/bengearig/ralph/internal/testutil"
	"github.com/bengearig/ralph/internal/units"
)

var ctx = context.Background()

type fixture struct {
	docs  documents.Service
	units units.Service
	dir   string
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	d := testutil.NewDB(t, db.Migrations)
	return fixture{docs: documents.NewService(d), units: units.NewService(d), dir: filepath.Join(t.TempDir(), "export")}
}

func (f fixture) doc(t *testing.T, title, body, briefID string) documents.Document {
	t.Helper()
	d, err := f.docs.Create(ctx, documents.NewDocument{Kind: documents.KindDesign, BriefID: briefID, Title: title, Body: body})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func (f fixture) brief(t *testing.T, title string) units.Unit {
	t.Helper()
	u, err := f.units.Create(ctx, units.NewUnit{Level: units.LevelBrief, Title: title, Body: "b"})
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func (f fixture) plan(t *testing.T) Plan {
	t.Helper()
	p, err := BuildPlan(ctx, f.docs, f.units, f.dir)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func (f fixture) apply(t *testing.T) Result {
	t.Helper()
	r, err := f.plan(t).Apply()
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func (f fixture) update(t *testing.T, id string, c documents.Change) {
	t.Helper()
	cur, err := f.docs.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.docs.Update(ctx, id, cur.Fingerprint, c); err != nil {
		t.Fatal(err)
	}
}

func (f fixture) write(t *testing.T, name, content string) string {
	t.Helper()
	if err := os.MkdirAll(f.dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(f.dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func actions(p Plan) map[string]Action {
	m := map[string]Action{}
	for _, c := range p.Changes {
		m[c.Name] = c.Action
	}
	return m
}

func wantActions(t *testing.T, p Plan, want map[string]Action) {
	t.Helper()
	if got := actions(p); !reflect.DeepEqual(got, want) {
		t.Fatalf("actions = %v, want %v", got, want)
	}
}

func ptr[T any](v T) *T { return &v }

func TestPlanEmptyProject(t *testing.T) {
	f := newFixture(t)
	p := f.plan(t)
	if len(p.Changes) != 0 || p.Dir != f.dir {
		t.Fatalf("plan = %+v", p)
	}
}

func TestPlanCreatesFilesForActiveDocuments(t *testing.T) {
	f := newFixture(t)
	b := f.brief(t, "Brief title")
	wide := f.doc(t, "Wide doc", "wide body", "")
	tied := f.doc(t, "Tied doc", "tied body", b.ID)
	p := f.plan(t)
	wantActions(t, p, map[string]Action{"D-1-wide-doc.md": ActionCreated, "D-2-tied-doc.md": ActionCreated})
	for _, c := range p.Changes {
		if c.Action != ActionCreated {
			t.Fatalf("change %+v", c)
		}
	}
	if !bytes.Equal(p.contents["D-1-wide-doc.md"], Render(Input{ID: wide.ID, Version: 1, Kind: wide.Kind, Title: "Wide doc", Body: "wide body"})) {
		t.Fatal("wide content differs from Render")
	}
	if !bytes.Equal(p.contents["D-2-tied-doc.md"], Render(Input{ID: tied.ID, Version: 1, Kind: tied.Kind, Title: "Tied doc", Body: "tied body", BriefID: b.ID, BriefTitle: "Brief title"})) {
		t.Fatal("tied content differs from Render")
	}
}

func TestPlanSkipsArchivedAndSuperseded(t *testing.T) {
	f := newFixture(t)
	keep := f.doc(t, "Keep", "b", "")
	arch := f.doc(t, "Arch", "b", "")
	sup := f.doc(t, "Sup", "b", "")
	if _, err := f.docs.Archive(ctx, arch.ID, arch.Fingerprint); err != nil {
		t.Fatal(err)
	}
	if _, err := f.docs.Supersede(ctx, sup.ID, sup.Fingerprint, keep.ID); err != nil {
		t.Fatal(err)
	}
	wantActions(t, f.plan(t), map[string]Action{"D-1-keep.md": ActionCreated})
}

func TestPlanUnchangedWhenIdentical(t *testing.T) {
	f := newFixture(t)
	f.doc(t, "One", "b", "")
	f.doc(t, "Two", "b", f.brief(t, "B").ID)
	f.apply(t)
	wantActions(t, f.plan(t), map[string]Action{"D-1-one.md": ActionUnchanged, "D-2-two.md": ActionUnchanged})
}

func TestPlanUpdatedWhenDocumentChanges(t *testing.T) {
	f := newFixture(t)
	b := f.brief(t, "Old")
	f.doc(t, "Body", "b", "")
	f.doc(t, "Kind", "b", "")
	f.doc(t, "Tied", "b", b.ID)
	f.apply(t)
	f.update(t, "D-1", documents.Change{Body: ptr("new body")})
	f.update(t, "D-2", documents.Change{Kind: ptr(documents.KindReference)})
	cur, err := f.units.Get(ctx, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.units.Update(ctx, b.ID, cur.Fingerprint, units.Change{Title: ptr("New")}); err != nil {
		t.Fatal(err)
	}
	wantActions(t, f.plan(t), map[string]Action{"D-1-body.md": ActionUpdated, "D-2-kind.md": ActionUpdated, "D-3-tied.md": ActionUpdated})
}

func TestPlanUpdatedWhenPersonEditedFile(t *testing.T) {
	f := newFixture(t)
	f.doc(t, "Doc", "b", "")
	f.apply(t)
	p := filepath.Join(f.dir, "D-1-doc.md")
	data, _ := os.ReadFile(p)
	if err := os.WriteFile(p, append(data, "edited\n"...), 0o644); err != nil {
		t.Fatal(err)
	}
	wantActions(t, f.plan(t), map[string]Action{"D-1-doc.md": ActionUpdated})
}

func TestPlanRenameIsCreateAndRemove(t *testing.T) {
	f := newFixture(t)
	f.doc(t, "Old title", "b", "")
	f.apply(t)
	f.update(t, "D-1", documents.Change{Title: ptr("New title")})
	wantActions(t, f.plan(t), map[string]Action{"D-1-new-title.md": ActionCreated, "D-1-old-title.md": ActionRemoved})
}

func TestPlanArchivedDocumentFileRemoved(t *testing.T) {
	f := newFixture(t)
	d := f.doc(t, "Doc", "b", "")
	f.apply(t)
	if _, err := f.docs.Archive(ctx, d.ID, d.Fingerprint); err != nil {
		t.Fatal(err)
	}
	wantActions(t, f.plan(t), map[string]Action{"D-1-doc.md": ActionRemoved})
}

func TestPlanSupersededDocumentFileRemoved(t *testing.T) {
	f := newFixture(t)
	old := f.doc(t, "Old", "b", "")
	f.doc(t, "New", "b", "")
	f.apply(t)
	if _, err := f.docs.Supersede(ctx, old.ID, old.Fingerprint, "D-2"); err != nil {
		t.Fatal(err)
	}
	wantActions(t, f.plan(t), map[string]Action{"D-1-old.md": ActionRemoved, "D-2-new.md": ActionUnchanged})
}

func TestPlanRestoredDocumentFileCreatedAgain(t *testing.T) {
	f := newFixture(t)
	d := f.doc(t, "Doc", "b", "")
	f.apply(t)
	arch, err := f.docs.Archive(ctx, d.ID, d.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	f.apply(t)
	if _, err := f.docs.Restore(ctx, d.ID, arch.Fingerprint); err != nil {
		t.Fatal(err)
	}
	wantActions(t, f.plan(t), map[string]Action{"D-1-doc.md": ActionCreated})
}

func TestPlanConflictWhenUnmarkedFileHasPlannedName(t *testing.T) {
	f := newFixture(t)
	f.doc(t, "Doc", "b", "")
	f.write(t, "D-1-doc.md", "# mine\n")
	p := f.plan(t)
	if len(p.Changes) != 1 || p.Changes[0].Action != ActionConflict || p.Changes[0].Detail != "exists and was not generated by ralph; not overwritten" || p.Changes[0].ID != "D-1" {
		t.Fatalf("changes = %+v", p.Changes)
	}
}

func TestPlanConflictForDirectoryAndSymlinkWithPlannedName(t *testing.T) {
	f := newFixture(t)
	f.doc(t, "Dir", "b", "")
	f.doc(t, "Link", "b", "")
	f.write(t, "keep.txt", "x")
	if err := os.Mkdir(filepath.Join(f.dir, "D-1-dir.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.md")
	if err := os.WriteFile(outside, []byte(Marker("D-2", 1)+"\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(f.dir, "D-2-link.md")); err != nil {
		t.Fatal(err)
	}
	p := f.plan(t)
	for _, c := range p.Changes {
		if c.Action != ActionConflict || c.Detail != "exists and is not a regular file" {
			t.Fatalf("change %+v", c)
		}
	}
	if len(p.Changes) != 2 {
		t.Fatalf("changes = %+v", p.Changes)
	}
	res, err := p.Apply()
	if err != nil || len(res.Conflicts) != 2 {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if data, _ := os.ReadFile(outside); string(data) != Marker("D-2", 1)+"\nbody\n" {
		t.Fatalf("symlink target changed: %q", data)
	}
}

func TestPlanLeavesForeignFilesAlone(t *testing.T) {
	f := newFixture(t)
	f.write(t, "README.md", Marker("D-1", 1)+"\n")
	f.write(t, "D-9-notes.md", "# notes\n")
	f.write(t, "D-8-second-line.md", "title\n"+Marker("D-8", 1)+"\n")
	if err := os.Mkdir(filepath.Join(f.dir, "D-7-sub.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "t.md")
	if err := os.WriteFile(target, []byte(Marker("D-6", 1)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(f.dir, "D-6-link.md")); err != nil {
		t.Fatal(err)
	}
	if p := f.plan(t); len(p.Changes) != 0 {
		t.Fatalf("changes = %+v", p.Changes)
	}
}

type failingUnits struct{ units.Service }

func (failingUnits) Get(context.Context, string) (units.Unit, error) {
	return units.Unit{}, errors.New("boom")
}

func TestPlanMissingBriefTitleIsError(t *testing.T) {
	f := newFixture(t)
	b := f.brief(t, "B")
	f.doc(t, "Tied", "b", b.ID)
	_, err := BuildPlan(ctx, f.docs, failingUnits{f.units}, f.dir)
	if err == nil || !strings.Contains(err.Error(), "D-1") || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v", err)
	}
}

func TestPlanSortedByName(t *testing.T) {
	f := newFixture(t)
	f.doc(t, "Zed", "b", "")
	f.doc(t, "Alpha", "b", "")
	f.write(t, "D-0-stale.md", Marker("D-0", 1)+"\n")
	var names []string
	for _, c := range f.plan(t).Changes {
		names = append(names, c.Name)
	}
	if !reflect.DeepEqual(names, []string{"D-0-stale.md", "D-1-zed.md", "D-2-alpha.md"}) {
		t.Fatalf("names = %v", names)
	}
}

func TestPlanWritesNothing(t *testing.T) {
	f := newFixture(t)
	f.doc(t, "Keep", "b", "")
	f.doc(t, "Change", "b", "")
	f.apply(t)
	f.update(t, "D-2", documents.Change{Body: ptr("x")})
	f.write(t, "D-5-stale.md", Marker("D-5", 1)+"\n")
	snap := func() map[string]time.Time {
		m := map[string]time.Time{}
		es, err := os.ReadDir(f.dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range es {
			i, _ := e.Info()
			m[e.Name()] = i.ModTime()
		}
		return m
	}
	before := snap()
	f.plan(t)
	if after := snap(); !reflect.DeepEqual(before, after) {
		t.Fatalf("before %v after %v", before, after)
	}
}
