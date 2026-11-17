//go:build !windows

package headless

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bengearig/ralph/internal/config"
	"github.com/bengearig/ralph/internal/harness"
	"github.com/bengearig/ralph/internal/harness/claudecode"
	"github.com/bengearig/ralph/internal/harness/opencode"
)

const stepLine = `{"type":"step_finish","sessionID":"s","part":{"type":"step-finish","reason":"tool-calls","cost":0.4,"tokens":{"input":1,"output":1,"reasoning":0,"cache":{"read":0,"write":0}}}}`

func budgetScript(t *testing.T) (harness.Invocation, string) {
	t.Helper()
	inv := script(t, "for i in 1 2 3; do echo '"+stepLine+"'; sleep 0.2; done\nsleep 30\ntouch marker")
	return inv, filepath.Join(inv.Dir, "marker")
}

func TestRunBudgetStopsOpenCodeStyleRun(t *testing.T) {
	inv, marker := budgetScript(t)
	res, err := Run(context.Background(), inv, opencode.New().NewParser(), Options{BudgetUSD: 0.5, Grace: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if !res.BudgetStopped || !res.Outcome.BudgetExceeded || res.Failure() != "budget exceeded" {
		t.Errorf("res = %+v, failure %q", res, res.Failure())
	}
	if res.Duration > 5*time.Second {
		t.Errorf("Duration = %v", res.Duration)
	}
	if got := res.Outcome.Usage.CostUSD; got < 0.79 || got > 0.81 {
		t.Errorf("cost = %v, want the run stopped after the second step", got)
	}
	time.Sleep(200 * time.Millisecond)
	if _, err := os.Stat(marker); err == nil {
		t.Error("the process kept running")
	}
}

func TestRunBudgetNotStoppedWhenNative(t *testing.T) {
	inv := script(t, "for i in 1 2 3; do echo '"+stepLine+"'; done\ntouch marker")
	res, err := Run(context.Background(), inv, opencode.New().NewParser(), Options{BudgetUSD: 0.5, NativeBudget: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.BudgetStopped || !res.Outcome.BudgetExceeded {
		t.Errorf("BudgetStopped = %v, BudgetExceeded = %v", res.BudgetStopped, res.Outcome.BudgetExceeded)
	}
	if _, err := os.Stat(filepath.Join(inv.Dir, "marker")); err != nil {
		t.Errorf("the run did not reach its end: %v", err)
	}
	if len(res.Notes) != 0 {
		t.Errorf("Notes = %q", res.Notes)
	}
}

func TestRunBudgetNoteWhenCostNotReported(t *testing.T) {
	res, _ := mustRun(t, script(t, "echo hi"), Options{BudgetUSD: 1})
	if len(res.Notes) != 1 || res.Notes[0] != "budget not checked: the harness reported no cost" {
		t.Errorf("Notes = %q", res.Notes)
	}
	if res.Outcome.BudgetExceeded || res.BudgetStopped {
		t.Errorf("res = %+v", res)
	}
}

func TestRunBudgetNoNoteWhenCostWithinCap(t *testing.T) {
	res, err := Run(context.Background(), script(t, "echo '"+stepLine+"'"), opencode.New().NewParser(), Options{BudgetUSD: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Notes) != 0 || res.Outcome.BudgetExceeded {
		t.Errorf("res = %+v", res)
	}
}

func TestRunBudgetZeroDoesNothing(t *testing.T) {
	inv := script(t, "for i in 1 2 3; do echo '"+stepLine+"'; done")
	res, err := Run(context.Background(), inv, opencode.New().NewParser(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.BudgetStopped || res.Outcome.BudgetExceeded || len(res.Notes) != 0 {
		t.Errorf("res = %+v", res)
	}
}

func TestExecuteBudgetUsesCapabilities(t *testing.T) {
	req := harness.HeadlessRequest{
		Dir: t.TempDir(), Prompt: "go", BudgetUSD: 0.5,
		MCP: harness.MCPServer{Name: "ralph", Command: "ralph", Args: []string{"mcp"}},
	}
	install := func(name, body string) {
		bin := t.TempDir()
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\ncat >/dev/null\n"+body+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	}

	install("opencode", "for i in 1 2 3; do echo '"+stepLine+"'; sleep 0.2; done\nsleep 30")
	res, err := Execute(context.Background(), opencode.New(), req, Options{Grace: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if !res.BudgetStopped || !res.Outcome.BudgetExceeded {
		t.Errorf("opencode: res = %+v", res)
	}

	argsFile := filepath.Join(t.TempDir(), "args")
	install("claude", `echo "$@" > '`+argsFile+`'
echo '{"type":"result","subtype":"success","is_error":false,"total_cost_usd":0.9,"result":"ok"}'`)
	res, err = Execute(context.Background(), claudecode.New(), req, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.BudgetStopped || !res.Outcome.BudgetExceeded || res.Failure() != "budget exceeded" {
		t.Errorf("claude: res = %+v", res)
	}
	args, err := os.ReadFile(argsFile)
	if err != nil || !strings.Contains(string(args), "--max-budget-usd 0.5 ") {
		t.Errorf("args = %q, %v", args, err)
	}
}

func budgetConfig(t *testing.T, projYAML string) *config.Config {
	t.Helper()
	return modeConfig(t, t.TempDir(), "", projYAML)
}

func TestNewRequestBudget(t *testing.T) {
	root := t.TempDir()
	req, err := NewRequest(modeConfig(t, root, "", ""), root, "x", modeMCP)
	if err != nil || req.BudgetUSD != 0 {
		t.Errorf("default: %v, %v", req.BudgetUSD, err)
	}
	root = t.TempDir()
	req, err = NewRequest(modeConfig(t, root, "", "headless:\n  budget_usd: 0.25\n"), root, "x", modeMCP)
	if err != nil || req.BudgetUSD != 0.25 {
		t.Errorf("set: %v, %v", req.BudgetUSD, err)
	}
}

func TestNewOptionsFromConfig(t *testing.T) {
	opt, err := NewOptions(budgetConfig(t, ""))
	if err != nil || opt.Timeout != 30*time.Minute || opt.Stdout != nil {
		t.Errorf("default: %+v, %v", opt, err)
	}
	opt, err = NewOptions(budgetConfig(t, "headless:\n  timeout: 90s\n"))
	if err != nil || opt.Timeout != 90*time.Second {
		t.Errorf("set: %+v, %v", opt, err)
	}
}

func TestNewOptionsInvalidValueNamesSetting(t *testing.T) {
	if _, err := NewOptions(&config.Config{}); err == nil || !strings.Contains(err.Error(), "headless.timeout") {
		t.Errorf("error = %v", err)
	}
}
