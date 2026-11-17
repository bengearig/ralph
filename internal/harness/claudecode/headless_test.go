package claudecode

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

const wantMCPConfig = `{"mcpServers":{"ralph":{"args":["mcp","--x"],"command":"/bin/ralph","type":"stdio"}}}`

func TestClaudeHeadlessArgsExact(t *testing.T) {
	req := headlessReq()
	inv, err := New().Headless(req)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"-p", "--output-format", "stream-json", "--verbose", "--no-session-persistence",
		"--setting-sources", "user,local", "--strict-mcp-config", "--mcp-config", wantMCPConfig,
		"--permission-mode", "dontAsk", "--permission-prompts", "none"}
	if !reflect.DeepEqual(inv.Args, want) {
		t.Errorf("Args =\n%q\nwant\n%q", inv.Args, want)
	}
	if inv.Command != "claude" || inv.Dir != req.Dir || inv.Stdin != req.Prompt || len(inv.Env) != 0 {
		t.Errorf("invocation = %+v", inv)
	}

	req.Model = "claude-opus-5-5"
	inv, err = New().Headless(req)
	if err != nil {
		t.Fatal(err)
	}
	want = []string{"-p", "--output-format", "stream-json", "--verbose", "--no-session-persistence",
		"--setting-sources", "user,local", "--strict-mcp-config", "--mcp-config", wantMCPConfig,
		"--model", "claude-opus-5-5", "--permission-mode", "dontAsk", "--permission-prompts", "none"}
	if !reflect.DeepEqual(inv.Args, want) {
		t.Errorf("Args with model =\n%q\nwant\n%q", inv.Args, want)
	}
}

func TestClaudeHeadlessMCPConfigJSON(t *testing.T) {
	req := headlessReq()
	req.MCP = harness.MCPServer{Name: "other", Command: "ralph"}
	inv, err := New().Headless(req)
	if err != nil {
		t.Fatal(err)
	}
	var cfg string
	for i, a := range inv.Args {
		if a == "--mcp-config" {
			cfg = inv.Args[i+1]
		}
	}
	var got map[string]map[string]map[string]any
	if err := json.Unmarshal([]byte(cfg), &got); err != nil {
		t.Fatalf("not JSON: %v: %s", err, cfg)
	}
	entry := got["mcpServers"]["other"]
	if entry["type"] != "stdio" || entry["command"] != "ralph" || !reflect.DeepEqual(entry["args"], []any{}) {
		t.Errorf("entry = %v", entry)
	}
	art, err := mcpContent(req.MCP)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cfg, string(art)) {
		t.Errorf("config %s does not contain the artifact entry %s", cfg, art)
	}
}

func TestClaudeHeadlessVersionGate(t *testing.T) {
	for _, v := range []string{"2.1.259", "2.1.325", "2.2.0-beta.1", "3.0.0", "", "unknown"} {
		req := headlessReq()
		req.Version = v
		if _, err := New().Headless(req); err != nil {
			t.Errorf("version %q: %v", v, err)
		}
	}
	for _, v := range []string{"2.1.258", "2.0.999", "1.9.9", "2.1.0-rc.1"} {
		req := headlessReq()
		req.Version = v
		_, err := New().Headless(req)
		want := "claude " + v + " is too old for headless runs; version 2.1.259 or later is needed"
		if err == nil || err.Error() != want {
			t.Errorf("version %q: error = %v, want %q", v, err, want)
		}
	}
}

func TestClaudeHeadlessAutoNotYetSupported(t *testing.T) {
	req := headlessReq()
	req.Mode = harness.PermissionAuto
	_, err := New().Headless(req)
	if err == nil || err.Error() != "auto mode is not implemented yet" {
		t.Errorf("error = %v", err)
	}
}

func TestClaudeCapabilities(t *testing.T) {
	if got := New().Capabilities(); !got.Auto || !got.NativeBudget {
		t.Errorf("Capabilities = %+v", got)
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

func TestClaudeParserSuccessStream(t *testing.T) {
	out := parse(fixture(t, "success.jsonl"))
	want := harness.Outcome{Finished: true, Success: true, Text: "All done.", SessionID: "sess-1"}
	if !reflect.DeepEqual(out, want) {
		t.Errorf("Outcome = %+v, want %+v", out, want)
	}
}

func TestClaudeParserErrorResultUsesErrorsThenSubtype(t *testing.T) {
	out := parse(fixture(t, "error.jsonl"))
	if !out.Finished || out.Success || out.Error != "Reached maximum budget ($0.0005)" {
		t.Errorf("Outcome = %+v", out)
	}
	out = parse(`{"type":"result","subtype":"error_max_turns","is_error":true,"errors":["a","b"],"session_id":"s"}`)
	if out.Error != "a; b" {
		t.Errorf("Error = %q", out.Error)
	}
	out = parse(`{"type":"result","subtype":"error_during_execution","is_error":true,"session_id":"s"}`)
	if out.Error != "error_during_execution" || out.Success {
		t.Errorf("Outcome = %+v", out)
	}
	out = parse(`{"type":"result","subtype":"success","is_error":true,"result":"x"}`)
	if out.Success || out.Error != "success" || !out.Finished {
		t.Errorf("is_error with success subtype: %+v", out)
	}
}

func TestClaudeParserSessionFromInit(t *testing.T) {
	out := parse(`{"type":"system","subtype":"init","session_id":"abc"}`)
	if out.SessionID != "abc" || out.Finished {
		t.Errorf("Outcome = %+v", out)
	}
	out = parse(fixture(t, "truncated.jsonl"))
	if out.SessionID != "sess-3" || out.Finished {
		t.Errorf("truncated Outcome = %+v", out)
	}
	out = parse(`{"type":"system","subtype":"permission_denied","session_id":"zzz"}`)
	if out.SessionID != "" {
		t.Errorf("non-init system event set the session: %+v", out)
	}
}

func TestClaudeParserIgnoresNonJSON(t *testing.T) {
	out := parse("hello\n\n{broken\n" + `{"type":"result","subtype":"success","is_error":false,"result":"ok","session_id":"s"}` + "\nmore text")
	if !out.Success || out.Text != "ok" {
		t.Errorf("Outcome = %+v", out)
	}
}
