package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bengearig/ralph/internal/db"
	"github.com/bengearig/ralph/internal/docexport"
	"github.com/bengearig/ralph/internal/documents"
)

func (e *docsEnv) newDoc(title, body string) documents.Document {
	e.t.Helper()
	var doc documents.Document
	e.with(func(_ *db.DB, svc documents.Service) {
		d, err := svc.Create(e.ctx, documents.NewDocument{Kind: documents.KindDesign, Title: title, Body: body})
		if err != nil {
			e.t.Fatal(err)
		}
		doc = d
	})
	return doc
}

func (e *docsEnv) change(doc documents.Document, c documents.Change) documents.Document {
	e.t.Helper()
	e.with(func(_ *db.DB, svc documents.Service) {
		d, err := svc.Update(e.ctx, doc.ID, doc.Fingerprint, c)
		if err != nil {
			e.t.Fatal(err)
		}
		doc = d
	})
	return doc
}

func (e *docsEnv) archive(doc documents.Document) {
	e.t.Helper()
	e.with(func(_ *db.DB, svc documents.Service) {
		if _, err := svc.Archive(e.ctx, doc.ID, doc.Fingerprint); err != nil {
			e.t.Fatal(err)
		}
	})
}

func (e *docsEnv) path(rel string) string { return filepath.Join(e.repo.Dir, filepath.FromSlash(rel)) }

func (e *docsEnv) read(rel string) string {
	e.t.Helper()
	b, err := os.ReadFile(e.path(rel))
	if err != nil {
		e.t.Fatal(err)
	}
	return string(b)
}

func (e *docsEnv) exists(rel string) bool {
	_, err := os.Lstat(e.path(rel))
	return err == nil
}

func (e *docsEnv) write(rel, content string) {
	e.t.Helper()
	if err := os.MkdirAll(filepath.Dir(e.path(rel)), 0o755); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(e.path(rel), []byte(content), 0o644); err != nil {
		e.t.Fatal(err)
	}
}

func exportPath(doc documents.Document) string {
	return "docs/ralph/" + docexport.FileName(doc.ID, doc.Title)
}

func renderOf(doc documents.Document) string {
	return string(docexport.Render(docexport.Input{ID: doc.ID, Version: doc.Version, Kind: doc.Kind, Title: doc.Title, Body: doc.Body}))
}

func TestDocsExportHelpListsExport(t *testing.T) {
	for _, args := range [][]string{{"docs"}, {"docs", "--help"}} {
		code, out, _ := run(t, args...)
		if code != 0 || !strings.Contains(out, "export") {
			t.Fatalf("%v: code %d out %q", args, code, out)
		}
	}
}

func TestDocsExportCreatesFiles(t *testing.T) {
	e := newDocsEnv(t)
	a := e.newDoc("Alpha design", "alpha body")
	b := e.newDoc("Beta design", "beta body")
	code, out, stderr := run(t, "docs", "export")
	want := "created " + exportPath(a) + "\ncreated " + exportPath(b) + "\nexported: created 2, updated 0, removed 0, unchanged 0\n"
	if code != 0 || out != want || stderr != "" {
		t.Fatalf("code %d out %q stderr %q", code, out, stderr)
	}
	for _, d := range []documents.Document{a, b} {
		if got := e.read(exportPath(d)); got != renderOf(d) {
			t.Errorf("%s content = %q", d.ID, got)
		}
	}
}

func TestDocsExportSecondRunUnchanged(t *testing.T) {
	e := newDocsEnv(t)
	a := e.newDoc("Alpha", "x")
	e.newDoc("Beta", "y")
	run(t, "docs", "export")
	before, err := os.Stat(e.path(exportPath(a)))
	if err != nil {
		t.Fatal(err)
	}
	old := before.ModTime().Add(-time.Hour)
	if err := os.Chtimes(e.path(exportPath(a)), old, old); err != nil {
		t.Fatal(err)
	}
	code, out, _ := run(t, "docs", "export")
	if code != 0 || out != "exported: created 0, updated 0, removed 0, unchanged 2\n" {
		t.Fatalf("code %d out %q", code, out)
	}
	after, _ := os.Stat(e.path(exportPath(a)))
	if !after.ModTime().Equal(old) {
		t.Errorf("modification time changed: %v -> %v", old, after.ModTime())
	}
}

