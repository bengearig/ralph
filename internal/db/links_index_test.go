package db

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestLinksTargetIndexExists(t *testing.T) {
	if m := Migrations[9]; m.Version != 10 || m.Name != "index document links by target" {
		t.Fatalf("migration 10 = %d %q", m.Version, m.Name)
	}
	d, err := openWith(t, filepath.Join(t.TempDir(), "fresh.db"), Migrations, MigrateAuto)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	var def string
	if err := d.SQL().QueryRow("SELECT sql FROM sqlite_master WHERE type = 'index' AND name = 'links_target'").Scan(&def); err != nil {
		t.Fatalf("links_target missing: %v", err)
	}
	if !strings.Contains(def, "links (kind, target)") {
		t.Errorf("definition = %q", def)
	}
	rows, err := d.SQL().Query("EXPLAIN QUERY PLAN SELECT unit_id FROM links WHERE kind = 'document' AND target = 'D-3'")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var plan []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	if !strings.Contains(strings.Join(plan, "\n"), "links_target") {
		t.Errorf("plan does not use links_target: %v", plan)
	}
}

func TestMigrationsApplyOnExistingDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	old, err := openWith(t, path, Migrations[:9], MigrateAuto)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.SQL().Exec("INSERT INTO units (id, level, number, parent_id, position, title, body, acceptance_criteria, state, created_at, updated_at) VALUES ('B-1', 'brief', 1, NULL, 0, 't', '', '', NULL, 'x', 'x')"); err != nil {
		t.Fatal(err)
	}
	if _, err := old.SQL().Exec("INSERT INTO links (unit_id, kind, target, created_at) VALUES ('B-1', 'document', 'D-1', 'x')"); err != nil {
		t.Fatal(err)
	}
	_ = old.Close()
	fresh, err := openWith(t, filepath.Join(t.TempDir(), "fresh.db"), Migrations, MigrateAuto)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = fresh.Close() }()
	migrated, err := openWith(t, path, Migrations, MigrateAuto)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = migrated.Close() }()
	if v := version(t, migrated); v != len(Migrations) {
		t.Errorf("version = %d, want %d", v, len(Migrations))
	}
	if strings.Join(schemaOf(t, fresh), "\n") != strings.Join(schemaOf(t, migrated), "\n") {
		t.Error("schemas differ")
	}
	var n int
	if err := migrated.SQL().QueryRow("SELECT count(*) FROM links").Scan(&n); err != nil || n != 1 {
		t.Errorf("links = %d, %v", n, err)
	}
}
