package cli

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bengearig/ralph/internal/db"
	"github.com/bengearig/ralph/internal/testutil"
)

func seedStore(t *testing.T, repo *testutil.Repo, migrations []db.Migration) {
	t.Helper()
	if migrations == nil {
		migrations = []db.Migration{}
	}
	d, err := db.Open(context.Background(), filepath.Join(repo.Dir, ".ralph", "store.db"), db.Options{Migrations: migrations})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	for _, s := range []string{"CREATE TABLE t (x)", "INSERT INTO t VALUES (1), (2), (3)"} {
		if _, err := d.SQL().Exec(s); err != nil {
			t.Fatal(err)
		}
	}
}

func countT(t *testing.T, path string) int {
	t.Helper()
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = raw.Close() }()
	var n int
	if err := raw.QueryRow("SELECT count(*) FROM t").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestBackupCommandWritesSnapshot(t *testing.T) {
	repo := inRepo(t)
	seedStore(t, repo, nil)
	code, stdout, stderr := run(t, "backup")
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	path := strings.TrimSuffix(stdout, "\n")
	if strings.Contains(path, "\n") {
		t.Fatalf("stdout = %q, want one line", stdout)
	}
	if ok, _ := filepath.Match("manual-*.db", filepath.Base(path)); !ok {
		t.Errorf("name = %q", filepath.Base(path))
	}
	if want := filepath.Join(repo.Dir, ".ralph", "backups"); filepath.Dir(path) != want {
		t.Errorf("dir = %q, want %q", filepath.Dir(path), want)
	}
	if n := countT(t, path); n != 3 {
		t.Errorf("rows = %d, want 3", n)
	}
}

func TestBackupCommandOutputFlag(t *testing.T) {
	repo := inRepo(t)
	seedStore(t, repo, nil)
	code, stdout, stderr := run(t, "backup", "--output", "snap.db")
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	want := filepath.Join(repo.Dir, "snap.db")
	if got := strings.TrimSpace(stdout); got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
	if n := countT(t, want); n != 3 {
		t.Errorf("rows = %d, want 3", n)
	}
}

func TestBackupCommandRefusesExistingDestination(t *testing.T) {
	repo := inRepo(t)
	seedStore(t, repo, nil)
	repo.WriteFile("snap.db", "keep me")
	code, _, stderr := run(t, "backup", "--output", "snap.db")
	if code != 1 || !strings.Contains(stderr, "already exists") {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	got, err := os.ReadFile(filepath.Join(repo.Dir, "snap.db"))
	if err != nil || string(got) != "keep me" {
		t.Errorf("destination = %q, %v", got, err)
	}
}

func TestBackupCommandNoDatabase(t *testing.T) {
	repo := inRepo(t)
	code, _, stderr := run(t, "backup")
	if code != 1 || !strings.Contains(stderr, "store.db") {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	for _, p := range []string{"store.db", "backups"} {
		if _, err := os.Stat(filepath.Join(repo.Dir, ".ralph", p)); !os.IsNotExist(err) {
			t.Errorf(".ralph/%s exists: %v", p, err)
		}
	}
}

func TestBackupCommandOutsideRepository(t *testing.T) {
	testutil.IsolateEnv(t)
	t.Chdir(t.TempDir())
	code, _, stderr := run(t, "backup")
	if code != 1 || !strings.Contains(stderr, "not in a git repository") {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
}

func TestBackupCommandDoesNotMigrate(t *testing.T) {
	repo := inRepo(t)
	seedStore(t, repo, nil)
	old := db.Migrations
	t.Cleanup(func() { db.Migrations = old })
	db.Migrations = []db.Migration{{Version: 1, Name: "one", SQL: "CREATE TABLE extra (x)"}}
	code, stdout, stderr := run(t, "backup")
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	raw, err := sql.Open("sqlite", filepath.Join(repo.Dir, ".ralph", "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = raw.Close() }()
	var v int
	if err := raw.QueryRow("PRAGMA user_version").Scan(&v); err != nil || v != 0 {
		t.Errorf("version = %d, %v", v, err)
	}
	if _, err := os.Stat(strings.TrimSpace(stdout)); err != nil {
		t.Error(err)
	}
}