func TestDocsExportUpdatesAfterEdit(t *testing.T) {
	e := newDocsEnv(t)
	a := e.newDoc("Alpha", "x")
	run(t, "docs", "export")
	body := "changed"
	a = e.change(a, documents.Change{Body: &body})
	code, out, _ := run(t, "docs", "export")
	want := "updated " + exportPath(a) + "\nexported: created 0, updated 1, removed 0, unchanged 0\n"
	if code != 0 || out != want {
		t.Fatalf("code %d out %q", code, out)
	}
	if got := e.read(exportPath(a)); got != renderOf(a) {
		t.Errorf("content = %q", got)
	}
}

func TestDocsExportRemovesStaleOnArchive(t *testing.T) {
	e := newDocsEnv(t)
	a := e.newDoc("Alpha", "x")
	b := e.newDoc("Beta", "y")
	run(t, "docs", "export")
	e.archive(a)
	code, out, _ := run(t, "docs", "export")
	want := "removed " + exportPath(a) + "\nexported: created 0, updated 0, removed 1, unchanged 1\n"
	if code != 0 || out != want {
		t.Fatalf("code %d out %q", code, out)
	}
	if e.exists(exportPath(a)) || !e.exists(exportPath(b)) {
		t.Error("wrong files remain")
	}
}

func TestDocsExportRenameRemovesOldFile(t *testing.T) {
	e := newDocsEnv(t)
	a := e.newDoc("Old title", "x")
	run(t, "docs", "export")
	oldPath := exportPath(a)
	title := "New title"
	a = e.change(a, documents.Change{Title: &title})
	code, out, _ := run(t, "docs", "export")
	want := "created " + exportPath(a) + "\nremoved " + oldPath + "\nexported: created 1, updated 0, removed 1, unchanged 0\n"
	if code != 0 || out != want {
		t.Fatalf("code %d out %q want %q", code, out, want)
	}
}

func TestDocsExportOverwritesEditedFile(t *testing.T) {
	e := newDocsEnv(t)
	a := e.newDoc("Alpha", "x")
	run(t, "docs", "export")
	e.write(exportPath(a), e.read(exportPath(a))+"my own edit\n")
	code, out, _ := run(t, "docs", "export")
	if code != 0 || !strings.HasPrefix(out, "updated "+exportPath(a)+"\n") {
		t.Fatalf("code %d out %q", code, out)
	}
	if got := e.read(exportPath(a)); strings.Contains(got, "my own edit") || got != renderOf(a) {
		t.Errorf("content = %q", got)
	}
}

func TestDocsExportConflictExitsOneButExportsRest(t *testing.T) {
	e := newDocsEnv(t)
	a := e.newDoc("Alpha", "x")
	b := e.newDoc("Beta", "y")
	e.write(exportPath(a), "my own file\n")
	code, out, stderr := run(t, "docs", "export")
	want := "conflict " + exportPath(a) + ": exists and was not generated by ralph; not overwritten\n" +
		"created " + exportPath(b) + "\nexported: created 1, updated 0, removed 0, unchanged 0\n"
	if code != 1 || out != want {
		t.Fatalf("code %d out %q stderr %q", code, out, stderr)
	}
	if e.read(exportPath(a)) != "my own file\n" || e.read(exportPath(b)) != renderOf(b) {
		t.Error("files wrong after conflict")
	}
}

func TestDocsExportCheckCleanSaysUpToDate(t *testing.T) {
	e := newDocsEnv(t)
	e.newDoc("Alpha", "x")
	run(t, "docs", "export")
	code, out, _ := run(t, "docs", "export", "--check")
	if code != 0 || out != "up to date\n" {
		t.Fatalf("code %d out %q", code, out)
	}
	e2 := newDocsEnv(t)
	code, out, _ = run(t, "docs", "export", "--check")
	if code != 0 || out != "up to date\n" || e2.exists("docs") {
		t.Fatalf("empty: code %d out %q", code, out)
	}
}

