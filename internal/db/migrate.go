package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

var (
	ErrNewerDatabase     = errors.New("database was written by a newer ralph")
	ErrMigrationRequired = errors.New("database migration required")
)

// Migration is one forward-only schema change; SQL may hold several statements.
type Migration struct {
	Version int
	Name    string
	SQL     string
}

// Migrations is the production list, applied in order.
var Migrations = []Migration{
	{Version: 1, Name: "create units", SQL: createUnitsSQL},
	{Version: 2, Name: "create links", SQL: createLinksSQL},
	{Version: 3, Name: "create notes", SQL: createNotesSQL},
	{Version: 4, Name: "record actors", SQL: recordActorsSQL},
	{Version: 5, Name: "create dependencies", SQL: createDependenciesSQL},
	{Version: 6, Name: "create documents", SQL: createDocumentsSQL},
	{Version: 7, Name: "create document versions", SQL: createDocumentVersionsSQL},
}

// MigrateMode says what Open does when migrations are pending.
type MigrateMode int

const (
	MigrateAuto MigrateMode = iota
	MigrateManual
)

// MigrateResult reports what Migrate changed.
type MigrateResult struct {
	From, To int
	Applied  []string
}

// SchemaVersion returns the database's PRAGMA user_version.
func (d *DB) SchemaVersion(ctx context.Context) (int, error) {
	var v int
	if err := d.sql.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v); err != nil {
		return 0, fmt.Errorf("db: read schema version of %q: %w", d.path, err)
	}
	return v, nil
}

// Migrate opens the database at path in MigrateAuto mode and reports the change.
func Migrate(ctx context.Context, path string, opts Options) (MigrateResult, error) {
	opts.Mode = MigrateAuto
	d, from, err := open(ctx, path, opts)
	if err != nil {
		return MigrateResult{}, err
	}
	to, err := d.SchemaVersion(ctx)
	if cerr := d.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return MigrateResult{}, err
	}
	res := MigrateResult{From: from, To: to}
	for _, m := range opts.list() {
		if m.Version > from && m.Version <= to {
			res.Applied = append(res.Applied, m.Name)
		}
	}
	return res, nil
}

func (o Options) list() []Migration {
	if o.Migrations == nil {
		return Migrations
	}
	return o.Migrations
}

func validateMigrations(list []Migration) error {
	for i, m := range list {
		if want := i + 1; m.Version != want {
			return fmt.Errorf("db: migration %q has version %d, want %d: versions must be 1, 2, 3 and so on in order", m.Name, m.Version, want)
		}
	}
	return nil
}

func (d *DB) migrate(ctx context.Context, current int, list []Migration, opts Options, fresh bool) error {
	newest := len(list)
	if current > newest {
		return fmt.Errorf("db: %q is at schema version %d but this ralph knows up to version %d; upgrade ralph: %w", d.path, current, newest, ErrNewerDatabase)
	}
	if current == newest {
		return nil
	}
	if opts.Mode == MigrateManual {
		return fmt.Errorf("db: %q is at schema version %d and version %d is available; run `ralph migrate`: %w", d.path, current, newest, ErrMigrationRequired)
	}
	if !fresh {
		if err := d.backupBeforeMigrating(ctx, current, opts); err != nil {
			return fmt.Errorf("back up before migrating: %w", err)
		}
	}
	for _, m := range list[current:] {
		err := d.WriteTx(ctx, func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, m.SQL); err != nil {
				return err
			}
			_, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", m.Version))
			return err
		})
		if err != nil {
			return fmt.Errorf("db: migration %d (%s) failed: %w", m.Version, m.Name, err)
		}
	}
	d.prune(opts)
	return nil
}
