package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/bengearig/ralph/internal/harness/all"
	"github.com/bengearig/ralph/internal/workflow"
)

const (
	fakeClaudeScript      = "#!/bin/sh\necho '2.1.325 (Claude Code)'\n"
	fakeOpenCodeScript    = "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then echo 'opencode v2.3.0'; elif [ \"$1\" = \"run\" ]; then echo '  --format  output format'; fi\n"
	fakeOldOpenCodeScript = "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then echo 'opencode v2.3.0'; else echo usage; fi\n"
	sectionBeginMarker    = "<!-- ralph:begin -->"
)

// withFakeTools puts git and the named fake executables, and nothing else, on PATH.
func withFakeTools(t *testing.T, tools map[string]string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell script fakes")
	}
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.Symlink(git, filepath.Join(dir, "git")); err != nil {
		t.Fatal(err)
	}
	for name, script := range tools {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
}

func exists(root, rel string) bool {
	_, err := os.Stat(filepath.Join(root, rel))
	return err == nil
}

func writeUserFile(t *testing.T, root, rel, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, rel), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestInitBothHarnessesInstalled(t *testing.T) {
	repo := inRepo(t)
	withFakeTools(t, map[string]string{"claude": fakeClaudeScript, "opencode": fakeOpenCodeScript})
	code, stdout, stderr := run(t, "init")
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	for _, want := range []string{"claude-code: 2.1.325", "opencode: 2.3.0"} {
		if !containsLine(outLines(stdout), want) {
			t.Errorf("missing %q in %q", want, stdout)
		}
	}
	instructions, err := workflow.All()
	if err != nil || len(instructions) == 0 {
		t.Fatalf("workflow.All: %d, %v", len(instructions), err)
	}
	for _, ins := range instructions {
		for _, p := range []string{".opencode/commands/ralph/" + ins.Name + ".md", ".claude/commands/ralph/" + ins.Name + ".md"} {
			if !exists(repo.Dir, p) {
				t.Errorf("%s not written", p)
			}
		}
	}
	if s := readFile(t, filepath.Join(repo.Dir, "opencode.json")); !strings.Contains(s, `"mcp"`) || !strings.Contains(s, `"servers"`) || !strings.Contains(s, `"ralph"`) {
		t.Errorf("opencode.json = %q", s)
	}
	if s := readFile(t, filepath.Join(repo.Dir, ".mcp.json")); !strings.Contains(s, `"mcpServers"`) || !strings.Contains(s, `"ralph"`) {
		t.Errorf(".mcp.json = %q", s)
	}
	if n := strings.Count(readFile(t, filepath.Join(repo.Dir, "CLAUDE.md")), sectionBeginMarker); n != 1 {
		t.Errorf("CLAUDE.md holds the section %d times", n)
	}
	if exists(repo.Dir, "AGENTS.md") {
		t.Error("AGENTS.md was created")
	}

	code, stdout, stderr = run(t, "init")
	if code != 0 {
		t.Fatalf("second run: code = %d, stderr = %q", code, stderr)
	}
	for _, prefix := range []string{"installed ", "updated ", "adopted ", "kept ", "skipped "} {
		if n := countPrefix(stdout, prefix); n != 0 {
			t.Errorf("second run printed %d %q lines in %q", n, prefix, stdout)
		}
	}
	if countPrefix(stdout, "unchanged ") == 0 {
		t.Errorf("second run printed no unchanged lines: %q", stdout)
	}
}

func TestInitOpenCodeKeepsUsersAgentsMd(t *testing.T) {
	repo := inRepo(t)
	withFakeTools(t, map[string]string{"opencode": fakeOpenCodeScript})
	const user = "# Mine\n\nDo not lose this.\n"
	writeUserFile(t, repo.Dir, "AGENTS.md", user)
	if code, _, stderr := run(t, "init"); code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	got := readFile(t, filepath.Join(repo.Dir, "AGENTS.md"))
	if !strings.HasPrefix(got, user) || strings.Count(got, sectionBeginMarker) != 1 {
		t.Errorf("AGENTS.md = %q", got)
	}
	if exists(repo.Dir, "CLAUDE.md") {
		t.Error("CLAUDE.md was created")
	}
}