func TestDocsExportCheckReportsWouldLines(t *testing.T) {
	e := newDocsEnv(t)
	a := e.newDoc("Alpha", "x")
	b := e.newDoc("Beta", "y")
	run(t, "docs", "export")
	body := "changed"
	b = e.change(b, documents.Change{Body: &body})
	e.archive(a)
	c := e.newDoc("Gamma", "z")
	code, out, _ := run(t, "docs", "export", "--check")
	want := "would remove " + exportPath(a) + "\nwould update " + exportPath(b) + "\nwould create " + exportPath(c) + "\n"
	if code != 1 || out != want {
		t.Fatalf("code %d out %q want %q", code, out, want)
	}
	if !e.exists(exportPath(a)) || e.exists(exportPath(c)) || e.read(exportPath(b)) == renderOf(b) {
		t.Error("check wrote files")
	}
}

func TestDocsExportCheckConflictExitsOne(t *testing.T) {
	e := newDocsEnv(t)
	a := e.newDoc("Alpha", "x")
	e.write(exportPath(a), "mine\n")
	code, out, _ := run(t, "docs", "export", "--check")
	want := "conflict " + exportPath(a) + ": exists and was not generated by ralph; not overwritten\n"
	if code != 1 || out != want {
		t.Fatalf("code %d out %q", code, out)
	}
}

func decodeExport(t *testing.T, out string) exportJSON {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(out))
	dec.DisallowUnknownFields()
	var doc exportJSON
	if err := dec.Decode(&doc); err != nil {
		t.Fatalf("decode %q: %v", out, err)
	}
	return doc
}

func TestDocsExportJSON(t *testing.T) {
	e := newDocsEnv(t)
	a := e.newDoc("Alpha", "x")
	b := e.newDoc("Beta", "y")
	c := e.newDoc("Gamma", "z")
	run(t, "docs", "export")
	body := "changed"
	b = e.change(b, documents.Change{Body: &body})
	e.archive(a)
	d := e.newDoc("Delta", "w")
	f := e.newDoc("Epsilon", "v")
	e.write(exportPath(f), "mine\n")

	wantFiles := []exportJSONFile{
		{exportPath(a), a.ID, "removed"}, {exportPath(b), b.ID, "updated"}, {exportPath(c), c.ID, "unchanged"},
		{exportPath(d), d.ID, "created"}, {exportPath(f), f.ID, "conflict"},
	}
	check := func(name string, args []string, counts [4]int) {
		t.Helper()
		code, out, _ := run(t, args...)
		got := decodeExport(t, out)
		if code != 1 || got.Dir != "docs/ralph" {
			t.Fatalf("%s: code %d dir %q", name, code, got.Dir)
		}
		if len(got.Files) != len(wantFiles) {
			t.Fatalf("%s: files = %+v", name, got.Files)
		}
		for i, w := range wantFiles {
			if got.Files[i] != w {
				t.Errorf("%s: file %d = %+v, want %+v", name, i, got.Files[i], w)
			}
		}
		if [4]int{got.Created, got.Updated, got.Removed, got.Unchanged} != counts {
			t.Errorf("%s: counts = %+v", name, got)
		}
		if len(got.Conflicts) != 1 || got.Conflicts[0] != (exportJSONConflict{exportPath(f), f.ID}) {
			t.Errorf("%s: conflicts = %+v", name, got.Conflicts)
		}
	}
	check("check", []string{"docs", "export", "--check", "--json"}, [4]int{1, 1, 1, 1})
	if e.exists(exportPath(d)) {
		t.Fatal("check wrote a file")
	}
	check("apply", []string{"docs", "export", "--json"}, [4]int{1, 1, 1, 1})
	if !e.exists(exportPath(d)) || e.exists(exportPath(a)) {
		t.Error("apply did not write")
	}
}

