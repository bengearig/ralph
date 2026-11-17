package opencode

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/bengearig/ralph/internal/harness"
)

func headlessReq() harness.HeadlessRequest {
	return harness.HeadlessRequest{
		Dir:    "/work/project",
		Prompt: "do the thing",
		MCP:    harness.MCPServer{Name: "ralph", Command: "/bin/ralph", Args: []string{"mcp", "--x"}},
	}
}

func TestOpenCodeHeadlessArgsExact(t *testing.T) {
	req := headlessReq()
	inv, err := New().Headless(req)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"run", "--standalone", "--format", "json", "--archived"}
	if !reflect.DeepEqual(inv.Args, want) {
		t.Errorf("Args = %q, want %q", inv.Args, want)
	}
	if inv.Command != "opencode" || inv.Dir != req.Dir || inv.Stdin != req.Prompt {
		t.Errorf("invocation = %+v", inv)
	}
	req.Model = "opencode/big-pickle"
	inv, err = New().Headless(req)
	if err != nil {
		t.Fatal(err)
	}
	want = append(want, "--model", "opencode/big-pickle")
	if !reflect.DeepEqual(inv.Args, want) {
		t.Errorf("Args with model = %q, want %q", inv.Args, want)
	}
}

func TestOpenCodeHeadlessEnvExact(t *testing.T) {
	inv, err := New().Headless(headlessReq())
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.Env) != 2 || inv.Env[0] != "OPENCODE_DISABLE_PROJECT_CONFIG=1" {
		t.Fatalf("Env = %q", inv.Env)
	}
	got, ok := strings.CutPrefix(inv.Env[1], "OPENCODE_CONFIG_CONTENT=")
	if !ok {
		t.Fatalf("Env[1] = %q", inv.Env[1])
	}
	golden, err := os.ReadFile("testdata/config_content.golden")
	if err != nil {
		t.Fatal(err)
	}
	if want := strings.TrimSuffix(string(golden), "\n"); got != want {
		t.Errorf("config content =\n%s\nwant\n%s", got, want)
	}
}

func TestOpenCodeHeadlessMCPEntryMatchesInstalledShape(t *testing.T) {
	req := headlessReq()
	inv, err := New().Headless(req)
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		MCP struct {
			Servers map[string]json.RawMessage `json:"servers"`
		} `json:"mcp"`
	}
	content := strings.TrimPrefix(inv.Env[1], "OPENCODE_CONFIG_CONTENT=")
	if err := json.Unmarshal([]byte(content), &cfg); err != nil {
		t.Fatal(err)
	}
	arts, err := New().Artifacts(harness.Inputs{MCP: req.MCP})
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range arts {
		if a.Kind == harness.KindJSONEntry && reflect.DeepEqual(a.KeyPath, []string{"mcp", "servers", req.MCP.Name}) {
			var got, want any
			if err := json.Unmarshal(cfg.MCP.Servers[req.MCP.Name], &got); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(a.Content, &want); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("entry %s, artifact writes %s", cfg.MCP.Servers[req.MCP.Name], a.Content)
			}
			return
		}
	}
	t.Fatal("no MCP artifact found")
}

func TestOpenCodeCapabilities(t *testing.T) {
	if got := New().Capabilities(); !got.Auto || got.NativeBudget {
		t.Errorf("Capabilities = %+v", got)
	}
}

func TestOpenCodeHeadlessAutoNotYetSupported(t *testing.T) {
	req := headlessReq()
	req.Mode = harness.PermissionAuto
	if _, err := New().Headless(req); err == nil || err.Error() != "auto mode is not implemented yet" {
		t.Errorf("error = %v", err)
	}
}

func parse(stream string) harness.Outcome {
	p := New().NewParser()
	for _, l := range strings.Split(stream, "\n") {
		p.Line([]byte(l))
	}
	return p.Outcome()
}

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestOpenCodeParserSuccessStream(t *testing.T) {
	out := parse(fixture(t, "success.jsonl"))
	want := harness.Outcome{Finished: true, Success: true, Text: "All done.", SessionID: "ses_1"}
	if !reflect.DeepEqual(out, want) {
		t.Errorf("Outcome = %+v, want %+v", out, want)
	}
}

func TestOpenCodeParserErrorEvent(t *testing.T) {
	out := parse(fixture(t, "error.jsonl"))
	if out.Success || out.Error != "provider.free-limit: Free usage limit reached" || out.SessionID != "ses_2" {
		t.Errorf("Outcome = %+v", out)
	}
	out = parse(`{"type":"error","sessionID":"s","error":{"message":"boom"}}`)
	if out.Error != "boom" {
		t.Errorf("Error = %q", out.Error)
	}
}

func TestOpenCodeParserFinishedOnlyOnStop(t *testing.T) {
	out := parse(`{"type":"step_finish","sessionID":"s","part":{"reason":"tool-calls"}}`)
	if out.Finished || out.Success {
		t.Errorf("tool-calls finished the run: %+v", out)
	}
	out = parse(`{"type":"step_finish","sessionID":"s","part":{"reason":"stop"}}` + "\n" + `{"type":"error","error":{"message":"late"}}`)
	if !out.Finished || out.Success {
		t.Errorf("error after stop: %+v", out)
	}
}

func TestOpenCodeParserLatestTextWins(t *testing.T) {
	out := parse(`{"type":"text","sessionID":"s","part":{"text":"first"}}` + "\n" + `{"type":"text","sessionID":"s","part":{"text":"second"}}`)
	if out.Text != "second" {
		t.Errorf("Text = %q", out.Text)
	}
}
