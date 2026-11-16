package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeWorkflowFile(t *testing.T, root, name, content string) {
	t.Helper()
	dir := filepath.Join(root, ".ralph", "workflow")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestInitOverridesReported(t *testing.T) {
	repo := inRepo(t)
	withFakeClaude(t)
	writeWorkflowFile(t, repo.Dir, "receive.md", "A brand new receive.\n")
	writeWorkflowFile(t, repo.Dir, "quick.append.md", "Always run the linter.\n")
	code, stdout, stderr := run(t, "init")
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	first := "workflow: quick extended by .ralph/workflow/quick.append.md"
	second := "workflow: receive replaced by .ralph/workflow/receive.md"
	lines := outLines(stdout)
	at := func(want string) int {
		for i, l := range lines {
			if l == want {
				return i
			}
		}
		t.Fatalf("no line %q in:\n%s", want, stdout)
		return -1
	}
	adapter := -1
	for i, l := range lines {
		if strings.HasPrefix(l, "claude-code:") {
			adapter = i
			break
		}
	}
	if at(first) > at(second) || at(second) > adapter || adapter < 0 {
		t.Errorf("order wrong:\n%s", stdout)
	}
	if c := readFile(t, filepath.Join(repo.Dir, ".claude/commands/ralph/receive.md")); !strings.Contains(c, "A brand new receive.") {
		t.Errorf("receive command = %q", c)
	}
	if c := readFile(t, filepath.Join(repo.Dir, ".claude/commands/ralph/quick.md")); !strings.Contains(c, "Always run the linter.") {
		t.Errorf("quick command = %q", c)
	}
}

func TestInitInvalidOverrideWritesNothing(t *testing.T) {
	repo := inRepo(t)
	withFakeClaude(t)
	writeWorkflowFile(t, repo.Dir, "bogus.md", "text\n")
	code, _, stderr := run(t, "init")
	if code != 1 || !strings.Contains(stderr, "bogus.md") {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	for _, p := range []string{".ralph/store.db", ".ralph/integration.json", ".claude", "CLAUDE.md", ".mcp.json"} {
		if _, err := os.Stat(filepath.Join(repo.Dir, p)); !os.IsNotExist(err) {
			t.Errorf("%s exists (%v)", p, err)
		}
	}
	entries, err := os.ReadDir(filepath.Join(repo.Dir, ".ralph"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "workflow" {
		t.Errorf(".ralph holds %v", entries)
	}
}

func TestStatusOverridesInvalidWarns(t *testing.T) {
	e := outdatedEnv(t)
	writeWorkflowFile(t, e.repo.Dir, "bogus.md", "text\n")
	code, out, stderr := e.status()
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	if strings.Contains(out, "out of date") || !strings.Contains(stderr, "bogus.md") {
		t.Errorf("out = %q, stderr = %q", out, stderr)
	}
}

func TestStatusOutOfDateAfterOverrideEdit(t *testing.T) {
	e := outdatedEnv(t)
	writeWorkflowFile(t, e.repo.Dir, "status.append.md", "Mention the weather.\n")
	_, out, _ := e.status()
	if !strings.Contains(out, integrationOutdatedLine) {
		t.Fatalf("no out-of-date line:\n%s", out)
	}
	if code, _, stderr := run(t, "init"); code != 0 {
		t.Fatalf("init: %d %q", code, stderr)
	}
	if c := readFile(t, filepath.Join(e.repo.Dir, statusCommandPath)); !strings.Contains(c, "Mention the weather.") {
		t.Errorf("status command = %q", c)
	}
	if _, out, _ := e.status(); strings.Contains(out, "out of date") {
		t.Errorf("still out of date:\n%s", out)
	}
}
