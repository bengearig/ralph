package claudecode

import (
	"encoding/json"
	"os"
	"reflect"
	"slices"
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

const wantMinimalSettings = `{"permissions":{"allow":["Edit(./**)"],"deny":["Edit(.claude/**)","Edit(.mcp.json)"]}}`

const wantMCPConfig = `{"mcpServers":{"ralph":{"args":["mcp","--x"],"command":"/bin/ralph","type":"stdio"}}}`

func TestClaudeHeadlessArgsExact(t *testing.T) {
	req := headlessReq()
	inv, err := New().Headless(req)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"-p", "--output-format", "stream-json", "--verbose", "--no-session-persistence",
		"--setting-sources", "user,local", "--strict-mcp-config", "--mcp-config", wantMCPConfig,
		"--permission-mode", "dontAsk", "--permission-prompts", "none", "--settings", wantMinimalSettings}
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
		"--model", "claude-opus-5-5", "--permission-mode", "dontAsk", "--permission-prompts", "none", "--settings", wantMinimalSettings}
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
	want := harness.Outcome{Finished: true, Success: true, Text: "All done.", SessionID: "sess-1", Denials: []harness.Denial{},
		Usage: harness.Usage{CostUSD: 0.00245511, CostReported: true, InputTokens: 4, OutputTokens: 440, CacheReadTokens: 31611, CacheWriteTokens: 9593}}
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

const budgetResult = `{"type":"result","subtype":"success","is_error":false,"terminal_reason":"completed","total_cost_usd":0.5,"usage":{"input_tokens":4,"output_tokens":440,"output_tokens_details":{"thinking_tokens":30},"cache_creation_input_tokens":9593,"cache_read_input_tokens":31611},"result":"ok"}`

func TestClaudeParserUsageFromResult(t *testing.T) {
	p := New().NewParser()
	p.Line([]byte(budgetResult))
	want := harness.Usage{CostUSD: 0.5, CostReported: true, InputTokens: 4, OutputTokens: 440, ReasoningTokens: 30, CacheReadTokens: 31611, CacheWriteTokens: 9593}
	if got := p.Usage(); got != want {
		t.Errorf("Usage = %+v, want %+v", got, want)
	}
	if got := p.Outcome(); got.Usage != want || got.BudgetExceeded {
		t.Errorf("Outcome = %+v", got)
	}
}

func TestClaudeParserUsageBeforeResultIsZero(t *testing.T) {
	p := New().NewParser()
	p.Line([]byte(`{"type":"system","subtype":"init","session_id":"s"}`))
	p.Line([]byte(`{"type":"assistant","message":{"id":"m","usage":{"input_tokens":9,"output_tokens":9},"content":[{"type":"text","text":"x"}]}}`))
	if got := p.Usage(); got != (harness.Usage{}) {
		t.Errorf("Usage = %+v", got)
	}
}

func TestClaudeParserIgnoresAssistantUsage(t *testing.T) {
	p := New().NewParser()
	p.Line([]byte(`{"type":"assistant","message":{"id":"m","usage":{"input_tokens":99,"output_tokens":99},"content":[]}}`))
	p.Line([]byte(budgetResult))
	if got := p.Usage(); got.InputTokens != 4 || got.OutputTokens != 440 {
		t.Errorf("Usage = %+v", got)
	}
}

func TestClaudeParserBudgetResultSetsExceeded(t *testing.T) {
	out := parse(`{"type":"result","subtype":"error_max_budget_usd","is_error":true,"terminal_reason":"budget_exhausted","total_cost_usd":0.00104264,"errors":["Reached maximum budget ($0.0005)"],"session_id":"s"}`)
	if !out.BudgetExceeded || out.Success || !out.Finished || !out.Usage.CostReported || out.Usage.CostUSD != 0.00104264 {
		t.Errorf("Outcome = %+v", out)
	}
	out = parse(`{"type":"result","subtype":"error_during_execution","is_error":true,"terminal_reason":"budget_exhausted"}`)
	if !out.BudgetExceeded {
		t.Errorf("terminal_reason alone: %+v", out)
	}
	if out = parse(budgetResult); out.BudgetExceeded {
		t.Errorf("success marked exceeded: %+v", out)
	}
}

func TestClaudeParserUsageWithoutCostFieldIsNotReported(t *testing.T) {
	out := parse(`{"type":"result","subtype":"success","is_error":false,"usage":{"input_tokens":3,"output_tokens":5},"result":"ok"}`)
	if out.Usage.CostReported || out.Usage.CostUSD != 0 || out.Usage.InputTokens != 3 || out.Usage.OutputTokens != 5 {
		t.Errorf("Usage = %+v", out.Usage)
	}
	out = parse(`{"type":"result","subtype":"success","is_error":false,"total_cost_usd":0,"result":"ok"}`)
	if !out.Usage.CostReported {
		t.Errorf("explicit zero cost not reported: %+v", out.Usage)
	}
}

func TestClaudeBudgetArgsOnlyWhenPositive(t *testing.T) {
	req := func(model string, budget float64) harness.HeadlessRequest {
		return harness.HeadlessRequest{Dir: t.TempDir(), Prompt: "p", Model: model, BudgetUSD: budget,
			MCP: harness.MCPServer{Name: "ralph", Command: "ralph", Args: []string{"mcp"}}}
	}
	index := func(args []string, s string) int { return slices.Index(args, s) }
	inv, err := New().Headless(req("claude-opus-5-5", 0.5))
	if err != nil {
		t.Fatal(err)
	}
	i := index(inv.Args, "--max-budget-usd")
	if i < 0 || inv.Args[i+1] != "0.5" || inv.Args[i-2] != "--model" || inv.Args[i+2] != "--permission-mode" {
		t.Errorf("args = %q", inv.Args)
	}
	inv, err = New().Headless(req("", 2))
	if err != nil {
		t.Fatal(err)
	}
	i = index(inv.Args, "--max-budget-usd")
	if i < 0 || inv.Args[i+1] != "2" || inv.Args[i-2] != "--mcp-config" || inv.Args[i+2] != "--permission-mode" {
		t.Errorf("args = %q", inv.Args)
	}
	inv, err = New().Headless(req("claude-opus-5-5", 0))
	if err != nil {
		t.Fatal(err)
	}
	if index(inv.Args, "--max-budget-usd") >= 0 {
		t.Errorf("args = %q", inv.Args)
	}
}
