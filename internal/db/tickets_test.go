package db

import (
	"context"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func ticketsMigration(t *testing.T) Migration {
	t.Helper()
	for _, m := range Migrations {
		if m.Name == "create tickets" {
			return m
		}
	}
	t.Fatal("no create tickets migration")
	return Migration{}
}

func TestMigrationAddsTicketTables(t *testing.T) {
	m := ticketsMigration(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "old.db")
	old, err := openWith(t, path, Migrations[:m.Version-1], MigrateAuto)
	if err != nil {
		t.Fatal(err)
	}
	if hasTable(t, old, "tickets") || hasTable(t, old, "ticket_state") {
		t.Fatal("ticket tables exist before the migration")
	}
	_ = old.Close()

	backups := filepath.Join(dir, "backups")
	d, err := Open(context.Background(), path, Options{Mode: MigrateAuto, BackupDir: backups})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	if v := version(t, d); v != len(Migrations) {
		t.Errorf("version = %d, want %d", v, len(Migrations))
	}
	for _, tb := range []string{"tickets", "ticket_state"} {
		if !hasTable(t, d, tb) {
			t.Errorf("table %s missing", tb)
		}
	}
	matches, err := filepath.Glob(filepath.Join(backups, "pre-migrate-v"+strconv.Itoa(m.Version-1)+"-*.db"))
	if err != nil || len(matches) != 1 {
		t.Errorf("backups = %v, %v", matches, err)
	}
	fresh, err := openWith(t, filepath.Join(t.TempDir(), "fresh.db"), Migrations, MigrateAuto)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = fresh.Close() }()
	if strings.Join(schemaOf(t, fresh), "\n") != strings.Join(schemaOf(t, d), "\n") {
		t.Error("schemas differ")
	}
}

func TestMigrationKeepsExistingData(t *testing.T) {
	m := ticketsMigration(t)
	path := filepath.Join(t.TempDir(), "old.db")
	old, err := openWith(t, path, Migrations[:m.Version-1], MigrateAuto)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		"INSERT INTO units (id, level, number, parent_id, position, title, body, acceptance_criteria, state, created_at, updated_at) VALUES ('B-1', 'brief', 1, NULL, 0, 'Brief', 'b', '', NULL, 'x', 'x')",
		"INSERT INTO units (id, level, number, parent_id, position, title, body, acceptance_criteria, state, created_at, updated_at) VALUES ('S-1', 'slice', 1, 'B-1', 0, 'Slice', 'b', '', NULL, 'x', 'x')",
	} {
		if _, err := old.SQL().Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	_ = old.Close()

	d, err := openWith(t, path, Migrations, MigrateAuto)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	var n int
	if err := d.SQL().QueryRow("SELECT count(*) FROM units").Scan(&n); err != nil || n != 2 {
		t.Fatalf("units = %d, %v", n, err)
	}
	if _, err := d.SQL().Exec("INSERT INTO tickets (unit_id, system, ticket_key) VALUES ('S-1', 'jira', 'ABC-1')"); err != nil {
		t.Fatal(err)
	}
}

func mappedStatusMigration(t *testing.T) Migration {
	t.Helper()
	for _, m := range Migrations {
		if m.Name == "record mapped ticket status" {
			return m
		}
	}
	t.Fatal("no mapped status migration")
	return Migration{}
}

func columnsOf(t *testing.T, d *DB, table string) []string {
	t.Helper()
	rows, err := d.SQL().Query("SELECT name FROM pragma_table_info('" + table + "')")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		out = append(out, n)
	}
	return out
}

func TestMigrationAddsMappedStatusColumn(t *testing.T) {
	m := mappedStatusMigration(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "old.db")
	old, err := openWith(t, path, Migrations[:m.Version-1], MigrateAuto)
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(columnsOf(t, old, "tickets"), "mapped_status") {
		t.Fatal("mapped_status exists before the migration")
	}
	_ = old.Close()

	backups := filepath.Join(dir, "backups")
	d, err := Open(context.Background(), path, Options{Mode: MigrateAuto, BackupDir: backups})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	if v := version(t, d); v != len(Migrations) {
		t.Errorf("version = %d, want %d", v, len(Migrations))
	}
	if !slices.Contains(columnsOf(t, d, "tickets"), "mapped_status") {
		t.Error("mapped_status missing")
	}
	matches, err := filepath.Glob(filepath.Join(backups, "pre-migrate-v"+strconv.Itoa(m.Version-1)+"-*.db"))
	if err != nil || len(matches) != 1 {
		t.Errorf("backups = %v, %v", matches, err)
	}
	fresh, err := openWith(t, filepath.Join(t.TempDir(), "fresh.db"), Migrations, MigrateAuto)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = fresh.Close() }()
	if strings.Join(schemaOf(t, fresh), "\n") != strings.Join(schemaOf(t, d), "\n") {
		t.Error("schemas differ")
	}
}

func TestMigrationKeepsTicketRows(t *testing.T) {
	m := mappedStatusMigration(t)
	path := filepath.Join(t.TempDir(), "old.db")
	old, err := openWith(t, path, Migrations[:m.Version-1], MigrateAuto)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		"INSERT INTO units (id, level, number, parent_id, position, title, body, acceptance_criteria, state, created_at, updated_at) VALUES ('B-1', 'brief', 1, NULL, 0, 'Brief', 'b', '', NULL, 'x', 'x')",
		"INSERT INTO tickets (unit_id, system, ticket_key, pushed_status) VALUES ('B-1', 'jira', 'ABC-1', 'In Progress')",
	} {
		if _, err := old.SQL().Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	_ = old.Close()

	d, err := openWith(t, path, Migrations, MigrateAuto)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	var key, pushed, mapped string
	if err := d.SQL().QueryRow("SELECT ticket_key, pushed_status, mapped_status FROM tickets WHERE unit_id = 'B-1'").Scan(&key, &pushed, &mapped); err != nil {
		t.Fatal(err)
	}
	if key != "ABC-1" || pushed != "In Progress" || mapped != "" {
		t.Errorf("row = %q %q %q", key, pushed, mapped)
	}
}
