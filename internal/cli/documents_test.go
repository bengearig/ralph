package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/bengearig/ralph/internal/config"
	"github.com/bengearig/ralph/internal/db"
	"github.com/bengearig/ralph/internal/documents"
	"github.com/bengearig/ralph/internal/testutil"
)

func TestNewDocumentsServiceUsesKeepSetting(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".ralph"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".ralph", "project.yaml"), []byte("documents:\n  versions:\n    keep: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(config.Options{
		Registry:    config.DefaultRegistry(),
		UserFile:    filepath.Join(t.TempDir(), "user.yaml"),
		ProjectRoot: root,
		Getenv:      func(string) (string, bool) { return "", false },
	})
	if err != nil {
		t.Fatal(err)
	}
	svc := newDocumentsService(&app{root: root, cfg: cfg}, testutil.NewDB(t, db.Migrations))

	ctx := context.Background()
	d, err := svc.Create(ctx, documents.NewDocument{Kind: documents.KindDesign, Title: "t", Body: "one"})
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{"two", "three"} {
		if d, err = svc.Update(ctx, d.ID, d.Fingerprint, documents.Change{Body: &body}); err != nil {
			t.Fatal(err)
		}
	}
	vs, err := svc.Versions(ctx, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 1 || vs[0].Version != 2 {
		t.Errorf("versions = %+v, want only version 2", vs)
	}
}
