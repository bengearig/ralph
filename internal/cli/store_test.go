package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bengearig/ralph/internal/db"
	"github.com/bengearig/ralph/internal/testutil"
)

func TestMigrateCommandCreatesStore(t *testing.T) {
	repo := inRepo(t)
	code, stdout, stderr := run(t, "migrate")
	if code != 0 || stdout != "schema version 0\n" {
		t.Fatalf("code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
	if _, err := os.Stat(filepath.Join(repo.Dir, ".ralph", "store.db")); err != nil {
		t.Error(err)
	}
}

func TestMigrateOutsideRepository(t *testing.T) {
	testutil.IsolateEnv(t)
	t.Chdir(t.TempDir())
	code, _, stderr := run(t, "migrate")
	if code != 1 || !strings.Contains(stderr, "not in a git repository") {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
}

func TestMigrateRefusesForeignDatabase(t *testing.T) {
	repo := inRepo(t)
	repo.WriteFile(".ralph/store.db", "hello")
	code, _, stderr := run(t, "migrate")
	if code != 1 || !strings.Contains(stderr, "not a ralph database") {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
}

func TestMigrateUsesBackupKeepSetting(t *testing.T) {
	repo := inRepo(t)
	t.Setenv("RALPH_STORAGE_BACKUPS_KEEP", "2")
	all := []db.Migration{
		{Version: 1, Name: "one", SQL: "CREATE TABLE a (x)"},
		{Version: 2, Name: "two", SQL: "CREATE TABLE b (x)"},
		{Version: 3, Name: "three", SQL: "CREATE TABLE c (x)"},
		{Version: 4, Name: "four", SQL: "CREATE TABLE d (x)"},
	}
	old := db.Migrations
	t.Cleanup(func() { db.Migrations = old })
	for n := 1; n <= len(all); n++ {
		db.Migrations = all[:n]
		if code, _, stderr := run(t, "migrate"); code != 0 {
			t.Fatalf("migrate %d: code = %d, stderr = %q", n, code, stderr)
		}
	}
	matches, err := filepath.Glob(filepath.Join(repo.Dir, ".ralph", "backups", "pre-migrate-v*.db"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 2 {
		t.Fatalf("backups = %v, want 2", matches)
	}
}
