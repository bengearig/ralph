//go:build !windows

package headless

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bengearig/ralph/internal/harness"
	"github.com/bengearig/ralph/internal/harness/claudecode"
	"github.com/bengearig/ralph/internal/harness/opencode"
)

func fakeHarness(t *testing.T, name, fixture string) {
	t.Helper()
	bin := t.TempDir()
	body := "#!/bin/sh\ncat >/dev/null\ncat '" + fixture + "'\n"
	if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestExecuteRunsAdapterAgainstFakeHarness(t *testing.T) {
	cases := []struct {
		adapter harness.Adapter
		command string
		dir     string
		errText string
	}{
		{claudecode.New(), "claude", "claudecode", "budget exceeded"},
		{opencode.New(), "opencode", "opencode", "Free usage limit"},
	}
	for _, c := range cases {
		t.Run(c.command, func(t *testing.T) {
			req := harness.HeadlessRequest{
				Dir:    t.TempDir(),
				Prompt: "go",
				MCP:    harness.MCPServer{Name: "ralph", Command: "ralph", Args: []string{"mcp"}},
			}
			fixtures := filepath.Join("..", "harness", c.dir, "testdata")
			abs, err := filepath.Abs(fixtures)
			if err != nil {
				t.Fatal(err)
			}

			fakeHarness(t, c.command, filepath.Join(abs, "success.jsonl"))
			res, err := Execute(context.Background(), c.adapter, req, Options{})
			if err != nil {
				t.Fatal(err)
			}
			if !res.Outcome.Success || res.Outcome.Text != "All done." || res.Failure() != "" {
				t.Errorf("success: outcome %+v, failure %q", res.Outcome, res.Failure())
			}

			fakeHarness(t, c.command, filepath.Join(abs, "error.jsonl"))
			res, err = Execute(context.Background(), c.adapter, req, Options{})
			if err != nil {
				t.Fatal(err)
			}
			if f := res.Failure(); !strings.Contains(f, c.errText) {
				t.Errorf("failure = %q, want it to mention %q", f, c.errText)
			}
		})
	}
}

func TestExecuteReturnsHeadlessError(t *testing.T) {
	_, err := Execute(context.Background(), claudecode.New(), harness.HeadlessRequest{}, Options{})
	if err == nil {
		t.Fatal("want the Validate error")
	}
}

type noAutoAdapter struct{ harness.Adapter }

func (noAutoAdapter) Name() string                       { return "plain" }
func (noAutoAdapter) Capabilities() harness.Capabilities { return harness.Capabilities{} }

func TestAutoUnsupportedExecuteRejected(t *testing.T) {
	req := harness.HeadlessRequest{Dir: t.TempDir(), Prompt: "p", Mode: harness.PermissionAuto,
		MCP: harness.MCPServer{Name: "ralph", Command: "ralph"}}
	_, err := Execute(context.Background(), noAutoAdapter{claudecode.New()}, req, Options{})
	if err == nil || err.Error() != "plain has no automatic mode; set headless.permissions to restricted" {
		t.Errorf("error = %v", err)
	}
}

func TestFailureFromDenialWithExitZero(t *testing.T) {
	abs, err := filepath.Abs(filepath.Join("..", "harness", "opencode", "testdata", "denied.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	fakeHarness(t, "opencode", abs)
	req := harness.HeadlessRequest{
		Dir:    t.TempDir(),
		Prompt: "go",
		MCP:    harness.MCPServer{Name: "ralph", Command: "ralph", Args: []string{"mcp"}},
	}
	res, err := Execute(context.Background(), opencode.New(), req, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("ExitCode = %d", res.ExitCode)
	}
	if got := res.Failure(); !strings.HasPrefix(got, "denied actions (1): shell") {
		t.Errorf("Failure = %q", got)
	}
}
