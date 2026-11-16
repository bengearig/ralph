package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

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
