package docexport

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

func wantRefused(t *testing.T, root, rel string) {
	t.Helper()
	got, err := ResolveDir(root, rel)
	if err == nil || !strings.Contains(err.Error(), "documents.export.dir") {
		t.Fatalf("ResolveDir(%q) = %q, %v; want an error naming the setting", rel, got, err)
	}
}

func TestResolveDirInsideRoot(t *testing.T) {
	root := newRoot(t)
	got, err := ResolveDir(root, "docs/ralph")
	if err != nil || got != filepath.Join(root, "docs", "ralph") {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestResolveDirCreatesNothing(t *testing.T) {
	root := newRoot(t)
	if _, err := ResolveDir(root, "a/b/c"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(root, "a")); !os.IsNotExist(err) {
		t.Fatalf("directory was created: %v", err)
	}
}

func TestResolveDirRefusesSymlinkEscape(t *testing.T) {
	root := newRoot(t)
	if err := os.Symlink(t.TempDir(), filepath.Join(root, "out")); err != nil {
		t.Fatal(err)
	}
	wantRefused(t, root, "out")
	wantRefused(t, root, "out/ralph")
}

func TestResolveDirRefusesSymlinkIntoGit(t *testing.T) {
	root := newRoot(t)
	if err := os.Symlink(".git", filepath.Join(root, "docs")); err != nil {
		t.Fatal(err)
	}
	wantRefused(t, root, "docs")
	wantRefused(t, root, "docs/sub")
}

func TestResolveDirRefusesFileComponent(t *testing.T) {
	root := newRoot(t)
	if err := os.WriteFile(filepath.Join(root, "docs"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	wantRefused(t, root, "docs")
	wantRefused(t, root, "docs/ralph")
}

func TestResolveDirAllowsSymlinkInsideRoot(t *testing.T) {
	root := newRoot(t)
	if err := os.Mkdir(filepath.Join(root, "real"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("real", filepath.Join(root, "docs")); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveDir(root, "docs/ralph")
	if err != nil || got != filepath.Join(root, "real", "ralph") {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestResolveDirErrorNamesSetting(t *testing.T) {
	root := newRoot(t)
	for _, rel := range []string{"", ".", "..", "../x", ".git", ".ralph/docs", "/abs"} {
		wantRefused(t, root, rel)
	}
}

func TestResolveDirResolvesRootSymlink(t *testing.T) {
	root := newRoot(t)
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveDir(link, "docs")
	if err != nil || got != filepath.Join(root, "docs") {
		t.Fatalf("got %q, %v", got, err)
	}
}
