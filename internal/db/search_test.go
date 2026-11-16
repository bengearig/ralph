package db

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestSearchMigrationApplies(t *testing.T) {
	last := Migrations[len(Migrations)-1]
	if last.Version != 8 || last.Name != "create search index" {
		t.Fatalf("last migration = %d %q", last.Version, last.Name)
	}
	fresh, err := openWith(t, filepath.Join(t.TempDir(), "fresh.db"), Migrations, MigrateAuto)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = fresh.Close() }()
	if v := version(t, fresh); v != 8 {
		t.Errorf("version = %d, want 8", v)
	}
	if !hasTable(t, fresh, "search_fts") {
		t.Fatal("search_fts missing")
	}
	if _, err := fresh.SQL().Exec("INSERT INTO search_fts (rowid, title, body, kind, ref, unit) VALUES (5, 'Dark themes', 'a body', 'task', 'T-1', '')"); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := fresh.SQL().QueryRow("SELECT count(*) FROM search_fts WHERE search_fts MATCH '\"theme\"'").Scan(&n); err != nil || n != 1 {
		t.Errorf("match count = %d, err %v", n, err)
	}

	path := filepath.Join(t.TempDir(), "old.db")
	old, err := openWith(t, path, Migrations[:7], MigrateAuto)
	if err != nil {
		t.Fatal(err)
	}
	if v := version(t, old); v != 7 {
		t.Fatalf("old version = %d, want 7", v)
	}
	_ = old.Close()
	migrated, err := openWith(t, path, Migrations, MigrateAuto)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = migrated.Close() }()
	if v := version(t, migrated); v != 8 {
		t.Errorf("migrated version = %d, want 8", v)
	}
	a, b := schemaOf(t, fresh), schemaOf(t, migrated)
	if strings.Join(a, "\n") != strings.Join(b, "\n") {
		t.Errorf("schemas differ:\nfresh:\n%s\nmigrated:\n%s", strings.Join(a, "\n"), strings.Join(b, "\n"))
	}
}
