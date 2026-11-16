package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestManifestMissingIsEmpty(t *testing.T) {
	m, err := LoadManifest(filepath.Join(t.TempDir(), "integration.json"))
	if err != nil {
		t.Fatal(err)
	}
	if m.Version != 1 || len(m.Entries) != 0 || len(m.Customized) != 0 {
		t.Fatalf("manifest = %+v", m)
	}
}

func TestManifestInvalidNamesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "integration.json")
	if err := os.WriteFile(path, []byte("{nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadManifest(path)
	if err == nil || !strings.Contains(err.Error(), path) {
		t.Fatalf("err = %v", err)
	}
}

func TestManifestSaveAtomicAndSorted(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "integration.json")
	m := Manifest{Entries: []Entry{{ID: "b", Path: "b"}, {ID: "a", Path: "a", KeyPath: []string{"k"}}}}
	if err := m.Save(path); err != nil {
		t.Fatal(err)
	}
	got, err := LoadManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != 1 || len(got.Entries) != 2 || got.Entries[0].ID != "a" || got.Entries[1].ID != "b" {
		t.Fatalf("manifest = %+v", got)
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), "\n  \"version\": 1") {
		t.Fatalf("not indented: %s", raw)
	}
	files, _ := os.ReadDir(filepath.Dir(path))
	if len(files) != 1 {
		t.Fatalf("leftover files: %v", files)
	}
}

func TestManifestKeepsCustomizedList(t *testing.T) {
	path := filepath.Join(t.TempDir(), "integration.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"customized":["CLAUDE.md"],"entries":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := LoadManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	m.record(Entry{ID: "x"})
	if err := m.Save(path); err != nil {
		t.Fatal(err)
	}
	got, err := LoadManifest(path)
	if err != nil || len(got.Customized) != 1 || got.Customized[0] != "CLAUDE.md" || len(got.Entries) != 1 {
		t.Fatalf("manifest = %+v, err = %v", got, err)
	}
}
