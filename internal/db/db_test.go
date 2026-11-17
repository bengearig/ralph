package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func openT(t *testing.T, path string) *DB {
	t.Helper()
	d, err := Open(context.Background(), path, Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := d.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	})
	return d
}

func pragmaInt(t *testing.T, d *sql.DB, name string) int64 {
	t.Helper()
	var v int64
	if err := d.QueryRow("PRAGMA " + name).Scan(&v); err != nil {
		t.Fatalf("PRAGMA %s: %v", name, err)
	}
	return v
}

func rawDB(t *testing.T, path string, stmts ...string) {
	t.Helper()
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range stmts {
		if _, err := raw.Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
}

func snapshot(t *testing.T, path string) (hash [32]byte, entries []string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	list, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range list {
		entries = append(entries, e.Name())
	}
	return sha256.Sum256(b), entries
}

func assertUntouched(t *testing.T, path string, hash [32]byte, entries []string) {
	t.Helper()
	h, e := snapshot(t, path)
	if h != hash {
		t.Error("file content changed")
	}
	if strings.Join(e, ",") != strings.Join(entries, ",") {
		t.Errorf("directory changed: before %v, after %v", entries, e)
	}
}

func TestOpenCreatesAndStamps(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing", "dir", "store.db")
	d := openT(t, path)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("file missing: %v", err)
	}
	if got := pragmaInt(t, d.SQL(), "application_id"); got != 0x52414C50 {
		t.Errorf("application_id = %#x", got)
	}
	var mode string
	if err := d.SQL().QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil || mode != "wal" {
		t.Errorf("journal_mode = %q, %v", mode, err)
	}
	if got := pragmaInt(t, d.SQL(), "foreign_keys"); got != 1 {
		t.Errorf("foreign_keys = %d", got)
	}
	if got := pragmaInt(t, d.SQL(), "busy_timeout"); got != 5000 {
		t.Errorf("busy_timeout = %d", got)
	}
	if got := pragmaInt(t, d.SQL(), "synchronous"); got != 1 {
		t.Errorf("synchronous = %d", got)
	}
}

func TestOpenReopensExistingDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.db")
	d := openT(t, path)
	if _, err := d.SQL().Exec("CREATE TABLE t (x)"); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	d2 := openT(t, path)
	var n int
	if err := d2.SQL().QueryRow("SELECT count(*) FROM sqlite_master WHERE name = 't'").Scan(&n); err != nil || n != 1 {
		t.Fatalf("table lost: n=%d err=%v", n, err)
	}
	if got := pragmaInt(t, d2.SQL(), "application_id"); got != 0x52414C50 {
		t.Errorf("application_id = %#x", got)
	}
}

func TestOpenAcceptsEmptyExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.db")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	d := openT(t, path)
	if got := pragmaInt(t, d.SQL(), "application_id"); got != 0x52414C50 {
		t.Errorf("application_id = %#x", got)
	}
}

func TestOpenAcceptsUnstampedEmptyDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.db")
	rawDB(t, path, "PRAGMA user_version = 0", "PRAGMA application_id = 0")
	d := openT(t, path)
	if got := pragmaInt(t, d.SQL(), "application_id"); got != 0x52414C50 {
		t.Errorf("application_id = %#x", got)
	}
}

func TestOpenRefusesForeignDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.db")
	rawDB(t, path, "CREATE TABLE t (x)")
	hash, entries := snapshot(t, path)
	_, err := Open(context.Background(), path, Options{})
	if !errors.Is(err, ErrForeignDatabase) {
		t.Fatalf("err = %v, want ErrForeignDatabase", err)
	}
	assertUntouched(t, path, hash, entries)
}

func TestOpenRefusesOtherApplicationID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.db")
	rawDB(t, path, "PRAGMA application_id = 12345")
	hash, entries := snapshot(t, path)
	_, err := Open(context.Background(), path, Options{})
	if !errors.Is(err, ErrForeignDatabase) {
		t.Fatalf("err = %v, want ErrForeignDatabase", err)
	}
	assertUntouched(t, path, hash, entries)
}

func TestOpenRefusesNonSQLiteFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.db")
	if err := os.WriteFile(path, []byte("just some text, not a database\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	hash, entries := snapshot(t, path)
	_, err := Open(context.Background(), path, Options{})
	if !errors.Is(err, ErrForeignDatabase) {
		t.Fatalf("err = %v, want ErrForeignDatabase", err)
	}
	assertUntouched(t, path, hash, entries)
}

func TestForeignErrorNamesPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.db")
	if err := os.WriteFile(path, []byte("text"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Open(context.Background(), path, Options{})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "not a ralph database") {
		t.Errorf("error = %q", err)
	}
}

func newTable(t *testing.T) *DB {
	t.Helper()
	d := openT(t, filepath.Join(t.TempDir(), "store.db"))
	if _, err := d.SQL().Exec("CREATE TABLE t (x INTEGER)"); err != nil {
		t.Fatal(err)
	}
	return d
}

func countRows(t *testing.T, d *DB) int {
	t.Helper()
	var n int
	if err := d.SQL().QueryRow("SELECT count(*) FROM t").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestWriteTxCommits(t *testing.T) {
	d := newTable(t)
	err := d.WriteTx(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.Exec("INSERT INTO t VALUES (1)")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, d); n != 1 {
		t.Errorf("rows = %d", n)
	}
}

func TestWriteTxRollsBackOnError(t *testing.T) {
	d := newTable(t)
	sentinel := errors.New("boom")
	err := d.WriteTx(context.Background(), func(tx *sql.Tx) error {
		if _, err := tx.Exec("INSERT INTO t VALUES (1)"); err != nil {
			return err
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v", err)
	}
	if n := countRows(t, d); n != 0 {
		t.Errorf("rows = %d", n)
	}
}

func TestWriteTxRollsBackOnPanic(t *testing.T) {
	d := newTable(t)
	func() {
		defer func() {
			if r := recover(); r != "kaboom" {
				t.Errorf("recovered %v", r)
			}
		}()
		_ = d.WriteTx(context.Background(), func(tx *sql.Tx) error {
			if _, err := tx.Exec("INSERT INTO t VALUES (1)"); err != nil {
				return err
			}
			panic("kaboom")
		})
		t.Error("panic did not propagate")
	}()
	if n := countRows(t, d); n != 0 {
		t.Errorf("rows = %d", n)
	}
}

func TestWriteTxConcurrentWriters(t *testing.T) {
	d := newTable(t)
	const writers, perWriter = 8, 25
	var wg sync.WaitGroup
	errs := make(chan error, writers*perWriter)
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				errs <- d.WriteTx(context.Background(), func(tx *sql.Tx) error {
					_, err := tx.Exec("INSERT INTO t VALUES (?)", i)
					return err
				})
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("WriteTx: %v", err)
		}
	}
	if n := countRows(t, d); n != writers*perWriter {
		t.Errorf("rows = %d, want %d", n, writers*perWriter)
	}
}

func TestOpenMustExistMissingFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sub")
	path := filepath.Join(dir, "store.db")
	_, err := Open(context.Background(), path, Options{MustExist: true})
	if err == nil || !strings.Contains(err.Error(), path) {
		t.Fatalf("err = %v, want one naming %q", err, path)
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("directory was created: %v", err)
	}
}

func TestOpenMustExistExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.db")
	_ = openT(t, path).Close()
	d, err := Open(context.Background(), path, Options{MustExist: true})
	if err != nil {
		t.Fatal(err)
	}
	_ = d.Close()
}

func TestOpenNoMigrateSkipsPending(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "store.db")
	d, err := openWith(t, path, three()[:1], MigrateAuto)
	if err != nil {
		t.Fatal(err)
	}
	_ = d.Close()
	d, err = Open(context.Background(), path, Options{Migrations: three(), NoMigrate: true, BackupDir: filepath.Join(dir, "backups")})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	if v := version(t, d); v != 1 {
		t.Errorf("version = %d, want 1", v)
	}
	if _, err := os.Stat(filepath.Join(dir, "backups")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("backups directory exists: %v", err)
	}
}

func TestOpenNoMigrateAllowsNewerDatabase(t *testing.T) {
	path := newerAt5(t)
	d, err := Open(context.Background(), path, Options{Migrations: three(), NoMigrate: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	if v := version(t, d); v != 5 {
		t.Errorf("version = %d, want 5", v)
	}
}

func TestOpenNoMigrateStillChecksOwnership(t *testing.T) {
	path := filepath.Join(t.TempDir(), "other.db")
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Open(context.Background(), path, Options{NoMigrate: true})
	if !errors.Is(err, ErrForeignDatabase) {
		t.Fatalf("err = %v", err)
	}
}

func TestWritesCountsCommittedTransactions(t *testing.T) {
	d := newTable(t)
	before := Writes()
	for i := 0; i < 3; i++ {
		err := d.WriteTx(context.Background(), func(tx *sql.Tx) error {
			_, err := tx.Exec("INSERT INTO t VALUES (?)", i)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if got := Writes() - before; got != 3 {
		t.Errorf("writes counted = %d, want 3", got)
	}
}

func TestWritesNotCountedOnRollback(t *testing.T) {
	d := newTable(t)
	before := Writes()
	_ = d.WriteTx(context.Background(), func(tx *sql.Tx) error { return errors.New("boom") })
	func() {
		defer func() { _ = recover() }()
		_ = d.WriteTx(context.Background(), func(tx *sql.Tx) error { panic("kaboom") })
	}()
	if got := Writes() - before; got != 0 {
		t.Errorf("writes counted = %d, want 0", got)
	}
}

func TestWritesConcurrent(t *testing.T) {
	d := newTable(t)
	before := Writes()
	const writers, perWriter = 6, 10
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				_ = d.WriteTx(context.Background(), func(tx *sql.Tx) error {
					_, err := tx.Exec("INSERT INTO t VALUES (?)", i)
					return err
				})
				_ = Writes()
			}
		}()
	}
	wg.Wait()
	if got := Writes() - before; got != writers*perWriter {
		t.Errorf("writes counted = %d, want %d", got, writers*perWriter)
	}
}
