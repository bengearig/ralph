package project

import (
	"os"
	"path/filepath"
	"reflect"
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
	want := []string{"/" + StoreFile + "*", "/" + BackupDirName + "/", "/" + LocalFile}
	if got := LocalIgnoreEntries(); !reflect.DeepEqual(got, want) {
		t.Fatalf("entries = %q, want %q", got, want)
	}
	if got := LocalIgnoreEntries(); got[0] != "/store.db*" || got[1] != "/backups/" || got[2] != "/project.local.yaml" {
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
	want := header + "/store.db*\n/backups/\n/project.local.yaml\n"
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
	if want := []string{"/store.db*", "/project.local.yaml"}; !reflect.DeepEqual(added, want) {
		t.Fatalf("added = %q, want %q", added, want)
	}
	want := "mine.log\n/backups/\nother\n/store.db*\n/project.local.yaml\n"
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
	want := "mine.log\n/store.db*\n/backups/\n/project.local.yaml\n"
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
	if want := []string{"/project.local.yaml"}; !reflect.DeepEqual(added, want) {
		t.Fatalf("added = %q, want %q", added, want)
	}
	want := "/store.db*\r\n/backups/  \r\n/project.local.yaml\n"
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
