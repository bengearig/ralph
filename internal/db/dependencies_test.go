package db

import (
	"path/filepath"
	"strings"
	"testing"
)

func schemaOf(t *testing.T, d *DB) []string {
	t.Helper()
	rows, err := d.SQL().Query("SELECT type || ' ' || name || ' ' || coalesce(sql, '') FROM sqlite_master WHERE name NOT LIKE 'sqlite_%' ORDER BY type, name")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestDependenciesMigrationApplies(t *testing.T) {
	last := Migrations[len(Migrations)-1]
	if last.Version != 5 || last.Name != "create dependencies" {
		t.Fatalf("last migration = %d %q", last.Version, last.Name)
	}
	fresh, err := openWith(t, filepath.Join(t.TempDir(), "fresh.db"), Migrations, MigrateAuto)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = fresh.Close() }()
	if v := version(t, fresh); v != 5 {
		t.Errorf("version = %d, want 5", v)
	}
	var tables, indexes int
	if err := fresh.SQL().QueryRow("SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'dependencies'").Scan(&tables); err != nil {
		t.Fatal(err)
	}
	if err := fresh.SQL().QueryRow("SELECT count(*) FROM sqlite_master WHERE type = 'index' AND tbl_name = 'dependencies' AND sql LIKE '%depends_on_id%'").Scan(&indexes); err != nil {
		t.Fatal(err)
	}
	if tables != 1 || indexes != 1 {
		t.Errorf("tables = %d, indexes on depends_on_id = %d", tables, indexes)
	}

	path := filepath.Join(t.TempDir(), "old.db")
	old, err := openWith(t, path, Migrations[:4], MigrateAuto)
	if err != nil {
		t.Fatal(err)
	}
	if v := version(t, old); v != 4 {
		t.Fatalf("old version = %d, want 4", v)
	}
	_ = old.Close()
	migrated, err := openWith(t, path, Migrations, MigrateAuto)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = migrated.Close() }()
	if v := version(t, migrated); v != 5 {
		t.Errorf("migrated version = %d, want 5", v)
	}
	a, b := schemaOf(t, fresh), schemaOf(t, migrated)
	if strings.Join(a, "\n") != strings.Join(b, "\n") {
		t.Errorf("schemas differ:\nfresh:\n%s\nmigrated:\n%s", strings.Join(a, "\n"), strings.Join(b, "\n"))
	}
}

func seededDependencies(t *testing.T) *DB {
	t.Helper()
	d, err := openWith(t, filepath.Join(t.TempDir(), "dep.db"), Migrations, MigrateAuto)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	const cols = "(id, level, number, parent_id, position, title, state, created_at, updated_at)"
	for _, q := range []string{
		"INSERT INTO units " + cols + " VALUES ('B-1', 'brief', 1, NULL, 0, 't', NULL, 'x', 'x')",
		"INSERT INTO units " + cols + " VALUES ('S-1', 'slice', 1, 'B-1', 1, 't', NULL, 'x', 'x')",
		"INSERT INTO units " + cols + " VALUES ('S-2', 'slice', 2, 'B-1', 2, 't', NULL, 'x', 'x')",
		"INSERT INTO units " + cols + " VALUES ('T-1', 'task', 1, 'S-1', 1, 't', 'TODO', 'x', 'x')",
	} {
		if _, err := d.SQL().Exec(q); err != nil {
			t.Fatalf("%q: %v", q, err)
		}
	}
	return d
}

func TestDependenciesTriggerRejectsNonSlices(t *testing.T) {
	d := seededDependencies(t)
	if _, err := d.SQL().Exec("INSERT INTO dependencies VALUES ('S-1', 'S-2', 'x')"); err != nil {
		t.Fatalf("slice to slice: %v", err)
	}
	for _, pair := range [][2]string{{"S-1", "B-1"}, {"B-1", "S-1"}, {"S-1", "T-1"}, {"T-1", "S-2"}} {
		_, err := d.SQL().Exec("INSERT INTO dependencies VALUES (?, ?, 'x')", pair[0], pair[1])
		if err == nil || !strings.Contains(err.Error(), "a dependency joins two slices") {
			t.Errorf("%v: err = %v", pair, err)
		}
	}
}

func TestDependenciesCheckRejectsSelf(t *testing.T) {
	d := seededDependencies(t)
	if _, err := d.SQL().Exec("INSERT INTO dependencies VALUES ('S-1', 'S-1', 'x')"); err == nil {
		t.Fatal("self dependency accepted")
	}
}
