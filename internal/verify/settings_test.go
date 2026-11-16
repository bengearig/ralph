package verify

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bengearig/ralph/internal/config"
	"github.com/bengearig/ralph/internal/project"
)

func loadConfig(t *testing.T, root string, env map[string]string) *config.Config {
	t.Helper()
	cfg, err := config.Load(config.Options{
		Registry:    config.DefaultRegistry(),
		UserFile:    filepath.Join(t.TempDir(), "user.yaml"),
		ProjectRoot: root,
		Getenv:      func(k string) (string, bool) { v, ok := env[k]; return v, ok },
	})
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func writeProject(t *testing.T, root, content string) {
	t.Helper()
	file := filepath.Join(project.RalphDir(root), project.ProjectFile)
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFromConfig(t *testing.T) {
	root := t.TempDir()
	writeProject(t, root, "verify:\n  commands:\n    - go test ./...\n    - go vet ./...\n  timeout: 2m\n")
	s := FromConfig(loadConfig(t, root, nil), root)
	if len(s.Commands) != 2 || s.Commands[0] != "go test ./..." || s.Commands[1] != "go vet ./..." {
		t.Fatalf("commands = %q", s.Commands)
	}
	if s.Timeout != 2*time.Minute {
		t.Fatalf("timeout = %v", s.Timeout)
	}
	want := "project (" + filepath.Join(".ralph", "project.yaml") + ":3)"
	if s.Source != want {
		t.Fatalf("source = %q, want %q", s.Source, want)
	}

	empty := t.TempDir()
	s = FromConfig(loadConfig(t, empty, nil), empty)
	if s.Commands != nil || s.Source != "" || s.Timeout != 10*time.Minute {
		t.Fatalf("empty settings = %+v", s)
	}

	env := map[string]string{"RALPH_VERIFY_COMMANDS": `["make test"]`}
	s = FromConfig(loadConfig(t, root, env), root)
	if len(s.Commands) != 1 || s.Commands[0] != "make test" || s.Source != "env (RALPH_VERIFY_COMMANDS)" {
		t.Fatalf("env settings = %+v", s)
	}
}
