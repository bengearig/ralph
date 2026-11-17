package db

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestPullRequestsMigrationApplies(t *testing.T) {
	last := Migrations[len(Migrations)-1]
	if last.Name != "create pull requests" {
		t.Fatalf("last migration = %d %q", last.Version, last.Name)
	}
	fresh, err := openWith(t, filepath.Join(t.TempDir(), "fresh.db"), Migrations, MigrateAuto)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = fresh.Close() }()
	for _, c := range []struct{ typ, name string }{
		{"table", "pull_requests"}, {"index", "pull_requests_by_slice"}, {"trigger", "pull_requests_slice_only"},
	} {
		var n int
		if err := fresh.SQL().QueryRow("SELECT count(*) FROM sqlite_master WHERE type = ? AND name = ?", c.typ, c.name).Scan(&n); err != nil || n != 1 {
			t.Errorf("%s %s: count %d, %v", c.typ, c.name, n, err)
		}
	}

	path := filepath.Join(t.TempDir(), "old.db")
	old, err := openWith(t, path, Migrations[:last.Version-1], MigrateAuto)
	if err != nil {
		t.Fatal(err)
	}
	_ = old.Close()
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
}

func TestPullRequestsTriggerRejectsNonSlices(t *testing.T) {
	d, err := openWith(t, filepath.Join(t.TempDir(), "s.db"), Migrations, MigrateAuto)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	for _, u := range []struct{ id, level, parent, state string }{{"B-1", "brief", "NULL", "NULL"}, {"S-1", "slice", "'B-1'", "NULL"}, {"T-1", "task", "'S-1'", "'TODO'"}} {
		if _, err := d.SQL().Exec("INSERT INTO units (id, level, number, parent_id, position, title, body, acceptance_criteria, state, created_at, updated_at) VALUES (?, ?, 1, "+u.parent+", 0, 't', 'b', '',"+u.state+", 'x', 'x')", u.id, u.level); err != nil {
			t.Fatal(err)
		}
	}
	insert := func(id string) error {
		_, err := d.SQL().Exec("INSERT INTO pull_requests (slice_id, number, url, head, base, state, created_at, updated_at) VALUES (?, 1, 'u', 'h', 'b', 'open', 'x', 'x')", id)
		return err
	}
	if err := insert("S-1"); err != nil {
		t.Fatalf("slice: %v", err)
	}
	for _, id := range []string{"B-1", "T-1", "S-9"} {
		err := insert(id)
		if err == nil || !strings.Contains(err.Error(), "a pull request belongs to a slice") {
			t.Errorf("%s: err = %v", id, err)
		}
	}
}
