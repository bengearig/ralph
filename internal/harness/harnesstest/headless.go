package harnesstest

import (
	"reflect"
	"strings"
	"testing"

	"github.com/bengearig/ralph/internal/harness"
)

// HeadlessFixtures are standard-output streams of one adapter's harness.
type HeadlessFixtures struct {
	// Success is a run that finished and succeeded, with a final text and a
	// session identifier.
	Success string
	// Error is a run that ended with an error.
	Error string
	// Truncated is a stream cut off before the run finished.
	Truncated string
}

const headlessPrompt = "PROMPT-MARKER implement the task"

func headlessRequest() harness.HeadlessRequest {
	return harness.HeadlessRequest{
		Dir:    "/tmp/project",
		Prompt: headlessPrompt,
		MCP:    harness.MCPServer{Name: "ralph", Command: "/usr/bin/ralph", Args: []string{"mcp"}},
	}
}

func feed(p harness.Parser, stream string) harness.Outcome {
	for _, l := range strings.Split(stream, "\n") {
		p.Line([]byte(l))
	}
	return p.Outcome()
}

// RunHeadless checks the headless conformance of a against the fixtures.
func RunHeadless(t *testing.T, a harness.Adapter, f HeadlessFixtures) {
	t.Helper()

	t.Run("invocation", func(t *testing.T) {
		req := headlessRequest()
		first, err := a.Headless(req)
		if err != nil {
			t.Fatalf("Headless: %v", err)
		}
		second, err := a.Headless(req)
		if err != nil {
			t.Fatalf("Headless again: %v", err)
		}
		if !reflect.DeepEqual(first, second) {
			t.Errorf("the same request gave different invocations:\n%+v\n%+v", first, second)
		}
		if first.Command == "" {
			t.Error("Command is empty")
		}
		if first.Stdin != headlessPrompt {
			t.Errorf("Stdin = %q, want the prompt", first.Stdin)
		}
		for _, s := range append(append([]string{}, first.Args...), first.Env...) {
			if strings.Contains(s, "PROMPT-MARKER") {
				t.Errorf("the prompt appears outside Stdin: %q", s)
			}
		}
		if first.Dir != req.Dir {
			t.Errorf("Dir = %q, want %q", first.Dir, req.Dir)
		}
	})

	t.Run("invalid request", func(t *testing.T) {
		req := headlessRequest()
		req.Prompt = ""
		want := req.Validate()
		if want == nil {
			t.Fatal("test request should be invalid")
		}
		_, err := a.Headless(req)
		if err == nil || err.Error() != want.Error() {
			t.Errorf("Headless error = %v, want %v", err, want)
		}
	})

	t.Run("empty mode is restricted", func(t *testing.T) {
		req := headlessRequest()
		empty, err := a.Headless(req)
		if err != nil {
			t.Fatal(err)
		}
		req.Mode = harness.PermissionRestricted
		restricted, err := a.Headless(req)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(empty, restricted) {
			t.Errorf("empty mode differs from restricted:\n%+v\n%+v", empty, restricted)
		}
	})

	t.Run("success stream", func(t *testing.T) {
		out := feed(a.NewParser(), f.Success)
		if !out.Finished || !out.Success {
			t.Errorf("Finished=%v Success=%v, want both true", out.Finished, out.Success)
		}
		if out.Text == "" {
			t.Error("Text is empty")
		}
		if out.SessionID == "" {
			t.Error("SessionID is empty")
		}
		if out.Error != "" {
			t.Errorf("Error = %q, want none", out.Error)
		}
	})

	t.Run("error stream", func(t *testing.T) {
		out := feed(a.NewParser(), f.Error)
		if out.Success {
			t.Error("Success is true for an error stream")
		}
		if out.Error == "" {
			t.Error("Error is empty")
		}
	})

	t.Run("truncated stream", func(t *testing.T) {
		out := feed(a.NewParser(), f.Truncated)
		if out.Finished {
			t.Error("Finished is true for a truncated stream")
		}
	})

	t.Run("garbage is ignored", func(t *testing.T) {
		p := a.NewParser()
		for _, l := range []string{"", "   ", "not json", "{", "[1,2]", "null", `"text"`, "\x00\xff"} {
			p.Line([]byte(l))
		}
		if out := p.Outcome(); !reflect.DeepEqual(out, harness.Outcome{}) {
			t.Errorf("garbage changed the outcome: %+v", out)
		}
		clean := feed(a.NewParser(), f.Success)
		mixed := a.NewParser()
		for _, l := range strings.Split(f.Success, "\n") {
			mixed.Line([]byte("garbage"))
			mixed.Line([]byte(""))
			mixed.Line([]byte(l))
		}
		if got := mixed.Outcome(); !reflect.DeepEqual(got, clean) {
			t.Errorf("garbage between lines changed the outcome:\n%+v\n%+v", got, clean)
		}
	})

	t.Run("empty stream", func(t *testing.T) {
		if out := feed(a.NewParser(), ""); !reflect.DeepEqual(out, harness.Outcome{}) {
			t.Errorf("empty stream gave %+v", out)
		}
	})
}
