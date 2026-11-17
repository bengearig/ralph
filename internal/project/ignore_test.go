package project

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
)

func ignorePath(root string) string { return filepath.Join(root, DirName, ".gitignore") }

func readIgnore(t *testing.T, root string) string {
	t.Helper()
	b, err := os.ReadFile(ignorePath(root))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func writeIgnore(t *testing.T, root, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, DirName), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ignorePath(root), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

const header = "# Local ralph files. Written by ralph init.\n"

func TestLocalIgnoreEntriesUseConstants(t *testing.T) {
	want := []string{"/" + StoreFile + "*", "/" + BackupDirName + "/", "/" + LocalFile, "/" + IntegrationLocalFile, "/runs/", "/loop.lock", "/index.db*", "/index.rebuild.db*", "/index.lock", "/tickets.lock"}
	if got := LocalIgnoreEntries(); !reflect.DeepEqual(got, want) {
		t.Fatalf("entries = %q, want %q", got, want)
	}
	if got := LocalIgnoreEntries(); got[0] != "/store.db*" || got[1] != "/backups/" || got[2] != "/project.local.yaml" || got[3] != "/integration.local.json" || got[4] != "/runs/" || got[5] != "/loop.lock" {
		t.Fatalf("entries = %q", got)
	}
}

func TestEnsureIgnoreCreatesFile(t *testing.T) {
	root := t.TempDir()
	added, err := EnsureIgnore(root, LocalIgnoreEntries())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(added, LocalIgnoreEntries()) {
		t.Fatalf("added = %q", added)
	}
	want := header + "/store.db*\n/backups/\n/project.local.yaml\n/integration.local.json\n/runs/\n/loop.lock\n/index.db*\n/index.rebuild.db*\n/index.lock\n/tickets.lock\n"
	if got := readIgnore(t, root); got != want {
		t.Fatalf("file = %q, want %q", got, want)
	}
}

func TestEnsureIgnoreIsIdempotent(t *testing.T) {
	root := t.TempDir()
	if _, err := EnsureIgnore(root, LocalIgnoreEntries()); err != nil {
		t.Fatal(err)
	}
	before := readIgnore(t, root)
	added, err := EnsureIgnore(root, LocalIgnoreEntries())
	if err != nil || len(added) != 0 {
		t.Fatalf("added = %q, err = %v", added, err)
	}
	if got := readIgnore(t, root); got != before {
		t.Fatalf("file changed: %q", got)
	}
}

func TestEnsureIgnoreKeepsUserLines(t *testing.T) {
	root := t.TempDir()
	writeIgnore(t, root, "mine.log\n/backups/\nother\n")
	added, err := EnsureIgnore(root, LocalIgnoreEntries())
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"/store.db*", "/project.local.yaml", "/integration.local.json", "/runs/", "/loop.lock", "/index.db*", "/index.rebuild.db*", "/index.lock", "/tickets.lock"}; !reflect.DeepEqual(added, want) {
		t.Fatalf("added = %q, want %q", added, want)
	}
	want := "mine.log\n/backups/\nother\n/store.db*\n/project.local.yaml\n/integration.local.json\n/runs/\n/loop.lock\n/index.db*\n/index.rebuild.db*\n/index.lock\n/tickets.lock\n"
	if got := readIgnore(t, root); got != want {
		t.Fatalf("file = %q, want %q", got, want)
	}
}

func TestEnsureIgnoreAddsMissingNewline(t *testing.T) {
	root := t.TempDir()
	writeIgnore(t, root, "mine.log")
	if _, err := EnsureIgnore(root, LocalIgnoreEntries()); err != nil {
		t.Fatal(err)
	}
	want := "mine.log\n/store.db*\n/backups/\n/project.local.yaml\n/integration.local.json\n/runs/\n/loop.lock\n/index.db*\n/index.rebuild.db*\n/index.lock\n/tickets.lock\n"
	if got := readIgnore(t, root); got != want {
		t.Fatalf("file = %q, want %q", got, want)
	}
}

func TestEnsureIgnoreHandlesCRLF(t *testing.T) {
	root := t.TempDir()
	writeIgnore(t, root, "/store.db*\r\n/backups/  \r\n")
	added, err := EnsureIgnore(root, LocalIgnoreEntries())
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"/project.local.yaml", "/integration.local.json", "/runs/", "/loop.lock", "/index.db*", "/index.rebuild.db*", "/index.lock", "/tickets.lock"}; !reflect.DeepEqual(added, want) {
		t.Fatalf("added = %q, want %q", added, want)
	}
	want := "/store.db*\r\n/backups/  \r\n/project.local.yaml\n/integration.local.json\n/runs/\n/loop.lock\n/index.db*\n/index.rebuild.db*\n/index.lock\n/tickets.lock\n"
	if got := readIgnore(t, root); got != want {
		t.Fatalf("file = %q, want %q", got, want)
	}
}

func TestEnsureIgnoreCreatesDirectory(t *testing.T) {
	root := t.TempDir()
	if _, err := os.Stat(filepath.Join(root, DirName)); !os.IsNotExist(err) {
		t.Fatalf("directory exists: %v", err)
	}
	if _, err := EnsureIgnore(root, LocalIgnoreEntries()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(ignorePath(root)); err != nil {
		t.Fatal(err)
	}
}

func TestEnsureIgnoreExactLinesOnly(t *testing.T) {
	root := t.TempDir()
	writeIgnore(t, root, "store.db\n")
	added, err := EnsureIgnore(root, []string{"/store.db*"})
	if err != nil || !reflect.DeepEqual(added, []string{"/store.db*"}) {
		t.Fatalf("added = %q, err = %v", added, err)
	}
	if got := readIgnore(t, root); got != "store.db\n/store.db*\n" {
		t.Fatalf("file = %q", got)
	}
}

func TestLocalIgnoreEntriesIncludeLocalManifest(t *testing.T) {
	for _, e := range LocalIgnoreEntries() {
		if e == "/integration.local.json" {
			return
		}
	}
	t.Fatalf("entries = %q", LocalIgnoreEntries())
}

func TestLocalIgnoreEntriesIncludeRunsAndLock(t *testing.T) {
	got := LocalIgnoreEntries()
	for _, want := range []string{"/runs/", "/loop.lock", "/index.db*", "/index.rebuild.db*", "/index.lock", "/tickets.lock"} {
		found := false
		for _, e := range got {
			found = found || e == want
		}
		if !found {
			t.Fatalf("entries = %q, missing %q", got, want)
		}
	}
}

func TestLocalIgnoreEntriesIncludeIndex(t *testing.T) {
	if !slices.Contains(LocalIgnoreEntries(), "/index.db*") {
		t.Fatalf("entries = %q", LocalIgnoreEntries())
	}
}

func TestLocalIgnoreEntriesIncludeRebuildAndLock(t *testing.T) {
	got := LocalIgnoreEntries()
	for _, want := range []string{"/index.rebuild.db*", "/index.lock", "/tickets.lock"} {
		if !slices.Contains(got, want) {
			t.Fatalf("entries = %q, missing %q", got, want)
		}
	}
}

func TestLocalIgnoreEntriesIncludeTicketsLock(t *testing.T) {
	if TicketsLockFile != "tickets.lock" {
		t.Fatalf("TicketsLockFile = %q", TicketsLockFile)
	}
	got := LocalIgnoreEntries()
	if got[len(got)-1] != "/tickets.lock" {
		t.Fatalf("entries = %q", got)
	}
}
