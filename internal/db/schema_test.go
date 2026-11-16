package db

import (
	"context"
	"path/filepath"
	"testing"
)

func TestMigrationsAreContiguous(t *testing.T) {
	if err := validateMigrations(Migrations); err != nil {
		t.Fatal(err)
	}
	if len(Migrations) == 0 || Migrations[0].Name != "create units" {
		t.Fatalf("first migration = %+v, want create units", Migrations)
	}
}

func TestUnitsConstraints(t *testing.T) {
	d, err := Open(context.Background(), filepath.Join(t.TempDir(), "store.db"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	exec := func(q string) error {
		_, err := d.SQL().Exec(q)
		return err
	}
	const cols = "(id, level, number, parent_id, position, title, state, created_at, updated_at)"
	for _, q := range []string{
		"INSERT INTO units " + cols + " VALUES ('B-1', 'brief', 1, NULL, 0, 't', NULL, 'x', 'x')",
		"INSERT INTO units " + cols + " VALUES ('S-1', 'slice', 1, 'B-1', 1, 't', NULL, 'x', 'x')",
		"INSERT INTO units " + cols + " VALUES ('T-1', 'task', 1, 'S-1', 1, 't', 'TODO', 'x', 'x')",
	} {
		if err := exec(q); err != nil {
			t.Fatalf("valid insert %q: %v", q, err)
		}
	}

	bad := map[string]string{
		"unknown level":           "INSERT INTO units " + cols + " VALUES ('X-1', 'epic', 1, NULL, 0, 't', NULL, 'x', 'x')",
		"brief with parent":       "INSERT INTO units " + cols + " VALUES ('B-2', 'brief', 2, 'B-1', 1, 't', NULL, 'x', 'x')",
		"slice without parent":    "INSERT INTO units " + cols + " VALUES ('S-2', 'slice', 2, NULL, 0, 't', NULL, 'x', 'x')",
		"slice under slice":       "INSERT INTO units " + cols + " VALUES ('S-2', 'slice', 2, 'S-1', 1, 't', NULL, 'x', 'x')",
		"slice under task":        "INSERT INTO units " + cols + " VALUES ('S-2', 'slice', 2, 'T-1', 1, 't', NULL, 'x', 'x')",
		"task under brief":        "INSERT INTO units " + cols + " VALUES ('T-2', 'task', 2, 'B-1', 1, 't', 'TODO', 'x', 'x')",
		"task under task":         "INSERT INTO units " + cols + " VALUES ('T-2', 'task', 2, 'T-1', 1, 't', 'TODO', 'x', 'x')",
		"missing parent":          "INSERT INTO units " + cols + " VALUES ('T-2', 'task', 2, 'S-9', 1, 't', 'TODO', 'x', 'x')",
		"task without state":      "INSERT INTO units " + cols + " VALUES ('T-2', 'task', 2, NULL, 0, 't', NULL, 'x', 'x')",
		"task with unknown state": "INSERT INTO units " + cols + " VALUES ('T-2', 'task', 2, NULL, 0, 't', 'OPEN', 'x', 'x')",
		"brief with state":        "INSERT INTO units " + cols + " VALUES ('B-2', 'brief', 2, NULL, 0, 't', 'TODO', 'x', 'x')",
		"slice with state":        "INSERT INTO units " + cols + " VALUES ('S-2', 'slice', 2, 'B-1', 2, 't', 'TODO', 'x', 'x')",
		"criteria on brief":       "INSERT INTO units (id, level, number, title, acceptance_criteria, created_at, updated_at) VALUES ('B-2', 'brief', 2, 't', 'c', 'x', 'x')",
		"criteria on slice":       "INSERT INTO units (id, level, number, parent_id, position, title, acceptance_criteria, created_at, updated_at) VALUES ('S-2', 'slice', 2, 'B-1', 2, 't', 'c', 'x', 'x')",
		"position without parent": "INSERT INTO units " + cols + " VALUES ('T-2', 'task', 2, NULL, 3, 't', 'TODO', 'x', 'x')",
		"duplicate number":        "INSERT INTO units " + cols + " VALUES ('T-9', 'task', 1, NULL, 0, 't', 'TODO', 'x', 'x')",
		"change parent":           "UPDATE units SET parent_id = NULL WHERE id = 'T-1'",
		"change id":               "UPDATE units SET id = 'T-7' WHERE id = 'T-1'",
		"change level":            "UPDATE units SET level = 'task' WHERE id = 'S-1'",
		"change number":           "UPDATE units SET number = 5 WHERE id = 'T-1'",
	}
	for name, q := range bad {
		if err := exec(q); err == nil {
			t.Errorf("%s: statement succeeded, want an error", name)
		}
	}
	if err := exec("UPDATE units SET title = 'new', position = 2 WHERE id = 'T-1'"); err != nil {
		t.Errorf("update of title and position: %v", err)
	}
}