func TestDocsExportJSONEmptyArrays(t *testing.T) {
	newDocsEnv(t)
	code, out, _ := run(t, "docs", "export", "--json")
	if code != 0 || !strings.Contains(out, `"files": []`) || !strings.Contains(out, `"conflicts": []`) {
		t.Fatalf("code %d out %q", code, out)
	}
}

func TestDocsExportFromSubdirectory(t *testing.T) {
	e := newDocsEnv(t)
	a := e.newDoc("Alpha", "x")
	sub := e.path("src/deep")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(sub)
	code, out, _ := run(t, "docs", "export")
	if code != 0 || !strings.HasPrefix(out, "created "+exportPath(a)+"\n") {
		t.Fatalf("code %d out %q", code, out)
	}
	if !e.exists(exportPath(a)) || e.exists("src/deep/docs") {
		t.Error("files in the wrong place")
	}
}

func TestDocsExportCustomDirSetting(t *testing.T) {
	e := newDocsEnv(t)
	a := e.newDoc("Alpha", "x")
	e.write(".ralph/project.yaml", e.read(".ralph/project.yaml")+"documents:\n  export:\n    dir: exports/docs\n")
	code, out, stderr := run(t, "docs", "export")
	rel := "exports/docs/" + docexport.FileName(a.ID, a.Title)
	if code != 0 || !strings.HasPrefix(out, "created "+rel+"\n") {
		t.Fatalf("code %d out %q stderr %q", code, out, stderr)
	}
	if e.read(rel) != renderOf(a) || e.exists("docs") {
		t.Error("wrong files")
	}
}

func TestDocsExportRejectsEscapingSymlink(t *testing.T) {
	e := newDocsEnv(t)
	e.newDoc("Alpha", "x")
	outside := t.TempDir()
	if err := os.Symlink(outside, e.path("docs")); err != nil {
		t.Fatal(err)
	}
	code, out, stderr := run(t, "docs", "export")
	if code != 1 || out != "" || !strings.Contains(stderr, "documents.export.dir") {
		t.Fatalf("code %d out %q stderr %q", code, out, stderr)
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Errorf("wrote outside the project: %v", entries)
	}
}

func TestDocsExportNoDocumentsCreatesNothing(t *testing.T) {
	e := newDocsEnv(t)
	code, out, _ := run(t, "docs", "export")
	if code != 0 || out != "exported: created 0, updated 0, removed 0, unchanged 0\n" || e.exists("docs") {
		t.Fatalf("code %d out %q", code, out)
	}
}

func TestDocsExportDoesNotTouchGit(t *testing.T) {
	e := newDocsEnv(t)
	e.newDoc("Alpha", "x")
	snapshot := func() string {
		return e.repo.Git("rev-parse", "HEAD") + e.repo.Git("for-each-ref") + e.repo.Git("diff", "--cached", "--name-only")
	}
	before := snapshot()
	if code, _, stderr := run(t, "docs", "export"); code != 0 {
		t.Fatalf("code %d stderr %q", code, stderr)
	}
	if after := snapshot(); after != before {
		t.Errorf("git state changed:\n%s\n%s", before, after)
	}
}

func TestDocsExportNotInitialized(t *testing.T) {
	inRepo(t)
	code, out, stderr := run(t, "docs", "export")
	if want := "ralph: " + errNoDatabase + "\n"; code != 1 || out != "" || stderr != want {
		t.Fatalf("code %d out %q stderr %q", code, out, stderr)
	}
	if !strings.Contains(errNoDatabase, `ralph init`) {
		t.Fatal("message does not mention ralph init")
	}
}

func TestDocsExportOutsideRepository(t *testing.T) {
	t.Chdir(t.TempDir())
	code, _, stderr := run(t, "docs", "export")
	if code != 1 || !strings.Contains(stderr, "not in a git repository") {
		t.Fatalf("code %d stderr %q", code, stderr)
	}
}
