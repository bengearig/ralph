package db

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"time"
)

const (
	defaultBackupKeep = 5
	backupTimeLayout  = "20060102T150405Z"
)

var preMigrateName = regexp.MustCompile(`^pre-migrate-v\d+-(\d{8}T\d{6}Z)(?:-(\d+))?\.db$`)

// Backup writes a consistent snapshot of the database to dest with VACUUM INTO,
// which is safe while another process writes. It refuses an existing dest and
// removes any partial file if the snapshot fails.
func (d *DB) Backup(ctx context.Context, dest string) error {
	if _, err := os.Lstat(dest); err == nil {
		return fmt.Errorf("db: backup destination %q already exists", dest)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("db: backup destination %q: %w", dest, err)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("db: create backup directory for %q: %w", dest, err)
	}
	if _, err := d.sql.ExecContext(ctx, "VACUUM INTO ?", dest); err != nil {
		_ = os.Remove(dest)
		return fmt.Errorf("db: back up %q to %q: %w", d.path, dest, err)
	}
	return nil
}

func (o Options) backupKeep() int {
	if o.BackupKeep <= 0 {
		return defaultBackupKeep
	}
	return o.BackupKeep
}

func (o Options) now() time.Time {
	if o.Now == nil {
		return time.Now()
	}
	return o.Now()
}

func (d *DB) backupDir(opts Options) string {
	if opts.BackupDir != "" {
		return opts.BackupDir
	}
	return filepath.Join(filepath.Dir(d.path), "backups")
}

// backupBeforeMigrating writes the pre-migration snapshot of schema version from.
func (d *DB) backupBeforeMigrating(ctx context.Context, from int, opts Options) error {
	dir := d.backupDir(opts)
	stem := fmt.Sprintf("pre-migrate-v%d-%s", from, opts.now().UTC().Format(backupTimeLayout))
	dest := filepath.Join(dir, stem+".db")
	for n := 2; ; n++ {
		_, err := os.Lstat(dest)
		if errors.Is(err, os.ErrNotExist) {
			break
		}
		if err != nil {
			return fmt.Errorf("db: backup destination %q: %w", dest, err)
		}
		dest = filepath.Join(dir, stem+"-"+strconv.Itoa(n)+".db")
	}
	return d.Backup(ctx, dest)
}

type preMigrateBackup struct {
	name   string
	stamp  string
	suffix int
}

// pruneBackups deletes all but the newest keep pre-migration backups in dir.
func pruneBackups(dir string, keep int) error {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var found []preMigrateBackup
	for _, e := range entries {
		m := preMigrateName.FindStringSubmatch(e.Name())
		if m == nil || !e.Type().IsRegular() {
			continue
		}
		suffix := 1
		if m[2] != "" {
			suffix, _ = strconv.Atoi(m[2])
		}
		found = append(found, preMigrateBackup{name: e.Name(), stamp: m[1], suffix: suffix})
	}
	sort.Slice(found, func(i, j int) bool {
		a, b := found[i], found[j]
		if a.stamp != b.stamp {
			return a.stamp > b.stamp
		}
		if a.suffix != b.suffix {
			return a.suffix > b.suffix
		}
		return a.name > b.name
	})
	if len(found) <= keep {
		return nil
	}
	var errs []error
	for _, b := range found[keep:] {
		if err := os.Remove(filepath.Join(dir, b.name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (d *DB) prune(opts Options) {
	dir := d.backupDir(opts)
	if err := pruneBackups(dir, opts.backupKeep()); err != nil {
		slog.Warn("could not prune old migration backups", "dir", dir, "error", err)
	}
}
