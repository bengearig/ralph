package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/bengearig/ralph/internal/testutil"
	"github.com/bengearig/ralph/internal/units"
)

func TestActorContextResolvesGitIdentity(t *testing.T) {
	r := testutil.NewRepo(t)
	t.Chdir(r.Dir)
	a := &app{}
	ctx := a.actorContext(context.Background(), NewRootCommand())
	if got, want := units.ActorFrom(ctx), "Test User <test@example.com>"; got != want {
		t.Errorf("actor = %q, want %q", got, want)
	}
}

func TestActorContextIsLazy(t *testing.T) {
	bin, marker := t.TempDir(), filepath.Join(t.TempDir(), "ran")
	script := "#!/bin/sh\ntouch '" + marker + "'\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	testutil.IsolateEnv(t)
	t.Chdir(t.TempDir())

	if code, _, stderr := run(t, "version"); code != 0 {
		t.Fatalf("version exited %d: %s", code, stderr)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("git ran during version")
	}
}