func TestInitOpenCodeWithOnlyClaudeMdUsesIt(t *testing.T) {
	repo := inRepo(t)
	withFakeTools(t, map[string]string{"opencode": fakeOpenCodeScript})
	const user = "# Claude notes\n\nKeep.\n"
	writeUserFile(t, repo.Dir, "CLAUDE.md", user)
	if code, _, stderr := run(t, "init"); code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	got := readFile(t, filepath.Join(repo.Dir, "CLAUDE.md"))
	if !strings.HasPrefix(got, user) || strings.Count(got, sectionBeginMarker) != 1 {
		t.Errorf("CLAUDE.md = %q", got)
	}
	if exists(repo.Dir, "AGENTS.md") {
		t.Error("AGENTS.md was created")
	}
}

func TestInitOpenCodeJSONCKept(t *testing.T) {
	repo := inRepo(t)
	withFakeTools(t, map[string]string{"opencode": fakeOpenCodeScript})
	const user = "{\n  // my comment\n  \"theme\": \"dark\",\n}\n"
	writeUserFile(t, repo.Dir, "opencode.jsonc", user)
	code, stdout, stderr := run(t, "init")
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	var line string
	for _, l := range outLines(stdout) {
		if strings.HasPrefix(l, "kept opencode.jsonc") {
			line = l
		}
	}
	if line == "" {
		t.Fatalf("no kept line in %q", stdout)
	}
	for _, want := range []string{"mcp.servers.ralph", `"command":["ralph","mcp"]`, `"type":"local"`} {
		if !strings.Contains(line, want) {
			t.Errorf("kept line lacks %q: %q", want, line)
		}
	}
	if got := readFile(t, filepath.Join(repo.Dir, "opencode.jsonc")); got != user {
		t.Errorf("opencode.jsonc changed: %q", got)
	}
	if exists(repo.Dir, "opencode.json") {
		t.Error("opencode.json was created next to opencode.jsonc")
	}
}

func TestInitOpenCodeUnsupportedSkipped(t *testing.T) {
	repo := inRepo(t)
	withFakeTools(t, map[string]string{"opencode": fakeOldOpenCodeScript})
	code, stdout, stderr := run(t, "init")
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	if !strings.Contains(stdout, "opencode: unsupported: ") {
		t.Errorf("stdout = %q", stdout)
	}
	for _, p := range []string{".opencode", "opencode.json", "AGENTS.md", "CLAUDE.md", ".ralph/integration.json"} {
		if exists(repo.Dir, p) {
			t.Errorf("%s was written", p)
		}
	}
}

func TestInitOpenCodeUnsupportedNamedErrors(t *testing.T) {
	repo := inRepo(t)
	withFakeTools(t, map[string]string{"opencode": fakeOldOpenCodeScript})
	code, stdout, stderr := run(t, "init", "--harness", "opencode")
	if code == 0 {
		t.Fatalf("code = 0, stdout = %q", stdout)
	}
	if !strings.Contains(stderr, "opencode: unsupported: ") {
		t.Errorf("stderr = %q", stderr)
	}
	for _, p := range []string{".ralph", ".opencode", "opencode.json", "AGENTS.md"} {
		if exists(repo.Dir, p) {
			t.Errorf("%s was written", p)
		}
	}
}

func TestInitOpenCodeOutOfDateCheckClean(t *testing.T) {
	inRepo(t)
	withFakeTools(t, map[string]string{"claude": fakeClaudeScript, "opencode": fakeOpenCodeScript})
	if code, _, stderr := run(t, "init"); code != 0 {
		t.Fatalf("init: code = %d, stderr = %q", code, stderr)
	}
	code, stdout, stderr := run(t, "status")
	if code != 0 {
		t.Fatalf("status: code = %d, stderr = %q", code, stderr)
	}
	if strings.Contains(stdout, "out of date") {
		t.Errorf("status = %q", stdout)
	}
}

func TestInitHintListsAdapterNames(t *testing.T) {
	inRepo(t)
	withFakeTools(t, nil)
	code, stdout, stderr := run(t, "init")
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	want := `no harness detected; run "ralph init --harness <name>" to install the integration, where <name> is one of: ` + strings.Join(all.Names(), ", ")
	if !containsLine(outLines(stdout), want) {
		t.Errorf("stdout = %q, want line %q", stdout, want)
	}
}
