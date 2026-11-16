package project

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

var uuidV4 = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func projectPath(root string) string { return filepath.Join(root, DirName, ProjectFile) }

func writeProject(t *testing.T, root, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, DirName), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(projectPath(root), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readProject(t *testing.T, root string) string {
	t.Helper()
	b, err := os.ReadFile(projectPath(root))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func parsedID(t *testing.T, root string) string {
	t.Helper()
	var v struct {
		Project struct {
			ID string `yaml:"id"`
		} `yaml:"project"`
	}
	if err := yaml.Unmarshal([]byte(readProject(t, root)), &v); err != nil {
		t.Fatal(err)
	}
	return v.Project.ID
}

func TestNewProjectIDShape(t *testing.T) {
	id, err := NewProjectID()
	if err != nil {
		t.Fatal(err)
	}
	if !uuidV4.MatchString(id) {
		t.Fatalf("id = %q", id)
	}
}

func TestNewProjectIDsDiffer(t *testing.T) {
	a, _ := NewProjectID()
	b, _ := NewProjectID()
	if a == b {
		t.Fatalf("both ids = %q", a)
	}
}

func TestEnsureProjectFileCreates(t *testing.T) {
	root := t.TempDir()
	id, changed, err := EnsureProjectFile(root)
	if err != nil || !changed {
		t.Fatalf("changed = %v, err = %v", changed, err)
	}
	if !uuidV4.MatchString(id) || parsedID(t, root) != id {
		t.Fatalf("id = %q, file = %q", id, readProject(t, root))
	}
}

func TestEnsureProjectFileKeepsExistingID(t *testing.T) {
	root := t.TempDir()
	content := "# keep\nproject:   {id:   abc}\n"
	writeProject(t, root, content)
	id, changed, err := EnsureProjectFile(root)
	if err != nil || changed || id != "abc" {
		t.Fatalf("id = %q, changed = %v, err = %v", id, changed, err)
	}
	if got := readProject(t, root); got != content {
		t.Fatalf("file = %q", got)
	}
}

func TestEnsureProjectFileFillsMissingID(t *testing.T) {
	root := t.TempDir()
	writeProject(t, root, "# my comment\nlog: {level: debug}\n")
	id, changed, err := EnsureProjectFile(root)
	if err != nil || !changed {
		t.Fatalf("changed = %v, err = %v", changed, err)
	}
	got := readProject(t, root)
	if !strings.Contains(got, "# my comment") || !strings.Contains(got, "level: debug") || parsedID(t, root) != id {
		t.Fatalf("file = %q", got)
	}
}

func TestEnsureProjectFileReplacesBlankID(t *testing.T) {
	for _, content := range []string{"project:\n  id: \"\"\n  other: x\n", "project:\n  id:\n  other: x\n", "project:\n", "project:\n  id: '  '\n"} {
		root := t.TempDir()
		writeProject(t, root, content)
		id, changed, err := EnsureProjectFile(root)
		if err != nil || !changed || !uuidV4.MatchString(id) || parsedID(t, root) != id {
			t.Fatalf("%q: id = %q, changed = %v, err = %v, file = %q", content, id, changed, err, readProject(t, root))
		}
		if strings.Contains(content, "other") && !strings.Contains(readProject(t, root), "other: x") {
			t.Fatalf("lost key: %q", readProject(t, root))
		}
	}
}

func TestEnsureProjectFileInvalidYAML(t *testing.T) {
	root := t.TempDir()
	content := "project: [unclosed\n"
	writeProject(t, root, content)
	_, _, err := EnsureProjectFile(root)
	if err == nil || !strings.Contains(err.Error(), projectPath(root)) {
		t.Fatalf("err = %v", err)
	}
	if got := readProject(t, root); got != content {
		t.Fatalf("file = %q", got)
	}
}

func TestEnsureProjectFileCommentsOnly(t *testing.T) {
	root := t.TempDir()
	writeProject(t, root, "# just a note\n")
	id, changed, err := EnsureProjectFile(root)
	if err != nil || !changed {
		t.Fatalf("changed = %v, err = %v", changed, err)
	}
	if got := readProject(t, root); !strings.HasPrefix(got, "# just a note\n") || parsedID(t, root) != id {
		t.Fatalf("file = %q", got)
	}
}
