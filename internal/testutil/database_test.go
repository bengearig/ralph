package testutil

import (
	"context"
	"testing"

	"github.com/bengearig/ralph/internal/db"
)

var testMigrations = []db.Migration{
	{Version: 1, Name: "alpha", SQL: "CREATE TABLE alpha (id INTEGER PRIMARY KEY, name TEXT)"},
	{Version: 2, Name: "beta", SQL: "CREATE TABLE beta (id INTEGER PRIMARY KEY)"},
}

func schemaVersion(t *testing.T, d *db.DB) int {
	t.Helper()
	v, err := d.SchemaVersion(context.Background())
	if err != nil {
		t.Fatalf("schema version: %v", err)
	}
	return v
}

func tableCount(t *testing.T, d *db.DB, name string) int {
	t.Helper()
	var n int
	err := d.SQL().QueryRow("SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?", name).Scan(&n)
	if err != nil {
		t.Fatalf("look up table %s: %v", name, err)
	}
	return n
}

func TestNewDBOpensMigrated(t *testing.T) {
	d := NewDB(t, testMigrations)
	if v := schemaVersion(t, d); v != 2 {
		t.Errorf("schema version = %d, want 2", v)
	}
	for _, name := range []string{"alpha", "beta"} {
		if tableCount(t, d, name) != 1 {
			t.Errorf("table %s is missing", name)
		}
	}
}

func TestNewDBWithoutMigrations(t *testing.T) {
	for name, list := range map[string][]db.Migration{"nil": nil, "empty": {}} {
		t.Run(name, func(t *testing.T) {
			if v := schemaVersion(t, NewDB(t, list)); v != 0 {
				t.Errorf("schema version = %d, want 0", v)
			}
		})
	}
}

func TestNewDBIsolated(t *testing.T) {
	a := NewDB(t, testMigrations)
	b := NewDB(t, testMigrations)
	if a.Path() == b.Path() {
		t.Fatalf("both databases are at %s", a.Path())
	}
	if _, err := a.SQL().Exec("INSERT INTO alpha (name) VALUES ('only-in-a')"); err != nil {
		t.Fatalf("insert: %v", err)
	}
	var inA, inB int
	if err := a.SQL().QueryRow("SELECT count(*) FROM alpha").Scan(&inA); err != nil {
		t.Fatalf("count in a: %v", err)
	}
	if err := b.SQL().QueryRow("SELECT count(*) FROM alpha").Scan(&inB); err != nil {
		t.Fatalf("count in b: %v", err)
	}
	if inA != 1 || inB != 0 {
		t.Errorf("rows in a = %d, in b = %d; want 1 and 0", inA, inB)
	}
}

func TestNewDBClosesOnCleanup(t *testing.T) {
	var d *db.DB
	t.Run("use", func(t *testing.T) {
		d = NewDB(t, testMigrations)
		if err := d.SQL().Ping(); err != nil {
			t.Fatalf("ping while open: %v", err)
		}
	})
	if err := d.SQL().Ping(); err == nil {
		t.Error("database is still usable after the test ended")
	}
}
