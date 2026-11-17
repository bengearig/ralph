package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bengearig/ralph/internal/harness"
)

type unsupportedAdapter struct{}

func (unsupportedAdapter) Name() string { return "oldharness" }
func (unsupportedAdapter) Detect(context.Context, string) harness.Detection {
	return harness.Detection{Found: true, Version: "0.1", Unsupported: "version too old"}
}
func (unsupportedAdapter) Artifacts(harness.Inputs) ([]harness.Artifact, error) {
	return []harness.Artifact{{Kind: harness.KindFile, Scope: harness.ScopeProject, Path: "old/cmd.md", Content: []byte("x")}}, nil
}

func withAdapters(t *testing.T, extra ...harness.Adapter) {
	t.Helper()
	orig := initAdapters
	t.Cleanup(func() { initAdapters = orig })
	initAdapters = func() []harness.Adapter { return append(orig(), extra...) }
}

func TestInitSkipsUnsupportedDetectedAdapter(t *testing.T) {
	repo := inRepo(t)
	withoutClaude(t)
	withAdapters(t, unsupportedAdapter{})
	code, stdout, stderr := run(t, "init")
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	lines := outLines(stdout)
	if !containsLine(lines, "oldharness: unsupported: version too old") {
		t.Errorf("stdout = %q", stdout)
	}
	if _, err := os.Stat(filepath.Join(repo.Dir, "old")); !os.IsNotExist(err) {
		t.Errorf("artifact installed for unsupported adapter: %v", err)
	}
	last := lines[len(lines)-1]
	if !strings.HasPrefix(last, "no harness detected") {
		t.Errorf("hint missing after unsupported line: %q", stdout)
	}
}

func TestInitUnsupportedNamedIsError(t *testing.T) {
	repo := inRepo(t)
	withoutClaude(t)
	withAdapters(t, unsupportedAdapter{})
	code, stdout, stderr := run(t, "init", "--harness", "oldharness")
	if code == 0 {
		t.Fatalf("code = 0, stdout = %q", stdout)
	}
	if !strings.Contains(stderr, "oldharness: unsupported: version too old") {
		t.Errorf("stderr = %q", stderr)
	}
	if _, err := os.Stat(filepath.Join(repo.Dir, ".ralph")); !os.IsNotExist(err) {
		t.Errorf(".ralph written: %v", err)
	}
}

func TestInitStillInstallsClaudeCode(t *testing.T) {
	repo := inRepo(t)
	withFakeClaude(t)
	withAdapters(t, unsupportedAdapter{})
	code, stdout, stderr := run(t, "init")
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	if !containsLine(outLines(stdout), "oldharness: unsupported: version too old") {
		t.Errorf("stdout = %q", stdout)
	}
	if _, err := os.Stat(filepath.Join(repo.Dir, ".claude/commands/ralph/status.md")); err != nil {
		t.Error(err)
	}
	if strings.Contains(stdout, "no harness detected") {
		t.Errorf("hint printed: %q", stdout)
	}
}

func containsLine(lines []string, want string) bool {
	for _, l := range lines {
		if l == want {
			return true
		}
	}
	return false
}

func (unsupportedAdapter) Headless(harness.HeadlessRequest) (harness.Invocation, error) {
	return harness.Invocation{}, nil
}
func (unsupportedAdapter) NewParser() harness.Parser          { return nil }
func (unsupportedAdapter) Capabilities() harness.Capabilities { return harness.Capabilities{} }
