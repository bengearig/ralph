// Package db opens ralph's SQLite database and guards it against foreign files.
package db

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// ApplicationID is stored in the database header: the ASCII letters RALP.
const ApplicationID = 0x52414C50

var ErrForeignDatabase = errors.New("not a ralph database")

var sqliteMagic = []byte("SQLite format 3\x00")

type Options struct {
	// Migrations nil selects the production list; a non-nil slice is used as given.
	Migrations []Migration
	Mode       MigrateMode
	// BackupDir defaults to a backups directory next to the database file.
	BackupDir string
	// BackupKeep is how many pre-migration backups to retain; zero or less means 5.
	BackupKeep int
	// Now defaults to time.Now.
	Now func() time.Time
}

type DB struct {
	sql  *sql.DB
	path string
}

// Open opens the database at path, creating and stamping it when it is new.
// A file that is not a ralph database is refused without being modified.
func Open(ctx context.Context, path string, opts Options) (*DB, error) {
	d, _, err := open(ctx, path, opts)
	return d, err
}

// open also returns the schema version found before any migration ran.
func open(ctx context.Context, path string, opts Options) (*DB, int, error) {
	list := opts.list()
	if err := validateMigrations(list); err != nil {
		return nil, 0, err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, 0, fmt.Errorf("db: resolve %q: %w", path, err)
	}
	if err := checkOwnership(ctx, abs); err != nil {
		return nil, 0, err
	}
	fresh := isFreshFile(abs)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return nil, 0, fmt.Errorf("db: create directory for %q: %w", abs, err)
	}

	sdb, err := sql.Open("sqlite", dsn(abs, url.Values{
		"_pragma": {
			"busy_timeout(5000)",
			"journal_mode(WAL)",
			"foreign_keys(1)",
			"synchronous(NORMAL)",
		},
		"_txlock": {"immediate"},
	}))
	if err != nil {
		return nil, 0, fmt.Errorf("db: open %q: %w", abs, err)
	}
	d := &DB{sql: sdb, path: abs}
	if err := d.stamp(ctx); err != nil {
		_ = sdb.Close()
		return nil, 0, err
	}
	from, err := d.SchemaVersion(ctx)
	if err == nil {
		err = d.migrate(ctx, from, list, opts, fresh)
	}
	if err != nil {
		_ = sdb.Close()
		return nil, 0, err
	}
	return d, from, nil
}

// isFreshFile reports whether opening path will create it or find it empty.
func isFreshFile(path string) bool {
	info, err := os.Stat(path)
	return errors.Is(err, os.ErrNotExist) || (err == nil && info.Size() == 0)
}

func (d *DB) SQL() *sql.DB { return d.sql }

func (d *DB) Path() string { return d.path }

func (d *DB) Close() error { return d.sql.Close() }

// WriteTx runs fn in a transaction begun with BEGIN IMMEDIATE. It commits when
// fn returns nil and rolls back on an error or a panic, which it re-raises.
func (d *DB) WriteTx(ctx context.Context, fn func(*sql.Tx) error) (err error) {
	tx, err := d.sql.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("db: begin write transaction: %w", err)
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
	}()
	if err := fn(tx); err != nil {
		if rbErr := tx.Rollback(); rbErr != nil {
			return errors.Join(err, fmt.Errorf("db: rollback: %w", rbErr))
		}
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("db: commit: %w", err)
	}
	return nil
}

func (d *DB) stamp(ctx context.Context) error {
	conn, err := d.sql.Conn(ctx)
	if err != nil {
		return fmt.Errorf("db: open %q: %w", d.path, err)
	}
	defer conn.Close()

	var id int64
	if err := conn.QueryRowContext(ctx, "PRAGMA application_id").Scan(&id); err != nil {
		return fmt.Errorf("db: read application id of %q: %w", d.path, err)
	}
	switch id {
	case ApplicationID:
		return nil
	case 0:
		if _, err := conn.ExecContext(ctx, fmt.Sprintf("PRAGMA application_id = %d", ApplicationID)); err != nil {
			return fmt.Errorf("db: stamp %q: %w", d.path, err)
		}
		return nil
	default:
		return foreignError(d.path)
	}
}

func foreignError(path string) error {
	return fmt.Errorf("db: %q is not a ralph database: %w", path, ErrForeignDatabase)
}

// checkOwnership inspects an existing file through a read-only connection so a
// refused file is never written to.
func checkOwnership(ctx context.Context, path string) error {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("db: stat %q: %w", path, err)
	}
	if info.IsDir() {
		return fmt.Errorf("db: %q is a directory: %w", path, ErrForeignDatabase)
	}
	if info.Size() == 0 {
		return nil
	}

	isSQLite, err := hasSQLiteHeader(path)
	if err != nil {
		return err
	}
	if !isSQLite {
		return foreignError(path)
	}

	ro, err := sql.Open("sqlite", dsn(path, url.Values{
		"mode":    {"ro"},
		"_pragma": {"busy_timeout(5000)"},
	}))
	if err != nil {
		return fmt.Errorf("db: inspect %q: %w", path, err)
	}
	defer ro.Close()

	var id, objects int64
	if err := ro.QueryRowContext(ctx, "PRAGMA application_id").Scan(&id); err != nil {
		return fmt.Errorf("db: inspect %q: %w", path, err)
	}
	if err := ro.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master").Scan(&objects); err != nil {
		return fmt.Errorf("db: inspect %q: %w", path, err)
	}
	if id == ApplicationID || (id == 0 && objects == 0) {
		return nil
	}
	return foreignError(path)
}

func hasSQLiteHeader(path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, fmt.Errorf("db: inspect %q: %w", path, err)
	}
	defer f.Close()
	head := make([]byte, len(sqliteMagic))
	if _, err := io.ReadFull(f, head); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return false, nil
		}
		return false, fmt.Errorf("db: inspect %q: %w", path, err)
	}
	return bytes.Equal(head, sqliteMagic), nil
}

func dsn(path string, q url.Values) string {
	u := url.URL{Path: path}
	return "file:" + u.EscapedPath() + "?" + q.Encode()
}
