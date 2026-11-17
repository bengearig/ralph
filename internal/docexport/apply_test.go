package docexport

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/bengearig/ralph/internal/documents"
)

func noTemps(t *testing.T, dir string) {
	t.Helper()
	es, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range es {
		if strings.HasPrefix(e.Name(), ".ralph-export-") {
			t.Fatalf("temporary file left: %s", e.Name())
		}
	}
}

func TestApplyCreatesDirectoryAndFiles(t *testing.T) {
	f := newFixture(t)
	f.dir = filepath.Join(f.dir, "nested", "dir")
	f.doc(t, "Doc", "b", "")
	old := syscall.Umask(0o077)
	defer syscall.Umask(old)
	f.apply(t)
	di, err := os.Stat(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(f.dir, "D-1-doc.md"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o644 {
		t.Fatalf("file mode %v", fi.Mode().Perm())
	}
	if di.Mode().Perm() != 0o700 && di.Mode().Perm() != 0o755 {
		t.Fatalf("dir mode %v", di.Mode().Perm())
	}
}

func TestApplyDirectoryMode(t *testing.T) {
	f := newFixture(t)
	f.doc(t, "Doc", "b", "")
	old := syscall.Umask(0)
	defer syscall.Umask(old)
	f.apply(t)
	di, _ := os.Stat(f.dir)
	if di.Mode().Perm() != 0o755 {
		t.Fatalf("dir mode %v", di.Mode().Perm())
	}
}

func TestApplyNoDirectoryWhenNothingToWrite(t *testing.T) {
	f := newFixture(t)
	res := f.apply(t)
	if _, err := os.Stat(f.dir); !os.IsNotExist(err) {
		t.Fatalf("directory exists: %v", err)
	}
	if res.Created+res.Updated+res.Removed+res.Unchanged != 0 {
		t.Fatalf("res = %+v", res)
	}
}

func TestApplyUnchangedFilesNotRewritten(t *testing.T) {
	f := newFixture(t)
	f.doc(t, "Doc", "b", "")
	f.apply(t)
	p := filepath.Join(f.dir, "D-1-doc.md")
	past := time.Now().Add(-time.Hour).Truncate(time.Second)
	if err := os.Chtimes(p, past, past); err != nil {
		t.Fatal(err)
	}
	before, _ := os.Stat(p)
	res := f.apply(t)
	after, _ := os.Stat(p)
	if res.Unchanged != 1 || !after.ModTime().Equal(before.ModTime()) || !os.SameFile(before, after) {
		t.Fatalf("res=%+v before=%v after=%v", res, before.ModTime(), after.ModTime())
	}
}

func TestApplyRemovesStaleFiles(t *testing.T) {
	f := newFixture(t)
	f.doc(t, "Doc", "b", "")
	f.apply(t)
	stale := f.write(t, "D-9-gone.md", Marker("D-9", 1)+"\n")
	foreign := f.write(t, "D-8-mine.md", "mine\n")
	res := f.apply(t)
	if res.Removed != 1 {
		t.Fatalf("res = %+v", res)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatal("stale file still exists")
	}
	if _, err := os.Stat(foreign); err != nil {
		t.Fatal(err)
	}
}

func TestApplyConflictsAreNotErrorsAndNotWritten(t *testing.T) {
	f := newFixture(t)
	f.doc(t, "Mine", "b", "")
	f.doc(t, "Other", "b", "")
	p := f.write(t, "D-1-mine.md", "hand written\n")
	res := f.apply(t)
	if res.Created != 1 || len(res.Conflicts) != 1 || res.Conflicts[0].Name != "D-1-mine.md" {
		t.Fatalf("res = %+v", res)
	}
	if data, _ := os.ReadFile(p); string(data) != "hand written\n" {
		t.Fatalf("conflicting file changed: %q", data)
	}
	if _, err := os.Stat(filepath.Join(f.dir, "D-2-other.md")); err != nil {
		t.Fatal(err)
	}
}

func TestApplyAtomicReplace(t *testing.T) {
	f := newFixture(t)
	f.doc(t, "Doc", "b", "")
	f.doc(t, "Fail", "b", "")
	f.apply(t)
	noTemps(t, f.dir)
	f.update(t, "D-1", documents.Change{Body: ptr("new")})
	f.update(t, "D-2", documents.Change{Body: ptr("new")})
	plan := f.plan(t)
	fail := filepath.Join(f.dir, "D-2-fail.md")
	if err := os.Remove(fail); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(fail, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := plan.Apply(); err == nil {
		t.Fatal("expected failure")
	}
	noTemps(t, f.dir)
	if data, _ := os.ReadFile(filepath.Join(f.dir, "D-1-doc.md")); !strings.Contains(string(data), "new") {
		t.Fatal("first file not replaced")
	}
}

func TestApplyContinuesAfterFailure(t *testing.T) {
	f := newFixture(t)
	f.doc(t, "A", "b", "")
	f.doc(t, "B", "b", "")
	f.apply(t)
	f.update(t, "D-1", documents.Change{Body: ptr("new")})
	f.update(t, "D-2", documents.Change{Body: ptr("new")})
	f.write(t, "D-9-stale.md", Marker("D-9", 1)+"\n")
	plan := f.plan(t)
	bad := filepath.Join(f.dir, "D-1-a.md")
	if err := os.Remove(bad); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(bad, 0o755); err != nil {
		t.Fatal(err)
	}
	res, err := plan.Apply()
	if err == nil || !strings.Contains(err.Error(), bad) {
		t.Fatalf("err = %v", err)
	}
	if res.Updated != 1 || res.Removed != 1 {
		t.Fatalf("res = %+v", res)
	}
}

func TestApplyResultCounts(t *testing.T) {
	f := newFixture(t)
	f.doc(t, "Same", "b", "")
	f.doc(t, "Changed", "b", "")
	f.apply(t)
	f.update(t, "D-2", documents.Change{Body: ptr("new")})
	f.doc(t, "Fresh", "b", "")
	f.write(t, "D-9-stale.md", Marker("D-9", 1)+"\n")
	res := f.apply(t)
	if res.Created != 1 || res.Updated != 1 || res.Removed != 1 || res.Unchanged != 1 || len(res.Conflicts) != 0 {
		t.Fatalf("res = %+v", res)
	}
}

func TestApplyIdempotent(t *testing.T) {
	f := newFixture(t)
	f.doc(t, "A", "b", "")
	f.doc(t, "B", "b", f.brief(t, "x").ID)
	f.write(t, "D-9-stale.md", Marker("D-9", 1)+"\n")
	f.apply(t)
	res := f.apply(t)
	if res.Created+res.Updated+res.Removed != 0 || res.Unchanged != 2 {
		t.Fatalf("res = %+v", res)
	}
}

func TestApplyConcurrentWithDocumentEdit(t *testing.T) {
	f := newFixture(t)
	f.doc(t, "Doc", "b", "")
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			if p, err := BuildPlan(ctx, f.docs, f.units, f.dir); err == nil {
				_, _ = p.Apply()
			}
		}()
		go func() {
			defer wg.Done()
			cur, err := f.docs.Get(ctx, "D-1")
			if err != nil {
				return
			}
			_, _ = f.docs.Update(ctx, "D-1", cur.Fingerprint, documents.Change{Body: ptr("edit")})
		}()
	}
	wg.Wait()
	f.update(t, "D-1", documents.Change{Body: ptr("final")})
	wantActions(t, f.plan(t), map[string]Action{"D-1-doc.md": ActionUpdated})
	noTemps(t, f.dir)
}
