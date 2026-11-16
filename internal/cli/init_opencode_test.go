package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// withOnlyFakeOpenCode puts a fake opencode and git, and no claude, on PATH.
func withOnlyFakeOpenCode(t *testing.T, help string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell script fake")
	}
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.Symlink(git, filepath.Join(dir, "git")); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then echo 'opencode v2.3.0'; else " + help + "; fi\n"
	if err := os.WriteFile(filepath.Join(dir, "opencode"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
}

func TestInitReportsOpenCodeDetection(t *testing.T) {
	inRepo(t)
	withOnlyFakeOpenCode(t, "echo '  --format  output format'")
	code, stdout, stderr := run(t, "init")
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	if !containsLine(outLines(stdout), "opencode: 2.3.0") {
		t.Errorf("stdout = %q", stdout)
	}
}

func TestInitReportsOpenCodeUnsupported(t *testing.T) {
	inRepo(t)
	withOnlyFakeOpenCode(t, "echo usage")
	code, stdout, stderr := run(t, "init")
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	if !strings.Contains(stdout, `opencode: unsupported: the opencode command on the path does not support "run --format"`) {
		t.Errorf("stdout = %q", stdout)
	}
	if !strings.Contains(stdout, "no harness detected") {
		t.Errorf("hint missing: %q", stdout)
	}
}
