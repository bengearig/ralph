package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/bengearig/ralph/internal/db"
	"github.com/bengearig/ralph/internal/testutil"
	"go.yaml.in/yaml/v3"
)

var projectIDLine = regexp.MustCompile(`(?m)^project id: ([0-9a-f-]{36})$`)

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func fileID(t *testing.T, root string) string {
	t.Helper()
	var v struct {
		Project struct {
			ID string `yaml:"id"`
		} `yaml:"project"`
	}
	if err := yaml.Unmarshal([]byte(readFile(t, filepath.Join(root, ".ralph", "project.yaml"))), &v); err != nil {
		t.Fatal(err)
	}
	return v.Project.ID
}

func TestInitCreatesProject(t *testing.T) {
	repo := inRepo(t)
	code, stdout, stderr := run(t, "init")
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	id := fileID(t, repo.Dir)
	if !regexp.MustCompile(`^[0-9a-f-]{36}$`).MatchString(id) {
		t.Fatalf("id = %q", id)
	}
	for _, want := range []string{"initialized ralph in ", "project id: " + id, "database: .ralph/store.db (schema version " + strconv.Itoa(len(db.Migrations)) + ")"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout = %q, want %q", stdout, want)
		}
	}
	if _, err := os.Stat(filepath.Join(repo.Dir, ".ralph", "store.db")); err != nil {
		t.Error(err)
	}
}

func TestInitIsIdempotent(t *testing.T) {
	repo := inRepo(t)
	run(t, "init")
	id := fileID(t, repo.Dir)
	code, stdout, stderr := run(t, "init")
	if code != 0 || !strings.Contains(stdout, "already set up") || fileID(t, repo.Dir) != id {
		t.Fatalf("code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
}

func TestInitFromSubdirectory(t *testing.T) {
	repo := inRepo(t)
	repo.WriteFile("a/b/file.txt", "x")
	t.Chdir(filepath.Join(repo.Dir, "a", "b"))
	if code, _, stderr := run(t, "init"); code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	if fileID(t, repo.Dir) == "" {
		t.Fatal("no id at root")
	}
	if _, err := os.Stat(filepath.Join(repo.Dir, "a", "b", ".ralph")); err == nil {
		t.Error(".ralph created in subdirectory")
	}
}

func TestInitFromLinkedWorktree(t *testing.T) {
	repo := inRepo(t)
	wt := repo.AddWorktree("wt", "feature")
	t.Chdir(wt)
	if code, _, stderr := run(t, "init"); code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	if fileID(t, repo.Dir) == "" {
		t.Fatal("no id in primary repository")
	}
	if _, err := os.Stat(filepath.Join(wt, ".ralph")); err == nil {
		t.Error(".ralph created in worktree")
	}
}

func TestInitOutsideRepository(t *testing.T) {
	testutil.IsolateEnv(t)
	t.Chdir(t.TempDir())
	code, _, stderr := run(t, "init")
	if code != 1 || !strings.Contains(stderr, "not in a git repository") || !strings.Contains(stderr, "git init") {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
}

func TestInitRepairsBlankIDDespiteConfigError(t *testing.T) {
	repo := inRepo(t)
	repo.WriteFile(".ralph/project.yaml", "project:\n  id: \"\"\n")
	if code, _, _ := run(t, "config", "show"); code == 0 {
		t.Fatal("config show should fail on a blank id")
	}
	if code, _, stderr := run(t, "init"); code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	if fileID(t, repo.Dir) == "" {
		t.Fatal("id still blank")
	}
}

func TestInitInvalidYAMLWritesNothing(t *testing.T) {
	repo := inRepo(t)
	content := "project: [unclosed\n"
	repo.WriteFile(".ralph/project.yaml", content)
	code, _, stderr := run(t, "init")
	if code != 1 || !strings.Contains(stderr, "project.yaml") {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	if got := readFile(t, filepath.Join(repo.Dir, ".ralph", "project.yaml")); got != content {
		t.Errorf("file = %q", got)
	}
	if _, err := os.Stat(filepath.Join(repo.Dir, ".ralph", "store.db")); err == nil {
		t.Error("store.db created")
	}
}

func TestInitForeignDatabaseWritesNothing(t *testing.T) {
	repo := inRepo(t)
	repo.WriteFile(".ralph/store.db", "hello")
	if code, _, _ := run(t, "init"); code != 1 {
		t.Fatalf("code = %d", code)
	}
	if _, err := os.Stat(filepath.Join(repo.Dir, ".ralph", "project.yaml")); err == nil {
		t.Error("project.yaml created")
	}
}

func TestInitLeavesOlderToolFilesAlone(t *testing.T) {
	repo := inRepo(t)
	repo.WriteFile(".ralph/config.yaml", "old: config\n")
	repo.WriteFile(".ralph/ralph.db", "old db bytes")
	if code, _, stderr := run(t, "init"); code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	for f, want := range map[string]string{"config.yaml": "old: config\n", "ralph.db": "old db bytes"} {
		if got := readFile(t, filepath.Join(repo.Dir, ".ralph", f)); got != want {
			t.Errorf("%s = %q", f, got)
		}
	}
}

func TestInitThenConfigShow(t *testing.T) {
	inRepo(t)
	_, stdout, _ := run(t, "init")
	id := projectIDLine.FindStringSubmatch(stdout)[1]
	code, out, stderr := run(t, "config", "show")
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	if got := row(t, out, "project.id"); got[1] != id || got[2] != "project" {
		t.Errorf("project.id = %q", got)
	}
}
