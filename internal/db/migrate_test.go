package db

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func three() []Migration {
	return []Migration{
		{1, "first", "CREATE TABLE a (x); CREATE TABLE a2 (x);"},
		{2, "second", "CREATE TABLE b (x)"},
		{3, "third", "CREATE TABLE c (x)"},
	}
}

func openWith(t *testing.T, path string, m []Migration, mode MigrateMode) (*DB, error) {
	t.Helper()
	return Open(context.Background(), path, Options{Migrations: m, Mode: mode})
}

func hasTable(t *testing.T, d *DB, name string) bool {
	t.Helper()
	var n int
	if err := d.SQL().QueryRow("SELECT count(*) FROM sqlite_master WHERE name = ?", name).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n == 1
}

func version(t *testing.T, d *DB) int {
	t.Helper()
	v, err := d.SchemaVersion(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestMigrationsApplyInOrder(t *testing.T) {
	d, err := openWith(t, filepath.Join(t.TempDir(), "s.db"), three(), MigrateAuto)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	for _, tb := range []string{"a", "a2", "b", "c"} {
		if !hasTable(t, d, tb) {
			t.Errorf("table %s missing", tb)
		}
	}
	if v := version(t, d); v != 3 {
		t.Errorf("version = %d", v)
	}
}

func TestMigrationsAreNotReapplied(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.db")
	d, err := openWith(t, path, three(), MigrateAuto)
	if err != nil {
		t.Fatal(err)
	}
	_ = d.Close()
	d, err = openWith(t, path, three(), MigrateAuto)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = d.Close() }()
	if v := version(t, d); v != 3 {
		t.Errorf("version = %d", v)
	}
}

func TestFailedMigrationRollsBackThatMigration(t *testing.T) {
	m := three()
	m[1].SQL = "CREATE TABLE b (x); CREATE TABLE a (x)"
	path := filepath.Join(t.TempDir(), "s.db")
	_, err := openWith(t, path, m, MigrateAuto)
	if err == nil || !strings.Contains(err.Error(), "2") || !strings.Contains(err.Error(), "second") {
		t.Fatalf("err = %v", err)
	}
	d, err := openWith(t, path, m[:1], MigrateAuto)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	if v := version(t, d); v != 1 {
		t.Errorf("version = %d", v)
	}
	if !hasTable(t, d, "a") || hasTable(t, d, "b") || hasTable(t, d, "c") {
		t.Error("unexpected tables")
	}
}

func newerAt5(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "s.db")
	d, err := openWith(t, path, three(), MigrateAuto)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.SQL().Exec("PRAGMA user_version = 5"); err != nil {
		t.Fatal(err)
	}
	_ = d.Close()
	return path
}

func TestNewerDatabaseRefused(t *testing.T) {
	path := newerAt5(t)
	_, err := openWith(t, path, three(), MigrateAuto)
	if !errors.Is(err, ErrNewerDatabase) {
		t.Fatalf("err = %v", err)
	}
	for _, want := range []string{path, "5", "3", "upgrade ralph"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message %q lacks %q", err, want)
		}
	}
}

func TestNewerDatabaseUnchanged(t *testing.T) {
	path := newerAt5(t)
	if _, err := openWith(t, path, three(), MigrateAuto); err == nil {
		t.Fatal("expected error")
	}
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = raw.Close() }()
	var v, tables int
	if err := raw.QueryRow("PRAGMA user_version").Scan(&v); err != nil {
		t.Fatal(err)
	}
	if err := raw.QueryRow("SELECT count(*) FROM sqlite_master WHERE type = 'table'").Scan(&tables); err != nil {
		t.Fatal(err)
	}
	if v != 5 || tables != 4 {
		t.Errorf("version = %d, tables = %d", v, tables)
	}
}

func TestMigrationListValidation(t *testing.T) {
	cases := map[string][]Migration{
		"gap":        {{1, "a", ""}, {3, "c", ""}},
		"duplicate":  {{1, "a", ""}, {1, "b", ""}},
		"start at 2": {{2, "a", ""}},
		"zero":       {{0, "a", ""}},
	}
	for name, list := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := openWith(t, filepath.Join(t.TempDir(), "s.db"), list, MigrateAuto); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestManualModeFailsWhenPending(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.db")
	d, err := openWith(t, path, three()[:1], MigrateAuto)
	if err != nil {
		t.Fatal(err)
	}
	_ = d.Close()
	_, err = openWith(t, path, three(), MigrateManual)
	if !errors.Is(err, ErrMigrationRequired) || !strings.Contains(err.Error(), "ralph migrate") || !strings.Contains(err.Error(), path) {
		t.Fatalf("err = %v", err)
	}
	d, err = openWith(t, path, three()[:1], MigrateManual)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	if v := version(t, d); v != 1 {
		t.Errorf("version = %d", v)
	}
}

func TestManualModeOpensWhenUpToDate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.db")
	d, err := openWith(t, path, three(), MigrateAuto)
	if err != nil {
		t.Fatal(err)
	}
	_ = d.Close()
	d, err = openWith(t, path, three(), MigrateManual)
	if err != nil {
		t.Fatal(err)
	}
	_ = d.Close()
}

func TestMigrateIgnoresManualMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.db")
	d, err := openWith(t, path, three()[:1], MigrateAuto)
	if err != nil {
		t.Fatal(err)
	}
	_ = d.Close()
	res, err := Migrate(context.Background(), path, Options{Migrations: three(), Mode: MigrateManual})
	if err != nil {
		t.Fatal(err)
	}
	if res.From != 1 || res.To != 3 || strings.Join(res.Applied, ",") != "second,third" {
		t.Errorf("res = %+v", res)
	}
	res, err = Migrate(context.Background(), path, Options{Migrations: three()})
	if err != nil || res.From != 3 || res.To != 3 || len(res.Applied) != 0 {
		t.Errorf("res = %+v, %v", res, err)
	}
}

func TestSchemaVersion(t *testing.T) {
	d := openT(t, filepath.Join(t.TempDir(), "s.db"))
	if v := version(t, d); v != len(Migrations) {
		t.Errorf("version = %d, want %d", v, len(Migrations))
	}
	if _, err := d.SQL().Exec("PRAGMA user_version = 7"); err != nil {
		t.Fatal(err)
	}
	if v := version(t, d); v != 7 {
		t.Errorf("version = %d", v)
	}
}

func TestNilMigrationsUsesProductionList(t *testing.T) {
	d, err := Open(context.Background(), filepath.Join(t.TempDir(), "s.db"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	if v := version(t, d); v != len(Migrations) {
		t.Errorf("version = %d", v)
	}
}
