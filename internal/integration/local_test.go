package integration

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/bengearig/ralph/internal/project"
)

func writeLocalManifest(t *testing.T, root, content string) string {
	t.Helper()
	path := filepath.Join(project.RalphDir(root), project.IntegrationLocalFile)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLocalPathsMissingManifest(t *testing.T) {
	got, err := LocalPaths(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("paths = %v", got)
	}
}

func TestLocalPathsListsSortedUnique(t *testing.T) {
	root := t.TempDir()
	writeLocalManifest(t, root, `{"version":1,"entries":[
		{"id":"c","path":"z/last.json"},
		{"id":"a","path":".mcp.json"},
		{"id":"b","path":".mcp.json","keyPath":["x"]}]}`)
	got, err := LocalPaths(root)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{".mcp.json", "z/last.json"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("paths = %v, want %v", got, want)
	}
}

func TestLocalPathsInvalidManifestNamesFile(t *testing.T) {
	root := t.TempDir()
	path := writeLocalManifest(t, root, "{nope")
	_, err := LocalPaths(root)
	if err == nil || !strings.Contains(err.Error(), path) {
		t.Fatalf("err = %v", err)
	}
}
