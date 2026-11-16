package testutil

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/bengearig/ralph/internal/db"
)

// NewDB opens a fresh database in a temporary directory and closes it when the
// test ends. A nil migrations slice means no migrations, not the production list.
func NewDB(t testing.TB, migrations []db.Migration) *db.DB {
	t.Helper()
	if migrations == nil {
		migrations = []db.Migration{}
	}
	path := filepath.Join(t.TempDir(), "store.db")
	d, err := db.Open(context.Background(), path, db.Options{Migrations: migrations})
	if err != nil {
		t.Fatalf("testutil: open test database %s: %v", path, err)
	}
	t.Cleanup(func() {
		if err := d.Close(); err != nil {
			t.Errorf("testutil: close test database %s: %v", path, err)
		}
	})
	return d
}
