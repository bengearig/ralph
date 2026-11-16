package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/bengearig/ralph/internal/version"
)

func run(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = Run(args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestVersionCommand(t *testing.T) {
	code, stdout, _ := run(t, "version")
	if code != 0 {
		t.Errorf("code = %d, want 0", code)
	}
	if stdout != "ralph dev\n" {
		t.Errorf("stdout = %q, want %q", stdout, "ralph dev\n")
	}
}

func TestVersionUsesStampedValue(t *testing.T) {
	old := version.Version
	t.Cleanup(func() { version.Version = old })
	version.Version = "1.2.3"
	code, stdout, _ := run(t, "version")
	if code != 0 {
		t.Errorf("code = %d, want 0", code)
	}
	if stdout != "ralph 1.2.3\n" {
		t.Errorf("stdout = %q, want %q", stdout, "ralph 1.2.3\n")
	}
}

func TestUnknownCommand(t *testing.T) {
	code, stdout, stderr := run(t, "nosuch")
	if code != 1 {
		t.Errorf("code = %d, want 1", code)
	}
	if !strings.Contains(stderr, "unknown command") {
		t.Errorf("stderr = %q, want it to contain %q", stderr, "unknown command")
	}
	if !strings.HasPrefix(stderr, "ralph: ") {
		t.Errorf("stderr = %q, want prefix %q", stderr, "ralph: ")
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want empty", stdout)
	}
}

func TestNoArgsPrintsHelp(t *testing.T) {
	code, stdout, _ := run(t)
	if code != 0 {
		t.Errorf("code = %d, want 0", code)
	}
	if !strings.Contains(stdout, "Usage:") {
		t.Errorf("stdout = %q, want it to contain %q", stdout, "Usage:")
	}
}
