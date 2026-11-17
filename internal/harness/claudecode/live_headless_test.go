package claudecode_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bengearig/ralph/internal/config"
	"github.com/bengearig/ralph/internal/harness"
	"github.com/bengearig/ralph/internal/harness/claudecode"
	"github.com/bengearig/ralph/internal/headless"
)

func liveEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// logHead shows the first lines of the raw output when the test fails, so a
// change in the harness's output is easy to see.
func logHead(t *testing.T, raw *bytes.Buffer) {
	t.Helper()
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		lines := strings.Split(raw.String(), "\n")
		if len(lines) > 40 {
			lines = lines[:40]
		}
		for _, l := range lines {
			if len(l) > 600 {
				l = l[:600] + "..."
			}
			t.Log(l)
		}
	})
}

// statusCallCompleted reports whether the stream shows a call of the ralph
// status tool whose result is not an error.
func statusCallCompleted(raw string) bool {
	type block struct {
		Type      string `json:"type"`
		ID        string `json:"id"`
		Name      string `json:"name"`
		ToolUseID string `json:"tool_use_id"`
		IsError   bool   `json:"is_error"`
	}
	var event struct {
		Type    string `json:"type"`
		Message struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}
	calls := map[string]bool{}
	for _, line := range strings.Split(raw, "\n") {
		event.Message.Content = nil
		if json.Unmarshal([]byte(line), &event) != nil {
			continue
		}
		var blocks []block
		if json.Unmarshal(event.Message.Content, &blocks) != nil {
			continue
		}
		for _, b := range blocks {
			switch {
			case event.Type == "assistant" && b.Type == "tool_use" && b.Name == "mcp__ralph__status":
				calls[b.ID] = true
			case event.Type == "user" && b.Type == "tool_result" && calls[b.ToolUseID] && !b.IsError:
				return true
			}
		}
	}
	return false
}

const liveHeadlessPrompt = `You are checking what a restricted agent may do. Do these steps in order, one tool call each, and do not stop when one is refused. Do not try other ways around a refusal. After the last step, report the result of every step in one line each.

1. Create the file allowed.txt in the current directory containing "ok", with an editing tool.
2. Run this command with the shell: echo shell > via-shell.txt
3. Run this command with the shell: sh -c 'echo sh > via-sh.txt'
4. Try to create the file .ralph/escape.txt containing "x".
5. Try to write the file .git/hooks/pre-commit containing "#!/bin/sh".
6. Try to write the file OUTSIDE_PATH containing "x".
7. Call the ralph status tool.`

// TestLiveClaudeHeadless runs the real Claude Code with a model. It spends
// money, up to the budget.
func TestLiveClaudeHeadless(t *testing.T) {
	if os.Getenv("RALPH_CLAUDE_LIVE") != "1" {
		t.Skip("set RALPH_CLAUDE_LIVE=1 to run headless against the real Claude Code, which spends money")
	}
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skipf("claude is not on PATH: %v", err)
	}
	budget, err := strconv.ParseFloat(liveEnv("RALPH_CLAUDE_LIVE_BUDGET_USD", "0.50"), 64)
	if err != nil {
		t.Fatalf("RALPH_CLAUDE_LIVE_BUDGET_USD: %v", err)
	}
	model := liveEnv("RALPH_CLAUDE_LIVE_MODEL", "haiku")

	ralph := filepath.Join(t.TempDir(), "ralph")
	build := exec.Command("go", "build", "-o", ralph, "github.com/bengearig/ralph/cmd/ralph")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}

	repo, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range [][]string{{"git", "init", "-q", repo}, {ralph, "init", "--harness", "claude-code"}} {
		cmd := exec.Command(c[0], c[1:]...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s: %v\n%s", strings.Join(c, " "), err, out)
		}
	}
	outside := filepath.Join(t.TempDir(), "outside.txt")

	cfg, err := config.Load(config.Options{
		Registry:    config.DefaultRegistry(),
		UserFile:    filepath.Join(t.TempDir(), "user.yaml"),
		ProjectRoot: repo,
		Getenv:      func(string) (string, bool) { return "", false },
	})
	if err != nil {
		t.Fatal(err)
	}
	mcp := harness.MCPServer{Name: "ralph", Command: ralph, Args: []string{"mcp"}}
	prompt := strings.ReplaceAll(liveHeadlessPrompt, "OUTSIDE_PATH", outside)
	req, err := headless.NewRequest(cfg, repo, prompt, mcp)
	if err != nil {
		t.Fatal(err)
	}
	req.Model, req.BudgetUSD = model, budget
	opt, err := headless.NewOptions(cfg)
	if err != nil {
		t.Fatal(err)
	}
	opt.Timeout = 8 * time.Minute
	var raw bytes.Buffer
	opt.Stdout = &raw
	logHead(t, &raw)

	res, err := headless.Execute(context.Background(), claudecode.New(), req, opt)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("exit %d, cost %v, denials %d, failure %q", res.ExitCode, res.Outcome.Usage.CostUSD, len(res.Outcome.Denials), res.Failure())

	for _, p := range []string{
		filepath.Join(repo, "via-shell.txt"),
		filepath.Join(repo, "via-sh.txt"),
		filepath.Join(repo, ".ralph", "escape.txt"),
		filepath.Join(repo, ".git", "hooks", "pre-commit"),
		outside,
	} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("%s exists: the restricted run wrote it", p)
		}
	}
	if !res.Outcome.Finished {
		t.Errorf("the run did not finish: %+v", res.Outcome)
	}
	if len(res.Outcome.Denials) == 0 {
		t.Error("no denial was reported")
	}
	if res.Failure() == "" {
		t.Error("Failure is empty")
	}
	if !statusCallCompleted(raw.String()) {
		t.Error("the ralph status tool call did not complete without an error")
	}

	tiny := req
	tiny.Prompt = "Run git status with the shell and describe the result."
	tiny.BudgetUSD = 0.0005
	var tinyRaw bytes.Buffer
	opt.Stdout = &tinyRaw
	logHead(t, &tinyRaw)
	res, err = headless.Execute(context.Background(), claudecode.New(), tiny, opt)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Outcome.BudgetExceeded {
		t.Errorf("BudgetExceeded is false: failure %q, outcome %+v", res.Failure(), res.Outcome)
	}
}
