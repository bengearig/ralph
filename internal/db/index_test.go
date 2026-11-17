package db

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestIndexMigrationApplies(t *testing.T) {
	last := Migrations[8]
	if last.Version != 9 || last.Name != "index records for search" {
		t.Fatalf("migration 9 = %d %q", last.Version, last.Name)
	}
	fresh, err := openWith(t, filepath.Join(t.TempDir(), "fresh.db"), Migrations[:9], MigrateAuto)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = fresh.Close() }()
	if v := version(t, fresh); v != 9 {
		t.Errorf("version = %d, want 9", v)
	}

	path := filepath.Join(t.TempDir(), "old.db")
	old, err := openWith(t, path, Migrations[:8], MigrateAuto)
	if err != nil {
		t.Fatal(err)
	}
	if v := version(t, old); v != 8 {
		t.Fatalf("old version = %d, want 8", v)
	}
	_ = old.Close()
	migrated, err := openWith(t, path, Migrations[:9], MigrateAuto)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = migrated.Close() }()
	if v := version(t, migrated); v != 9 {
		t.Errorf("migrated version = %d, want 9", v)
	}
	a, b := schemaOf(t, fresh), schemaOf(t, migrated)
	if strings.Join(a, "\n") != strings.Join(b, "\n") {
		t.Errorf("schemas differ:\nfresh:\n%s\nmigrated:\n%s", strings.Join(a, "\n"), strings.Join(b, "\n"))
	}
}

func TestIndexMigrationBackfillsExistingRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	old, err := openWith(t, path, Migrations[:8], MigrateAuto)
	if err != nil {
		t.Fatal(err)
	}
	const cols = "(id, level, number, parent_id, position, title, body, acceptance_criteria, state, created_at, updated_at)"
	for _, q := range []string{
		"INSERT INTO units " + cols + " VALUES ('B-1', 'brief', 1, NULL, 0, 'brief heron', 'brief body', '', NULL, 'x', 'x')",
		"INSERT INTO units " + cols + " VALUES ('S-2', 'slice', 2, 'B-1', 1, 'slice egret', 'slice body', '', NULL, 'x', 'x')",
		"INSERT INTO units " + cols + " VALUES ('T-3', 'task', 3, 'S-2', 1, 'task ibis', 'task body', 'stork criterion', 'TODO', 'x', 'x')",
		"INSERT INTO units " + cols + " VALUES ('T-4', 'task', 4, 'S-2', 2, 'bare crane', '', '', 'TODO', 'x', 'x')",
		"INSERT INTO documents " + docCols + " VALUES ('D-5', 5, 'design', NULL, 'doc finch', 'doc body', 'ACTIVE', NULL, 'x', 'x')",
		"INSERT INTO notes (id, unit_id, kind, body, created_at) VALUES (7, 'T-3', 'GENERAL', 'note wren', 'x')",
	} {
		if _, err := old.SQL().Exec(q); err != nil {
			t.Fatalf("%q: %v", q, err)
		}
	}
	_ = old.Close()

	d, err := openWith(t, path, Migrations, MigrateAuto)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	want := []struct {
		rowid                        int
		title, body, kind, ref, unit string
	}{
		{5, "brief heron", "brief body", "brief", "B-1", ""},
		{11, "slice egret", "slice body", "slice", "S-2", ""},
		{17, "task ibis", "task body\nstork criterion", "task", "T-3", ""},
		{22, "bare crane", "", "task", "T-4", ""},
		{28, "doc finch", "doc body", "document", "D-5", ""},
		{39, "", "note wren", "note", "7", "T-3"},
	}
	if n := scalar(t, d.SQL(), "SELECT count(*) FROM search_fts"); n != "6" {
		t.Errorf("entries = %s, want 6", n)
	}
	for _, w := range want {
		var title, body, kind, ref, unit string
		err := d.SQL().QueryRow("SELECT title, body, kind, ref, unit FROM search_fts WHERE rowid = ?", w.rowid).Scan(&title, &body, &kind, &ref, &unit)
		if err != nil {
			t.Fatalf("rowid %d: %v", w.rowid, err)
		}
		if title != w.title || body != w.body || kind != w.kind || ref != w.ref || unit != w.unit {
			t.Errorf("rowid %d = %q %q %q %q %q, want %+v", w.rowid, title, body, kind, ref, unit, w)
		}
	}
	for _, word := range []string{"heron", "egret", "ibis", "stork", "finch", "wren"} {
		if n := scalar(t, d.SQL(), "SELECT count(*) FROM search_fts WHERE search_fts MATCH '"+word+"'"); n != "1" {
			t.Errorf("%s matches = %s, want 1", word, n)
		}
	}
}
