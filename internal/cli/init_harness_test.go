package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func withFakeClaude(t *testing.T) {
	t.Helper()
	withFakeTools(t, map[string]string{"claude": "#!/bin/sh\necho '2.0.1 (Claude Code)'\n"})
}

func withoutClaude(t *testing.T) {
	t.Helper()
	withFakeTools(t, nil)
}

func outLines(out string) []string { return strings.Split(strings.TrimSpace(out), "\n") }

func countPrefix(out, prefix string) int {
	n := 0
	for _, l := range outLines(out) {
		if strings.HasPrefix(l, prefix) {
			n++
		}
	}
	return n
}

func TestInitInstallsClaudeCode(t *testing.T) {
	repo := inRepo(t)
	withFakeClaude(t)
	code, stdout, stderr := run(t, "init")
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	for _, p := range []string{".claude/commands/ralph/status.md", "CLAUDE.md", ".mcp.json", ".claude/settings.local.json", ".ralph/integration.json", ".ralph/integration.local.json"} {
		if _, err := os.Stat(filepath.Join(repo.Dir, p)); err != nil {
			t.Errorf("%s: %v", p, err)
		}
	}
	if c := readFile(t, filepath.Join(repo.Dir, "CLAUDE.md")); !strings.Contains(c, "<!-- ralph:begin -->") || !strings.Contains(c, "<!-- ralph:end -->") {
		t.Errorf("CLAUDE.md = %q", c)
	}
	var mcp struct {
		MCPServers map[string]any `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(readFile(t, filepath.Join(repo.Dir, ".mcp.json"))), &mcp); err != nil || mcp.MCPServers["ralph"] == nil {
		t.Errorf(".mcp.json: %v %v", mcp, err)
	}
	if s := readFile(t, filepath.Join(repo.Dir, ".claude/settings.local.json")); !strings.Contains(s, `"mcp__ralph"`) {
		t.Errorf("settings = %q", s)
	}
	if !strings.Contains(stdout, "claude-code: 2.0.1") {
		t.Errorf("stdout = %q", stdout)
	}
	if n := countPrefix(stdout, "installed "); n != 6 {
		t.Errorf("installed lines = %d in %q", n, stdout)
	}
}

func TestInitInstallsQuickCommand(t *testing.T) {
	repo := inRepo(t)
	withFakeClaude(t)
	code, stdout, stderr := run(t, "init")
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	if _, err := os.Stat(filepath.Join(repo.Dir, ".claude/commands/ralph/quick.md")); err != nil {
		t.Error(err)
	}
	if !strings.Contains(stdout, "installed .claude/commands/ralph/quick.md") {
		t.Errorf("stdout = %q", stdout)
	}
}

func TestInitHarnessFlag(t *testing.T) {
	repo := inRepo(t)
	withoutClaude(t)
	code, stdout, stderr := run(t, "init", "--harness", "claude-code")
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	if _, err := os.Stat(filepath.Join(repo.Dir, ".claude/commands/ralph/status.md")); err != nil {
		t.Error(err)
	}
	if !strings.Contains(stdout, "installed .mcp.json") {
		t.Errorf("stdout = %q", stdout)
	}
}

func TestInitHarnessUnknown(t *testing.T) {
	repo := inRepo(t)
	code, _, stderr := run(t, "init", "--harness", "nope")
	if code == 0 || !strings.Contains(stderr, "claude-code") {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	if _, err := os.Stat(filepath.Join(repo.Dir, ".ralph")); !os.IsNotExist(err) {
		t.Errorf(".ralph exists: %v", err)
	}
}

func TestInitNoHarnessDetected(t *testing.T) {
	inRepo(t)
	withoutClaude(t)
	code, stdout, stderr := run(t, "init")
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	if !strings.Contains(stdout, `no harness detected; run "ralph init --harness <name>" to install the integration, where <name> is one of: claude-code, opencode`) {
		t.Errorf("stdout = %q", stdout)
	}
}

func TestInitIntegrationIdempotent(t *testing.T) {
	inRepo(t)
	withFakeClaude(t)
	run(t, "init")
	code, stdout, stderr := run(t, "init")
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	if n := countPrefix(stdout, "unchanged "); n != 6 {
		t.Errorf("unchanged lines = %d in %q", n, stdout)
	}
	for _, a := range []string{"installed ", "updated ", "adopted ", "kept "} {
		if countPrefix(stdout, a) != 0 {
			t.Errorf("unexpected %q line in %q", a, stdout)
		}
	}
}

func TestInitKeepsOlderToolFiles(t *testing.T) {
	repo := inRepo(t)
	withFakeClaude(t)
	const cmdFile = "older status command\n"
	const claudeMD = "# Mine\n\n<!-- ralph:begin -->\nolder block\n<!-- ralph:end -->\n"
	const mcp = "{\n \"mcpServers\": {\n  \"ralph\": {\"type\": \"stdio\", \"command\": \"ralph\", \"args\": [\"mcp\"]}\n }\n}\n"
	repo.WriteFile(".claude/commands/ralph/status.md", cmdFile)
	repo.WriteFile("CLAUDE.md", claudeMD)
	repo.WriteFile(".mcp.json", mcp)

	code, stdout, stderr := run(t, "init")
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	if n := countPrefix(stdout, "kept "); n != 2 {
		t.Errorf("kept lines = %d in %q", n, stdout)
	}
	if !strings.Contains(stdout, "adopted .mcp.json") || !strings.Contains(stdout, "customized") {
		t.Errorf("stdout = %q", stdout)
	}
	for p, want := range map[string]string{".claude/commands/ralph/status.md": cmdFile, "CLAUDE.md": claudeMD, ".mcp.json": mcp} {
		if got := readFile(t, filepath.Join(repo.Dir, p)); got != want {
			t.Errorf("%s changed: %q", p, got)
		}
	}
	if _, err := os.Stat(filepath.Join(repo.Dir, ".claude/commands/ralph/status.md.new")); err != nil {
		t.Error(err)
	}
	if _, err := os.Stat(filepath.Join(repo.Dir, "CLAUDE.md.new")); !os.IsNotExist(err) {
		t.Errorf("CLAUDE.md.new: %v", err)
	}
}

func TestInitIntegrationErrorKeepsEarlierWork(t *testing.T) {
	repo := inRepo(t)
	withFakeClaude(t)
	repo.WriteFile(".claude", "a file where a directory should be")
	code, _, stderr := run(t, "init")
	if code == 0 || stderr == "" {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	for _, p := range []string{".ralph/project.yaml", ".ralph/store.db"} {
		if _, err := os.Stat(filepath.Join(repo.Dir, p)); err != nil {
			t.Errorf("%s: %v", p, err)
		}
	}
}

func TestInitIgnoresLocalManifest(t *testing.T) {
	repo := inRepo(t)
	withFakeClaude(t)
	run(t, "init")
	if err := exec.Command("git", "-C", repo.Dir, "check-ignore", "-q", ".ralph/integration.local.json").Run(); err != nil {
		t.Errorf("local manifest not ignored: %v", err)
	}
	if err := exec.Command("git", "-C", repo.Dir, "check-ignore", "-q", ".ralph/integration.json").Run(); err == nil {
		t.Error("project manifest is ignored")
	}
}

func TestInitDoesNotCommitLocalPermission(t *testing.T) {
	repo := inRepo(t)
	withFakeClaude(t)
	run(t, "init")
	for _, p := range []string{".claude/settings.json", ".mcp.json"} {
		if b, err := os.ReadFile(filepath.Join(repo.Dir, p)); err == nil && strings.Contains(string(b), "mcp__ralph") {
			t.Errorf("%s holds the permission", p)
		}
	}
	if _, err := os.Stat(filepath.Join(repo.Dir, ".claude/settings.json")); !os.IsNotExist(err) {
		t.Errorf("settings.json exists: %v", err)
	}
}
