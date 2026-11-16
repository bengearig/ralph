package db

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func openPlain(t *testing.T) *DB {
	t.Helper()
	d, err := openWith(t, filepath.Join(t.TempDir(), "s.db"), []Migration{{1, "t", "CREATE TABLE t (n INTEGER)"}}, MigrateAuto)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func insertRows(t *testing.T, d *DB, from, to int) {
	t.Helper()
	err := d.WriteTx(context.Background(), func(tx *sql.Tx) error {
		for i := from; i < to; i++ {
			if _, err := tx.Exec("INSERT INTO t (n) VALUES (?)", i); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func openCopy(t *testing.T, path string) *sql.DB {
	t.Helper()
	c, err := sql.Open("sqlite", dsn(path, map[string][]string{"mode": {"ro"}}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func scalar(t *testing.T, c *sql.DB, q string) (v string) {
	t.Helper()
	if err := c.QueryRow(q).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestBackupIsConsistentSnapshot(t *testing.T) {
	d := openPlain(t)
	insertRows(t, d, 0, 50)
	dest := filepath.Join(t.TempDir(), "copy.db")
	if err := d.Backup(context.Background(), dest); err != nil {
		t.Fatal(err)
	}
	c := openCopy(t, dest)
	if got := scalar(t, c, "PRAGMA integrity_check"); got != "ok" {
		t.Errorf("integrity_check = %q", got)
	}
	if got := scalar(t, c, "SELECT count(*) FROM t"); got != "50" {
		t.Errorf("rows = %s", got)
	}
	if got := scalar(t, c, "SELECT sum(n) FROM t"); got != "1225" {
		t.Errorf("sum = %s", got)
	}
	if got := scalar(t, c, "PRAGMA application_id"); got != "1380011088" {
		t.Errorf("application_id = %s", got)
	}
	if got := scalar(t, c, "PRAGMA user_version"); got != "1" {
		t.Errorf("user_version = %s", got)
	}
}

func TestBackupWhileWriting(t *testing.T) {
	d := openPlain(t)
	insertRows(t, d, 0, 100)
	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		total = 100
		stop  = make(chan struct{})
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 100; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			err := d.WriteTx(context.Background(), func(tx *sql.Tx) error {
				_, err := tx.Exec("INSERT INTO t (n) VALUES (?)", i)
				return err
			})
			if err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			total = i + 1
			mu.Unlock()
		}
	}()
	time.Sleep(20 * time.Millisecond)
	mu.Lock()
	start := total
	mu.Unlock()
	dest := filepath.Join(t.TempDir(), "copy.db")
	err := d.Backup(context.Background(), dest)
	mu.Lock()
	end := total
	mu.Unlock()
	close(stop)
	wg.Wait()
	if err != nil {
		t.Fatal(err)
	}
	c := openCopy(t, dest)
	if got := scalar(t, c, "PRAGMA integrity_check"); got != "ok" {
		t.Errorf("integrity_check = %q", got)
	}
	var n int
	if err := c.QueryRow("SELECT count(*) FROM t").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n < start || n > end+1 {
		t.Errorf("rows = %d, want between %d and %d", n, start, end)
	}
}

func TestBackupRefusesExistingDestination(t *testing.T) {
	d := openPlain(t)
	dest := filepath.Join(t.TempDir(), "copy.db")
	if err := os.WriteFile(dest, []byte("precious"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := d.Backup(context.Background(), dest)
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("err = %v", err)
	}
	if b, _ := os.ReadFile(dest); string(b) != "precious" {
		t.Errorf("contents = %q", b)
	}
}

func TestBackupCreatesParentDirectory(t *testing.T) {
	d := openPlain(t)
	dest := filepath.Join(t.TempDir(), "a", "b", "copy.db")
	if err := d.Backup(context.Background(), dest); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dest); err != nil {
		t.Error(err)
	}
}

func TestBackupFailureLeavesNoPartialFile(t *testing.T) {
	d := openPlain(t)
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(blocker, "copy.db")
	if err := d.Backup(context.Background(), dest); err == nil {
		t.Fatal("expected error")
	}
	if b, err := os.ReadFile(blocker); err != nil || string(b) != "x" {
		t.Errorf("blocker = %q, %v", b, err)
	}
	if _, err := os.Stat(dest); err == nil {
		t.Error("destination exists")
	}
}

func twoMigrations() []Migration {
	return []Migration{
		{1, "first", "CREATE TABLE t (n INTEGER)"},
		{2, "second", "CREATE TABLE u (n INTEGER)"},
	}
}

func backupFiles(t *testing.T, dir, pattern string) []string {
	t.Helper()
	m, err := filepath.Glob(filepath.Join(dir, pattern))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func fixedNow() func() time.Time {
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	return func() time.Time { return at }
}

func TestMigrationMakesBackupFirst(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.db")
	d, err := openWith(t, path, twoMigrations()[:1], MigrateAuto)
	if err != nil {
		t.Fatal(err)
	}
	insertRows(t, d, 0, 3)
	d.Close()

	d, err = Open(context.Background(), path, Options{Migrations: twoMigrations(), Now: fixedNow()})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if v := version(t, d); v != 2 {
		t.Errorf("live version = %d", v)
	}
	files := backupFiles(t, filepath.Join(dir, "backups"), "pre-migrate-v1-*.db")
	if len(files) != 1 || filepath.Base(files[0]) != "pre-migrate-v1-20260102T030405Z.db" {
		t.Fatalf("backups = %v", files)
	}
	c := openCopy(t, files[0])
	if got := scalar(t, c, "PRAGMA user_version"); got != "1" {
		t.Errorf("backup version = %s", got)
	}
	if got := scalar(t, c, "SELECT count(*) FROM t"); got != "3" {
		t.Errorf("backup rows = %s", got)
	}
}

func TestNoBackupForNewDatabase(t *testing.T) {
	dir := t.TempDir()
	d, err := openWith(t, filepath.Join(dir, "s.db"), three(), MigrateAuto)
	if err != nil {
		t.Fatal(err)
	}
	d.Close()
	if _, err := os.Stat(filepath.Join(dir, "backups")); err == nil {
		t.Error("backups directory created for a new database")
	}
}

func TestBackupFailureBlocksMigration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.db")
	d, err := openWith(t, path, twoMigrations()[:1], MigrateAuto)
	if err != nil {
		t.Fatal(err)
	}
	d.Close()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = Open(context.Background(), path, Options{Migrations: twoMigrations(), BackupDir: blocker})
	if err == nil || !strings.HasPrefix(err.Error(), "back up before migrating:") {
		t.Fatalf("err = %v", err)
	}
	d, err = openWith(t, path, twoMigrations()[:1], MigrateAuto)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if v := version(t, d); v != 1 {
		t.Errorf("version = %d", v)
	}
}

func TestBackupNameCollisionAddsSuffix(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.db")
	list := []Migration{
		{1, "a", "CREATE TABLE a (x)"},
		{2, "b", "CREATE TABLE b (x)"},
		{3, "c", "CREATE TABLE c (x)"},
	}
	for _, n := range []int{1, 2, 3} {
		if _, err := Migrate(context.Background(), path, Options{Migrations: list[:n], Now: fixedNow()}); err != nil {
			t.Fatal(err)
		}
	}
	var names []string
	for _, f := range backupFiles(t, filepath.Join(dir, "backups"), "*.db") {
		names = append(names, filepath.Base(f))
	}
	want := []string{"pre-migrate-v1-20260102T030405Z.db", "pre-migrate-v2-20260102T030405Z.db"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("names = %v", names)
	}

	// Same version and instant again: the suffix appears.
	d, err := Open(context.Background(), path, Options{Migrations: list[:3], Now: fixedNow()})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := d.backupBeforeMigrating(context.Background(), 2, Options{Now: fixedNow()}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "backups", "pre-migrate-v2-20260102T030405Z-2.db")); err != nil {
		t.Error(err)
	}
}

func TestPruneKeepsNewest(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.db")
	var list []Migration
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	for i := 1; i <= 8; i++ {
		list = append(list, Migration{i, "m", "CREATE TABLE t" + string(rune('a'+i)) + " (x)"})
		now := at.Add(time.Duration(i) * time.Minute)
		if _, err := Migrate(context.Background(), path, Options{Migrations: list, BackupKeep: 3, Now: func() time.Time { return now }}); err != nil {
			t.Fatal(err)
		}
	}
	var names []string
	for _, f := range backupFiles(t, filepath.Join(dir, "backups"), "pre-migrate-v*-*.db") {
		names = append(names, filepath.Base(f))
	}
	want := []string{
		"pre-migrate-v5-20260102T031005Z.db",
		"pre-migrate-v6-20260102T031105Z.db",
		"pre-migrate-v7-20260102T031205Z.db",
	}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("names = %v, want %v", names, want)
	}
}

func TestPruneLeavesOtherFiles(t *testing.T) {
	dir := t.TempDir()
	bdir := filepath.Join(dir, "backups")
	if err := os.MkdirAll(bdir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"manual-1.db", "notes.txt", "pre-migrate-v0-20200101T000000Z.db"} {
		if err := os.WriteFile(filepath.Join(bdir, n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(dir, "s.db")
	if _, err := Migrate(context.Background(), path, Options{Migrations: twoMigrations()[:1]}); err != nil {
		t.Fatal(err)
	}
	if _, err := Migrate(context.Background(), path, Options{Migrations: twoMigrations(), BackupKeep: 1, Now: fixedNow()}); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"manual-1.db", "notes.txt", "pre-migrate-v1-20260102T030405Z.db"} {
		if _, err := os.Stat(filepath.Join(bdir, n)); err != nil {
			t.Errorf("%s: %v", n, err)
		}
	}
	if _, err := os.Stat(filepath.Join(bdir, "pre-migrate-v0-20200101T000000Z.db")); err == nil {
		t.Error("older pre-migrate backup survived")
	}
}

func TestBackupKeepDefaultsToFive(t *testing.T) {
	if got := (Options{}).backupKeep(); got != 5 {
		t.Errorf("zero = %d", got)
	}
	if got := (Options{BackupKeep: -3}).backupKeep(); got != 5 {
		t.Errorf("negative = %d", got)
	}
	if got := (Options{BackupKeep: 2}).backupKeep(); got != 2 {
		t.Errorf("explicit = %d", got)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "s.db")
	var list []Migration
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	for i := 1; i <= 8; i++ {
		list = append(list, Migration{i, "m", "CREATE TABLE t" + string(rune('a'+i)) + " (x)"})
		now := at.Add(time.Duration(i) * time.Minute)
		if _, err := Migrate(context.Background(), path, Options{Migrations: list, Now: func() time.Time { return now }}); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(backupFiles(t, filepath.Join(dir, "backups"), "pre-migrate-v*-*.db")); n != 5 {
		t.Errorf("backups = %d, want 5", n)
	}
}
